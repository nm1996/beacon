package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/nm1996/beacon/client"
	"github.com/nm1996/beacon/internal/bootstrap"
	"github.com/nm1996/beacon/internal/config"
)

// startBeacon serves a real Beacon API, in memory, until the test ends.
func startBeacon(t *testing.T) string {
	t.Helper()

	settings := config.Default()
	settings.RateLimit.RegistrationsPerSecond = 0

	components, err := bootstrap.New(settings, slog.New(slog.DiscardHandler), "test")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(components.Close)

	server := httptest.NewServer(components.Server.Handler)
	t.Cleanup(server.Close)

	return server.URL
}

// startApp runs the app against beaconURL on a free port and returns its
// base URL, a function that stops it, and the channel serve's result
// arrives on.
func startApp(t *testing.T, beaconURL string) (string, context.CancelFunc, <-chan error) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)

	go func() {
		result <- serve(ctx, settings{
			service:   "orders",
			beaconURL: beaconURL,
			address:   "127.0.0.1",
			port:      port,
		}, listener, slog.New(slog.DiscardHandler))
	}()

	t.Cleanup(cancel)

	return "http://127.0.0.1:" + strconv.Itoa(port), cancel, result
}

// getWork calls GET /work and returns the status and decoded body.
func getWork(t *testing.T, appURL string) (int, work) {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, appURL+"/work", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, work{}
	}
	defer response.Body.Close()

	var body work
	if response.StatusCode == http.StatusOK {
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
	}

	return response.StatusCode, body
}

// waitResult returns serve's result, failing the test if it takes too long.
func waitResult(t *testing.T, result <-chan error) error {
	t.Helper()

	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("app did not stop")
		return nil
	}
}

func TestAppRegistersAnswersAndUnregisters(t *testing.T) {
	beaconURL := startBeacon(t)
	appURL, stop, result := startApp(t, beaconURL)

	var answered work
	deadline := time.Now().Add(5 * time.Second)

	for {
		status, body := getWork(t, appURL)
		if status == http.StatusOK {
			answered = body
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("GET /work never succeeded, last status %d", status)
		}

		time.Sleep(20 * time.Millisecond)
	}

	beacon, err := client.New(beaconURL)
	if err != nil {
		t.Fatal(err)
	}

	instances, err := beacon.Discover(t.Context(), "orders")
	if err != nil {
		t.Fatal(err)
	}

	if len(instances) != 1 || instances[0].ID != answered.InstanceID || answered.Service != "orders" {
		t.Fatalf("Beacon lists %+v, the app answered %+v", instances, answered)
	}

	stop()

	if err := waitResult(t, result); err != nil {
		t.Fatalf("serve returned %v after a clean stop", err)
	}

	instances, err = beacon.Discover(t.Context(), "orders")
	if err != nil {
		t.Fatal(err)
	}

	if len(instances) != 0 {
		t.Fatalf("still registered after stopping: %+v", instances)
	}
}

func TestAppAnswers503UntilRegistered(t *testing.T) {
	// Nothing listens here, so every registration attempt fails.
	unreachable := httptest.NewServer(http.NotFoundHandler())
	beaconURL := unreachable.URL
	unreachable.Close()

	appURL, stop, result := startApp(t, beaconURL)

	deadline := time.Now().Add(5 * time.Second)

	for {
		status, _ := getWork(t, appURL)
		if status == http.StatusServiceUnavailable {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("GET /work answered %d, want 503 before registering", status)
		}

		time.Sleep(20 * time.Millisecond)
	}

	stop()

	if err := waitResult(t, result); err != nil {
		t.Fatalf("serve returned %v after a stop while registering", err)
	}
}

func TestParseSettings(t *testing.T) {
	parsed, err := parseSettings([]string{"-service", "orders", "-address", "orders-1"})
	if err != nil {
		t.Fatal(err)
	}

	if parsed.beaconURL != "http://lb" || parsed.port != 9000 {
		t.Fatalf("defaults: %+v", parsed)
	}

	for _, args := range [][]string{
		{"-address", "orders-1"},
		{"-service", "orders"},
		{"-service", "orders", "-address", "orders-1", "-port", "0"},
		{"-unknown"},
	} {
		if _, err := parseSettings(args); err == nil {
			t.Errorf("parseSettings(%q) succeeded", args)
		}
	}
}

func TestReadToken(t *testing.T) {
	dir := t.TempDir()

	path := filepath.Join(dir, "orders.token")
	if err := os.WriteFile(path, []byte("  orders-token-0123456789\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if token, err := readToken(path); err != nil || token != "orders-token-0123456789" {
		t.Fatalf("readToken = %q, %v", token, err)
	}

	empty := filepath.Join(dir, "empty.token")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{empty, filepath.Join(dir, "missing.token")} {
		if _, err := readToken(path); err == nil {
			t.Errorf("readToken(%s) succeeded", path)
		}
	}
}
