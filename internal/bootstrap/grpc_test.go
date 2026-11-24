package bootstrap

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	beaconv1 "github.com/nm1996/beacon/api/beacon/v1"
	"github.com/nm1996/beacon/internal/config"
	"github.com/nm1996/beacon/internal/tlstest"
)

func TestGRPCIsOffByDefault(t *testing.T) {
	if components := mustNew(t, config.Default(), "test"); components.GRPC != nil {
		t.Error("expected no gRPC server without grpc.port")
	}
}

func TestGRPCOverTLSWithTokensSharesTheHTTPLimits(t *testing.T) {
	certificate := tlstest.WriteCertificate(t)

	settings := authSettings(t)
	settings.TLS = config.TLSConfig{CertFile: certificate.CertFile, KeyFile: certificate.KeyFile}
	settings.GRPC.Port = 9090
	settings.RateLimit.RegistrationsPerSecond = 0.001
	settings.RateLimit.RegistrationBurst = 2

	components := mustNew(t, settings, "test")
	if components.GRPC == nil || !strings.HasSuffix(components.GRPCAddr, ":9090") {
		t.Fatalf("expected a gRPC server on port 9090, got %v at %q", components.GRPC, components.GRPCAddr)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	go func() { _ = components.GRPC.Serve(listener) }()
	t.Cleanup(components.GRPC.Stop)

	conn, err := grpc.NewClient(listener.Addr().String(),
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: certificate.Pool, ServerName: "localhost", MinVersion: tls.VersionTLS12})))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := beaconv1.NewRegistryServiceClient(conn)
	ctx := metadata.AppendToOutgoingContext(t.Context(), "authorization", "Bearer "+serviceToken)

	if _, err := client.Register(ctx, &beaconv1.RegisterRequest{Name: "orders", Address: "10.0.0.1", Port: 8081}); err != nil {
		t.Fatalf("register over TLS with a token: %v", err)
	}

	if _, err := client.ListInstances(t.Context(), &beaconv1.ListInstancesRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated without a token, got %v", err)
	}

	// The same caller over HTTP spends the rest of the shared burst of 2.
	request := httptest.NewRequest(http.MethodPost, "/api/v1/services", strings.NewReader(`{"name":"orders","address":"10.0.0.1","port":8082}`))
	request.Header.Set("Authorization", "Bearer "+serviceToken)

	recorder := httptest.NewRecorder()
	components.Server.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected the HTTP registration to fit the burst, got %d", recorder.Code)
	}

	if _, err := client.Register(ctx, &beaconv1.RegisterRequest{Name: "orders", Address: "10.0.0.1", Port: 8083}); status.Code(err) != codes.ResourceExhausted {
		t.Errorf("expected gRPC to see the budget HTTP used, got %v", err)
	}
}
