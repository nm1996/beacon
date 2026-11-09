// Command beacon runs the Beacon service registry as an HTTP server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/nm1996/beacon/internal/bootstrap"
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

// main exits with a non-zero status if the server fails to start or stop.
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run starts the HTTP server and the cleanup worker, then blocks until the
// server fails or the process gets SIGINT or SIGTERM. On a signal it gives
// in-flight requests up to shutdownTimeout to finish. The cleanup worker is
// always stopped and waited for before run returns.
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	components := bootstrap.New(bootstrap.Settings{Addr: listenAddr})
	server := components.Server

	workerCtx, stopWorkers := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() {
		stopWorkers()
		workers.Wait()
	}()

	workers.Go(func() {
		if err := components.Registry.RunCleanup(workerCtx, cleanupInterval, serviceTTL); err != nil {
			log.Printf("cleanup worker: %v", err)
		}
	})

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("Beacon listening on %s", server.Addr)
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

	log.Println("Beacon shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}

	return nil
}
