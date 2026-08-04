package hubmcpserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// run_metrics (compare-wall plan §3.5) closes the gap that let an agent see
// WHICH runs a director is comparing (the compare UIRef) and nothing about what
// the curves said. It must reach GET /v1/teams/{team}/runs/{run}/metrics and
// hand the hub's rows back untouched.
func TestToolsCall_RunMetrics(t *testing.T) {
	var sawPath string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"train/loss","points":[{"step":1,"value":2.5}],"last_value":2.5}]`))
	})
	tools := buildTools()

	line := []byte(`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"run_metrics","arguments":{"run":"r_1"}}}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response")
	}
	if sawPath != "/v1/teams/team-alpha/runs/r_1/metrics" {
		t.Errorf("hub saw path %q", sawPath)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *jsonrpcError `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal envelope: %v (%s)", err, raw)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected rpc error: %+v", resp.Error)
	}
	if resp.Result.IsError {
		t.Fatalf("tool reported isError: %+v", resp.Result.Content)
	}
	if len(resp.Result.Content) != 1 || !strings.Contains(resp.Result.Content[0].Text, `"train/loss"`) {
		t.Errorf("metric rows did not survive the passthrough: %+v", resp.Result.Content)
	}
}

// A missing `run` must be refused BEFORE any hub request — the same
// boundary rule runs_get follows. Reaching the hub with an empty id would
// GET .../runs//metrics, which is a 404 dressed up as a routing accident.
// The dispatcher's schema check (InputSchema `required`) fires first and the
// closure's own guard backs it up, so this asserts the outcome rather than
// which of the two layers spoke.
func TestToolsCall_RunMetrics_RequiresRun(t *testing.T) {
	called := false
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	tools := buildTools()

	line := []byte(`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"run_metrics","arguments":{}}}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response")
	}
	if called {
		t.Error("an argument-less run_metrics still reached the hub")
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal envelope: %v (%s)", err, raw)
	}
	if !resp.Result.IsError {
		t.Fatalf("an argument-less run_metrics reported success: %s", raw)
	}
	if len(resp.Result.Content) == 0 || !strings.Contains(resp.Result.Content[0].Text, `"run" is required`) {
		t.Errorf("refusal does not name the missing argument: %s", raw)
	}
}

// The catalog/spec/meta trio must move together (CLAUDE.md: a handler
// without a catalog entry is invisible to agents). This asserts the third
// leg too — a read tool that inherits the fail-closed ReadOnly=false default
// is advertised to clients as side-effecting and unsafe to batch.
func TestRunMetrics_IsAdvertisedAsARead(t *testing.T) {
	var spec *ToolSpec
	for _, s := range toolRegistry() {
		if s.Name == "run_metrics" {
			cp := s
			spec = &cp
			break
		}
	}
	if spec == nil {
		t.Fatal("run_metrics is missing from the ToolSpec registry")
	}
	if spec.Short == "" {
		t.Error("run_metrics has no one-line contract (ADR-031 W2.a)")
	}
	if !spec.ReadOnly {
		t.Error("run_metrics observes and must be ReadOnly")
	}
	if spec.Backend != "run_metrics" {
		t.Errorf("run_metrics Backend = %q, want the authority dispatch key", spec.Backend)
	}
	if !spec.WorkerEligible {
		t.Error("a worker reading its own run's curves needs no elevation")
	}
	// And it is reachable from the tool an agent lands on first.
	if !containsName(toolMeta["runs_get"].seeAlso, "run_metrics") {
		t.Error("runs_get should point at run_metrics — it is the next question about a run")
	}
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
