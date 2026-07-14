// Package grouping turns an error event into a fingerprint.
//
// This is the algorithm the product lives or dies by. Ten thousand raw errors
// are noise; one issue that says "this broke 10,000 times since the 2.4.1
// deploy" is the product. Grouping too aggressively hides distinct bugs behind
// one row; grouping too finely buries the real one under a thousand near
// duplicates. Both failures look like "your tool is useless".
//
// It runs in the processor **after symbolication**. Fingerprinting a minified
// frame would produce a hash that changes on every deploy, and every release
// would arrive looking like a wave of brand-new issues.
package grouping

import (
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/ebnsina/sabab-api/internal/event"
)

// Result is a fingerprint and the reason for it.
type Result struct {
	// Hash is the group identity. Postgres stores it as 16-char hex,
	// ClickHouse as UInt64 — same value, different column types.
	Hash uint64

	// Method records which rule produced the hash.
	Method Method

	// Components are the exact strings that were hashed.
	//
	// Stored alongside the hash because "why are these two errors grouped
	// together?" is a question users *will* ask, and "trust me" is not an
	// answer. It is also what makes merge/split possible later.
	Components []string
}

// Method is the rule that produced a fingerprint, in precedence order.
type Method string

const (
	// MethodCustom: the SDK supplied an explicit fingerprint.
	MethodCustom Method = "custom"
	// MethodStackTrace: hashed the in-app frames. The good case.
	MethodStackTrace Method = "stack_trace"
	// MethodMessage: no usable stack, so the parameterized message was hashed.
	MethodMessage Method = "message"
	// MethodFallback: nothing else was available.
	MethodFallback Method = "fallback"
)

// DefaultFingerprint is the sentinel an SDK sends to mean "use your algorithm".
//
// It exists so a user can *add* a component — ["{{default}}", "tenant-acme"] —
// rather than being forced to replace the whole hash to tweak it.
const DefaultFingerprint = "{{default}}"

// Hex renders a hash the way Postgres stores it.
func Hex(hash uint64) string {
	const digits = "0123456789abcdef"
	var buf [16]byte
	for i := 15; i >= 0; i-- {
		buf[i] = digits[hash&0xf]
		hash >>= 4
	}
	return string(buf[:])
}

// Fingerprint groups an error event.
func Fingerprint(e *event.Error) Result {
	if custom, ok := customComponents(e.Fingerprint); ok {
		return finish(MethodCustom, custom)
	}
	if components, ok := stackComponents(e); ok {
		return finish(MethodStackTrace, components)
	}
	if components, ok := messageComponents(e); ok {
		return finish(MethodMessage, components)
	}
	return finish(MethodFallback, fallbackComponents(e))
}

func finish(method Method, components []string) Result {
	digest := xxhash.New()
	for i, c := range components {
		if i > 0 {
			// A separator, so ["ab","c"] and ["a","bc"] cannot collide.
			_, _ = digest.Write([]byte{0x1f})
		}
		_, _ = digest.WriteString(c)
	}
	return Result{Hash: digest.Sum64(), Method: method, Components: components}
}

// customComponents honours an explicit SDK fingerprint. A fingerprint that
// still contains {{default}} is not fully custom — the caller wants our
// algorithm plus their extras — so it is not handled here.
func customComponents(fingerprint []string) ([]string, bool) {
	if len(fingerprint) == 0 {
		return nil, false
	}
	components := make([]string, 0, len(fingerprint))
	for _, f := range fingerprint {
		f = strings.TrimSpace(f)
		if f == DefaultFingerprint {
			return nil, false
		}
		if f != "" {
			components = append(components, f)
		}
	}
	if len(components) == 0 {
		return nil, false
	}
	return components, true
}

// stackComponents hashes the exception type plus the shape of the stack.
//
// Two deliberate choices, and the product depends on both:
//
//   - Only in_app frames are used (falling back to all frames when none are
//     marked). Hashing framework and node_modules frames would group unrelated
//     bugs together purely because they both went through Express.
//
//   - lineno and colno are DISCARDED. Adding a blank line to a file shifts every
//     line below it; keeping line numbers would split one ongoing issue into a
//     brand-new one on the next deploy, which is exactly the behaviour that makes
//     people stop trusting an error tracker.
func stackComponents(e *event.Error) ([]string, bool) {
	// The innermost exception is the one that actually threw; it is what the
	// user needs to see, and what identifies the bug.
	exc, ok := innermost(e)
	if !ok {
		return nil, false
	}

	frames := inAppFrames(exc.Frames)
	if len(frames) == 0 {
		frames = exc.Frames
	}
	if len(frames) == 0 {
		return nil, false
	}

	components := make([]string, 0, len(frames)+1)
	if exc.Type != "" {
		components = append(components, "type:"+exc.Type)
	}
	for _, f := range frames {
		if c := frameComponent(f); c != "" {
			components = append(components, c)
		}
	}
	// A type alone is not a stack. Fall through to message grouping rather than
	// grouping every TypeError in the app into one issue.
	if len(components) == 0 || (len(components) == 1 && exc.Type != "") {
		return nil, false
	}
	return components, true
}

// frameComponent identifies a frame by where the code *is*, never by where it
// happens to sit in the file today.
func frameComponent(f event.Frame) string {
	module := f.Module
	if module == "" {
		// Fall back to the filename, normalized: a content-hashed bundle name
		// changes on every build, and using it raw would make every deploy look
		// like a new issue.
		module = normalizeFilename(f.Filename)
	}
	function := f.Function
	if module == "" && function == "" {
		return ""
	}
	return "frame:" + module + ":" + function
}

func inAppFrames(frames []event.Frame) []event.Frame {
	var out []event.Frame
	for _, f := range frames {
		if f.InApp {
			out = append(out, f)
		}
	}
	return out
}

// innermost returns the last exception in the chain — the one that threw.
func innermost(e *event.Error) (event.Exception, bool) {
	for i := len(e.Exceptions) - 1; i >= 0; i-- {
		exc := e.Exceptions[i]
		if len(exc.Frames) > 0 || exc.Type != "" || exc.Value != "" {
			return exc, true
		}
	}
	return event.Exception{}, false
}

// messageComponents groups by the parameterized message.
//
// "user 8412 not found" and "user 9137 not found" are one bug, and a store that
// treats them as two is a log search box, not an error tracker.
func messageComponents(e *event.Error) ([]string, bool) {
	message := e.Message
	if message == "" {
		if exc, ok := innermost(e); ok {
			message = exc.Value
		}
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, false
	}
	return []string{"message:" + Parameterize(message)}, true
}

// fallbackComponents is the last resort: something is always better than
// dropping the event into an unnamed bucket.
func fallbackComponents(e *event.Error) []string {
	exc, ok := innermost(e)
	if !ok {
		return []string{"empty"}
	}
	return []string{"type:" + exc.Type, "value:" + Parameterize(exc.Value)}
}
