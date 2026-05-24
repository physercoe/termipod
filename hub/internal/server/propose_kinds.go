package server

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
)

// ProposeKind describes one entry in the governed-actions registry that
// the `propose` MCP verb (W4) dispatches through. Three function fields:
//
//   - Validate runs at propose-call time, before any attention row is
//     written. Returns an error to fail-fast with a 400; nil = accept.
//   - DryRun runs when the caller passes dry_run=true; returns a JSON
//     preview the awaiting_response payload echoes back to the agent.
//   - Apply runs after /decide(approve) reaches quorum; the returned
//     `executed` payload is mirrored into attention_items.executed_json
//     and fanned back to the requester's session.
//
// Registration is via `init()` blocks in each per-kind apply file
// (W5/W6/W7/W8). Lookup is via LookupProposeKind. The linter
// (`scripts/lint-governed-actions.sh`) statically greps for
// `RegisterProposeKind(` calls to enumerate registered kinds at CI
// time without booting a hub.
type ProposeKind struct {
	// Kind is the dot-separated identifier the propose verb's `kind`
	// argument matches against (e.g. "deliverable.set_state",
	// "task.set_status"). Must be a snake_case-with-dots token —
	// the linter enforces the shape.
	Kind string

	// Validate is called before the attention row is inserted. Use it
	// to enforce target_ref shape, change_spec field presence, and
	// transition validity (e.g. "you can't go ratified→draft").
	Validate func(ctx context.Context, s *Server, targetRef, changeSpec json.RawMessage) error

	// DryRun returns a JSON preview without mutating state. Optional —
	// kinds without a meaningful preview leave this nil and the
	// propose handler returns an empty `dry_run` envelope.
	DryRun func(ctx context.Context, s *Server, targetRef, changeSpec json.RawMessage) (preview json.RawMessage, err error)

	// Apply is called after /decide(approve) reaches quorum. The
	// returned `executed` payload is round-tripped to the requester
	// so the agent knows the change landed.
	Apply func(ctx context.Context, s *Server, targetRef, changeSpec json.RawMessage) (executed json.RawMessage, err error)
}

var (
	proposeKindsMu sync.RWMutex
	proposeKinds   = map[string]ProposeKind{}
)

// RegisterProposeKind installs a kind into the global registry.
// Idempotent on re-registration of the same kind name — the last
// registration wins, with a panic on Kind-empty. Designed to be
// called from `func init()` in each per-kind apply file.
//
// Static-grep contract: the lint script
// (`scripts/lint-governed-actions.sh`) discovers registered kinds by
// matching `RegisterProposeKind(ProposeKind{` (or equivalent literal)
// in *.go files and extracting the `Kind:` string literal. Do NOT
// register a kind via a runtime-computed name — the lint won't see it.
func RegisterProposeKind(p ProposeKind) {
	if p.Kind == "" {
		panic("RegisterProposeKind: Kind is empty")
	}
	proposeKindsMu.Lock()
	defer proposeKindsMu.Unlock()
	proposeKinds[p.Kind] = p
}

// LookupProposeKind returns the registered ProposeKind for `kind`, or
// (_, false) if not registered. Called by the W4 propose handler at
// dispatch time.
func LookupProposeKind(kind string) (ProposeKind, bool) {
	proposeKindsMu.RLock()
	defer proposeKindsMu.RUnlock()
	p, ok := proposeKinds[kind]
	return p, ok
}

// ListProposeKinds returns the registered kind names in sorted order.
// Used by introspection endpoints (post-MVP `tools.get propose`
// could enumerate supported kinds) and by tests that need a stable
// view of what's wired up.
func ListProposeKinds() []string {
	proposeKindsMu.RLock()
	defer proposeKindsMu.RUnlock()
	out := make([]string, 0, len(proposeKinds))
	for k := range proposeKinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// resetProposeKindsForTest clears the registry. Tests that mutate the
// global registry should call this in a t.Cleanup hook to avoid
// poisoning sibling tests. Exported only within the package.
func resetProposeKindsForTest() {
	proposeKindsMu.Lock()
	defer proposeKindsMu.Unlock()
	proposeKinds = map[string]ProposeKind{}
}
