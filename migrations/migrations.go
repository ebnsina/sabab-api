// Package migrations embeds the .sql files so every binary that can talk to a
// database also carries the schema it expects. No separate migration image, and
// no chance of a container running against a schema it was not built for.
package migrations

import "embed"

//go:embed postgres/*.sql clickhouse/*.sql
var FS embed.FS

// Directory names inside FS.
const (
	PostgresDir   = "postgres"
	ClickHouseDir = "clickhouse"
)
