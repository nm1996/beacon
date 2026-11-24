package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// summaryName is the summary written in the reports directory and in each
// day's folder. GitHub shows a folder's README.md below its file list.
const summaryName = "README.md"

// dayReports is what one day's folder holds.
type dayReports struct {
	day     string
	reports []fileReport
	stats   []string
}

// fileReport is a report with the name of the file it came from.
type fileReport struct {
	file   string
	report *Report
}

// writeSummaries reads every report under directory and writes a summary
// for each day's folder, and an index of the days in directory itself.
func writeSummaries(directory string) error {
	days, err := readDays(directory)
	if err != nil {
		return err
	}

	for _, day := range days {
		if err := writeFile(filepath.Join(directory, day.day, summaryName), daySummary(day)); err != nil {
			return err
		}
	}

	return writeFile(filepath.Join(directory, summaryName), indexSummary(days))
}

// readDays loads the reports in every folder of directory named as a
// date, newest day first.
func readDays(directory string) ([]dayReports, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}

	var days []dayReports

	for _, entry := range entries {
		if _, err := time.Parse(time.DateOnly, entry.Name()); err != nil || !entry.IsDir() {
			continue
		}

		day, err := readDay(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}

		day.day = entry.Name()
		days = append(days, day)
	}

	slices.SortFunc(days, func(left, right dayReports) int { return strings.Compare(right.day, left.day) })

	return days, nil
}

// readDay loads the reports and docker stats files in folder, oldest
// first.
func readDay(folder string) (dayReports, error) {
	var day dayReports

	files, err := os.ReadDir(folder)
	if err != nil {
		return day, err
	}

	for _, file := range files {
		switch {
		case strings.HasSuffix(file.Name(), "-stats.jsonl"):
			day.stats = append(day.stats, file.Name())
		case strings.HasSuffix(file.Name(), ".json"):
			encoded, err := os.ReadFile(filepath.Join(folder, file.Name())) //nolint:gosec // A file the runner wrote.
			if err != nil {
				return day, err
			}

			report := &Report{}
			if err := json.Unmarshal(encoded, report); err != nil {
				return day, fmt.Errorf("%s: %w", file.Name(), err)
			}

			day.reports = append(day.reports, fileReport{file: file.Name(), report: report})
		}
	}

	slices.SortFunc(day.reports, func(left, right fileReport) int {
		return left.report.Started.Compare(right.report.Started)
	})

	for index, stats := range day.stats {
		day.stats[index] = filepath.Join(folder, stats)
	}

	return day, nil
}

// runOf groups a report: its run.sh invocation, or none.
func runOf(report *Report) string {
	return cmp.Or(report.Run, "by hand")
}

// runs lists the runs of day in the order they started, each with its
// reports.
func runs(day dayReports) ([]string, map[string][]fileReport) {
	var order []string

	grouped := map[string][]fileReport{}

	for _, file := range day.reports {
		run := runOf(file.report)
		if _, ok := grouped[run]; !ok {
			order = append(order, run)
		}

		grouped[run] = append(grouped[run], file)
	}

	return order, grouped
}

// counts returns how many of reports passed and failed.
func counts(reports []fileReport) (passed, failed int) {
	for _, file := range reports {
		if file.report.Passed {
			passed++
		} else {
			failed++
		}
	}

	return passed, failed
}

// daySummary is the README.md of one day's folder: per run, a table of
// every check, then the failed steps, then the resources the load
// profiles used.
func daySummary(day dayReports) string {
	var out strings.Builder

	passed, failed := counts(day.reports)
	fmt.Fprintf(&out, "# Simulation runs on %s\n\n", day.day)
	fmt.Fprintf(&out, "%d checks: %d passed, %d failed. Written by `runner -summarize` from the JSON reports in this folder; see [the simulation](../../README.md) for what each scenario does.\n", len(day.reports), passed, failed)

	order, grouped := runs(day)
	for _, run := range slices.Backward(order) {
		reports := grouped[run]
		passed, failed := counts(reports)

		fmt.Fprintf(&out, "\n## Run %s: %s\n\n", run, verdict(passed, failed))
		out.WriteString("| Started (UTC) | run.sh scenario | Check | Result | Measured | Took |\n|---|---|---|---|---|---|\n")

		for _, file := range reports {
			report := file.report
			fmt.Fprintf(&out, "| %s | %s | [%s](%s) | %s | %s | %s |\n",
				report.Started.Format(time.TimeOnly), cmp.Or(report.Label, "–"), report.Scenario, file.file,
				result(report.Passed), measured(report), report.Finished.Sub(report.Started).Round(100*time.Millisecond))
		}

		writeFailures(&out, reports)
	}

	writeStats(&out, day.stats)

	return out.String()
}

// verdict sums up a run.
func verdict(passed, failed int) string {
	if failed == 0 {
		return fmt.Sprintf("all %d checks passed", passed)
	}

	return fmt.Sprintf("%d of %d checks failed", failed, passed+failed)
}

// result marks a report as passed or failed.
func result(passed bool) string {
	if passed {
		return "✅ pass"
	}

	return "❌ fail"
}

// measured lists a report's timings, in seconds. The time to settle is
// left out when anything else was measured.
func measured(report *Report) string {
	names := sortedKeys(report.Timings)
	if len(names) > 1 {
		names = slices.DeleteFunc(names, func(name string) bool { return name == "settle" })
	}

	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s %.2fs", strings.ReplaceAll(name, "_", " "), report.Timings[name]/1000))
	}

	return cmp.Or(strings.Join(parts, ", "), "–")
}

// writeFailures lists every failed step of reports.
func writeFailures(out *strings.Builder, reports []fileReport) {
	var lines []string

	for _, file := range reports {
		for _, step := range file.report.Steps {
			if !step.Passed {
				lines = append(lines, fmt.Sprintf("- **%s**, %s (`%s`): %s\n",
					cmp.Or(file.report.Label, file.report.Scenario), step.Name, file.file, cmp.Or(step.Detail, "failed")))
			}
		}
	}

	if len(lines) == 0 {
		return
	}

	out.WriteString("\nFailed steps:\n\n" + strings.Join(lines, ""))
}

// containerStats is the peak use of one container in a docker stats file.
type containerStats struct {
	cpu    float64
	memory string
	bytes  float64
}

// writeStats summarizes each docker stats file the load profiles wrote:
// the highest CPU and memory use of every container.
func writeStats(out *strings.Builder, files []string) {
	for _, file := range files {
		peaks, err := readStats(file)
		if err != nil || len(peaks) == 0 {
			continue
		}

		fmt.Fprintf(out, "\n## Resources: [%s](%s)\n\n| Container | Peak CPU | Peak memory |\n|---|---|---|\n",
			filepath.Base(file), filepath.Base(file))

		for _, name := range sortedKeys(peaks) {
			fmt.Fprintf(out, "| %s | %.1f%% | %s |\n", name, peaks[name].cpu, peaks[name].memory)
		}
	}
}

// readStats returns the peak use of each container in a file of docker
// stats lines, as docker stats --format '{{json .}}' writes them.
func readStats(file string) (map[string]containerStats, error) {
	opened, err := os.Open(file) //nolint:gosec // A file run.sh wrote.
	if err != nil {
		return nil, err
	}
	defer opened.Close()

	peaks := map[string]containerStats{}
	scanner := bufio.NewScanner(opened)

	for scanner.Scan() {
		var sample struct {
			Name     string `json:"Name"`
			CPUPerc  string `json:"CPUPerc"`
			MemUsage string `json:"MemUsage"`
		}

		if json.Unmarshal(scanner.Bytes(), &sample) != nil {
			continue
		}

		cpu, _ := strconv.ParseFloat(strings.TrimSuffix(sample.CPUPerc, "%"), 64)
		memory, _, _ := strings.Cut(sample.MemUsage, " / ")
		peak := peaks[sample.Name]

		peak.cpu = max(peak.cpu, cpu)
		if size := byteSize(memory); size >= peak.bytes {
			peak.bytes, peak.memory = size, memory
		}

		peaks[sample.Name] = peak
	}

	return peaks, scanner.Err()
}

// byteSize parses a size as docker stats writes it, such as 25.3MiB.
func byteSize(value string) float64 {
	units := []struct {
		suffix string
		bytes  float64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"B", 1}}

	for _, unit := range units {
		if number, ok := strings.CutSuffix(value, unit.suffix); ok {
			parsed, err := strconv.ParseFloat(number, 64)
			if err != nil {
				return 0
			}

			return parsed * unit.bytes
		}
	}

	return 0
}

// indexSummary is the README.md of the reports directory: one line per
// day, newest first.
func indexSummary(days []dayReports) string {
	var out strings.Builder

	out.WriteString("# Simulation reports\n\nOne folder per day, each with its JSON reports and a summary. Written by `runner -summarize`; `run.sh` updates it after every run.\n\n")
	out.WriteString("| Day | Runs | Checks | Passed | Failed |\n|---|---|---|---|---|\n")

	for _, day := range days {
		order, _ := runs(day)
		passed, failed := counts(day.reports)
		fmt.Fprintf(&out, "| [%s](%s/%s) | %d | %d | %d | %d |\n", day.day, day.day, summaryName, len(order), len(day.reports), passed, failed)
	}

	return out.String()
}

// writeFile writes content to path, readable by everyone, as a committed
// file is.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644) //nolint:gosec // As above.
}
