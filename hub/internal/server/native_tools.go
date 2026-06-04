// native_tools.go — the native half of the ADR-033 tool registry.
//
// W1–W4 migrated the authority-backed tools (those with a buildTools()
// REST adapter) into hubmcpserver's ToolSpec registry. The remaining
// native tools are dispatched by a (*Server) method, not a REST
// adapter, so their handler cannot live in a hubmcpserver ToolSpec
// value (hubmcpserver does not import server).
//
// buildNativeTools() is the single declaration point for every native
// tool (ADR-033 D-3) — name, aliases, catalog metadata, input schema,
// tier, worker-eligibility, and handler in one value. nativeHandlers
// and nativeToolRegistry() are thin derivations of it, so the
// multi-place lockstep defect class is unrepresentable for native
// tools: a tool either is in buildNativeTools() with all its parts, or
// it does not exist. ToolSpec.Backend is "" for a native tool — that
// empty Backend is the marker that dispatch must use a nativeHandler
// rather than dispatchAuthorityToolRaw.
//
// Naming: each native tool keeps its current name as canonical; the
// three dotted orchestration names are flattened (agents.fanout →
// agents_fanout, …) per ADR-033 D-1 with the dotted form kept as a
// deprecated alias. The resource-first pass over the verb-first names
// (get_feed → feed_get, …) remains a reviewable follow-up — see
// docs/plans/tool-catalog-w6-teardown.md §2.

package server

import (
	"context"
	"encoding/json"

	"github.com/termipod/hub/internal/hubmcpserver"
)

// nativeHandler is the uniform dispatch signature for a native MCP
// tool. The dispatcher passes every context value; each adapter uses
// the subset its (*Server) method needs.
type nativeHandler func(s *Server, ctx context.Context, agentID string, scope mcpScope, args json.RawMessage) (any, *jrpcError)

// Adapter constructors lift a (*Server) method expression to a
// nativeHandler. Splitting by argument shape keeps the handler table a
// list of one-liners and makes a team/agentID mix-up a compile error
// where the shapes differ — argsOnly vs teamAgentArgs cannot be
// swapped. (agentArgs and teamArgs share a Go signature; those two
// are cross-checked against the old switch by review + tests.)

func argsOnly(fn func(*Server, context.Context, json.RawMessage) (any, *jrpcError)) nativeHandler {
	return func(s *Server, ctx context.Context, _ string, _ mcpScope, args json.RawMessage) (any, *jrpcError) {
		return fn(s, ctx, args)
	}
}

func agentArgs(fn func(*Server, context.Context, string, json.RawMessage) (any, *jrpcError)) nativeHandler {
	return func(s *Server, ctx context.Context, agentID string, _ mcpScope, args json.RawMessage) (any, *jrpcError) {
		return fn(s, ctx, agentID, args)
	}
}

func teamArgs(fn func(*Server, context.Context, string, json.RawMessage) (any, *jrpcError)) nativeHandler {
	return func(s *Server, ctx context.Context, _ string, scope mcpScope, args json.RawMessage) (any, *jrpcError) {
		return fn(s, ctx, scope.Team, args)
	}
}

func teamAgentArgs(fn func(*Server, context.Context, string, string, json.RawMessage) (any, *jrpcError)) nativeHandler {
	return func(s *Server, ctx context.Context, agentID string, scope mcpScope, args json.RawMessage) (any, *jrpcError) {
		return fn(s, ctx, scope.Team, agentID, args)
	}
}

func teamAgent(fn func(*Server, context.Context, string, string) (any, *jrpcError)) nativeHandler {
	return func(s *Server, ctx context.Context, agentID string, scope mcpScope, _ json.RawMessage) (any, *jrpcError) {
		return fn(s, ctx, scope.Team, agentID)
	}
}

// rawOnly lifts a handler that takes only the raw arguments — the
// shape of the catalog meta-tool mcpToolsGet, which reads no scope.
func rawOnly(fn func(*Server, json.RawMessage) (any, *jrpcError)) nativeHandler {
	return func(s *Server, _ context.Context, _ string, _ mcpScope, args json.RawMessage) (any, *jrpcError) {
		return fn(s, args)
	}
}

// nativeTool is the single declaration for one native MCP tool —
// catalog metadata, input schema, and handler in one value
// (ADR-033 D-3). InputSchema is the JSON-Schema object (the shape the
// catalog emits under "inputSchema"); nativeToolRegistry marshals it
// to the registry's json.RawMessage form.
type nativeTool struct {
	Name           string
	Aliases        []string
	Short          string
	Description    string
	InputSchema    map[string]any
	Tier           string
	WorkerEligible bool
	Handler        nativeHandler
}

// buildNativeTools returns the one table of native MCP tools. Tier and
// WorkerEligible mirror tiers.go and roles.yaml so dispatch behaviour
// is preserved exactly.
func buildNativeTools() []nativeTool {
	return []nativeTool{
		{
			Name:        "post_message",
			Short:       "Post a message to a channel. Required: channel_id, text.",
			Description: "Post a message event to a channel. Text goes into a single text part.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"channel_id": map[string]any{"type": "string"},
					"text":       map[string]any{"type": "string"},
				},
				"required": []string{"channel_id", "text"},
			},
			Tier: TierSignificant, WorkerEligible: true,
			Handler: agentArgs((*Server).mcpPostMessage),
		},
		{
			Name:        "get_feed",
			Short:       "Read recent events from a channel feed.",
			Description: "List recent events in a channel, optionally since a received_ts.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"channel_id": map[string]any{"type": "string"},
					"since":      map[string]any{"type": "string"},
					"limit":      map[string]any{"type": "integer"},
				},
				"required": []string{"channel_id"},
			},
			Tier: TierTrivial, WorkerEligible: true,
			Handler: argsOnly((*Server).mcpGetFeed),
		},
		{
			Name:        "list_channels",
			Short:       "List channels visible to the caller.",
			Description: "List all channels this agent's team can see for a project.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_id": map[string]any{"type": "string"},
				},
				"required": []string{"project_id"},
			},
			Tier: TierTrivial, WorkerEligible: true,
			Handler: teamArgs((*Server).mcpListChannels),
		},
		{
			Name:        "search",
			Short:       "Full-text search across hub content.",
			Description: "Full-text search across event contents (FTS5).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"q":     map[string]any{"type": "string"},
					"limit": map[string]any{"type": "integer"},
				},
				"required": []string{"q"},
			},
			Tier: TierTrivial, WorkerEligible: true,
			Handler: argsOnly((*Server).mcpSearch),
		},
		{
			Name:        "journal_append",
			Short:       "Append an entry to the caller's working journal.",
			Description: "Append an entry to this agent's journal (identity that survives respawns).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"entry":  map[string]any{"type": "string"},
					"header": map[string]any{"type": "string"},
				},
				"required": []string{"entry"},
			},
			Tier: TierRoutine, WorkerEligible: true,
			Handler: teamAgentArgs((*Server).mcpJournalAppend),
		},
		{
			Name:        "journal_read",
			Short:       "Read the caller's working journal.",
			Description: "Read this agent's journal.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Tier: TierTrivial, WorkerEligible: true,
			Handler: teamAgent((*Server).mcpJournalRead),
		},
		{
			Name:        "get_project_doc",
			Short:       "Fetch a project document by reference.",
			Description: "Fetch a FILE from the project's `docs_root` (a filesystem directory of shared human-authored context, e.g. `plans/research-plan.md`). `path` is a FILESYSTEM PATH relative to `docs_root` — NOT a document id. Returns the raw file body.\n\nThis is NOT the tool to read documents created via `documents.create` (those live in the database, not the filesystem) — use `documents.get` with the ULID returned by `documents.create` instead.\n\nReturns 404 if `docs_root` is unset, or if the file doesn't exist within it.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_id": map[string]any{"type": "string"},
					"path": map[string]any{
						"type":        "string",
						"description": "filesystem path relative to project's docs_root (e.g. 'plans/research.md'); NOT a document ULID",
					},
				},
				"required": []string{"project_id", "path"},
			},
			Tier: TierTrivial, WorkerEligible: true,
			Handler: teamArgs((*Server).mcpGetProjectDoc),
		},
		{
			Name:        "get_attention",
			Short:       "Fetch an attention item by id.",
			Description: "List open attention items for this team (decisions / approvals pending).",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"scope": map[string]any{"type": "string"}},
			},
			Tier: TierTrivial, WorkerEligible: true,
			Handler: argsOnly((*Server).mcpGetAttention),
		},
		{
			Name:  "post_excerpt",
			Short: "Post a code/text excerpt to a channel.",
			Description: "Post an excerpt from this agent's own pane as an event. " +
				"The agent supplies the captured text; the hub records the " +
				"line range and a one-line summary so the dashboard can render " +
				"a compact card with a link back to the source pane.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"channel_id": map[string]any{"type": "string"},
					"line_from":  map[string]any{"type": "integer"},
					"line_to":    map[string]any{"type": "integer"},
					"content":    map[string]any{"type": "string"},
					"summary":    map[string]any{"type": "string"},
				},
				"required": []string{"channel_id", "content"},
			},
			Tier: TierSignificant, WorkerEligible: true,
			Handler: agentArgs((*Server).mcpPostExcerpt),
		},
		{
			Name:  "delegate",
			Short: "Redirect another agent's work (steward-only).",
			Description: "Hand a task to another agent by handle. Posts a message event " +
				"with to_ids=[handle] + metadata.context_refs — refs, not prose (§10A).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"to":           map[string]any{"type": "string"},
					"channel_id":   map[string]any{"type": "string"},
					"text":         map[string]any{"type": "string"},
					"context_refs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []string{"to", "channel_id", "text"},
			},
			Tier: TierSignificant, WorkerEligible: false,
			Handler: agentArgs((*Server).mcpDelegate),
		},
		{
			Name:  "propose",
			Short: "Propose a load-bearing change (deliverable state, task status, phase, …) for tiered approval.",
			Description: "Generic governed-action verb (ADR-030). Raises a `propose` attention " +
				"item carrying `change_kind`, `target_ref`, and `change_spec`; on approve the " +
				"system applies the change via the registered apply function. Returns immediately " +
				"with `{request_id, status: \"awaiting_response\"}`. END YOUR TURN AFTER CALLING. " +
				"The authoriser's decision arrives as your next user turn. " +
				"`dry_run: true` returns a preview without inserting a row. " +
				"`addressee_tier` (optional) pins one of `worker|project-steward|general-steward|principal`; " +
				"omit to use the per-kind default from team policy. Workers may only target their own " +
				"`project_id`; stewards may cross projects.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"kind": map[string]any{
						"type":        "string",
						"description": "registered governed-action kind (e.g. 'deliverable.set_state', 'task.set_status')",
					},
					"target_ref": map[string]any{
						"type":        "object",
						"description": "per-kind target identifier; shape varies (e.g. {project_id, task_id})",
					},
					"change_spec": map[string]any{
						"type":        "object",
						"description": "per-kind mutation payload applied on approve",
					},
					"reason":         map[string]any{"type": "string"},
					"addressee_tier": map[string]any{"type": "string"},
					"dry_run":        map[string]any{"type": "boolean"},
				},
				"required": []string{"kind", "target_ref", "change_spec"},
			},
			Tier: TierSignificant, WorkerEligible: true,
			Handler: teamAgentArgs((*Server).mcpPropose),
		},
		{
			Name:  "request_approval",
			Short: "Raise an approval attention item for the principal.",
			Description: "Ask a human (or higher-tier agent) to approve an action. " +
				"Returns immediately with `{id, status: \"awaiting_response\"}`. " +
				"END YOUR TURN AFTER CALLING. The principal's decision arrives as " +
				"your next user turn (e.g. \"Approved\" / \"Rejected: <reason>\"). " +
				"See docs/reference/attention-kinds.md.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tier":       map[string]any{"type": "string"},
					"scope_kind": map[string]any{"type": "string"},
					"scope_id":   map[string]any{"type": "string"},
					"summary":    map[string]any{"type": "string"},
					"severity":   map[string]any{"type": "string"},
				},
				"required": []string{"summary"},
			},
			Tier: TierRoutine, WorkerEligible: true,
			Handler: teamAgentArgs((*Server).mcpRequestApproval),
		},
		{
			Name:    "request_select",
			Short:   "Raise a select/decision attention item for the principal.",
			Description: "Ask for a choice between named options. Creates an " +
				"attention_item. Returns immediately with `{id, status: " +
				"\"awaiting_response\"}`. END YOUR TURN AFTER CALLING. The " +
				"principal's pick arrives as your next user turn (e.g. " +
				"\"Selected: <option>\"). See docs/reference/attention-kinds.md.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question":   map[string]any{"type": "string"},
					"options":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"scope_kind": map[string]any{"type": "string"},
					"scope_id":   map[string]any{"type": "string"},
				},
				"required": []string{"question", "options"},
			},
			Tier: TierRoutine, WorkerEligible: true,
			Handler: teamAgentArgs((*Server).mcpRequestSelect),
		},
		{
			Name:  "request_help",
			Short: "Raise a help attention item for the principal.",
			Description: "Ask the principal for free-text input when the answer space is open. " +
				"USE WHEN: you need clarification (\"did you mean X or Y?\"), direction (\"how would you " +
				"approach this?\"), opinion, or you can't proceed (situation too complex, missing context, " +
				"hand-back). DO NOT USE for binary go/no-go (use request_approval) or when you can list the " +
				"valid answers ahead of time (use request_select). Tiebreaker: when in doubt between kinds, " +
				"prefer the more open one — request_help expands the answer space rather than constraining it. " +
				"`mode` tunes the urgency framing: 'clarify' for routine questions, 'handoff' when you're " +
				"genuinely blocked and need the principal to take over. " +
				"Returns immediately with `{id, status: \"awaiting_response\"}`. END YOUR TURN AFTER CALLING. " +
				"The principal's free-text reply arrives as your next user turn. " +
				"See docs/reference/attention-kinds.md for the full decision tree.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question":   map[string]any{"type": "string", "description": "What you're asking the principal."},
					"context":    map[string]any{"type": "string", "description": "Optional — your own framing of why you're stuck or what you've already considered."},
					"mode":       map[string]any{"type": "string", "enum": []string{"clarify", "handoff"}, "description": "clarify=routine question; handoff=I'm blocked, you may want to take over."},
					"severity":   map[string]any{"type": "string", "enum": []string{"minor", "major", "critical"}},
					"scope_kind": map[string]any{"type": "string"},
					"scope_id":   map[string]any{"type": "string"},
				},
				"required": []string{"question"},
			},
			Tier: TierRoutine, WorkerEligible: true,
			Handler: teamAgentArgs((*Server).mcpRequestHelp),
		},
		{
			Name:  "request_project_steward",
			Short: "Ask the director to assign a project steward (general-steward delegation).",
			Description: "General-steward delegation channel (ADR-025 W4). Use when " +
				"asked to operate inside a project that has no live steward yet — you " +
				"are blocked from `agents.spawn` with a project_id (ADR-025 D2). This " +
				"raises a `project_steward_request` attention item the director taps to " +
				"materialize the project steward via the host-picker sheet. " +
				"`suggested_host_id` prefills the sheet. Returns immediately with " +
				"`{id, status: \"awaiting_response\"}`; END YOUR TURN AFTER CALLING.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_id":        map[string]any{"type": "string", "description": "Target project's id."},
					"reason":            map[string]any{"type": "string", "description": "Why a steward is needed for this project."},
					"suggested_host_id": map[string]any{"type": "string", "description": "Optional — prefill the host picker."},
				},
				"required": []string{"project_id", "reason"},
			},
			Tier: TierRoutine, WorkerEligible: false,
			Handler: teamAgentArgs((*Server).mcpRequestProjectSteward),
		},
		{
			Name:  "attach",
			Short: "Upload a small file as a content-addressed blob.",
			Description: "Upload content (≤25 MiB) into the hub's content-addressed blob store. " +
				"`filename` is a human-friendly label, NOT a server-side path (this tool does NOT " +
				"read files from disk). Supply the bytes via EXACTLY ONE of:\n\n" +
				"  - `content` — plain text/JSON, passed verbatim, NO encoding needed. Prefer this " +
				"whenever the payload is text. This is the convenience path; reach for it unless the " +
				"data is genuinely binary.\n" +
				"  - `content_base64` — base64 for true binary (images, archives, checkpoints). " +
				"Line-wrapped/whitespaced base64 is tolerated (the hub strips whitespace before " +
				"decoding), but it must otherwise be valid base64 — if you pass raw un-encoded text " +
				"here it will fail; use `content` for that.\n\n" +
				"There is NO 32 KB or small-inline limit — the only cap is 25 MiB of decoded bytes. " +
				"Returns `{sha256, size, mime, filename}`.\n\n" +
				"The returned `sha256` is the content-addressed handle: cite it as `blob:sha256/<hex>` in " +
				"artifact URIs, A2A messages, and document references. Same bytes = same sha (dedup'd).\n\n" +
				"To read bytes back, call `blob_get` with the sha.\n\n" +
				"LARGE PAYLOADS: inline `content`/`content_base64` travels through your agent transcript, " +
				"inflating your context — and on M1 (ACP) / M2 (structured-stdio) engines a single " +
				"transcript line is buffered at 1 MiB, so a multi-MB inline call can break the stream. " +
				"For any sizeable file prefer the path marker: emit `<<mcp:attach {\"path\": \"...\"}>>` " +
				"on a line — the host-runner reads the file from your local disk and forwards the bytes " +
				"directly, with no base64 round-trip and nothing large in your transcript.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"filename":       map[string]any{"type": "string", "description": "human-friendly label (e.g. 'screenshot.png'); not a server-side path"},
					"content":        map[string]any{"type": "string", "description": "plain text/JSON, stored verbatim (no encoding). Use this for text; mutually exclusive with content_base64"},
					"content_base64": map[string]any{"type": "string", "description": "base64-encoded bytes for binary (≤25 MiB decoded). Whitespace/newlines tolerated. Mutually exclusive with content"},
					"mime":           map[string]any{"type": "string", "description": "optional; defaults to application/octet-stream"},
				},
				"required": []string{"filename"},
			},
			Tier: TierRoutine, WorkerEligible: true,
			Handler: argsOnly((*Server).mcpAttach),
		},
		{
			Name:  "blob_get",
			Short: "Read bytes from the hub blob store by sha256.",
			Description: "Fetch the bytes of a content-addressed blob by its sha256 (the handle " +
				"returned by `attach`, or the hex portion of a `blob:sha256/<hex>` / " +
				"`hub-blob://<hex>` URI). Returns `{sha256, size, mime, content_base64}` — bytes are " +
				"base64-encoded so the JSON-RPC frame stays printable.\n\n" +
				"Required: ONE of `sha256` (bare 64-char lowercase hex) OR `uri` (full `blob:sha256/...` " +
				"or `hub-blob://...` form). Passing the URI verbatim lets you skip slicing the scheme " +
				"yourself when reading it from an A2A file part.\n\n" +
				"Use this when another agent has called `attach` and referenced the sha in a message, " +
				"artifact row, or document — this tool turns that reference back into the bytes.\n\n" +
				"Returns -32602 if the sha shape is invalid, -32000 \"blob not found\" if no row " +
				"matches, and -32000 \"blob row exists but bytes missing on disk\" if the DB row " +
				"survives without the file (typically a partial-restore operator error).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"sha256": map[string]any{"type": "string", "description": "bare 64-character lowercase hex sha256"},
					"uri":    map[string]any{"type": "string", "description": "alternative to sha256: 'blob:sha256/<hex>' or 'hub-blob://<hex>'"},
				},
			},
			Tier: TierTrivial, WorkerEligible: true,
			Handler: argsOnly((*Server).mcpGetBlob),
		},
		{
			Name:        "get_event",
			Short:       "Fetch an event by id.",
			Description: "Fetch one event by id, including full parts.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": map[string]any{"type": "string"}},
				"required":   []string{"id"},
			},
			Tier: TierTrivial, WorkerEligible: true,
			Handler: argsOnly((*Server).mcpGetEvent),
		},
		{
			Name:        "get_parent_thread",
			Short:       "Fetch the caller's parent-steward conversation thread.",
			Description: "Fetch recent messages from the spawning agent (parent). Useful for respawn continuity.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{"type": "integer"},
				},
			},
			Tier: TierTrivial, WorkerEligible: true,
			Handler: agentArgs((*Server).mcpGetParentThread),
		},
		{
			Name:        "update_own_task_status",
			Short:       "Update the status of the task assigned to the caller.",
			Description: "Update status on a task assigned to this agent. Rejects tasks belonging to others.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string"},
					"status":  map[string]any{"type": "string"},
				},
				"required": []string{"task_id", "status"},
			},
			Tier: TierRoutine, WorkerEligible: true,
			Handler: agentArgs((*Server).mcpUpdateOwnTaskStatus),
		},
		{
			Name:        "templates_propose",
			Short:       "Propose a template change for steward review.",
			Description: "Propose a new or revised template. Creates an attention_item for review.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"category":  map[string]any{"type": "string"},
					"name":      map[string]any{"type": "string"},
					"content":   map[string]any{"type": "string"},
					"rationale": map[string]any{"type": "string"},
				},
				"required": []string{"category", "name", "content"},
			},
			Tier: TierSignificant, WorkerEligible: false,
			Handler: teamAgentArgs((*Server).mcpTemplatesPropose),
		},
		{
			Name:        "pause_self",
			Short:       "Pause the calling agent.",
			Description: "Ask the host-runner to SIGSTOP this agent's pane. Owner must resume manually.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"reason": map[string]any{"type": "string"}},
			},
			Tier: TierRoutine, WorkerEligible: true,
			Handler: agentArgs((*Server).mcpPauseSelf),
		},
		{
			Name:        "shutdown_self",
			Short:       "Terminate the calling agent.",
			Description: "Cleanly terminate this agent. Host-agent removes the tmux pane and may clean up the worktree.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"reason": map[string]any{"type": "string"}},
			},
			Tier: TierSignificant, WorkerEligible: true,
			Handler: agentArgs((*Server).mcpShutdownSelf),
		},
		{
			Name:  "permission_prompt",
			Short: "The tool-call permission gate (used by --permission-prompt-tool).",
			Description: "Approval gate for tool calls (Anthropic permission_prompt contract). " +
				"Returns {behavior:'allow'|'deny', updatedInput|message}. Requests are " +
				"surfaced as attention_items so the principal can approve/deny from the inbox.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tool_name":   map[string]any{"type": "string"},
					"input":       map[string]any{"type": "object"},
					"tool_use_id": map[string]any{"type": "string"},
				},
				"required": []string{"tool_name", "input"},
			},
			Tier: TierTrivial, WorkerEligible: true,
			Handler: teamAgentArgs((*Server).mcpPermissionPrompt),
		},
		{
			Name:    "agents_fanout",
			Short:   "Spawn N workers in one orchestrator-worker fan-out.",
			Description: "Spawn N workers in parallel under one correlation_id. " +
				"Each worker spec carries (handle, kind, spawn_spec_yaml, persona_seed, " +
				"task). The hub creates N agents in one transaction with " +
				"auto_open_session, then posts each worker's task as an input event so " +
				"work starts immediately. Pair with agents.gather to wait.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"correlation_id": map[string]any{"type": "string"},
					"workers": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"handle":          map[string]any{"type": "string"},
								"kind":            map[string]any{"type": "string"},
								"host_id":         map[string]any{"type": "string"},
								"spawn_spec_yaml": map[string]any{"type": "string"},
								"persona_seed":    map[string]any{"type": "string"},
								"permission_mode": map[string]any{"type": "string"},
								"task":            map[string]any{"type": "string"},
							},
							"required": []string{"handle", "kind", "spawn_spec_yaml", "task"},
						},
					},
				},
				"required": []string{"correlation_id", "workers"},
			},
			Tier: TierSignificant, WorkerEligible: false,
			Handler: teamArgs((*Server).mcpAgentsFanout),
		},
		{
			Name:    "agents_gather",
			Short:   "Long-poll for the results of a fan-out's workers.",
			Description: "Long-poll until every agent in a correlation_id either posts " +
				"a worker_report event or reaches terminal status. Returns the per-worker " +
				"result list; the steward synthesizes from there. Times out at " +
				"~10 minutes; partial results are returned on timeout so the steward " +
				"can decide whether to wait again.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"correlation_id": map[string]any{"type": "string"},
					"timeout_s":      map[string]any{"type": "integer"},
				},
				"required": []string{"correlation_id"},
			},
			Tier: TierTrivial, WorkerEligible: true,
			Handler: teamArgs((*Server).mcpAgentsGather),
		},
		{
			Name:    "reports_post",
			Short:   "Post a worker's structured report back to its orchestrator.",
			Description: "Worker writes a typed completion report. Stored as an " +
				"agent_event of kind=worker_report with structured frontmatter " +
				"(status, summary_md, output_artifacts[], budget_used_usd, next_steps[]). " +
				"agents.gather treats this event as the worker's done-signal.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status":           map[string]any{"type": "string"},
					"summary_md":       map[string]any{"type": "string"},
					"output_artifacts": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"budget_used_usd":  map[string]any{"type": "number"},
					"next_steps":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []string{"status", "summary_md"},
			},
			Tier: TierTrivial, WorkerEligible: true,
			Handler: agentArgs((*Server).mcpReportsPost),
		},
		{
			// The catalog meta-tool (ADR-031 W1). It reads mcpToolDefs()
			// rather than being dispatched like the others; folding it
			// here (W6.2) retires the last dispatchTool switch case.
			Name:        "tools_get",
			Short:       "Fetch the full description and input schema for one MCP tool.",
			Description: "Fetch the full description and input schema for one MCP tool by name. Required: tool_name (string). Call tools/list for the available set.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"tool_name": map[string]any{"type": "string"}},
				"required":   []string{"tool_name"},
			},
			Tier: TierTrivial, WorkerEligible: true,
			Handler: rawOnly((*Server).mcpToolsGet),
		},
	}
}

// nativeHandlers maps a canonical tool name to its handler — a
// derivation of buildNativeTools() kept for dispatch lookup.
// Deprecated aliases resolve to the canonical name via
// nativeToolRegistry before lookup; see nativeHandlerFor.
var nativeHandlers = func() map[string]nativeHandler {
	m := map[string]nativeHandler{}
	for _, t := range buildNativeTools() {
		m[t.Name] = t.Handler
	}
	return m
}()

// nativeToolMeta is the W2.b overlay for the native registry — the
// ADR-031 D-1 ReadOnly + SeeAlso fields, keyed by canonical name. It
// mirrors hubmcpserver.toolMeta for the authority side. {readOnly,
// seeAlso}; Examples / FailureModes are authored by W2.b.2.
var nativeToolMeta = map[string]struct {
	readOnly bool
	seeAlso  []string
}{
	"post_message":            {false, []string{"post_excerpt", "channels_post_event"}},
	"get_feed":                {true, []string{"search", "get_event"}},
	"list_channels":           {true, []string{"channels_post_event", "get_feed"}},
	"search":                  {true, []string{"get_feed", "documents_list"}},
	"journal_append":          {false, []string{"journal_read"}},
	"journal_read":            {true, []string{"journal_append", "get_feed"}},
	"get_project_doc":         {true, []string{"documents_get"}},
	"get_attention":           {true, []string{"request_help", "request_approval"}},
	"post_excerpt":            {false, []string{"post_message"}},
	"delegate":                {false, []string{"a2a_invoke", "request_help"}},
	"request_approval":        {false, []string{"request_select", "request_help"}},
	"request_select":          {false, []string{"request_approval", "request_help"}},
	"request_help":            {false, []string{"request_select", "get_attention"}},
	"request_project_steward": {false, []string{"agents_spawn", "delegate"}},
	"attach":                  {false, []string{"blob_get", "artifacts_create", "documents_create"}},
	"blob_get":                {true, []string{"attach", "artifacts_get", "documents_get"}},
	"get_event":               {true, []string{"get_feed", "search"}},
	"get_parent_thread":       {true, []string{"a2a_invoke", "agents_get"}},
	"update_own_task_status":  {false, []string{"tasks_update", "tasks_complete"}},
	"templates_propose":       {false, []string{"templates_agent_scaffold", "templates_agent_create"}},
	"propose":                 {false, []string{"request_approval", "tasks_update", "templates_propose"}},
	"pause_self":              {false, []string{"shutdown_self", "agents_terminate"}},
	"shutdown_self":           {false, []string{"pause_self"}},
	"permission_prompt":       {false, []string{"request_approval"}},
	"agents_fanout":           {false, []string{"agents_gather", "agents_spawn"}},
	"agents_gather":           {true, []string{"agents_fanout", "reports_post"}},
	"reports_post":            {false, []string{"tasks_complete", "agents_gather"}},
	"tools_get":               {true, nil},
}

// nativeToolRegistry returns the ToolSpecs for the native tools,
// derived from buildNativeTools(). InputSchema is marshalled to the
// registry's json.RawMessage form; Backend is "" — the marker for
// native dispatch. The ADR-031 D-1 ReadOnly / SeeAlso fields are
// overlaid from nativeToolMeta.
func nativeToolRegistry() []hubmcpserver.ToolSpec {
	tools := buildNativeTools()
	out := make([]hubmcpserver.ToolSpec, 0, len(tools))
	for _, t := range tools {
		var raw json.RawMessage
		if t.InputSchema != nil {
			raw, _ = json.Marshal(t.InputSchema)
		}
		s := hubmcpserver.ToolSpec{
			Name:           t.Name,
			Aliases:        t.Aliases,
			Short:          t.Short,
			Description:    t.Description,
			InputSchema:    raw,
			Tier:           t.Tier,
			WorkerEligible: t.WorkerEligible,
			Backend:        "",
		}
		if m, ok := nativeToolMeta[t.Name]; ok {
			s.ReadOnly = m.readOnly
			s.SeeAlso = m.seeAlso
		}
		out = append(out, s)
	}
	return out
}

// lookupNativeToolSpec resolves a name — canonical or deprecated alias
// — against the native registry.
func lookupNativeToolSpec(name string) (spec hubmcpserver.ToolSpec, found bool, viaAlias bool) {
	for _, s := range nativeToolRegistry() {
		if s.Name == name {
			return s, true, false
		}
		for _, a := range s.Aliases {
			if a == name {
				return s, true, true
			}
		}
	}
	return hubmcpserver.ToolSpec{}, false, false
}

// lookupToolSpec resolves a name against BOTH ADR-033 registries —
// the authority registry in hubmcpserver and the native registry
// here. It is the server-side combined lookup the catalog, tier
// table, and role gate consult.
func lookupToolSpec(name string) (spec hubmcpserver.ToolSpec, found bool, viaAlias bool) {
	if s, ok, alias := hubmcpserver.LookupToolSpec(name); ok {
		return s, true, alias
	}
	return lookupNativeToolSpec(name)
}

// nativeHandlerFor resolves a name (canonical or alias) to its native
// handler.
func nativeHandlerFor(name string) (nativeHandler, bool) {
	canonical := name
	if spec, ok, _ := lookupNativeToolSpec(name); ok {
		canonical = spec.Name
	}
	h, ok := nativeHandlers[canonical]
	return h, ok
}

// nativeRegistryCatalogDefs renders the native registry as catalog
// entries in the []map[string]any shape mcpToolDefs() composes — the
// canonical entry plus one [DEPRECATED] entry per alias. Each entry
// carries `short`, the long `description`, and the ADR-031 D-1
// structured payload — via hubmcpserver.CatalogEntry, the same
// projection the authority registry uses.
func nativeRegistryCatalogDefs() []map[string]any {
	specs := nativeToolRegistry()
	out := make([]map[string]any, 0, len(specs))
	for _, s := range specs {
		var schemaObj any
		_ = json.Unmarshal(s.InputSchema, &schemaObj)
		out = append(out, hubmcpserver.CatalogEntry(s.Name, s.Short, s.Description, schemaObj, s))
		for _, a := range s.Aliases {
			depPrefix := "[DEPRECATED — use " + s.Name + "] "
			out = append(out, hubmcpserver.CatalogEntry(a,
				depPrefix+s.Short, depPrefix+s.Description, schemaObj, s))
		}
	}
	return out
}
