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

// toolRegistry is the W1 registry: the `documents` domain, migrated
// as the ADR-033 proof. documents.* were authority tools; their REST
// adapters stay in buildTools() under the dotted names, named here by
// Backend. Short/Description/InputSchema reuse the existing authored
// catalog so W1 is a pure restructuring with no copy drift.
func toolRegistry() []ToolSpec {
	tools := buildTools()
	def := func(name string) toolDef {
		t, _ := findTool(tools, name)
		return t
	}
	docList := def("documents.list")
	docGet := def("documents.get")
	docCreate := def("documents.create")
	return []ToolSpec{
		{
			Name:           "documents_list",
			Aliases:        []string{"documents.list"},
			Short:          "List documents in the team (rows, not bodies). Optional: project (id).",
			Description:    docList.Description,
			InputSchema:    docList.InputSchema,
			Tier:           "trivial", // tier vocabulary: trivial|routine|significant|strategic
			WorkerEligible: true,
			Backend:        "documents.list",
		},
		{
			Name:           "documents_get",
			Aliases:        []string{"documents.get"},
			Short:          "Fetch one document by id, with its full body. Required: document_id (ULID).",
			Description:    docGet.Description,
			InputSchema:    docGet.InputSchema,
			Tier:           "trivial", // tier vocabulary: trivial|routine|significant|strategic
			WorkerEligible: true,
			Backend:        "documents.get",
		},
		{
			Name:           "documents_create",
			Aliases:        []string{"documents.create"},
			Short:          "Create a document. Required: project_id, kind, title, and one of content_inline | artifact_id.",
			Description:    docCreate.Description,
			InputSchema:    docCreate.InputSchema,
			Tier:           "routine",
			WorkerEligible: true,
			Backend:        "documents.create",
		},
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
