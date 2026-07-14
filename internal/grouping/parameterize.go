package grouping

import (
	"regexp"
	"strings"
)

// The variable parts of a message, in the order they are replaced. Order
// matters: a UUID must be recognised before the plain-number rule gets a chance
// to chew its digits out, and an email before the path rule sees the slashes.
//
// Each pattern is deliberately conservative. Over-replacing collapses genuinely
// different bugs into one issue — far worse than under-replacing, which merely
// leaves a few near-duplicates side by side.
var replacements = []struct {
	name        string
	pattern     *regexp.Regexp
	placeholder string
}{
	{
		name:        "uuid",
		pattern:     regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`),
		placeholder: "<uuid>",
	},
	{
		name:        "email",
		pattern:     regexp.MustCompile(`(?i)\b[\w.+-]+@[\w-]+\.[\w.-]+\b`),
		placeholder: "<email>",
	},
	{
		name:        "url",
		pattern:     regexp.MustCompile(`(?i)\bhttps?://[^\s'"]+`),
		placeholder: "<url>",
	},
	{
		// A long hex run: object ids, hashes, bundle fingerprints. 8+ so we do
		// not eat ordinary words like "deadbeef" written in prose... which is
		// itself hex, so this is a judgement call, not a certainty.
		name:        "hex",
		pattern:     regexp.MustCompile(`(?i)\b(?:0x)?[0-9a-f]{8,}\b`),
		placeholder: "<hex>",
	},
	{
		name:        "path",
		pattern:     regexp.MustCompile(`(?:/[\w.@:-]+){2,}/?`),
		placeholder: "<path>",
	},
	{
		// Quoted strings are almost always the variable part: the offending
		// value, the missing key, the bad input.
		name:        "quoted",
		pattern:     regexp.MustCompile(`'[^']*'|"[^"]*"`),
		placeholder: "<str>",
	},
	{
		// Numbers last: everything above that legitimately contains digits has
		// already been consumed.
		name:        "number",
		pattern:     regexp.MustCompile(`\b\d[\d.,]*\b`),
		placeholder: "<num>",
	},
}

// Parameterize replaces the variable parts of a message with placeholders, so
// that the same bug reported about a thousand different ids collapses into one
// issue.
//
//	"user 8412 not found"  →  "user <num> not found"
//	"user 9137 not found"  →  "user <num> not found"
func Parameterize(message string) string {
	out := strings.TrimSpace(message)
	for _, r := range replacements {
		out = r.pattern.ReplaceAllString(out, r.placeholder)
	}
	// Collapse whitespace so trivial formatting differences do not split a group.
	return strings.Join(strings.Fields(out), " ")
}

// bundleHash matches the content hash in a built asset name — main.a3f9.js,
// chunk.9f8e7d6c.js, index-4dK2pQ8x.mjs.
//
// Two alternatives, because bundlers disagree: a short hex digest (4+ chars, as
// esbuild and older webpack emit) or a longer base62 one (8+, as Vite emits).
// The hex arm is kept to hex characters precisely so it does not eat ordinary
// words — "main.min.js" and "jquery.slim.js" survive untouched, because "min"
// and "slim" are not hex.
var bundleHash = regexp.MustCompile(`(?i)[.\-_](?:[0-9a-f]{4,}|[0-9a-z]{8,})(\.\w+)$`)

// normalizeFilename strips the parts of a filename that change every build.
//
// Without this, a content-hashed bundle name puts the build id inside the
// fingerprint, and every single deploy would present the same ongoing bug as a
// brand-new issue — the exact failure this whole package exists to prevent.
func normalizeFilename(filename string) string {
	if filename == "" {
		return ""
	}
	// Query strings and cache-busters carry no identity.
	if i := strings.IndexAny(filename, "?#"); i >= 0 {
		filename = filename[:i]
	}
	base := filename
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	return bundleHash.ReplaceAllString(base, "$1")
}
