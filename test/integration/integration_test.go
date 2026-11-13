// Package integration runs Beacon the way cmd/beacon does, on a real TCP
// port with its cleanup worker, and drives it with the Go client, so the
// pieces are tested together rather than each against fakes.
package integration_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nm1996/beacon/client"
	"github.com/nm1996/beacon/internal/auth"
	"github.com/nm1996/beacon/internal/bootstrap"
	"github.com/nm1996/beacon/internal/config"
	"github.com/nm1996/beacon/internal/tlstest"
)

// beacon is a running server.
type beacon struct {
	components *bootstrap.Components
	address    string
	scheme     string
	stop       func()
}

// url is the base URL clients use for the server.
func (server *beacon) url() string {
	return server.scheme + "://" + server.address
}

// settings returns defaults suited to tests: no registration rate limit,
// since every client here shares 127.0.0.1, and a quick cleanup.
func settings() config.Config {
	settings := config.Default()
	settings.Registry.CleanupInterval = 100 * time.Millisecond
	settings.RateLimit.RegistrationsPerSecond = 0

	return settings
}

// shortTTL returns settings with a one-second TTL, for tests that need
// expiry to happen while they watch.
func shortTTL() config.Config {
	settings := settings()
	settings.Registry.ServiceTTL = time.Second

	return settings
}

// start runs Beacon with settings on address, or on a free port when
// address is empty, until stop is called or the test ends.
func start(test *testing.T, settings config.Config, address string) *beacon {
	test.Helper()

	if err := settings.Validate(); err != nil {
		test.Fatalf("invalid settings: %v", err)
	}

	components, err := bootstrap.New(settings, slog.New(slog.DiscardHandler), "integration")
	if err != nil {
		test.Fatalf("build server: %v", err)
	}

	if address == "" {
		address = "127.0.0.1:0"
	}

	var listener net.Listener

	// The address of a server that just stopped can take a moment to free.
	for attempt := 0; ; attempt++ {
		listener, err = net.Listen("tcp", address)
		if err == nil {
			break
		}

		if attempt == 50 {
			test.Fatalf("listen on %s: %v", address, err)
		}

		time.Sleep(20 * time.Millisecond)
	}

	ctx, cancel := context.WithCancel(context.Background())
	server := components.Server

	// Serve writes to TLSConfig, so it is read only before serving starts.
	servesTLS := server.TLSConfig != nil

	scheme := "http"
	if servesTLS {
		scheme = "https"
	}

	var workers sync.WaitGroup

	workers.Go(func() { _ = components.Discovery.RunCleanup(ctx) })
	workers.Go(func() {
		if servesTLS {
			_ = server.ServeTLS(listener, "", "")
			return
		}

		_ = server.Serve(listener)
	})

	var once sync.Once

	stop := func() {
		once.Do(func() {
			cancel()

			shutdownCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()

			_ = server.Shutdown(shutdownCtx)
			workers.Wait()
		})
	}
	test.Cleanup(stop)

	return &beacon{components: components, address: listener.Addr().String(), scheme: scheme, stop: stop}
}

// newClient returns a client for server, failing the test if that is
// rejected.
func newClient(test *testing.T, server *beacon, options ...client.Option) *client.Client {
	test.Helper()

	beaconClient, err := client.New(server.url(), options...)
	if err != nil {
		test.Fatalf("new client: %v", err)
	}

	return beaconClient
}

// discover returns the instances named name, failing the test on error.
func discover(test *testing.T, beaconClient *client.Client, name string) []client.Instance {
	test.Helper()

	instances, err := beaconClient.Discover(test.Context(), name)
	if err != nil {
		test.Fatalf("discover %q: %v", name, err)
	}

	return instances
}

// eventually polls condition until it holds or timeout passes.
func eventually(test *testing.T, timeout time.Duration, what string, condition func() bool) {
	test.Helper()

	deadline := time.Now().Add(timeout)

	for !condition() {
		if time.Now().After(deadline) {
			test.Fatalf("timed out after %v waiting for %s", timeout, what)
		}

		time.Sleep(50 * time.Millisecond)
	}
}

// announce starts count leases for name concurrently.
func announce(test *testing.T, beaconClient *client.Client, name string, count int) []*client.Lease {
	test.Helper()

	leases := make([]*client.Lease, count)

	var group sync.WaitGroup

	for index := range count {
		group.Go(func() {
			lease, err := beaconClient.Announce(test.Context(), client.Registration{Name: name, Address: "127.0.0.1", Port: 9000 + index})
			if err != nil {
				test.Errorf("announce %d: %v", index, err)
				return
			}

			leases[index] = lease
		})
	}

	group.Wait()

	if test.Failed() {
		test.FailNow()
	}

	test.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		for _, lease := range leases {
			_ = lease.Close(ctx)
		}
	})

	return leases
}

// closeAll closes leases concurrently, failing the test on any error.
func closeAll(test *testing.T, leases []*client.Lease) {
	test.Helper()

	var group sync.WaitGroup

	for _, lease := range leases {
		group.Go(func() {
			if err := lease.Close(test.Context()); err != nil {
				test.Errorf("close lease %s: %v", lease.ID(), err)
			}
		})
	}

	group.Wait()
}

func TestLeasesOutliveTheTTLAndLeaveCleanly(test *testing.T) {
	test.Parallel()

	server := start(test, shortTTL(), "")
	beaconClient := newClient(test, server)

	const count = 40

	leases := announce(test, beaconClient, "orders", count)

	// Three TTLs: every instance would have expired without heartbeats.
	time.Sleep(3 * time.Second)

	if got := len(discover(test, beaconClient, "orders")); got != count {
		test.Fatalf("expected all %d leased instances to stay alive, got %d", count, got)
	}

	closeAll(test, leases[:count/2])

	remaining := discover(test, beaconClient, "orders")
	if len(remaining) != count/2 {
		test.Fatalf("expected %d instances after closing half, got %d", count/2, len(remaining))
	}

	for _, lease := range leases[count/2:] {
		if !slices.ContainsFunc(remaining, func(instance client.Instance) bool { return instance.ID == lease.ID() }) {
			test.Errorf("expected open lease %s to remain registered", lease.ID())
		}
	}

	closeAll(test, leases[count/2:])

	if got := len(discover(test, beaconClient, "orders")); got != 0 {
		test.Errorf("expected no instances after closing every lease, got %d", got)
	}
}

func TestLeasesRegisterAgainAfterBeaconRestarts(test *testing.T) {
	test.Parallel()

	first := start(test, shortTTL(), "")
	address := first.address
	beaconClient := newClient(test, first)

	const count = 10

	leases := announce(test, beaconClient, "payments", count)

	before := make(map[string]bool, count)
	for _, lease := range leases {
		before[lease.ID()] = true
	}

	// The in-memory registry forgets everything; the leases must notice
	// and register again on the new server at the same address.
	first.stop()
	start(test, shortTTL(), address)

	eventually(test, 15*time.Second, "every lease to register again", func() bool {
		instances, err := beaconClient.Discover(test.Context(), "payments")
		return err == nil && len(instances) == count
	})

	for _, lease := range leases {
		if before[lease.ID()] {
			test.Errorf("expected lease %s to have a new ID after registering again", lease.ID())
		}
	}

	closeAll(test, leases)
}

func TestDiscoverPagesConsistentlyWhileInstancesChurn(test *testing.T) {
	test.Parallel()

	server := start(test, settings(), "")
	beaconClient := newClient(test, server)
	ctx := test.Context()

	// Three and a half pages of instances that stay for the whole test,
	// kept alive by leases.
	const stable = 1750

	stableIDs := make(map[string]bool, stable)
	for _, lease := range announce(test, beaconClient, "orders", stable) {
		stableIDs[lease.ID()] = true
	}

	churnCtx, stopChurn := context.WithCancel(ctx)

	var churn sync.WaitGroup

	for worker := range 4 {
		churn.Go(func() {
			for churnCtx.Err() == nil {
				registered, err := beaconClient.Register(churnCtx, client.Registration{Name: "orders", Address: "127.0.0.1", Port: 20000 + worker})
				if err != nil {
					continue
				}

				_ = beaconClient.Unregister(churnCtx, registered.ID)
			}
		})
	}

	for round := range 10 {
		instances := discover(test, beaconClient, "orders")

		seen := make(map[string]bool, len(instances))
		for index, instance := range instances {
			if seen[instance.ID] {
				test.Fatalf("round %d: instance %s returned twice", round, instance.ID)
			}

			seen[instance.ID] = true

			if index > 0 && instances[index-1].ID >= instance.ID {
				test.Fatalf("round %d: instances out of order at %d", round, index)
			}
		}

		// Paging is a live view, but an instance present the whole time
		// must never be missed.
		for id := range stableIDs {
			if !seen[id] {
				test.Fatalf("round %d: stable instance %s missing", round, id)
			}
		}
	}

	stopChurn()
	churn.Wait()
}

// writeTokens writes a tokens file giving each name in tokens its token, with
// the default permissions.
func writeTokens(test *testing.T, path string, tokens map[string]string) {
	test.Helper()

	var content string

	for name, token := range tokens {
		hash, err := auth.HashToken(token)
		if err != nil {
			test.Fatalf("hash token: %v", err)
		}

		content += fmt.Sprintf("[[tokens]]\nname = %q\nsha256 = %q\n\n", name, hash)
	}

	// Write and rename, as a careful operator or a secret mount would, so
	// a reload never reads a half-written file.
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(content), 0o600); err != nil {
		test.Fatalf("write tokens file: %v", err)
	}

	if err := os.Rename(temporary, path); err != nil {
		test.Fatalf("replace tokens file: %v", err)
	}
}

func TestTokenRotationKeepsOwnership(test *testing.T) {
	test.Parallel()

	tokensFile := filepath.Join(test.TempDir(), "tokens.toml")
	writeTokens(test, tokensFile, map[string]string{"orders": "old-orders-token-0123456789"})

	serverSettings := settings()
	serverSettings.Auth = config.AuthConfig{Enabled: true, TokensFile: tokensFile}

	server := start(test, serverSettings, "")
	oldClient := newClient(test, server, client.WithToken("old-orders-token-0123456789"))
	newClientWithToken := newClient(test, server, client.WithToken("new-orders-token-0123456789"))

	registered, err := oldClient.Register(test.Context(), client.Registration{Name: "orders", Address: "127.0.0.1", Port: 8081})
	if err != nil {
		test.Fatalf("register with the old token: %v", err)
	}

	if _, err := newClientWithToken.Discover(test.Context(), "orders"); !errors.Is(err, client.ErrUnauthorized) {
		test.Fatalf("expected the new token to be refused before the reload, got %v", err)
	}

	// Rotate: the same name, a new token, the old one gone.
	writeTokens(test, tokensFile, map[string]string{"orders": "new-orders-token-0123456789"})

	if err := server.components.Credentials.Reload(); err != nil {
		test.Fatalf("reload: %v", err)
	}

	if _, err := oldClient.Discover(test.Context(), "orders"); !errors.Is(err, client.ErrUnauthorized) {
		test.Errorf("expected the old token to stop working at once, got %v", err)
	}

	// The instance belongs to the name, so the new token may keep it alive
	// and remove it.
	if err := newClientWithToken.Heartbeat(test.Context(), registered.ID); err != nil {
		test.Errorf("expected the rotated token to send heartbeats for its instance, got %v", err)
	}

	if err := newClientWithToken.Unregister(test.Context(), registered.ID); err != nil {
		test.Errorf("expected the rotated token to unregister its instance, got %v", err)
	}
}

func TestCertificateRotationReachesNewConnections(test *testing.T) {
	test.Parallel()

	first := tlstest.WriteCertificate(test)

	serverSettings := settings()
	serverSettings.TLS = config.TLSConfig{CertFile: first.CertFile, KeyFile: first.KeyFile}

	server := start(test, serverSettings, "")

	second := tlstest.WriteCertificate(test)
	trusted := first.Pool.Clone()
	trusted.AppendCertsFromPEM(mustRead(test, second.CertFile))
	tlsConfig := &tls.Config{RootCAs: trusted, MinVersion: tls.VersionTLS12}

	// A connection opened before the rotation, kept open across it.
	dialer := &tls.Dialer{Config: tlsConfig}

	open, err := dialer.DialContext(test.Context(), "tcp", server.address)
	if err != nil {
		test.Fatalf("dial: %v", err)
	}
	defer open.Close()

	if served := open.(*tls.Conn).ConnectionState().PeerCertificates[0].Raw; !slices.Equal(served, first.KeyPair.Leaf.Raw) {
		test.Fatal("expected the first certificate before the rotation")
	}

	// Replace the files in place and reload, as a certificate renewal does.
	for source, target := range map[string]string{second.CertFile: first.CertFile, second.KeyFile: first.KeyFile} {
		if err := os.WriteFile(target, mustRead(test, source), 0o600); err != nil {
			test.Fatalf("replace %s: %v", target, err)
		}
	}

	if err := server.components.Credentials.Reload(); err != nil {
		test.Fatalf("reload: %v", err)
	}

	// The open connection still serves requests, with the certificate it
	// was made with.
	if _, err := io.WriteString(open, "GET /health HTTP/1.1\r\nHost: beacon\r\n\r\n"); err != nil {
		test.Fatalf("write on the open connection: %v", err)
	}

	response, err := http.ReadResponse(bufio.NewReader(open), nil)
	if err != nil {
		test.Fatalf("read on the open connection: %v", err)
	}
	_ = response.Body.Close()

	if response.StatusCode != http.StatusOK {
		test.Errorf("expected the open connection to keep serving, got %d", response.StatusCode)
	}

	// Every new connection gets the new certificate.
	httpClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsConfig, DisableKeepAlives: true},
		Timeout:   5 * time.Second,
	}

	health, err := httpClient.Get(server.url() + "/health")
	if err != nil {
		test.Fatalf("health on a new connection: %v", err)
	}
	_ = health.Body.Close()

	if !slices.Equal(health.TLS.PeerCertificates[0].Raw, second.KeyPair.Leaf.Raw) {
		test.Error("expected a new connection to get the new certificate")
	}

	beaconClient := newClient(test, server, client.WithHTTPClient(httpClient))
	if _, err := beaconClient.Register(test.Context(), client.Registration{Name: "orders", Address: "127.0.0.1", Port: 8081}); err != nil {
		test.Errorf("expected the client to work over the new certificate, got %v", err)
	}
}

// mustRead returns the contents of path.
func mustRead(test *testing.T, path string) []byte {
	test.Helper()

	contents, err := os.ReadFile(path)
	if err != nil {
		test.Fatalf("read %s: %v", path, err)
	}

	return contents
}
