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
