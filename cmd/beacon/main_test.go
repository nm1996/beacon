package main

import (
	"bytes"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/nm1996/beacon/internal/bootstrap"
	"github.com/nm1996/beacon/internal/config"
	"github.com/nm1996/beacon/internal/tlstest"
)

func TestRunFailsBeforeLoggingWhenPortIsTaken(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer occupied.Close()

	settings := config.Default()
	settings.Server.Host = "127.0.0.1"
	settings.Server.Port = occupied.Addr().(*net.TCPAddr).Port

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	err = run(settings, logger)

	if err == nil || !strings.Contains(err.Error(), "listen on") {
		t.Fatalf("expected a listen error, got %v", err)
	}

	if strings.Contains(logs.String(), "beacon listening") {
		t.Errorf("expected nothing to claim it was listening, logs:\n%s", logs.String())
	}
}

// startServer builds Beacon from settings, serves it on a loopback port with
// serve, and returns the base URL. The server is shut down when the test ends.
func startServer(test *testing.T, settings config.Config, scheme string) string {
	test.Helper()

	components, err := bootstrap.New(settings, slog.New(slog.DiscardHandler), "v0.0.0-test")
	if err != nil {
		test.Fatalf("build components: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatalf("listen: %v", err)
	}

	servesTLS := components.Server.TLSConfig != nil

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- serve(components.Server, listener, servesTLS)
	}()

	test.Cleanup(func() {
		if err := components.Server.Close(); err != nil {
			test.Errorf("close server: %v", err)
		}

		if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
			test.Errorf("expected the server to stop with ErrServerClosed, got %v", err)
		}
	})

	return scheme + "://" + listener.Addr().String()
}

func TestServeUsesTLSWhenConfigured(test *testing.T) {
	certificate := tlstest.WriteCertificate(test)

	settings := config.Default()
	settings.TLS = config.TLSConfig{CertFile: certificate.CertFile, KeyFile: certificate.KeyFile}

	baseURL := startServer(test, settings, "https")

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: certificate.Pool, MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2: true,
	}}
	defer client.CloseIdleConnections()

	response, err := client.Get(baseURL + "/health")
	if err != nil {
		test.Fatalf("GET /health over TLS: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		test.Errorf("expected 200, got %s", response.Status)
	}

	if response.TLS == nil {
		test.Error("expected the response to arrive over TLS")
	}

	if response.ProtoMajor != 2 {
		test.Errorf("expected HTTP/2 to be offered over TLS, got %s", response.Proto)
	}

	if err := checkHealth(test.Context(), baseURL+"/health"); err != nil {
		test.Errorf("expected --health-check to pass over TLS, got %v", err)
	}
}

func TestServeRejectsPlainHTTPWhenTLSIsConfigured(test *testing.T) {
	certificate := tlstest.WriteCertificate(test)

	settings := config.Default()
	settings.TLS = config.TLSConfig{CertFile: certificate.CertFile, KeyFile: certificate.KeyFile}

	baseURL := startServer(test, settings, "http")

	response, err := http.Get(baseURL + "/health")
	if err != nil {
		test.Fatalf("GET /health over plain HTTP: %v", err)
	}
	defer response.Body.Close()

	// Go's TLS server answers a plain HTTP request with 400 rather than
	// serving it.
	if response.StatusCode != http.StatusBadRequest {
		test.Errorf("expected plain HTTP to be refused with 400, got %s", response.Status)
	}
}

func TestServeUsesPlainHTTPByDefault(test *testing.T) {
	baseURL := startServer(test, config.Default(), "http")

	if err := checkHealth(test.Context(), baseURL+"/health"); err != nil {
		test.Errorf("expected plain HTTP to work by default, got %v", err)
	}
}

func TestLogListeningWarnsAboutPlainHTTPBeyondLoopback(test *testing.T) {
	cases := []struct {
		name      string
		address   string
		servesTLS bool
		wantWarn  bool
	}{
		{name: "plain on all IPv4 interfaces", address: "0.0.0.0", wantWarn: true},
		{name: "plain on all IPv6 interfaces", address: "::", wantWarn: true},
		{name: "plain on a private address", address: "10.0.0.5", wantWarn: true},
		{name: "plain on loopback", address: "127.0.0.1"},
		{name: "TLS on all interfaces", address: "0.0.0.0", servesTLS: true},
	}

	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			address := &net.TCPAddr{IP: net.ParseIP(testCase.address), Port: 8080}

			logListening(logger, address, testCase.servesTLS, config.StorageMemory)

			if !strings.Contains(logs.String(), "beacon listening") {
				test.Errorf("expected the listening message, logs:\n%s", logs.String())
			}

			if warned := strings.Contains(logs.String(), "level=WARN"); warned != testCase.wantWarn {
				test.Errorf("expected warning %t, got %t, logs:\n%s", testCase.wantWarn, warned, logs.String())
			}
		})
	}
}

func TestIsLoopback(test *testing.T) {
	cases := []struct {
		address string
		want    bool
	}{
		{address: "127.0.0.1", want: true},
		{address: "::1", want: true},
		{address: "0.0.0.0", want: false},
		{address: "::", want: false},
		{address: "10.0.0.5", want: false},
	}

	for _, testCase := range cases {
		address := &net.TCPAddr{IP: net.ParseIP(testCase.address), Port: 8080}

		if got := isLoopback(address); got != testCase.want {
			test.Errorf("%s: expected %t, got %t", testCase.address, testCase.want, got)
		}
	}
}
