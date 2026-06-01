package server

import (
	"encoding/json"
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
