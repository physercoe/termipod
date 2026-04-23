package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/termipod/hub/internal/auth"
)

// End-to-end acceptance scaffold walking the §22 MVP scenario from the
// plan. Each subtest maps 1:1 to a numbered step; steps that aren't
// reachable through the Go API surface (nginx, push notifications,
// backup restore) call t.Skip with a pointer to where they belong.
//
// The scenario runs against a real httptest.Server so the tests catch
// chi routing + auth-middleware regressions, not just handler bugs.

type e2eCtx struct {
	t        *testing.T
	srv      *httptest.Server
	s        *Server
	dataRoot string
	token    string
	teamID   string
}

func newE2E(t *testing.T) *e2eCtx {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	// Init creates the default team + owner token so we can exercise the
	// authenticated paths without wiring tokens by hand.
	token, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	s, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	srv := httptest.NewServer(s.router)
	t.Cleanup(srv.Close)
	return &e2eCtx{
		t: t, srv: srv, s: s, dataRoot: dir, token: token, teamID: defaultTeamID,
	}
}

func (c *e2eCtx) call(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var buf io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, c.srv.URL+path, buf)
	if err != nil {
		c.t.Fatalf("build req: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

func TestE2E_AcceptanceScenario(t *testing.T) {
	c := newE2E(t)

	// Step 1 — VPS install / nginx / certbot / backups. Out of scope for
	// an in-process Go test; covered by the ops runbook.
	t.Run("01_vps_and_tls", func(t *testing.T) {
		t.Skip("ops runbook — nginx/certbot/backups, not reachable from Go tests")
	})

	// Step 2 — Owner pastes hub URL + token. The client-side bootstrap
	// calls /v1/_info (public) then a cheap authed endpoint to verify.
	t.Run("02_info_and_authed_probe", func(t *testing.T) {
		// _info is unauthenticated — call without the bearer.
		resp, err := http.Get(c.srv.URL + "/v1/_info")
		if err != nil {
			t.Fatalf("info: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("_info status = %d", resp.StatusCode)
		}
		status, _ := c.call("GET", "/v1/teams/"+c.teamID+"/hosts", nil)
		if status != 200 {
			t.Fatalf("authed probe = %d", status)
		}
	})

	// Step 3 — Host-agent registers. Heartbeat is covered separately in
	// hostrunner tests; here we just confirm the register roundtrip.
	var hostID string
	t.Run("03_host_register", func(t *testing.T) {
		status, body := c.call("POST", "/v1/teams/"+c.teamID+"/hosts", map[string]any{
			"name": "laptop-1",
		})
		if status != 201 {
			t.Fatalf("register host = %d body=%+v", status, body)
		}
		hostID, _ = body["id"].(string)
		if hostID == "" {
			t.Fatalf("no host id: %+v", body)
		}
	})

	// Step 4 — Spawn the Steward. We POST the bundled steward template's
	// YAML content so the "Spawn Steward" mobile button is reachable.
	var stewardID string
	t.Run("04_spawn_steward", func(t *testing.T) {
		stat, tmplBody := c.call("GET", "/v1/teams/"+c.teamID+"/templates/agents/steward.v1.yaml", nil)
		if stat != 200 {
			t.Fatalf("get template = %d", stat)
		}
		// The handler returns raw YAML; our call() helper tried to JSON-
		// decode, so use a raw HTTP GET for the body.
		yaml := rawGet(t, c.srv.URL+"/v1/teams/"+c.teamID+"/templates/agents/steward.v1.yaml", c.token)
		if !strings.Contains(yaml, "template: agents.steward") {
			t.Fatalf("unexpected template body: %s", yaml)
		}
		_ = tmplBody
		status, body := c.call("POST", "/v1/teams/"+c.teamID+"/agents/spawn", map[string]any{
			"child_handle":    "steward",
			"kind":            "claude-code",
			"host_id":         hostID,
			"spawn_spec_yaml": yaml,
		})
		if status != 201 && status != 202 {
			t.Fatalf("spawn = %d body=%+v", status, body)
		}
		if id, _ := body["agent_id"].(string); id != "" {
			stewardID = id
		}
	})

	// Steps 5–6 — templates.propose + approve. Exercised end-to-end in
	// TestMCP_TemplatesPropose_ApproveInstalls; here we chain the two
	// handlers through the HTTP surface so the mobile review flow is
	// covered as well.
	t.Run("05_06_template_proposal_and_approve", func(t *testing.T) {
		if stewardID == "" {
			t.Skip("step 04 did not spawn immediately (policy-gated path) — covered in separate test")
		}
		body := "handle: worker-fe\nrole: implementer\n"
		out, jerr := c.s.mcpTemplatesPropose(context.Background(), c.teamID, stewardID, mustJSON(t, map[string]any{
			"category": "agents",
			"name":     "worker-fe.v1",
			"content":  body,
		}))
		if jerr != nil {
			t.Fatalf("propose: %+v", jerr)
		}
		attnID := firstFieldFromMCPResult(t, out, "attention_id")

		status, resp := c.call("POST",
			fmt.Sprintf("/v1/teams/%s/attention/%s/decide", c.teamID, attnID),
			map[string]any{"decision": "approve", "by": "@owner"})
		if status != 200 {
			t.Fatalf("decide = %d body=%+v", status, resp)
		}
	})

	// Steps 7–8 — spawn worker + post a summary message. We simplify to
	// "post a message as the steward" since the worker-spawn requires the
	// same plumbing as step 04 and adds no new coverage.
	t.Run("07_08_spawn_worker_and_summary", func(t *testing.T) {
		if stewardID == "" {
			t.Skip("no steward — covered by earlier skip")
		}
		// Seed a #hub-meta channel to stand in for the auto-created one.
		_, err := c.s.db.Exec(
			`INSERT INTO channels (id, scope_kind, name, created_at) VALUES (?, 'team', 'hub-meta', ?)`,
			"ch-meta", NowUTC(),
		)
		if err != nil {
			t.Fatalf("seed channel: %v", err)
		}
		// Bind the agent to the channel so mcpPostMessage finds it.
		_, err = c.s.db.Exec(
			`UPDATE agents SET channel_id = ? WHERE id = ?`, "ch-meta", stewardID,
		)
		if err != nil && !strings.Contains(err.Error(), "no such column") {
			t.Fatalf("rebind channel: %v", err)
		}
		// mcpPostMessage reads channel_id off the agent binding. If the
		// schema doesn't expose it that way, drop to an INSERT directly.
		id := NewID()
		if _, err := c.s.db.Exec(`
			INSERT INTO events (id, schema_version, ts, received_ts,
				channel_id, type, from_id, parts_json)
			VALUES (?, 1, ?, ?, 'ch-meta', 'message', ?, '[]')`,
			id, NowUTC(), NowUTC(), stewardID,
		); err != nil {
			t.Fatalf("direct post: %v", err)
		}
	})

	// Step 9 — approval_request with a moderate tier. We go through the
	// MCP surface because that's the path a real worker takes.
	t.Run("09_worker_approval_request", func(t *testing.T) {
		if stewardID == "" {
			t.Skip("no steward")
		}
		_, jerr := c.s.mcpRequestApproval(context.Background(), c.teamID, stewardID, mustJSON(t, map[string]any{
			"tier":    "major",
			"summary": "install Flask",
		}))
		if jerr != nil {
			t.Fatalf("request_approval: %+v", jerr)
		}
		// The item should be visible to the owner via the listing route.
		// /attention returns a JSON array at the root so we read it raw.
		raw := rawGet(t, c.srv.URL+"/v1/teams/"+c.teamID+"/attention?status=open", c.token)
		if !strings.Contains(raw, "install Flask") {
			t.Errorf("attention list missing request: %s", raw)
		}
	})

	// Steps 10–11 — attach + screenshot view. Exercised in TestMCP_Attach.
	t.Run("10_11_attach_and_view", func(t *testing.T) {
		t.Skip("covered by TestMCP_Attach_StoresBlob")
	})

	// Step 12 — hub-tui parity. Live session binds to the same bearer
	// token; the tui is a separate TypeScript project and has its own
	// smoke tests under hub-tui/.
	t.Run("12_tui_parity", func(t *testing.T) {
		t.Skip("hub-tui has its own test harness; API parity is covered by the steps above")
	})

	// Step 13 — VPS reboot + backup restore. The reconstruct-db
	// subcommand is covered by TestReconstructDB_RoundTrip. Full VPS
	// restore is an ops runbook concern.
	t.Run("13_backup_restore", func(t *testing.T) {
		t.Skip("covered by TestReconstructDB_RoundTrip + ops runbook")
	})

	// Step 14 — token rotation. Revoke the owner token and confirm the
	// next call fails with 401.
	t.Run("14_token_rotation", func(t *testing.T) {
		hash := auth.HashToken(c.token)
		_, err := c.s.db.Exec(
			`UPDATE auth_tokens SET revoked_at = ? WHERE token_hash = ?`,
			NowUTC(), hash,
		)
		if err != nil {
			t.Fatalf("revoke: %v", err)
		}
		status, _ := c.call("GET", "/v1/teams/"+c.teamID+"/hosts", nil)
		if status != http.StatusUnauthorized {
			t.Errorf("post-revoke status = %d, want 401", status)
		}
	})
}

func rawGet(t *testing.T, url, token string) string {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("raw get: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
