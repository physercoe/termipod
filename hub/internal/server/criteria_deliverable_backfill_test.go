package server

import (
	"context"
	"database/sql"
	"testing"

	hub "github.com/termipod/hub"
)

// Migration 0058 heals projects created before the #56 hydration fix:
// acceptance_criteria rows left with a NULL deliverable_id (so the
// deliverable viewer, which filters by deliverable_id, showed none) are
// bound to their deliverable. Two paths: gate criteria via the ULID
// already in body.params.deliverable_id; everything else via the phase's
// sole deliverable. Phases with no deliverable stay NULL.
func TestMigration0058_BackfillsCriteriaDeliverableID(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	proj := seedProject(t, s, defaultTeamID)

	// One deliverable in phase "design"; phase "idea" has none.
	delID := seedDeliverable(t, s, proj, "design", "research_plan", deliverableStateDraft)

	// Orphaned criteria (seedCriterion inserts no deliverable_id → NULL).
	textID := seedCriterion(t, s, proj, "design", "text",
		map[string]any{"text": "budget under cap"})
	gateID := seedCriterion(t, s, proj, "design", "gate",
		map[string]any{"gate": "deliverable.ratified",
			"params": map[string]any{"deliverable_id": delID}})
	ideaID := seedCriterion(t, s, proj, "idea", "text",
		map[string]any{"text": "novelty stated"})

	// Apply the real migration SQL.
	body, err := hub.MigrationsFS.ReadFile(
		"migrations/0058_backfill_criteria_deliverable_id.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := s.writeDB.ExecContext(ctx, string(body)); err != nil {
		t.Fatalf("apply 0058: %v", err)
	}

	delivOf := func(id string) string {
		var d sql.NullString
		if err := s.db.QueryRow(
			`SELECT deliverable_id FROM acceptance_criteria WHERE id = ?`, id).Scan(&d); err != nil {
			t.Fatalf("read deliverable_id for %s: %v", id, err)
		}
		if d.Valid {
			return d.String
		}
		return ""
	}

	// gate bound via its body ULID; text bound via the phase's sole deliverable.
	if got := delivOf(gateID); got != delID {
		t.Errorf("gate criterion deliverable_id=%q want %q", got, delID)
	}
	if got := delivOf(textID); got != delID {
		t.Errorf("text criterion deliverable_id=%q want %q", got, delID)
	}
	// idea phase has no deliverable → stays unbound.
	if got := delivOf(ideaID); got != "" {
		t.Errorf("idea criterion deliverable_id=%q want empty (no deliverable to bind)", got)
	}
}
