package api

import (
	"net/http"
	"strings"

	"github.com/ebnsina/sabab-api/internal/query"
)

// requestEnvironment reads the ?environment= filter. Empty means "all
// environments" — the default across every list.
func requestEnvironment(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("environment"))
}

// applyEnvironment narrows a compiled DSL query to a single environment when the
// request carries ?environment=. Errors, logs and spans all share the
// LowCardinality `environment` column, so one bound predicate works everywhere
// the DSL runs — and, being a bound parameter, it stays injection-safe like the
// rest of the compiled query.
func applyEnvironment(r *http.Request, sql query.SQL) query.SQL {
	env := requestEnvironment(r)
	if env == "" {
		return sql
	}
	sql.Where += " AND environment = ?"
	sql.Args = append(append([]any{}, sql.Args...), env)
	return sql
}
