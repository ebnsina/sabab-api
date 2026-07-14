package query

import (
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, input string) *Query {
	t.Helper()
	q, err := Parse(input, Errors)
	if err != nil {
		t.Fatalf("Parse(%q): %v", input, err)
	}
	return q
}

func TestParseFieldValuePairs(t *testing.T) {
	q := mustParse(t, "level:error release:web@2.4.1")

	if len(q.Conditions) != 2 {
		t.Fatalf("want 2 conditions, got %d", len(q.Conditions))
	}
	if q.Conditions[0].Field.Column != "level" || q.Conditions[0].Value != "error" {
		t.Errorf("condition 0 = %+v", q.Conditions[0])
	}
	// The colon inside "web@2.4.1" must not be read as a second separator, and
	// the "." must not turn it into a map key.
	if q.Conditions[1].Field.Column != "release" || q.Conditions[1].Value != "web@2.4.1" {
		t.Errorf("condition 1 = %+v", q.Conditions[1])
	}
}

func TestParseNegation(t *testing.T) {
	for _, input := range []string{"!browser:Safari", "-browser:Safari"} {
		q := mustParse(t, input)
		if len(q.Conditions) != 1 {
			t.Fatalf("%q: want 1 condition", input)
		}
		if q.Conditions[0].Op != OpNe {
			t.Errorf("%q: op = %q, want !=", input, q.Conditions[0].Op)
		}
	}
}

// A hyphen inside a value is a hyphen, not a negation.
func TestHyphenInsideValueIsNotNegation(t *testing.T) {
	q := mustParse(t, "release:my-app-2.0")

	if q.Conditions[0].Negated {
		t.Error("the hyphen inside the value was read as a negation")
	}
	if q.Conditions[0].Value != "my-app-2.0" {
		t.Errorf("value = %v", q.Conditions[0].Value)
	}
}

// Unknown fields are the user's own tags. Rejecting them would mean asking users
// to declare their tags to us first, which nobody will do.
func TestUnknownFieldBecomesATagLookup(t *testing.T) {
	q := mustParse(t, "tenant:acme")

	cond := q.Conditions[0]
	if cond.Field.Kind != KindMap || cond.Field.Column != "tags" {
		t.Fatalf("condition = %+v, want a tags map lookup", cond)
	}
	if cond.MapKey != "tenant" {
		t.Errorf("map key = %q, want tenant", cond.MapKey)
	}
}

func TestExplicitTagSyntax(t *testing.T) {
	q := mustParse(t, "tags.feature_flag.new_cart:true")

	cond := q.Conditions[0]
	if cond.Field.Column != "tags" {
		t.Fatalf("want a tags lookup, got %+v", cond)
	}
	// The key keeps its dots — a flag really is named "feature_flag.new_cart".
	if cond.MapKey != "feature_flag.new_cart" {
		t.Errorf("map key = %q", cond.MapKey)
	}
}

// A declared dotted field must win over the tag interpretation.
func TestDeclaredDottedFieldIsNotATag(t *testing.T) {
	q := mustParse(t, "user.email:a@example.com")

	cond := q.Conditions[0]
	if cond.Field.Column != "user_email" {
		t.Errorf("column = %q, want user_email — a declared field beats the catch-all", cond.Field.Column)
	}
	if cond.MapKey != "" {
		t.Errorf("map key = %q, want none", cond.MapKey)
	}
}

func TestQuotedValuesKeepSpacesAndColons(t *testing.T) {
	q := mustParse(t, `value:"Cannot read properties of undefined"`)

	if len(q.Conditions) != 1 {
		t.Fatalf("want 1 condition, got %d (the quoted value was split)", len(q.Conditions))
	}
	if q.Conditions[0].Value != "Cannot read properties of undefined" {
		t.Errorf("value = %v", q.Conditions[0].Value)
	}
}

// An operator inside quotes is literal text, not a comparison.
func TestOperatorInsideQuotesIsLiteral(t *testing.T) {
	q := mustParse(t, `value:">= is a comparison"`)

	cond := q.Conditions[0]
	if cond.Op != OpEq {
		t.Errorf("op = %q, want = (the >= was inside quotes)", cond.Op)
	}
	if cond.Value != ">= is a comparison" {
		t.Errorf("value = %v", cond.Value)
	}
}

func TestFreeTextTerms(t *testing.T) {
	q := mustParse(t, "level:error undefined property")

	if len(q.FreeText) != 2 {
		t.Fatalf("free text = %v, want 2 terms", q.FreeText)
	}
	if len(q.Conditions) != 1 {
		t.Errorf("want the level: pair still parsed as a condition")
	}
}

func TestEmptyQueryIsValid(t *testing.T) {
	q := mustParse(t, "   ")
	if len(q.Conditions) != 0 || len(q.FreeText) != 0 {
		t.Errorf("an empty query should match everything, got %+v", q)
	}
}

// A bad value must be a clear error. Silently matching nothing is the most
// infuriating possible behaviour for a search box.
func TestBadValuesAreExplained(t *testing.T) {
	spans := Schema{
		Table: "spans",
		Fields: map[string]Field{
			"duration": {Column: "duration_ns", Kind: KindDuration},
			"name":     {Column: "name", Kind: KindString},
		},
	}

	tests := []struct {
		name  string
		input string
		want  string // a substring the message must contain
	}{
		{name: "unparseable duration", input: "duration:>abc", want: "duration"},
		{name: "ordering op on a text field", input: "name:>x", want: "text field"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.input, spans)
			if err == nil {
				t.Fatal("want an error explaining the problem, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err, tc.want)
			}
		})
	}
}

func TestDurationValuesAreConvertedToNanoseconds(t *testing.T) {
	spans := Schema{
		Table:  "spans",
		Fields: map[string]Field{"duration": {Column: "duration_ns", Kind: KindDuration}},
	}

	q, err := Parse("duration:>500ms", spans)
	if err != nil {
		t.Fatal(err)
	}
	cond := q.Conditions[0]
	if cond.Op != OpGt {
		t.Errorf("op = %q, want >", cond.Op)
	}
	// The user says "500ms"; the column stores nanoseconds. They must never have
	// to know that.
	if cond.Value != int64(500*time.Millisecond) {
		t.Errorf("value = %v, want %v ns", cond.Value, int64(500*time.Millisecond))
	}
}

// --- compilation ------------------------------------------------------------

func TestCompileBindsEveryValue(t *testing.T) {
	q := mustParse(t, "level:error tenant:acme")

	sql, err := Compile(q, Errors, 4, "from", "to")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// The project scope and time range are never optional: without them every
	// query is a full scan, and without project_id it reads another tenant's data.
	if !strings.HasPrefix(sql.Where, "project_id = ? AND timestamp >= ? AND timestamp < ?") {
		t.Errorf("where = %q, must always be project- and time-scoped", sql.Where)
	}
	// Not one user value may appear in the SQL text.
	for _, leaked := range []string{"error", "acme"} {
		if strings.Contains(sql.Where, leaked) {
			t.Errorf("value %q was interpolated into the SQL instead of bound: %s", leaked, sql.Where)
		}
	}
	if got := strings.Count(sql.Where, "?"); got != len(sql.Args) {
		t.Errorf("%d placeholders but %d args — they must match exactly", got, len(sql.Args))
	}
}

// The search box is the most obvious injection surface in the product.
func TestCompileIsInjectionSafe(t *testing.T) {
	malicious := []string{
		`level:"error' OR 1=1 --"`,
		`value:"'; DROP TABLE errors; --"`,
		`"'; DROP TABLE errors; --"`,
	}

	for _, input := range malicious {
		t.Run(input, func(t *testing.T) {
			q, err := Parse(input, Errors)
			if err != nil {
				return // rejecting it outright is also fine
			}
			sql, err := Compile(q, Errors, 4, "from", "to")
			if err != nil {
				return
			}
			lowered := strings.ToLower(sql.Where)
			for _, danger := range []string{"drop", "--", "or 1=1"} {
				if strings.Contains(lowered, danger) {
					t.Errorf("SQL contains %q: %s", danger, sql.Where)
				}
			}
		})
	}
}

// A tag KEY is user input too, so it must be bound rather than interpolated
// into the map subscript.
func TestMapKeyIsBoundNotInterpolated(t *testing.T) {
	q := mustParse(t, `tags.evil':x:1`)

	sql, err := Compile(q, Errors, 4, "from", "to")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(sql.Where, "tags[?]") {
		t.Errorf("the map key must be a placeholder: %s", sql.Where)
	}
	if strings.Contains(sql.Where, "evil") {
		t.Errorf("the map key was interpolated into the SQL: %s", sql.Where)
	}
}

func TestCompileFreeText(t *testing.T) {
	q := mustParse(t, "undefined")

	sql, err := Compile(q, Errors, 4, "from", "to")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// positionCaseInsensitive, not LIKE — it is what the tokenbf_v1 skip index on
	// exception_value can actually serve.
	if !strings.Contains(sql.Where, "positionCaseInsensitive") {
		t.Errorf("free text should use positionCaseInsensitive: %s", sql.Where)
	}
	if strings.Contains(sql.Where, "undefined") {
		t.Errorf("the search term was interpolated instead of bound: %s", sql.Where)
	}
}

func TestCompileNegatedComparison(t *testing.T) {
	spans := Schema{
		Table:  "spans",
		Fields: map[string]Field{"duration": {Column: "duration_ns", Kind: KindDuration}},
	}
	q, err := Parse("!duration:>500ms", spans)
	if err != nil {
		t.Fatal(err)
	}

	sql, err := Compile(q, spans, 4, "from", "to")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql.Where, "NOT (duration_ns > ?)") {
		t.Errorf("where = %q, want a wrapped NOT", sql.Where)
	}
}
