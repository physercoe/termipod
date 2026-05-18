package server

import (
	"testing"
)

// ADR-033 W4n CI-lock. The native registry's two halves — the
// nativeHandlers map and the nativeToolRegistry spec list — must be
// mutually exhaustive, or a tool ships with a spec but no dispatch
// (invisible 404) or a handler but no catalog entry (uncallable).

func TestNativeRegistry_EverySpecHasHandler(t *testing.T) {
	for _, s := range nativeToolRegistry() {
		if _, ok := nativeHandlers[s.Name]; !ok {
			t.Errorf("native ToolSpec %q has no nativeHandlers entry", s.Name)
		}
	}
}

func TestNativeRegistry_EveryHandlerHasSpec(t *testing.T) {
	specByName := map[string]bool{}
	for _, s := range nativeToolRegistry() {
		specByName[s.Name] = true
	}
	for name := range nativeHandlers {
		if !specByName[name] {
			t.Errorf("nativeHandlers has %q with no native ToolSpec", name)
		}
	}
}

// Every nativeToolMeta.legacyName must name a real pre-registry
// catalog entry — otherwise the spec ships with an empty Description
// and InputSchema.
func TestNativeRegistry_LegacyDefsResolve(t *testing.T) {
	legacy := legacyNativeDefs()
	for _, m := range nativeToolMeta {
		d, ok := legacy[m.legacyName]
		if !ok {
			t.Errorf("native tool %q: legacyName %q is not a catalog entry", m.name, m.legacyName)
			continue
		}
		if desc, _ := d["description"].(string); desc == "" {
			t.Errorf("native tool %q: legacy def %q has no description", m.name, m.legacyName)
		}
	}
}

// Every native canonical name and every deprecated alias appears
// exactly once in the composed catalog and resolves a tier.
func TestNativeRegistry_CatalogIsConsistent(t *testing.T) {
	count := map[string]int{}
	for _, d := range mcpToolDefs() {
		if n, _ := d["name"].(string); n != "" {
			count[n]++
		}
	}
	for _, s := range nativeToolRegistry() {
		if count[s.Name] != 1 {
			t.Errorf("native tool %q appears %d time(s) in tools/list, want 1", s.Name, count[s.Name])
		}
		if tierFor(s.Name) == "" {
			t.Errorf("native tool %q: tierFor returned empty", s.Name)
		}
		for _, a := range s.Aliases {
			if count[a] != 1 {
				t.Errorf("alias %q (of %q) appears %d time(s) in tools/list, want 1", a, s.Name, count[a])
			}
		}
	}
}

// nativeHandlerFor resolves a native tool under its canonical name
// and each deprecated alias.
func TestNativeRegistry_DispatchResolves(t *testing.T) {
	for _, s := range nativeToolRegistry() {
		if _, ok := nativeHandlerFor(s.Name); !ok {
			t.Errorf("nativeHandlerFor(%q) did not resolve", s.Name)
		}
		for _, a := range s.Aliases {
			if _, ok := nativeHandlerFor(a); !ok {
				t.Errorf("nativeHandlerFor(%q) (alias of %q) did not resolve", a, s.Name)
			}
		}
	}
}
