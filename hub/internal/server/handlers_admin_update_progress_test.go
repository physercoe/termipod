package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestHostUpdateProgress_PostThenGet round-trips one sample: the host
// POSTs progress mid-update (same auth posture as heartbeat), the
// owner-scope admin endpoint reads the latest back.
func TestHostUpdateProgress_PostThenGet(t *testing.T) {
	s, token := newA2ATestServer(t)

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/hosts/host-p/update-progress",
		map[string]any{"phase": "downloading", "done": 5, "total": 10, "to_version": "v1.0.1-alpha"})
	if status != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", status, body)
	}

	status, body = doReq(t, s, token, http.MethodGet,
		"/v1/admin/hosts/host-p/update-progress", nil)
	if status != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", status, body)
	}
	var p hostUpdateProgress
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Phase != "downloading" || p.Done != 5 || p.Total != 10 || p.ToVersion != "v1.0.1-alpha" {
		t.Errorf("progress = %+v, want downloading 5/10 to v1.0.1-alpha", p)
	}
	if p.At.IsZero() {
		t.Error("At not stamped server-side")
	}
}

// TestHostUpdateProgress_IdleWhenNeverReported: a host with no sample
// reads as phase=idle so the client can tell "not started" from error.
func TestHostUpdateProgress_IdleWhenNeverReported(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/admin/hosts/host-ghost/update-progress", nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if !strings.Contains(string(body), `"idle"`) {
		t.Errorf("body=%s, want phase idle", body)
	}
}

// TestHostUpdateProgress_AdminGetNonOwner403 locks the owner-scope gate
// on the read half.
func TestHostUpdateProgress_AdminGetNonOwner403(t *testing.T) {
	s, _ := newA2ATestServer(t)
	memberToken := mintNonOwnerToken(t, s, defaultTeamID)
	status, body := doReq(t, s, memberToken, http.MethodGet,
		"/v1/admin/hosts/host-p/update-progress", nil)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", status, body)
	}
}

// TestHostUpdateProgress_BadPhase400 rejects an unknown phase so a
// garbage sample never reaches the admin pane.
func TestHostUpdateProgress_BadPhase400(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/hosts/host-p/update-progress",
		map[string]any{"phase": "vibing"})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", status, body)
	}
}

// TestNormalizeUpdateChannel pins the post-lane-split default: an unset
// channel resolves alpha (hub/host lanes ship prereleases only), an
// explicit one passes through untouched.
func TestNormalizeUpdateChannel(t *testing.T) {
	in := AdminFleetUpdateRequest{}
	normalizeUpdateChannel(&in)
	if in.Channel != "alpha" {
		t.Errorf("empty channel normalized to %q, want alpha", in.Channel)
	}
	in = AdminFleetUpdateRequest{Channel: "stable"}
	normalizeUpdateChannel(&in)
	if in.Channel != "stable" {
		t.Errorf("explicit channel rewritten to %q, want stable untouched", in.Channel)
	}
}
