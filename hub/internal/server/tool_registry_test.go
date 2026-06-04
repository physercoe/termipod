package server

import (
	"testing"

	"github.com/termipod/hub/internal/hubmcpserver"
)

// ADR-033 W1 CI-lock. Every ToolSpec with an authority Backend must
// name a real hubmcpserver tool — otherwise dispatch silently 404s
// the moment the tool is called.
func TestToolRegistry_BackendsResolve(t *testing.T) {
	for _, s := range hubmcpserver.ToolRegistry() {
		if s.Backend == "" {
			continue // native-dispatch specs — none in W1
		}
		if !hubmcpserver.HasTool(s.Backend) {
			t.Errorf("ToolSpec %q: Backend %q is not a hubmcpserver tool", s.Name, s.Backend)
		}
	}
}

// Every registry tool, and every deprecated alias, appears exactly
// once in the composed catalog — no double-listing with a leftover
// authority entry, no collision with a pre-registry tool — and
// resolves a tier.
func TestToolRegistry_CatalogIsConsistent(t *testing.T) {
	count := map[string]int{}
	for _, d := range mcpToolDefs() {
		if n, _ := d["name"].(string); n != "" {
			count[n]++
		}
	}
	for _, s := range hubmcpserver.ToolRegistry() {
		if count[s.Name] != 1 {
			t.Errorf("registry tool %q appears %d time(s) in tools/list, want 1", s.Name, count[s.Name])
		}
		if tierFor(s.Name) == "" {
			t.Errorf("registry tool %q: tierFor returned empty", s.Name)
		}
		for _, a := range s.Aliases {
			if count[a] != 1 {
				t.Errorf("alias %q (of %q) appears %d time(s) in tools/list, want 1", a, s.Name, count[a])
			}
		}
	}
}

// Security-relevant (ADR-033 W3). dispatchTool gates agents_spawn and
// a2a_invoke by their *canonical* name, resolved through the registry. WS1.1
// retired the dotted spellings, so this pins both halves of the invariant:
// (a) the canonical name resolves to itself (the gate keys on it), and (b)
// the old dotted spelling no longer resolves at all — a caller can't reach
// the tool by a spelling that would slip past the gate, because that spelling
// now 404s before dispatch.
func TestDispatchGate_GatedToolNamesResolve(t *testing.T) {
	for _, canonical := range []string{"agents_spawn", "a2a_invoke"} {
		spec, found, _ := hubmcpserver.LookupToolSpec(canonical)
		if !found || spec.Name != canonical {
			t.Errorf("gated tool %q must resolve to itself; found=%v name=%q",
				canonical, found, spec.Name)
		}
	}
	for _, dotted := range []string{"agents.spawn", "a2a.invoke"} {
		if _, found, _ := hubmcpserver.LookupToolSpec(dotted); found {
			t.Errorf("retired alias %q still resolves — WS1.1 retired the dotted spellings", dotted)
		}
	}
}

// ADR-033 W6. mcpToolDefs() no longer composes in authorityToolDefs()
// — every authority tool must be in the ToolSpec registry, or it
// silently vanishes from the catalog. This locks that invariant: a
// new buildTools() tool added without a registry entry trips CI.
func TestEveryAuthorityToolRegistered(t *testing.T) {
	// The catalog names buildTools() adapters by their dotted spelling; since
	// WS1.1 those dotted names are no longer callable (LookupToolSpec won't
	// resolve them), so the invariant is checked against each spec's Backend:
	// every authority adapter must be the Backend of some ToolSpec, or it is
	// invisible in tools/list.
	backends := map[string]bool{}
	for _, s := range hubmcpserver.ToolRegistry() {
		if s.Backend != "" {
			backends[s.Backend] = true
		}
	}
	for _, d := range hubmcpserver.ToolCatalog() {
		name, _ := d["name"].(string)
		if name == "" {
			continue
		}
		if !backends[name] {
			t.Errorf("authority tool %q is not the Backend of any ToolSpec — "+
				"it would be invisible in tools/list (add a spec to toolspec.go)", name)
		}
	}
}

// ADR-033 W5 / D-4 consolidated the duplicate-pair twins (list_agents,
// get_audit, get_task) into their canonical authority tools; WS1.1 then
// retired the twins' names entirely. Each retired name must NO LONGER
// resolve (no alias keeps it alive), while its canonical supersessor does —
// so an agent calling the old name gets a clean 404 instead of a silent hit.
func TestDuplicatePairsConsolidated(t *testing.T) {
	cases := []struct{ retired, canonical string }{
		{"list_agents", "agents_list"},
		{"get_audit", "audit_read"},
		{"get_task", "tasks_get"},
	}
	for _, c := range cases {
		if _, found, _ := lookupToolSpec(c.retired); found {
			t.Errorf("retired twin %q still resolves — WS1.1 retired it; an agent must get 404", c.retired)
		}
		if _, found, _ := lookupToolSpec(c.canonical); !found {
			t.Errorf("canonical %q must resolve", c.canonical)
		}
	}
}

// Every registry tool resolves under its canonical name, and no tool carries
// a (now-retired) alias. LookupToolSpec never reports viaAlias=true post-WS1.1.
func TestToolRegistry_AliasResolution(t *testing.T) {
	specs := hubmcpserver.ToolRegistry()
	if len(specs) == 0 {
		t.Fatal("registry is empty")
	}
	for _, s := range specs {
		got, found, viaAlias := hubmcpserver.LookupToolSpec(s.Name)
		if !found || viaAlias || got.Name != s.Name {
			t.Errorf("canonical lookup %q: found=%v viaAlias=%v name=%q", s.Name, found, viaAlias, got.Name)
		}
		if len(s.Aliases) != 0 {
			t.Errorf("tool %q still carries aliases %v — all were retired in WS1.1", s.Name, s.Aliases)
		}
	}
}
