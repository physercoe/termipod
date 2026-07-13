package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
)

// Exercises the reference-annotation store methods (shared by REST + the
// reference_annotation_* MCP tools): create → get → list-ordered → patch →
// delete, team/reference scoping, the opaque position round-trip, and the MCP
// wrappers.

func TestReferenceAnnotationCRUD(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	team := defaultTeamID

	ref, err := s.createReference(ctx, team, referenceBody{Title: "Annotated Paper", Source: "manual"})
	if err != nil {
		t.Fatalf("create reference: %v", err)
	}

	// Create a highlight on page 5 (rect geometry, Zotero-shaped).
	pos := json.RawMessage(`{"pageIndex":5,"rects":[[68.6,246.5,174.3,267.0]]}`)
	created, err := s.createAnnotation(ctx, team, ref.ID, annotationBody{
		Type:      "highlight",
		Color:     "#ffd400",
		PageIndex: 5,
		SortIndex: "00005|0000246",
		Text:      "the passage",
		Comment:   "key claim",
		Author:    "@director",
		Position:  pos,
		Tags:      []string{"cited"},
	})
	if err != nil {
		t.Fatalf("create annotation: %v", err)
	}
	if created.ID == "" || created.ReferenceID != ref.ID || created.Type != "highlight" {
		t.Fatalf("unexpected created annotation: %+v", created)
	}
	if !json_contains(created.Position, "pageIndex") || created.Color != "#ffd400" {
		t.Fatalf("position/color not round-tripped: %+v", created)
	}

	// Get by id.
	got, err := s.getAnnotationByID(ctx, team, ref.ID, created.ID)
	if err != nil || got.Text != "the passage" || len(got.Tags) != 1 {
		t.Fatalf("get: %v %+v", err, got)
	}

	// A second annotation on an earlier page must sort first.
	early, err := s.createAnnotation(ctx, team, ref.ID, annotationBody{
		Type: "underline", PageIndex: 2, SortIndex: "00002|0000010",
		Position: json.RawMessage(`{"pageIndex":2,"rects":[[10,10,20,20]]}`),
	})
	if err != nil {
		t.Fatalf("create early: %v", err)
	}
	list, err := s.listAnnotations(ctx, team, ref.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}
	if list[0].ID != early.ID {
		t.Fatalf("annotations not ordered by page: got %+v first", list[0])
	}

	// Patch: change comment + move the position, keep everything else.
	newPos := json.RawMessage(`{"pageIndex":5,"rects":[[100,200,300,220]]}`)
	patched, err := s.patchAnnotation(ctx, team, ref.ID, created.ID,
		json.RawMessage(`{"comment":"revised","position":`+string(newPos)+`}`))
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if patched.Comment != "revised" || patched.Text != "the passage" || patched.Color != "#ffd400" {
		t.Fatalf("patch didn't preserve untouched fields: %+v", patched)
	}
	if !json_contains(patched.Position, "300") {
		t.Fatalf("patch didn't update position: %s", patched.Position)
	}

	// Delete one; the other remains.
	ok, err := s.deleteAnnotation(ctx, team, ref.ID, created.ID)
	if err != nil || !ok {
		t.Fatalf("delete: %v ok=%v", err, ok)
	}
	if list, _ := s.listAnnotations(ctx, team, ref.ID); len(list) != 1 {
		t.Fatalf("after delete: want 1, got %d", len(list))
	}
}

// Creating an annotation on a reference the team does not own is a not-found,
// never a silent cross-team write.
func TestReferenceAnnotationScoping(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	team := defaultTeamID

	if _, err := s.createAnnotation(ctx, team, "no-such-ref", annotationBody{Type: "note"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("create on missing reference: want ErrNoRows, got %v", err)
	}
}

// Deleting the parent reference cascades to its annotations (FK ON DELETE
// CASCADE, foreign_keys enforced on the runtime connection).
func TestReferenceAnnotationCascade(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	team := defaultTeamID

	ref, _ := s.createReference(ctx, team, referenceBody{Title: "Doomed", Source: "manual"})
	if _, err := s.createAnnotation(ctx, team, ref.ID, annotationBody{
		Type: "highlight", PageIndex: 0,
		Position: json.RawMessage(`{"pageIndex":0,"rects":[[1,1,2,2]]}`),
	}); err != nil {
		t.Fatalf("create annotation: %v", err)
	}
	if _, err := s.deleteReference(ctx, team, ref.ID); err != nil {
		t.Fatalf("delete reference: %v", err)
	}
	if list, _ := s.listAnnotations(ctx, team, ref.ID); len(list) != 0 {
		t.Fatalf("cascade: annotations not removed, got %d", len(list))
	}
}

func TestReferenceAnnotationMCP(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	team := defaultTeamID

	ref, _ := s.createReference(ctx, team, referenceBody{Title: "MCP Paper", Source: "manual"})

	res, jerr := s.mcpAnnotationCreate(ctx, team, json.RawMessage(
		`{"reference_id":"`+ref.ID+`","type":"highlight","page_index":3,"position":{"pageIndex":3,"rects":[[1,2,3,4]]},"comment":"note"}`))
	if jerr != nil {
		t.Fatalf("mcp create: %v", jerr)
	}
	if res == nil {
		t.Fatal("mcp create returned nil")
	}

	// reference_id is required.
	if _, jerr := s.mcpAnnotationCreate(ctx, team, json.RawMessage(`{"type":"note"}`)); jerr == nil {
		t.Fatal("mcp create should reject missing reference_id")
	}
	// A reference the team doesn't own is not-found, not a write.
	if _, jerr := s.mcpAnnotationCreate(ctx, team, json.RawMessage(`{"reference_id":"nope","type":"note"}`)); jerr == nil {
		t.Fatal("mcp create should reject unknown reference")
	}

	listed, jerr := s.mcpAnnotationList(ctx, team, json.RawMessage(`{"reference_id":"`+ref.ID+`"}`))
	if jerr != nil {
		t.Fatalf("mcp list: %v", jerr)
	}
	b, _ := json.Marshal(listed)
	if !json_contains(b, "note") {
		t.Fatalf("mcp list missing created annotation: %s", b)
	}
}
