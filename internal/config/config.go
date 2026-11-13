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
	"math"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/nm1996/beacon/internal/discovery"
)

// Config holds every setting Beacon reads at startup.
type Config struct {
	Server    ServerConfig    `toml:"server"`
	TLS       TLSConfig       `toml:"tls"`
	Auth      AuthConfig      `toml:"auth"`
	Registry  RegistryConfig  `toml:"registry"`
	Storage   StorageConfig   `toml:"storage"`
	RateLimit RateLimitConfig `toml:"rate_limit"`
	Metrics   MetricsConfig   `toml:"metrics"`
	Logging   LoggingConfig   `toml:"logging"`
}

// ServerConfig controls the HTTP server.
type ServerConfig struct {
	// Host is the interface to listen on. The default, 127.0.0.1, only
	// accepts connections from the same machine; "0.0.0.0" or empty means
	// every interface.
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
	// ReloadInterval is how often the TLS files and the tokens file are
	// checked for changes and reloaded when their contents differ. Zero
	// turns checking off; SIGHUP still reloads on platforms that have it.
	ReloadInterval time.Duration `toml:"reload_interval"`
}

// TLSConfig turns on HTTPS. Setting both files serves the API, /health and
// /metrics over TLS only; leaving both empty serves plain HTTP.
type TLSConfig struct {
	// CertFile is the PEM certificate chain the server presents, leaf first.
	CertFile string `toml:"cert_file"`
	// KeyFile is the PEM private key for the leaf certificate in CertFile.
	KeyFile string `toml:"key_file"`
	// ClientCAFile is a PEM bundle of CAs whose client certificates Beacon
	// accepts as identities. Clients without a certificate can still connect
	// and use a bearer token. It needs TLS and authentication enabled.
	ClientCAFile string `toml:"client_ca_file"`
}

// Enabled reports whether the server should serve TLS. Validate ensures both
// files are set whenever either one is.
func (tls TLSConfig) Enabled() bool {
	return tls.CertFile != "" && tls.KeyFile != ""
}

// AuthConfig controls bearer-token authentication.
type AuthConfig struct {
	// Enabled requires a valid bearer token on every endpoint except
	// /health.
	Enabled bool `toml:"enabled"`
	// TokensFile lists the accepted tokens, by SHA-256 hash, and what each
	// may do. It is required when Enabled is set.
	TokensFile string `toml:"tokens_file"`
}

// RegistryConfig controls how long instances live without heartbeats and
// what a registration may contain.
type RegistryConfig struct {
	// ServiceTTL is how long an instance may go without a heartbeat before
	// it is removed.
	ServiceTTL time.Duration `toml:"service_ttl"`
	// CleanupInterval is how often stale instances are looked for.
	CleanupInterval time.Duration `toml:"cleanup_interval"`
	// MaxNameLength is the longest service name accepted.
	MaxNameLength int `toml:"max_name_length"`
	// MaxAddressLength is the longest host name accepted as an address. IP
	// addresses are not held to it.
	MaxAddressLength int `toml:"max_address_length"`
	// MaxInstances is the most instances Beacon holds at once; registrations
	// beyond it are refused until others leave or expire.
	MaxInstances int `toml:"max_instances"`
	// MaxInstancesPerOwner is the most instances one token or certificate
	// may hold at once, so no single caller can take every place. Zero means
	// no limit per owner. It needs authentication, since without it callers
	// are not told apart.
	MaxInstancesPerOwner int `toml:"max_instances_per_owner"`
}

// StorageMemory selects the in-memory registry backend, which keeps
// nothing across restarts.
const StorageMemory = "memory"

// storageBackends lists every value storage.backend accepts. bootstrap
// builds the matching backend for each one.
var storageBackends = []string{StorageMemory}

// StorageConfig selects where registered instances are kept.
type StorageConfig struct {
	// Backend names the registry backend. Only "memory" exists today.
	Backend string `toml:"backend"`
}

// RateLimitConfig limits how often a client may do what is costly or a
// sign of abuse. A rate of zero turns that limit off.
type RateLimitConfig struct {
	// RegistrationsPerSecond is how many registrations each caller may make
	// per second on average: each token or certificate with authentication
	// on, each client network without it.
	RegistrationsPerSecond float64 `toml:"registrations_per_second"`
	// RegistrationBurst is how many registrations a caller may make at once
	// before RegistrationsPerSecond applies.
	RegistrationBurst int `toml:"registration_burst"`
	// AuthFailuresPerSecond is how many failed authentication attempts each
	// client network may make per second on average. Beyond it, every
	// request from that network gets 429 until it is back under the limit.
	AuthFailuresPerSecond float64 `toml:"auth_failures_per_second"`
	// AuthFailureBurst is how many failed attempts a client network may
	// make at once before AuthFailuresPerSecond applies.
	AuthFailureBurst int `toml:"auth_failure_burst"`
}

// MetricsConfig controls the Prometheus endpoint.
type MetricsConfig struct {
	// Enabled serves Prometheus metrics at GET /metrics on the API port.
	Enabled bool `toml:"enabled"`
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
			Host:                "127.0.0.1",
			Port:                8080,
			ReadTimeout:         5 * time.Second,
			WriteTimeout:        10 * time.Second,
			IdleTimeout:         60 * time.Second,
			ShutdownTimeout:     10 * time.Second,
			MaxRequestBodyBytes: 64 << 10,
		},
		Registry: RegistryConfig{
			ServiceTTL:       30 * time.Second,
			CleanupInterval:  5 * time.Second,
			MaxNameLength:    discovery.DefaultMaxNameLength,
			MaxAddressLength: discovery.DefaultMaxAddressLength,
			MaxInstances:     10000,
		},
		Storage: StorageConfig{
			Backend: StorageMemory,
		},
		RateLimit: RateLimitConfig{
			RegistrationsPerSecond: 20,
			RegistrationBurst:      200,
			AuthFailuresPerSecond:  1,
			AuthFailureBurst:       20,
		},
		Metrics: MetricsConfig{
			Enabled: true,
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
	// Each check reads every file, so sub-second intervals would only add
	// load; zero means off.
	check(server.ReloadInterval == 0 || server.ReloadInterval >= time.Second,
		"server.reload_interval must be 0s (off) or at least 1s, got %s", server.ReloadInterval)

	tls := config.TLS
	check((tls.CertFile == "") == (tls.KeyFile == ""),
		"tls.cert_file and tls.key_file must be set together, got cert_file %q and key_file %q", tls.CertFile, tls.KeyFile)
	// Client certificates only arrive over TLS, and only the per-route
	// checks of authentication decide what a certificate may do.
	check(tls.ClientCAFile == "" || tls.Enabled(),
		"tls.client_ca_file needs tls.cert_file and tls.key_file, since client certificates are only sent over TLS")
	check(tls.ClientCAFile == "" || config.Auth.Enabled,
		"tls.client_ca_file needs auth.enabled = true, since authentication decides what each certificate may do")

	check(!config.Auth.Enabled || config.Auth.TokensFile != "",
		"auth.tokens_file must be set when auth.enabled is true")

	registry := config.Registry
	// Clients are told the TTL as ttl_seconds when they register, so it must
	// be a whole number of seconds for that to be exact.
	check(registry.ServiceTTL >= time.Second, "registry.service_ttl must be at least 1s, got %s", registry.ServiceTTL)
	check(registry.ServiceTTL%time.Second == 0,
		"registry.service_ttl must be a whole number of seconds, such as \"30s\" or \"1m\", got %s", registry.ServiceTTL)
	check(registry.CleanupInterval > 0, "registry.cleanup_interval must be positive, got %s", registry.CleanupInterval)
	check(registry.MaxInstances > 0, "registry.max_instances must be positive, got %d", registry.MaxInstances)
	// A stale instance stays visible for up to TTL plus one interval, so an
	// interval longer than the TTL would more than double that window.
	check(registry.CleanupInterval <= registry.ServiceTTL,
		"registry.cleanup_interval (%s) must not be longer than registry.service_ttl (%s)",
		registry.CleanupInterval, registry.ServiceTTL)
	check(registry.MaxNameLength >= 1 && registry.MaxNameLength <= discovery.MaxLengthLimit,
		"registry.max_name_length must be between 1 and %d, got %d", discovery.MaxLengthLimit, registry.MaxNameLength)
	check(registry.MaxAddressLength >= 1 && registry.MaxAddressLength <= discovery.MaxLengthLimit,
		"registry.max_address_length must be between 1 and %d, got %d", discovery.MaxLengthLimit, registry.MaxAddressLength)
	check(registry.MaxInstancesPerOwner >= 0 && registry.MaxInstancesPerOwner <= registry.MaxInstances,
		"registry.max_instances_per_owner must be between 0 (no limit) and registry.max_instances (%d), got %d",
		registry.MaxInstances, registry.MaxInstancesPerOwner)

	limits := config.RateLimit
	checkRate := func(key string, perSecond float64, burstKey string, burst int) {
		check(perSecond >= 0 && !math.IsInf(perSecond, 0) && !math.IsNaN(perSecond),
			"rate_limit.%s must be 0 (off) or a positive number, got %v", key, perSecond)
		check(perSecond <= 0 || burst >= 1,
			"rate_limit.%s must be at least 1 while rate_limit.%s is on, got %d", burstKey, key, burst)
	}
	checkRate("registrations_per_second", limits.RegistrationsPerSecond, "registration_burst", limits.RegistrationBurst)
	checkRate("auth_failures_per_second", limits.AuthFailuresPerSecond, "auth_failure_burst", limits.AuthFailureBurst)

	check(slices.Contains(storageBackends, config.Storage.Backend),
		"storage.backend must be one of %s, got %q", strings.Join(storageBackends, ", "), config.Storage.Backend)

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
