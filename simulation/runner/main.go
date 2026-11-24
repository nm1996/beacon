// Command runner drives one simulation scenario against the Beacon nodes
// and application instances in the Compose project, writes a JSON report,
// prints a summary, and exits non-zero if any check failed.
//
//	docker compose -f simulation/compose.yaml run --rm runner -scenario normal
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// endpoint is one name=URL pair from -nodes.
type endpoint struct {
	name string
	url  string
}

// options is what the command line configures for every scenario.
type options struct {
	scenario string
	nodes    []endpoint
	expected map[string]int
	reports  string
	timeout  time.Duration
	rounds   int
	token    string

	// For the scenarios about one instance: which one, when it was
	// killed, stopped or started, and how long the change may take.
	service string
	address string
	since   time.Time
	within  time.Duration
	// oldID is the instance's ID before it was killed; back checks that it
	// never returns. wantID is the ID it must come back with, if fixed.
	oldID  string
	wantID string
}

// scenario runs its steps against cluster, recording each in the report.
type scenario func(ctx context.Context, cluster *cluster, options options) error

// scenarios maps each -scenario name to its implementation.
var scenarios = map[string]scenario{
	"normal": runNormal,
	"gone":   runGone,
	"back":   runBack,
}

// aboutOneInstance lists the scenarios that need -service and -address.
var aboutOneInstance = []string{"gone", "back"}

// main runs the scenario and exits 1 if it failed, 2 if it could not run.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)

	stop()
	os.Exit(code)
}

// run parses args, runs the scenario and writes its report, returning the
// exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	parsed, err := parseOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	endpoints := make(map[string]string, len(parsed.nodes))
	for _, target := range parsed.nodes {
		endpoints[target.name] = target.url
	}

	report := newReport(parsed.scenario, endpoints)
	report.Expected = parsed.expected

	cluster, err := newCluster(parsed.nodes, parsed.token, report)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	cluster.snapshotMetrics(ctx, false)

	runErr := scenarios[parsed.scenario](ctx, cluster, parsed)
	if runErr != nil {
		report.check("scenario completed", time.Now(), runErr)
	}

	cluster.snapshotMetrics(context.WithoutCancel(ctx), true)
	report.finish()
	report.print(stdout)

	path, err := report.write(parsed.reports)
	if err != nil {
		fmt.Fprintln(stderr, "write report:", err)
		return 2
	}

	fmt.Fprintln(stdout, "  report:", path)

	if !report.Passed {
		return 1
	}

	return 0
}

// parseOptions reads the flags in args.
func parseOptions(args []string) (options, error) {
	flags := flag.NewFlagSet("runner", flag.ContinueOnError)

	var (
		parsed   options
		nodes    string
		expected string
		since    string
	)

	flags.StringVar(&parsed.scenario, "scenario", "normal", "scenario to run: "+strings.Join(scenarioNames(), ", "))
	flags.StringVar(&nodes, "nodes", "beacon-a=http://beacon-a:8080,beacon-b=http://beacon-b:8080", "Beacon nodes to observe, as name=URL pairs separated by commas")
	flags.StringVar(&expected, "expect", "orders=2,payments=1,inventory=1", "instances each service should have, as name=count pairs separated by commas")
	flags.StringVar(&parsed.reports, "reports", "/reports", "directory to write the JSON report to")
	flags.DurationVar(&parsed.timeout, "timeout", time.Minute, "how long to wait for the cluster to reach the expected state")
	flags.IntVar(&parsed.rounds, "rounds", 20, "how many times to repeat each lookup and call, for latency")
	flags.StringVar(&parsed.token, "token", os.Getenv("RUNNER_TOKEN"), "bearer token for Beacon; defaults to $RUNNER_TOKEN")
	flags.StringVar(&parsed.service, "service", "", "gone, back: the service of the instance to watch")
	flags.StringVar(&parsed.address, "address", "", "gone, back: the address of the instance to watch, such as orders-2, or host:port")
	flags.StringVar(&since, "since", "", "gone, back: when the instance was killed, stopped or started, as Unix seconds or RFC 3339; empty is now")
	flags.DurationVar(&parsed.within, "within", 9*time.Second, "gone, back: how long after -since the change may take")
	flags.StringVar(&parsed.oldID, "old-id", "", "back: the ID the instance had before; it must not return")
	flags.StringVar(&parsed.wantID, "want-id", "", "back: the ID the instance must come back with")

	if err := flags.Parse(args); err != nil {
		return options{}, err
	}

	if _, ok := scenarios[parsed.scenario]; !ok {
		return options{}, fmt.Errorf("unknown -scenario %q; want one of %s", parsed.scenario, strings.Join(scenarioNames(), ", "))
	}

	var err error

	if parsed.nodes, err = parseEndpoints(nodes); err != nil {
		return options{}, err
	}

	if parsed.expected, err = parseExpected(expected); err != nil {
		return options{}, err
	}

	if parsed.rounds < 1 || parsed.timeout <= 0 || parsed.within <= 0 {
		return options{}, errors.New("-rounds, -timeout and -within must be positive")
	}

	if parsed.since, err = parseSince(since); err != nil {
		return options{}, err
	}

	if slices.Contains(aboutOneInstance, parsed.scenario) {
		if parsed.service == "" || parsed.address == "" {
			return options{}, fmt.Errorf("-scenario %s needs -service and -address", parsed.scenario)
		}

		if _, ok := parsed.expected[parsed.service]; !ok {
			return options{}, fmt.Errorf("-service %s is not in -expect", parsed.service)
		}
	}

	return parsed, nil
}

// parseSince parses -since: Unix seconds with an optional fraction, as
// date +%s.%N prints them, or RFC 3339. Empty means now.
func parseSince(value string) (time.Time, error) {
	if value == "" {
		return time.Now(), nil
	}

	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		whole := int64(seconds)
		return time.Unix(whole, int64((seconds-float64(whole))*float64(time.Second))), nil
	}

	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("-since %q is neither Unix seconds nor RFC 3339", value)
	}

	return parsed, nil
}

// scenarioNames lists the scenarios, sorted.
func scenarioNames() []string {
	names := make([]string, 0, len(scenarios))
	for name := range scenarios {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

// parseEndpoints parses name=URL pairs separated by commas.
func parseEndpoints(value string) ([]endpoint, error) {
	var endpoints []endpoint

	for pair := range strings.SplitSeq(value, ",") {
		name, address, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || name == "" || address == "" {
			return nil, fmt.Errorf("-nodes: %q is not name=URL", pair)
		}

		if slices.ContainsFunc(endpoints, func(existing endpoint) bool { return existing.name == name }) {
			return nil, fmt.Errorf("-nodes: %q appears twice", name)
		}

		endpoints = append(endpoints, endpoint{name: name, url: address})
	}

	return endpoints, nil
}

// parseExpected parses name=count pairs separated by commas.
func parseExpected(value string) (map[string]int, error) {
	expected := map[string]int{}

	for pair := range strings.SplitSeq(value, ",") {
		name, count, ok := strings.Cut(strings.TrimSpace(pair), "=")
		number, err := strconv.Atoi(count)

		if !ok || name == "" || err != nil || number < 0 {
			return nil, fmt.Errorf("-expect: %q is not name=count", pair)
		}

		expected[name] = number
	}

	return expected, nil
}
