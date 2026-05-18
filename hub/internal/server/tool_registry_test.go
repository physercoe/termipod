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
// a2a_invoke by their *canonical* name, resolved through the registry.
// If either tool's canonical name drifts, or its old dotted spelling
// stops resolving as an alias, a caller using the un-resolved spelling
// would slip past authorizeAgentsSpawn / authorizeA2ATarget. This test
// pins the two names and both spellings so any such drift trips CI.
func TestDispatchGate_GatedToolNamesResolve(t *testing.T) {
	cases := []struct{ called, wantCanonical string }{
		{"agents_spawn", "agents_spawn"},
		{"agents.spawn", "agents_spawn"},
		{"a2a_invoke", "a2a_invoke"},
		{"a2a.invoke", "a2a_invoke"},
	}
	for _, c := range cases {
		spec, found, _ := hubmcpserver.LookupToolSpec(c.called)
		if !found {
			t.Errorf("gated tool %q does not resolve in the registry — dispatchTool gate would not fire", c.called)
			continue
		}
		if spec.Name != c.wantCanonical {
			t.Errorf("%q resolves to canonical %q, want %q", c.called, spec.Name, c.wantCanonical)
		}
	}
}

// A registry tool resolves under its canonical name and each alias;
// LookupToolSpec reports alias-ness so deprecation can be surfaced.
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
		for _, a := range s.Aliases {
			got, found, viaAlias := hubmcpserver.LookupToolSpec(a)
			if !found || !viaAlias || got.Name != s.Name {
				t.Errorf("alias lookup %q: found=%v viaAlias=%v name=%q", a, found, viaAlias, got.Name)
			}
		}
	}
}
