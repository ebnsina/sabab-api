package symbolicate

import "strings"

// splitLines splits source content into lines, tolerating CRLF.
func splitLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.Split(content, "\n")
}

// vendorMarkers identify code the customer did not write. Frames from these are
// collapsed in the stack viewer and — more importantly — excluded from grouping,
// so two unrelated bugs that both went through React are not merged into one
// issue.
var vendorMarkers = []string{
	"node_modules/",
	"webpack/bootstrap",
	"webpack-internal:",
	"/deps/",
	".pnpm/",
	"/vendor/",
}

// isInApp reports whether a source path is the customer's own code.
//
// The default is TRUE: after symbolication we are looking at original paths like
// "src/routes/cart.ts", and treating those as third-party would collapse the
// entire stack and leave the user staring at nothing. We only mark a frame as
// vendor when we can point at a reason.
func isInApp(source string) bool {
	lower := strings.ToLower(source)
	for _, marker := range vendorMarkers {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

// moduleFor turns an original source path into the module name used for
// grouping and shown as the culprit: "src/routes/cart.ts" → "src/routes/cart".
//
// The extension is dropped because renaming cart.js to cart.ts during a
// migration is not a new bug, and keeping the extension would split the issue.
func moduleFor(source string) string {
	module := source

	// Bundlers emit webpack:// and similar scheme prefixes into source maps.
	if i := strings.Index(module, "://"); i >= 0 {
		module = module[i+3:]
	}
	module = strings.TrimPrefix(module, "./")
	module = strings.TrimPrefix(module, "/")

	if i := strings.LastIndex(module, "."); i > strings.LastIndex(module, "/") {
		module = module[:i]
	}
	return module
}
