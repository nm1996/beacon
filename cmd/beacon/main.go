// Command beacon runs the Beacon service registry as an HTTP server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"

	"github.com/nm1996/beacon/internal/bootstrap"
	"github.com/nm1996/beacon/internal/config"
)

// version is the release version, set when release binaries are built with
//
//	go build -ldflags "-X main.version=v0.1.0" ./cmd/beacon
//
// It has to be a package-level variable because that is the only kind
// -ldflags -X can set, and nothing assigns it at run time. Left empty, the
// version comes from the build info Go embeds instead.
var version string

// main loads the configuration from the file and environment, then runs the
// server until it fails or is told to stop, exiting with a non-zero status on
// failure. With --version it prints the build version instead, and with
// --health-check it asks an already running Beacon for its health, which is
// what the container health check uses since the image has no shell or curl.
// With --hash-token it prints the hash of the token on standard input for a
// tokens file.
func main() {
	configPath := flag.String("config", "", "path to a TOML configuration file; BEACON_* environment variables override it, and built-in defaults fill the rest")
	showVersion := flag.Bool("version", false, "print the version and exit")
	healthCheck := flag.Bool("health-check", false, "ask the Beacon configured by --config whether it is healthy, then exit 0 if it is and 1 if not")
	hashTokenFlag := flag.Bool("hash-token", false, "read a bearer token from standard input and print its SHA-256 hash for the tokens file, then exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(buildVersion())
		return
	}

	if *hashTokenFlag {
		if err := hashToken(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		return
	}

	settings, err := config.Load(*configPath)
	if err != nil {
		// No configured logger exists yet, so report in the default format.
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("beacon failed to start", slog.Any("error", err))
		os.Exit(1)
	}

	if *healthCheck {
		if err := checkHealth(context.Background(), healthURL(settings)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		return
	}

	logger := newLogger(os.Stdout, settings.Logging)

	if err := run(settings, logger); err != nil {
		logger.Error("beacon stopped with an error", slog.Any("error", err))
		os.Exit(1)
	}
}

// newLogger builds the logger described by settings. Load has already
// validated the level and format, so neither can be unknown here.
func newLogger(output io.Writer, settings config.LoggingConfig) *slog.Logger {
	level, _ := settings.SlogLevel()
	options := &slog.HandlerOptions{Level: level}

	if settings.Format == "text" {
		return slog.New(slog.NewTextHandler(output, options))
	}

	return slog.New(slog.NewJSONHandler(output, options))
}

// run binds the listen address, starts the HTTP server, the cleanup worker
// and, when TLS or authentication is on, the credentials reload worker,
// which reloads on SIGHUP and every server.reload_interval. It then blocks
// until the server fails or the process gets SIGINT or SIGTERM. On a signal
// it gives in-flight requests up to server.shutdown_timeout to finish; a
// second signal during that time ends the process at once. The workers are
// always stopped and waited for before run returns.
func run(settings config.Config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	components, err := bootstrap.New(settings, logger, buildVersion())
	if err != nil {
		return err
	}

	server := components.Server

	// Bind before starting anything else, so a port that is already in use
	// fails startup cleanly instead of after "listening" has been logged.
	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(ctx, "tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", server.Addr, err)
	}

	workerCtx, stopWorkers := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() {
		stopWorkers()
		workers.Wait()
	}()

	workers.Go(func() {
		if err := components.Discovery.RunCleanup(workerCtx); err != nil {
			logger.Error("cleanup worker stopped", slog.Any("error", err))
		}
	})

	if store := components.Credentials; store != nil {
		// SIGHUP never arrives on Windows; server.reload_interval covers it.
		reloadSignals := make(chan os.Signal, 1)
		signal.Notify(reloadSignals, syscall.SIGHUP)
		defer signal.Stop(reloadSignals)

		workers.Go(func() {
			store.Run(workerCtx, reloadSignals, settings.Server.ReloadInterval)
		})
	}

	// Read this before serving: Serve fills in a nil TLSConfig while it sets
	// up HTTP/2, so afterwards it is neither meaningful nor safe to read.
	servesTLS := server.TLSConfig != nil

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- serve(server, listener, servesTLS)
	}()

	logListening(logger, listener.Addr(), servesTLS, settings.Auth.Enabled, settings.Storage.Backend)

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	// Restore default signal handling, so a second Ctrl+C or SIGTERM stops
	// the process immediately instead of waiting for the graceful shutdown.
	stop()

	logger.Info("beacon shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), settings.Server.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}

	return nil
}

// serve runs server on listener until it stops, over TLS when servesTLS is
// set. ServeTLS uses the certificate bootstrap already loaded into the
// server's TLSConfig, and also offers HTTP/2.
func serve(server *http.Server, listener net.Listener, servesTLS bool) error {
	if servesTLS {
		return server.ServeTLS(listener, "", "")
	}

	return server.Serve(listener)
}

// logListening reports that Beacon is serving on address, and warns when it
// serves plain HTTP to anything beyond this host, more urgently when
// clients send bearer tokens over it.
func logListening(logger *slog.Logger, address net.Addr, servesTLS, authEnabled bool, storage string) {
	logger.Info("beacon listening",
		slog.String("addr", address.String()),
		slog.Bool("tls", servesTLS),
		slog.Bool("auth", authEnabled),
		slog.String("storage", storage),
		slog.String("version", buildVersion()),
	)

	if servesTLS || isLoopback(address) {
		return
	}

	message := "beacon serves plain HTTP on a non-loopback address; set tls.cert_file and tls.key_file to serve HTTPS"
	if authEnabled {
		message = "beacon serves plain HTTP on a non-loopback address, so bearer tokens cross the network unencrypted; " +
			"set tls.cert_file and tls.key_file to serve HTTPS, unless a TLS proxy sits in front of Beacon"
	}

	logger.Warn(message, slog.String("addr", address.String()))
}

// isLoopback reports whether address only accepts connections from this
// host. A wildcard address such as 0.0.0.0 or [::] is not loopback.
func isLoopback(address net.Addr) bool {
	tcpAddress, ok := address.(*net.TCPAddr)

	return ok && tcpAddress.IP.IsLoopback()
}

// buildVersion returns the version set at link time, if any. Otherwise it
// returns what the Go toolchain stamped into the binary: a release tag such
// as v0.1.0, or a pseudo-version built from the git commit. Binaries built
// without either, such as inside a container without .git, report "(devel)".
func buildVersion() string {
	if version != "" {
		return version
	}

	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "unknown"
	}

	return info.Main.Version
}
