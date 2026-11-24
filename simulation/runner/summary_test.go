package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeReport saves a report as the runner does, for the summaries.
func writeReport(t *testing.T, directory string, report *Report) {
	t.Helper()

	report.finish()

	if _, err := report.write(directory); err != nil {
		t.Fatal(err)
	}
}

func TestSummariesGroupRunsByDay(t *testing.T) {
	directory := t.TempDir()
	started := time.Date(2026, 11, 24, 21, 19, 9, 0, time.UTC)

	passed := newReport("gone", nil)
	passed.Started, passed.Run, passed.Label = started, "211900Z", "instance-killed"
	passed.check("orders-2 leaves discovery on every node within 9s", started, nil)
	passed.timing("gone_beacon-a", 6414*time.Millisecond)
	passed.timing("settle", 9*time.Millisecond)
	writeReport(t, directory, passed)

	failed := newReport("storage-down", nil)
	failed.Started, failed.Run, failed.Label = started.Add(time.Minute), "211900Z", "storage-down"
	failed.check("every instance is back within 4s", started, errorString("back 8.661s after the nodes served again"))
	writeReport(t, directory, failed)

	earlier := newReport("normal", nil)
	earlier.Started = started.Add(-24 * time.Hour)
	earlier.check("every node is healthy", earlier.Started, nil)
	writeReport(t, directory, earlier)

	stats := filepath.Join(directory, "2026-11-24", "212000Z-load-100-stats.jsonl")
	lines := `{"Name":"beacon-sim-beacon-a-1","CPUPerc":"3.50%","MemUsage":"25.1MiB / 15.4GiB"}
{"Name":"beacon-sim-beacon-a-1","CPUPerc":"7.25%","MemUsage":"31.9MiB / 15.4GiB"}
{"Name":"beacon-sim-beacon-a-1","CPUPerc":"1.00%","MemUsage":"28MiB / 15.4GiB"}
`
	if err := os.WriteFile(stats, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writeSummaries(directory); err != nil {
		t.Fatal(err)
	}

	day := read(t, filepath.Join(directory, "2026-11-24", "README.md"))
	for _, want := range []string{
		"2 checks: 1 passed, 1 failed",
		"## Run 211900Z: 1 of 2 checks failed",
		"| 21:19:09 | instance-killed | [gone](211909Z-gone.json) | ✅ pass | gone beacon-a 6.41s |",
		"| storage-down | [storage-down](212009Z-storage-down.json) | ❌ fail |",
		"- **storage-down**, every instance is back within 4s (`212009Z-storage-down.json`): back 8.661s after the nodes served again",
		"| beacon-sim-beacon-a-1 | 7.2% | 31.9MiB |",
	} {
		if !strings.Contains(day, want) {
			t.Errorf("day summary lacks %q:\n%s", want, day)
		}
	}

	index := read(t, filepath.Join(directory, "README.md"))
	if !strings.Contains(index, "| [2026-11-24](2026-11-24/README.md) | 1 | 2 | 1 | 1 |\n| [2026-11-23](2026-11-23/README.md) | 1 | 1 | 1 | 0 |") {
		t.Errorf("index, newest day first:\n%s", index)
	}

	if !strings.Contains(read(t, filepath.Join(directory, "2026-11-23", "README.md")), "## Run by hand: all 1 checks passed") {
		t.Error("a report without run.sh is not listed as run by hand")
	}
}

// errorString is an error with a fixed message.
type errorString string

// Error returns the message.
func (message errorString) Error() string { return string(message) }

// read returns the content of path.
func read(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(content)
}
