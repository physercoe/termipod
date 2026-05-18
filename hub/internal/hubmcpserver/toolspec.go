// toolspec.go — the unified tool registry (ADR-033).
//
// ADR-033 D-3 collapses the catalog's four sources in two shapes into
// one typed ToolSpec list. A ToolSpec carries everything the catalog,
// the tier table, and the role gate need; the dispatch handler is
// wired separately and CI-locked (rollout plan §1, W1).
//
// W1 migrates the `documents` domain as the proof. Migrated tools
// keep their REST-adapter dispatch — the toolDef.call closures in
// tools.go, reached via Dispatch — and ToolSpec.Backend names the
// legacy entry that still carries that closure. Later wedges add
// native-handler dispatch and the remaining domains.
package hubmcpserver

import "encoding/json"

// Tier vocabulary (mirrors server/tiers.go). A registry tool carries
// its tier here; tierFor() reads it instead of the toolTiers table.
const (
	tierTrivial     = "trivial"
	tierRoutine     = "routine"
	tierSignificant = "significant"
	tierStrategic   = "strategic"
)

var _ = tierStrategic // reserved; no W1/W2 tool is strategic-tier yet

// ToolSpec is the single declaration for one MCP tool (ADR-033 D-3).
// It is metadata only — the dispatch handler is bound by name on the
// server side. Fields beyond the W1 set (examples, failure_modes,
// see_also, safety flags) are added by the migration wedges as the
// ADR-031 D-1 payload is authored.
type ToolSpec struct {
	Name           string          // snake_case, resource-first (ADR-033 D-1)
	Aliases        []string        // deprecated old names, still resolve (D-2)
	Short          string          // one-line contract (ADR-031 D-1)
	Description    string          // full body
	InputSchema    json.RawMessage //
	Tier           string          // permission tier — replaces tiers.go toolTiers
	WorkerEligible bool            // default role eligibility (D-3); stewards always allowed
	Backend        string          // authority dispatch: the buildTools() name carrying the REST adapter
}

// deprecatedPrefix marks an alias entry in the catalog so a client
// listing tools sees the rename (ADR-033 D-2).
func deprecatedPrefix(canonical string) string {
	return "[DEPRECATED — use " + canonical + "] "
}

// toolRegistry is the unified registry. Migrated authority tools
// keep their REST adapters in buildTools() under the dotted names;
// each ToolSpec names that adapter as both Backend and deprecated
// alias, and reuses its Description + InputSchema so the migration
// introduces no copy drift. WorkerEligible mirrors what roles.yaml
// grants today (verified per tool) so authz behaviour is preserved.
//
//   W1 — documents.   W2 — projects / plans / runs / artifacts.
//   W3 — agents / hosts / reviews / channels / a2a (authority-backed
//        tools only; the native switch-dispatched tools in these
//        domains — list_agents, agents.fanout/gather, list_channels —
//        await the native-dispatch path and migrate in a later wedge).
//   W4 — tasks / schedules + the authority-backed misc tools
//        (audit.read, policy.read, mobile.navigate, the two channel-
//        creation tools). The native W4 tools (post_message,
//        pause_self, templates.propose, …) await the native-dispatch
//        path too.
func toolRegistry() []ToolSpec {
	tools := buildTools()
	// spec builds one ToolSpec for an authority-backed tool.
	spec := func(name, backend, short, tier string, workerEligible bool) ToolSpec {
		d, _ := findTool(tools, backend)
		return ToolSpec{
			Name:           name,
			Aliases:        []string{backend},
			Short:          short,
			Description:    d.Description,
			InputSchema:    d.InputSchema,
			Tier:           tier,
			WorkerEligible: workerEligible,
			Backend:        backend,
		}
	}
	return []ToolSpec{
		// --- documents (W1) ---
		spec("documents_list", "documents.list",
			"List documents in the team (rows, not bodies). Optional: project (id).",
			tierTrivial, true),
		spec("documents_get", "documents.get",
			"Fetch one document by id, with its full body. Required: document_id (ULID).",
			tierTrivial, true),
		spec("documents_create", "documents.create",
			"Create a document. Required: project_id, kind, title, and one of content_inline | artifact_id.",
			tierRoutine, true),
		// --- projects (W2) ---
		spec("projects_list", "projects.list",
			"List projects in the team. Optional: kind (goal|standing).",
			tierTrivial, true),
		spec("projects_get", "projects.get",
			"Fetch one project by id. Required: project.",
			tierTrivial, true),
		spec("projects_create", "projects.create",
			"Create a project or project template. Required: name, kind (goal|standing).",
			tierSignificant, false),
		spec("projects_update", "projects.update",
			"Update a project's editable fields (goal, budget, steward, …). Required: project.",
			tierRoutine, false),
		// --- plans + steps (W2) ---
		spec("plans_list", "plans.list",
			"List plans, optionally filtered to one project.",
			tierTrivial, true),
		spec("plans_get", "plans.get",
			"Fetch one plan by id. Required: plan.",
			tierTrivial, true),
		spec("plans_create", "plans.create",
			"Create a plan for a project. Required: project, title.",
			tierRoutine, false),
		spec("plan_steps_create", "plans.steps.create",
			"Add a step to a plan. Required: plan, phase_idx, step_idx, kind.",
			tierRoutine, false),
		spec("plan_steps_list", "plans.steps.list",
			"List the steps of a plan. Required: plan.",
			tierTrivial, true),
		spec("plan_steps_update", "plans.steps.update",
			"Update a plan step's status or refs. Required: plan, step.",
			tierRoutine, false),
		// --- runs (W2) ---
		spec("runs_list", "runs.list",
			"List runs, optionally filtered to one project.",
			tierTrivial, true),
		spec("runs_get", "runs.get",
			"Fetch one run by id. Required: run.",
			tierTrivial, true),
		spec("runs_create", "runs.create",
			"Create a run under a project. Required: project_id.",
			tierRoutine, true),
		spec("runs_attach_artifact", "runs.attach_artifact",
			"Attach an artifact to a run. Required: run, project_id, kind, name, uri.",
			tierRoutine, true),
		// --- artifacts (W2) ---
		spec("artifacts_list", "artifacts.list",
			"List artifacts, optionally filtered by project, run, or kind.",
			tierTrivial, true),
		spec("artifacts_get", "artifacts.get",
			"Fetch one artifact by id. Required: artifact.",
			tierTrivial, true),
		spec("artifacts_create", "artifacts.create",
			"Create an artifact record. Required: project_id, kind, name, uri.",
			tierRoutine, false),
		// --- agents (W3) ---
		spec("agents_list", "agents.list",
			"List agents in the team (terminal-status rows hidden by default). Optional: host_id, status, live, project_id, include_terminated, include_archived.",
			tierTrivial, true),
		spec("agents_get", "agents.get",
			"Fetch one agent by id, with full detail. Required: agent (id).",
			tierTrivial, true),
		spec("agents_spawn", "agents.spawn",
			"Spawn a child agent. Required: child_handle, kind, spawn_spec_yaml. Project-bound spawns require the caller to be that project's steward.",
			tierSignificant, false),
		spec("agents_terminate", "agents.terminate",
			"Mark an agent terminated; the host-runner kills the process on its next loop. Required: agent (id).",
			tierSignificant, false),
		// --- hosts (W3) ---
		spec("hosts_list", "hosts.list",
			"List host-runners registered with the team (id, name, status, capabilities). No arguments.",
			tierTrivial, true),
		spec("hosts_get", "hosts.get",
			"Fetch one host-runner by id. Required: host (id).",
			tierTrivial, true),
		spec("hosts_update_ssh_hint", "hosts.update_ssh_hint",
			"Patch a host's non-secret ssh_hint_json. Required: host (id), ssh_hint (object). Secret-bearing keys are rejected.",
			tierSignificant, false),
		// --- reviews (W3) ---
		spec("reviews_list", "reviews.list",
			"List reviews. Optional: project (id).",
			tierTrivial, true),
		spec("reviews_create", "reviews.create",
			"Create a review request. Typical fields: project, document_id, reviewer, question.",
			tierRoutine, true),
		// --- channels (W3) ---
		spec("channels_post_event", "channels.post_event",
			"Post an event to a channel. Required: channel, type, and a non-empty parts array.",
			tierRoutine, true),
		// --- a2a (W3) ---
		spec("a2a_invoke", "a2a.invoke",
			"Send an A2A message to another agent by handle. Required: handle, text. Workers may target only their parent steward.",
			tierSignificant, true),
		spec("a2a_cards_list", "a2a.cards.list",
			"List A2A agent cards in the team — the directory a2a_invoke resolves handles against. Optional: handle (scope to one).",
			tierTrivial, true),
		// --- tasks (W4) ---
		spec("tasks_list", "tasks.list",
			"List tasks for a project. Required: project_id. Optional: status, priority, sort.",
			tierTrivial, true),
		spec("tasks_get", "tasks.get",
			"Get one task by id. Required: project_id, task.",
			tierTrivial, true),
		spec("tasks_create", "tasks.create",
			"Create a task under a project. Required: project_id, title.",
			tierRoutine, true),
		spec("tasks_update", "tasks.update",
			"Patch a task's fields (title, body_md, status, priority, …). Required: project_id, task.",
			tierRoutine, true),
		spec("tasks_complete", "tasks.complete",
			"Close out an assigned task — bundles status=done + result_summary. Required: project_id, task.",
			tierRoutine, false),
		spec("tasks_delete", "tasks.delete",
			"Delete a task. Required: project_id, task. Use tasks_update status=cancelled to keep it for the audit trail.",
			tierRoutine, false),
		// --- schedules (W4) ---
		spec("schedules_list", "schedules.list",
			"List schedules for the team. Optional: project (id).",
			tierTrivial, true),
		spec("schedules_create", "schedules.create",
			"Create a schedule that fires a plan from a template. Required: project_id, template_id, trigger_kind.",
			tierSignificant, false),
		spec("schedules_update", "schedules.update",
			"Patch a schedule (enabled, cron_expr, parameters_json). Required: schedule (id).",
			tierRoutine, false),
		spec("schedules_delete", "schedules.delete",
			"Delete a schedule. Required: schedule (id).",
			tierRoutine, false),
		spec("schedules_run", "schedules.run",
			"Manually fire a schedule, returning the new plan_id. Required: schedule (id).",
			tierSignificant, false),
		// --- misc authority-backed (W4) ---
		spec("audit_read", "audit.read",
			"List audit events for the team. Optional: limit, since.",
			tierTrivial, true),
		spec("policy_read", "policy.read",
			"Read the team policy document (STUB — returns placeholder rules). No arguments.",
			tierTrivial, true),
		spec("mobile_navigate", "mobile.navigate",
			"Navigate the user's mobile app to a termipod:// URI.",
			tierTrivial, false),
		spec("project_channels_create", "project_channels.create",
			"Create a channel scoped to one project. Required: project_id, name.",
			tierRoutine, false),
		spec("team_channels_create", "team_channels.create",
			"Create a team-scope channel. Required: name.",
			tierRoutine, false),
	}
}

// ToolRegistry returns the unified ToolSpec list (ADR-033).
func ToolRegistry() []ToolSpec { return toolRegistry() }

// LookupToolSpec resolves a tool name — canonical or a deprecated
// alias — to its ToolSpec. `viaAlias` reports whether `name` was a
// deprecated alias rather than the canonical name.
func LookupToolSpec(name string) (spec ToolSpec, found bool, viaAlias bool) {
	for _, s := range toolRegistry() {
		if s.Name == name {
			return s, true, false
		}
		for _, a := range s.Aliases {
			if a == name {
				return s, true, true
			}
		}
	}
	return ToolSpec{}, false, false
}

// RegistryCatalogDefs returns the registry as catalog entries in the
// `[]map[string]any` shape mcp.go composes. Each spec yields its
// canonical entry plus one entry per deprecated alias — the rename
// stays visible to a client listing tools (ADR-033 D-2).
func RegistryCatalogDefs() []map[string]any {
	specs := toolRegistry()
	out := make([]map[string]any, 0, len(specs)*2)
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
				"description": deprecatedPrefix(s.Name) + s.Description,
				"inputSchema": schemaObj,
			})
		}
	}
	return out
}

// RegistryBackends returns the set of buildTools() names the registry
// has taken over — mcp.go excludes these from the authority catalog
// so a migrated tool is not listed twice.
func RegistryBackends() map[string]bool {
	out := map[string]bool{}
	for _, s := range toolRegistry() {
		if s.Backend != "" {
			out[s.Backend] = true
		}
	}
	return out
}
