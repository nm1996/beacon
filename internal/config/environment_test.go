package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadAppliesEveryEnvironmentVariable(test *testing.T) {
	test.Setenv("BEACON_SERVER_HOST", "127.0.0.1")
	test.Setenv("BEACON_SERVER_PORT", "9090")
	test.Setenv("BEACON_SERVER_READ_TIMEOUT", "1s")
	test.Setenv("BEACON_SERVER_WRITE_TIMEOUT", "2s")
	test.Setenv("BEACON_SERVER_IDLE_TIMEOUT", "3s")
	test.Setenv("BEACON_SERVER_SHUTDOWN_TIMEOUT", "4s")
	test.Setenv("BEACON_SERVER_MAX_REQUEST_BODY_BYTES", "1024")
	test.Setenv("BEACON_SERVER_RELOAD_INTERVAL", "30s")
	test.Setenv("BEACON_TLS_CERT_FILE", "/etc/beacon/tls/cert.pem")
	test.Setenv("BEACON_TLS_KEY_FILE", "/etc/beacon/tls/key.pem")
	test.Setenv("BEACON_TLS_CLIENT_CA_FILE", "/etc/beacon/tls/clients.pem")
	test.Setenv("BEACON_AUTH_ENABLED", "true")
	test.Setenv("BEACON_AUTH_TOKENS_FILE", "/etc/beacon/tokens.toml")
	test.Setenv("BEACON_REGISTRY_SERVICE_TTL", "1m")
	test.Setenv("BEACON_REGISTRY_CLEANUP_INTERVAL", "500ms")
	test.Setenv("BEACON_REGISTRY_MAX_NAME_LENGTH", "100")
	test.Setenv("BEACON_REGISTRY_MAX_ADDRESS_LENGTH", "120")
	test.Setenv("BEACON_REGISTRY_MAX_INSTANCES", "250")
	test.Setenv("BEACON_REGISTRY_MAX_INSTANCES_PER_OWNER", "25")
	test.Setenv("BEACON_STORAGE_BACKEND", "memory")
	test.Setenv("BEACON_METRICS_ENABLED", "false")
	test.Setenv("BEACON_LOGGING_LEVEL", "debug")
	test.Setenv("BEACON_LOGGING_FORMAT", "text")

	config, err := Load("")
	if err != nil {
		test.Fatalf("load: %v", err)
	}

	want := Config{
		Server: ServerConfig{
			Host:                "127.0.0.1",
			Port:                9090,
			ReadTimeout:         time.Second,
			WriteTimeout:        2 * time.Second,
			IdleTimeout:         3 * time.Second,
			ShutdownTimeout:     4 * time.Second,
			MaxRequestBodyBytes: 1024,
			ReloadInterval:      30 * time.Second,
		},
		TLS: TLSConfig{
			CertFile:     "/etc/beacon/tls/cert.pem",
			KeyFile:      "/etc/beacon/tls/key.pem",
			ClientCAFile: "/etc/beacon/tls/clients.pem",
		},
		Auth: AuthConfig{
			Enabled:    true,
			TokensFile: "/etc/beacon/tokens.toml",
		},
		Registry: RegistryConfig{
			ServiceTTL:           time.Minute,
			CleanupInterval:      500 * time.Millisecond,
			MaxNameLength:        100,
			MaxAddressLength:     120,
			MaxInstances:         250,
			MaxInstancesPerOwner: 25,
		},
		Storage: StorageConfig{
			Backend: StorageMemory,
		},
		Metrics: MetricsConfig{
			Enabled: false,
		},
		Logging: LoggingConfig{
			Level:  "debug",
			Format: "text",
		},
	}

	if config != want {
		test.Errorf("expected %+v, got %+v", want, config)
	}
}

func TestEnvironmentOverridesFile(test *testing.T) {
	path := writeConfig(test, `
[server]
port = 9090
read_timeout = "1m"
`)
	test.Setenv("BEACON_SERVER_PORT", "7070")

	config, err := Load(path)
	if err != nil {
		test.Fatalf("load: %v", err)
	}

	want := Default()
	want.Server.Port = 7070
	want.Server.ReadTimeout = time.Minute

	if config != want {
		test.Errorf("expected %+v, got %+v", want, config)
	}
}

func TestEmptyEnvironmentVariableIsApplied(test *testing.T) {
	test.Setenv("BEACON_SERVER_HOST", "")

	config, err := Load("")
	if err != nil {
		test.Fatalf("load: %v", err)
	}

	if config.Server.Host != "" {
		test.Errorf("expected an empty host, got %q", config.Server.Host)
	}
}

func TestLoadEnvironmentFailures(test *testing.T) {
	testCases := []struct {
		name      string
		variables map[string]string
		wantErr   []string
	}{
		{
			name:      "port is not an integer",
			variables: map[string]string{"BEACON_SERVER_PORT": "eighty"},
			wantErr:   []string{"read environment", "BEACON_SERVER_PORT must be an integer", `"eighty"`},
		},
		{
			name:      "empty port",
			variables: map[string]string{"BEACON_SERVER_PORT": ""},
			wantErr:   []string{"BEACON_SERVER_PORT must be an integer"},
		},
		{
			name:      "max instances is not an integer",
			variables: map[string]string{"BEACON_REGISTRY_MAX_INSTANCES": "lots"},
			wantErr:   []string{"BEACON_REGISTRY_MAX_INSTANCES must be an integer"},
		},
		{
			name:      "body limit is not an integer",
			variables: map[string]string{"BEACON_SERVER_MAX_REQUEST_BODY_BYTES": "1.5"},
			wantErr:   []string{"BEACON_SERVER_MAX_REQUEST_BODY_BYTES must be an integer"},
		},
		{
			name:      "unparsable duration",
			variables: map[string]string{"BEACON_REGISTRY_SERVICE_TTL": "soon"},
			wantErr:   []string{"BEACON_REGISTRY_SERVICE_TTL must be a duration"},
		},
		{
			name: "several unparsable values at once",
			variables: map[string]string{
				"BEACON_SERVER_PORT":          "eighty",
				"BEACON_SERVER_WRITE_TIMEOUT": "later",
			},
			wantErr: []string{"BEACON_SERVER_PORT", "BEACON_SERVER_WRITE_TIMEOUT"},
		},
		{
			name:      "port out of range",
			variables: map[string]string{"BEACON_SERVER_PORT": "70000"},
			wantErr:   []string{"invalid config", "server.port"},
		},
		{
			name: "cleanup interval longer than TTL",
			variables: map[string]string{
				"BEACON_REGISTRY_SERVICE_TTL":      "5s",
				"BEACON_REGISTRY_CLEANUP_INTERVAL": "1m",
			},
			wantErr: []string{"invalid config", "registry.cleanup_interval", "registry.service_ttl"},
		},
		{
			name:      "metrics flag is not a boolean",
			variables: map[string]string{"BEACON_METRICS_ENABLED": "sometimes"},
			wantErr:   []string{"BEACON_METRICS_ENABLED must be true or false"},
		},
		{
			name:      "unknown storage backend",
			variables: map[string]string{"BEACON_STORAGE_BACKEND": "redis"},
			wantErr:   []string{"invalid config", `storage.backend must be one of memory, got "redis"`},
		},
		{
			name:      "unknown log level",
			variables: map[string]string{"BEACON_LOGGING_LEVEL": "verbose"},
			wantErr:   []string{"logging.level"},
		},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(subtest *testing.T) {
			for name, value := range testCase.variables {
				subtest.Setenv(name, value)
			}

			_, err := Load("")
			if err == nil {
				subtest.Fatal("expected an error")
			}

			for _, want := range testCase.wantErr {
				if !strings.Contains(err.Error(), want) {
					subtest.Errorf("expected error to mention %q, got %v", want, err)
				}
			}
		})
	}
}
