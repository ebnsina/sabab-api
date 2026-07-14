// Package enrich derives searchable fields from what the SDK sent.
//
// The dashboard's most-used filters are "which browser?" and "which OS?", and
// they must be LowCardinality columns to be aggregatable. A raw user-agent
// string is neither — it is high-cardinality free text — so it is parsed once,
// here, on the way in. Doing it at query time instead would mean parsing a
// string on every row of every scan.
package enrich

import "strings"

// Client is what we extract from a user agent.
type Client struct {
	Browser string // "Chrome", "Safari", "Firefox"
	OS      string // "macOS", "Windows", "Android"
}

// browsers is checked in order, because user agents lie by design: Edge claims
// to be Chrome, Chrome claims to be Safari, and everything claims to be Mozilla.
// The most specific, most-often-impersonated names must be tested first, or
// every Edge user is recorded as Chrome.
var browsers = []struct{ token, name string }{
	{"Edg/", "Edge"}, // Edge says "Chrome" AND "Safari" too
	{"EdgA/", "Edge"},
	{"OPR/", "Opera"},
	{"Opera", "Opera"},
	{"SamsungBrowser", "Samsung Internet"},
	{"Firefox/", "Firefox"},
	{"FxiOS/", "Firefox"},
	{"CriOS/", "Chrome"}, // Chrome on iOS
	{"Chrome/", "Chrome"},
	{"Chromium/", "Chromium"},
	{"Safari/", "Safari"}, // last: everyone else claims Safari as well
}

// operatingSystems is likewise ordered most-specific first: an Android UA also
// contains "Linux", and iPadOS still says "Mac OS X" in some modes.
var operatingSystems = []struct{ token, name string }{
	{"Android", "Android"}, // contains "Linux" too
	{"iPhone", "iOS"},
	{"iPad", "iPadOS"},
	{"iPod", "iOS"},
	{"Windows NT", "Windows"},
	{"Mac OS X", "macOS"},
	{"Macintosh", "macOS"},
	{"CrOS", "ChromeOS"},
	{"Ubuntu", "Ubuntu"},
	{"Linux", "Linux"},
}

// ParseUserAgent extracts the browser and OS.
//
// It is deliberately a small ordered table rather than a full UA database: the
// answer only has to be good enough to filter and group by, and a dependency
// that needs monthly updates to keep telling us "Chrome" is not worth it. An
// unrecognised agent yields empty strings, which the schema stores as "" — not
// as a guess.
func ParseUserAgent(ua string) Client {
	if ua == "" {
		return Client{}
	}

	var client Client
	for _, b := range browsers {
		if strings.Contains(ua, b.token) {
			client.Browser = b.name
			break
		}
	}
	for _, os := range operatingSystems {
		if strings.Contains(ua, os.token) {
			client.OS = os.name
			break
		}
	}
	return client
}

// FromContexts pulls the browser and OS out of the SDK's contexts.
//
// An SDK that already knows its browser and version says so explicitly, and
// that is more trustworthy than anything we can infer from a user-agent string.
// We only fall back to parsing when it did not tell us.
func FromContexts(contexts map[string]any, userAgent string) Client {
	client := Client{
		Browser: contextName(contexts, "browser"),
		OS:      contextName(contexts, "os"),
	}
	if client.Browser != "" && client.OS != "" {
		return client
	}

	parsed := ParseUserAgent(userAgent)
	if client.Browser == "" {
		client.Browser = parsed.Browser
	}
	if client.OS == "" {
		client.OS = parsed.OS
	}
	return client
}

// contextName reads contexts["browser"]["name"].
func contextName(contexts map[string]any, key string) string {
	section, ok := contexts[key].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := section["name"].(string)
	return strings.TrimSpace(name)
}
