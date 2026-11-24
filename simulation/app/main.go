// Command app is the application service the simulation runs as orders,
// payments and inventory. It serves GET /health and GET /work, registers
// itself with Beacon through client.Announce, and unregisters on SIGTERM.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nm1996/beacon/client"
)

const (
	// firstAnnounceDelay is how long the first failed registration waits
	// before the next attempt. Each further failure doubles it.
	firstAnnounceDelay = 500 * time.Millisecond

	// maxAnnounceDelay caps the wait between registration attempts.
	maxAnnounceDelay = 5 * time.Second

	// closeTimeout bounds unregistering on shutdown, so a Beacon that cannot
	// be reached does not hold the process.
	closeTimeout = 5 * time.Second
)

// settings is what the command line configures.
type settings struct {
	service   string
	beaconURL string
	address   string
	port      int
	id        string
	tokenFile string
}

// main runs the service until SIGTERM or SIGINT and exits non-zero if it
// could not start.
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	if err := run(ctx, os.Args[1:], logger); err != nil {
		logger.Error("app failed", slog.Any("error", err))
		stop()
		os.Exit(1) //nolint:gocritic // stop has run; nothing else is deferred.
	}
}

// parseSettings reads the flags in args.
func parseSettings(args []string) (settings, error) {
	flags := flag.NewFlagSet("app", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var parsed settings
	flags.StringVar(&parsed.service, "service", "", "service name to register, such as orders (required)")
	flags.StringVar(&parsed.beaconURL, "beacon", "http://lb", "base URL of Beacon")
	flags.StringVar(&parsed.address, "address", "", "address other services reach this instance at, such as orders-1 (required)")
	flags.IntVar(&parsed.port, "port", 9000, "port to listen on and register")
	flags.StringVar(&parsed.id, "id", "", "instance ID to register; empty lets Beacon choose")
	flags.StringVar(&parsed.tokenFile, "token-file", "", "file holding the bearer token for Beacon; empty sends none")

	if err := flags.Parse(args); err != nil {
		return settings{}, err
	}

	if parsed.service == "" || parsed.address == "" {
		return settings{}, errors.New("-service and -address are required")
	}

	if parsed.port < 1 || parsed.port > 65535 {
		return settings{}, fmt.Errorf("-port %d is out of range", parsed.port)
	}

	return parsed, nil
}

// run serves on the configured port, registers with Beacon and keeps the
// registration alive until ctx ends. It then unregisters, before the server
// stops, so discovery stops returning the instance while it still answers.
func run(ctx context.Context, args []string, logger *slog.Logger) error {
	config, err := parseSettings(args)
	if err != nil {
		return err
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", ":"+strconv.Itoa(config.port))
	if err != nil {
		return err
	}

	return serve(ctx, config, listener, logger)
}

// serve is run once the listener is open, which tests choose themselves.
func serve(ctx context.Context, config settings, listener net.Listener, logger *slog.Logger) error {
	logger = logger.With(slog.String("service", config.service), slog.String("address", config.address))

	options := []client.Option{client.WithLogger(logger)}

	if config.tokenFile != "" {
		token, err := readToken(config.tokenFile)
		if err != nil {
			return err
		}

		options = append(options, client.WithToken(token))
	}

	beacon, err := client.New(config.beaconURL, options...)
	if err != nil {
		return err
	}

	instance := &instance{service: config.service}
	server := &http.Server{
		Handler:           instance.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// A server that fails stops the process too, instead of leaving it
	// registered without answering.
	ctx, fail := context.WithCancelCause(ctx)
	defer fail(nil)

	served := make(chan struct{})

	go func() {
		defer close(served)

		if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			fail(fmt.Errorf("serve: %w", err))
		}
	}()

	// announce only fails once ctx has ended, so there is nothing to undo.
	lease, err := announce(ctx, beacon, client.Registration{
		ID:      config.id,
		Name:    config.service,
		Address: config.address,
		Port:    config.port,
	}, logger)
	if err == nil {
		instance.lease.Store(lease)
		logger.InfoContext(ctx, "registered", slog.String("id", lease.ID()))

		<-ctx.Done()
		closeLease(lease, logger)
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()

	shutdownErr := server.Shutdown(shutdownCtx)
	<-served

	if cause := context.Cause(ctx); !errors.Is(cause, context.Canceled) {
		return cause
	}

	return shutdownErr
}

// readToken returns the token in path, without surrounding white space.
func readToken(path string) (string, error) {
	content, err := os.ReadFile(path) //nolint:gosec // The path comes from the command line, by design.
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}

	token := strings.TrimSpace(string(content))
	if token == "" {
		return "", fmt.Errorf("read token: %s is empty", path)
	}

	return token, nil
}

// announce registers until it succeeds or ctx ends, waiting longer after
// each failure, since Beacon may not be reachable yet when the simulation
// starts.
func announce(ctx context.Context, beacon *client.Client, registration client.Registration, logger *slog.Logger) (*client.Lease, error) {
	delay := firstAnnounceDelay

	for {
		lease, err := beacon.Announce(ctx, registration)
		if err == nil {
			return lease, nil
		}

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		logger.WarnContext(ctx, "registration failed, retrying", slog.Any("error", err), slog.Duration("delay", delay))

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}

		delay = min(2*delay, maxAnnounceDelay)
	}
}

// closeLease unregisters the instance, logging the outcome.
func closeLease(lease *client.Lease, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()

	id := lease.ID()
	if err := lease.Close(ctx); err != nil {
		logger.Warn("unregister failed; the instance will expire", slog.String("id", id), slog.Any("error", err))
		return
	}

	logger.Info("unregistered", slog.String("id", id))
}

// instance answers HTTP requests for one running service.
type instance struct {
	service string
	// lease is nil until the first registration succeeds.
	lease atomic.Pointer[client.Lease]
}

// work is the body of GET /work.
type work struct {
	Service    string    `json:"service"`
	InstanceID string    `json:"instance_id"`
	Time       time.Time `json:"time"`
}

// routes returns the handler for /health and /work.
func (instance *instance) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", instance.health)
	mux.HandleFunc("GET /work", instance.work)

	return mux
}

// health answers 200 while the process serves, registered or not.
func (instance *instance) health(writer http.ResponseWriter, _ *http.Request) {
	writer.WriteHeader(http.StatusOK)
}

// work reports which instance answered, so callers can check it against
// the ID Beacon returned for this address. Before registering, the instance
// has no ID yet and answers 503.
func (instance *instance) work(writer http.ResponseWriter, _ *http.Request) {
	lease := instance.lease.Load()
	if lease == nil {
		http.Error(writer, "not registered yet", http.StatusServiceUnavailable)
		return
	}

	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(work{
		Service:    instance.service,
		InstanceID: lease.ID(),
		Time:       time.Now().UTC(),
	})
}
