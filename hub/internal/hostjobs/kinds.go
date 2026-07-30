// Package hostjobs names the `host_commands` kinds that execute detached on a
// host-runner (ADR-058).
//
// It exists so two consumers cannot drift apart:
//
//   - the host-runner reads it as the executor allowlist — a kind listed here
//     is dispatched to a goroutine, a kind absent from it runs inline on the
//     poll tick;
//   - the hub reads it to scope the stale-heartbeat sweep — only a detached
//     kind is expected to heartbeat, and the teleport kinds
//     (`session_handoff_pack`/`unpack`) still run long *inline*, so sweeping
//     them would fail healthy work.
//
// A kind added to one consumer and forgotten in the other is precisely the
// failure this package removes; there is nowhere else to declare one.
//
// A kind may be listed before its handler exists: the host-runner then fails
// the command with a typed "not supported by this host-runner build" error
// rather than accepting work it cannot do. `dataset_export_rrd` is in that
// state until task #162 lands its handler.
package hostjobs

import "sort"

// Detached job kinds (ADR-058 §1). Each is its own kind with its own typed,
// host-validated args. There is deliberately no generic `job_run(argv)`: the
// command queue is reachable by anyone who can write hub rows for a host, and
// a generic kind would turn it into a remote exec surface.
const (
	// KindDatasetExportRRD exports one LeRobot episode to a `.rrd` through the
	// pinned (lerobot, rerun-sdk) pair. Handler: task #162.
	KindDatasetExportRRD = "dataset_export_rrd"
)

// KindCancel targets a running or queued job by command id.
//
// It is deliberately NOT a job kind. Cancellation has to be serviced *while*
// the single job slot is occupied, so it runs inline on the poll tick like
// pause/resume (ADR-058 §3). Listing it below would make it wait behind the
// very job it is meant to stop.
const KindCancel = "job_cancel"

// detached is the allowlist itself. Keep it in sync with the constants above —
// a constant without an entry here runs inline, which for a long computation
// means blocking the host-runner's single main-loop goroutine.
var detached = map[string]struct{}{
	KindDatasetExportRRD: {},
}

// Is reports whether kind executes detached.
func Is(kind string) bool {
	_, ok := detached[kind]
	return ok
}

// Kinds returns the detached kinds in a stable order, for SQL `IN` clauses
// and log lines.
func Kinds() []string {
	out := make([]string, 0, len(detached))
	for k := range detached {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
