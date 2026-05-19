package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// TestAdminDBVacuum_NonOwner403 locks the owner gate.
func TestAdminDBVacuum_NonOwner403(t *testing.T) {
	s, _ := newA2ATestServer(t)
	memberToken := mintNonOwnerToken(t, s, defaultTeamID)
	status, _ := doReq(t, s, memberToken, http.MethodPost,
		"/v1/admin/db/vacuum", map[string]any{})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
}

// TestAdminDBVacuum_OK runs VACUUM on the live test DB and confirms the
// response carries non-zero sizes and an audit row lands.
func TestAdminDBVacuum_OK(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/admin/db/vacuum", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var out AdminDBVacuumResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.BytesAfter <= 0 {
		t.Errorf("bytes_after = %d, want a positive page-derived size", out.BytesAfter)
	}
	if out.Reclaimed != out.BytesBefore-out.BytesAfter {
		t.Errorf("reclaimed=%d, want bytes_before-bytes_after=%d",
			out.Reclaimed, out.BytesBefore-out.BytesAfter)
	}
	var n int
	_ = s.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM audit_events WHERE action = 'db.vacuum'`).Scan(&n)
	if n != 1 {
		t.Errorf("db.vacuum audit rows = %d, want 1", n)
	}
}
