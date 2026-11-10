package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nm1996/beacon/internal/registry"
)

// TestConcurrentTrafficWithBackgroundCleanup runs the same mix Beacon sees in
// production: many clients registering, heartbeating, looking up and
// unregistering through the router while the cleanup worker sweeps the same
// registry. Run it with go test -race.
func TestConcurrentTrafficWithBackgroundCleanup(t *testing.T) {
	const clients = 16
	const heartbeatsPerClient = 50

	store := registry.New()
	logger := slog.New(slog.DiscardHandler)
	router := NewRouter(store, logger, testConfig)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cleanupDone := make(chan error, 1)

	go func() {
		cleanupDone <- store.RunCleanup(ctx, registry.CleanupConfig{Interval: time.Millisecond, TTL: time.Hour}, logger)
	}()

	send := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		return rec
	}

	var wg sync.WaitGroup
	start := make(chan struct{})

	for client := range clients {
		wg.Go(func() {
			<-start

			name := fmt.Sprintf("client-%d", client)

			rec := send(http.MethodPost, "/api/v1/services", fmt.Sprintf(`{"name":%q,"address":"10.0.0.1","port":%d}`, name, 8000+client))
			if rec.Code != http.StatusCreated {
				t.Errorf("%s: register: status %d", name, rec.Code)
				return
			}

			var registered RegisterResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &registered); err != nil {
				t.Errorf("%s: decode register response: %v", name, err)
				return
			}

			for range heartbeatsPerClient {
				if rec := send(http.MethodPost, "/api/v1/services/"+registered.ID+"/heartbeat", ""); rec.Code != http.StatusNoContent {
					t.Errorf("%s: heartbeat: status %d", name, rec.Code)
				}

				rec := send(http.MethodGet, "/api/v1/services?name="+name, "")

				var services []Service
				if err := json.Unmarshal(rec.Body.Bytes(), &services); err != nil || len(services) != 1 {
					t.Errorf("%s: expected to find itself, got %q (%v)", name, rec.Body.String(), err)
				}

				// Listing everything races with other clients registering
				// and unregistering; it must still always be valid JSON.
				if rec := send(http.MethodGet, "/api/v1/services", ""); !json.Valid(rec.Body.Bytes()) {
					t.Errorf("%s: list all returned invalid JSON: %q", name, rec.Body.String())
				}
			}

			if rec := send(http.MethodDelete, "/api/v1/services/"+registered.ID, ""); rec.Code != http.StatusNoContent {
				t.Errorf("%s: unregister: status %d", name, rec.Code)
			}
		})
	}

	close(start)
	wg.Wait()
	cancel()

	if err := <-cleanupDone; err != nil {
		t.Errorf("expected cleanup to stop cleanly, got %v", err)
	}

	if remaining := store.GetServices(""); len(remaining) != 0 {
		t.Errorf("expected every client to have unregistered, %d services remain", len(remaining))
	}
}
