package server

// Agent terminal statuses — ONE definition, because this predicate decides
// whether an agent still counts as alive and had drifted into two spellings.
//
// The lifecycle (docs/reference/glossary.md, ADR-009) is
//
//	pending → running → terminated | crashed | failed → archived
//
// so all three of terminated/crashed/failed are ends of the line:
//
//   - terminated — the operator stopped it (or a swap replaced it);
//   - crashed    — the process died under it (the host-runner's reconcile
//     loop reports this when a pane is gone or a driver is missing);
//   - failed     — it stopped itself with an error.
//
// They differ only in *why* the process ended, never in *whether* it did, so
// any query asking "is this agent still alive" must treat them identically.
// Before this constant existed, handleDeleteHost listed only two of the
// three, which made a crashed agent — a corpse by every other query's
// reckoning — permanently block deletion of the host it died on.
//
// archived_at is a separate axis: archiving is only reachable from a
// terminal status (handleArchiveAgent), so a live-agent check never needs it.
const (
	// sqlAgentNotTerminal is the WHERE-clause fragment for "this agent is
	// still alive". Concatenate it into a query; it names no bind
	// parameters and no table qualifier, so it composes anywhere `agents`
	// is the only table in scope.
	sqlAgentNotTerminal = `status NOT IN ('terminated','crashed','failed')`
)

// agentTerminalStatuses is the same set as Go values, for callers deciding in
// code rather than in SQL. Keep in lockstep with sqlAgentNotTerminal —
// TestAgentTerminalStatusesAgreeWithSQL asserts they do.
var agentTerminalStatuses = []string{"terminated", "crashed", "failed"}

// isAgentTerminal reports whether an agent status means the process is over.
func isAgentTerminal(status string) bool {
	for _, s := range agentTerminalStatuses {
		if status == s {
			return true
		}
	}
	return false
}
