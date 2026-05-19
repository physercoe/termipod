package main

import (
	"encoding/json"
	"flag"
	"fmt"

	"github.com/termipod/hub/internal/buildinfo"
)

// runVersion implements `hub-server version` (ADR-028 plan W14, local
// half): report the release tag plus the git revision and build time
// embedded by the Go toolchain. The `--remote` fleet fan-out is a
// follow-on wedge that rides the host control verb bus.
func runVersion(args []string) {
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit the build info as JSON")
	_ = fs.Parse(args)

	if *asJSON {
		out := map[string]any{"version": buildinfo.Version}
		if buildinfo.Commit != "" {
			out["commit"] = buildinfo.Commit
			out["modified"] = buildinfo.Modified
		}
		if buildinfo.BuildTime != "" {
			out["build_time"] = buildinfo.BuildTime
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return
	}

	fmt.Printf("hub-server %s\n", buildinfo.Version)
	if buildinfo.Commit != "" {
		mod := ""
		if buildinfo.Modified {
			mod = " (dirty)"
		}
		fmt.Printf("  commit: %s%s\n", buildinfo.Commit, mod)
	}
	if buildinfo.BuildTime != "" {
		fmt.Printf("  built:  %s\n", buildinfo.BuildTime)
	}
}
