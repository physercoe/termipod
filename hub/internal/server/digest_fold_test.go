package server

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// digestVector mirrors testdata/digest_canonical_vector.json — the shared
// Go/Dart canonical-error + digest fixture (ADR-038). The Dart side
// (test/widget/digest_canonical_vector_test.dart) reads the SAME file.
type digestVector struct {
	AgentID string `json:"agent_id"`
	TeamID  string `json:"team_id"`
	Events  []struct {
		Seq      int64          `json:"seq"`
		Kind     string         `json:"kind"`
		TS       string         `json:"ts"`
		Producer string         `json:"producer"`
		Payload  map[string]any `json:"payload"`
	} `json:"events"`
	Expected struct {
		EventCount      int64              `json:"event_count"`
		TurnCount       int64              `json:"turn_count"`
		WatermarkSeq    int64              `json:"watermark_seq"`
		DurationMs      int64              `json:"duration_ms"`
		CostUSD         float64            `json:"cost_usd"`
		ErrorCount      int64              `json:"error_count"`
		ToolTotal       int64              `json:"tool_total"`
		ToolFailed      int64              `json:"tool_failed"`
		Errors          map[string]int64   `json:"errors"`
		ErrorSampleSeqs map[string][]int64 `json:"error_sample_seqs"`
		Tools           map[string]struct {
			Calls  int64 `json:"calls"`
			Failed int64 `json:"failed"`
		} `json:"tools"`
		ByModel map[string]struct {
			In  int64 `json:"in"`
			Out int64 `json:"out"`
		} `json:"by_model"`
		Turns []struct {
			TurnID     string  `json:"turn_id"`
			Idx        int     `json:"idx"`
			StartSeq   int64   `json:"start_seq"`
			EndSeq     int64   `json:"end_seq"`
			DurationMs int64   `json:"duration_ms"`
			Status     string  `json:"status"`
			CostUSD    float64 `json:"cost_usd"`
			InTokens   int64   `json:"in_tokens"`
			OutTokens  int64   `json:"out_tokens"`
			ToolCount  int64   `json:"tool_count"`
			ToolFailed int64   `json:"tool_failed"`
			ErrorCount int64   `json:"error_count"`
		} `json:"turns"`
	} `json:"expected"`
}

func loadDigestVector(t *testing.T) (digestVector, []foldEvent) {
	t.Helper()
	raw, err := os.ReadFile("testdata/digest_canonical_vector.json")
	if err != nil {
		t.Fatalf("read vector: %v", err)
	}
	var v digestVector
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal vector: %v", err)
	}
	events := make([]foldEvent, 0, len(v.Events))
	for _, e := range v.Events {
		p := e.Payload
		if p == nil {
			p = map[string]any{}
		}
		events = append(events, foldEvent{Seq: e.Seq, Kind: e.Kind, TS: e.TS, Producer: e.Producer, Payload: p})
	}
	return v, events
}

// TestDigestCanonicalVector pins the brute-force digest computation to the
// shared fixture — the Go half of the cross-language contract (ADR-038 §1).
func TestDigestCanonicalVector(t *testing.T) {
	v, events := loadDigestVector(t)
	d, turns := computeAgentDigest(v.AgentID, v.TeamID, events)
	exp := v.Expected

	if d.EventCount != exp.EventCount {
		t.Errorf("event_count = %d, want %d", d.EventCount, exp.EventCount)
	}
	if d.TurnCount != exp.TurnCount {
		t.Errorf("turn_count = %d, want %d", d.TurnCount, exp.TurnCount)
	}
	if d.WatermarkSeq != exp.WatermarkSeq {
		t.Errorf("watermark_seq = %d, want %d", d.WatermarkSeq, exp.WatermarkSeq)
	}
	if d.DurationMs != exp.DurationMs {
		t.Errorf("duration_ms = %d, want %d", d.DurationMs, exp.DurationMs)
	}
	if d.CostUSD != exp.CostUSD {
		t.Errorf("cost_usd = %v, want %v", d.CostUSD, exp.CostUSD)
	}
	if d.ErrorCount != exp.ErrorCount {
		t.Errorf("error_count = %d, want %d", d.ErrorCount, exp.ErrorCount)
	}
	if d.ToolTotal != exp.ToolTotal {
		t.Errorf("tool_total = %d, want %d", d.ToolTotal, exp.ToolTotal)
	}
	if d.ToolFailed != exp.ToolFailed {
		t.Errorf("tool_failed = %d, want %d", d.ToolFailed, exp.ToolFailed)
	}
	for class, want := range exp.Errors {
		got := d.Errors[class]
		if got == nil || got.Count != want {
			t.Errorf("errors[%q].count = %v, want %d", class, got, want)
		}
	}
	for class, want := range exp.ErrorSampleSeqs {
		got := d.Errors[class]
		if got == nil || !equalInt64Slice(got.SampleSeqs, want) {
			t.Errorf("errors[%q].sample_seqs = %v, want %v", class, got, want)
		}
	}
	for name, want := range exp.Tools {
		got := d.Tools[name]
		if got == nil || got.Calls != want.Calls || got.Failed != want.Failed {
			t.Errorf("tools[%q] = %v, want calls=%d failed=%d", name, got, want.Calls, want.Failed)
		}
	}
	for model, want := range exp.ByModel {
		got := d.ByModel[model]
		if got == nil || got.In != want.In || got.Out != want.Out {
			t.Errorf("by_model[%q] = %v, want in=%d out=%d", model, got, want.In, want.Out)
		}
	}

	if len(turns) != len(exp.Turns) {
		t.Fatalf("turns = %d, want %d", len(turns), len(exp.Turns))
	}
	for i, want := range exp.Turns {
		g := turns[i]
		if g.TurnID != want.TurnID || g.Idx != want.Idx || g.StartSeq != want.StartSeq ||
			g.EndSeq != want.EndSeq || g.DurationMs != want.DurationMs || g.Status != want.Status ||
			g.CostUSD != want.CostUSD || g.InTokens != want.InTokens || g.OutTokens != want.OutTokens ||
			g.ToolCount != want.ToolCount || g.ToolFailed != want.ToolFailed || g.ErrorCount != want.ErrorCount {
			t.Errorf("turn[%d] = %+v, want %+v", i, g, want)
		}
	}

	// Latency histogram holds one sample per closed turn.
	var samples int64
	for _, c := range d.Latency.Counts {
		samples += c
	}
	if samples != exp.TurnCount {
		t.Errorf("latency samples = %d, want %d", samples, exp.TurnCount)
	}
}

func equalInt64Slice(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDigestErrorSampleTS verifies recordError captures each sampled error's
// timestamp aligned 1:1 with its seq — the data the mobile analysis surface
// needs to jump to an error via the (ts, seq) random-access reset rather than
// the bounded page-walk.
func TestDigestErrorSampleTS(t *testing.T) {
	events := []foldEvent{
		{Seq: 1, Kind: "input.text", TS: "2026-06-02T00:00:00Z", Producer: "user",
			Payload: map[string]any{"text": "hi"}},
		{Seq: 2, Kind: "error", TS: "2026-06-02T00:00:05Z", Producer: "agent",
			Payload: map[string]any{"message": "boom"}},
		{Seq: 3, Kind: "turn.result", TS: "2026-06-02T00:00:06Z", Producer: "agent",
			Payload: map[string]any{"status": "error"}},
	}
	d, _ := computeAgentDigest("a", "t", events)

	// Every error class keeps its sample timestamps aligned with its seqs.
	for class, agg := range d.Errors {
		if len(agg.SampleTSs) != len(agg.SampleSeqs) {
			t.Errorf("errors[%q]: sample_ts len %d != sample_seqs len %d",
				class, len(agg.SampleTSs), len(agg.SampleSeqs))
		}
	}

	// The bare error event (seq 2) → class "error" with its exact ts.
	got := d.Errors["error"]
	if got == nil {
		t.Fatalf("missing error class; classes=%v", d.Errors)
	}
	if !equalInt64Slice(got.SampleSeqs, []int64{2}) {
		t.Errorf("errors[error].sample_seqs = %v, want [2]", got.SampleSeqs)
	}
	if len(got.SampleTSs) != 1 || got.SampleTSs[0] != "2026-06-02T00:00:05Z" {
		t.Errorf("errors[error].sample_ts = %v, want [2026-06-02T00:00:05Z]",
			got.SampleTSs)
	}
}

// TestDigestErrorSeqsWholeRun pins ADR-039: the error seq list is NOT capped at
// the 25 tool-sample cap — it keeps the whole run's errors (up to
// maxDigestErrorSeqs) so the mobile Errors lens can render the complete,
// navigable error list. error_count stays the exact total regardless.
func TestDigestErrorSeqsWholeRun(t *testing.T) {
	const n = 40 // > maxDigestSampleSeqs (25), < maxDigestErrorSeqs (200)
	events := make([]foldEvent, 0, n)
	for i := 1; i <= n; i++ {
		events = append(events, foldEvent{
			Seq: int64(i), Kind: "tool_result",
			TS:       fmt.Sprintf("2026-06-02T00:%02d:00Z", i),
			Producer: "agent",
			Payload:  map[string]any{"is_error": true, "tool_use_id": fmt.Sprintf("t%d", i)},
		})
	}
	d, _ := computeAgentDigest("a", "t", events)

	if d.SchemaVersion != digestSchemaVersion {
		t.Errorf("schema_version = %d, want %d", d.SchemaVersion, digestSchemaVersion)
	}
	if d.ErrorCount != n {
		t.Errorf("error_count = %d, want %d", d.ErrorCount, n)
	}
	agg := d.Errors["tool_error"]
	if agg == nil {
		t.Fatalf("missing tool_error class; classes=%v", d.Errors)
	}
	// The whole run's error seqs are kept (was capped at 25 before ADR-039).
	if len(agg.SampleSeqs) != n {
		t.Errorf("tool_error sample_seqs len = %d, want %d (whole run, not 25-capped)",
			len(agg.SampleSeqs), n)
	}
	if len(agg.SampleTSs) != len(agg.SampleSeqs) {
		t.Errorf("sample_ts len %d != sample_seqs len %d",
			len(agg.SampleTSs), len(agg.SampleSeqs))
	}
}

// TestDigestTurnStartAdoptsSyntheticTurn pins the fold's turn.start adoption:
// the hub inserts the user's input.text (opening a synthetic turn) before the
// driver emits turn.start, so turn.start must ADOPT that synthetic turn — one
// turn with the real id and start_seq at the prompt — not close+reopen into a
// spurious empty turn. (Guards the claude M2/M4 turn.start emission + the
// latent ACP case.)
func TestDigestTurnStartAdoptsSyntheticTurn(t *testing.T) {
	events := []foldEvent{
		{Seq: 1, Kind: "input.text", TS: "2026-06-02T00:00:00Z", Producer: "user",
			Payload: map[string]any{"text": "do it"}},
		{Seq: 2, Kind: "turn.start", TS: "2026-06-02T00:00:01Z", Producer: "agent",
			Payload: map[string]any{"turn_id": "t1"}},
		{Seq: 3, Kind: "text", TS: "2026-06-02T00:00:02Z", Producer: "agent",
			Payload: map[string]any{"text": "ok"}},
		{Seq: 4, Kind: "turn.result", TS: "2026-06-02T00:00:03Z", Producer: "agent",
			Payload: map[string]any{"turn_id": "t1", "status": "success"}},
	}
	d, turns := computeAgentDigest("a", "t", events)
	if d.TurnCount != 1 {
		t.Errorf("turn_count = %d, want 1", d.TurnCount)
	}
	if len(turns) != 1 {
		t.Fatalf("len(turns) = %d, want 1 (no spurious synthetic turn); got %+v",
			len(turns), turns)
	}
	got := turns[0]
	if got.TurnID != "t1" {
		t.Errorf("turn id = %q, want t1 (adopted real id)", got.TurnID)
	}
	if got.StartSeq != 1 {
		t.Errorf("start_seq = %d, want 1 (the prompt, kept on adoption)", got.StartSeq)
	}
	if got.EndSeq != 4 || got.Status != "success" {
		t.Errorf("end_seq/status = %d/%q, want 4/success", got.EndSeq, got.Status)
	}
}
