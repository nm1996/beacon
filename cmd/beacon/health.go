package main

import (
	"context"
	"crypto/tls"
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

// healthURL returns the /health URL of the server described by settings,
// using https when TLS is enabled. A wildcard listen address is reached
// through loopback, because the check runs on the same host or in the same
// container as the server.
func healthURL(settings config.Config) string {
	host := settings.Server.Host

	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}

	scheme := "http"
	if settings.TLS.Enabled() {
		scheme = "https"
	}

	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(settings.Server.Port)) + "/health"
}

// healthClient checks health over plain HTTP or HTTPS. It does not verify
// the server's certificate: the check connects to the server's own listen
// address, usually loopback, which the certificate rarely names, sends no
// credentials, and only reads the status code, so there is nothing for an
// impostor to learn or fake that a crashed server would not also show.
var healthClient = &http.Client{
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // See healthClient's doc comment.
			MinVersion:         tls.VersionTLS12,
		},
	},
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

	response, err := healthClient.Do(request)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health check: %s answered %s", url, response.Status)
	}

	return nil
}
