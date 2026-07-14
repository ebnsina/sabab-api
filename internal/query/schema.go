package query

import (
	"fmt"
	"strings"
)

// Kind is how a field's values are interpreted and compared.
type Kind int

const (
	// KindString compares literally. Supports = and !=.
	KindString Kind = iota
	// KindNumber supports the ordering operators.
	KindNumber
	// KindDuration parses "500ms", "1.5s" into a number of nanoseconds, so a
	// user never has to know what unit we store internally.
	KindDuration
	// KindTime parses timestamps and relative ages like "-24h".
	KindTime
	// KindMap addresses a ClickHouse Map column: "tenant:acme" becomes
	// tags['tenant'] = 'acme'.
	KindMap
)

// Field is one queryable column.
type Field struct {
	// Column is the SQL column. It comes from this table and NEVER from user
	// input, which is what makes the compiler injection-safe by construction.
	Column string
	Kind   Kind
	// MapColumn is set for the catch-all field that absorbs unknown keys, so
	// "tenant:acme" can be routed to tags['tenant'] without tags having to
	// enumerate every key a customer might invent.
	MapColumn string
}

// Schema is the set of fields queryable on one signal. One per table; the
// parser and compiler are shared.
type Schema struct {
	// Table is the ClickHouse table this schema targets.
	Table string
	// Fields is the allowlist. A field not in here is rejected with a message
	// naming what IS available — a silent "no results" for a typo'd field name
	// is the most infuriating possible behaviour for a search box.
	Fields map[string]Field
	// FreeTextColumns are searched by a bare word with no field.
	FreeTextColumns []string
	// CatchAll routes unknown field names into a Map column. Without it,
	// "tenant:acme" would be an error, and users cannot be expected to declare
	// their tags to us first.
	CatchAll string
}

// Errors is the schema for the errors table.
var Errors = Schema{
	Table: "errors",
	Fields: map[string]Field{
		"level":       {Column: "level", Kind: KindString},
		"environment": {Column: "environment", Kind: KindString},
		"release":     {Column: "release", Kind: KindString},
		"platform":    {Column: "platform", Kind: KindString},
		"type":        {Column: "exception_type", Kind: KindString},
		"value":       {Column: "exception_value", Kind: KindString},
		"culprit":     {Column: "culprit", Kind: KindString},
		"browser":     {Column: "browser", Kind: KindString},
		"os":          {Column: "os", Kind: KindString},
		"country":     {Column: "geo_country", Kind: KindString},
		"sdk":         {Column: "sdk_name", Kind: KindString},
		"user.id":     {Column: "user_id", Kind: KindString},
		"user.email":  {Column: "user_email", Kind: KindString},
		"trace":       {Column: "trace_id", Kind: KindString},
		"timestamp":   {Column: "timestamp", Kind: KindTime},
		"tags":        {Column: "tags", Kind: KindMap},
	},
	FreeTextColumns: []string{"exception_value", "culprit"},
	CatchAll:        "tags",
}

// Lookup resolves a field name, routing anything unknown to the catch-all map.
func (s Schema) Lookup(name string) (Field, string, error) {
	name = strings.TrimSpace(name)

	// "tags.tenant" and "tenant" both mean tags['tenant'].
	if base, key, found := strings.Cut(name, "."); found {
		if field, ok := s.Fields[name]; ok {
			// A dotted field that IS declared (user.email) wins over the map
			// interpretation.
			return field, "", nil
		}
		if field, ok := s.Fields[base]; ok && field.Kind == KindMap {
			return field, key, nil
		}
	}

	if field, ok := s.Fields[name]; ok {
		return field, "", nil
	}

	// Unknown: treat it as a tag key rather than rejecting it, because a user's
	// tags are theirs to invent and we cannot know them in advance.
	if s.CatchAll != "" {
		if field, ok := s.Fields[s.CatchAll]; ok {
			return field, name, nil
		}
	}
	return Field{}, "", fmt.Errorf("unknown field %q", name)
}
