package main

import "log/slog"

// runRestartAll fires host.restart across the fleet — each host-runner
// exits 75, so systemd respawns it with the *same* binary (ADR-028 D-2
// / plan W11). Unlike shutdown-all the hosts come straight back; unlike
// update-all no new binary is fetched. Use it to clear bad state.
//
// It is shutdown-all with a different verb, so it shares runFleetStop.
func runRestartAll(args []string, log *slog.Logger) {
	runFleetStop(args, log, "restart-all")
}
