// Package symbolicate maps minified stack frames back to original source.
//
// This is what turns "main.a3f9.js:1:48213" — which tells a developer nothing —
// into "src/routes/cart.ts:42, in renderCart", with the offending line of code
// shown in context. It is the difference between an error tracker people use
// and one they close.
//
// It runs BEFORE grouping. Fingerprinting a minified frame would produce a hash
// that changes on every deploy, so every release would arrive looking like a
// wave of brand-new issues.
package symbolicate

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/ebnsina/sabab-api/internal/event"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
	"github.com/go-sourcemap/sourcemap"
)

// contextLines is how many lines of source to show either side of the offending
// one. Five is enough to orient a reader without turning the stack view into a
// file browser.
const contextLines = 5

// maxMapBytes caps a single source map. They are routinely megabytes; one that
// is hundreds of megabytes is a mistake or an attack, and either way we must not
// let it exhaust the processor's memory.
const maxMapBytes = 64 << 20 // 64 MiB

// ArtifactStore is where the map bytes live.
type ArtifactStore interface {
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

// ReleaseStore is the index of what was uploaded for which release.
type ReleaseStore interface {
	ReleaseFilesFor(ctx context.Context, projectID uint64, version string) ([]postgres.ReleaseFile, error)
}

// Symbolicator resolves frames through source maps.
type Symbolicator struct {
	releases  ReleaseStore
	artifacts ArtifactStore
	cache     *cache
	log       *slog.Logger
}

// New builds a Symbolicator. Parsed maps are cached for ttl.
func New(releases ReleaseStore, artifacts ArtifactStore, log *slog.Logger) *Symbolicator {
	return &Symbolicator{
		releases:  releases,
		artifacts: artifacts,
		cache:     newCache(15 * time.Minute),
		log:       log,
	}
}

// Symbolicate rewrites e's frames in place.
//
// It is best-effort by design. A missing map, a corrupt map, a frame that
// resolves to nothing — none of these may cost us the event. A minified stack is
// a poor experience; a dropped error is a broken product. Every failure path
// here therefore leaves the original frame intact and returns nil.
func (s *Symbolicator) Symbolicate(ctx context.Context, projectID uint64, release string, e *event.Error) error {
	if release == "" || e == nil {
		// Without a release we cannot know which build's maps to use. Guessing
		// would be worse than not trying: the wrong map produces confidently
		// wrong line numbers, which is how you send someone to debug a function
		// that had nothing to do with it.
		return nil
	}

	files, err := s.releases.ReleaseFilesFor(ctx, projectID, release)
	if err != nil {
		return fmt.Errorf("look up release artifacts: %w", err)
	}
	if len(files) == 0 {
		return nil
	}

	// Index the artifacts by pattern once per event, not once per frame.
	byPattern := make(map[string]postgres.ReleaseFile, len(files))
	patterns := make([]string, 0, len(files))
	for _, f := range files {
		byPattern[f.URLPattern] = f
		patterns = append(patterns, f.URLPattern)
	}

	for i := range e.Exceptions {
		for j := range e.Exceptions[i].Frames {
			s.frame(ctx, projectID, release, &e.Exceptions[i].Frames[j], patterns, byPattern)
		}
	}
	return nil
}

func (s *Symbolicator) frame(
	ctx context.Context,
	projectID uint64,
	release string,
	frame *event.Frame,
	patterns []string,
	byPattern map[string]postgres.ReleaseFile,
) {
	// A frame with no position cannot be mapped — source maps are addressed by
	// line and column.
	if frame.Lineno <= 0 {
		return
	}

	pattern, ok := matchArtifact(frame.Filename, patterns)
	if !ok {
		return
	}
	artifact := byPattern[pattern]

	consumer, err := s.consumer(ctx, projectID, release, artifact)
	if err != nil {
		s.log.Warn("source map unavailable, keeping the minified frame",
			slog.String("file", frame.Filename),
			slog.String("release", release),
			slog.Any("error", err))
		return
	}

	// Source maps are 0-based on columns and 1-based on lines, same as us.
	source, function, line, column, ok := consumer.Source(frame.Lineno, frame.Colno)
	if !ok {
		return
	}

	frame.Filename = source
	frame.AbsPath = source
	frame.Lineno = line
	frame.Colno = column
	if function != "" {
		frame.Function = function
	}
	// The module is what grouping hashes, so it must come from the ORIGINAL
	// source path — not the bundle name, which changes every build.
	frame.Module = moduleFor(source)
	frame.InApp = isInApp(source)

	if content := consumer.SourceContent(source); content != "" {
		frame.PreContext, frame.ContextLine, frame.PostContext = sourceContext(content, line)
	}
}

// consumer returns a parsed source map, fetching and caching it if needed.
func (s *Symbolicator) consumer(ctx context.Context, projectID uint64, release string, artifact postgres.ReleaseFile) (*sourcemap.Consumer, error) {
	key := cacheKey{projectID: projectID, release: release, artifact: artifact.ArtifactKey}
	if consumer, ok := s.cache.get(key); ok {
		return consumer, nil
	}

	body, err := s.artifacts.Get(ctx, artifact.ArtifactKey)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	raw, err := io.ReadAll(io.LimitReader(body, maxMapBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read source map: %w", err)
	}
	if len(raw) > maxMapBytes {
		return nil, fmt.Errorf("source map exceeds %d bytes", maxMapBytes)
	}

	consumer, err := sourcemap.Parse(artifact.URLPattern, raw)
	if err != nil {
		return nil, fmt.Errorf("parse source map: %w", err)
	}

	s.cache.put(key, consumer)
	return consumer, nil
}

// sourceContext returns the lines around the offending one, so the stack view
// can show the actual code rather than just a coordinate.
func sourceContext(content string, line int) (pre []string, contextLine string, post []string) {
	lines := splitLines(content)
	// Source maps are 1-based; slices are not.
	idx := line - 1
	if idx < 0 || idx >= len(lines) {
		return nil, "", nil
	}

	start := max(idx-contextLines, 0)
	end := min(idx+contextLines+1, len(lines))

	pre = append(pre, lines[start:idx]...)
	contextLine = lines[idx]
	post = append(post, lines[idx+1:end]...)
	return pre, contextLine, post
}

// cacheKey identifies a parsed map. The release is part of the key because two
// releases legitimately have different maps for the same filename.
type cacheKey struct {
	projectID uint64
	release   string
	artifact  string
}

// cache holds parsed source maps.
//
// Parsing is the expensive part — a map can be megabytes of VLQ — and a burst of
// errors from one bad deploy means thousands of events all wanting the same
// handful of maps. Without this, each one would refetch from S3 and reparse.
type cache struct {
	ttl     time.Duration
	mu      sync.RWMutex
	entries map[cacheKey]cacheEntry
}

type cacheEntry struct {
	consumer *sourcemap.Consumer
	expires  time.Time
}

func newCache(ttl time.Duration) *cache {
	return &cache{ttl: ttl, entries: make(map[cacheKey]cacheEntry)}
}

func (c *cache) get(key cacheKey) (*sourcemap.Consumer, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expires) {
		return nil, false
	}
	return entry.consumer, true
}

func (c *cache) put(key cacheKey, consumer *sourcemap.Consumer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{consumer: consumer, expires: time.Now().Add(c.ttl)}
}
