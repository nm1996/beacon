package config

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// environmentVariable binds one BEACON_* variable to the setting it overrides.
type environmentVariable struct {
	name  string
	apply func(value string) error
}

// environmentVariables lists every supported variable. Each name is BEACON_
// followed by the setting's TOML key in upper case, with the dot replaced by
// an underscore.
func environmentVariables(config *Config) []environmentVariable {
	return []environmentVariable{
		{name: "BEACON_SERVER_HOST", apply: setString(&config.Server.Host)},
		{name: "BEACON_SERVER_PORT", apply: setInt(&config.Server.Port)},
		{name: "BEACON_SERVER_READ_TIMEOUT", apply: setDuration(&config.Server.ReadTimeout)},
		{name: "BEACON_SERVER_WRITE_TIMEOUT", apply: setDuration(&config.Server.WriteTimeout)},
		{name: "BEACON_SERVER_IDLE_TIMEOUT", apply: setDuration(&config.Server.IdleTimeout)},
		{name: "BEACON_SERVER_SHUTDOWN_TIMEOUT", apply: setDuration(&config.Server.ShutdownTimeout)},
		{name: "BEACON_SERVER_MAX_REQUEST_BODY_BYTES", apply: setInt64(&config.Server.MaxRequestBodyBytes)},
		{name: "BEACON_SERVER_RELOAD_INTERVAL", apply: setDuration(&config.Server.ReloadInterval)},
		{name: "BEACON_TLS_CERT_FILE", apply: setString(&config.TLS.CertFile)},
		{name: "BEACON_TLS_KEY_FILE", apply: setString(&config.TLS.KeyFile)},
		{name: "BEACON_TLS_CLIENT_CA_FILE", apply: setString(&config.TLS.ClientCAFile)},
		{name: "BEACON_AUTH_ENABLED", apply: setBool(&config.Auth.Enabled)},
		{name: "BEACON_AUTH_TOKENS_FILE", apply: setString(&config.Auth.TokensFile)},
		{name: "BEACON_REGISTRY_SERVICE_TTL", apply: setDuration(&config.Registry.ServiceTTL)},
		{name: "BEACON_REGISTRY_CLEANUP_INTERVAL", apply: setDuration(&config.Registry.CleanupInterval)},
		{name: "BEACON_REGISTRY_MAX_NAME_LENGTH", apply: setInt(&config.Registry.MaxNameLength)},
		{name: "BEACON_REGISTRY_MAX_ADDRESS_LENGTH", apply: setInt(&config.Registry.MaxAddressLength)},
		{name: "BEACON_REGISTRY_MAX_INSTANCES", apply: setInt(&config.Registry.MaxInstances)},
		{name: "BEACON_STORAGE_BACKEND", apply: setString(&config.Storage.Backend)},
		{name: "BEACON_METRICS_ENABLED", apply: setBool(&config.Metrics.Enabled)},
		{name: "BEACON_LOGGING_LEVEL", apply: setString(&config.Logging.Level)},
		{name: "BEACON_LOGGING_FORMAT", apply: setString(&config.Logging.Format)},
	}
}

// applyEnvironment overlays config with every variable that lookup reports as
// set, even to an empty value, and reports every value that cannot be parsed.
// Range checks are left to Validate.
func applyEnvironment(config *Config, lookup func(name string) (string, bool)) error {
	var problems []error

	for _, variable := range environmentVariables(config) {
		value, found := lookup(variable.name)
		if !found {
			continue
		}

		if err := variable.apply(value); err != nil {
			problems = append(problems, fmt.Errorf("%s %w, got %q", variable.name, err, value))
		}
	}

	return errors.Join(problems...)
}

func setString(target *string) func(string) error {
	return func(value string) error {
		*target = value
		return nil
	}
}

func setInt(target *int) func(string) error {
	return func(value string) error {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return errors.New("must be an integer")
		}

		*target = parsed
		return nil
	}
}

func setInt64(target *int64) func(string) error {
	return func(value string) error {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return errors.New("must be an integer")
		}

		*target = parsed
		return nil
	}
}

func setBool(target *bool) func(string) error {
	return func(value string) error {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return errors.New("must be true or false")
		}

		*target = parsed
		return nil
	}
}

func setDuration(target *time.Duration) func(string) error {
	return func(value string) error {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return errors.New("must be a duration such as 500ms, 5s or 1m30s")
		}

		*target = parsed
		return nil
	}
}
