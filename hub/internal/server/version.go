package server

import (
	"runtime/debug"
	"strings"
)

// Commit / BuildTime / Modified are populated from runtime/debug.ReadBuildInfo
// at startup. Go embeds vcs.* settings automatically when `go build` runs
// inside a git tree, so no -ldflags or Makefile is required. Values stay
// empty when the binary was built from a tarball or outside a VCS.
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
