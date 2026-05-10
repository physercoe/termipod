// Package buildinfo exposes the running binary's release version, git
// revision, and build time. Both hub-server and host-runner import this
// so they self-report consistent build metadata.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Version is the release tag the hub/host-runner binaries report.
// MUST match pubspec.yaml's `version:` (without the +N build suffix) so
// mobile and hub use the same x.y.z-alpha numbering. Use
// `make bump VERSION=...` from the repo root to update both files
// atomically.
const Version = "1.0.472-alpha"

var (
	Commit    string
	BuildTime string
	Modified  bool
)

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			Commit = s.Value
		case "vcs.time":
			BuildTime = s.Value
		case "vcs.modified":
			Modified = strings.EqualFold(s.Value, "true")
		}
	}
}
