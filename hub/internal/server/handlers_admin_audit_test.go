package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// TestAdminListAudit_NonOwner403 locks the owner gate.
func TestAdminListAudit_NonOwner403(t *testing.T) {
	s, _ := newA2ATestServer(t)
	memberToken := mintNonOwnerToken(t, s, defaultTeamID)
	status, _ := doReq(t, s, memberToken, http.MethodGet,
		"/v1/admin/audit", nil)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
}

// TestAdminListAudit_PrefixAndTargetKind seeds a spread of audit rows
// across teams and confirms the action_prefix and target_kind filters
// behave — prefix is left-anchored so "host." catches the whole verb
// family in one query.
func TestAdminListAudit_PrefixAndTargetKind(t *testing.T) {
	s, token := newA2ATestServer(t)
	ctx := context.Background()
	s.recordAudit(ctx, defaultTeamID, "host.shutdown", "host", "h1", "shutdown h1", nil)
	s.recordAudit(ctx, defaultTeamID, "host.restart", "host", "h2", "restart h2", nil)
	s.recordAudit(ctx, defaultTeamID, "host.update", "host", "h3", "update h3", nil)
	s.recordAudit(ctx, defaultTeamID, "agent.spawn", "agent", "a1", "spawn a1", nil)

	// action_prefix=host. — catches all three host verbs in one query.
	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/admin/audit?action_prefix=host.", nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var out struct {
		Events []AdminAuditRow `json:"events"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Events) != 3 {
		t.Fatalf("host. prefix returned %d events, want 3", len(out.Events))
	}
	for _, e := range out.Events {
		if e.TargetKind != "host" {
			t.Errorf("event %s target_kind=%q, want host", e.Action, e.TargetKind)
		}
	}

	// target_kind=agent narrows to the single agent.spawn row.
	status, body = doReq(t, s, token, http.MethodGet,
		"/v1/admin/audit?target_kind=agent", nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	out.Events = nil
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Events) != 1 || out.Events[0].Action != "agent.spawn" {
		t.Fatalf("target_kind=agent returned %+v, want one agent.spawn", out.Events)
	}
}
