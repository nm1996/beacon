package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
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

// serveWork serves /work for an instance of service, answering with
// answerAs, or, when answerAs is empty, with the ID passed to the returned
// function once the instance is registered. It returns the registration
// to make, without an ID.
func serveWork(t *testing.T, service, answerAs string) (client.Registration, func(id string)) {
	t.Helper()

	var id atomic.Pointer[string]

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		answered := answerAs
		if answered == "" {
			if current := id.Load(); current != nil {
				answered = *current
			}
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

	return client.Registration{Name: service, Address: host, Port: portNumber}, func(registered string) { id.Store(&registered) }
}

// startInstance registers an instance of service with Beacon, served by
// a /work handler that answers with answerAs, or with its own ID when
// answerAs is empty. It returns the registration.
func startInstance(t *testing.T, beaconURL, service, answerAs string) client.Registration {
	t.Helper()

	registration, setID := serveWork(t, service, answerAs)

	beacon, err := client.New(beaconURL)
	if err != nil {
		t.Fatal(err)
	}

	// The ID Beacon will choose is not known before registering, so the
	// instance names its own, as the simulation's applications may.
	registration.ID = "instance-" + strconv.Itoa(registration.Port)
	setID(registration.ID)

	if _, err := beacon.Register(t.Context(), registration); err != nil {
		t.Fatal(err)
	}

	return registration
}

// hostPort is how -address names registration.
func hostPort(registration client.Registration) string {
	return net.JoinHostPort(registration.Address, strconv.Itoa(registration.Port))
}

// runScenario runs the runner with args against beaconURL and returns its
// exit code, its output, and the report it wrote.
func runScenario(t *testing.T, beaconURL string, args ...string) (int, string, *Report) {
	t.Helper()

	reports := t.TempDir()
	args = append([]string{"-nodes", "beacon-a=" + beaconURL, "-reports", reports, "-timeout", "2s", "-rounds", "2"}, args...)

	var stdout, stderr bytes.Buffer

	code := run(t.Context(), args, &stdout, &stderr)

	files, err := filepath.Glob(filepath.Join(reports, "*", "*.json"))
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
		{"-scenario", "gone", "-service", "orders"},
		{"-scenario", "back", "-service", "nope", "-address", "x"},
		{"-scenario", "node-down"},
		{"-scenario", "ownership", "-service", "orders"},
		{"-scenario", "load", "-service", "nope"},
		{"-scenario", "load", "-service", "orders", "-call-rate", "0"},
		{"-token-file", "/does/not/exist"},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Errorf("parseOptions(%q) succeeded", args)
		}
	}
}

// unregisterAfter removes registration from Beacon after delay.
func unregisterAfter(t *testing.T, beaconURL string, registration client.Registration, delay time.Duration) {
	t.Helper()

	beacon, err := client.New(beaconURL)
	if err != nil {
		t.Fatal(err)
	}

	time.AfterFunc(delay, func() { _ = beacon.Unregister(context.Background(), registration.ID) })
}

func TestGonePassesWhenTheInstanceLeavesInTime(t *testing.T) {
	beaconURL := startBeacon(t)
	startInstance(t, beaconURL, "orders", "")
	target := startInstance(t, beaconURL, "orders", "")
	unregisterAfter(t, beaconURL, target, 300*time.Millisecond)

	code, output, report := runScenario(t, beaconURL,
		"-scenario", "gone", "-expect", "orders=2", "-service", "orders", "-address", hostPort(target), "-within", "2s")

	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, output)
	}

	if report.Target == nil || report.Target.ID != target.ID || !strings.Contains(output, "target id: "+target.ID) {
		t.Errorf("target %+v, want ID %s:\n%s", report.Target, target.ID, output)
	}
}

func TestGoneFailsWhenTheInstanceStays(t *testing.T) {
	beaconURL := startBeacon(t)
	startInstance(t, beaconURL, "orders", "")
	target := startInstance(t, beaconURL, "orders", "")

	code, output, _ := runScenario(t, beaconURL,
		"-scenario", "gone", "-expect", "orders=2", "-service", "orders", "-address", hostPort(target), "-within", "200ms")

	if code != 1 || !strings.Contains(output, "left not seen within") {
		t.Fatalf("exit %d, want 1 with the instance never leaving:\n%s", code, output)
	}
}

func TestGoneFailsWhenAnotherInstanceLeaves(t *testing.T) {
	beaconURL := startBeacon(t)
	other := startInstance(t, beaconURL, "orders", "")
	target := startInstance(t, beaconURL, "orders", "")
	unregisterAfter(t, beaconURL, target, 300*time.Millisecond)
	unregisterAfter(t, beaconURL, other, 300*time.Millisecond)

	code, output, _ := runScenario(t, beaconURL,
		"-scenario", "gone", "-expect", "orders=2", "-service", "orders", "-address", hostPort(target), "-within", "2s")

	if code != 1 || !strings.Contains(output, "FAIL every other instance stays registered") {
		t.Fatalf("exit %d, want 1 with the other instance gone:\n%s", code, output)
	}
}

func TestBackPassesUnderANewID(t *testing.T) {
	beaconURL := startBeacon(t)
	since := strconv.FormatInt(time.Now().Unix(), 10)
	target := startInstance(t, beaconURL, "orders", "")

	code, output, _ := runScenario(t, beaconURL,
		"-scenario", "back", "-expect", "orders=1", "-service", "orders", "-address", hostPort(target),
		"-since", since, "-within", "3s", "-old-id", "the-old-one")

	if code != 0 || !strings.Contains(output, "comes back under a new ID") {
		t.Fatalf("exit %d, want 0:\n%s", code, output)
	}
}

func TestBackFailsUnderTheOldID(t *testing.T) {
	beaconURL := startBeacon(t)
	target := startInstance(t, beaconURL, "orders", "")

	code, output, _ := runScenario(t, beaconURL,
		"-scenario", "back", "-expect", "orders=1", "-service", "orders", "-address", hostPort(target),
		"-within", "3s", "-old-id", target.ID)

	if code != 1 || !strings.Contains(output, "FAIL the old ID never returns") {
		t.Fatalf("exit %d, want 1 with the old ID returned:\n%s", code, output)
	}
}

func TestBackChecksAFixedID(t *testing.T) {
	beaconURL := startBeacon(t)
	target := startInstance(t, beaconURL, "orders", "")

	code, output, _ := runScenario(t, beaconURL,
		"-scenario", "back", "-expect", "orders=1", "-service", "orders", "-address", hostPort(target),
		"-within", "3s", "-want-id", "orders-2")

	if code != 1 || !strings.Contains(output, "FAIL "+hostPort(target)+" comes back as orders-2") {
		t.Fatalf("exit %d, want 1 with the wrong ID:\n%s", code, output)
	}
}

func TestParseSince(t *testing.T) {
	parsed, err := parseSince("1700000000.25")
	if err != nil || !parsed.Equal(time.Unix(1700000000, 250_000_000)) {
		t.Fatalf("Unix seconds: %v, %v", parsed, err)
	}

	parsed, err = parseSince("2026-11-24T18:00:00.5Z")
	if err != nil || !parsed.Equal(time.Date(2026, 11, 24, 18, 0, 0, 500_000_000, time.UTC)) {
		t.Fatalf("RFC 3339: %v, %v", parsed, err)
	}

	if _, err := parseSince("yesterday"); err == nil {
		t.Fatal("parsed yesterday")
	}
}

func TestGoneFailsWhenTheInstanceLeavesLate(t *testing.T) {
	beaconURL := startBeacon(t)
	startInstance(t, beaconURL, "orders", "")
	target := startInstance(t, beaconURL, "orders", "")
	unregisterAfter(t, beaconURL, target, 800*time.Millisecond)

	code, output, _ := runScenario(t, beaconURL,
		"-scenario", "gone", "-expect", "orders=2", "-service", "orders", "-address", hostPort(target), "-within", "200ms")

	if code != 1 || !strings.Contains(output, "bound 200ms") {
		t.Fatalf("exit %d, want 1 with the instance late:\n%s", code, output)
	}
}

func TestBackWaitsForAFutureSince(t *testing.T) {
	beaconURL := startBeacon(t)
	since := time.Now().Add(time.Second)

	target, setID := serveWork(t, "orders", "")
	target.ID = "late-orders"
	setID(target.ID)

	beacon, err := client.New(beaconURL)
	if err != nil {
		t.Fatal(err)
	}

	registered := make(chan error, 1)

	time.AfterFunc(1500*time.Millisecond, func() {
		_, err := beacon.Register(context.Background(), target)
		registered <- err
	})

	code, output, report := runScenario(t, beaconURL,
		"-scenario", "back", "-expect", "orders=1", "-service", "orders", "-address", hostPort(target),
		"-since", strconv.FormatFloat(float64(since.UnixMicro())/1e6, 'f', 6, 64), "-within", "2s")

	if err := <-registered; err != nil {
		t.Fatal(err)
	}

	if code != 0 || !strings.Contains(output, "ok   the runner is ready before -since") {
		t.Fatalf("exit %d, want 0 having waited:\n%s", code, output)
	}

	// Measured from -since, not from the runner's start a second earlier.
	if after := report.Timings["back_beacon-a"]; after < 300 || after > 1500 {
		t.Errorf("back after %vms, want about 500ms after -since", after)
	}
}

func TestOthersAnsweredCountsFailedPicks(t *testing.T) {
	caller := &traffic{calls: map[string]int{"orders-1:9000": 3}, failures: map[string]int{}, pickFailures: 2}

	if err := othersAnswered(caller, ""); err == nil || !strings.Contains(err.Error(), "could not pick an instance 2 times") {
		t.Fatalf("othersAnswered = %v, want the failed picks named", err)
	}

	caller.pickFailures = 0
	if err := othersAnswered(caller, ""); err != nil {
		t.Fatalf("othersAnswered = %v with every call answered", err)
	}
}

// unreachableURL returns the URL of a server that has already stopped.
func unreachableURL(t *testing.T) string {
	t.Helper()

	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()

	return server.URL
}

func TestNodeDownPassesWhileTheOtherNodeServes(t *testing.T) {
	beaconURL := startBeacon(t)
	startInstance(t, beaconURL, "orders", "")

	code, output, report := runScenario(t, beaconURL,
		"-nodes", "beacon-a="+beaconURL+",beacon-b="+unreachableURL(t),
		"-scenario", "node-down", "-down", "beacon-b", "-expect", "orders=1", "-within", "1s", "-watch", "1s")

	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, output)
	}

	if _, ok := report.Timings["unlisted_beacon-a"]; !ok {
		t.Errorf("no unlisting time in %v", report.Timings)
	}
}

func TestNodeDownFailsWhenAnInstanceExpires(t *testing.T) {
	beaconURL := startBeacon(t)
	startInstance(t, beaconURL, "orders", "")
	leaving := startInstance(t, beaconURL, "orders", "")
	unregisterAfter(t, beaconURL, leaving, 300*time.Millisecond)

	code, output, _ := runScenario(t, beaconURL,
		"-nodes", "beacon-a="+beaconURL+",beacon-b="+unreachableURL(t),
		"-scenario", "node-down", "-down", "beacon-b", "-expect", "orders=2", "-within", "1s", "-watch", "1s")

	if code != 1 || !strings.Contains(output, "FAIL every instance stays registered for 1s: beacon-a returns 1 orders") {
		t.Fatalf("exit %d, want 1 with an instance gone:\n%s", code, output)
	}
}

func TestNodeDownFailsWhileTheNodeIsListed(t *testing.T) {
	beaconURL := startBeacon(t)
	startInstance(t, beaconURL, "orders", "")

	// The node really is beacon-a, so it keeps listing beacon-a.
	code, output, _ := runScenario(t, beaconURL,
		"-nodes", "beacon-a="+unreachableURL(t)+",beacon-b="+beaconURL,
		"-scenario", "node-down", "-down", "beacon-a", "-expect", "orders=1", "-within", "1s", "-watch", "1s")

	if code != 1 || !strings.Contains(output, "FAIL the other nodes stop listing beacon-a within 1s") {
		t.Fatalf("exit %d, want 1 with beacon-a still listed:\n%s", code, output)
	}
}

// startSecureBeacon serves a real Beacon API in memory with
// authentication on, as the node beacon-a, and returns its URL and the
// tokens of the callers orders, payments and runner, the last of which
// may also manage and read metrics.
func startSecureBeacon(t *testing.T) (string, map[string]string) {
	t.Helper()

	tokens := map[string]string{}
	var file strings.Builder

	for _, caller := range []string{"orders", "payments", "runner"} {
		token := caller + "-token-for-the-simulation-test"
		tokens[caller] = token

		permissions := `["register", "discover"]`
		if caller == "runner" {
			permissions = `["register", "discover", "metrics", "manage"]`
		}

		sum := sha256.Sum256([]byte(token))
		fmt.Fprintf(&file, "[[tokens]]\nname = %q\nsha256 = %q\npermissions = %s\n\n", caller, hex.EncodeToString(sum[:]), permissions)
	}

	path := filepath.Join(t.TempDir(), "tokens.toml")
	if err := os.WriteFile(path, []byte(file.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	settings := config.Default()
	settings.RateLimit.RegistrationsPerSecond = 0
	settings.Server.NodeID = "beacon-a"
	settings.Auth.Enabled = true
	settings.Auth.TokensFile = path

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

	return server.URL, tokens
}

// writeToken saves token in a file and returns its path.
func writeToken(t *testing.T, token string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestOwnershipHoldsAgainstAnotherService(t *testing.T) {
	beaconURL, tokens := startSecureBeacon(t)

	orders, err := client.New(beaconURL, client.WithToken(tokens["orders"]))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := orders.Register(t.Context(), client.Registration{Name: "orders", Address: "orders-1", Port: 9000}); err != nil {
		t.Fatal(err)
	}

	// In this order: an attacker with manage succeeds, removing the
	// instance, so it goes last.
	for _, attack := range []struct {
		attacker string
		wantCode int
	}{{"payments", 0}, {"runner", 1}} {
		code, output, _ := runScenario(t, beaconURL,
			"-scenario", "ownership", "-expect", "orders=1", "-service", "orders",
			"-token-file", writeToken(t, tokens["runner"]), "-attacker-token-file", writeToken(t, tokens[attack.attacker]))

		if code != attack.wantCode {
			t.Errorf("attacking as %s: exit %d, want %d:\n%s", attack.attacker, code, attack.wantCode, output)
		}
	}
}

func TestLoadPassesWhileEveryInstanceStays(t *testing.T) {
	beaconURL := startBeacon(t)
	instance := startInstance(t, beaconURL, "catalog", "")
	startInstance(t, beaconURL, "catalog", "")

	code, output, report := runScenario(t, beaconURL,
		"-scenario", "load", "-expect", "catalog=2", "-service", "catalog", "-duration", "1500ms", "-lookup-rate", "10", "-call-rate", "20")

	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, output)
	}

	if count := report.Calls[hostPort(instance)]; count.Calls == 0 || count.Failures != 0 {
		t.Errorf("calls to %s: %+v", hostPort(instance), count)
	}
}

func TestLoadFailsWhenAnInstanceLeaves(t *testing.T) {
	beaconURL := startBeacon(t)
	startInstance(t, beaconURL, "catalog", "")
	leaving := startInstance(t, beaconURL, "catalog", "")
	unregisterAfter(t, beaconURL, leaving, 300*time.Millisecond)

	code, output, _ := runScenario(t, beaconURL,
		"-scenario", "load", "-expect", "catalog=2", "-service", "catalog", "-duration", "1500ms")

	if code != 1 || !strings.Contains(output, "FAIL every instance stays registered for 1.5s") {
		t.Fatalf("exit %d, want 1 with an instance gone:\n%s", code, output)
	}
}

func TestLookupPairCoversEveryNodeAndService(t *testing.T) {
	for _, shape := range [][2]int{{2, 4}, {2, 3}, {3, 3}, {1, 4}} {
		nodes, services := shape[0], shape[1]
		seen := map[[2]int]bool{}

		for turn := range nodes * services {
			nodeIndex, serviceIndex := lookupPair(turn, nodes, services)
			seen[[2]int{nodeIndex, serviceIndex}] = true
		}

		if len(seen) != nodes*services {
			t.Errorf("%d nodes, %d services: %d of %d pairs covered", nodes, services, len(seen), nodes*services)
		}
	}
}

func TestLoadFailsWhenInterrupted(t *testing.T) {
	beaconURL := startBeacon(t)
	startInstance(t, beaconURL, "catalog", "")

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(time.Second, cancel)

	var stdout, stderr bytes.Buffer

	code := run(ctx, []string{
		"-nodes", "beacon-a=" + beaconURL, "-reports", t.TempDir(), "-timeout", "2s",
		"-scenario", "load", "-expect", "catalog=1", "-service", "catalog", "-duration", "1m",
	}, &stdout, &stderr)

	if code != 1 || !strings.Contains(stdout.String(), "FAIL scenario completed: context canceled") {
		t.Fatalf("exit %d, want 1 for an interrupted run:\n%s%s", code, stdout.String(), stderr.String())
	}
}
