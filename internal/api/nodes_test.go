package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nm1996/beacon/internal/auth"
)

// twoNodes lists two fixed nodes.
func twoNodes(context.Context) ([]Node, error) {
	started := time.Date(2026, time.November, 23, 9, 0, 0, 0, time.UTC)

	return []Node{
		{ID: "a", Address: "10.0.0.1:8080", Version: "v0.3.0", StartedAt: started, LastSeen: started.Add(time.Hour)},
		{ID: "b", Address: "10.0.0.2:8080", Version: "v0.3.0", StartedAt: started, LastSeen: started.Add(time.Hour)},
	}, nil
}

func TestNodesNeedTheManagePermission(t *testing.T) {
	config := testConfig
	config.Nodes = twoNodes
	config.Guard = NewGuard(fakeTokens{
		"operator-token": auth.NewIdentity("operator", auth.PermissionRegister, auth.PermissionManage),
		"orders-token":   auth.NewIdentity("orders", auth.PermissionRegister, auth.PermissionDiscover),
	}, discard, nil)
	router := NewRouter(newDiscovery(t, testTTL), discard, config)

	if recorder := send(router, http.MethodGet, nodesPath, "orders-token", ""); recorder.Code != http.StatusForbidden {
		t.Errorf("expected 403 without manage, got %d", recorder.Code)
	}

	recorder := send(router, http.MethodGet, nodesPath, "operator-token", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200 with manage, got %d", recorder.Code)
	}

	var nodes []Node
	if err := json.Unmarshal(recorder.Body.Bytes(), &nodes); err != nil || len(nodes) != 2 || nodes[1].ID != "b" {
		t.Errorf("expected both nodes, got %s, %v", recorder.Body.String(), err)
	}

	if want := `"started_at":"2026-11-23T09:00:00Z"`; !strings.Contains(recorder.Body.String(), want) {
		t.Errorf("expected %s in %s", want, recorder.Body.String())
	}
}

func TestNodesFailureIsAnInternalError(t *testing.T) {
	config := testConfig
	config.Nodes = func(context.Context) ([]Node, error) { return nil, errors.New("database down") }
	router := NewRouter(newDiscovery(t, testTTL), discard, config)

	if recorder := serve(router, http.MethodGet, nodesPath, ""); recorder.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", recorder.Code)
	}
}

func TestNodesAreNotRoutedWithoutALister(t *testing.T) {
	router := NewRouter(newDiscovery(t, testTTL), discard, testConfig)

	if recorder := serve(router, http.MethodGet, nodesPath, ""); recorder.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", recorder.Code)
	}
}
