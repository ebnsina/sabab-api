// Package postgres connects to the control plane.
//
// The control plane holds everything mutable and transactional: orgs, projects,
// ingest keys, issue state, alert rules, releases. It is low-volume by design —
// event bodies belong in ClickHouse.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ebnsina/sabab-api/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a pgx connection pool.
type DB struct {
	*pgxpool.Pool
}

// Connect opens the pool and verifies it with a ping, so a bad DSN fails at
// boot rather than on the first request.
func Connect(ctx context.Context, cfg config.Postgres) (*DB, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &DB{Pool: pool}, nil
}

// isNoRows reports whether err is "the query matched nothing", which for many
// of our lookups is an expected outcome rather than a failure.
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// Ping satisfies health.Check.
func (db *DB) Ping(ctx context.Context) error {
	if err := db.Pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres unreachable: %w", err)
	}
	return nil
}

// Close releases every pooled connection.
func (db *DB) Close() { db.Pool.Close() }

// encodeJSON marshals a value for a jsonb column.
func encodeJSON(v any) ([]byte, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode json: %w", err)
	}
	return body, nil
}
