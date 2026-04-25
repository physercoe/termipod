// Package buildinfo exposes the running binary's git revision and build
// time, populated automatically from runtime/debug.ReadBuildInfo when
// `go build` runs inside a git tree. Both hub-server and host-runner
// import this so they self-report consistent build metadata.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

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
