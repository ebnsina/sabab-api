package query

import (
	"fmt"
	"strings"
)

// SQL is a compiled WHERE clause and its bound arguments.
type SQL struct {
	// Where is a SQL fragment containing only `?` placeholders — never a user
	// value, and never a column name that did not come from the schema
	// allowlist. Those two rules together are what make this injection-safe by
	// construction rather than by careful escaping.
	Where string
	Args  []any
}

// Compile turns a parsed query into SQL for the given schema.
//
// projectID and the time range are always applied, and are NOT optional: the
// ORDER BY of every event table starts with (project_id, hour bucket), so a
// query without them is a full table scan — and one without project_id would
// also read another tenant's data.
func Compile(q *Query, schema Schema, projectID uint64, from, to any) (SQL, error) {
	clauses := []string{"project_id = ?", "timestamp >= ?", "timestamp < ?"}
	args := []any{projectID, from, to}

	for _, cond := range q.Conditions {
		clause, condArgs, err := compileCondition(cond)
		if err != nil {
			return SQL{}, err
		}
		clauses = append(clauses, clause)
		args = append(args, condArgs...)
	}

	if len(q.FreeText) > 0 {
		clause, textArgs, err := compileFreeText(q.FreeText, schema)
		if err != nil {
			return SQL{}, err
		}
		clauses = append(clauses, clause)
		args = append(args, textArgs...)
	}

	return SQL{Where: strings.Join(clauses, " AND "), Args: args}, nil
}

func compileCondition(cond Condition) (string, []any, error) {
	// The column is from the schema, so interpolating it is safe. The value
	// never is, so it is always a placeholder.
	column := cond.Field.Column

	if cond.Field.Kind == KindMap {
		if cond.MapKey == "" {
			return "", nil, fmt.Errorf("%q needs a key, e.g. tags.tenant:acme", column)
		}
		// The map KEY is user input, so it is bound too — not interpolated into
		// the subscript.
		switch cond.Op {
		case OpNe:
			return fmt.Sprintf("%s[?] != ?", column), []any{cond.MapKey, cond.Value}, nil
		default:
			return fmt.Sprintf("%s[?] = ?", column), []any{cond.MapKey, cond.Value}, nil
		}
	}

	op := cond.Op
	if cond.Negated && op != OpNe {
		// "!duration:>500ms" means NOT(duration > 500ms). Expressed by wrapping
		// rather than by flipping the operator, because flipping > to <= silently
		// changes how NULL-ish values behave.
		clause := fmt.Sprintf("NOT (%s %s ?)", column, op)
		return clause, []any{cond.Value}, nil
	}

	return fmt.Sprintf("%s %s ?", column, op), []any{cond.Value}, nil
}

// compileFreeText matches a bare word against the schema's text columns.
//
// positionCaseInsensitive rather than LIKE: it is what the tokenbf_v1 skip index
// on exception_value can actually serve, so a text search stays a granule skip
// instead of becoming a full scan.
func compileFreeText(terms []string, schema Schema) (string, []any, error) {
	if len(schema.FreeTextColumns) == 0 {
		return "", nil, fmt.Errorf("free-text search is not supported on %s", schema.Table)
	}

	var (
		clauses []string
		args    []any
	)
	for _, term := range terms {
		var perColumn []string
		for _, column := range schema.FreeTextColumns {
			perColumn = append(perColumn, fmt.Sprintf("positionCaseInsensitive(%s, ?) > 0", column))
			args = append(args, term)
		}
		// Every term must appear SOMEWHERE (AND across terms, OR across
		// columns) — which is how people expect a search box to behave.
		clauses = append(clauses, "("+strings.Join(perColumn, " OR ")+")")
	}
	return strings.Join(clauses, " AND "), args, nil
}
