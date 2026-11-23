package grpcapi

import (
	"context"
	"crypto/x509"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	beaconv1 "github.com/nm1996/beacon/api/beacon/v1"
	"github.com/nm1996/beacon/internal/auth"
	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/ratelimit"
	"github.com/nm1996/beacon/internal/registry/memory"
)

// tokens is a fixed set of callers for the tests.
type tokens map[string]auth.Identity

// Authenticate looks token up.
func (set tokens) Authenticate(token string) (auth.Identity, bool) {
	identity, ok := set[token]
	return identity, ok
}

// AuthenticateCertificate accepts no certificate.
func (tokens) AuthenticateCertificate(*x509.Certificate) (auth.Identity, bool) {
	return auth.Identity{}, false
}

// testTokens holds two registering callers and one that may only discover.
var testTokens = tokens{
	"orders-token":   auth.NewIdentity("orders", auth.PermissionRegister, auth.PermissionDiscover),
	"payments-token": auth.NewIdentity("payments", auth.PermissionRegister, auth.PermissionDiscover),
	"reader-token":   auth.NewIdentity("reader", auth.PermissionDiscover),
}

// dial starts a server with config over an in-memory connection and
// returns a client connection to it.
func dial(t *testing.T, config Config) *grpc.ClientConn {
	t.Helper()

	service, err := discovery.New(memory.New(), discovery.Config{TTL: 30 * time.Second, CleanupInterval: time.Second, MaxInstances: 1000}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	listener := bufconn.Listen(1 << 20)
	server := NewServer(service, slog.New(slog.DiscardHandler), config)

	go func() { _ = server.Serve(listener) }()

	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// as returns ctx carrying token as bearer token.
func as(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}

// reasonOf returns the code and ErrorInfo reason of err.
func reasonOf(err error) (codes.Code, string) {
	result := status.Convert(err)

	for _, detail := range result.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok {
			return result.Code(), info.GetReason()
		}
	}

	return result.Code(), ""
}

func TestRegistryLifecycleOverGRPC(t *testing.T) {
	observed := atomic.Int32{}
	conn := dial(t, Config{Observe: func(string, codes.Code, time.Duration) { observed.Add(1) }})
	client := beaconv1.NewRegistryServiceClient(conn)
	ctx := t.Context()

	registered, err := client.Register(ctx, &beaconv1.RegisterRequest{Id: "orders-1", Name: "orders", Address: "10.0.0.1", Port: 8081})
	if err != nil || registered.GetId() != "orders-1" || !registered.GetCreated() || registered.GetTtl().AsDuration() != 30*time.Second {
		t.Fatalf("register: %+v, %v", registered, err)
	}

	again, err := client.Register(ctx, &beaconv1.RegisterRequest{Id: "orders-1", Name: "orders", Address: "10.0.0.1", Port: 8081})
	if err != nil || again.GetCreated() {
		t.Errorf("expected a repeat to refresh, got %+v, %v", again, err)
	}

	if _, err := client.Heartbeat(ctx, &beaconv1.HeartbeatRequest{Id: "orders-1"}); err != nil {
		t.Errorf("heartbeat: %v", err)
	}

	listed, err := client.ListInstances(ctx, &beaconv1.ListInstancesRequest{Name: "orders"})
	if err != nil || len(listed.GetInstances()) != 1 || listed.GetInstances()[0].GetPort() != 8081 || listed.GetNextPageToken() != "" {
		t.Errorf("list: %+v, %v", listed, err)
	}

	if _, err := client.Unregister(ctx, &beaconv1.UnregisterRequest{Id: "orders-1"}); err != nil {
		t.Errorf("unregister: %v", err)
	}

	if code, reason := reasonOf(func() error { _, err := client.Heartbeat(ctx, &beaconv1.HeartbeatRequest{Id: "orders-1"}); return err }()); code != codes.NotFound || reason != "service_not_found" {
		t.Errorf("expected NotFound service_not_found after unregistering, got %v %q", code, reason)
	}

	if observed.Load() != 6 {
		t.Errorf("expected every call observed, got %d", observed.Load())
	}
}

func TestListInstancesPages(t *testing.T) {
	client := beaconv1.NewRegistryServiceClient(dial(t, Config{}))
	ctx := t.Context()

	for _, id := range []string{"a", "b", "c"} {
		if _, err := client.Register(ctx, &beaconv1.RegisterRequest{Id: id, Name: "orders", Address: "10.0.0.1", Port: 8081}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := client.ListInstances(ctx, &beaconv1.ListInstancesRequest{PageSize: 2})
	if err != nil || len(first.GetInstances()) != 2 || first.GetNextPageToken() == "" {
		t.Fatalf("first page: %+v, %v", first, err)
	}

	second, err := client.ListInstances(ctx, &beaconv1.ListInstancesRequest{PageSize: 2, PageToken: first.GetNextPageToken()})
	if err != nil || len(second.GetInstances()) != 1 || second.GetInstances()[0].GetId() != "c" || second.GetNextPageToken() != "" {
		t.Errorf("second page: %+v, %v", second, err)
	}

	for _, request := range []*beaconv1.ListInstancesRequest{{PageSize: 1001}, {PageSize: -1}, {PageToken: "%%%"}} {
		if code, reason := reasonOf(func() error { _, err := client.ListInstances(ctx, request); return err }()); code != codes.InvalidArgument || reason != "invalid_query" {
			t.Errorf("%+v: expected InvalidArgument invalid_query, got %v %q", request, code, reason)
		}
	}
}

func TestErrorsCarryTheHTTPReasons(t *testing.T) {
	client := beaconv1.NewRegistryServiceClient(dial(t, Config{}))
	ctx := t.Context()

	if _, err := client.Register(ctx, &beaconv1.RegisterRequest{Id: "x", Name: "orders", Address: "10.0.0.1", Port: 8081}); err != nil {
		t.Fatal(err)
	}

	for name, tt := range map[string]struct {
		request *beaconv1.RegisterRequest
		code    codes.Code
		reason  string
	}{
		"invalid":    {request: &beaconv1.RegisterRequest{Name: "Orders!", Address: "10.0.0.1", Port: 8081}, code: codes.InvalidArgument, reason: "invalid_service"},
		"conflict":   {request: &beaconv1.RegisterRequest{Id: "x", Name: "orders", Address: "10.0.0.2", Port: 8081}, code: codes.AlreadyExists, reason: "instance_exists"},
		"id and key": {request: &beaconv1.RegisterRequest{Id: "y", IdempotencyKey: "k", Name: "orders", Address: "10.0.0.1", Port: 8081}, code: codes.InvalidArgument, reason: "invalid_service"},
	} {
		_, err := client.Register(ctx, tt.request)
		if code, reason := reasonOf(err); code != tt.code || reason != tt.reason {
			t.Errorf("%s: expected %v %q, got %v %q", name, tt.code, tt.reason, code, reason)
		}
	}
}

func TestAuthenticationAndOwnershipOverGRPC(t *testing.T) {
	failed := map[string]int{}
	client := beaconv1.NewRegistryServiceClient(dial(t, Config{
		Authenticator: testTokens,
		AuthFailed:    func(reason string) { failed[reason]++ },
	}))
	ctx := t.Context()

	if code, reason := reasonOf(func() error { _, err := client.ListInstances(ctx, &beaconv1.ListInstancesRequest{}); return err }()); code != codes.Unauthenticated || reason != "unauthorized" {
		t.Errorf("expected Unauthenticated without a token, got %v %q", code, reason)
	}

	if code, _ := reasonOf(func() error {
		_, err := client.ListInstances(as(ctx, "wrong"), &beaconv1.ListInstancesRequest{})
		return err
	}()); code != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated for a wrong token, got %v", code)
	}

	if code, reason := reasonOf(func() error {
		_, err := client.Register(as(ctx, "reader-token"), &beaconv1.RegisterRequest{Name: "orders", Address: "10.0.0.1", Port: 8081})
		return err
	}()); code != codes.PermissionDenied || reason != "forbidden" {
		t.Errorf("expected PermissionDenied forbidden without register, got %v %q", code, reason)
	}

	registered, err := client.Register(as(ctx, "orders-token"), &beaconv1.RegisterRequest{Name: "orders", Address: "10.0.0.1", Port: 8081})
	if err != nil {
		t.Fatal(err)
	}

	if code, reason := reasonOf(func() error {
		_, err := client.Heartbeat(as(ctx, "payments-token"), &beaconv1.HeartbeatRequest{Id: registered.GetId()})
		return err
	}()); code != codes.PermissionDenied || reason != "instance_not_owned" {
		t.Errorf("expected another caller to be refused, got %v %q", code, reason)
	}

	if _, err := client.Heartbeat(as(ctx, "orders-token"), &beaconv1.HeartbeatRequest{Id: registered.GetId()}); err != nil {
		t.Errorf("expected the owner's heartbeat to pass, got %v", err)
	}

	if failed["missing"] != 1 || failed["invalid"] != 1 || failed["forbidden"] != 1 {
		t.Errorf("expected one failure of each reason reported, got %v", failed)
	}
}

func TestLimitsAreSharedWithTheHTTPAPI(t *testing.T) {
	limited := map[string]int{}
	registrations := ratelimit.New(1, 1, 0)
	failures := ratelimit.New(1, 1, 0)

	// The HTTP API took the only registration token of this network key.
	registrations.Allow("network:bufconn")

	client := beaconv1.NewRegistryServiceClient(dial(t, Config{
		RegistrationLimit: registrations,
		RateLimited:       func(limit string) { limited[limit]++ },
	}))

	_, err := client.Register(t.Context(), &beaconv1.RegisterRequest{Name: "orders", Address: "10.0.0.1", Port: 8081})
	if code, reason := reasonOf(err); code != codes.ResourceExhausted || reason != "rate_limited" {
		t.Fatalf("expected ResourceExhausted rate_limited, got %v %q", code, reason)
	}

	var retry *errdetails.RetryInfo
	for _, detail := range status.Convert(err).Details() {
		if info, ok := detail.(*errdetails.RetryInfo); ok {
			retry = info
		}
	}

	if retry == nil || retry.GetRetryDelay().AsDuration() != time.Second {
		t.Errorf("expected RetryInfo of 1s, got %v", retry)
	}

	// Failed authentication uses the shared failure limiter too.
	authClient := beaconv1.NewRegistryServiceClient(dial(t, Config{Authenticator: testTokens, FailureLimit: failures}))
	_, _ = authClient.ListInstances(as(t.Context(), "wrong"), &beaconv1.ListInstancesRequest{})

	_, err = authClient.ListInstances(as(t.Context(), "orders-token"), &beaconv1.ListInstancesRequest{})
	if code, reason := reasonOf(err); code != codes.ResourceExhausted || reason != "rate_limited" {
		t.Errorf("expected the network to be limited after a failure, got %v %q", code, reason)
	}

	if limited[limitRegistrations] != 1 {
		t.Errorf("expected the registration refusal reported, got %v", limited)
	}
}

func TestHealthNeedsNoCredentials(t *testing.T) {
	conn := dial(t, Config{Authenticator: testTokens})

	response, err := healthpb.NewHealthClient(conn).Check(t.Context(), &healthpb.HealthCheckRequest{Service: beaconv1.RegistryService_ServiceDesc.ServiceName})
	if err != nil || response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Errorf("expected SERVING, got %v, %v", response, err)
	}
}
