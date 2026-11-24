package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeNode is a Beacon node whose storage goes away between downAt and
// upAt. It holds one orders instance. It accepts heartbeats from memory
// for acceptFor after storage went away, as batching does.
type fakeNode struct {
	downAt    time.Time
	upAt      time.Time
	acceptFor time.Duration

	mu            sync.Mutex
	flushFailures int
}

// down reports whether storage is away at now.
func (fake *fakeNode) down(now time.Time) bool {
	return !now.Before(fake.downAt) && now.Before(fake.upAt)
}

// ServeHTTP answers the requests storage-down sends.
func (fake *fakeNode) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	now := time.Now()

	switch {
	case request.URL.Path == "/health":
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	case request.URL.Path == "/api/v1/nodes":
		_, _ = writer.Write([]byte(`[{"id":"beacon-a"}]`))
	case request.URL.Path == "/metrics":
		fake.mu.Lock()
		fmt.Fprintf(writer, "beacon_heartbeat_flushes_total{result=\"error\"} %d\n", fake.flushFailures)
		fake.mu.Unlock()
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/services":
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"id":"probe","ttl_seconds":9}`))
	case request.Method == http.MethodPost:
		if fake.down(now) && now.Sub(fake.downAt) > fake.acceptFor {
			fake.mu.Lock()
			fake.flushFailures++
			fake.mu.Unlock()
			http.Error(writer, `{"code":"internal_error","message":"storage"}`, http.StatusInternalServerError)

			return
		}

		writer.WriteHeader(http.StatusNoContent)
	case request.Method == http.MethodDelete:
		writer.WriteHeader(http.StatusNoContent)
	case fake.down(now):
		http.Error(writer, `{"code":"internal_error","message":"storage"}`, http.StatusInternalServerError)
	default:
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode([]map[string]any{{
			"id": "orders-1", "name": "orders", "address": "orders-1", "port": 9000, "last_heartbeat": now,
		}})
	}
}

// runOutage runs storage-down against a fakeNode whose storage is away
// from half a second from now for outage.
func runOutage(t *testing.T, outage, acceptFor time.Duration) (int, string) {
	t.Helper()

	since := time.Now().Add(500 * time.Millisecond)
	fake := &fakeNode{downAt: since, upAt: since.Add(outage), acceptFor: acceptFor}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	code, output, _ := runScenario(t, server.URL,
		"-scenario", "storage-down", "-expect", "orders=1",
		"-since", strconv.FormatFloat(float64(since.UnixMicro())/1e6, 'f', 6, 64),
		"-outage", outage.String(), "-recover", "2s", "-within", "2s")

	return code, output
}

func TestStorageDownPassesWhenBeaconBehaves(t *testing.T) {
	code, output := runOutage(t, time.Second, 0)

	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, output)
	}
}

func TestStorageDownFailsWhenHeartbeatsAreAcceptedTooLong(t *testing.T) {
	code, output := runOutage(t, 3*time.Second, time.Hour)

	if code != 1 || !strings.Contains(output, "after the first failed lookup") {
		t.Fatalf("exit %d, want 1 with heartbeats accepted late:\n%s", code, output)
	}
}

func TestStorageDownFailsWhenNoHeartbeatIsRefused(t *testing.T) {
	code, output := runOutage(t, time.Second, time.Hour)

	if code != 1 || !strings.Contains(output, "no heartbeat was refused with 500") {
		t.Fatalf("exit %d, want 1 with no heartbeat refused:\n%s", code, output)
	}
}
