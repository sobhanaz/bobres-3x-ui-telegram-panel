// Package config loads service configuration from environment variables.
// Secrets are only read from the environment (or *_FILE paths), never from git.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Common holds settings shared by every service.
type Common struct {
	Service     string
	Env         string // dev | staging | prod
	LogLevel    string // debug | info | warn | error
	LogFormat   string // json | text
	HTTPAddr    string
	DatabaseURL string
	RedisURL    string
	// ServiceToken authenticates internal service-to-service calls.
	ServiceToken string
	ShutdownWait time.Duration
}

// Load reads the shared configuration for the named service.
func Load(service string) (Common, error) {
	c := Common{
		Service:      service,
		Env:          getenv("BOBRES_ENV", "dev"),
		LogLevel:     getenv("BOBRES_LOG_LEVEL", "info"),
		LogFormat:    getenv("BOBRES_LOG_FORMAT", "json"),
		HTTPAddr:     getenv("BOBRES_HTTP_ADDR", ":8080"),
		DatabaseURL:  Secret("BOBRES_DATABASE_URL"),
		RedisURL:     Secret("BOBRES_REDIS_URL"),
		ServiceToken: Secret("BOBRES_SERVICE_TOKEN"),
		ShutdownWait: getDuration("BOBRES_SHUTDOWN_WAIT", 15*time.Second),
	}
	return c, c.Validate()
}

// Validate checks values that would otherwise fail later and less clearly.
func (c Common) Validate() error {
	var errs []error
	switch c.Env {
	case "dev", "staging", "prod":
	default:
		errs = append(errs, fmt.Errorf("BOBRES_ENV must be dev|staging|prod, got %q", c.Env))
	}
	switch c.LogFormat {
	case "json", "text":
	default:
		errs = append(errs, fmt.Errorf("BOBRES_LOG_FORMAT must be json|text, got %q", c.LogFormat))
	}
	if c.Env == "prod" && len(c.ServiceToken) < 32 {
		errs = append(errs, errors.New("BOBRES_SERVICE_TOKEN must be at least 32 characters in prod"))
	}
	return errors.Join(errs...)
}

// Secret returns the value of NAME, or the trimmed contents of the file named
// by NAME_FILE (Docker/Kubernetes secrets). NAME_FILE wins when both are set.
func Secret(name string) string {
	if path := os.Getenv(name + "_FILE"); path != "" {
		if b, err := os.ReadFile(path); err == nil { //nolint:gosec // path comes from operator-controlled env
			return strings.TrimSpace(string(b))
		}
	}
	return os.Getenv(name)
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	return def
}
