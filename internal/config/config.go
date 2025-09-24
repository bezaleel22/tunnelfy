package config

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all the configuration for the application.
type Config struct {
	Zone           string
	SSHListen      string
	HTTPListen     string
	AuthorizedKeys string
	LogRequests    bool
}

// Load loads the configuration from environment variables or a .env file.
func Load() (*Config, error) {
	// Load .env if present
	_ = godotenv.Load()

	cfg := &Config{
		Zone:           getenvOrDefault("ZONE", "example.com"),
		SSHListen:      getenvOrDefault("SSH_LISTEN", ":2222"),
		HTTPListen:     getenvOrDefault("HTTP_LISTEN", ":8080"),
		AuthorizedKeys: os.Getenv("AUTHORIZED_KEYS_DATA"),
		LogRequests:    strings.ToLower(os.Getenv("LOG_REQUESTS")) != "false",
	}

	if cfg.AuthorizedKeys == "" {
		// Instead of fatal, return an error to let the caller handle it
		return nil, &ConfigError{Message: "AUTHORIZED_KEYS_DATA must be set (newline-separated authorized public keys)"}
	}

	return cfg, nil
}

// getenvOrDefault is a helper to get an environment variable or a default value.
func getenvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ConfigError represents a configuration loading error.
type ConfigError struct {
	Message string
}

func (e *ConfigError) Error() string {
	return e.Message
}
