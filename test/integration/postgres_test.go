package integration_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/nm1996/beacon/client"
	"github.com/nm1996/beacon/internal/config"
	"github.com/nm1996/beacon/internal/registry/postgres/postgrestest"
)

// postgresSettings returns settings that keep instances in url's database.
// Every node started with them shares it.
func postgresSettings(url string) config.Config {
	settings := settings()
	settings.Storage.Backend = config.StoragePostgres
	settings.Storage.Postgres.URL = url

	return settings
}

// ids returns the IDs of instances.
func ids(instances []client.Instance) []string {
	result := make([]string, 0, len(instances))
	for _, instance := range instances {
		result = append(result, instance.ID)
	}

	return result
}

func TestNodesShareInstancesOverPostgres(test *testing.T) {
	shared := postgresSettings(postgrestest.URL(test))
	first := newClient(test, start(test, shared, ""))
	second := newClient(test, start(test, shared, ""))
	ctx := test.Context()

	registered, err := first.Register(ctx, client.Registration{Name: "orders", Address: "10.0.0.1", Port: 8081})
	if err != nil {
		test.Fatal(err)
	}

	if got := ids(discover(test, second, "orders")); !slices.Equal(got, []string{registered.ID}) {
		test.Fatalf("expected the other node to find %s, got %v", registered.ID, got)
	}

	if err := second.Heartbeat(ctx, registered.ID); err != nil {
		test.Fatalf("heartbeat through the other node: %v", err)
	}

	if err := second.Unregister(ctx, registered.ID); err != nil {
		test.Fatalf("unregister through the other node: %v", err)
	}

	if got := discover(test, first, "orders"); len(got) != 0 {
		test.Errorf("expected the instance gone from the node that registered it, got %v", ids(got))
	}

	if err := first.Heartbeat(ctx, registered.ID); !errors.Is(err, client.ErrNotFound) {
		test.Errorf("expected a heartbeat for the removed instance to be refused, got %v", err)
	}
}

func TestHeartbeatsBatchedOnOneNodeKeepInstancesAliveOnAnother(test *testing.T) {
	shared := postgresSettings(postgrestest.URL(test))
	shared.Registry.ServiceTTL = time.Second
	shared.Storage.Postgres.HeartbeatFlushInterval = 100 * time.Millisecond

	registering := newClient(test, start(test, shared, ""))
	beating := newClient(test, start(test, shared, ""))
	ctx := test.Context()

	registered, err := registering.Register(ctx, client.Registration{Name: "orders", Address: "10.0.0.1", Port: 8081})
	if err != nil {
		test.Fatal(err)
	}

	// Three TTLs of heartbeats, every one only through the other node, while
	// both nodes run cleanup every 100ms.
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if err := beating.Heartbeat(ctx, registered.ID); err != nil {
			test.Fatalf("heartbeat: %v", err)
		}

		time.Sleep(200 * time.Millisecond)
	}

	if got := ids(discover(test, registering, "orders")); !slices.Equal(got, []string{registered.ID}) {
		test.Fatalf("expected batched heartbeats to keep the instance alive on every node, got %v", got)
	}

	// Without heartbeats it expires everywhere.
	eventually(test, 5*time.Second, "the instance to expire on both nodes", func() bool {
		return len(discover(test, registering, "orders")) == 0 && len(discover(test, beating, "orders")) == 0
	})
}

func TestInstancesSurviveARestartOverPostgres(test *testing.T) {
	shared := postgresSettings(postgrestest.URL(test))
	server := start(test, shared, "")
	ctx := test.Context()

	registered, err := newClient(test, server).Register(ctx, client.Registration{Name: "orders", Address: "10.0.0.1", Port: 8081})
	if err != nil {
		test.Fatal(err)
	}

	server.stop()

	restarted := newClient(test, start(test, shared, ""))

	if got := ids(discover(test, restarted, "orders")); !slices.Equal(got, []string{registered.ID}) {
		test.Fatalf("expected %s to survive the restart, got %v", registered.ID, got)
	}

	if err := restarted.Heartbeat(ctx, registered.ID); err != nil {
		test.Errorf("heartbeat after the restart: %v", err)
	}
}

func TestANodeKeepsServingWhenAnotherStops(test *testing.T) {
	shared := postgresSettings(postgrestest.URL(test))
	leaving := start(test, shared, "")
	staying := start(test, shared, "")
	ctx := test.Context()

	eventually(test, 5*time.Second, "both nodes to be listed", func() bool {
		return len(liveNodes(test, staying)) == 2
	})

	registered, err := newClient(test, leaving).Register(ctx, client.Registration{Name: "orders", Address: "10.0.0.1", Port: 8081})
	if err != nil {
		test.Fatal(err)
	}

	leaving.stop()

	// A node that shuts down removes itself at once instead of after LiveFor.
	if nodes := liveNodes(test, staying); len(nodes) != 1 {
		test.Errorf("expected only the remaining node to be listed, got %v", nodes)
	}

	remaining := newClient(test, staying)

	if err := remaining.Heartbeat(ctx, registered.ID); err != nil {
		test.Fatalf("heartbeat through the remaining node: %v", err)
	}

	if got := ids(discover(test, remaining, "orders")); !slices.Equal(got, []string{registered.ID}) {
		test.Errorf("expected the remaining node to serve %s, got %v", registered.ID, got)
	}
}

// liveNodes returns the IDs GET /api/v1/nodes lists on server.
func liveNodes(test *testing.T, server *beacon) []string {
	test.Helper()

	request, err := http.NewRequestWithContext(test.Context(), http.MethodGet, server.url()+"/api/v1/nodes", nil)
	if err != nil {
		test.Fatal(err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		test.Fatalf("list nodes: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		test.Fatalf("list nodes: status %d", response.StatusCode)
	}

	var nodes []struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(response.Body).Decode(&nodes); err != nil {
		test.Fatalf("decode nodes: %v", err)
	}

	result := make([]string, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, node.ID)
	}

	return result
}
