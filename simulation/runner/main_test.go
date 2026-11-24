package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nm1996/beacon/client"
	"github.com/nm1996/beacon/internal/bootstrap"
	"github.com/nm1996/beacon/internal/config"
)

// startBeacon serves a real Beacon API in memory, as the node beacon-a,
// until the test ends.
func startBeacon(t *testing.T) string {
	t.Helper()

	settings := config.Default()
	settings.RateLimit.RegistrationsPerSecond = 0
	settings.Server.NodeID = "beacon-a"

	components, err := bootstrap.New(settings, slog.New(slog.DiscardHandler), "test")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(components.Close)

	if err := components.Membership.Join(t.Context()); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(components.Server.Handler)
	t.Cleanup(server.Close)

	return server.URL
}

// startInstance registers an instance of service with Beacon, served by
// a /work handler that answers with answerAs, or with its own ID when
// answerAs is empty.
func startInstance(t *testing.T, beaconURL, service, answerAs string) {
	t.Helper()

	var id string

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		answered := id
		if answerAs != "" {
			answered = answerAs
		}

		_ = json.NewEncoder(writer).Encode(work{Service: service, InstanceID: answered, Time: time.Now()})
	}))
	t.Cleanup(server.Close)

	host, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	portNumber, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}

	beacon, err := client.New(beaconURL)
	if err != nil {
		t.Fatal(err)
	}

	registered, err := beacon.Register(t.Context(), client.Registration{Name: service, Address: host, Port: portNumber})
	if err != nil {
		t.Fatal(err)
	}

	id = registered.ID
}

// runScenario runs the runner with args against beaconURL and returns its
// exit code, its output, and the report it wrote.
func runScenario(t *testing.T, beaconURL string, args ...string) (int, string, *Report) {
	t.Helper()

	reports := t.TempDir()
	args = append([]string{"-nodes", "beacon-a=" + beaconURL, "-reports", reports, "-timeout", "2s", "-rounds", "2"}, args...)

	var stdout, stderr bytes.Buffer

	code := run(t.Context(), args, &stdout, &stderr)

	files, err := filepath.Glob(filepath.Join(reports, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("want one report, got %v (%v); stderr: %s", files, err, stderr.String())
	}

	encoded, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}

	report := &Report{}
	if err := json.Unmarshal(encoded, report); err != nil {
		t.Fatal(err)
	}

	return code, stdout.String(), report
}

func TestNormalPassesWhenEveryInstanceAnswers(t *testing.T) {
	beaconURL := startBeacon(t)
	startInstance(t, beaconURL, "orders", "")
	startInstance(t, beaconURL, "orders", "")
	startInstance(t, beaconURL, "payments", "")

	code, output, report := runScenario(t, beaconURL, "-expect", "orders=2,payments=1")

	if code != 0 || !report.Passed {
		t.Fatalf("exit %d, want 0:\n%s", code, output)
	}

	if got := report.Requests["work"].Total; got != 6 {
		t.Errorf("work requests = %d, want 3 instances × 2 rounds", got)
	}

	if len(report.Seen["beacon-a"]["orders"]) != 2 {
		t.Errorf("report saw %+v", report.Seen)
	}
}

func TestNormalFailsWhenAnInstanceAnswersAsAnother(t *testing.T) {
	beaconURL := startBeacon(t)
	startInstance(t, beaconURL, "orders", "someone-else")

	code, output, report := runScenario(t, beaconURL, "-expect", "orders=1")

	if code != 1 || report.Passed || !strings.Contains(output, "FAIL every instance answers as itself") {
		t.Fatalf("exit %d, want 1 with the work step failed:\n%s", code, output)
	}
}

func TestNormalFailsWhenAnInstanceIsMissing(t *testing.T) {
	beaconURL := startBeacon(t)
	startInstance(t, beaconURL, "orders", "")

	code, output, _ := runScenario(t, beaconURL, "-expect", "orders=2")

	if code != 1 || !strings.Contains(output, "returns 1 orders, want 2") {
		t.Fatalf("exit %d, want 1 naming the missing instance:\n%s", code, output)
	}
}

func TestExpectConsistentFindsDisagreement(t *testing.T) {
	orders := func(ids ...string) []client.Instance {
		instances := make([]client.Instance, 0, len(ids))
		for index, id := range ids {
			instances = append(instances, client.Instance{ID: id, Name: "orders", Address: "orders-" + strconv.Itoa(index), Port: 9000})
		}

		return instances
	}

	agreeing := view{
		"beacon-a": {"orders": orders("1", "2")},
		"beacon-b": {"orders": orders("1", "2")},
	}
	if err := expectConsistent(agreeing); err != nil {
		t.Fatalf("agreeing nodes: %v", err)
	}

	for name, seen := range map[string]view{
		"different instances": {"beacon-a": {"orders": orders("1", "2")}, "beacon-b": {"orders": orders("1", "3")}},
		"duplicate ID":        {"beacon-a": {"orders": orders("1", "1")}},
		"empty ID":            {"beacon-a": {"orders": orders("")}},
		"shared address": {"beacon-a": {"orders": {
			{ID: "1", Name: "orders", Address: "orders-1", Port: 9000},
			{ID: "2", Name: "orders", Address: "orders-1", Port: 9000},
		}}},
	} {
		if err := expectConsistent(seen); err == nil {
			t.Errorf("%s: no problem found", name)
		}
	}
}

func TestPercentile(t *testing.T) {
	sorted := make([]time.Duration, 0, 100)
	for value := 1; value <= 100; value++ {
		sorted = append(sorted, time.Duration(value)*time.Millisecond)
	}

	for fraction, want := range map[float64]time.Duration{0.5: 50 * time.Millisecond, 0.95: 95 * time.Millisecond, 0.99: 99 * time.Millisecond, 1: 100 * time.Millisecond} {
		if got := percentile(sorted, fraction); got != want {
			t.Errorf("percentile(%v) = %v, want %v", fraction, got, want)
		}
	}

	if got := percentile([]time.Duration{time.Second}, 0.5); got != time.Second {
		t.Errorf("one value: %v", got)
	}
}

func TestParseOptionsRejectsBadInput(t *testing.T) {
	for _, args := range [][]string{
		{"-scenario", "nope"},
		{"-nodes", "beacon-a"},
		{"-nodes", "beacon-a=http://a,beacon-a=http://b"},
		{"-expect", "orders=x"},
		{"-expect", "orders=-1"},
		{"-rounds", "0"},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Errorf("parseOptions(%q) succeeded", args)
		}
	}
}
