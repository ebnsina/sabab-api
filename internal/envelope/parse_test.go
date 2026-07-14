package envelope

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/ebnsina/sabab-api/internal/event"
)

// build assembles a well-formed envelope body from payloads, computing each
// item's length the way an SDK would.
func build(header string, items ...struct {
	kind    string
	payload string
}) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	for _, it := range items {
		fmt.Fprintf(&b, "{\"type\":%q,\"length\":%d}\n%s\n", it.kind, len(it.payload), it.payload)
	}
	return b.String()
}

type item = struct {
	kind    string
	payload string
}

func TestParseHappyPath(t *testing.T) {
	body := build(`{"sent_at":"2026-07-14T10:00:00Z","sdk":{"name":"sabab.javascript.browser","version":"1.0.0"}}`,
		item{"error", `{"message":"boom"}`},
		item{"log", `{"body":"hello"}`},
		item{"span", `{"name":"GET /users/:id"}`},
	)

	env, err := Parse(strings.NewReader(body), Limits{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if env.Header.SDK.Name != "sabab.javascript.browser" {
		t.Errorf("sdk name = %q", env.Header.SDK.Name)
	}
	if env.Header.SentAt.IsZero() {
		t.Error("sent_at was not parsed")
	}
	if len(env.Items) != 3 {
		t.Fatalf("want 3 items, got %d", len(env.Items))
	}
	want := []event.Kind{event.KindError, event.KindLog, event.KindSpan}
	for i, kind := range want {
		if env.Items[i].Type != kind {
			t.Errorf("item %d: type = %q, want %q", i, env.Items[i].Type, kind)
		}
	}
	if got := string(env.Items[0].Payload); got != `{"message":"boom"}` {
		t.Errorf("payload 0 = %q", got)
	}
}

// The whole point of the length field: a v1 gateway must keep the items it
// understands from an envelope a v2 SDK sent.
func TestParseSkipsUnknownItemTypesAndKeepsTheRest(t *testing.T) {
	body := build(`{"sdk":{"name":"x","version":"1"}}`,
		item{"error", `{"message":"kept"}`},
		item{"hologram", `{"some":"future signal"}`},
		item{"log", `{"body":"also kept"}`},
	)

	env, err := Parse(strings.NewReader(body), Limits{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(env.Items) != 2 {
		t.Fatalf("want the 2 known items, got %d", len(env.Items))
	}
	if env.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 — dropped items must be counted, not hidden", env.Skipped)
	}
	if string(env.Items[1].Payload) != `{"body":"also kept"}` {
		t.Errorf("the item after the unknown one was misaligned: %q", env.Items[1].Payload)
	}
}

func TestParsePayloadWithoutTrailingNewline(t *testing.T) {
	// Last payload ends the body with no trailing newline at all.
	body := `{"sdk":{"name":"x","version":"1"}}` + "\n" +
		`{"type":"error","length":5}` + "\n" + `12345`

	env, err := Parse(strings.NewReader(body), Limits{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(env.Items) != 1 || string(env.Items[0].Payload) != "12345" {
		t.Fatalf("got %#v", env.Items)
	}
}

// A payload containing newlines must be read by length, not by scanning for the
// next newline — otherwise any pretty-printed JSON corrupts the whole envelope.
func TestParsePayloadContainingNewlines(t *testing.T) {
	payload := "{\n  \"message\": \"multi\\nline\"\n}"
	body := fmt.Sprintf("{\"sdk\":{\"name\":\"x\",\"version\":\"1\"}}\n{\"type\":\"error\",\"length\":%d}\n%s\n",
		len(payload), payload)

	env, err := Parse(strings.NewReader(body), Limits{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(env.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(env.Items))
	}
	if string(env.Items[0].Payload) != payload {
		t.Errorf("payload = %q, want %q", env.Items[0].Payload, payload)
	}
}

func TestParseMalformed(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "empty body", body: ""},
		{name: "header is not JSON", body: "not json\n"},
		{name: "item header is not JSON", body: `{"sdk":{}}` + "\nnot json\n"},
		{
			name: "length longer than the remaining body",
			body: `{"sdk":{}}` + "\n" + `{"type":"error","length":9999}` + "\nshort",
		},
		{
			name: "negative length",
			body: `{"sdk":{}}` + "\n" + `{"type":"error","length":-1}` + "\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tc.body), Limits{})
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("want ErrMalformed (so the gateway answers 400), got %v", err)
			}
		})
	}
}

func TestParseEnforcesLimits(t *testing.T) {
	t.Run("too many items", func(t *testing.T) {
		items := make([]item, 4)
		for i := range items {
			items[i] = item{"error", `{}`}
		}
		body := build(`{"sdk":{}}`, items...)

		_, err := Parse(strings.NewReader(body), Limits{MaxItems: 3})
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("want ErrTooLarge, got %v", err)
		}
	})

	t.Run("item payload too large", func(t *testing.T) {
		body := build(`{"sdk":{}}`, item{"error", strings.Repeat("x", 100)})

		_, err := Parse(strings.NewReader(body), Limits{MaxItemBytes: 10})
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("want ErrTooLarge, got %v", err)
		}
	})

	t.Run("decompressed body too large", func(t *testing.T) {
		body := build(`{"sdk":{}}`, item{"error", strings.Repeat("x", 500)})

		_, err := Parse(strings.NewReader(body), Limits{MaxDecompressedBytes: 64})
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("want ErrTooLarge, got %v", err)
		}
	})

	// An endless line with no newline must not be buffered without bound.
	t.Run("header line without a newline is bounded", func(t *testing.T) {
		body := strings.Repeat("x", maxHeaderLine+100)

		_, err := Parse(strings.NewReader(body), Limits{})
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("want ErrTooLarge, got %v", err)
		}
	})
}

// A zip bomb must fail on the limit, not after inflating into memory.
func TestParseRejectsZipBomb(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write([]byte(strings.Repeat("A", 10<<20))); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	// Highly compressible: a small body that inflates to 10 MiB.
	if compressed.Len() > 1<<20 {
		t.Fatalf("test bomb is not compressed enough: %d bytes", compressed.Len())
	}

	zr, err := Decompress(&compressed, "gzip")
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	_, err = Parse(zr, Limits{MaxDecompressedBytes: 1 << 20})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestDecompress(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	t.Run("gzip", func(t *testing.T) {
		r, err := Decompress(bytes.NewReader(buf.Bytes()), "gzip")
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "hello" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("identity", func(t *testing.T) {
		r, err := Decompress(strings.NewReader("hello"), "")
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		got, _ := io.ReadAll(r)
		if string(got) != "hello" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("gzip claimed but body is not gzip", func(t *testing.T) {
		_, err := Decompress(strings.NewReader("plain text"), "gzip")
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("want ErrMalformed, got %v", err)
		}
	})

	t.Run("unsupported encoding is rejected, not passed through", func(t *testing.T) {
		_, err := Decompress(strings.NewReader("x"), "br")
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("want ErrMalformed, got %v", err)
		}
	})
}
