package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/nm1996/beacon/internal/config"
)

// healthCheckTimeout bounds a --health-check run, so a hung server counts as
// unhealthy instead of blocking the container runtime's check.
const healthCheckTimeout = 3 * time.Second

// healthURL returns the /health URL of the server described by settings.
// A wildcard listen address is reached through loopback, because the check
// runs on the same host or in the same container as the server.
func healthURL(settings config.ServerConfig) string {
	host := settings.Host

	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}

	return "http://" + net.JoinHostPort(host, strconv.Itoa(settings.Port)) + "/health"
}

// checkHealth calls url and reports an error unless it answers 200 within
// healthCheckTimeout.
func checkHealth(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health check: %s answered %s", url, response.Status)
	}

	return nil
}
