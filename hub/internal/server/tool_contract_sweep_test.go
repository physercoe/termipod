package server

import (
	"encoding/json"
	"testing"

	"github.com/termipod/hub/internal/hubmcpserver"
)

// This file is the static half of the L1 contract-conformance rung
// (docs/discussions/agent-driven-system-probing.md §4): catalog-wide
// sweeps that extend the lockstep/alias/tier guards already in
// tool_registry_test.go + native_tools_meta_test.go. Two gaps those
// leave open:
//
//   - required[] is enforced at dispatch (schema_validate.go) and the
//     validator's logic is unit-tested (schema_validate_test.go), but
//     nothing asserts every *catalog* tool that declares required
//     fields actually rejects an empty argument object.
//   - SeeAlso targets (the ADR-031 D-1 discovery payload) are not
//     validated against the registry the way aliases and Backends are.
//
// Both are LLM-free, in-process, and deterministic.

// knownToolNames is the set of every name a caller can resolve —
// canonical tools and deprecated aliases across both registries. Built
// from the registries directly (not the composed catalog) so it is
// complete regardless of how mcpToolDefs composes the two halves.
func knownToolNames() map[string]bool {
	known := map[string]bool{}
	for _, s := range hubmcpserver.ToolRegistry() {
		known[s.Name] = true
		for _, a := range s.Aliases {
			known[a] = true
		}
	}
	for _, tl := range buildNativeTools() {
		known[tl.Name] = true
		for _, a := range tl.Aliases {
			known[a] = true
		}
	}
	return known
}

// schemaDeclaresRequired reports whether a JSON-Schema object declares
// a non-empty top-level `required` array.
func schemaDeclaresRequired(schema json.RawMessage) bool {
	if len(schema) == 0 {
		return false
	}
	var m map[string]any
	if json.Unmarshal(schema, &m) != nil {
		return false
	}
	req, ok := m["required"].([]any)
	return ok && len(req) > 0
}

// TestToolContract_RequiredFieldsRejectEmpty sweeps every tool in both
// registries: if its schema declares required fields, ValidateArgs must
// reject an empty argument object. A tool whose declared contract does
// NOT actually reject the empty call is a silent permissive boundary —
// the agents.spawn host_id incident class.
func TestToolContract_RequiredFieldsRejectEmpty(t *testing.T) {
	checked := 0
	check := func(name string, schema json.RawMessage) {
		if !schemaDeclaresRequired(schema) {
			return
		}
		checked++
		if err := hubmcpserver.ValidateArgs(schema, map[string]any{}); err == nil {
			t.Errorf("tool %q declares required fields but ValidateArgs accepts {} — required[] is not a real boundary", name)
		}
	}
	for _, s := range hubmcpserver.ToolRegistry() {
		check(s.Name, s.InputSchema)
	}
	for _, tl := range buildNativeTools() {
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Errorf("native tool %q: InputSchema fails to marshal: %v", tl.Name, err)
			continue
		}
		check(tl.Name, raw)
	}
	// Non-vacuity floor: the catalog has dozens of tools with required
	// fields. If this sweep ever sees almost none, a refactor has
	// emptied the schemas off the registry specs and the test is
	// silently checking nothing — fail loudly so it gets re-baselined.
	t.Logf("swept %d tools declaring required[]", checked)
	if checked < 15 {
		t.Fatalf("only %d tools had a non-empty required[] — expected dozens; the sweep is likely vacuous (schemas not reaching the specs)", checked)
	}
}

// TestToolContract_SeeAlsoResolves asserts every SeeAlso target an
// agent might follow from tools_get names a real, resolvable tool. A
// target that was renamed or removed sends an agent reaching for a tool
// that 404s.
func TestToolContract_SeeAlsoResolves(t *testing.T) {
	known := knownToolNames()
	checked := 0

	// Authority side: SeeAlso is overlaid onto the registry specs.
	for _, s := range hubmcpserver.ToolRegistry() {
		for _, target := range s.SeeAlso {
			checked++
			if !known[target] {
				t.Errorf("authority tool %q: SeeAlso %q resolves to no catalog tool", s.Name, target)
			}
		}
	}
	// Native side: SeeAlso lives in the nativeToolMeta overlay.
	for name, m := range nativeToolMeta {
		for _, target := range m.seeAlso {
			checked++
			if !known[target] {
				t.Errorf("native tool %q: SeeAlso %q resolves to no catalog tool", name, target)
			}
		}
	}
	// Non-vacuity floor: the D-1 overlay populates SeeAlso for most
	// tools. If almost none are seen, the overlay isn't reaching the
	// specs and the check is hollow.
	t.Logf("checked %d SeeAlso targets", checked)
	if checked < 15 {
		t.Fatalf("only %d SeeAlso targets checked — expected dozens; the SeeAlso overlay is likely not populated", checked)
	}
}
