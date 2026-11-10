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
	"github.com/nm1996/beacon/internal/registry"
)

// main loads the configuration from the file and environment, then runs the server until it fails or is
// told to stop, exiting with a non-zero status on failure. With --version it
// prints the build version and exits instead.
func main() {
	configPath := flag.String("config", "", "path to a TOML configuration file; BEACON_* environment variables override it, and built-in defaults fill the rest")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(buildVersion())
		return
	}

	settings, err := config.Load(*configPath)
	if err != nil {
		// No configured logger exists yet, so report in the default format.
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("beacon failed to start", slog.Any("error", err))
		os.Exit(1)
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

// run binds the listen address, starts the HTTP server and the cleanup
// worker, then blocks until the server fails or the process gets SIGINT or
// SIGTERM. On a signal it gives in-flight requests up to
// server.shutdown_timeout to finish; a second signal during that time ends
// the process at once. The cleanup worker is always stopped and waited for
// before run returns.
func run(settings config.Config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	components := bootstrap.New(settings, logger)
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
		if err := components.Registry.RunCleanup(workerCtx, registry.CleanupConfig{
			Interval: settings.Registry.CleanupInterval,
			TTL:      settings.Registry.ServiceTTL,
		}, logger); err != nil {
			logger.Error("cleanup worker stopped", slog.Any("error", err))
		}
	})

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	logger.Info("beacon listening",
		slog.String("addr", listener.Addr().String()),
		slog.String("version", buildVersion()),
	)

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

// buildVersion returns the version the Go toolchain stamped into the binary:
// a release tag such as v0.1.0, or a pseudo-version built from the git
// commit. Binaries built without version control information report
// "(devel)".
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "unknown"
	}

	return info.Main.Version
}
