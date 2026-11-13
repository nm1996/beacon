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
max_name_length = 100
max_address_length = 120

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
	want.Registry.MaxNameLength = 100
	want.Registry.MaxAddressLength = 120
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
		{name: "unknown section", content: "[tracing]\nenabled = true", wantErr: []string{"unknown keys", "tracing"}},
		{name: "port out of range", content: "[server]\nport = 70000", wantErr: []string{"server.port"}},
		{name: "zero timeout", content: "[server]\nwrite_timeout = \"0s\"", wantErr: []string{"server.write_timeout"}},
		{name: "fractional TTL", content: "[registry]\nservice_ttl = \"1.5s\"\ncleanup_interval = \"1s\"", wantErr: []string{"registry.service_ttl must be a whole number of seconds"}},
		{name: "zero max instances", content: "[registry]\nmax_instances = 0", wantErr: []string{"registry.max_instances must be positive"}},
		{name: "negative TTL", content: "[registry]\nservice_ttl = \"-5s\"", wantErr: []string{"registry.service_ttl"}},
		{name: "sub-second TTL", content: "[registry]\nservice_ttl = \"500ms\"\ncleanup_interval = \"100ms\"", wantErr: []string{"registry.service_ttl must be at least 1s"}},
		{name: "zero name length", content: "[registry]\nmax_name_length = 0", wantErr: []string{"registry.max_name_length must be between 1 and 253, got 0"}},
		{name: "name length over 253", content: "[registry]\nmax_name_length = 254", wantErr: []string{"registry.max_name_length must be between 1 and 253, got 254"}},
		{name: "negative address length", content: "[registry]\nmax_address_length = -1", wantErr: []string{"registry.max_address_length must be between 1 and 253, got -1"}},
		{name: "address length over 253", content: "[registry]\nmax_address_length = 254", wantErr: []string{"registry.max_address_length must be between 1 and 253, got 254"}},
		{name: "zero body limit", content: "[server]\nmax_request_body_bytes = 0", wantErr: []string{"server.max_request_body_bytes"}},
		{name: "sub-second reload interval", content: "[server]\nreload_interval = \"500ms\"", wantErr: []string{"server.reload_interval must be 0s (off) or at least 1s"}},
		{name: "negative reload interval", content: "[server]\nreload_interval = \"-1s\"", wantErr: []string{"server.reload_interval"}},
		{name: "unknown log level", content: "[logging]\nlevel = \"verbose\"", wantErr: []string{"logging.level"}},
		{name: "unknown log format", content: "[logging]\nformat = \"xml\"", wantErr: []string{"logging.format"}},
		{name: "metrics flag is not a boolean", content: "[metrics]\nenabled = \"yes\"", wantErr: []string{"read config"}},
		{name: "unknown storage backend", content: "[storage]\nbackend = \"redis\"", wantErr: []string{`storage.backend must be one of memory, got "redis"`}},
		{name: "empty storage backend", content: "[storage]\nbackend = \"\"", wantErr: []string{"storage.backend"}},
		{
			name:    "cleanup interval longer than TTL",
			content: "[registry]\nservice_ttl = \"10s\"\ncleanup_interval = \"20s\"",
			wantErr: []string{"registry.cleanup_interval (20s) must not be longer than registry.service_ttl (10s)"},
		},
		{
			name:    "TLS certificate without key",
			content: "[tls]\ncert_file = \"cert.pem\"",
			wantErr: []string{"tls.cert_file and tls.key_file must be set together"},
		},
		{
			name:    "TLS key without certificate",
			content: "[tls]\nkey_file = \"key.pem\"",
			wantErr: []string{"tls.cert_file and tls.key_file must be set together"},
		},
		{
			name:    "client CA without TLS",
			content: "[tls]\nclient_ca_file = \"clients.pem\"\n[auth]\nenabled = true\ntokens_file = \"tokens.toml\"",
			wantErr: []string{"tls.client_ca_file needs tls.cert_file and tls.key_file"},
		},
		{
			name:    "client CA without auth",
			content: "[tls]\ncert_file = \"cert.pem\"\nkey_file = \"key.pem\"\nclient_ca_file = \"clients.pem\"",
			wantErr: []string{"tls.client_ca_file needs auth.enabled = true"},
		},
		{name: "auth without tokens file", content: "[auth]\nenabled = true", wantErr: []string{"auth.tokens_file must be set when auth.enabled is true"}},
		{name: "auth flag is not a boolean", content: "[auth]\nenabled = \"yes\"", wantErr: []string{"read config"}},
		{name: "unknown TLS key", content: "[tls]\nca_file = \"ca.pem\"", wantErr: []string{"unknown keys", "tls.ca_file"}},
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

func TestClientCAWithTLSAndAuthIsAccepted(test *testing.T) {
	path := writeConfig(test, "[tls]\ncert_file = \"cert.pem\"\nkey_file = \"key.pem\"\nclient_ca_file = \"clients.pem\"\n"+
		"[auth]\nenabled = true\ntokens_file = \"tokens.toml\"")

	config, err := Load(path)
	if err != nil {
		test.Fatalf("load: %v", err)
	}

	if config.TLS.ClientCAFile != "clients.pem" {
		test.Errorf("expected client_ca_file to be read, got %q", config.TLS.ClientCAFile)
	}
}

func TestTLSEnabled(test *testing.T) {
	path := writeConfig(test, "[tls]\ncert_file = \"cert.pem\"\nkey_file = \"key.pem\"")

	config, err := Load(path)
	if err != nil {
		test.Fatalf("load: %v", err)
	}

	if !config.TLS.Enabled() {
		test.Error("expected TLS to be enabled when both files are set")
	}

	if Default().TLS.Enabled() {
		test.Error("expected TLS to be disabled by default")
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
