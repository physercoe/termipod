// native_tools.go — the native half of the ADR-033 tool registry (W4n).
//
// W1–W4 migrated the authority-backed tools (those with a buildTools()
// REST adapter) into hubmcpserver's ToolSpec registry. The remaining
// ~28 tools are dispatched by a (*Server) method, not a REST adapter,
// so their handler cannot live in a hubmcpserver ToolSpec value
// (hubmcpserver does not import server). W4n adds the missing native
// half here:
//
//   - nativeHandlers — canonical-name → handler map, the data form of
//     the dispatchTool switch it replaces.
//   - nativeToolRegistry() — the ToolSpecs for the same tools, with
//     Description + InputSchema pulled from the existing catalog defs
//     (mcpToolDefsBase/Extra/orchestrationToolDefs) so the migration
//     introduces no schema drift.
//
// The two are CI-locked mutually exhaustive (native_tools_test.go), so
// the four-place lockstep defect class is unrepresentable for native
// tools too. ToolSpec.Backend is "" for a native tool — that empty
// Backend is the marker that dispatch must use a nativeHandler rather
// than dispatchAuthorityToolRaw.
//
// Naming: W4n keeps each native tool's current name as canonical and
// only mechanically flattens the three dotted names (agents.fanout →
// agents_fanout, …) per ADR-033 D-1. A resource-first pass over the
// verb-first names (get_feed → feed_get, …) is deferred until after
// W5 resolves the get_task / list_agents / get_audit duplicate pairs —
// flipping those now would collide with the W4 authority tools
// tasks_get / agents_list / audit_read.

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
// nativeHandler. Splitting by argument shape keeps the handler map a
// table of one-liners and makes a team/agentID mix-up a compile error
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

// nativeHandlers maps a canonical tool name to its handler. This is
// the data form of the dispatchTool switch (W4n removed those cases).
// Deprecated aliases resolve to the canonical name via
// nativeToolRegistry before lookup; see nativeHandlerFor.
var nativeHandlers = map[string]nativeHandler{
	"post_message":            agentArgs((*Server).mcpPostMessage),
	"get_feed":                argsOnly((*Server).mcpGetFeed),
	"list_channels":           teamArgs((*Server).mcpListChannels),
	"search":                  argsOnly((*Server).mcpSearch),
	"journal_append":          teamAgentArgs((*Server).mcpJournalAppend),
	"journal_read":            teamAgent((*Server).mcpJournalRead),
	"get_project_doc":         teamArgs((*Server).mcpGetProjectDoc),
	"get_attention":           argsOnly((*Server).mcpGetAttention),
	"post_excerpt":            agentArgs((*Server).mcpPostExcerpt),
	"delegate":                agentArgs((*Server).mcpDelegate),
	"request_approval":        teamAgentArgs((*Server).mcpRequestApproval),
	"request_select":          teamAgentArgs((*Server).mcpRequestSelect),
	"request_help":            teamAgentArgs((*Server).mcpRequestHelp),
	"request_project_steward": teamAgentArgs((*Server).mcpRequestProjectSteward),
	"attach":                  argsOnly((*Server).mcpAttach),
	"get_event":               argsOnly((*Server).mcpGetEvent),
	"get_task":                argsOnly((*Server).mcpGetTask),
	"get_parent_thread":       agentArgs((*Server).mcpGetParentThread),
	"update_own_task_status":  agentArgs((*Server).mcpUpdateOwnTaskStatus),
	"templates_propose":       teamAgentArgs((*Server).mcpTemplatesPropose),
	"pause_self":              agentArgs((*Server).mcpPauseSelf),
	"shutdown_self":           agentArgs((*Server).mcpShutdownSelf),
	"permission_prompt":       teamAgentArgs((*Server).mcpPermissionPrompt),
	"agents_fanout":           teamArgs((*Server).mcpAgentsFanout),
	"agents_gather":           teamArgs((*Server).mcpAgentsGather),
	"reports_post":            agentArgs((*Server).mcpReportsPost),
}

// nativeToolMeta declares the per-tool metadata the registry needs
// beyond Description + InputSchema (those are pulled from the legacy
// catalog defs). legacyName names the catalog entry carrying the
// schema; for a tool whose name is unchanged it equals name. tier and
// workerEligible mirror tiers.go and roles.yaml as of W4n so dispatch
// behaviour is preserved exactly.
type nativeToolMetaEntry struct {
	name           string
	legacyName     string
	aliases        []string
	short          string
	tier           string
	workerEligible bool
}

var nativeToolMeta = []nativeToolMetaEntry{
	{"post_message", "post_message", nil,
		"Post a message to a channel. Required: channel_id, text.", TierSignificant, true},
	{"get_feed", "get_feed", nil,
		"Read recent events from a channel feed.", TierTrivial, true},
	{"list_channels", "list_channels", nil,
		"List channels visible to the caller.", TierTrivial, true},
	{"search", "search", nil,
		"Full-text search across hub content.", TierTrivial, true},
	{"journal_append", "journal_append", nil,
		"Append an entry to the caller's working journal.", TierRoutine, true},
	{"journal_read", "journal_read", nil,
		"Read the caller's working journal.", TierTrivial, true},
	{"get_project_doc", "get_project_doc", nil,
		"Fetch a project document by reference.", TierTrivial, true},
	{"get_attention", "get_attention", nil,
		"Fetch an attention item by id.", TierTrivial, true},
	{"post_excerpt", "post_excerpt", nil,
		"Post a code/text excerpt to a channel.", TierSignificant, true},
	{"delegate", "delegate", nil,
		"Redirect another agent's work (steward-only).", TierSignificant, false},
	{"request_approval", "request_approval", nil,
		"Raise an approval attention item for the principal.", TierRoutine, true},
	{"request_select", "request_select", []string{"request_decision"},
		"Raise a select/decision attention item for the principal.", TierRoutine, true},
	{"request_help", "request_help", nil,
		"Raise a help attention item for the principal.", TierRoutine, true},
	{"request_project_steward", "request_project_steward", nil,
		"Ask the director to assign a project steward (general-steward delegation).", TierRoutine, false},
	{"attach", "attach", nil,
		"Attach a resource to an entity.", TierRoutine, true},
	{"get_event", "get_event", nil,
		"Fetch an event by id.", TierTrivial, true},
	{"get_task", "get_task", nil,
		"Fetch a task by id.", TierTrivial, true},
	{"get_parent_thread", "get_parent_thread", nil,
		"Fetch the caller's parent-steward conversation thread.", TierTrivial, true},
	{"update_own_task_status", "update_own_task_status", nil,
		"Update the status of the task assigned to the caller.", TierRoutine, true},
	{"templates_propose", "templates_propose", []string{"templates.propose"},
		"Propose a template change for steward review.", TierSignificant, false},
	{"pause_self", "pause_self", nil,
		"Pause the calling agent.", TierRoutine, true},
	{"shutdown_self", "shutdown_self", nil,
		"Terminate the calling agent.", TierSignificant, true},
	{"permission_prompt", "permission_prompt", nil,
		"The tool-call permission gate (used by --permission-prompt-tool).", TierTrivial, true},
	{"agents_fanout", "agents.fanout", []string{"agents.fanout"},
		"Spawn N workers in one orchestrator-worker fan-out.", TierSignificant, false},
	{"agents_gather", "agents.gather", []string{"agents.gather"},
		"Long-poll for the results of a fan-out's workers.", TierTrivial, true},
	{"reports_post", "reports.post", []string{"reports.post"},
		"Post a worker's structured report back to its orchestrator.", TierTrivial, true},
}

// legacyNativeDefs builds a name-keyed index of the pre-registry
// catalog entries (base + extra + orchestration) so a native ToolSpec
// can borrow its sibling's Description + InputSchema verbatim.
func legacyNativeDefs() map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, group := range [][]map[string]any{
		mcpToolDefsBase(), mcpToolDefsExtra(), orchestrationToolDefs(),
	} {
		for _, d := range group {
			if name, _ := d["name"].(string); name != "" {
				out[name] = d
			}
		}
	}
	return out
}

// nativeToolRegistry returns the ToolSpecs for the switch-dispatched
// native tools. Description + InputSchema come from the legacy catalog
// def named by legacyName; Backend is "" — the marker for native
// dispatch.
func nativeToolRegistry() []hubmcpserver.ToolSpec {
	legacy := legacyNativeDefs()
	out := make([]hubmcpserver.ToolSpec, 0, len(nativeToolMeta))
	for _, m := range nativeToolMeta {
		d := legacy[m.legacyName]
		desc, _ := d["description"].(string)
		var raw json.RawMessage
		if sch, ok := d["inputSchema"]; ok {
			raw, _ = json.Marshal(sch)
		}
		out = append(out, hubmcpserver.ToolSpec{
			Name:           m.name,
			Aliases:        m.aliases,
			Short:          m.short,
			Description:    desc,
			InputSchema:    raw,
			Tier:           m.tier,
			WorkerEligible: m.workerEligible,
			Backend:        "",
		})
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

// registryServedNames returns every tool name served by either
// ADR-033 registry — canonical names and deprecated aliases, authority
// and native. mcpToolDefs() drops these from the hand-written legacy
// defs so a migrated tool is listed exactly once (the registry
// re-emits it via RegistryCatalogDefs / nativeRegistryCatalogDefs).
// Covering aliases — not just canonical names — is what lets a D-4
// consolidation retire a twin: list_agents no longer appears as its
// own legacy entry once it is an alias of agents_list.
func registryServedNames() map[string]bool {
	out := map[string]bool{}
	add := func(specs []hubmcpserver.ToolSpec) {
		for _, s := range specs {
			out[s.Name] = true
			for _, a := range s.Aliases {
				out[a] = true
			}
		}
	}
	add(hubmcpserver.ToolRegistry())
	add(nativeToolRegistry())
	return out
}

// nativeRegistryCatalogDefs renders the native registry as catalog
// entries in the []map[string]any shape mcpToolDefs() composes — the
// canonical entry plus one [DEPRECATED] entry per alias.
func nativeRegistryCatalogDefs() []map[string]any {
	specs := nativeToolRegistry()
	out := make([]map[string]any, 0, len(specs))
	for _, s := range specs {
		var schemaObj any
		_ = json.Unmarshal(s.InputSchema, &schemaObj)
		out = append(out, map[string]any{
			"name":        s.Name,
			"description": s.Description,
			"inputSchema": schemaObj,
		})
		for _, a := range s.Aliases {
			out = append(out, map[string]any{
				"name":        a,
				"description": "[DEPRECATED — use " + s.Name + "] " + s.Description,
				"inputSchema": schemaObj,
			})
		}
	}
	return out
}
