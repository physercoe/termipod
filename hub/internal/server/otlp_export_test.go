package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/termipod/hub/internal/otlptrace"
)

// findAttrKey reports whether a span carries an attribute with the key.
func hasAttrKey(sp otlptrace.Span, key string) bool {
	for _, a := range sp.Attrs {
		if a.Key == key {
			return true
		}
	}
	return false
}

// TestBuildSessionSpans_ProjectsTurnsAndTools drives the ADR-038 §4 projection
// off the shared canonical vector: two turns (t1 success, t2 error), two tools
// (bash ok under t1, edit error under t2), and a free-standing error under t2.
// Asserts the span tree shape — deterministic ids, parent linkage, timing,
// status, and the exception span events.
func TestBuildSessionSpans_ProjectsTurnsAndTools(t *testing.T) {
	c := newE2E(t)
	agentID, sessionID := seedVectorRun(t, c)
	ctx := context.Background()
	// Backfill the turn index the projection reads from.
	if _, err := ensureAgentDigest(ctx, c.s.db, agentID, defaultTeamID); err != nil {
		t.Fatalf("ensureAgentDigest: %v", err)
	}

	spans, err := c.s.buildSessionSpans(ctx, sessionID)
	if err != nil {
		t.Fatalf("buildSessionSpans: %v", err)
	}
	if len(spans) != 4 {
		t.Fatalf("want 4 spans (2 turns + 2 tools), got %d", len(spans))
	}

	trace := traceIDForSession(sessionID)
	t1, t2 := spanIDFor(sessionID, "t1"), spanIDFor(sessionID, "t2")
	c1, c2 := spanIDFor(sessionID, "c1"), spanIDFor(sessionID, "c2")
	zero := [8]byte{}

	byID := map[[8]byte]otlptrace.Span{}
	for _, sp := range spans {
		if sp.TraceID != trace {
			t.Errorf("span %q is not on the session trace", sp.Name)
		}
		byID[sp.SpanID] = sp
	}

	// --- turn t1: root span, success → OK, name "turn 0" ---
	s1, ok := byID[t1]
	if !ok {
		t.Fatal("missing turn t1 span")
	}
	if s1.Name != "turn 0" {
		t.Errorf("t1 name = %q, want \"turn 0\"", s1.Name)
	}
	if s1.ParentID != zero {
		t.Errorf("turn span must be a trace root (no parent), got % x", s1.ParentID)
	}
	if s1.Status.Code != otlptrace.StatusOK {
		t.Errorf("t1 status = %d, want OK", s1.Status.Code)
	}
	if s1.StartNano == 0 || s1.StartNano >= s1.EndNano {
		t.Errorf("t1 timing not start<end: start=%d end=%d", s1.StartNano, s1.EndNano)
	}
	for _, k := range []string{"gen_ai.system", "termipod.turn.idx", "termipod.agent_id"} {
		if !hasAttrKey(s1, k) {
			t.Errorf("t1 missing attr %q", k)
		}
	}

	// --- turn t2: error → ERROR, carries the free-standing error as an event ---
	s2 := byID[t2]
	if s2.Status.Code != otlptrace.StatusError {
		t.Errorf("t2 status = %d, want ERROR", s2.Status.Code)
	}
	if len(s2.Events) != 1 {
		t.Errorf("t2 should carry 1 exception event (the seq-10 error), got %d", len(s2.Events))
	}

	// --- tool bash (c1): child of t1, OK ---
	bash := byID[c1]
	if bash.Name != "bash" {
		t.Errorf("c1 name = %q, want bash", bash.Name)
	}
	if bash.ParentID != t1 {
		t.Errorf("bash must be parented to turn t1")
	}
	if bash.Status.Code != otlptrace.StatusOK {
		t.Errorf("bash status = %d, want OK", bash.Status.Code)
	}

	// --- tool edit (c2): child of t2, ERROR + exception event ---
	edit := byID[c2]
	if edit.ParentID != t2 {
		t.Errorf("edit must be parented to turn t2")
	}
	if edit.Status.Code != otlptrace.StatusError {
		t.Errorf("edit status = %d, want ERROR", edit.Status.Code)
	}
	if len(edit.Events) != 1 {
		t.Errorf("failed tool should carry 1 exception event, got %d", len(edit.Events))
	}

	// --- gen_ai.system value (encode round-trip; the projection wires kind) ---
	body := otlptrace.Encode(otlptrace.Resource{ServiceName: "termipod-hub"}, []otlptrace.Span{s1})
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("encode round-trip: %v", err)
	}
	rs := doc["resourceSpans"].([]any)[0].(map[string]any)
	sp := rs["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)[0].(map[string]any)
	var sys string
	for _, a := range sp["attributes"].([]any) {
		m := a.(map[string]any)
		if m["key"] == "gen_ai.system" {
			sys = m["value"].(map[string]any)["stringValue"].(string)
		}
	}
	if sys != "claude-code" {
		t.Errorf("gen_ai.system = %q, want claude-code (the agent kind)", sys)
	}

	// --- determinism: same rows → identical projection (idempotent re-export) ---
	again, err := c.s.buildSessionSpans(ctx, sessionID)
	if err != nil {
		t.Fatalf("re-projection: %v", err)
	}
	if !reflect.DeepEqual(spans, again) {
		t.Error("projection is not deterministic across calls")
	}
}

// TestSessionsWithClosedTurnsSince surfaces the export watermark: a session
// with closed turns appears with its max end_ts.
func TestSessionsWithClosedTurnsSince(t *testing.T) {
	c := newE2E(t)
	agentID, sessionID := seedVectorRun(t, c)
	ctx := context.Background()
	if _, err := ensureAgentDigest(ctx, c.s.db, agentID, defaultTeamID); err != nil {
		t.Fatalf("ensureAgentDigest: %v", err)
	}
	due, err := c.s.sessionsWithClosedTurnsSince(ctx)
	if err != nil {
		t.Fatalf("sessionsWithClosedTurnsSince: %v", err)
	}
	maxEnd, ok := due[sessionID]
	if !ok {
		t.Fatalf("session %s not reported as due; got %v", sessionID, due)
	}
	// seedAgentEvent stamps NowUTC (not the vector's literal ts), so assert the
	// watermark is a real, parseable end_ts rather than a fixed value.
	if _, parsed := tsNano(maxEnd); !parsed {
		t.Errorf("watermark = %q, want a parseable RFC3339Nano end_ts", maxEnd)
	}
}

// TestExportDueSessions_ShipsThenWatermarks drives the loop body against an
// in-process OTLP receiver: the first sweep ships the session's spans; the
// second is a no-op because the watermark already covers it (no new turns).
func TestExportDueSessions_ShipsThenWatermarks(t *testing.T) {
	c := newE2E(t)
	agentID, sessionID := seedVectorRun(t, c)
	ctx := context.Background()
	if _, err := ensureAgentDigest(ctx, c.s.db, agentID, defaultTeamID); err != nil {
		t.Fatalf("ensureAgentDigest: %v", err)
	}

	var mu sync.Mutex
	var posts int
	var lastBody []byte
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" {
			t.Errorf("OTLP POST to %q, want /v1/traces", r.URL.Path)
		}
		mu.Lock()
		posts++
		lastBody, _ = io.ReadAll(r.Body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer recv.Close()

	c.s.otlp = &otlptrace.Client{
		Endpoint: recv.URL,
		Resource: otlptrace.Resource{ServiceName: "termipod-hub"},
	}
	c.s.otlpWatermark = map[string]string{}

	// First sweep: ships the session.
	c.s.exportDueSessions(ctx)
	mu.Lock()
	gotPosts, body := posts, lastBody
	mu.Unlock()
	if gotPosts != 1 {
		t.Fatalf("first sweep: %d POSTs, want 1", gotPosts)
	}
	if !json.Valid(body) {
		t.Fatalf("receiver got invalid JSON: %s", body)
	}
	wm := c.s.otlpWatermark[sessionID]
	if _, ok := tsNano(wm); !ok {
		t.Fatalf("watermark not advanced for %s: %q", sessionID, wm)
	}

	// Second sweep: no new turns → no POST.
	c.s.exportDueSessions(ctx)
	mu.Lock()
	gotPosts = posts
	mu.Unlock()
	if gotPosts != 1 {
		t.Fatalf("second sweep should be a no-op, but total POSTs = %d", gotPosts)
	}
}
