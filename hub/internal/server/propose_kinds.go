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
//     and fanned back to the requester's session. Receives a
//     ProposeApplyContext so per-kind audit rows can carry the propose
//     lineage (attention_id, by_tier, decider_handle).
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
	Apply func(ctx context.Context, s *Server, ac ProposeApplyContext, targetRef, changeSpec json.RawMessage) (executed json.RawMessage, err error)
}

// ProposeApplyContext carries the attention-row lineage into the apply
// function so per-kind audit rows can record `via="propose"`,
// `by_tier=<assigned_tier>`, and `propose_id=<attention_id>` without
// re-querying. Populated by W8 at /decide(approve) dispatch time;
// in-test callers populate it directly.
type ProposeApplyContext struct {
	// AttentionID is the propose row that authorised this apply.
	AttentionID string
	// Team scopes recordAudit's actor lookup.
	Team string
	// AssignedTier is the tier the row was addressed to (the tier
	// whose quorum approved the change). Lands in audit meta as
	// `by_tier`.
	AssignedTier string
	// DeciderHandle is the @-prefix-stripped handle of the actor who
	// approved. May be "" when /decide came in unauthenticated (test
	// path); apply functions tolerate that and skip the by-actor
	// stamp on the audit row.
	DeciderHandle string
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

// resetProposeKindsForTest clears the registry. Used by the W3
// registry tests that need to assert a clean empty starting state.
// Most W4+ tests use snapshotProposeKindsForTest/restoreProposeKindsForTest
// instead so init()-time registrations survive sibling tests.
func resetProposeKindsForTest() {
	proposeKindsMu.Lock()
	defer proposeKindsMu.Unlock()
	proposeKinds = map[string]ProposeKind{}
}

// snapshotProposeKindsForTest returns a shallow copy of the current
// registry so a test can mutate the registry and restore it on
// cleanup. Use this whenever a per-kind init() registration must
// survive across tests — calling resetProposeKindsForTest alone would
// drop them.
//
// Pairing pattern:
//
//	saved := snapshotProposeKindsForTest()
//	t.Cleanup(func() { restoreProposeKindsForTest(saved) })
//	RegisterProposeKind(ProposeKind{Kind: "test.kind"})
func snapshotProposeKindsForTest() map[string]ProposeKind {
	proposeKindsMu.RLock()
	defer proposeKindsMu.RUnlock()
	out := make(map[string]ProposeKind, len(proposeKinds))
	for k, v := range proposeKinds {
		out[k] = v
	}
	return out
}

// restoreProposeKindsForTest replaces the registry with the provided
// snapshot. Pairs with snapshotProposeKindsForTest in t.Cleanup
// closures.
func restoreProposeKindsForTest(saved map[string]ProposeKind) {
	proposeKindsMu.Lock()
	defer proposeKindsMu.Unlock()
	proposeKinds = make(map[string]ProposeKind, len(saved))
	for k, v := range saved {
		proposeKinds[k] = v
	}
}
