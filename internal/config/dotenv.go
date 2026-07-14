package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// EnvFile is the dotenv file loaded at startup unless SABAB_ENV_FILE overrides it.
const EnvFile = ".env"

// loadDotenv reads KEY=VALUE pairs from path into the process environment.
//
// A variable already set in the real environment always wins: the file is a
// convenience for local development, and it must never quietly override what a
// deploy explicitly passed in. A missing file is not an error — production sets
// real environment variables and ships no .env at all.
//
// This is deliberately not a dependency. The format we need is a few dozen
// lines, and the same file has to be readable by docker compose, which supports
// only this subset anyway.
func loadDotenv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		key, value, ok := parseDotenvLine(scanner.Text())
		if !ok {
			continue
		}
		if key == "" {
			return fmt.Errorf("%s:%d: missing key before '='", path, line)
		}
		// Real environment wins.
		if _, set := os.LookupEnv(key); set {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("%s:%d: set %s: %w", path, line, key, err)
		}
	}
	return scanner.Err()
}

// parseDotenvLine splits one line into a key and value. ok is false for blank
// lines and comments.
func parseDotenvLine(raw string) (key, value string, ok bool) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")

	key, value, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)

	// Quoted values keep their inner whitespace; unquoted ones lose a trailing
	// inline comment, which is how compose reads them too.
	switch {
	case len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`):
		value = strings.ReplaceAll(value[1:len(value)-1], `\n`, "\n")
	case len(value) >= 2 && strings.HasPrefix(value, `'`) && strings.HasSuffix(value, `'`):
		value = value[1 : len(value)-1]
	default:
		if hash := strings.Index(value, " #"); hash >= 0 {
			value = strings.TrimSpace(value[:hash])
		}
	}
	return key, value, true
}
