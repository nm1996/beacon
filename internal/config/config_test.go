package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeConfig writes content to a TOML file in a temporary directory and
// returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "beacon.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("expected default config to be valid, got %v", err)
	}
}

func TestLoadWithoutPathReturnsDefaults(t *testing.T) {
	config, err := Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if config != Default() {
		t.Errorf("expected defaults, got %+v", config)
	}
}

func TestExampleConfigMatchesDefaults(t *testing.T) {
	config, err := Load(filepath.Join("..", "..", "configs", "beacon.example.toml"))
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}

	if config != Default() {
		t.Errorf("example config and Default() have drifted apart:\nexample: %+v\ndefault: %+v", config, Default())
	}
}

func TestLoadOverridesOnlyGivenKeys(t *testing.T) {
	path := writeConfig(t, `
[server]
port = 9090
read_timeout = "1m30s"

[registry]
service_ttl = "2m"

[logging]
format = "text"
`)

	config, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	want := Default()
	want.Server.Port = 9090
	want.Server.ReadTimeout = 90 * time.Second
	want.Registry.ServiceTTL = 2 * time.Minute
	want.Logging.Format = "text"

	if config != want {
		t.Errorf("expected %+v, got %+v", want, config)
	}
}

func TestLoadEmptyFileReturnsDefaults(t *testing.T) {
	config, err := Load(writeConfig(t, ""))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if config != Default() {
		t.Errorf("expected defaults, got %+v", config)
	}
}

func TestLoadFailures(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr []string
	}{
		{name: "invalid TOML", content: `[server`, wantErr: []string{"read config"}},
		{name: "wrong value type", content: "[server]\nport = \"8080\"", wantErr: []string{"read config"}},
		{name: "unparsable duration", content: "[registry]\nservice_ttl = \"soon\"", wantErr: []string{"read config"}},
		{name: "unknown key", content: "[server]\nprot = 8080", wantErr: []string{"unknown keys", "server.prot"}},
		{name: "unknown section", content: "[metrics]\nenabled = true", wantErr: []string{"unknown keys", "metrics"}},
		{name: "port out of range", content: "[server]\nport = 70000", wantErr: []string{"server.port"}},
		{name: "zero timeout", content: "[server]\nwrite_timeout = \"0s\"", wantErr: []string{"server.write_timeout"}},
		{name: "negative TTL", content: "[registry]\nservice_ttl = \"-5s\"", wantErr: []string{"registry.service_ttl"}},
		{name: "sub-second TTL", content: "[registry]\nservice_ttl = \"500ms\"\ncleanup_interval = \"100ms\"", wantErr: []string{"registry.service_ttl must be at least 1s"}},
		{name: "zero body limit", content: "[server]\nmax_request_body_bytes = 0", wantErr: []string{"server.max_request_body_bytes"}},
		{name: "unknown log level", content: "[logging]\nlevel = \"verbose\"", wantErr: []string{"logging.level"}},
		{name: "unknown log format", content: "[logging]\nformat = \"xml\"", wantErr: []string{"logging.format"}},
		{
			name:    "cleanup interval longer than TTL",
			content: "[registry]\nservice_ttl = \"10s\"\ncleanup_interval = \"20s\"",
			wantErr: []string{"registry.cleanup_interval (20s) must not be longer than registry.service_ttl (10s)"},
		},
		{
			name:    "several problems at once",
			content: "[server]\nport = 0\n[registry]\ncleanup_interval = \"0s\"",
			wantErr: []string{"server.port", "registry.cleanup_interval"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.content))
			if err == nil {
				t.Fatal("expected an error")
			}

			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("expected error to mention %q, got %v", want, err)
				}
			}
		})
	}
}

func TestCleanupIntervalMayEqualTTL(t *testing.T) {
	path := writeConfig(t, "[registry]\nservice_ttl = \"10s\"\ncleanup_interval = \"10s\"")

	if _, err := Load(path); err != nil {
		t.Errorf("expected an interval equal to the TTL to be accepted, got %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.toml"))

	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected a file-not-found error, got %v", err)
	}
}

func TestServerAddr(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{host: "0.0.0.0", want: "0.0.0.0:8080"},
		{host: "", want: ":8080"},
		{host: "::1", want: "[::1]:8080"},
	}

	for _, tt := range tests {
		server := ServerConfig{Host: tt.host, Port: 8080}

		if got := server.Addr(); got != tt.want {
			t.Errorf("host %q: expected %q, got %q", tt.host, tt.want, got)
		}
	}
}
