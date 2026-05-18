package server

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestNativeDefs_MatchLegacy is the ADR-033 W6.2 verified-move
// checkpoint. buildNativeTools() carries a fresh copy of each native
// tool's Description + InputSchema; this asserts the copy is faithful
// to the pre-registry catalog def (mcpToolDefsBase/Extra/
// orchestrationToolDefs) it replaces. Green here means W6.2 commit B
// can delete the legacy defs — and this whole file — safely.
func TestNativeDefs_MatchLegacy(t *testing.T) {
	legacy := legacyNativeDefs()
	legacyName := map[string]string{}
	for _, m := range nativeToolMeta {
		legacyName[m.name] = m.legacyName
	}
	for _, nt := range buildNativeTools() {
		ln, ok := legacyName[nt.Name]
		if !ok {
			t.Errorf("%q: no nativeToolMeta entry to name its legacy def", nt.Name)
			continue
		}
		d, ok := legacy[ln]
		if !ok {
			t.Errorf("%q: legacy def %q is missing", nt.Name, ln)
			continue
		}
		if desc, _ := d["description"].(string); desc != nt.Description {
			t.Errorf("%q description drift:\n new: %q\n old: %q", nt.Name, nt.Description, desc)
		}
		// json.Marshal of a Go map sorts keys, so this compares the
		// schemas canonically — key order in the literal is irrelevant.
		newJSON, _ := json.Marshal(nt.InputSchema)
		oldJSON, _ := json.Marshal(d["inputSchema"])
		if !bytes.Equal(newJSON, oldJSON) {
			t.Errorf("%q inputSchema drift:\n new: %s\n old: %s", nt.Name, newJSON, oldJSON)
		}
	}
}
