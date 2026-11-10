package main

import (
	"bytes"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/nm1996/beacon/internal/config"
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
