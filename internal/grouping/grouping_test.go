package grouping

import (
	"testing"

	"github.com/ebnsina/sabab-api/internal/event"
)

// errorWith builds an event with one exception and the given frames.
func errorWith(excType, value string, frames ...event.Frame) *event.Error {
	return &event.Error{
		Exceptions: []event.Exception{{Type: excType, Value: value, Frames: frames}},
	}
}

func frame(module, function string, line int, inApp bool) event.Frame {
	return event.Frame{Module: module, Function: function, Lineno: line, InApp: inApp}
}

// THE acceptance test from the plan: the same bug in two releases, with every
// line number shifted by someone adding an import at the top of the file, must
// produce ONE issue. If it does not, every deploy looks like a wave of new bugs
// and the tool is worthless.
func TestSameBugAcrossReleasesWithShiftedLinesGroupsTogether(t *testing.T) {
	before := errorWith("TypeError", "Cannot read properties of undefined (reading 'id')",
		frame("app/cart", "renderCart", 42, true),
		frame("app/page", "render", 17, true),
	)
	after := errorWith("TypeError", "Cannot read properties of undefined (reading 'id')",
		frame("app/cart", "renderCart", 48, true), // +6: an import was added
		frame("app/page", "render", 23, true),
	)

	a, b := Fingerprint(before), Fingerprint(after)
	if a.Hash != b.Hash {
		t.Fatalf("line-number shift split the issue in two: %x vs %x\n a=%v\n b=%v",
			a.Hash, b.Hash, a.Components, b.Components)
	}
	if a.Method != MethodStackTrace {
		t.Errorf("method = %q, want stack_trace", a.Method)
	}
}

// Two genuinely different bugs must NOT collide. Over-grouping hides real bugs,
// which is the opposite failure and just as fatal.
func TestDifferentBugsDoNotCollide(t *testing.T) {
	cart := errorWith("TypeError", "Cannot read properties of undefined (reading 'id')",
		frame("app/cart", "renderCart", 42, true),
	)
	checkout := errorWith("TypeError", "Cannot read properties of undefined (reading 'id')",
		frame("app/checkout", "submitOrder", 42, true),
	)

	if Fingerprint(cart).Hash == Fingerprint(checkout).Hash {
		t.Fatal("two different functions were grouped into one issue")
	}
}

// Different exception types in the same function are different bugs.
func TestDifferentExceptionTypesDoNotCollide(t *testing.T) {
	a := errorWith("TypeError", "boom", frame("app/cart", "renderCart", 1, true))
	b := errorWith("RangeError", "boom", frame("app/cart", "renderCart", 1, true))

	if Fingerprint(a).Hash == Fingerprint(b).Hash {
		t.Fatal("a TypeError and a RangeError were grouped together")
	}
}

// "user 1 not found" and "user 2 not found" are one bug.
func TestMessagesWithVaryingIdsCollapse(t *testing.T) {
	one := &event.Error{Message: "user 8412 not found"}
	two := &event.Error{Message: "user 9137 not found"}

	a, b := Fingerprint(one), Fingerprint(two)
	if a.Hash != b.Hash {
		t.Fatalf("ids in the message split the issue: %v vs %v", a.Components, b.Components)
	}
	if a.Method != MethodMessage {
		t.Errorf("method = %q, want message", a.Method)
	}
}

// ...but two genuinely different messages must not collapse into one.
func TestDifferentMessagesDoNotCollapse(t *testing.T) {
	a := &event.Error{Message: "user 8412 not found"}
	b := &event.Error{Message: "order 8412 not found"}

	if Fingerprint(a).Hash == Fingerprint(b).Hash {
		t.Fatal("'user not found' and 'order not found' were grouped together")
	}
}

// Dependency frames must not decide the group: two unrelated bugs that both
// happen to go through the framework would otherwise merge.
func TestOnlyInAppFramesAreUsedWhenPresent(t *testing.T) {
	a := errorWith("Error", "boom",
		frame("node_modules/express/lib/router", "handle", 300, false),
		frame("app/cart", "renderCart", 42, true),
	)
	b := errorWith("Error", "boom",
		frame("node_modules/express/lib/router", "handle", 300, false),
		frame("app/checkout", "submitOrder", 12, true),
	)

	if Fingerprint(a).Hash == Fingerprint(b).Hash {
		t.Fatal("grouping used the shared framework frame instead of the in-app frames")
	}

	// The framework frame must not appear in the components at all.
	for _, c := range Fingerprint(a).Components {
		if c == "frame:node_modules/express/lib/router:handle" {
			t.Errorf("a non-in-app frame leaked into the fingerprint: %v", Fingerprint(a).Components)
		}
	}
}

// When nothing is marked in_app (common for a minified bundle before we have
// source maps) we must still group on something rather than give up.
func TestFallsBackToAllFramesWhenNoneAreInApp(t *testing.T) {
	e := errorWith("TypeError", "boom",
		frame("vendor", "doThing", 10, false),
	)

	got := Fingerprint(e)
	if got.Method != MethodStackTrace {
		t.Errorf("method = %q, want stack_trace", got.Method)
	}
}

// A content-hashed bundle name changes every build. If it reached the
// fingerprint, every deploy would resurrect every issue as new.
func TestBundleHashInFilenameDoesNotSplitTheGroup(t *testing.T) {
	build1 := errorWith("TypeError", "boom", event.Frame{
		Filename: "/static/js/main.a3f9.js", Function: "renderCart", Lineno: 1, InApp: true,
	})
	build2 := errorWith("TypeError", "boom", event.Frame{
		Filename: "/static/js/main.b7c2.js", Function: "renderCart", Lineno: 1, InApp: true,
	})

	if Fingerprint(build1).Hash != Fingerprint(build2).Hash {
		t.Fatal("the bundle content hash split one issue across two builds")
	}
}

func TestNormalizeFilename(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		// Content hashes are stripped: they change every build.
		{"/static/js/main.a3f9.js", "main.js"},
		{"/assets/chunk.9f8e7d6c.js", "chunk.js"},
		{"/assets/index-4dK2pQ8x.mjs", "index.mjs"},
		{"/static/js/main.a3f9.js?v=2", "main.js"},
		// ...but ordinary words must survive. Stripping "min" or "slim" would
		// merge genuinely different files.
		{"/static/js/app.min.js", "app.min.js"},
		{"/static/js/jquery.slim.js", "jquery.slim.js"},
		{"/src/routes/cart.ts", "cart.ts"},
		{"", ""},
	}

	for _, tc := range tests {
		if got := normalizeFilename(tc.in); got != tc.want {
			t.Errorf("normalizeFilename(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCustomFingerprintWins(t *testing.T) {
	a := errorWith("TypeError", "boom", frame("app/cart", "renderCart", 1, true))
	a.Fingerprint = []string{"checkout-outage"}
	b := errorWith("RangeError", "entirely different", frame("app/other", "somethingElse", 9, true))
	b.Fingerprint = []string{"checkout-outage"}

	fa, fb := Fingerprint(a), Fingerprint(b)
	if fa.Hash != fb.Hash {
		t.Fatal("an explicit fingerprint must group events regardless of their stacks")
	}
	if fa.Method != MethodCustom {
		t.Errorf("method = %q, want custom", fa.Method)
	}
}

// {{default}} means "your algorithm, plus my extras" — it must not be treated
// as a fully custom fingerprint, or the escape hatch becomes an override.
func TestDefaultSentinelDoesNotCountAsCustom(t *testing.T) {
	e := errorWith("TypeError", "boom", frame("app/cart", "renderCart", 1, true))
	e.Fingerprint = []string{DefaultFingerprint}

	if got := Fingerprint(e); got.Method != MethodStackTrace {
		t.Errorf("method = %q, want stack_trace — {{default}} must fall through", got.Method)
	}
}

// An empty event must still produce a hash. Dropping it would mean silently
// losing an event we already told the SDK we accepted.
func TestEmptyErrorStillGroups(t *testing.T) {
	got := Fingerprint(&event.Error{})
	if got.Hash == 0 {
		t.Error("an empty error produced no hash")
	}
	if got.Method != MethodFallback {
		t.Errorf("method = %q, want fallback", got.Method)
	}
}

// The components are shown to users to answer "why are these grouped?", so they
// must be populated for every method.
func TestComponentsAreAlwaysRecorded(t *testing.T) {
	cases := map[string]*event.Error{
		"stack":    errorWith("TypeError", "boom", frame("app/cart", "renderCart", 1, true)),
		"message":  {Message: "user 1 not found"},
		"fallback": {},
	}
	for name, e := range cases {
		if got := Fingerprint(e); len(got.Components) == 0 {
			t.Errorf("%s: no components recorded — the UI cannot explain this grouping", name)
		}
	}
}

func TestHexIsSixteenChars(t *testing.T) {
	if got := Hex(0x1); got != "0000000000000001" {
		t.Errorf("Hex(1) = %q", got)
	}
	if got := Hex(0xffffffffffffffff); got != "ffffffffffffffff" {
		t.Errorf("Hex(max) = %q", got)
	}
}

func TestParameterize(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"user 8412 not found", "user <num> not found"},
		{"user 6fa459ea-ee8a-3ca4-894e-db77e160355e not found", "user <uuid> not found"},
		{"failed to email alice@example.com", "failed to email <email>"},
		{"GET https://api.example.com/v1/users failed", "GET <url> failed"},
		{"cannot open /var/lib/sabab/data.db", "cannot open <path>"},
		{`key "session_id" is missing`, "key <str> is missing"},
		{"object 5f8d0d55b54764421b7156c3 not found", "object <hex> not found"},
		// Nothing variable: must be left completely alone.
		{"connection refused", "connection refused"},
	}

	for _, tc := range tests {
		if got := Parameterize(tc.in); got != tc.want {
			t.Errorf("Parameterize(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
		}
	}
}
