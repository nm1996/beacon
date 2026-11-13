// Package credentials holds the files Beacon proves and checks identity
// with, the TLS certificate and key, the client CA bundle and the tokens
// file, and reloads them while Beacon runs.
//
// A Store loads every file once at startup and fails if any is unusable.
// Later reloads, triggered by SIGHUP or by noticing that a file changed,
// replace everything at once or nothing: a reload that fails anywhere keeps
// the credentials already in use, so a half-written file can never lock
// clients out. New TLS handshakes and every request after a successful
// reload see the new files; connections that are already open keep the
// certificate they were made with.
package credentials

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nm1996/beacon/internal/auth"
	"github.com/nm1996/beacon/internal/config"
)

// Reload results, passed to the Store's reload callback and used as the
// result label of beacon_credential_reloads_total.
const (
	ReloadOK    = "ok"
	ReloadError = "error"
)

// Store holds the TLS configuration and identities currently in use. It is
// safe for concurrent use: handshakes and requests read it while Run
// reloads it. Both are published together as one snapshot, so a reader
// never pairs the TLS configuration of one reload with the identities of
// another.
type Store struct {
	tlsSettings  config.TLSConfig
	authSettings config.AuthConfig
	logger       *slog.Logger
	reloaded     func(result string)

	current atomic.Pointer[snapshot]

	// mu serializes reloads and guards the fields below.
	mu sync.Mutex
	// fingerprint identifies the contents of the files in use.
	fingerprint [sha256.Size]byte
	// failed identifies the last contents, or the last read error, that a
	// reload rejected, so an unchanged broken file is not retried, or
	// reported, on every check.
	failedFingerprint [sha256.Size]byte
	failedRead        string
}

// New loads the files named by tlsSettings, when TLS is enabled, and
// authSettings, when authentication is enabled, and returns a Store
// holding them. It returns nil and no error when neither is enabled, since
// there is then nothing to load. reloaded, if not nil, is called with
// ReloadOK or ReloadError after every later reload attempt.
func New(tlsSettings config.TLSConfig, authSettings config.AuthConfig, logger *slog.Logger, reloaded func(result string)) (*Store, error) {
	if !tlsSettings.Enabled() && !authSettings.Enabled {
		return nil, nil
	}

	if reloaded == nil {
		reloaded = func(string) {}
	}

	store := &Store{tlsSettings: tlsSettings, authSettings: authSettings, logger: logger, reloaded: reloaded}

	files, err := store.read()
	if err != nil {
		return nil, err
	}

	loaded, err := store.build(files)
	if err != nil {
		return nil, err
	}

	store.use(loaded)

	return store, nil
}

// ServerTLSConfig returns the TLS configuration for the HTTP server, or nil
// when TLS is not enabled. It holds no certificate itself: every handshake
// asks the Store for the configuration in use at that moment.
func (store *Store) ServerTLSConfig() *tls.Config {
	if !store.tlsSettings.Enabled() {
		return nil
	}

	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			return store.current.Load().handshake, nil
		},
	}
}

// Authenticate returns the identity token belongs to, using the tokens file
// in use. It must only be called when authentication is enabled.
func (store *Store) Authenticate(token string) (auth.Identity, bool) {
	return store.current.Load().identities.Authenticate(token)
}

// AuthenticateCertificate returns the identity certificate belongs to,
// using the tokens file in use. It must only be called when authentication
// is enabled.
func (store *Store) AuthenticateCertificate(certificate *x509.Certificate) (auth.Identity, bool) {
	return store.current.Load().identities.AuthenticateCertificate(certificate)
}

// Reload reads every file again and starts using them if all of them are
// usable. Otherwise it logs why, keeps the credentials in use, and returns
// the error.
func (store *Store) Reload() error {
	store.mu.Lock()
	defer store.mu.Unlock()

	return store.reload("signal", nil, nil)
}

// Run reloads the files whenever signals delivers a value and, when
// interval is positive, whenever a check every interval finds that their
// contents changed. It returns when ctx is done.
func (store *Store) Run(ctx context.Context, signals <-chan os.Signal, interval time.Duration) {
	var ticks <-chan time.Time

	if interval > 0 {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		ticks = ticker.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-signals:
			_ = store.Reload() // Reload logs its own failures.
		case <-ticks:
			store.reloadIfChanged()
		}
	}
}

// reloadIfChanged reloads when the files' contents differ from those in use
// and from the last contents a reload rejected.
func (store *Store) reloadIfChanged() {
	store.mu.Lock()
	defer store.mu.Unlock()

	files, err := store.read()
	if err != nil {
		if err.Error() == store.failedRead {
			return
		}

		_ = store.reload("file change", nil, err)

		return
	}

	if files.fingerprint == store.fingerprint || files.fingerprint == store.failedFingerprint {
		return
	}

	_ = store.reload("file change", &files, nil)
}

// reload loads files, or reads them first when files is nil, and swaps
// them in if they are all usable. readErr is a read failure the caller
// already hit. store.mu must be held.
func (store *Store) reload(trigger string, files *fileSet, readErr error) error {
	if files == nil && readErr == nil {
		read, err := store.read()
		files, readErr = &read, err
	}

	if readErr != nil {
		store.failedRead = readErr.Error()
		return store.rejected(trigger, readErr)
	}

	loaded, err := store.build(*files)
	if err != nil {
		store.failedRead = ""
		store.failedFingerprint = files.fingerprint

		return store.rejected(trigger, err)
	}

	store.use(loaded)
	store.failedRead = ""
	store.failedFingerprint = [sha256.Size]byte{}
	store.reloaded(ReloadOK)
	store.logger.Info("credentials reloaded",
		slog.String("trigger", trigger),
		slog.Uint64("generation", store.current.Load().generation),
	)

	return nil
}

// rejected reports a failed reload and returns err.
func (store *Store) rejected(trigger string, err error) error {
	store.reloaded(ReloadError)
	store.logger.Error("credentials reload failed; keeping the files already in use",
		slog.String("trigger", trigger), slog.Any("error", err))

	return err
}

// fileSet is the raw contents of every file the Store uses, with a
// fingerprint of all of them together.
type fileSet struct {
	cert, key, clientCA, tokens []byte
	fingerprint                 [sha256.Size]byte
}

// loaded is what a fileSet becomes once every file has been parsed.
type loaded struct {
	handshake   *tls.Config
	identities  *auth.Identities
	fingerprint [sha256.Size]byte
}

// read reads every configured file. The same bytes are both fingerprinted
// and parsed, so a file replaced between two reads cannot mix versions.
func (store *Store) read() (fileSet, error) {
	var (
		files    fileSet
		problems []error
	)

	// The os errors already name the file, so the prefix only says what
	// the file is for.
	readFile := func(target *[]byte, prefix, path string) {
		if path == "" {
			return
		}

		contents, err := os.ReadFile(path) //nolint:gosec // Paths are the operator's own TLS and auth settings.
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", prefix, err))
			return
		}

		*target = contents
	}

	if store.tlsSettings.Enabled() {
		readFile(&files.cert, "load TLS certificate", store.tlsSettings.CertFile)
		readFile(&files.key, "load TLS certificate key", store.tlsSettings.KeyFile)
		readFile(&files.clientCA, "load TLS client CA file", store.tlsSettings.ClientCAFile)
	}

	if store.authSettings.Enabled {
		readFile(&files.tokens, "read tokens file", store.authSettings.TokensFile)
	}

	if err := errors.Join(problems...); err != nil {
		return fileSet{}, err
	}

	hash := sha256.New()
	for _, contents := range [][]byte{files.cert, files.key, files.clientCA, files.tokens} {
		// Length-prefix each file, so moving bytes from one file to the
		// next cannot produce the same fingerprint.
		fmt.Fprintf(hash, "%d:", len(contents))
		hash.Write(contents)
	}

	copy(files.fingerprint[:], hash.Sum(nil))

	return files, nil
}

// build parses files into the TLS configuration and identities to use,
// applying the same checks as at startup.
func (store *Store) build(files fileSet) (loaded, error) {
	result := loaded{fingerprint: files.fingerprint}

	if store.tlsSettings.Enabled() {
		handshake, err := store.buildTLS(files)
		if err != nil {
			return loaded{}, err
		}

		result.handshake = handshake
	}

	if store.authSettings.Enabled {
		identities, err := auth.Parse(files.tokens, store.authSettings.TokensFile)
		if err != nil {
			return loaded{}, err
		}

		switch {
		case store.tlsSettings.ClientCAFile != "" && !identities.HasCertificates():
			return loaded{}, fmt.Errorf("tls.client_ca_file is set but tokens file %s has no [[certificates]] entries, so no client certificate could be used",
				store.authSettings.TokensFile)
		case store.tlsSettings.ClientCAFile == "" && identities.HasCertificates():
			store.logger.Warn("tokens file lists [[certificates]] but tls.client_ca_file is not set, so client certificates are not accepted",
				slog.String("tokens_file", store.authSettings.TokensFile))
		}

		result.identities = identities
	}

	return result, nil
}

// buildTLS builds the configuration every handshake uses. With a client CA
// bundle it asks clients for a certificate and verifies any they send, but
// does not require one, so /health stays reachable and bearer tokens keep
// working; the API's guard decides per route whether a caller is known.
func (store *Store) buildTLS(files fileSet) (*tls.Config, error) {
	settings := store.tlsSettings

	certificate, err := tls.X509KeyPair(files.cert, files.key)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate %s and key %s: %w", settings.CertFile, settings.KeyFile, err)
	}

	handshake := &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
		// http.Server.ServeTLS sets these on the server's own config, but
		// a config returned by GetConfigForClient replaces that one for the
		// handshake, so without them HTTP/2 would silently stop.
		NextProtos: []string{"h2", "http/1.1"},
	}

	if settings.ClientCAFile != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(files.clientCA) {
			return nil, fmt.Errorf("load TLS client CA file %s: no PEM certificates found", settings.ClientCAFile)
		}

		handshake.ClientCAs = pool
		handshake.ClientAuth = tls.VerifyClientCertIfGiven
	}

	return handshake, nil
}

// use makes result the credentials in use. store.mu must be held, except
// in New, before the Store is shared.
func (store *Store) use(result loaded) {
	var generation uint64
	if previous := store.current.Load(); previous != nil {
		generation = previous.generation + 1
	}

	// One Store publishes the TLS configuration and the identities that
	// were built from the same files, in a single step.
	store.current.Store(&snapshot{
		handshake:  result.handshake,
		identities: result.identities,
		generation: generation,
	})

	store.fingerprint = result.fingerprint
}

// snapshot is one generation of credentials: the TLS configuration and the
// identities built from one read of the files. It is never changed after it
// is published; a reload publishes a new one.
type snapshot struct {
	// handshake is nil when TLS is not enabled.
	handshake *tls.Config
	// identities is nil when authentication is not enabled.
	identities *auth.Identities
	// generation counts successful loads, starting at 0, so tests and logs
	// can tell snapshots apart.
	generation uint64
}
