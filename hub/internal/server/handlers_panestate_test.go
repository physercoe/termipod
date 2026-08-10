package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/termipod/hub/internal/auth"
)

// A real codex approval screen, from the P1 corpus (which lifted it from
// herdr's own manifest tests). Using upstream's screen rather than one written
// to satisfy the rules is what makes an end-to-end assertion mean anything.
const codexApprovalScreen = "• Working (4s • esc to interrupt)\n" +
	"› 1. Yes, proceed\n" +
	"Press enter to confirm or esc to cancel\n"

func explainReq(t *testing.T, s *Server, token string, body any) (int, []byte) {
	t.Helper()
	return doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/pane_explain", body)
}

// The supplied-screen mode is herdr's `--file`: no host, no pane, no agent, so
// it is the half of P4 that CI can actually exercise. It is also the mode a
// rule author uses against a screen pasted into a bug report.
func TestPaneExplainSuppliedScreen(t *testing.T) {
	s, token := newA2ATestServer(t)

	code, body := explainReq(t, s, token, map[string]any{
		"family": "codex",
		"screen": codexApprovalScreen,
	})
	if code != http.StatusOK {
		t.Fatalf("explain = %d: %s", code, body)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["mode"] != "supplied" {
		t.Errorf("mode = %v, want supplied — a reader must know whether this "+
			"describes a live pane or a hypothetical", got["mode"])
	}
	if got["family"] != "codex" {
		t.Errorf("family = %v", got["family"])
	}
	ex, ok := got["explain"].(map[string]any)
	if !ok {
		t.Fatalf("no explain object: %s", body)
	}
	if ex["state"] != "blocked" {
		t.Errorf("state = %v, want blocked", ex["state"])
	}
	matched, ok := ex["matched_rule"].(map[string]any)
	if !ok || matched["id"] != "live_strong_blocker" {
		t.Errorf("matched_rule = %v, want live_strong_blocker", ex["matched_rule"])
	}
	if ex["visible_blocker"] != true {
		t.Errorf("a drawn dialog must report visible_blocker: %v", ex)
	}

	// The acceptance clause that makes this a debugger rather than a
	// classifier: the rules that did NOT match are in the answer, with the
	// region each one looked at.
	rules, ok := ex["rules"].([]any)
	if !ok || len(rules) < 2 {
		t.Fatalf("want the full rule set, got %v", ex["rules"])
	}
	var matchedCount, unmatchedCount, withPreview int
	for _, r := range rules {
		rm, _ := r.(map[string]any)
		if rm["matched"] == true {
			matchedCount++
		} else {
			unmatchedCount++
		}
		ev, _ := rm["evidence"].(map[string]any)
		if ev == nil {
			t.Errorf("rule %v carries no evidence", rm["id"])
			continue
		}
		if _, has := ev["region_bytes"]; !has {
			t.Errorf("rule %v evidence has no region_bytes", rm["id"])
		}
		// The preview is what turns "did not match" into something a human can
		// act on: it says what the rule was looking AT, not just what it
		// wanted. Without it an unmatched rule is an unfalsifiable claim.
		if p, _ := ev["region_preview"].(string); p != "" {
			withPreview++
		}
	}
	if matchedCount == 0 || unmatchedCount == 0 {
		t.Errorf("want both matched and unmatched rules to be distinguishable; "+
			"got %d matched / %d unmatched", matchedCount, unmatchedCount)
	}
	if withPreview == 0 {
		t.Error("no rule carried a region preview; the card would show verdicts with no evidence")
	}
}

// An engine nobody has a manifest for is a definite answer, not an error: the
// caller needs to see WHICH family went unmapped, because the usual cause is a
// typo or an engine that was never added to the overlay.
func TestPaneExplainUnmappedFamily(t *testing.T) {
	s, token := newA2ATestServer(t)

	code, body := explainReq(t, s, token, map[string]any{
		"family": "kimi-code-ts", // registered engine, deliberately unmapped
		"screen": "anything",
	})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("explain = %d, want 422: %s", code, body)
	}
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	if got["error"] != "unmapped_family" || got["family"] != "kimi-code-ts" {
		t.Errorf("body should name the family and the reason: %s", body)
	}
}

func TestPaneExplainRejectsMalformedRequests(t *testing.T) {
	s, token := newA2ATestServer(t)

	for _, tc := range []struct {
		name string
		body map[string]any
		want int
	}{
		{"neither", map[string]any{}, http.StatusBadRequest},
		// Exclusive on purpose: one asks about a live pane, the other about
		// text. A body with both would have to pick one silently.
		//
		// `family` is present deliberately. Without it this case 400s for a
		// missing family instead, and the exclusivity check could be deleted
		// with the test still green — which is exactly what happened before
		// the mutation pass caught it.
		{"both", map[string]any{"agent_id": "a", "family": "codex", "screen": "s"}, http.StatusBadRequest},
		{"screen without family", map[string]any{"screen": "s"}, http.StatusBadRequest},
		{"screen too large", map[string]any{
			"family": "codex",
			"screen": strings.Repeat("x", paneExplainScreenCap+1),
		}, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := explainReq(t, s, token, tc.body)
			if code != tc.want {
				t.Errorf("code = %d, want %d: %s", code, tc.want, body)
			}
		})
	}
}

// From the NETWORK an agent bearer never reaches this route at all — but the
// refusal comes from `auth.Middleware`'s bearer-kind allowlist, not from the
// handler. Asserted with the layer named, because a test that just sees 403
// would keep passing if the handler's own guard were deleted (it did: the
// mutation survived until this test was split in two).
func TestPaneExplainAgentBearerIsRefusedAtTheAuthLayer(t *testing.T) {
	s, _ := newA2ATestServer(t)
	agentTok := mintToken(t, s, "agent", map[string]any{
		"team": defaultTeamID, "role": "worker", "agent_id": "ag-1", "handle": "w1",
	})

	code, body := explainReq(t, s, agentTok, map[string]any{
		"family": "codex", "screen": codexApprovalScreen,
	})
	if code != http.StatusForbidden {
		t.Fatalf("agent token got %d, want 403: %s", code, body)
	}
	if !strings.Contains(string(body), "bearer auth") {
		t.Errorf("the 403 should come from the bearer-kind allowlist, not the "+
			"handler — if this changed, the handler guard is now load-bearing "+
			"on the network path too: %s", body)
	}

	// The same body from a director-tier token works, so the test pins the
	// token kind rather than a broken request.
	code, body = explainReq(t, s, mintToken(t, s, "user",
		map[string]any{"team": defaultTeamID, "handle": "dir"}),
		map[string]any{"family": "codex", "screen": codexApprovalScreen})
	if code != http.StatusOK {
		t.Fatalf("user token got %d, want 200: %s", code, body)
	}
}

// The handler's own guard, reached directly.
//
// The bearer-kind allowlist EXEMPTS the hub's in-process authority dispatch,
// where an agent token is the legitimate credential — so an MCP tool that
// dispatched to this route would arrive here with the network guard out of the
// path. That is the case this guard exists for, and it is the only test that
// reaches it: without it, deleting the guard passes the whole suite.
func TestPaneExplainRefusesAgentTokensInProcess(t *testing.T) {
	s, _ := newA2ATestServer(t)

	body := strings.NewReader(`{"family":"codex","screen":"x"}`)
	req := httptest.NewRequest(http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/pane_explain", body)
	ctx := auth.WithInProcessDispatch(req.Context())
	ctx = auth.WithToken(ctx, &auth.Token{ID: "t-1", Kind: "agent",
		ScopeJSON: `{"team":"` + defaultTeamID + `","agent_id":"ag-1","handle":"w1"}`})
	rr := httptest.NewRecorder()

	s.handlePaneExplain(rr, req.WithContext(ctx))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("in-process agent dispatch got %d, want 403: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "director tool") {
		t.Errorf("the refusal should be the handler's own: %s", rr.Body.String())
	}
}

// The coverage table is D-3's mapping, which is otherwise readable only by
// opening a YAML compiled into the binary. An unmapped engine has no row —
// listing one as `false` would suggest a manifest exists for it.
func TestPaneCoverageListsMappedFamiliesOnly(t *testing.T) {
	s, token := newA2ATestServer(t)

	code, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/pane_explain", nil)
	if code != http.StatusOK {
		t.Fatalf("coverage = %d: %s", code, body)
	}
	var got struct {
		Families []map[string]any `json:"families"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Families) == 0 {
		t.Fatal("no families mapped; the overlay lost its engines block")
	}
	byFamily := map[string]map[string]any{}
	for _, f := range got.Families {
		name, _ := f["family"].(string)
		byFamily[name] = f
	}
	codex, ok := byFamily["codex"]
	if !ok {
		t.Fatalf("codex is mapped in the overlay but missing here: %v", byFamily)
	}
	if codex["manifest_id"] != "codex" || codex["source"] != "vendor" {
		t.Errorf("codex row = %v; the row must name the manifest and where it came from", codex)
	}
	if codex["manifest_version"] == nil {
		t.Errorf("codex row has no manifest_version: %v", codex)
	}
	// The negative that makes the positive mean something.
	if _, listed := byFamily["kimi-code-ts"]; listed {
		t.Error("kimi-code-ts is deliberately unmapped and must not appear")
	}
}

// Live mode's preconditions each name what is missing. They have different
// fixes and the caller is a human debugging a detector, so collapsing them
// into one "cannot explain" would cost exactly the information they came for.
func TestPaneExplainLivePreconditions(t *testing.T) {
	s, token := newA2ATestServer(t)

	code, body := explainReq(t, s, token, map[string]any{"agent_id": "nope"})
	if code != http.StatusNotFound {
		t.Fatalf("unknown agent = %d, want 404: %s", code, body)
	}

	// An agent with no pane: a paneless driving mode, or one that never
	// launched. Seeded directly because no launcher runs in this test.
	paneless := seedAgentWithKind(t, s, defaultTeamID, "paneless", "codex", "")
	code, body = explainReq(t, s, token, map[string]any{"agent_id": paneless})
	if code != http.StatusConflict {
		t.Fatalf("paneless agent = %d, want 409: %s", code, body)
	}
	if !strings.Contains(string(body), "pane") {
		t.Errorf("409 should say which precondition failed: %s", body)
	}
}

// A terminated agent keeps the host and pane id it died with, and under
// tmux's remain-on-exit the pane may still be on screen. Classifying it would
// answer confidently about an agent that is not there — `idle` for a corpse —
// so it is refused by name.
func TestPaneExplainRefusesATerminalAgent(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-1", "box-1")

	for _, status := range []string{"terminated", "crashed", "failed", "archived"} {
		t.Run(status, func(t *testing.T) {
			id := seedAgentWithKind(t, s, defaultTeamID, "dead-"+status, "codex", "")
			if _, err := s.writeDB.Exec(
				`UPDATE agents SET host_id = ?, pane_id = ?, status = ? WHERE id = ?`,
				"host-1", "%7", status, id); err != nil {
				t.Fatalf("seed: %v", err)
			}
			code, body := explainReq(t, s, token, map[string]any{"agent_id": id})
			if code != http.StatusConflict {
				t.Fatalf("status %s = %d, want 409: %s", status, code, body)
			}
			if !strings.Contains(string(body), status) {
				t.Errorf("the 409 should name the status it refused on: %s", body)
			}
		})
	}

	// The control: the same row, running, gets past the precondition and on to
	// the host call. Without this the test above would pass for a handler that
	// refused every agent.
	live := seedAgentWithKind(t, s, defaultTeamID, "alive", "codex", "")
	if _, err := s.writeDB.Exec(
		`UPDATE agents SET host_id = ?, pane_id = ?, status = 'running' WHERE id = ?`,
		"host-1", "%7", live); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// No host-runner is attached in this test, so the tunnel call times out —
	// 504, which is proof the request reached the routing step.
	code, body := explainReq(t, s, token, map[string]any{"agent_id": live})
	if code != http.StatusGatewayTimeout {
		t.Fatalf("running agent = %d, want 504 (no host attached in this test): %s", code, body)
	}
}
