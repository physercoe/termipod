package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/termipod/hub/internal/auth"
)

// seedHostToken inserts an active host-kind token and returns its
// plaintext. The rotate orchestrator templates the new token's scope
// off whatever host token it finds.
func seedHostToken(t *testing.T, s *Server) string {
	t.Helper()
	plain := auth.NewToken()
	scope := `{"team":"` + defaultTeamID + `","role":"host"}`
	if err := auth.InsertToken(context.Background(), s.db, "host", scope,
		plain, NewID(), NowUTC()); err != nil {
		t.Fatalf("seed host token: %v", err)
	}
	return plain
}

func countLiveHostTokens(t *testing.T, s *Server) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM auth_tokens WHERE kind = 'host' AND revoked_at IS NULL`).
		Scan(&n); err != nil {
		t.Fatalf("count host tokens: %v", err)
	}
	return n
}

// TestAdminTokensRotate_NoHostToken400 confirms rotation refuses when
// there is no host token to template from.
func TestAdminTokensRotate_NoHostToken400(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/admin/tokens/rotate", map[string]any{})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", status, body)
	}
}

// TestAdminTokensRotate_NonOwnerGets403 locks the owner-scope gate.
func TestAdminTokensRotate_NonOwnerGets403(t *testing.T) {
	s, _ := newA2ATestServer(t)
	memberToken := mintNonOwnerToken(t, s, defaultTeamID)
	status, _ := doReq(t, s, memberToken, http.MethodPost,
		"/v1/admin/tokens/rotate", map[string]any{})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
}

// TestAdminTokensRotate_NoLiveHostsKeepsOldToken confirms that with no
// live host to confirm the new token, the old tokens are NOT revoked.
func TestAdminTokensRotate_NoLiveHostsKeepsOldToken(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedHostToken(t, s)

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/admin/tokens/rotate", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var out AdminTokenRotateResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.NewTokenID == "" || out.NewToken == "" {
		t.Error("a new token should still be issued")
	}
	if out.OldRevoked {
		t.Error("old tokens must not be revoked with no host to confirm the new one")
	}
	// Both the old and the new token remain active.
	if n := countLiveHostTokens(t, s); n != 2 {
		t.Errorf("live host tokens = %d, want 2 (old + new)", n)
	}
}

// TestAdminTokensRotate_RotatesAndRevokes wires the full path: a host
// token to template from, one live host whose fake host-runner acks
// host.token_rotate, then asserts the old token is revoked and only the
// new one survives.
func TestAdminTokensRotate_RotatesAndRevokes(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedHostToken(t, s)
	seedTestHost(t, s, defaultTeamID, "host-live", "live-1")
	if _, err := s.db.ExecContext(context.Background(),
		`UPDATE hosts SET last_seen_at = datetime('now') WHERE id = ?`,
		"host-live"); err != nil {
		t.Fatalf("set last_seen: %v", err)
	}

	ts := httptest.NewServer(s.router)
	defer ts.Close()
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			if pollCtx.Err() != nil {
				return
			}
			pollReq, _ := http.NewRequestWithContext(pollCtx, http.MethodGet,
				ts.URL+"/v1/teams/"+defaultTeamID+"/hosts/host-live/a2a/tunnel/next?wait_ms=3000",
				nil)
			pollReq.Header.Set("Authorization", "Bearer "+token)
			resp, err := http.DefaultClient.Do(pollReq)
			if err != nil {
				return
			}
			if resp.StatusCode == http.StatusNoContent {
				resp.Body.Close()
				continue
			}
			var env tunnelRequest
			_ = json.NewDecoder(resp.Body).Decode(&env)
			resp.Body.Close()
			if env.Kind != "host.token_rotate" {
				continue
			}
			ack, _ := json.Marshal(map[string]any{"acked": true, "ok": true})
			reply := tunnelResponse{
				ReqID:   env.ReqID,
				Status:  http.StatusOK,
				Headers: map[string]string{"Content-Type": "application/json"},
				BodyB64: base64.StdEncoding.EncodeToString(ack),
			}
			b, _ := json.Marshal(reply)
			pr, _ := http.NewRequestWithContext(pollCtx, http.MethodPost,
				ts.URL+"/v1/teams/"+defaultTeamID+"/hosts/host-live/a2a/tunnel/responses",
				bytes.NewReader(b))
			pr.Header.Set("Authorization", "Bearer "+token)
			pr.Header.Set("Content-Type", "application/json")
			pResp, _ := http.DefaultClient.Do(pr)
			if pResp != nil {
				pResp.Body.Close()
			}
			return
		}
	}()
	time.Sleep(50 * time.Millisecond)

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/admin/tokens/rotate", map[string]any{"reason": "test-rotate"})
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var out AdminTokenRotateResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Hosts) != 1 || !out.Hosts[0].Acked {
		t.Fatalf("hosts = %+v, want one acked host", out.Hosts)
	}
	if !out.OldRevoked || out.RevokedCount != 1 {
		t.Errorf("old_revoked=%v revoked_count=%d, want true / 1",
			out.OldRevoked, out.RevokedCount)
	}
	if n := countLiveHostTokens(t, s); n != 1 {
		t.Errorf("live host tokens = %d, want 1 (only the new one)", n)
	}
	var n int
	_ = s.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM audit_events WHERE action = 'token.rotate'`).Scan(&n)
	if n != 1 {
		t.Errorf("token.rotate audit rows = %d, want 1", n)
	}

	pollCancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}
