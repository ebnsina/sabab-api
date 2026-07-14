// Package migrate applies ordered .sql migrations to Postgres and ClickHouse.
//
// Both databases are versioned by the same rules: files are named
// NNNN_description.sql, applied in filename order, recorded in a
// schema_migrations table, and never re-applied. A migration whose contents
// changed after it was applied is a hard error — silently diverging schemas
// between a developer's laptop and production is not a failure mode we accept.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Migration is a single .sql file to apply.
type Migration struct {
	Version  string // "0001"
	Name     string // "init"
	Filename string // "0001_init.sql"
	SQL      string
	Checksum string // sha256 of SQL, hex-encoded
}

// Driver is the database-specific half of the runner.
type Driver interface {
	// Name identifies the target in logs and errors ("postgres", "clickhouse").
	Name() string
	// EnsureVersionTable creates schema_migrations if it does not exist.
	EnsureVersionTable(ctx context.Context) error
	// AppliedVersions returns version -> checksum for every applied migration.
	AppliedVersions(ctx context.Context) (map[string]string, error)
	// Apply runs one migration and records it. Implementations make this
	// atomic where the engine allows it (Postgres does; ClickHouse does not).
	Apply(ctx context.Context, m Migration) error
	// Lock serialises concurrent runners. The returned func releases the lock.
	Lock(ctx context.Context) (unlock func(context.Context) error, err error)
}

// filenamePattern enforces the NNNN_description.sql convention. Rejecting
// anything else keeps ordering total and unambiguous.
var filenamePattern = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// ErrChecksumMismatch reports that an already-applied migration file has been
// edited since it ran.
var ErrChecksumMismatch = errors.New("migration checksum mismatch")

// Load reads and validates every migration in dir of fsys, ordered by version.
func Load(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir %q: %w", dir, err)
	}

	var migrations []Migration
	seen := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		match := filenamePattern.FindStringSubmatch(name)
		if match == nil {
			return nil, fmt.Errorf("migration %q: name must match NNNN_description.sql", name)
		}
		version := match[1]
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("migration version %s used twice: %q and %q", version, prev, name)
		}
		seen[version] = name

		raw, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}
		sql := string(raw)
		if strings.TrimSpace(sql) == "" {
			return nil, fmt.Errorf("migration %q is empty", name)
		}
		sum := sha256.Sum256(raw)

		migrations = append(migrations, Migration{
			Version:  version,
			Name:     match[2],
			Filename: name,
			SQL:      sql,
			Checksum: hex.EncodeToString(sum[:]),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})
	return migrations, nil
}

// Run applies every migration not yet recorded by the driver. It is safe to
// call on an up-to-date database: it becomes a no-op.
func Run(ctx context.Context, d Driver, migrations []Migration, log *slog.Logger) error {
	log = log.With(slog.String("target", d.Name()))

	unlock, err := d.Lock(ctx)
	if err != nil {
		return fmt.Errorf("%s: acquire migration lock: %w", d.Name(), err)
	}
	defer func() {
		// Releasing the lock must not be skipped because ctx was cancelled.
		if err := unlock(context.WithoutCancel(ctx)); err != nil {
			log.Error("release migration lock", slog.Any("error", err))
		}
	}()

	if err := d.EnsureVersionTable(ctx); err != nil {
		return fmt.Errorf("%s: ensure version table: %w", d.Name(), err)
	}
	applied, err := d.AppliedVersions(ctx)
	if err != nil {
		return fmt.Errorf("%s: read applied versions: %w", d.Name(), err)
	}

	pending := 0
	for _, m := range migrations {
		if checksum, done := applied[m.Version]; done {
			if checksum != m.Checksum {
				return fmt.Errorf("%s: %w: %s was applied with a different body; "+
					"migrations are immutable — add a new one instead",
					d.Name(), ErrChecksumMismatch, m.Filename)
			}
			continue
		}
		log.Info("applying migration", slog.String("migration", m.Filename))
		if err := d.Apply(ctx, m); err != nil {
			return fmt.Errorf("%s: apply %s: %w", d.Name(), m.Filename, err)
		}
		pending++
	}

	if pending == 0 {
		log.Info("schema up to date", slog.Int("applied", len(applied)))
	} else {
		log.Info("migrations applied", slog.Int("count", pending))
	}
	return nil
}
