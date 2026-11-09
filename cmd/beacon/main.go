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
	"syscall"
	"time"

	"github.com/nm1996/beacon/internal/bootstrap"
)

// For now these are fixed; they will move to configuration once it exists.
const (
	listenAddr      = ":8080"
	shutdownTimeout = 10 * time.Second
)

// main exits with a non-zero status if the server fails to start or stop.
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run starts the HTTP server and blocks until it fails or the process gets
// SIGINT or SIGTERM. On a signal it gives in-flight requests up to
// shutdownTimeout to finish before returning.
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	components := bootstrap.New(bootstrap.Settings{Addr: listenAddr})
	server := components.Server

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
