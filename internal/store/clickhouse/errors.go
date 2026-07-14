package clickhouse

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// ErrorRow is one row of the `errors` table. The field order is irrelevant —
// the column list in the INSERT is what binds them.
type ErrorRow struct {
	ProjectID  uint64
	GroupHash  uint64
	EventID    uuid.UUID
	Timestamp  time.Time
	ReceivedAt time.Time

	Level          string
	Environment    string
	Release        string
	Platform       string
	ExceptionType  string
	ExceptionValue string
	Culprit        string

	TraceID uuid.UUID
	SpanID  uint64

	UserID     string
	UserEmail  string
	UserIP     netip.Addr
	GeoCountry string

	Browser    string
	OS         string
	SDKName    string
	SDKVersion string

	Tags map[string]string

	Stacktrace  string
	Breadcrumbs string
	Contexts    string
}

// insertErrors is the column list. Written out rather than relying on positional
// order, so adding a column to the table cannot silently shift every value one
// place to the left.
const insertErrors = `INSERT INTO errors (
	project_id, group_hash, event_id, timestamp, received_at,
	level, environment, release, platform,
	exception_type, exception_value, culprit,
	trace_id, span_id,
	user_id, user_email, user_ip, geo_country,
	browser, os, sdk_name, sdk_version,
	tags, stacktrace, breadcrumbs, contexts
)`

// InsertErrors writes a batch of error rows.
//
// A batch, never one row at a time: every INSERT creates a part, and per-row
// inserts would produce thousands of tiny parts, overwhelm the background merge
// process and eventually make ClickHouse refuse writes altogether
// (insert-batch-size, impact CRITICAL). The batching itself lives in
// processor.Writer, which decides *when* to call this.
func (db *DB) InsertErrors(ctx context.Context, rows []ErrorRow) error {
	if len(rows) == 0 {
		return nil
	}

	batch, err := db.PrepareBatch(ctx, insertErrors)
	if err != nil {
		return fmt.Errorf("prepare errors batch: %w", err)
	}
	// Close releases the connection the batch holds. Without it, a failure
	// partway through leaks a connection out of the pool every time.
	defer func() { _ = batch.Close() }()

	for i, row := range rows {
		if err := batch.Append(
			row.ProjectID, row.GroupHash, row.EventID, row.Timestamp, row.ReceivedAt,
			row.Level, row.Environment, row.Release, row.Platform,
			row.ExceptionType, row.ExceptionValue, row.Culprit,
			row.TraceID, row.SpanID,
			row.UserID, row.UserEmail, ipOrZero(row.UserIP), row.GeoCountry,
			row.Browser, row.OS, row.SDKName, row.SDKVersion,
			row.Tags, row.Stacktrace, row.Breadcrumbs, row.Contexts,
		); err != nil {
			return fmt.Errorf("append row %d/%d: %w", i+1, len(rows), err)
		}
	}

	if err := batch.Send(); err != nil {
		return fmt.Errorf("send errors batch of %d: %w", len(rows), err)
	}
	return nil
}

// ipOrZero renders an address for the IPv6 column. The column is not Nullable
// (schema-types-avoid-nullable), so a missing address is stored as :: rather
// than costing every row an extra null-tracking byte.
func ipOrZero(addr netip.Addr) netip.Addr {
	if !addr.IsValid() {
		return netip.IPv6Unspecified()
	}
	// IPv4 is stored mapped into the IPv6 column, which is what lets one column
	// hold both families.
	if addr.Is4() {
		return netip.AddrFrom16(addr.As16())
	}
	return addr
}
