// Package version reports the build identity.
//
// It is surfaced on /healthz and stamped on every event we ingest, so that when
// the platform itself misbehaves we can tell which build did it.
package version

import "runtime/debug"

// Version is overridable at build time:
//
//	go build -ldflags "-X github.com/ebnsina/sabab-api/internal/version.Version=1.2.3"
//
// When it is not set, the VCS revision the binary was built from is used, which
// is more useful than "dev" and costs nothing.
var Version = ""

func init() {
	if Version != "" {
		return
	}
	Version = "dev"

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			revision := setting.Value
			if len(revision) > 12 {
				revision = revision[:12]
			}
			Version = "dev+" + revision
			return
		}
	}
}
