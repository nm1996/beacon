// Package config loads Beacon's settings and validates them.
//
// Settings start from built-in defaults, are overlaid by an optional TOML
// file, and are then overlaid by BEACON_* environment variables. A file only
// needs the keys it wants to change. Unknown keys are rejected so a typo
// cannot silently fall back to a default.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config holds every setting Beacon reads at startup.
type Config struct {
	Server   ServerConfig   `toml:"server"`
	Registry RegistryConfig `toml:"registry"`
	Logging  LoggingConfig  `toml:"logging"`
}

// ServerConfig controls the HTTP server.
type ServerConfig struct {
	// Host is the interface to listen on. Empty or "0.0.0.0" means all.
	Host string `toml:"host"`
	// Port is the TCP port to listen on.
	Port int `toml:"port"`
	// ReadTimeout limits how long reading a request, headers included, may take.
	ReadTimeout time.Duration `toml:"read_timeout"`
	// WriteTimeout limits how long writing a response may take.
	WriteTimeout time.Duration `toml:"write_timeout"`
	// IdleTimeout is how long a keep-alive connection may wait for its next request.
	IdleTimeout time.Duration `toml:"idle_timeout"`
	// ShutdownTimeout is how long in-flight requests get to finish on shutdown.
	ShutdownTimeout time.Duration `toml:"shutdown_timeout"`
	// MaxRequestBodyBytes is the largest request body the API accepts.
	MaxRequestBodyBytes int64 `toml:"max_request_body_bytes"`
}

// RegistryConfig controls how long instances live without heartbeats.
type RegistryConfig struct {
	// ServiceTTL is how long an instance may go without a heartbeat before
	// it is removed.
	ServiceTTL time.Duration `toml:"service_ttl"`
	// CleanupInterval is how often stale instances are looked for.
	CleanupInterval time.Duration `toml:"cleanup_interval"`
}

// LoggingConfig controls log output.
type LoggingConfig struct {
	// Level is the minimum level logged: debug, info, warn or error.
	Level string `toml:"level"`
	// Format is "json" or "text".
	Format string `toml:"format"`
}

// Default returns the settings Beacon uses when no file is given. They match
// configs/beacon.example.toml.
func Default() Config {
	return Config{
		Server: ServerConfig{
			Host:                "0.0.0.0",
			Port:                8080,
			ReadTimeout:         5 * time.Second,
			WriteTimeout:        10 * time.Second,
			IdleTimeout:         60 * time.Second,
			ShutdownTimeout:     10 * time.Second,
			MaxRequestBodyBytes: 64 << 10,
		},
		Registry: RegistryConfig{
			ServiceTTL:      30 * time.Second,
			CleanupInterval: 5 * time.Second,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// Load returns the default settings overlaid with those in the TOML file at
// path, then with any BEACON_* environment variables, and validates the
// result. An empty path skips the file. It fails if the file is missing, is
// not valid TOML, contains an unknown key, if an environment variable cannot
// be parsed, or if the final settings are rejected by Validate.
func Load(path string) (Config, error) {
	config := Default()

	if path != "" {
		if err := decodeFile(path, &config); err != nil {
			return Config{}, err
		}
	}

	if err := applyEnvironment(&config, os.LookupEnv); err != nil {
		return Config{}, fmt.Errorf("read environment: %w", err)
	}

	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config: %w", err)
	}

	return config, nil
}

// decodeFile overlays config with the TOML file at path, rejecting keys that
// do not match a setting.
func decodeFile(path string, config *Config) error {
	metadata, err := toml.DecodeFile(path, config)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}

	if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}

		return fmt.Errorf("read config %s: unknown keys: %s", path, strings.Join(keys, ", "))
	}

	return nil
}

// Validate reports every setting that is out of range, each named by its
// TOML key, so one run shows everything that needs fixing.
func (config Config) Validate() error {
	var problems []error

	check := func(ok bool, format string, args ...any) {
		if !ok {
			problems = append(problems, fmt.Errorf(format, args...))
		}
	}

	server := config.Server
	check(server.Port >= 1 && server.Port <= 65535, "server.port must be between 1 and 65535, got %d", server.Port)
	check(server.ReadTimeout > 0, "server.read_timeout must be positive, got %s", server.ReadTimeout)
	check(server.WriteTimeout > 0, "server.write_timeout must be positive, got %s", server.WriteTimeout)
	check(server.IdleTimeout > 0, "server.idle_timeout must be positive, got %s", server.IdleTimeout)
	check(server.ShutdownTimeout > 0, "server.shutdown_timeout must be positive, got %s", server.ShutdownTimeout)
	check(server.MaxRequestBodyBytes > 0, "server.max_request_body_bytes must be positive, got %d", server.MaxRequestBodyBytes)

	registry := config.Registry
	// Clients are told the TTL in whole seconds when they register.
	check(registry.ServiceTTL >= time.Second, "registry.service_ttl must be at least 1s, got %s", registry.ServiceTTL)
	check(registry.CleanupInterval > 0, "registry.cleanup_interval must be positive, got %s", registry.CleanupInterval)
	// A stale instance stays visible for up to TTL plus one interval, so an
	// interval longer than the TTL would more than double that window.
	check(registry.CleanupInterval <= registry.ServiceTTL,
		"registry.cleanup_interval (%s) must not be longer than registry.service_ttl (%s)",
		registry.CleanupInterval, registry.ServiceTTL)

	_, levelErr := config.Logging.SlogLevel()
	check(levelErr == nil, "logging.level must be debug, info, warn or error, got %q", config.Logging.Level)
	check(config.Logging.Format == "json" || config.Logging.Format == "text",
		"logging.format must be json or text, got %q", config.Logging.Format)

	return errors.Join(problems...)
}

// Addr returns the host and port in the form net.Listen expects.
func (server ServerConfig) Addr() string {
	return net.JoinHostPort(server.Host, strconv.Itoa(server.Port))
}

// SlogLevel parses Level into a slog.Level.
func (logging LoggingConfig) SlogLevel() (slog.Level, error) {
	var level slog.Level

	err := level.UnmarshalText([]byte(logging.Level))

	return level, err
}
