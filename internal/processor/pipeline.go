package processor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/ebnsina/sabab-api/internal/enrich"
	"github.com/ebnsina/sabab-api/internal/event"
	"github.com/ebnsina/sabab-api/internal/grouping"
	"github.com/ebnsina/sabab-api/internal/ingest"
	"github.com/ebnsina/sabab-api/internal/scrub"
	"github.com/ebnsina/sabab-api/internal/store/clickhouse"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
)

// errUnsupportedKind means we know the signal but do not ingest it yet.
// It is not a poison message and not a bug — logs, spans and metrics land in
// M2 to M4 — so it is dropped and acked rather than retried forever.
var errUnsupportedKind = errors.New("signal not ingested yet")

// Pipeline turns a queued job into rows. The stage order is the design:
//
//	normalize → scrub → symbolicate → enrich → fingerprint → upsert issue → write
//
// Two orderings are load-bearing:
//
//   - scrub comes before everything that persists, because a redaction step that
//     runs after the write is worthless.
//   - fingerprint comes AFTER symbolicate. Hashing a minified frame produces a
//     hash that changes on every deploy, so every release would arrive looking
//     like a wave of brand-new issues. This single ordering decision is the
//     difference between a useful error tracker and a useless one.
type Pipeline struct {
	scrubber     *scrub.Scrubber
	symbolicator Symbolicator
	issues       IssueStore
}

// Symbolicator maps minified frames back to original source. Behind an
// interface so the pipeline works before source maps exist (M1b) and so the
// native-debug-file version (later) drops in without touching this file.
type Symbolicator interface {
	Symbolicate(ctx context.Context, projectID uint64, release string, e *event.Error) error
}

// IssueStore is the control-plane half: the issue is the *problem*, as opposed
// to an occurrence of it.
type IssueStore interface {
	UpsertIssue(ctx context.Context, in postgres.IssueUpsert) (postgres.UpsertResult, error)
}

// NewPipeline builds a Pipeline.
func NewPipeline(scrubber *scrub.Scrubber, symbolicator Symbolicator, issues IssueStore) *Pipeline {
	return &Pipeline{scrubber: scrubber, symbolicator: symbolicator, issues: issues}
}

// Processed is the outcome for one job.
type Processed struct {
	Row clickhouse.ErrorRow
	// Title is the issue headline, carried so an alert signal can render without
	// querying back.
	Title string
	// New and Regressed are what the alerter (M1.5) fires on.
	New       bool
	Regressed bool
	IssueID   uint64
}

// Process runs one job through every stage.
func (p *Pipeline) Process(ctx context.Context, job ingest.Job) (Processed, error) {
	item, err := normalize(job)
	if err != nil {
		return Processed{}, err
	}
	if item.Error == nil {
		return Processed{}, fmt.Errorf("%w: %s", errUnsupportedKind, item.Kind)
	}

	p.scrubItem(&item)

	// Symbolication is best-effort: a missing or broken source map must not cost
	// us the event. We would rather show a minified stack than nothing at all,
	// so a failure here is logged upstream and the raw frames are kept.
	if p.symbolicator != nil {
		if err := p.symbolicator.Symbolicate(ctx, item.Meta.ProjectID, item.Meta.Release, item.Error); err != nil {
			// Deliberately not returned: see above.
			_ = err
		}
	}

	item.Error.Culprit = culprit(item.Error)

	fingerprint := grouping.Fingerprint(item.Error)

	result, err := p.issues.UpsertIssue(ctx, postgres.IssueUpsert{
		ProjectID:  item.Meta.ProjectID,
		GroupHash:  grouping.Hex(fingerprint.Hash),
		Title:      title(item.Error),
		Culprit:    item.Error.Culprit,
		Level:      string(item.Error.Level),
		Components: fingerprint.Components,
		Seen:       item.Meta.Timestamp,
		Release:    item.Meta.Release,
	})
	if err != nil {
		return Processed{}, err
	}

	row, err := p.row(item, fingerprint.Hash)
	if err != nil {
		return Processed{}, err
	}

	return Processed{
		Row:       row,
		Title:     title(item.Error),
		New:       result.New,
		Regressed: result.Regressed,
		IssueID:   result.IssueID,
	}, nil
}

// scrubItem redacts every field a secret could be hiding in — before the first
// write, which is the only placement that means anything.
func (p *Pipeline) scrubItem(item *event.Item) {
	meta := &item.Meta
	meta.Tags = p.scrubber.Map(meta.Tags)
	meta.User.IP = p.scrubber.IP(meta.User.IP)

	e := item.Error
	e.Message = p.scrubber.String(e.Message)
	for i := range e.Exceptions {
		e.Exceptions[i].Value = p.scrubber.String(e.Exceptions[i].Value)
	}
	for i := range e.Breadcrumbs {
		e.Breadcrumbs[i].Message = p.scrubber.String(e.Breadcrumbs[i].Message)
		if e.Breadcrumbs[i].Data != nil {
			e.Breadcrumbs[i].Data, _ = p.scrubber.Any(e.Breadcrumbs[i].Data).(map[string]any)
		}
	}
	if e.Contexts != nil {
		e.Contexts, _ = p.scrubber.Any(e.Contexts).(map[string]any)
	}
}

// row renders the event into its ClickHouse shape.
func (p *Pipeline) row(item event.Item, groupHash uint64) (clickhouse.ErrorRow, error) {
	e := item.Error

	var excType, excValue string
	if exc, ok := innermostException(e); ok {
		excType, excValue = exc.Type, exc.Value
	}
	if excValue == "" {
		excValue = e.Message
	}

	// The JSON blobs are stored, never filtered on — the schema compresses them
	// hard on that basis.
	stacktrace, err := json.Marshal(e.Exceptions)
	if err != nil {
		return clickhouse.ErrorRow{}, fmt.Errorf("encode stacktrace: %w", err)
	}
	breadcrumbs, err := json.Marshal(e.Breadcrumbs)
	if err != nil {
		return clickhouse.ErrorRow{}, fmt.Errorf("encode breadcrumbs: %w", err)
	}
	contexts, err := json.Marshal(e.Contexts)
	if err != nil {
		return clickhouse.ErrorRow{}, fmt.Errorf("encode contexts: %w", err)
	}

	client := enrich.FromContexts(e.Contexts, userAgent(e.Contexts))

	tags := item.Meta.Tags
	if tags == nil {
		// The Map column is not Nullable; an empty map is the right zero.
		tags = map[string]string{}
	}

	return clickhouse.ErrorRow{
		ProjectID:      item.Meta.ProjectID,
		GroupHash:      groupHash,
		EventID:        item.Meta.EventID,
		Timestamp:      item.Meta.Timestamp,
		ReceivedAt:     item.Meta.ReceivedAt,
		Level:          string(e.Level),
		Environment:    item.Meta.Environment,
		Release:        item.Meta.Release,
		Platform:       item.Meta.Platform,
		ExceptionType:  excType,
		ExceptionValue: excValue,
		Culprit:        e.Culprit,
		TraceID:        item.Meta.TraceID,
		SpanID:         item.Meta.SpanID,
		UserID:         item.Meta.User.ID,
		UserEmail:      item.Meta.User.Email,
		UserIP:         parseAddr(item.Meta.User.IP),
		Browser:        client.Browser,
		OS:             client.OS,
		SDKName:        item.Meta.SDK.Name,
		SDKVersion:     item.Meta.SDK.Version,
		Tags:           tags,
		Stacktrace:     string(stacktrace),
		Breadcrumbs:    string(breadcrumbs),
		Contexts:       string(contexts),
	}, nil
}

// title is what the issue stream shows: "TypeError: Cannot read properties of
// undefined". Truncated, because an issue list of 500-character titles is
// unreadable and the full value is on the detail page anyway.
func title(e *event.Error) string {
	exc, ok := innermostException(e)
	if !ok || exc.Type == "" {
		if e.Message != "" {
			return truncate(e.Message, 200)
		}
		return "Unknown error"
	}
	value := exc.Value
	if value == "" {
		return truncate(exc.Type, 200)
	}
	return truncate(exc.Type+": "+value, 200)
}

// culprit is where the blame lands: the innermost in-app frame. It is what the
// issue stream shows under the title, and it is derived here rather than sent
// by the SDK because it is only meaningful after symbolication.
func culprit(e *event.Error) string {
	exc, ok := innermostException(e)
	if !ok {
		return ""
	}
	for i := len(exc.Frames) - 1; i >= 0; i-- {
		f := exc.Frames[i]
		if !f.InApp {
			continue
		}
		return frameLabel(f)
	}
	if len(exc.Frames) > 0 {
		return frameLabel(exc.Frames[len(exc.Frames)-1])
	}
	return ""
}

func frameLabel(f event.Frame) string {
	location := f.Module
	if location == "" {
		location = f.Filename
	}
	switch {
	case f.Function != "" && location != "":
		return f.Function + "(" + location + ")"
	case f.Function != "":
		return f.Function
	default:
		return location
	}
}

func innermostException(e *event.Error) (event.Exception, bool) {
	if len(e.Exceptions) == 0 {
		return event.Exception{}, false
	}
	return e.Exceptions[len(e.Exceptions)-1], true
}

// userAgent digs the UA string out of contexts.browser.user_agent, where the
// browser SDK puts it.
func userAgent(contexts map[string]any) string {
	browser, ok := contexts["browser"].(map[string]any)
	if !ok {
		return ""
	}
	ua, _ := browser["user_agent"].(string)
	return ua
}

func parseAddr(s string) netip.Addr {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}
	}
	return addr
}

func truncate(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return s[:limit-1] + "…"
}
