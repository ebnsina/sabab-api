package api

import (
	"net/http"
	"time"

	"github.com/ebnsina/sabab-api/internal/cursor"
	"github.com/ebnsina/sabab-api/internal/httpx"
	"github.com/google/uuid"
)

// timeCursor paginates a purely timestamp-ordered stream (logs).
type timeCursor struct {
	T time.Time `json:"t"`
}

// timeUUIDCursor paginates a timestamp-ordered stream that needs a unique
// tiebreaker at equal timestamps (events by event_id, traces by trace_id).
type timeUUIDCursor struct {
	T  time.Time `json:"t"`
	ID uuid.UUID `json:"id"`
}

// decodeCursor reads the ?cursor= token into out. A malformed token is a client
// error, not a silent fall back to page one — a tampered cursor should fail
// loudly rather than quietly re-serve the first page.
func decodeCursor(r *http.Request, out any) error {
	if err := cursor.Decode(r.URL.Query().Get("cursor"), out); err != nil {
		return httpx.NewError(http.StatusBadRequest, "bad_cursor", "The pagination cursor is invalid.")
	}
	return nil
}

// paginated shapes the standard list response: the items under `key`, plus a
// `next_cursor` only when a further page exists.
func paginated(key string, items any, nextCursor string) map[string]any {
	resp := map[string]any{key: items}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
	}
	return resp
}
