// Package config loads service configuration from environment variables.
// Secrets are only read from the environment (or *_FILE paths), never from git.
// Defaults fail closed: an unset BOBRES_ENV means prod, and placeholder or
// empty secrets are rejected.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
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
	GRPCAddr    string
	DatabaseURL string
	RedisURL    string
	// ServiceToken authenticates internal service-to-service calls.
	ServiceToken string
	ShutdownWait time.Duration
}

// LogValue redacts secrets so logging a Common value can never leak them.
func (c Common) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("service", c.Service),
		slog.String("env", c.Env),
		slog.String("http_addr", c.HTTPAddr),
		slog.Bool("database_url_set", c.DatabaseURL != ""),
		slog.Bool("redis_url_set", c.RedisURL != ""),
		slog.Bool("service_token_set", c.ServiceToken != ""),
	)
}

// String never prints secrets either (covers %v and %+v formatting).
func (c Common) String() string { return fmt.Sprint(c.LogValue()) }

// Load reads the shared configuration for the named service.
func Load(service string) (Common, error) {
	var errs []error
	get := func(name string) string {
		v, err := Secret(name)
		if err != nil {
			errs = append(errs, err)
		}
		return v
	}
	c := Common{
		Service:      service,
		Env:          getenv("BOBRES_ENV", "prod"),
		LogLevel:     getenv("BOBRES_LOG_LEVEL", "info"),
		LogFormat:    getenv("BOBRES_LOG_FORMAT", "json"),
		HTTPAddr:     getenv("BOBRES_HTTP_ADDR", ":8080"),
		GRPCAddr:     getenv("BOBRES_GRPC_ADDR", ":9090"),
		DatabaseURL:  get("BOBRES_DATABASE_URL"),
		RedisURL:     get("BOBRES_REDIS_URL"),
		ServiceToken: get("BOBRES_SERVICE_TOKEN"),
		ShutdownWait: getDuration("BOBRES_SHUTDOWN_WAIT", 15*time.Second),
	}
	errs = append(errs, c.Validate())
	return c, errors.Join(errs...)
}

const placeholderMarker = "CHANGE_ME"

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
	minToken := 16
	if c.Env == "prod" {
		minToken = 32
	}
	if len(c.ServiceToken) < minToken {
		errs = append(errs, fmt.Errorf("BOBRES_SERVICE_TOKEN must be at least %d characters in %s", minToken, c.Env))
	}
	for name, v := range map[string]string{
		"BOBRES_SERVICE_TOKEN": c.ServiceToken, "BOBRES_DATABASE_URL": c.DatabaseURL, "BOBRES_REDIS_URL": c.RedisURL,
	} {
		if strings.Contains(v, placeholderMarker) {
			errs = append(errs, fmt.Errorf("%s still contains the %s placeholder", name, placeholderMarker))
		}
	}
	if c.ShutdownWait <= 0 {
		errs = append(errs, errors.New("BOBRES_SHUTDOWN_WAIT must be positive"))
	}
	return errors.Join(errs...)
}

// Secret returns the value of NAME, or the trimmed contents of the file named
// by NAME_FILE (Docker/Kubernetes secrets). NAME_FILE wins when set, and if it
// cannot be read or is empty that is an error: it never falls back to NAME.
func Secret(name string) (string, error) {
	path := os.Getenv(name + "_FILE")
	if path == "" {
		return os.Getenv(name), nil
	}
	b, err := os.ReadFile(path) //nolint:gosec // path comes from operator-controlled env
	if err != nil {
		return "", fmt.Errorf("%s_FILE: cannot read secret file: %w", name, errors.Unwrap(err))
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("%s_FILE: secret file is empty", name)
	}
	return v, nil
}

// HTTPAddrFromEnv returns the listen address without loading or validating
// secrets; the container healthcheck only needs this.
func HTTPAddrFromEnv() string { return getenv("BOBRES_HTTP_ADDR", ":8080") }

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// getDuration accepts "15s" style values or plain seconds. Invalid or
// non-positive values fall back to def.
func getDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		var n int
		if _, serr := fmt.Sscanf(v, "%d", &n); serr != nil || fmt.Sprint(n) != v {
			return def
		}
		d = time.Duration(n) * time.Second
	}
	if d <= 0 {
		return def
	}
	return d
}
