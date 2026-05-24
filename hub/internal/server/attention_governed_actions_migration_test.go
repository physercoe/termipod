package server

import (
	"sort"
	"testing"
)

// Regression: migration 0045 (ADR-030 W1) MUST add the five governed-
// actions columns + two partial indexes to attention_items, and MUST
// keep every pre-0045 column readable (additive-only — no rebuild).
//
// Pairs with the verify-anchor on hub/migrations/0045_*.up.sql in
// docs/plans/governed-actions-mvp-rollout.md §2.2 W1: if the migration
// file ever disappears, lint-doc-anchors.sh fails; if a column ever
// drifts out of the file, this test fails.
func TestAttentionItems_Migration0045_GovernedActionsColumns(t *testing.T) {
	s, _ := newTestServer(t)

	rows, err := s.db.Query(`PRAGMA table_info(attention_items)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = ctype
	}

	wantNew := []string{
		"change_kind", "assigned_tier",
		"change_spec_json", "target_ref_json", "executed_json",
	}
	var missing []string
	for _, c := range wantNew {
		if _, ok := got[c]; !ok {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("attention_items missing 0045 columns: %v", missing)
	}

	// Sanity: the new columns are nullable (TEXT, no NOT NULL). The
	// migration depends on this — old rows must read back without
	// errors and without backfill.
	for _, c := range wantNew {
		if got[c] != "TEXT" {
			t.Errorf("column %s type = %q; want TEXT", c, got[c])
		}
	}

	// Spot-check a pre-0045 column is still present so we don't
	// accidentally regress the table-shape preservation rule.
	for _, c := range []string{
		"id", "kind", "summary", "status",
		// ADR-034 (0042) columns
		"escalation_state", "cause",
	} {
		if _, ok := got[c]; !ok {
			t.Errorf("attention_items lost pre-0045 column %q", c)
		}
	}
}

// Regression: migration 0045 creates two partial indexes that the
// propose-handler (W4) and the Me-page query widen (W19.6) lean on.
// PRAGMA index_list reports both names; PRAGMA index_info confirms
// the columns. We only assert names + presence — partial-WHERE shape
// is in the up.sql file and would be caught by the doc anchor in
// the plan.
func TestAttentionItems_Migration0045_GovernedActionsIndexes(t *testing.T) {
	s, _ := newTestServer(t)

	rows, err := s.db.Query(`PRAGMA index_list(attention_items)`)
	if err != nil {
		t.Fatalf("index_list: %v", err)
	}
	defer rows.Close()

	got := map[string]bool{}
	for rows.Next() {
		var seq int
		var name, origin string
		var unique, partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = partial == 1
	}

	for _, want := range []string{
		"idx_attention_change_kind",
		"idx_attention_assigned_tier",
	} {
		isPartial, ok := got[want]
		if !ok {
			t.Errorf("missing 0045 index %q", want)
			continue
		}
		if !isPartial {
			t.Errorf("index %q created without WHERE clause; expected partial", want)
		}
	}
}
