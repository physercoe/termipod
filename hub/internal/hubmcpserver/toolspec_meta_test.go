package hubmcpserver

import "testing"

// Every authority tool must have a toolMeta row (ADR-031 W2.b). A
// missing row leaves ReadOnly false — the fail-closed default — which
// silently mislabels a read tool as side-effecting. This guards the
// overlay against drifting behind the registry.
func TestToolMeta_EveryAuthorityToolCovered(t *testing.T) {
	for _, s := range toolRegistry() {
		if _, ok := toolMeta[s.Name]; !ok {
			t.Errorf("authority tool %q has no toolMeta row — add one (W2.b overlay)", s.Name)
		}
	}
	// And no stale rows: every toolMeta key is a real canonical tool.
	known := map[string]bool{}
	for _, s := range toolRegistry() {
		known[s.Name] = true
	}
	for name := range toolMeta {
		if !known[name] {
			t.Errorf("toolMeta has a row for %q, which is not a canonical authority tool", name)
		}
	}
}

// applyToolMeta must land ReadOnly + SeeAlso on the live registry.
func TestToolMeta_OverlayApplied(t *testing.T) {
	specs := toolRegistry()
	byName := map[string]ToolSpec{}
	for _, s := range specs {
		byName[s.Name] = s
	}
	// documents_get is read-only and points at the cross-tier sibling.
	g := byName["documents_get"]
	if !g.ReadOnly {
		t.Error("documents_get should be ReadOnly")
	}
	if len(g.SeeAlso) == 0 {
		t.Error("documents_get should carry SeeAlso")
	}
	// documents_create mutates.
	if byName["documents_create"].ReadOnly {
		t.Error("documents_create must not be ReadOnly")
	}
}
