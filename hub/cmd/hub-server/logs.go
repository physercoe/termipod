package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
)

// runLogs dispatches `hub-server logs tail` (ADR-028 plan W16). Scoped
// to the LOCAL hub by design: it reads this machine's journald, never
// the tunnel — there is no per-host log fan-out. To read a host's logs,
// run `host-runner` ops on that host.
func runLogs(args []string, log *slog.Logger) {
	if len(args) == 0 || args[0] != "tail" {
		fmt.Fprintln(os.Stderr, "usage: hub-server logs tail [flags]")
		os.Exit(2)
	}
	runLogsTail(args[1:], log)
}

// journalctlArgs builds the journalctl argv for a tail. Split out so the
// flag-to-argv mapping is unit-testable without a systemd host.
func journalctlArgs(unit string, lines int, follow bool) []string {
	a := []string{"-u", unit, "-n", strconv.Itoa(lines), "--no-pager"}
	if follow {
		a = append(a, "-f")
	}
	return a
}

// runLogsTail tails the hub-server systemd unit's journald output. With
// --follow it streams until interrupted (Ctrl-C reaches journalctl via
// the shared process group). On a non-systemd host it reports cleanly
// rather than failing obscurely.
func runLogsTail(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("logs tail", flag.ExitOnError)
	unit := fs.String("unit", "termipod-hub.service", "systemd unit to read logs from")
	lines := fs.Int("lines", 200, "number of recent lines to show")
	follow := fs.Bool("follow", false, "stream new log lines as they arrive (Ctrl-C to stop)")
	_ = fs.Parse(args)
	_ = log

	if _, err := exec.LookPath("journalctl"); err != nil {
		fmt.Fprintln(os.Stderr,
			"logs tail: journalctl not found — this command needs a systemd host. "+
				"If the hub runs in a terminal, read its stderr directly.")
		os.Exit(1)
	}
	cmd := exec.Command("journalctl", journalctlArgs(*unit, *lines, *follow)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// journalctl exits non-zero when interrupted by a signal too;
		// propagate its exit code rather than dressing it as an error.
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "logs tail: %v\n", err)
		os.Exit(1)
	}
}
