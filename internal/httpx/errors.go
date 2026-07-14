// Package httpx is the shared HTTP layer: structured errors, panic recovery,
// request logging.
//
// Every response this platform produces — including the ones nobody planned
// for, like a 404 on a typo'd path or a panic in a handler — is JSON with the
// same shape. An SDK parses our error body to decide whether to retry or to
// drop the events on the floor; handing it Go's default plain-text "404 page
// not found" makes that decision impossible.
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// Error is a failure we can describe to the caller.
type Error struct {
	// Status is the HTTP status code.
	Status int
	// Code is a stable, machine-readable identifier. Clients switch on this;
	// they must never have to match on Message, which is free to change.
	Code string
	// Message is for a human reading logs or a dashboard.
	Message string
	// cause is logged but never sent: it may name internal hosts, tables or
	// keys, and the caller is not entitled to any of it.
	cause error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return e.Code + ": " + e.Message + ": " + e.cause.Error()
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.cause }

// NewError builds an Error without an internal cause.
func NewError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// Wrap attaches an internal cause that will be logged but not disclosed.
func Wrap(status int, code, message string, cause error) *Error {
	return &Error{Status: status, Code: code, Message: message, cause: cause}
}

// The errors every service can produce.
var (
	ErrNotFound         = NewError(http.StatusNotFound, "not_found", "The requested resource does not exist.")
	ErrMethodNotAllowed = NewError(http.StatusMethodNotAllowed, "method_not_allowed", "That method is not allowed on this resource.")
	ErrUnauthorized     = NewError(http.StatusUnauthorized, "unauthorized", "Missing or invalid credentials.")
	ErrForbidden        = NewError(http.StatusForbidden, "forbidden", "You do not have access to this resource.")
	ErrInternal         = NewError(http.StatusInternalServerError, "internal_error", "Something went wrong on our end.")
)

// errorBody is the wire shape of every error response.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteError sends err as JSON and logs it.
//
// An unrecognised error becomes a generic 500: a failure we did not anticipate
// must not leak its internals to an anonymous caller just because we forgot to
// classify it. It is logged in full.
func WriteError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		apiErr = Wrap(ErrInternal.Status, ErrInternal.Code, ErrInternal.Message, err)
	}

	attrs := []any{
		slog.String("code", apiErr.Code),
		slog.Int("status", apiErr.Status),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
	}
	if apiErr.cause != nil {
		attrs = append(attrs, slog.Any("error", apiErr.cause))
	}
	// Only our own failures are worth waking someone for. A client sending a
	// malformed body is not an incident, and logging it at error level would
	// bury the ones that are.
	if apiErr.Status >= http.StatusInternalServerError {
		log.Error("request failed", attrs...)
	} else {
		log.Debug("request rejected", attrs...)
	}

	WriteJSON(w, apiErr.Status, errorBody{Error: errorDetail{
		Code:    apiErr.Code,
		Message: apiErr.Message,
	}})
}

// WriteJSON sends v as JSON.
//
// The body is encoded before the status is written, so an encoding failure
// becomes a 500 rather than a 200 with a truncated body — a client cannot
// recover from the latter, because we already told it everything was fine.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"internal_error","message":"Something went wrong on our end."}}`))
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// NotFound handles any path no route matched, in our JSON shape.
func NotFound(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, log, ErrNotFound)
	}
}
