// Command beacon runs the Beacon service registry as an HTTP server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/nm1996/beacon/internal/bootstrap"
	"github.com/nm1996/beacon/internal/registry"
)

// For now these are fixed; they will move to configuration once it exists.
const (
	listenAddr      = ":8080"
	shutdownTimeout = 10 * time.Second

	// serviceTTL is how long an instance may go without a heartbeat before
	// cleanup removes it; cleanupInterval is how often cleanup runs.
	serviceTTL      = 30 * time.Second
	cleanupInterval = 5 * time.Second
)

// main logs as JSON to standard output and exits with a non-zero status if
// the server fails to start or stop.
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("beacon stopped with an error", slog.Any("error", err))
		os.Exit(1)
	}
}

// run starts the HTTP server and the cleanup worker, then blocks until the
// server fails or the process gets SIGINT or SIGTERM. On a signal it gives
// in-flight requests up to shutdownTimeout to finish. The cleanup worker is
// always stopped and waited for before run returns.
func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	components := bootstrap.New(bootstrap.Settings{Addr: listenAddr}, logger)
	server := components.Server

	workerCtx, stopWorkers := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() {
		stopWorkers()
		workers.Wait()
	}()

	workers.Go(func() {
		if err := components.Registry.RunCleanup(workerCtx, registry.CleanupConfig{
			Interval: cleanupInterval,
			TTL:      serviceTTL,
		}, logger); err != nil {
			logger.Error("cleanup worker stopped", slog.Any("error", err))
		}
	})

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("beacon listening", slog.String("addr", server.Addr))
		serveErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	logger.Info("beacon shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}

	return nil
}
