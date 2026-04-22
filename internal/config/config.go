package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds all server and ArgoCD client settings.
type Config struct {
	Port         int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration

	ArgoTLSSkipVerify bool
	ArgoHTTPTimeout   time.Duration
	ArgoMaxApps       int
}

// Load returns a Config populated from environment variables, falling back to
// sensible defaults. No external config file is required.
func Load() Config {
	return Config{
		Port:         envInt("PORT", 8080),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,

		ArgoTLSSkipVerify: envBool("ARGOCD_TLS_SKIP_VERIFY", false),
		ArgoHTTPTimeout:   envDuration("ARGOCD_HTTP_TIMEOUT", 15*time.Second),
		ArgoMaxApps:       envInt("ARGOCD_MAX_APPS", 500),
	}
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
