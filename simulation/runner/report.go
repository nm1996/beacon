package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Report is what one run found. It is written as JSON, and holds no
// tokens.
type Report struct {
	Scenario string `json:"scenario"`
	// Run names the run.sh invocation the report belongs to, and Label
	// the run.sh scenario within it, such as instance-killed. Both are
	// empty when the runner is started by hand.
	Run   string `json:"run,omitempty"`
	Label string `json:"label,omitempty"`

	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Passed   bool      `json:"passed"`

	// Endpoints maps each node's name to the base URL used for it.
	Endpoints map[string]string `json:"endpoints"`
	// Expected is how many instances of each service should be registered.
	Expected map[string]int `json:"expected,omitempty"`
	// Target is the instance a scenario about one instance watched.
	Target *Target `json:"target,omitempty"`
	// Seen is what each node returned for each service at the last look.
	Seen map[string]map[string][]Seen `json:"seen,omitempty"`

	// Calls counts the calls made through a Resolver, by instance address.
	Calls map[string]CallCount `json:"calls_by_address,omitempty"`

	Steps    []Step                     `json:"steps"`
	Requests map[string]RequestSummary  `json:"requests"`
	Timings  map[string]float64         `json:"timings_ms,omitempty"`
	Metrics  map[string]MetricSnapshots `json:"metrics,omitempty"`

	mu       sync.Mutex
	requests map[string]*tally
}

// Target names the instance a scenario watched, with the ID it last had.
type Target struct {
	Service string `json:"service"`
	Address string `json:"address"`
	ID      string `json:"id,omitempty"`
}

// Seen is one instance as a node returned it.
type Seen struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// Step is one check and its outcome.
type Step struct {
	Name     string    `json:"name"`
	Passed   bool      `json:"passed"`
	Detail   string    `json:"detail,omitempty"`
	At       time.Time `json:"at"`
	Duration float64   `json:"duration_ms"`
}

// CallCount is how many calls went to one address, and how many failed.
type CallCount struct {
	Calls    int `json:"calls"`
	Failures int `json:"failures"`
}

// RequestSummary counts one kind of request and its latency.
type RequestSummary struct {
	Total  int            `json:"total"`
	Errors int            `json:"errors"`
	ByKind map[string]int `json:"errors_by_kind,omitempty"`
	P50    float64        `json:"p50_ms"`
	P95    float64        `json:"p95_ms"`
	P99    float64        `json:"p99_ms"`
	Max    float64        `json:"max_ms"`
}

// MetricSnapshots holds a node's Beacon metrics before and after the run.
type MetricSnapshots struct {
	Before []string `json:"before,omitempty"`
	After  []string `json:"after,omitempty"`
}

// tally collects the outcomes of one kind of request.
type tally struct {
	latencies []time.Duration
	errors    map[string]int
}

// newReport starts a report for scenario.
func newReport(scenario string, endpoints map[string]string) *Report {
	return &Report{
		Scenario:  scenario,
		Started:   time.Now().UTC(),
		Endpoints: endpoints,
		Timings:   map[string]float64{},
		Metrics:   map[string]MetricSnapshots{},
		requests:  map[string]*tally{},
	}
}

// check records a step that passed when err is nil, and returns whether it
// did.
func (report *Report) check(name string, started time.Time, err error) bool {
	step := Step{
		Name:     name,
		Passed:   err == nil,
		At:       started.UTC(),
		Duration: milliseconds(time.Since(started)),
	}
	if err != nil {
		step.Detail = err.Error()
	}

	report.mu.Lock()
	report.Steps = append(report.Steps, step)
	report.mu.Unlock()

	return err == nil
}

// timing records a measured duration under name.
func (report *Report) timing(name string, duration time.Duration) {
	report.mu.Lock()
	report.Timings[name] = milliseconds(duration)
	report.mu.Unlock()
}

// observe records one request of kind. A non-empty failure names the kind
// of error, such as "status 500" or "connect".
func (report *Report) observe(kind string, latency time.Duration, failure string) {
	report.mu.Lock()
	defer report.mu.Unlock()

	counts, ok := report.requests[kind]
	if !ok {
		counts = &tally{errors: map[string]int{}}
		report.requests[kind] = counts
	}

	counts.latencies = append(counts.latencies, latency)
	if failure != "" {
		counts.errors[failure]++
	}
}

// finish summarizes the requests and decides the result: the run passed
// only if every step did.
func (report *Report) finish() {
	report.mu.Lock()
	defer report.mu.Unlock()

	report.Finished = time.Now().UTC()
	report.Passed = len(report.Steps) > 0
	report.Requests = map[string]RequestSummary{}

	for _, step := range report.Steps {
		report.Passed = report.Passed && step.Passed
	}

	for kind, counts := range report.requests {
		report.Requests[kind] = summarize(counts)
	}
}

// summarize turns a tally into counts and percentiles.
func summarize(counts *tally) RequestSummary {
	sorted := slices.Clone(counts.latencies)
	slices.Sort(sorted)

	summary := RequestSummary{Total: len(sorted)}
	for _, count := range counts.errors {
		summary.Errors += count
	}

	if summary.Errors > 0 {
		summary.ByKind = counts.errors
	}

	if len(sorted) > 0 {
		summary.P50 = milliseconds(percentile(sorted, 0.50))
		summary.P95 = milliseconds(percentile(sorted, 0.95))
		summary.P99 = milliseconds(percentile(sorted, 0.99))
		summary.Max = milliseconds(sorted[len(sorted)-1])
	}

	return summary
}

// percentile returns the nearest-rank percentile of sorted, which must not
// be empty.
func percentile(sorted []time.Duration, fraction float64) time.Duration {
	rank := int(math.Ceil(fraction*float64(len(sorted)))) - 1
	return sorted[max(rank, 0)]
}

// milliseconds converts duration for the report, to a microsecond.
func milliseconds(duration time.Duration) float64 {
	return math.Round(float64(duration.Microseconds())) / 1000
}

// write saves the report in a folder of directory named after the day it
// started, as HHMMSSZ-<scenario>.json, and returns the file's path.
func (report *Report) write(directory string) (string, error) {
	day := filepath.Join(directory, report.Started.Format(time.DateOnly))
	if err := os.MkdirAll(day, 0o755); err != nil { //nolint:gosec // Reports are meant to be read, and committed.
		return "", err
	}

	path := filepath.Join(day, report.Started.Format("150405Z")+"-"+report.Scenario+".json")

	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}

	return path, os.WriteFile(path, append(encoded, '\n'), 0o644) //nolint:gosec // As above.
}

// print writes a short human summary: each step, then the request counts.
func (report *Report) print(out io.Writer) {
	result := "PASS"
	if !report.Passed {
		result = "FAIL"
	}

	fmt.Fprintf(out, "%s %s in %s\n", result, report.Scenario, report.Finished.Sub(report.Started).Round(time.Millisecond))

	for _, step := range report.Steps {
		mark := "ok  "
		if !step.Passed {
			mark = "FAIL"
		}

		line := fmt.Sprintf("  %s %s", mark, step.Name)
		if step.Detail != "" {
			line += ": " + step.Detail
		}

		fmt.Fprintln(out, line)
	}

	kinds := make([]string, 0, len(report.Requests))
	for kind := range report.Requests {
		kinds = append(kinds, kind)
	}

	sort.Strings(kinds)

	for _, kind := range kinds {
		summary := report.Requests[kind]
		fmt.Fprintf(out, "  %-18s %6d requests, %d errors, p50 %.1fms p95 %.1fms p99 %.1fms\n",
			kind, summary.Total, summary.Errors, summary.P50, summary.P95, summary.P99)
	}

	timings := make([]string, 0, len(report.Timings))
	for name, value := range report.Timings {
		timings = append(timings, fmt.Sprintf("%s=%.0fms", name, value))
	}

	sort.Strings(timings)

	if len(timings) > 0 {
		fmt.Fprintln(out, "  timings: "+strings.Join(timings, " "))
	}

	// run.sh reads this line to pass the ID on to the next scenario.
	if report.Target != nil && report.Target.ID != "" {
		fmt.Fprintln(out, "  target id: "+report.Target.ID)
	}
}
