package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDotenvLine(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantKey   string
		wantValue string
		wantOK    bool
	}{
		{name: "simple", line: "KEY=value", wantKey: "KEY", wantValue: "value", wantOK: true},
		{name: "spaces around equals", line: "  KEY = value  ", wantKey: "KEY", wantValue: "value", wantOK: true},
		{name: "export prefix", line: "export KEY=value", wantKey: "KEY", wantValue: "value", wantOK: true},
		{name: "empty value", line: "KEY=", wantKey: "KEY", wantValue: "", wantOK: true},
		{name: "comment", line: "# a note", wantOK: false},
		{name: "blank", line: "   ", wantOK: false},
		{name: "no equals sign", line: "JUST_A_WORD", wantOK: false},
		{
			// A DSN is the reason quoting matters: it is full of characters
			// that a naive parser would treat as syntax.
			name:      "dsn with special characters",
			line:      "SABAB_POSTGRES_DSN=postgres://sabab:sabab@localhost:5432/sabab?sslmode=disable",
			wantKey:   "SABAB_POSTGRES_DSN",
			wantValue: "postgres://sabab:sabab@localhost:5432/sabab?sslmode=disable",
			wantOK:    true,
		},
		{
			name:      "inline comment is stripped from an unquoted value",
			line:      "PORT=5432 # the default",
			wantKey:   "PORT",
			wantValue: "5432",
			wantOK:    true,
		},
		{
			// ...but a '#' inside a quoted value is data, not a comment. A
			// password is exactly where this bites.
			name:      "hash inside a quoted value is kept",
			line:      `PASSWORD="p#ss word"`,
			wantKey:   "PASSWORD",
			wantValue: "p#ss word",
			wantOK:    true,
		},
		{
			name:      "single quotes are stripped",
			line:      `KEY='value'`,
			wantKey:   "KEY",
			wantValue: "value",
			wantOK:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key, value, ok := parseDotenvLine(tc.line)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if key != tc.wantKey || value != tc.wantValue {
				t.Errorf("got (%q, %q), want (%q, %q)", key, value, tc.wantKey, tc.wantValue)
			}
		})
	}
}

// The real environment must always beat the file. A deploy that passes a
// DATABASE_URL cannot have it silently overridden by a stale .env in the image.
func TestLoadDotenvDoesNotOverrideRealEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "SABAB_TEST_PRESET=from_file\nSABAB_TEST_UNSET=from_file\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SABAB_TEST_PRESET", "from_env")

	if err := loadDotenv(path); err != nil {
		t.Fatalf("loadDotenv: %v", err)
	}
	if got := os.Getenv("SABAB_TEST_PRESET"); got != "from_env" {
		t.Errorf("preset var: got %q, want it left as %q", got, "from_env")
	}
	if got := os.Getenv("SABAB_TEST_UNSET"); got != "from_file" {
		t.Errorf("unset var: got %q, want %q", got, "from_file")
	}
	// Not leaked into the rest of the suite.
	t.Cleanup(func() { os.Unsetenv("SABAB_TEST_UNSET") })
}

// Production ships no .env; a missing file must not be an error.
func TestLoadDotenvMissingFileIsFine(t *testing.T) {
	if err := loadDotenv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Fatalf("want nil for a missing file, got %v", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("SABAB_ENV_FILE", filepath.Join(t.TempDir(), "absent.env"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env != "development" {
		t.Errorf("Env = %q, want development", cfg.Env)
	}
	if cfg.Processor.ConsumerName == "" {
		t.Error("ConsumerName must default to the hostname, not empty")
	}
	if len(cfg.ClickHouse.Addr) == 0 {
		t.Error("ClickHouse.Addr must have a default")
	}
}

// A malformed value must fail at boot, loudly — not ten seconds later as a
// confusing connection error.
func TestLoadReportsEveryBadValueAtOnce(t *testing.T) {
	t.Setenv("SABAB_ENV_FILE", filepath.Join(t.TempDir(), "absent.env"))
	t.Setenv("SABAB_PROCESSOR_BATCH_SIZE", "lots")
	t.Setenv("SABAB_POSTGRES_MAX_CONN_LIFETIME", "forever")
	t.Setenv("SABAB_LOG_LEVEL", "shouty")

	_, err := Load()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	// All three problems should be reported together, so a misconfigured deploy
	// takes one restart to fix rather than three.
	for _, want := range []string{"SABAB_PROCESSOR_BATCH_SIZE", "SABAB_POSTGRES_MAX_CONN_LIFETIME", "SABAB_LOG_LEVEL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s; got: %v", want, err)
		}
	}
}
