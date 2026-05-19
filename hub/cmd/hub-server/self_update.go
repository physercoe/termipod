package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/termipod/hub/internal/selfupdate"
)

// runSelfUpdate fetches a release of hub-server from GitHub, verifies
// it against the release SHA256SUMS, and atomically replaces this
// binary on disk (ADR-028 D-4 / plan W7). On success it exits 75 so
// hub-server's own systemd unit (termipod-hub.service, also
// Restart=on-failure) respawns with the new binary; on any failure it
// exits 1 — a generic failure that still respawns the same binary.
//
// Hub-server self-update is a separate, operator-initiated step from
// the host fleet's update-all: hub stays out of the host-fleet exit
// loop (ADR-028 D-2).
func runSelfUpdate(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("self-update", flag.ExitOnError)
	version := fs.String("version", "", "explicit release tag to install (e.g. v1.0.634-alpha); overrides --channel")
	channel := fs.String("channel", "stable", "release channel when --version is unset: stable|alpha")
	repo := fs.String("upstream-repo", selfupdate.DefaultRepo, "GitHub owner/name to fetch releases from")
	installPath := fs.String("install-path", "", "file to replace (default: this binary's resolved path)")
	dryRun := fs.Bool("dry-run", false, "resolve and report the target release without downloading or replacing")
	_ = fs.Parse(args)

	res, err := selfupdate.Run(context.Background(), selfupdate.Options{
		Binary:      "hub-server",
		Repo:        *repo,
		Channel:     *channel,
		Version:     *version,
		InstallPath: *installPath,
		DryRun:      *dryRun,
		Log:         log,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "self-update failed: %v\n", err)
		os.Exit(1)
	}
	if *dryRun {
		fmt.Printf("self-update (dry run): hub-server %s -> %s [no changes made]\n",
			res.FromVersion, res.ToVersion)
		return
	}
	fmt.Printf("self-update: hub-server %s -> %s installed at %s\n",
		res.FromVersion, res.ToVersion, res.InstallPath)
	fmt.Println("exiting 75 so systemd respawns hub-server with the new binary")
	os.Exit(75)
}
