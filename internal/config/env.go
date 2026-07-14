package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// env returns the trimmed value of key, or fallback when unset or blank.
func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// envList parses a comma-separated value into a slice, dropping blank entries.
func envList(key string, fallback []string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

// The must* helpers append a parse error to errs rather than returning it, so
// Load can report every bad variable in one pass instead of one per restart.

func mustInt(errs *[]error, key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %q is not an integer", key, raw))
		return fallback
	}
	return v
}

func mustBool(errs *[]error, key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %q is not a boolean", key, raw))
		return fallback
	}
	return v
}

func mustDuration(errs *[]error, key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %q is not a duration (e.g. 10s, 5m, 1h)", key, raw))
		return fallback
	}
	return v
}
