package event

import "time"

// Error is a crash, an unhandled rejection, or an explicitly captured exception.
type Error struct {
	Level Level `json:"level"`

	// Exceptions is the chain of causes, innermost last. A Go error wrapped
	// three times, or a JS error with a `cause`, arrives as three entries —
	// flattening that to one loses the frames that actually explain the bug.
	Exceptions []Exception `json:"exception"`

	// Message is set when an event was captured without an exception at all
	// (captureMessage). Grouping falls back to it when Exceptions is empty.
	Message string `json:"message,omitempty"`

	Breadcrumbs []Breadcrumb   `json:"breadcrumbs,omitempty"`
	Contexts    map[string]any `json:"contexts,omitempty"` // browser, os, device, runtime

	// Fingerprint lets the SDK override grouping. The sentinel "{{default}}"
	// means "use your own algorithm", which is what makes it possible to *add*
	// a component rather than replace the whole hash.
	Fingerprint []string `json:"fingerprint,omitempty"`

	// Culprit is derived by the processor after symbolication, never sent by
	// the client: "renderCart(app/cart)".
	Culprit string `json:"-"`
}

// Exception is one link in the cause chain.
type Exception struct {
	Type      string    `json:"type"`  // "TypeError"
	Value     string    `json:"value"` // "Cannot read properties of undefined (reading 'id')"
	Module    string    `json:"module,omitempty"`
	Mechanism Mechanism `json:"mechanism,omitzero"`

	// Frames are ordered outermost-first, so the last frame is where it threw.
	//
	// Structured frames, never a pre-formatted stack string: grouping, source
	// maps and the stack viewer all need real fields. A string here would make
	// every one of them a parsing problem.
	Frames []Frame `json:"frames,omitempty"`
}

// Mechanism records how the error reached us, which is what distinguishes
// "the app called captureException deliberately" from "this crashed the tab".
type Mechanism struct {
	Type string `json:"type,omitempty"` // "onunhandledrejection", "instrument", "generic"
	// Handled is false for a genuine crash. It drives whether an issue is
	// treated as fatal, so it is a pointer: absent and false are not the same
	// claim, and we must not silently upgrade "unknown" to "crashed".
	Handled *bool `json:"handled,omitempty"`
}

// Frame is one stack frame.
type Frame struct {
	Function string `json:"function,omitempty"`
	Module   string `json:"module,omitempty"`   // "app/cart"
	Filename string `json:"filename,omitempty"` // "/static/js/main.a3f9.js"
	AbsPath  string `json:"abs_path,omitempty"`

	Lineno int `json:"lineno,omitempty"`
	Colno  int `json:"colno,omitempty"`

	// InApp separates the customer's code from their dependencies. The stack
	// viewer collapses everything else, and grouping prefers in-app frames —
	// hashing node_modules frames would group unrelated bugs together.
	InApp bool `json:"in_app"`

	// Source context, filled in by the symbolicator from the source map.
	PreContext  []string `json:"pre_context,omitempty"`
	ContextLine string   `json:"context_line,omitempty"`
	PostContext []string `json:"post_context,omitempty"`
}

// Breadcrumb is one thing the app did before it broke.
type Breadcrumb struct {
	Timestamp time.Time      `json:"ts"`
	Type      string         `json:"type,omitempty"`     // "default", "http", "navigation"
	Category  string         `json:"category,omitempty"` // "ui.click", "fetch"
	Level     Level          `json:"level,omitempty"`
	Message   string         `json:"message,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}
