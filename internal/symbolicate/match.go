package symbolicate

import (
	"path"
	"strings"
)

// matchArtifact picks the artifact whose url_pattern best matches a frame's
// filename.
//
// This is fiddly because the same file is named three different ways by three
// different parties, and none of them can be changed:
//
//	the browser reports  https://app.example.com/static/js/main.a3f9.js
//	the bundler emits    dist/static/js/main.a3f9.js
//	the upload declares  ~/static/js/main.a3f9.js
//
// The "~" convention means "any host". So matching works from the right-hand
// side — the path suffix — which is the only part all three agree on. Getting
// this wrong means source maps that were uploaded correctly silently never
// apply, and the user sees minified stacks with no explanation.
func matchArtifact(filename string, patterns []string) (string, bool) {
	if filename == "" || len(patterns) == 0 {
		return "", false
	}
	target := normalizeURL(filename)

	var (
		best      string
		bestScore int
	)
	for _, pattern := range patterns {
		score := matchScore(target, normalizeURL(pattern))
		if score > bestScore {
			best, bestScore = pattern, score
		}
	}
	if bestScore == 0 {
		return "", false
	}
	return best, true
}

// normalizeURL reduces any of the three spellings to a comparable path.
func normalizeURL(s string) string {
	s = strings.TrimSpace(s)

	// Strip a query string or fragment: a cache-buster is not identity.
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	// Drop the scheme and host — "~" is the upload convention for "any host".
	if i := strings.Index(s, "://"); i >= 0 {
		rest := s[i+3:]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			s = rest[slash:]
		} else {
			s = "/"
		}
	}
	s = strings.TrimPrefix(s, "~")
	// A leading ./ or dist/ prefix is a build artifact of where the bundler ran.
	s = strings.TrimPrefix(s, ".")

	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return s
}

// matchScore counts how many trailing path segments two paths share. More
// shared segments is a more specific match, so "/static/js/main.js" beats a
// pattern that only agrees on "main.js".
func matchScore(target, pattern string) int {
	targetParts := splitPath(target)
	patternParts := splitPath(pattern)

	i, j := len(targetParts)-1, len(patternParts)-1
	score := 0
	for i >= 0 && j >= 0 {
		if targetParts[i] != patternParts[j] {
			break
		}
		score++
		i--
		j--
	}
	// The basename must agree at minimum. Sharing only a directory name is not
	// a match — it would let "app.js" symbolicate with "vendor.js"'s map.
	if score == 0 {
		return 0
	}
	return score
}

func splitPath(p string) []string {
	parts := strings.Split(path.Clean(p), "/")
	out := parts[:0]
	for _, part := range parts {
		if part != "" && part != "." {
			out = append(out, part)
		}
	}
	return out
}
