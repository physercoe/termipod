package hostrunner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/termipod/hub/internal/hostrunner/a2a"
)

// explainRunner builds a Runner whose pane-state watcher reads a fixed screen
// instead of a tmux server. Nothing in this file may reach tmux.
func explainRunner(t *testing.T, screen, title string, capErr error) *Runner {
	t.Helper()
	w := newPaneStateWatch(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if w == nil {
		t.Fatal("embedded manifests failed to load")
	}
	w.capture = func(context.Context, string) (string, error) {
		if capErr != nil {
			return "", capErr
		}
		return screen, nil
	}
	w.meta = func(context.Context) (map[string]paneMeta, error) {
		return map[string]paneMeta{"%7": {title: title}}, nil
	}
	return &Runner{
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		HostID:     "host-1",
		paneStates: w,
	}
}

func callExplain(t *testing.T, r *Runner, payload map[string]any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r-1", Kind: "host.pane_explain", Payload: raw,
	})
	if resp == nil {
		t.Fatal("host.pane_explain returned nil — the dispatcher does not route it")
	}
	body, err := base64.StdEncoding.DecodeString(resp.BodyB64)
	if err != nil {
		t.Fatalf("body was not base64: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("body was not JSON: %v (%q)", err, body)
	}
	return resp.Status, out
}

// A handler with no dispatcher case returns nil, which the tunnel loop turns
// into unknown_verb — invisible to the hub exactly as a missing MCP dispatcher
// case is invisible to an agent. Pinned so the handler cannot ship without its
// route (the same lockstep the MCP catalog needs).
func TestPaneExplainVerbIsRouted(t *testing.T) {
	r := explainRunner(t, codexBlockedScreen, "project", nil)
	resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r", Kind: "host.pane_explain", Payload: json.RawMessage(`{}`),
	})
	if resp == nil {
		t.Fatal("host.pane_explain is not routed by handleHostVerb")
	}
	// Negative control: without it the assertion above would pass for any
	// string at all.
	if resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r", Kind: "host.pane_nonexistent", Payload: json.RawMessage(`{}`),
	}); resp != nil {
		t.Errorf("an unknown verb returned %+v, want nil", resp)
	}
}

func TestPaneExplainVerbReturnsTheFullRecord(t *testing.T) {
	r := explainRunner(t, codexBlockedScreen, "my project", nil)

	status, out := callExplain(t, r, map[string]any{
		"agent_id": "ag-1", "pane_id": "%7", "family": "codex",
	})
	if status != 200 {
		t.Fatalf("status = %d: %+v", status, out)
	}
	if out["mode"] != "live" {
		t.Errorf("mode = %v, want live", out["mode"])
	}
	if out["agent_id"] != "ag-1" || out["pane_id"] != "%7" || out["host_id"] != "host-1" {
		t.Errorf("record lost its subject: %+v", out)
	}
	if out["osc_title"] != "my project" {
		t.Errorf("osc_title = %v — the one input whose absence silently disables "+
			"a whole rule class must travel", out["osc_title"])
	}
	if out["screen_lines"] != float64(3) {
		t.Errorf("screen_lines = %v, want 3", out["screen_lines"])
	}
	ex, _ := out["explain"].(map[string]any)
	if ex == nil || ex["state"] != "blocked" {
		t.Fatalf("explain = %+v, want blocked", ex)
	}
	rules, _ := ex["rules"].([]any)
	if len(rules) < 2 {
		t.Fatalf("want every rule's outcome, got %d", len(rules))
	}
	// (That previews are BOUNDED — the part that matters for a pane holding a
	// secret — is pinned where the bound lives:
	// panestate.TestExplainResultDoesNotCarryTheWholeScreen. Asserting it here
	// against a 3-line fixture would pass for a reason unrelated to the bound.)
}

// A verb whose capture fails is not a 500: the pane is gone, or tmux is, and
// the caller should be told which layer failed.
func TestPaneExplainVerbCaptureFailure(t *testing.T) {
	r := explainRunner(t, "", "", errors.New("no such pane"))
	status, out := callExplain(t, r, map[string]any{
		"agent_id": "ag-1", "pane_id": "%7", "family": "codex",
	})
	if status != 502 {
		t.Fatalf("status = %d, want 502: %+v", status, out)
	}
	if out["error"] != "capture_failed" {
		t.Errorf("error = %v, want capture_failed", out["error"])
	}
}

// An unmapped family is a definite answer about an unclassifiable engine, not
// a failure to compute one — and the family has to be in it, because the usual
// cause is a typo or an engine nobody added to the overlay.
func TestPaneExplainVerbUnmappedFamily(t *testing.T) {
	r := explainRunner(t, codexBlockedScreen, "", nil)
	status, out := callExplain(t, r, map[string]any{
		"agent_id": "ag-1", "pane_id": "%7", "family": "kimi-code-ts",
	})
	if status != 422 {
		t.Fatalf("status = %d, want 422: %+v", status, out)
	}
	if out["error"] != "unmapped_family" || out["family"] != "kimi-code-ts" {
		t.Errorf("body should name the family and the reason: %+v", out)
	}
}

// A host whose embedded manifests failed to load supervises its agents without
// classification (newPaneStateWatch's deliberate degradation). The verb must
// say THAT, not fail in a way that reads as "the rules are wrong".
func TestPaneExplainVerbWhenDetectionIsDisabled(t *testing.T) {
	r := &Runner{Log: slog.New(slog.NewTextHandler(io.Discard, nil))} // paneStates nil
	status, out := callExplain(t, r, map[string]any{
		"agent_id": "ag-1", "pane_id": "%7", "family": "codex",
	})
	if status != 503 {
		t.Fatalf("status = %d, want 503: %+v", status, out)
	}
	if out["error"] != "detection_disabled" {
		t.Errorf("error = %v, want detection_disabled", out["error"])
	}
}

func TestPaneExplainVerbRequiresItsArguments(t *testing.T) {
	r := explainRunner(t, codexBlockedScreen, "", nil)
	for _, p := range []map[string]any{
		{"agent_id": "ag-1", "family": "codex"}, // no pane
		{"agent_id": "ag-1", "pane_id": "%7"},   // no family
	} {
		status, out := callExplain(t, r, p)
		if status != 400 {
			t.Errorf("payload %+v gave %d, want 400: %+v", p, status, out)
		}
	}
}

// explain deliberately ignores the tick's capture gate and startup grace: both
// exist to skip work nobody asked for, and this call IS the asking. A gate that
// leaked into it would answer about a pane it did not read.
func TestPaneExplainIgnoresTheCaptureGate(t *testing.T) {
	w := newPaneStateWatch(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if w == nil {
		t.Fatal("embedded manifests failed to load")
	}
	captures := 0
	w.capture = func(context.Context, string) (string, error) {
		captures++
		return codexBlockedScreen, nil
	}
	w.meta = func(context.Context) (map[string]paneMeta, error) { return nil, nil }
	// An entry that the tick would skip outright: idle, armed gate, in grace.
	w.entries["ag-1"] = &paneStateEntry{
		manifestID:   "codex",
		published:    paneStatePublish{state: "idle"},
		scanActivity: 1770000000,
	}

	for i := 0; i < 3; i++ {
		if _, err := w.explain(context.Background(), "ag-1", "%7", "codex"); err != nil {
			t.Fatalf("explain: %v", err)
		}
	}
	if captures != 3 {
		t.Errorf("captured %d times for 3 explain calls; the gate leaked in", captures)
	}
}
