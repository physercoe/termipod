package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Round-trip: fanout 2 workers → both have sessions tagged with the
// correlation_id → both received their task as input.text → posting
// worker_report from one + terminating the other satisfies gather.
//
// This is the load-bearing integration: if any of those four stages
// drift apart the steward's orchestration loop silently breaks.
func TestOrchestrate_FanoutGatherReport(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, _ = seedChannelAndAgent(t, s, "", "host-x")

	// agents.fanout — spawn two workers, atomically post their tasks.
	args, _ := json.Marshal(map[string]any{
		"correlation_id": "test-corr-1",
		"workers": []map[string]any{
			{
				"handle":          "worker-a",
				"kind":            "claude-code",
				"host_id":         "host-x",
				"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude\n",
				"task":            "evaluate config A",
			},
			{
				"handle":          "worker-b",
				"kind":            "claude-code",
				"host_id":         "host-x",
				"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude\n",
				"task":            "evaluate config B",
			},
		},
	})
	out, jerr := s.mcpAgentsFanout(context.Background(), defaultTeamID, args)
	if jerr != nil {
		t.Fatalf("fanout: %+v", jerr)
	}

	// Decode the JSON-text MCP result back into a map.
	body := mcpResultBody(t, out)
	workers, ok := body["workers"].([]any)
	if !ok || len(workers) != 2 {
		t.Fatalf("fanout result missing workers: %+v", body)
	}
	idA := pickAgentID(t, workers, "worker-a")
	idB := pickAgentID(t, workers, "worker-b")

	// Both sessions should carry the correlation_id.
	var corrCount int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE correlation_id = ?`,
		"test-corr-1",
	).Scan(&corrCount)
	if corrCount != 2 {
		t.Errorf("session correlation_id stamping count = %d; want 2", corrCount)
	}

	// Each worker should have an input.text event with their task body.
	for _, want := range []struct{ id, task string }{
		{idA, "evaluate config A"},
		{idB, "evaluate config B"},
	} {
		var payload string
		_ = s.db.QueryRow(
			`SELECT payload_json FROM agent_events
			   WHERE agent_id = ? AND kind = 'input.text'
			   ORDER BY seq DESC LIMIT 1`,
			want.id,
		).Scan(&payload)
		if !strings.Contains(payload, want.task) {
			t.Errorf("worker %s input payload missing task %q: %q",
				want.id, want.task, payload)
		}
	}

	// Worker A posts its report.
	reportArgs, _ := json.Marshal(map[string]any{
		"status":     "success",
		"summary_md": "Config A converged at step 850, loss=2.41",
		"output_artifacts": []string{"trackio://run-A"},
		"budget_used_usd": 0.42,
	})
	if _, jerr := s.mcpReportsPost(context.Background(), idA, reportArgs); jerr != nil {
		t.Fatalf("reports.post A: %+v", jerr)
	}

	// Worker B is terminated (simulating a crash that satisfies gather
	// with terminal status, sans report).
	doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/agents/"+idB,
		map[string]any{"status": "terminated"})

	// agents.gather should now return both as done. Use a tight timeout
	// because we know everything is settled — long-poll just walks one
	// iteration before allDone trips.
	gatherArgs, _ := json.Marshal(map[string]any{
		"correlation_id": "test-corr-1",
		"timeout_s":      5,
	})
	gOut, jerr := s.mcpAgentsGather(context.Background(), defaultTeamID, gatherArgs)
	if jerr != nil {
		t.Fatalf("gather: %+v", jerr)
	}
	gBody := mcpResultBody(t, gOut)
	if got, _ := gBody["timed_out"].(bool); got {
		t.Errorf("gather timed_out=true; expected all done")
	}
	gw, _ := gBody["workers"].([]any)
	for _, w := range gw {
		m := w.(map[string]any)
		if done, _ := m["done"].(bool); !done {
			t.Errorf("worker %v not marked done: %+v", m["handle"], m)
		}
	}
}

// reports.post requires status to be one of success|partial|failed.
// The shape contract is the only thing the steward parses against; an
// arbitrary status string would let workers smuggle freeform text into
// what should be a typed handshake.
func TestOrchestrate_ReportsPost_StatusValidation(t *testing.T) {
	s, _ := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	for _, bad := range []string{"", "done", "ok", "in-progress"} {
		args, _ := json.Marshal(map[string]any{
			"status":     bad,
			"summary_md": "x",
		})
		_, jerr := s.mcpReportsPost(context.Background(), agentID, args)
		if jerr == nil {
			t.Errorf("status=%q accepted; should be rejected", bad)
		}
	}

	// Sanity: success/partial/failed all accept.
	for _, good := range []string{"success", "partial", "failed"} {
		args, _ := json.Marshal(map[string]any{
			"status":     good,
			"summary_md": "x",
		})
		if _, jerr := s.mcpReportsPost(context.Background(), agentID, args); jerr != nil {
			t.Errorf("status=%q rejected: %+v", good, jerr)
		}
	}
}

// agents.gather returns timed_out=true with whatever partial results
// it gathered when no worker has reached a done state by the deadline.
// The steward needs the partial set to decide: wait again or give up.
func TestOrchestrate_GatherTimesOutWithPartial(t *testing.T) {
	s, _ := newA2ATestServer(t)
	_, _ = seedChannelAndAgent(t, s, "", "host-x")

	// Fanout one worker, don't satisfy it.
	args, _ := json.Marshal(map[string]any{
		"correlation_id": "test-corr-timeout",
		"workers": []map[string]any{{
			"handle":          "slow-worker",
			"kind":            "claude-code",
			"host_id":         "host-x",
			"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude\n",
			"task":            "think for an hour",
		}},
	})
	if _, jerr := s.mcpAgentsFanout(context.Background(), defaultTeamID, args); jerr != nil {
		t.Fatalf("fanout: %+v", jerr)
	}

	gatherArgs, _ := json.Marshal(map[string]any{
		"correlation_id": "test-corr-timeout",
		"timeout_s":      1,
	})
	start := time.Now()
	out, jerr := s.mcpAgentsGather(context.Background(), defaultTeamID, gatherArgs)
	elapsed := time.Since(start)
	if jerr != nil {
		t.Fatalf("gather: %+v", jerr)
	}
	if elapsed > 2*time.Second {
		t.Errorf("gather elapsed %v; expected ~1s", elapsed)
	}
	body := mcpResultBody(t, out)
	if got, _ := body["timed_out"].(bool); !got {
		t.Errorf("gather timed_out=false; expected true")
	}
}

// --- helpers ---

func mcpResultBody(t *testing.T, raw any) map[string]any {
	t.Helper()
	m, _ := raw.(map[string]any)
	content, _ := m["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("MCP result has no content: %+v", raw)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode result text: %v (text=%q)", err, text)
	}
	return out
}

func pickAgentID(t *testing.T, workers []any, handle string) string {
	t.Helper()
	for _, w := range workers {
		m, _ := w.(map[string]any)
		if (m["handle"]) == handle {
			id, _ := m["agent_id"].(string)
			if id == "" {
				t.Fatalf("worker %s missing agent_id: %+v", handle, m)
			}
			return id
		}
	}
	t.Fatalf("worker %s not found in fanout result", handle)
	return ""
}
