// Package clickhouse connects to the event plane.
//
// The event plane holds immutable, high-volume bodies — errors, logs, spans,
// metrics — queried almost exclusively in aggregate. Writes go through batches;
// never insert row-by-row on a hot path.
package clickhouse

import (
	"context"
	"errors"
	"fmt"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/ebnsina/sabab-api/internal/config"
)

// ErrNotFound is returned when a query matches no row.
var ErrNotFound = errors.New("not found")

// DB wraps a ClickHouse native-protocol connection pool.
type DB struct {
	driver.Conn
}

// Connect opens the pool over the native protocol and pings it. clickhouse-go
// dials lazily, so the ping is what turns a bad address into a boot failure.
func Connect(ctx context.Context, cfg config.ClickHouse) (*DB, error) {
	conn, err := ch.Open(&ch.Options{
		Addr: cfg.Addr,
		Auth: ch.Auth{
			Database: cfg.Database,
			Username: cfg.Username,
			Password: cfg.Password,
		},
		DialTimeout:     cfg.DialTimeout,
		MaxOpenConns:    cfg.MaxOpenConns,
		MaxIdleConns:    cfg.MaxIdleConns,
		ConnMaxLifetime: cfg.ConnMaxLifetime,
		Compression:     &ch.Compression{Method: ch.CompressionLZ4},
	})
	if err != nil {
		return nil, fmt.Errorf("open clickhouse: %w", err)
	}
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping clickhouse: %w", err)
	}
	return &DB{Conn: conn}, nil
}

// Ping satisfies health.Check.
func (db *DB) Ping(ctx context.Context) error {
	if err := db.Conn.Ping(ctx); err != nil {
		return fmt.Errorf("clickhouse unreachable: %w", err)
	}
	return nil
}

// Close shuts the pool down.
func (db *DB) Close() error { return db.Conn.Close() }
