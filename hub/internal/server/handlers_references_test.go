package server

import (
	"context"
	"encoding/json"
	"testing"
)

// Exercises the reference-library store methods (shared by the REST handlers and
// the reference_* MCP tools) end to end: create → get → list/filter → patch →
// delete, plus the MCP create/list wrappers.

func TestReferenceCRUD(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	team := defaultTeamID

	yr := 2023
	created, err := s.createReference(ctx, team, referenceBody{
		Type:        "preprint",
		Title:       "Attention Is All You Need",
		Authors:     []string{"Ashish Vaswani", "Noam Shazeer"},
		Year:        &yr,
		ArxivID:     "1706.03762",
		Source:      "zotero",
		ExternalID:  "zotero:ABC123",
		Tags:        []string{"transformers"},
		Collections: []string{"信息技术"},
		Details:     map[string]string{"publisher": "NeurIPS"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" || created.Type != "preprint" || len(created.Authors) != 2 {
		t.Fatalf("unexpected created row: %+v", created)
	}
	if created.Details["publisher"] != "NeurIPS" {
		t.Fatalf("details not round-tripped: %+v", created.Details)
	}

	// Get by id.
	got, err := s.getReferenceByID(ctx, team, created.ID)
	if err != nil || got.Title != "Attention Is All You Need" {
		t.Fatalf("get: %v %+v", err, got)
	}

	// Filter by tag + collection + q.
	if refs, _ := s.listReferences(ctx, team, referenceFilter{Tag: "transformers"}); len(refs) != 1 {
		t.Fatalf("tag filter: want 1, got %d", len(refs))
	}
	if refs, _ := s.listReferences(ctx, team, referenceFilter{Collection: "信息技术"}); len(refs) != 1 {
		t.Fatalf("collection filter: want 1, got %d", len(refs))
	}
	if refs, _ := s.listReferences(ctx, team, referenceFilter{Q: "vaswani"}); len(refs) != 1 {
		t.Fatalf("q filter (author): want 1, got %d", len(refs))
	}
	if refs, _ := s.listReferences(ctx, team, referenceFilter{Tag: "nope"}); len(refs) != 0 {
		t.Fatalf("tag filter miss: want 0, got %d", len(refs))
	}

	// Patch: change notes, keep everything else (partial-update semantics).
	patched, err := s.patchReference(ctx, team, created.ID, json.RawMessage(`{"notes":"seminal"}`))
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if patched.Notes != "seminal" || patched.Title != "Attention Is All You Need" || *patched.Year != 2023 {
		t.Fatalf("patch didn't preserve untouched fields: %+v", patched)
	}

	// Delete.
	ok, err := s.deleteReference(ctx, team, created.ID)
	if err != nil || !ok {
		t.Fatalf("delete: %v ok=%v", err, ok)
	}
	if refs, _ := s.listReferences(ctx, team, referenceFilter{}); len(refs) != 0 {
		t.Fatalf("after delete: want 0, got %d", len(refs))
	}
}

// The scraper enrichment blob (migration 0063) is stored opaquely and must
// round-trip verbatim: preserved by a patch that doesn't mention it, replaced by
// one that does.
func TestReferenceEnrichmentRoundTrip(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	team := defaultTeamID

	enr := json.RawMessage(`{"citedByCount":42,"journal":{"twoYearMeanCitedness":8.1},` +
		`"resourceLinks":[{"url":"https://github.com/x/y","kind":"code","host":"github.com"}]}`)
	created, err := s.createReference(ctx, team, referenceBody{
		Title:      "Enriched Paper",
		Source:     "scrape",
		Enrichment: enr,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !json_contains(created.Enrichment, "twoYearMeanCitedness") {
		t.Fatalf("enrichment not round-tripped on create: %s", created.Enrichment)
	}

	// A patch that omits enrichment preserves it.
	patched, err := s.patchReference(ctx, team, created.ID, json.RawMessage(`{"notes":"n"}`))
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if !json_contains(patched.Enrichment, "resourceLinks") {
		t.Fatalf("patch dropped enrichment: %s", patched.Enrichment)
	}

	// A patch that sets enrichment replaces it.
	patched2, err := s.patchReference(ctx, team, created.ID, json.RawMessage(`{"enrichment":{"topics":["ml"]}}`))
	if err != nil {
		t.Fatalf("patch2: %v", err)
	}
	if !json_contains(patched2.Enrichment, "topics") || json_contains(patched2.Enrichment, "resourceLinks") {
		t.Fatalf("enrichment not replaced: %s", patched2.Enrichment)
	}
}

func TestReferenceMCPCreateList(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	team := defaultTeamID

	res, jerr := s.mcpReferenceCreate(ctx, team, json.RawMessage(`{"title":"FlexGen","source":"manual","tags":["inference"]}`))
	if jerr != nil {
		t.Fatalf("mcp create: %v", jerr)
	}
	if res == nil {
		t.Fatal("mcp create returned nil")
	}

	// Missing title AND external_id must be rejected.
	if _, jerr := s.mcpReferenceCreate(ctx, team, json.RawMessage(`{"source":"manual"}`)); jerr == nil {
		t.Fatal("mcp create should reject empty title+external_id")
	}

	listed, jerr := s.mcpReferenceList(ctx, team, json.RawMessage(`{"tag":"inference"}`))
	if jerr != nil {
		t.Fatalf("mcp list: %v", jerr)
	}
	// mcpResultJSON wraps as {"content":[...]}; assert the payload marshals and
	// carries the row by round-tripping through JSON.
	b, _ := json.Marshal(listed)
	if !json_contains(b, "FlexGen") {
		t.Fatalf("mcp list missing created row: %s", b)
	}
}

func json_contains(b []byte, sub string) bool {
	return len(b) > 0 && (indexOf(string(b), sub) >= 0)
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
