package credentials

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nm1996/beacon/internal/auth"
	"github.com/nm1996/beacon/internal/config"
	"github.com/nm1996/beacon/internal/tlstest"
)

const (
	firstToken  = "first-token-0123456789"
	secondToken = "second-token-0123456789"
)

// tokensFor returns a tokens file accepting token under name.
func tokensFor(test *testing.T, name, token string) string {
	test.Helper()

	hash, err := auth.HashToken(token)
	if err != nil {
		test.Fatalf("hash token: %v", err)
	}

	return "[[tokens]]\nname = \"" + name + "\"\nsha256 = \"" + hash + "\"\n"
}

// writeFile writes contents to path, failing the test on error.
func writeFile(test *testing.T, path string, contents []byte) {
	test.Helper()

	if err := os.WriteFile(path, contents, 0o600); err != nil {
		test.Fatalf("write %s: %v", path, err)
	}
}

// copyFile copies the file at source to target.
func copyFile(test *testing.T, source, target string) {
	test.Helper()

	contents, err := os.ReadFile(source)
	if err != nil {
		test.Fatalf("read %s: %v", source, err)
	}

	writeFile(test, target, contents)
}

// results records the reload results a Store reports.
type results struct {
	mu   sync.Mutex
	seen []string
	next chan string
}

func newResults() *results {
	return &results{next: make(chan string, 16)}
}

func (recorded *results) record(result string) {
	recorded.mu.Lock()
	recorded.seen = append(recorded.seen, result)
	recorded.mu.Unlock()

	recorded.next <- result
}

func (recorded *results) all() string {
	recorded.mu.Lock()
	defer recorded.mu.Unlock()

	return strings.Join(recorded.seen, ",")
}

// tokenStore returns a Store with only authentication enabled, reading the
// tokens file it returns the path of, initially accepting firstToken.
func tokenStore(test *testing.T, logger *slog.Logger) (*Store, string, *results) {
	test.Helper()

	path := filepath.Join(test.TempDir(), "tokens.toml")
	writeFile(test, path, []byte(tokensFor(test, "first", firstToken)))

	recorded := newResults()

	store, err := New(config.TLSConfig{}, config.AuthConfig{Enabled: true, TokensFile: path}, logger, recorded.record)
	if err != nil {
		test.Fatalf("new store: %v", err)
	}

	return store, path, recorded
}

var discard = slog.New(slog.DiscardHandler)

func TestNewReturnsNilWithNothingToLoad(test *testing.T) {
	store, err := New(config.TLSConfig{}, config.AuthConfig{}, discard, nil)

	if store != nil || err != nil {
		test.Errorf("expected no store and no error, got %v, %v", store, err)
	}
}

func TestReloadSwapsTokens(test *testing.T) {
	store, path, recorded := tokenStore(test, discard)

	if _, ok := store.Authenticate(firstToken); !ok {
		test.Fatal("expected the first token before reloading")
	}

	writeFile(test, path, []byte(tokensFor(test, "second", secondToken)))

	if err := store.Reload(); err != nil {
		test.Fatalf("reload: %v", err)
	}

	if _, ok := store.Authenticate(firstToken); ok {
		test.Error("expected the first token to be revoked by the reload")
	}

	if identity, ok := store.Authenticate(secondToken); !ok || identity.Name != "second" {
		test.Errorf("expected the second token after reloading, got %q, %t", identity.Name, ok)
	}

	if got := recorded.all(); got != ReloadOK {
		test.Errorf("expected one successful reload to be reported, got %q", got)
	}
}

func TestFailedReloadKeepsCredentialsInUse(test *testing.T) {
	var logs bytes.Buffer

	store, path, recorded := tokenStore(test, slog.New(slog.NewTextHandler(&logs, nil)))

	for name, contents := range map[string]string{
		"invalid TOML":     "[[tokens]",
		"invalid entry":    "[[tokens]]\nname = \"broken\"\nsha256 = \"nothex\"\n",
		"no tokens at all": "",
	} {
		writeFile(test, path, []byte(contents))

		if err := store.Reload(); err == nil {
			test.Errorf("%s: expected the reload to fail", name)
		}

		if _, ok := store.Authenticate(firstToken); !ok {
			test.Errorf("%s: expected the first token to keep working", name)
		}
	}

	if err := os.Remove(path); err != nil {
		test.Fatalf("remove tokens file: %v", err)
	}

	if err := store.Reload(); err == nil {
		test.Error("missing file: expected the reload to fail")
	}

	if _, ok := store.Authenticate(firstToken); !ok {
		test.Error("missing file: expected the first token to keep working")
	}

	if got := recorded.all(); got != "error,error,error,error" {
		test.Errorf("expected four failed reloads to be reported, got %q", got)
	}

	if !strings.Contains(logs.String(), "keeping the files already in use") {
		test.Errorf("expected failures to be logged, logs:\n%s", logs.String())
	}
}

func TestReloadIfChangedOnlyActsOnNewContents(test *testing.T) {
	store, path, recorded := tokenStore(test, discard)

	store.reloadIfChanged()
	if got := recorded.all(); got != "" {
		test.Fatalf("expected no reload while the file is unchanged, got %q", got)
	}

	writeFile(test, path, []byte(tokensFor(test, "second", secondToken)))
	store.reloadIfChanged()
	store.reloadIfChanged()

	if got := recorded.all(); got != ReloadOK {
		test.Fatalf("expected exactly one reload for one change, got %q", got)
	}

	if _, ok := store.Authenticate(secondToken); !ok {
		test.Error("expected the changed file to be in use")
	}

	// A broken file is reported once, not on every check, and a fix after
	// it is picked up.
	writeFile(test, path, []byte("[[tokens]"))
	store.reloadIfChanged()
	store.reloadIfChanged()

	if err := os.Remove(path); err != nil {
		test.Fatalf("remove tokens file: %v", err)
	}

	store.reloadIfChanged()
	store.reloadIfChanged()

	writeFile(test, path, []byte(tokensFor(test, "first", firstToken)))
	store.reloadIfChanged()

	if got := recorded.all(); got != "ok,error,error,ok" {
		test.Errorf("expected each new state to be tried once, got %q", got)
	}

	if _, ok := store.Authenticate(firstToken); !ok {
		test.Error("expected the fixed file to be in use")
	}
}

func TestRunReloadsOnSignalAndOnChange(test *testing.T) {
	store, path, recorded := tokenStore(test, discard)

	ctx, cancel := context.WithCancel(test.Context())
	signals := make(chan os.Signal, 1)
	stopped := make(chan struct{})

	go func() {
		store.Run(ctx, signals, 10*time.Millisecond)
		close(stopped)
	}()

	await := func(want string) {
		test.Helper()

		select {
		case got := <-recorded.next:
			if got != want {
				test.Fatalf("expected a %q reload, got %q", want, got)
			}
		case <-time.After(5 * time.Second):
			test.Fatalf("timed out waiting for a %q reload", want)
		}
	}

	writeFile(test, path, []byte(tokensFor(test, "second", secondToken)))
	await(ReloadOK)

	if _, ok := store.Authenticate(secondToken); !ok {
		test.Error("expected the interval check to load the changed file")
	}

	signals <- os.Interrupt
	await(ReloadOK)

	cancel()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		test.Fatal("expected Run to return once its context is done")
	}
}

// tlsStore returns a Store serving the server certificate whose files it
// copies to the paths in the returned settings, with client certificates
// from clientCA accepted when clientCA is not nil.
func tlsStore(test *testing.T, server tlstest.Certificate, clientCA *tlstest.Authority, recorded *results) (*Store, config.TLSConfig) {
	test.Helper()

	directory := test.TempDir()
	settings := config.TLSConfig{
		CertFile: filepath.Join(directory, "cert.pem"),
		KeyFile:  filepath.Join(directory, "key.pem"),
	}

	copyFile(test, server.CertFile, settings.CertFile)
	copyFile(test, server.KeyFile, settings.KeyFile)

	authSettings := config.AuthConfig{}

	if clientCA != nil {
		settings.ClientCAFile = filepath.Join(directory, "clients.pem")
		copyFile(test, clientCA.CertFile, settings.ClientCAFile)

		authSettings = config.AuthConfig{Enabled: true, TokensFile: filepath.Join(directory, "tokens.toml")}
		writeFile(test, authSettings.TokensFile, []byte("[[certificates]]\nname = \"orders\"\ndns = \"orders.internal\"\n"))
	}

	var callback func(string)
	if recorded != nil {
		callback = recorded.record
	}

	store, err := New(settings, authSettings, discard, callback)
	if err != nil {
		test.Fatalf("new store: %v", err)
	}

	return store, settings
}

// serveTLS accepts TLS connections with store's server config on a
// loopback port until the test ends, completing each handshake, and
// returns the address.
func serveTLS(test *testing.T, store *Store) string {
	test.Helper()

	listener, err := tls.Listen("tcp", "127.0.0.1:0", store.ServerTLSConfig())
	if err != nil {
		test.Fatalf("listen: %v", err)
	}

	test.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}

			go func() {
				defer connection.Close()

				if tlsConnection, ok := connection.(*tls.Conn); ok {
					_ = tlsConnection.Handshake()
				}

				_, _ = io.Copy(io.Discard, connection)
			}()
		}
	}()

	return listener.Addr().String()
}

// dial opens a TLS connection to address that trusts roots and presents
// certificates, and returns the server's leaf certificate serial number.
func dial(test *testing.T, address string, roots *tlstest.Authority, certificates ...tls.Certificate) (string, error) {
	test.Helper()

	tlsConfig := &tls.Config{ServerName: "localhost", RootCAs: roots.Pool, Certificates: certificates, MinVersion: tls.VersionTLS12}

	connection, err := tls.Dial("tcp", address, tlsConfig)
	if err != nil {
		return "", err
	}
	defer connection.Close()

	// With TLS 1.3 the server reports a rejected client certificate only
	// after the client's side of the handshake is done, so read once to
	// see it.
	if err := connection.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		return "", err
	}

	if _, err := connection.Read(make([]byte, 1)); err != nil {
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() {
			return "", err
		}
	}

	return connection.ConnectionState().PeerCertificates[0].SerialNumber.String(), nil
}

func TestReloadRotatesServerCertificateWithoutRestart(test *testing.T) {
	authority := tlstest.NewAuthority(test)
	first := authority.IssueServer(test)
	second := authority.IssueServer(test)

	store, settings := tlsStore(test, first, nil, nil)
	address := serveTLS(test, store)

	before, err := dial(test, address, authority)
	if err != nil {
		test.Fatalf("dial before reload: %v", err)
	}

	if before != first.KeyPair.Leaf.SerialNumber.String() {
		test.Fatalf("expected the first certificate before reloading")
	}

	copyFile(test, second.CertFile, settings.CertFile)
	copyFile(test, second.KeyFile, settings.KeyFile)

	if err := store.Reload(); err != nil {
		test.Fatalf("reload: %v", err)
	}

	after, err := dial(test, address, authority)
	if err != nil {
		test.Fatalf("dial after reload: %v", err)
	}

	if after != second.KeyPair.Leaf.SerialNumber.String() {
		test.Errorf("expected the second certificate after reloading, on the same listener")
	}
}

func TestReloadRejectsMismatchedKeyAndKeepsServing(test *testing.T) {
	authority := tlstest.NewAuthority(test)
	first := authority.IssueServer(test)
	other := authority.IssueServer(test)

	recorded := newResults()
	store, settings := tlsStore(test, first, nil, recorded)
	address := serveTLS(test, store)

	// A rotation caught halfway: the new certificate is in place, but its
	// key is not.
	copyFile(test, other.CertFile, settings.CertFile)

	err := store.Reload()
	if err == nil || !strings.Contains(err.Error(), "load TLS certificate") {
		test.Fatalf("expected a key mismatch error, got %v", err)
	}

	serial, err := dial(test, address, authority)
	if err != nil {
		test.Fatalf("expected the server to keep serving, got %v", err)
	}

	if serial != first.KeyPair.Leaf.SerialNumber.String() {
		test.Error("expected the first certificate to stay in use")
	}

	if got := recorded.all(); got != ReloadError {
		test.Errorf("expected one failed reload, got %q", got)
	}
}

func TestReloadSwitchesClientCA(test *testing.T) {
	serverAuthority := tlstest.NewAuthority(test)
	oldClients := tlstest.NewAuthority(test)
	newClients := tlstest.NewAuthority(test)

	store, settings := tlsStore(test, serverAuthority.IssueServer(test), oldClients, nil)
	address := serveTLS(test, store)

	oldClient := oldClients.IssueClient(test, []string{"orders.internal"}).KeyPair
	newClient := newClients.IssueClient(test, []string{"orders.internal"}).KeyPair

	if _, err := dial(test, address, serverAuthority, oldClient); err != nil {
		test.Fatalf("expected the old CA's client before reloading, got %v", err)
	}

	if _, err := dial(test, address, serverAuthority, newClient); err == nil {
		test.Fatal("expected the new CA's client to be refused before reloading")
	}

	copyFile(test, newClients.CertFile, settings.ClientCAFile)

	if err := store.Reload(); err != nil {
		test.Fatalf("reload: %v", err)
	}

	if _, err := dial(test, address, serverAuthority, newClient); err != nil {
		test.Errorf("expected the new CA's client after reloading, got %v", err)
	}

	if _, err := dial(test, address, serverAuthority, oldClient); err == nil {
		test.Error("expected the old CA's client to be refused after reloading")
	}
}

func TestReloadKeepsTLSWhenTokensFileBreaks(test *testing.T) {
	serverAuthority := tlstest.NewAuthority(test)
	clients := tlstest.NewAuthority(test)
	first := serverAuthority.IssueServer(test)
	second := serverAuthority.IssueServer(test)

	store, settings := tlsStore(test, first, clients, nil)
	address := serveTLS(test, store)

	// The certificate is rotated correctly, but the tokens file is broken
	// in the same reload: nothing may change.
	copyFile(test, second.CertFile, settings.CertFile)
	copyFile(test, second.KeyFile, settings.KeyFile)
	writeFile(test, store.authSettings.TokensFile, []byte("[[tokens]]\nname = \"x\"\n"))

	if err := store.Reload(); err == nil {
		test.Fatal("expected the reload to fail")
	}

	serial, err := dial(test, address, serverAuthority)
	if err != nil {
		test.Fatalf("dial: %v", err)
	}

	if serial != first.KeyPair.Leaf.SerialNumber.String() {
		test.Error("expected the first certificate to stay in use when another file is broken")
	}
}
