package symbolicate

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ebnsina/sabab-api/internal/event"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
)

// The three spellings of the same file — browser URL, upload pattern, bundler
// path — must all match. When this is wrong, maps that were uploaded correctly
// silently never apply, and the user sees minified stacks with no explanation.
func TestMatchArtifact(t *testing.T) {
	patterns := []string{
		"~/static/js/main.a3f9.js",
		"~/static/js/vendor.b1c2.js",
		"~/static/css/app.css",
	}

	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{
			name:     "absolute browser URL matches the ~ pattern",
			filename: "https://app.example.com/static/js/main.a3f9.js",
			want:     "~/static/js/main.a3f9.js",
		},
		{
			name:     "root-relative path matches",
			filename: "/static/js/main.a3f9.js",
			want:     "~/static/js/main.a3f9.js",
		},
		{
			name:     "cache-busting query string is ignored",
			filename: "https://app.example.com/static/js/main.a3f9.js?v=2",
			want:     "~/static/js/main.a3f9.js",
		},
		{
			name:     "a different host still matches, because ~ means any host",
			filename: "https://cdn.other.net/static/js/vendor.b1c2.js",
			want:     "~/static/js/vendor.b1c2.js",
		},
		{
			name:     "the more specific path wins",
			filename: "https://app.example.com/static/js/vendor.b1c2.js",
			want:     "~/static/js/vendor.b1c2.js",
		},
		{
			// The basename must agree. Sharing only a directory would let one
			// bundle symbolicate with another's map — confidently wrong output,
			// which is worse than none.
			name:     "a file we have no map for does not match",
			filename: "https://app.example.com/static/js/unknown.js",
			want:     "",
		},
		{name: "empty filename", filename: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := matchArtifact(tc.filename, patterns)
			if tc.want == "" {
				if ok {
					t.Fatalf("want no match, got %q", got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Errorf("matchArtifact(%q) = %q (ok=%v), want %q", tc.filename, got, ok, tc.want)
			}
		})
	}
}

func TestModuleFor(t *testing.T) {
	tests := []struct{ in, want string }{
		{"src/routes/cart.ts", "src/routes/cart"},
		{"webpack://app/./src/routes/cart.ts", "app/./src/routes/cart"},
		{"./src/lib/api.js", "src/lib/api"},
		{"/src/lib/api.js", "src/lib/api"},
	}
	for _, tc := range tests {
		if got := moduleFor(tc.in); got != tc.want {
			t.Errorf("moduleFor(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsInApp(t *testing.T) {
	// The customer's own code: the default must be true, or symbolication
	// collapses the entire stack and shows the user nothing.
	for _, source := range []string{"src/routes/cart.ts", "app/lib/api.ts"} {
		if !isInApp(source) {
			t.Errorf("%q should be in-app", source)
		}
	}
	// Dependencies must not be, or two unrelated bugs that both went through
	// React get grouped into one issue.
	for _, source := range []string{
		"node_modules/react/index.js",
		"webpack-internal:///./node_modules/svelte/store.js",
		"/vendor/lodash.js",
	} {
		if isInApp(source) {
			t.Errorf("%q should NOT be in-app", source)
		}
	}
}

// --- fakes ------------------------------------------------------------------

type fakeReleases struct {
	files []postgres.ReleaseFile
	err   error
}

func (f *fakeReleases) ReleaseFilesFor(context.Context, uint64, string) ([]postgres.ReleaseFile, error) {
	return f.files, f.err
}

type fakeArtifacts struct {
	blobs map[string]string
	err   error
}

func (f *fakeArtifacts) Get(_ context.Context, key string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	body, ok := f.blobs[key]
	if !ok {
		return nil, io.EOF
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// testMap is a REAL source map, produced by esbuild from testdata/cart.ts:
//
//	export function renderCart(items: Item[] | undefined): string {
//	  const first = items![0];
//	  return first.id;          // ← line 5: the bug
//	}
//
// minified to: (()=>{function e(t){return t[0].id}function i(){e(void 0)}})();
//
// It is a fixture rather than a hand-written map because hand-encoding VLQ
// mappings is exactly the kind of thing you get subtly wrong, and a test built
// on a wrong map would prove nothing.
func testMap(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "main.a3f9.js.map"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(body)
}

// minifiedColumn is the 0-based column of `t[0].id` in the minified bundle —
// where a browser would report the TypeError.
const minifiedColumn = 27

// The M1 acceptance test, in miniature: a minified frame becomes original
// TypeScript, with the offending source line shown in context.
func TestSymbolicateRewritesFrameToOriginalSource(t *testing.T) {
	s := New(
		&fakeReleases{files: []postgres.ReleaseFile{{
			URLPattern:  "~/static/js/main.a3f9.js",
			ArtifactKey: "sourcemaps/1/web@2.4.1/abc.map",
		}}},
		&fakeArtifacts{blobs: map[string]string{"sourcemaps/1/web@2.4.1/abc.map": testMap(t)}},
		discard(),
	)

	e := &event.Error{Exceptions: []event.Exception{{
		Type: "TypeError",
		Frames: []event.Frame{{
			Filename: "https://app.example.com/static/js/main.a3f9.js",
			Function: "e", // minified name
			Lineno:   1,
			Colno:    minifiedColumn,
			InApp:    true,
		}},
	}}}

	if err := s.Symbolicate(t.Context(), 1, "web@2.4.1", e); err != nil {
		t.Fatalf("Symbolicate: %v", err)
	}

	frame := e.Exceptions[0].Frames[0]
	if frame.Filename != "src/routes/cart.ts" {
		t.Errorf("filename = %q, want the ORIGINAL source src/routes/cart.ts", frame.Filename)
	}
	if frame.Lineno == 1 {
		t.Errorf("lineno is still 1 — the frame was not mapped back to the original line")
	}
	// The module is what grouping hashes: it MUST come from the original path,
	// not the content-hashed bundle name.
	if frame.Module != "src/routes/cart" {
		t.Errorf("module = %q, want src/routes/cart", frame.Module)
	}
	// The whole point: show the developer their own code.
	if !strings.Contains(frame.ContextLine, "first") {
		t.Errorf("context line = %q, want a line of the original TypeScript", frame.ContextLine)
	}
	t.Logf("symbolicated to %s:%d — %q", frame.Filename, frame.Lineno, strings.TrimSpace(frame.ContextLine))
}

// Every failure path must leave the frame intact. A minified stack is a poor
// experience; a dropped error is a broken product.
func TestSymbolicationFailuresNeverLoseTheEvent(t *testing.T) {
	original := func() *event.Error {
		return &event.Error{Exceptions: []event.Exception{{
			Frames: []event.Frame{{
				Filename: "https://app.example.com/static/js/main.a3f9.js",
				Function: "n", Lineno: 1, Colno: 40, InApp: true,
			}},
		}}}
	}

	tests := []struct {
		name    string
		s       *Symbolicator
		release string
	}{
		{
			name:    "no release: we cannot know which build's maps to use",
			s:       New(&fakeReleases{}, &fakeArtifacts{}, discard()),
			release: "",
		},
		{
			name:    "no maps uploaded for this release",
			s:       New(&fakeReleases{}, &fakeArtifacts{}, discard()),
			release: "web@2.4.1",
		},
		{
			name: "artifact missing from the object store",
			s: New(
				&fakeReleases{files: []postgres.ReleaseFile{{
					URLPattern: "~/static/js/main.a3f9.js", ArtifactKey: "gone.map",
				}}},
				&fakeArtifacts{blobs: map[string]string{}},
				discard(),
			),
			release: "web@2.4.1",
		},
		{
			name: "source map is corrupt",
			s: New(
				&fakeReleases{files: []postgres.ReleaseFile{{
					URLPattern: "~/static/js/main.a3f9.js", ArtifactKey: "bad.map",
				}}},
				&fakeArtifacts{blobs: map[string]string{"bad.map": "{not json"}},
				discard(),
			),
			release: "web@2.4.1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := original()
			if err := tc.s.Symbolicate(t.Context(), 1, tc.release, e); err != nil {
				t.Fatalf("Symbolicate returned an error, which would cost us the event: %v", err)
			}
			frame := e.Exceptions[0].Frames[0]
			if frame.Function != "n" || frame.Lineno != 1 {
				t.Errorf("the original frame was damaged: %+v", frame)
			}
		})
	}
}

// A burst of errors from one bad deploy means thousands of events wanting the
// same map. It must be fetched once.
func TestParsedMapsAreCached(t *testing.T) {
	artifacts := &countingArtifacts{blobs: map[string]string{"abc.map": testMap(t)}}
	s := New(
		&fakeReleases{files: []postgres.ReleaseFile{{
			URLPattern: "~/static/js/main.a3f9.js", ArtifactKey: "abc.map",
		}}},
		artifacts,
		discard(),
	)

	for range 5 {
		e := &event.Error{Exceptions: []event.Exception{{
			Frames: []event.Frame{{
				Filename: "/static/js/main.a3f9.js", Lineno: 1, Colno: 40, InApp: true,
			}},
		}}}
		if err := s.Symbolicate(t.Context(), 1, "web@2.4.1", e); err != nil {
			t.Fatal(err)
		}
	}

	if artifacts.fetches != 1 {
		t.Errorf("fetched the map %d times, want 1 — it must be cached", artifacts.fetches)
	}
}

type countingArtifacts struct {
	blobs   map[string]string
	fetches int
}

func (c *countingArtifacts) Get(_ context.Context, key string) (io.ReadCloser, error) {
	c.fetches++
	body, ok := c.blobs[key]
	if !ok {
		return nil, io.EOF
	}
	return io.NopCloser(strings.NewReader(body)), nil
}
