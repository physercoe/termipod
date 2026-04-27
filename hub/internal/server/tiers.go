package server

import "strings"

// Tier vocabulary for tool-call gating, per
// `docs/steward-sessions.md` §6.5. Stored as the property of the
// *tool definition*, not as a flag the agent picks at call time —
// otherwise an adversarial agent could declassify its own actions.
//
//   trivial     — read-only, idempotent, no external effect.
//                 Never reaches the user (audit-only).
//   routine     — write within the agent's capability scope.
//                 Auto-allowed; visible in audit verbose mode.
//   significant — cross-scope writes, irreversible local effects.
//                 Inline approval card; default-deny on timeout.
//   strategic   — money / identity / policy / external services.
//                 Always asks; reason field; non-default-yes;
//                 optional biometric.
//
// `tierFor(toolName)` is the single lookup. Inputs come from two
// namespaces that flow through `permission_prompt`:
//   1. The MCP catalog this hub serves (post_message, delegate, …)
//      — when the agent calls those directly, our own naming.
//   2. claude-code's own tool surface (Bash, Edit, Read, Write, …)
//      — when claude is launched with --permission-prompt-tool
//      mcp__termipod__permission_prompt and routes *every* tool
//      call through our gate, the tool_name is claude's, not ours.
// The map below covers both. Unknown names default to "routine"
// per §6.5.6 question 4.
const (
	TierTrivial     = "trivial"
	TierRoutine     = "routine"
	TierSignificant = "significant"
	TierStrategic   = "strategic"
)

// toolTiers covers known names, both surfaces. Lookup is
// case-sensitive; claude tools are PascalCase by convention.
// Bash deliberately defaults to routine — the safe-pattern
// allowlist lives at a future Tools-Settings UI (see
// `docs/wedges/transcript-ux-comparison.md` W1.A) and is the
// right place to upgrade specific commands rather than tier the
// generic tool name here.
var toolTiers = map[string]string{
	// --- our MCP catalog (mcp.go + mcp_more.go) ---
	"post_message":           TierSignificant, // team-visible broadcast
	"get_feed":               TierTrivial,
	"list_channels":          TierTrivial,
	"search":                 TierTrivial,
	"journal_append":         TierRoutine,
	"journal_read":           TierTrivial,
	"get_project_doc":        TierTrivial,
	"get_attention":          TierTrivial,
	"post_excerpt":           TierSignificant, // team-visible broadcast
	"delegate":               TierSignificant, // redirects another agent's work
	"request_approval":       TierRoutine,     // meta — wrapped action carries real tier
	"request_decision":       TierRoutine,     // meta — same
	"attach":                 TierRoutine,
	"get_event":              TierTrivial,
	"get_task":               TierTrivial,
	"get_parent_thread":      TierTrivial,
	"list_agents":            TierTrivial,
	"update_own_task_status": TierRoutine,
	"templates_propose":      TierSignificant, // proposes a team-wide change
	"templates.propose":      TierSignificant, // legacy alias accepted by dispatch
	"pause_self":             TierRoutine,
	"shutdown_self":          TierSignificant, // irreversible self-terminate
	"get_audit":              TierTrivial,
	"permission_prompt":      TierTrivial, // the gate itself, not the gated action
	// --- orchestrator-worker primitives (mcp_orchestrate.go) ---
	"agents.fanout": TierSignificant, // spawns N workers; budget impact
	"agents.gather": TierTrivial,     // long-poll read; safe
	"reports.post":  TierTrivial,     // worker writes own report

	// --- rich-authority surface (mcp_authority.go → hubmcpserver) ---
	"projects.list":           TierTrivial,
	"projects.get":            TierTrivial,
	"projects.create":         TierSignificant, // new project = new scope
	"projects.update":         TierRoutine,
	"plans.list":              TierTrivial,
	"plans.get":               TierTrivial,
	"plans.create":            TierRoutine,
	"plans.steps.create":      TierRoutine,
	"plans.steps.list":        TierTrivial,
	"plans.steps.update":      TierRoutine,
	"runs.list":               TierTrivial,
	"runs.get":                TierTrivial,
	"runs.create":             TierRoutine,
	"runs.attach_artifact":    TierRoutine,
	"documents.list":          TierTrivial,
	"documents.create":        TierRoutine,
	"reviews.list":            TierTrivial,
	"reviews.create":          TierRoutine,
	"policy.read":             TierTrivial,
	"artifacts.list":          TierTrivial,
	"artifacts.get":           TierTrivial,
	"artifacts.create":        TierRoutine,
	"agents.spawn":            TierSignificant, // spawns a worker
	"channels.post_event":     TierRoutine,
	"a2a.invoke":              TierSignificant, // peer message
	"hosts.update_ssh_hint":   TierSignificant, // host config change
	"project_channels.create": TierRoutine,
	"team_channels.create":    TierRoutine,
	"tasks.list":              TierTrivial,
	"tasks.get":               TierTrivial,
	"tasks.create":            TierRoutine,
	"tasks.update":            TierRoutine,
	"schedules.list":          TierTrivial,
	"schedules.create":        TierSignificant, // scheduled side effects
	"schedules.update":        TierRoutine,
	"schedules.delete":        TierRoutine,
	"schedules.run":           TierSignificant, // manual fire of a schedule
	"audit.read":              TierTrivial,

	// --- claude-code's own tool surface ---
	"Read":           TierTrivial,
	"Glob":           TierTrivial,
	"Grep":           TierTrivial,
	"WebSearch":      TierTrivial,
	"NotebookRead":   TierTrivial,
	"Edit":           TierRoutine,
	"Write":          TierRoutine,
	"MultiEdit":      TierRoutine,
	"NotebookEdit":   TierRoutine,
	"TodoWrite":      TierRoutine,
	"TodoRead":       TierTrivial,
	"WebFetch":       TierRoutine,
	"Task":           TierSignificant, // spawns a sub-agent
	"Bash":           TierRoutine,     // pattern-aware allowlist lands with W1.A
	"BashOutput":     TierTrivial,
	"KillBash":       TierRoutine,
	"AskUserQuestion": TierTrivial,
	"ExitPlanMode":   TierTrivial,
	"SlashCommand":   TierRoutine,
}

// tierFor returns the tier string for a tool name. Unknowns get
// "routine" — never silently "trivial" (would skip user attention)
// and never silently "strategic" (would block routine work).
func tierFor(toolName string) string {
	if t, ok := toolTiers[toolName]; ok {
		return t
	}
	// Defensive: handle a few common case-insensitive misspellings
	// without enumerating every variant. claude/codex/etc. converge
	// on PascalCase; lower-case fallback catches odd protocol shapes.
	if t, ok := toolTiers[strings.ToLower(toolName)]; ok {
		return t
	}
	return TierRoutine
}
