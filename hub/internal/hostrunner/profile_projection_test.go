package hostrunner

import (
	"reflect"
	"testing"

	"github.com/termipod/hub/internal/agentfamilies"
)

// Projection tests for the two collection re-shapers a profile can
// declare: `payload_maps` (walk a map's values, keep its keys) and
// `payload_lists` (walk an array's elements, keep their order).
//
// Both exist for the same reason and fail the same way when absent:
// a collection of engine-shaped objects passed through verbatim
// carries the engine's field names into a typed payload, which no
// consumer can detect — it reads the field it was promised, finds
// nothing, and renders a zero or a blank row. So the assertions here
// are about the *output* field names, not just about arity.

func listProfile(fields map[string]string) *agentfamilies.FrameProfile {
	return &agentfamilies.FrameProfile{
		ProfileVersion: 1,
		Rules: []agentfamilies.Rule{{
			Match: map[string]any{"method": "turn/plan/updated"},
			Emit: agentfamilies.Emit{
				Kind:     "plan",
				Producer: "agent",
				Payload:  map[string]string{"turn_id": "$.params.turnId"},
				PayloadLists: map[string]agentfamilies.ListProjection{
					"entries": {Source: "$.params.plan", Fields: fields},
				},
			},
		}},
	}
}

func planFrame(steps []any) map[string]any {
	return map[string]any{
		"method": "turn/plan/updated",
		"params": map[string]any{"turnId": "turn_7", "plan": steps},
	}
}

// TestProjectList_RenamesElementFieldsInOrder — the motivating case.
// codex names a plan step `step`; the vocabulary (set by ACP) names it
// `content`. Order is the plan's meaning, so it is preserved.
func TestProjectList_RenamesElementFieldsInOrder(t *testing.T) {
	profile := listProfile(map[string]string{"content": "$.step", "status": "$.status"})
	got := ApplyProfile(planFrame([]any{
		map[string]any{"step": "read auth.go", "status": "completed"},
		map[string]any{"step": "refactor middleware", "status": "inProgress"},
	}), profile)

	if len(got) != 1 || got[0].Kind != "plan" {
		t.Fatalf("got %+v; want one plan event", got)
	}
	want := []any{
		map[string]any{"content": "read auth.go", "status": "completed"},
		map[string]any{"content": "refactor middleware", "status": "inProgress"},
	}
	if !reflect.DeepEqual(got[0].Payload["entries"], want) {
		t.Errorf("entries = %#v; want %#v", got[0].Payload["entries"], want)
	}
	if got[0].Payload["turn_id"] != "turn_7" {
		t.Errorf("plain payload fields must still resolve alongside the projection; got %#v", got[0].Payload)
	}
}

// TestProjectList_OuterScope — inside `fields`, `$.` is the element
// and `$$.` is the rule's own scope. Without this a projected element
// can never reference the frame that carried it.
func TestProjectList_OuterScope(t *testing.T) {
	profile := listProfile(map[string]string{"content": "$.step", "turn": "$$.params.turnId"})
	got := ApplyProfile(planFrame([]any{map[string]any{"step": "one"}}), profile)
	entries, _ := got[0].Payload["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %#v; want 1", got[0].Payload["entries"])
	}
	if entries[0].(map[string]any)["turn"] != "turn_7" {
		t.Errorf("$$. should reach the frame; got %#v", entries[0])
	}
}

// TestProjectList_AbsentSourceOmitsTheField — absent and empty are
// different claims. "codex sent no plan" must not read as "codex sent
// a plan with no steps", which is what an empty list would say.
func TestProjectList_AbsentSourceOmitsTheField(t *testing.T) {
	profile := listProfile(map[string]string{"content": "$.step"})
	frame := map[string]any{
		"method": "turn/plan/updated",
		"params": map[string]any{"turnId": "turn_7"},
	}
	got := ApplyProfile(frame, profile)
	if _, present := got[0].Payload["entries"]; present {
		t.Errorf("absent source should omit the field entirely; got %#v", got[0].Payload)
	}
}

// TestProjectList_EmptySourceProjectsToEmpty — the other side of that
// line: an empty array IS a claim the engine made, so it survives as
// an empty list rather than vanishing.
func TestProjectList_EmptySourceProjectsToEmpty(t *testing.T) {
	profile := listProfile(map[string]string{"content": "$.step"})
	got := ApplyProfile(planFrame([]any{}), profile)
	entries, present := got[0].Payload["entries"]
	if !present {
		t.Fatalf("empty source should still project; got %#v", got[0].Payload)
	}
	if !reflect.DeepEqual(entries, []any{}) {
		t.Errorf("entries = %#v; want an empty list", entries)
	}
}

// TestProjectList_WrongTypedSourceOmits — a source that resolves to a
// map (or a scalar) isn't an array; the field is omitted rather than
// half-projected.
func TestProjectList_WrongTypedSourceOmits(t *testing.T) {
	profile := listProfile(map[string]string{"content": "$.step"})
	frame := map[string]any{
		"method": "turn/plan/updated",
		"params": map[string]any{"turnId": "turn_7", "plan": map[string]any{"step": "one"}},
	}
	got := ApplyProfile(frame, profile)
	if _, present := got[0].Payload["entries"]; present {
		t.Errorf("non-array source should omit the field; got %#v", got[0].Payload)
	}
}

// TestProjectList_SkipsNonObjectElements — one malformed element
// can't void the whole list. It shortens it instead: a projection
// declares the shape it produces, and there is no honest placeholder
// for an element that can't take that shape.
func TestProjectList_SkipsNonObjectElements(t *testing.T) {
	profile := listProfile(map[string]string{"content": "$.step"})
	got := ApplyProfile(planFrame([]any{
		map[string]any{"step": "one"},
		"a bare string",
		map[string]any{"step": "two"},
	}), profile)
	want := []any{
		map[string]any{"content": "one"},
		map[string]any{"content": "two"},
	}
	if !reflect.DeepEqual(got[0].Payload["entries"], want) {
		t.Errorf("entries = %#v; want %#v", got[0].Payload["entries"], want)
	}
}

// TestProjectList_MissingElementFieldIsNil — an element that lacks a
// projected field gets nil, not a dropped key: the consumer sees a
// step with no status rather than a differently-shaped object.
func TestProjectList_MissingElementFieldIsNil(t *testing.T) {
	profile := listProfile(map[string]string{"content": "$.step", "status": "$.status"})
	got := ApplyProfile(planFrame([]any{map[string]any{"step": "one"}}), profile)
	entries, _ := got[0].Payload["entries"].([]any)
	elem, _ := entries[0].(map[string]any)
	if v, present := elem["status"]; !present || v != nil {
		t.Errorf("missing element field should be present-and-nil; got %#v", elem)
	}
}

// TestProjectMap_KeepsKeysAndRenamesValues — the map twin. E1(c)
// covered it through the *shipped* claude profile
// (TestProfile_ByModelIsProjectedToTheTypedShape), which pins that
// profile's field list; this pins the construct's own contract, so the
// two projections' shared rules have one place to read and a future
// profile edit can't take the grammar's coverage with it.
func TestProjectMap_KeepsKeysAndRenamesValues(t *testing.T) {
	profile := &agentfamilies.FrameProfile{
		ProfileVersion: 1,
		Rules: []agentfamilies.Rule{{
			Match: map[string]any{"type": "result"},
			Emit: agentfamilies.Emit{
				Kind: "turn.result",
				PayloadMaps: map[string]agentfamilies.MapProjection{
					"by_model": {
						Source: "$.modelUsage",
						Fields: map[string]string{"input": "$.inputTokens"},
					},
				},
			},
		}},
	}
	got := ApplyProfile(map[string]any{
		"type": "result",
		"modelUsage": map[string]any{
			"claude-opus-5": map[string]any{"inputTokens": float64(12)},
			"not-an-object": "skipped",
		},
	}, profile)
	want := map[string]any{
		"claude-opus-5": map[string]any{"input": float64(12)},
	}
	if !reflect.DeepEqual(got[0].Payload["by_model"], want) {
		t.Errorf("by_model = %#v; want %#v", got[0].Payload["by_model"], want)
	}
}

// TestProjections_PlainPayloadWinsANameCollision — documented merge
// order (Emit.PayloadMaps / PayloadLists doc comments). A profile that
// declares both for one field has a bug; resolving it toward the
// simpler declaration keeps the resolution predictable.
func TestProjections_PlainPayloadWinsANameCollision(t *testing.T) {
	profile := &agentfamilies.FrameProfile{
		ProfileVersion: 1,
		Rules: []agentfamilies.Rule{{
			Match: map[string]any{"method": "turn/plan/updated"},
			Emit: agentfamilies.Emit{
				Kind:    "plan",
				Payload: map[string]string{"entries": "$.params.turnId"},
				PayloadLists: map[string]agentfamilies.ListProjection{
					"entries": {Source: "$.params.plan", Fields: map[string]string{"content": "$.step"}},
				},
			},
		}},
	}
	got := ApplyProfile(planFrame([]any{map[string]any{"step": "one"}}), profile)
	if got[0].Payload["entries"] != "turn_7" {
		t.Errorf("plain payload should win; got %#v", got[0].Payload["entries"])
	}
}

// TestProjections_IgnoredUnderPayloadExpr — payload_expr is
// whole-payload passthrough and is declared mutually exclusive with
// the field-wise forms (schema + Emit doc). Pinned so a profile that
// sets both gets the documented answer rather than a merge.
func TestProjections_IgnoredUnderPayloadExpr(t *testing.T) {
	profile := &agentfamilies.FrameProfile{
		ProfileVersion: 1,
		Rules: []agentfamilies.Rule{{
			Match: map[string]any{"method": "turn/plan/updated"},
			Emit: agentfamilies.Emit{
				Kind:        "plan",
				PayloadExpr: "$.params",
				PayloadLists: map[string]agentfamilies.ListProjection{
					"entries": {Source: "$.params.plan", Fields: map[string]string{"content": "$.step"}},
				},
			},
		}},
	}
	got := ApplyProfile(planFrame([]any{map[string]any{"step": "one"}}), profile)
	if _, present := got[0].Payload["entries"].([]any); present {
		if _, isProjected := got[0].Payload["entries"].([]any)[0].(map[string]any)["content"]; isProjected {
			t.Errorf("payload_expr should win outright; got %#v", got[0].Payload)
		}
	}
	if got[0].Payload["turnId"] != "turn_7" {
		t.Errorf("payload_expr should pass params through; got %#v", got[0].Payload)
	}
}
