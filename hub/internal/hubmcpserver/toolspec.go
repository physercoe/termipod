// toolspec.go — the unified tool registry (ADR-033).
//
// ADR-033 D-3 collapses the catalog's four sources in two shapes into
// one typed ToolSpec list. A ToolSpec carries everything the catalog,
// the tier table, and the role gate need; the dispatch handler is
// wired separately and CI-locked (rollout plan §1, W1).
//
// W1 migrates the `documents` domain as the proof. Migrated tools
// keep their REST-adapter dispatch — the toolDef.call closures in
// tools.go, reached via Dispatch under their canonical snake_case
// name (Backend == Name). Later wedges add native-handler dispatch
// and the remaining domains.
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
// server side.
//
// The ADR-031 D-1 structured-payload fields (ReadOnly, SeeAlso,
// Examples, FailureModes) ride on the spec and surface via tools_get.
// ReadOnly / SeeAlso are populated for every tool by the toolMeta
// overlay (W2.b); Examples / FailureModes are authored per tool by
// W2.b.2 and stay nil until then.
type ToolSpec struct {
	Name           string          // snake_case, resource-first (ADR-033 D-1)
	Aliases        []string        // reserved; empty since WS1.1 retired all dotted/legacy aliases
	Short          string          // one-line contract (ADR-031 D-1)
	Description    string          // full body
	InputSchema    json.RawMessage //
	Tier           string          // permission tier — replaces tiers.go toolTiers
	WorkerEligible bool            // default role eligibility (D-3); stewards always allowed
	Backend        string          // authority dispatch key == Name (non-empty); "" marks a native tool (see native_tools.go)

	// --- ADR-031 D-1 structured payload (W2.b) ---

	// ReadOnly: the tool observes, never mutates. The catalog derives
	// the D-1 pair from it — concurrency_safe == ReadOnly,
	// side_effecting == !ReadOnly. The zero value (false) is the
	// fail-closed reading D-1 mandates: an un-tagged tool is treated
	// as side-effecting and unsafe to batch. (Across the catalog the
	// two D-1 flags always move together; a tool that needs them to
	// diverge would split this into two fields.)
	ReadOnly bool
	// SeeAlso names sibling tools an agent reaching for this one may
	// have actually wanted — the discovery payload (D-1).
	SeeAlso []string
	// Examples / FailureModes — D-1's worked-example and enumerated-
	// failure payload. Authored per tool by W2.b.2; nil until then.
	Examples     []ToolExample
	FailureModes []ToolFailureMode
}

// ToolExample is one worked invocation of a tool (ADR-031 D-1).
type ToolExample struct {
	Description string         `json:"description"`
	Args        map[string]any `json:"args"`
}

// ToolFailureMode is one enumerated 4xx outcome of a tool, with the
// recovery hint an agent should act on (ADR-031 D-1; the hint shape
// mirrors the D-3 error envelope).
type ToolFailureMode struct {
	Code string `json:"code"`
	When string `json:"when"`
	Hint string `json:"hint"`
}

// deprecatedPrefix marks an alias entry in the catalog so a client
// listing tools sees the rename (ADR-033 D-2).
func deprecatedPrefix(canonical string) string {
	return "[DEPRECATED — use " + canonical + "] "
}

// toolRegistry is the unified registry. Authority tools keep their REST
// adapters in buildTools() under their canonical snake_case name; each
// ToolSpec sets Backend == Name (the closure key == the agent-callable name,
// one spelling across every layer) and reuses the closure's Description +
// InputSchema so there is no copy drift. WorkerEligible mirrors what
// roles.yaml grants today (verified per tool) so authz is preserved.
//
//	W1 — documents.   W2 — projects / plans / runs / artifacts.
//	W3 — agents / hosts / reviews / channels / a2a (authority-backed
//	     tools only; the native switch-dispatched tools in these
//	     domains — list_agents, agents.fanout/gather, list_channels —
//	     await the native-dispatch path and migrate in a later wedge).
//	W4 — tasks / schedules + the authority-backed misc tools
//	     (audit.read, policy.read, mobile.navigate, the two channel-
//	     creation tools). The native W4 tools (post_message,
//	     pause_self, templates.propose, …) await the native-dispatch
//	     path too.
func toolRegistry() []ToolSpec {
	tools := buildTools()
	// spec builds one ToolSpec for an authority-backed tool. `name` is the
	// canonical snake_case spelling (ADR-033 D-1): the only agent-callable
	// name, the buildTools() closure key, AND the dispatch key — Backend ==
	// Name. (The dotted resource.verb spellings the closures once carried
	// were retired as callable aliases in WS1.1, then collapsed into the
	// snake name in the naming-unify refactor; Backend stays a distinct
	// field only because native tools set it "" as the authority-vs-native
	// dispatch marker — see native_tools.go.)
	spec := func(name, short, tier string, workerEligible bool) ToolSpec {
		d, _ := findTool(tools, name)
		return ToolSpec{
			Name:           name,
			Short:          short,
			Description:    d.Description,
			InputSchema:    d.InputSchema,
			Tier:           tier,
			WorkerEligible: workerEligible,
			Backend:        name,
		}
	}
	specs := []ToolSpec{
		// --- documents (W1) ---
		spec("documents_list",
			"List documents in the team (rows, not bodies). Optional: project (id).",
			tierTrivial, true),
		spec("documents_get",
			"Fetch one document by id, with its full body. Required: document_id (ULID).",
			tierTrivial, true),
		spec("documents_create",
			"Create a document. Required: project_id, kind, title, and one of content_inline | artifact_id.",
			tierRoutine, true),
		// --- projects (W2) ---
		spec("projects_list",
			"List projects in the team. Optional: kind (goal|standing).",
			tierTrivial, true),
		spec("projects_get",
			"Fetch one project by id. Required: project.",
			tierTrivial, true),
		spec("projects_create",
			"Create a project or project template. Required: name, kind (goal|standing).",
			tierSignificant, false),
		spec("projects_update",
			"Update a project's editable fields (goal, budget, steward, …). Required: project.",
			tierRoutine, false),
		// --- project lifecycle: deliverables + criteria + phase (ADR-044 P1) ---
		spec("deliverables_list",
			"List a project's deliverables (work products gated per phase). Required: project. Optional: phase, state.",
			tierTrivial, true),
		spec("deliverables_get",
			"Fetch one deliverable with its components. Required: project, deliverable.",
			tierTrivial, true),
		spec("criteria_list",
			"List a project's acceptance criteria (the rubric). Required: project. Optional: phase, deliverable_id.",
			tierTrivial, true),
		spec("phase_status",
			"Project lifecycle status for the active phase: phase + deliverables (with components) + criteria/deliverable counts. Required: project.",
			tierTrivial, true),
		spec("deliverables_add_component",
			"Attach a produced document/artifact/run/commit to a deliverable you are materializing. Required: project, deliverable, kind, ref_id.",
			tierRoutine, true),
		spec("deliverables_remove_component",
			"Remove a component you attached to a deliverable. Required: project, deliverable, component.",
			tierRoutine, true),
		spec("deliverables_set_state",
			"Move your own deliverable between draft and in-review. Required: project, deliverable, state. (Ratify via a deliverable.set_state proposal.)",
			tierRoutine, true),
		spec("criteria_set_state",
			"Mark a text/metric criterion met or failed as you complete its work. Required: project, criterion, state. gate criteria are chassis-evaluated.",
			tierRoutine, true),
		// --- plans + steps (W2) ---
		spec("plans_list",
			"List plans, optionally filtered to one project.",
			tierTrivial, true),
		spec("plans_get",
			"Fetch one plan by id. Required: plan.",
			tierTrivial, true),
		spec("plans_create",
			"Create a plan for a project. Required: project, title.",
			tierRoutine, false),
		spec("plan_steps_create",
			"Add a step to a plan. Required: plan, phase_idx, step_idx, kind.",
			tierRoutine, false),
		spec("plan_steps_list",
			"List the steps of a plan. Required: plan.",
			tierTrivial, true),
		spec("plan_steps_update",
			"Update a plan step's status or refs. Required: plan, step.",
			tierRoutine, false),
		// --- runs (W2) ---
		spec("runs_list",
			"List runs, optionally filtered to one project.",
			tierTrivial, true),
		spec("runs_get",
			"Fetch one run by id. Required: run.",
			tierTrivial, true),
		spec("runs_create",
			"Create a run under a project. Required: project_id.",
			tierRoutine, true),
		spec("runs_update",
			"Update an existing run's mutable fields (status, config, or link "+
				"trackio metrics). Required: run. Fixes typos without recreating.",
			tierRoutine, true),
		spec("runs_delete",
			"Delete a run created in error (digests removed, artifacts detached). "+
				"Required: run. Use runs.update status=cancelled to keep it for audit.",
			tierRoutine, false),
		spec("runs_detach_artifact",
			"Unlink a wrongly-attached artifact from a run (keeps the artifact). "+
				"Required: run, artifact.",
			tierRoutine, false),
		spec("runs_attach_artifact",
			"Attach an artifact to a run. Required: run, project_id, kind, name, uri.",
			tierRoutine, true),
		// --- artifacts (W2) ---
		spec("artifacts_list",
			"List artifacts, optionally filtered by project, run, or kind.",
			tierTrivial, true),
		spec("artifacts_get",
			"Fetch one artifact by id. Required: artifact.",
			tierTrivial, true),
		spec("artifacts_create",
			"Create an artifact record. Required: project_id, kind, name, uri.",
			tierRoutine, false),
		// --- agents (W3) ---
		// agents_list absorbed the legacy thin `list_agents` (ADR-033
		// D-4); the `list_agents` alias was retired in WS1.1.
		spec("agents_list",
			"List agents in the team (terminal-status rows hidden by default). Optional: host_id, status, live, project_id, include_terminated, include_archived.",
			tierTrivial, true),
		spec("agents_get",
			"Fetch one agent by id, with full detail. Required: agent (id).",
			tierTrivial, true),
		spec("agents_spawn",
			"Spawn a child agent. Required: child_handle, kind, spawn_spec_yaml, host_id (call hosts.list first). Project-bound spawns require the caller to be that project's steward.",
			tierSignificant, false),
		spec("agents_stop",
			"Stop a worker (reversible): kill the agent, session → paused. Resumable via agents.resume. Required: agent (id).",
			tierRoutine, false),
		spec("agents_terminate",
			"Terminate a worker permanently: kill the agent and archive its session (fork-only, not resumable). Required: agent (id). Use agents.stop if you may want it back.",
			tierSignificant, false),
		spec("agents_resume",
			"Inverse of agents.stop: respawn a stopped agent's paused session (fresh process, continues from worktree+cursor). Required: agent (id).",
			tierRoutine, false),
		// --- hosts (W3) ---
		spec("hosts_list",
			"List host-runners registered with the team (id, name, status, capabilities). No arguments.",
			tierTrivial, true),
		spec("hosts_get",
			"Fetch one host-runner by id. Required: host (id).",
			tierTrivial, true),
		spec("hosts_update_ssh_hint",
			"Patch a host's non-secret ssh_hint_json. Required: host (id), ssh_hint (object). Secret-bearing keys are rejected.",
			tierSignificant, false),
		// --- reviews (W3) ---
		spec("reviews_list",
			"List reviews. Optional: project (id).",
			tierTrivial, true),
		spec("reviews_create",
			"Create a review request. Typical fields: project, document_id, reviewer, question.",
			tierRoutine, true),
		// --- channels (W3) ---
		spec("channels_post_event",
			"Post an event to a channel. Required: channel, type, and a non-empty parts array.",
			tierRoutine, true),
		// --- a2a (W3) ---
		spec("a2a_invoke",
			"Send an A2A message to another agent by handle. Required: handle, text. Workers may target only their parent steward.",
			tierSignificant, true),
		spec("a2a_cards_list",
			"List A2A agent cards in the team — the directory a2a_invoke resolves handles against. Optional: handle (scope to one).",
			tierTrivial, true),
		// --- tasks (W4) ---
		spec("tasks_list",
			"List tasks for a project. Required: project_id. Optional: status, priority, sort.",
			tierTrivial, true),
		// tasks_get absorbed the legacy native `get_task` (ADR-033 D-4);
		// the adapter accepts a bare task id. The `get_task` alias was
		// retired in WS1.1.
		spec("tasks_get",
			"Get one task by id. Required: task (ULID). Optional: project_id.",
			tierTrivial, true),
		spec("tasks_create",
			"Create a task under a project. Required: project_id, title.",
			tierRoutine, true),
		spec("tasks_update",
			"Patch a task's fields (title, body_md, status, priority, …). Required: project_id, task.",
			tierRoutine, true),
		// tasks_complete is the worker-facing close-out verb (ADR-029
		// W2.8). The close-out protocol footer rendered into every
		// worker's CLAUDE.md (renderTaskInstructions) explicitly tells
		// the worker to call this when its task is done, and every
		// bundled worker template's default_capabilities lists it. The
		// pre-fix `false` here contradicted both surfaces and forced
		// workers into request_help to finish their own tasks.
		spec("tasks_complete",
			"Hand off an assigned task for review — bundles status=in_review + result_summary (ADR-029 D-8). Required: project_id, task.",
			tierRoutine, true),
		spec("tasks_delete",
			"Delete a task. Required: project_id, task. Use tasks_update status=cancelled to keep it for the audit trail.",
			tierRoutine, false),
		// --- schedules (W4) ---
		spec("schedules_list",
			"List schedules for the team. Optional: project (id).",
			tierTrivial, true),
		spec("schedules_create",
			"Create a schedule that fires a plan from a template. Required: project_id, template_id, trigger_kind.",
			tierSignificant, false),
		spec("schedules_update",
			"Patch a schedule (enabled, cron_expr, parameters_json). Required: schedule (id).",
			tierRoutine, false),
		spec("schedules_delete",
			"Delete a schedule. Required: schedule (id).",
			tierRoutine, false),
		spec("schedules_run",
			"Manually fire a schedule, returning the new plan_id. Required: schedule (id).",
			tierSignificant, false),
		// --- misc authority-backed (W4) ---
		// audit_read absorbed the legacy `get_audit` (ADR-033 D-4) — same
		// data, same 500 cap, forwards the `action` filter. The
		// `get_audit` alias was retired in WS1.1.
		spec("audit_read",
			"List audit events for the team. Optional: limit, since, action.",
			tierTrivial, true),
		spec("policy_read",
			"Read the team policy document (STUB — returns placeholder rules). No arguments.",
			tierTrivial, true),
		spec("mobile_navigate",
			"Navigate the user's mobile app to a termipod:// URI.",
			tierTrivial, false),
		spec("project_channels_create",
			"Create a channel scoped to one project. Required: project_id, name.",
			tierRoutine, false),
		spec("team_channels_create",
			"Create a team-scope channel. Required: name.",
			tierRoutine, false),
		// --- templates (W6) — 3 categories × 6 ops ---
		spec("templates_agent_create",
			"Create an agent template in the team overlay. Required: name, content.",
			tierSignificant, false),
		spec("templates_agent_update",
			"Update an agent template. Required: name, content (full overwrite).",
			tierSignificant, false),
		spec("templates_agent_delete",
			"Delete an agent template from the team overlay. Required: name.",
			tierSignificant, false),
		spec("templates_agent_list",
			"List agent templates in the team overlay.",
			tierTrivial, true),
		spec("templates_agent_get",
			"Fetch an agent template (overlay merged with the bundled built-in). Required: name.",
			tierTrivial, true),
		spec("templates_agent_scaffold",
			"Return an empty agent-template skeleton to customise.",
			tierTrivial, false),
		spec("templates_prompt_create",
			"Create a prompt template in the team overlay. Required: name, content.",
			tierSignificant, false),
		spec("templates_prompt_update",
			"Update a prompt template. Required: name, content (full overwrite).",
			tierSignificant, false),
		spec("templates_prompt_delete",
			"Delete a prompt template from the team overlay. Required: name.",
			tierSignificant, false),
		spec("templates_prompt_list",
			"List prompt templates in the team overlay.",
			tierTrivial, true),
		spec("templates_prompt_get",
			"Fetch a prompt template (overlay merged with the bundled built-in). Required: name.",
			tierTrivial, true),
		spec("templates_prompt_scaffold",
			"Return an empty prompt-template skeleton to customise.",
			tierTrivial, false),
		spec("templates_plan_create",
			"Create a plan template in the team overlay. Required: name, content.",
			tierSignificant, false),
		spec("templates_plan_update",
			"Update a plan template. Required: name, content (full overwrite).",
			tierSignificant, false),
		spec("templates_plan_delete",
			"Delete a plan template from the team overlay. Required: name.",
			tierSignificant, false),
		spec("templates_plan_list",
			"List plan templates in the team overlay.",
			tierTrivial, true),
		spec("templates_plan_get",
			"Fetch a plan template (overlay merged with the bundled built-in). Required: name.",
			tierTrivial, true),
		spec("templates_plan_scaffold",
			"Return an empty plan-template skeleton to customise.",
			tierTrivial, false),
	}
	applyToolMeta(specs)
	return specs
}

// toolMetaEntry is one row of the ADR-031 D-1 operational/discovery
// overlay. readOnly drives ToolSpec.ReadOnly (and the derived
// concurrency_safe / side_effecting catalog pair); seeAlso drives
// ToolSpec.SeeAlso.
type toolMetaEntry struct {
	readOnly bool
	seeAlso  []string
}

// toolMeta is the W2.b overlay: ADR-031 D-1's ReadOnly + SeeAlso for
// every authority tool, keyed by canonical name. Kept as a table
// rather than spec() arguments so the 66 spec() call sites stay
// readable. Examples / FailureModes are authored separately (W2.b.2).
var toolMeta = map[string]toolMetaEntry{
	"documents_list":                {true, []string{"documents_get", "documents_create"}},
	"documents_get":                 {true, []string{"documents_list", "get_project_doc"}},
	"documents_create":              {false, []string{"documents_list", "reviews_create"}},
	"projects_list":                 {true, []string{"projects_get", "projects_create"}},
	"projects_get":                  {true, []string{"projects_list", "projects_update"}},
	"projects_create":               {false, []string{"projects_update", "templates_plan_create"}},
	"projects_update":               {false, []string{"projects_get", "projects_create"}},
	"deliverables_list":             {true, []string{"deliverables_get", "phase_status"}},
	"deliverables_get":              {true, []string{"deliverables_list", "criteria_list"}},
	"criteria_list":                 {true, []string{"phase_status", "deliverables_list"}},
	"phase_status":                  {true, []string{"criteria_list", "deliverables_list"}},
	"deliverables_add_component":    {false, []string{"deliverables_get", "deliverables_set_state"}},
	"deliverables_remove_component": {false, []string{"deliverables_get", "deliverables_add_component"}},
	"deliverables_set_state":        {false, []string{"deliverables_add_component", "deliverables_get"}},
	"criteria_set_state":            {false, []string{"criteria_list", "phase_status"}},
	"plans_list":                    {true, []string{"plans_get", "plans_create"}},
	"plans_get":                     {true, []string{"plans_list", "plan_steps_list"}},
	"plans_create":                  {false, []string{"plan_steps_create", "projects_create"}},
	"plan_steps_create":             {false, []string{"plan_steps_list", "plans_create"}},
	"plan_steps_list":               {true, []string{"plan_steps_update", "plans_get"}},
	"plan_steps_update":             {false, []string{"plan_steps_list"}},
	"runs_list":                     {true, []string{"runs_get", "runs_create"}},
	"runs_get":                      {true, []string{"runs_list", "artifacts_list"}},
	"runs_create":                   {false, []string{"runs_attach_artifact", "runs_list"}},
	"runs_update":                   {false, []string{"runs_get", "runs_create"}},
	"runs_delete":                   {false, []string{"runs_update", "runs_list"}},
	"runs_detach_artifact":          {false, []string{"runs_get", "artifacts_list"}},
	"runs_attach_artifact":          {false, []string{"runs_get", "artifacts_create"}},
	"artifacts_list":                {true, []string{"artifacts_get", "artifacts_create"}},
	"artifacts_get":                 {true, []string{"artifacts_list", "runs_get"}},
	"artifacts_create":              {false, []string{"artifacts_list", "runs_attach_artifact"}},
	"agents_list":                   {true, []string{"agents_get", "agents_spawn"}},
	"agents_get":                    {true, []string{"agents_list", "get_parent_thread"}},
	"agents_spawn":                  {false, []string{"agents_fanout", "agents_list"}},
	"agents_stop":                   {false, []string{"agents_resume", "agents_list"}},
	"agents_terminate":              {false, []string{"agents_list", "pause_self"}},
	"agents_resume":                 {false, []string{"agents_get", "agents_stop"}},
	"hosts_list":                    {true, []string{"hosts_get", "hosts_update_ssh_hint"}},
	"hosts_get":                     {true, []string{"hosts_list"}},
	"hosts_update_ssh_hint":         {false, []string{"hosts_get"}},
	"reviews_list":                  {true, []string{"reviews_create", "documents_get"}},
	"reviews_create":                {false, []string{"documents_get", "reviews_list"}},
	"channels_post_event":           {false, []string{"list_channels", "project_channels_create"}},
	"a2a_invoke":                    {false, []string{"a2a_cards_list", "request_help"}},
	"a2a_cards_list":                {true, []string{"a2a_invoke", "agents_list"}},
	"tasks_list":                    {true, []string{"tasks_get", "tasks_create"}},
	"tasks_get":                     {true, []string{"tasks_list", "tasks_update"}},
	"tasks_create":                  {false, []string{"tasks_update", "tasks_list"}},
	"tasks_update":                  {false, []string{"tasks_complete", "tasks_get"}},
	"tasks_complete":                {false, []string{"tasks_update", "reports_post"}},
	"tasks_delete":                  {false, []string{"tasks_update"}},
	"schedules_list":                {true, []string{"schedules_create", "schedules_run"}},
	"schedules_create":              {false, []string{"schedules_update", "schedules_run"}},
	"schedules_update":              {false, []string{"schedules_list", "schedules_delete"}},
	"schedules_delete":              {false, []string{"schedules_list"}},
	"schedules_run":                 {false, []string{"schedules_list", "schedules_create"}},
	"audit_read":                    {true, []string{"policy_read", "get_feed"}},
	"policy_read":                   {true, []string{"audit_read"}},
	"mobile_navigate":               {false, []string{"get_feed"}},
	"project_channels_create":       {false, []string{"channels_post_event", "team_channels_create"}},
	"team_channels_create":          {false, []string{"channels_post_event", "project_channels_create"}},
	"templates_agent_create":        {false, []string{"templates_agent_scaffold", "templates_agent_get"}},
	"templates_agent_update":        {false, []string{"templates_agent_get", "templates_agent_list"}},
	"templates_agent_delete":        {false, []string{"templates_agent_list"}},
	"templates_agent_list":          {true, []string{"templates_agent_get", "templates_agent_scaffold"}},
	"templates_agent_get":           {true, []string{"templates_agent_list", "templates_agent_scaffold"}},
	"templates_agent_scaffold":      {true, []string{"templates_agent_create", "templates_agent_get"}},
	"templates_prompt_create":       {false, []string{"templates_prompt_scaffold", "templates_prompt_get"}},
	"templates_prompt_update":       {false, []string{"templates_prompt_get", "templates_prompt_list"}},
	"templates_prompt_delete":       {false, []string{"templates_prompt_list"}},
	"templates_prompt_list":         {true, []string{"templates_prompt_get", "templates_prompt_scaffold"}},
	"templates_prompt_get":          {true, []string{"templates_prompt_list", "templates_prompt_scaffold"}},
	"templates_prompt_scaffold":     {true, []string{"templates_prompt_create", "templates_prompt_get"}},
	"templates_plan_create":         {false, []string{"templates_plan_scaffold", "templates_plan_get"}},
	"templates_plan_update":         {false, []string{"templates_plan_get", "templates_plan_list"}},
	"templates_plan_delete":         {false, []string{"templates_plan_list"}},
	"templates_plan_list":           {true, []string{"templates_plan_get", "templates_plan_scaffold"}},
	"templates_plan_get":            {true, []string{"templates_plan_list", "templates_plan_scaffold"}},
	"templates_plan_scaffold":       {true, []string{"templates_plan_create", "templates_plan_get"}},
}

// applyToolMeta overlays the W2.b ReadOnly + SeeAlso fields onto each
// spec. A spec with no toolMeta row keeps ReadOnly false — the
// fail-closed default D-1 mandates; TestToolMeta_EveryToolCovered
// guards against a tool silently missing its row.
func applyToolMeta(specs []ToolSpec) {
	for i := range specs {
		if m, ok := toolMeta[specs[i].Name]; ok {
			specs[i].ReadOnly = m.readOnly
			specs[i].SeeAlso = m.seeAlso
		}
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

// CatalogEntry projects a ToolSpec to the `map[string]any` catalog
// shape mcp.go composes. `short` is the one-line contract (W2.a);
// `description` is the long body; the ADR-031 D-1 structured-payload
// fields (`concurrency_safe` / `side_effecting` / `see_also` and, when
// authored, `examples` / `failure_modes`) ride alongside. tools/list
// projects this down to `short`; tools_get serves it whole.
func CatalogEntry(name, short, description string, schema any, s ToolSpec) map[string]any {
	e := map[string]any{
		"name":        name,
		"short":       short,
		"description": description,
		"inputSchema": schema,
		// D-1 fail-closed: a non-ReadOnly tool is side-effecting and
		// not safe to batch.
		"concurrency_safe": s.ReadOnly,
		"side_effecting":   !s.ReadOnly,
	}
	if len(s.SeeAlso) > 0 {
		e["see_also"] = s.SeeAlso
	}
	if len(s.Examples) > 0 {
		e["examples"] = s.Examples
	}
	if len(s.FailureModes) > 0 {
		e["failure_modes"] = s.FailureModes
	}
	return e
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
		out = append(out, CatalogEntry(s.Name, s.Short, s.Description, schemaObj, s))
		for _, a := range s.Aliases {
			out = append(out, CatalogEntry(a,
				deprecatedPrefix(s.Name)+s.Short,
				deprecatedPrefix(s.Name)+s.Description,
				schemaObj, s))
		}
	}
	return out
}
