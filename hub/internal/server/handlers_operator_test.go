package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/termipod/hub/internal/auth"
)

// TestBootstrapMintsOperator: ADR-037 D4 — `hub init` mints the hub
// root as an `operator`, not a `default` owner.
func TestBootstrapMintsOperator(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	plain, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	s, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var kind string
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT kind FROM auth_tokens WHERE token_hash = ?`,
		auth.HashToken(plain)).Scan(&kind); err != nil {
		t.Fatalf("query bootstrap token: %v", err)
	}
	if kind != "operator" {
		t.Errorf("bootstrap token kind = %q, want operator", kind)
	}
}

// TestOperatorPrincipalSplit_AdminGate: ADR-037 D2 — only an operator
// reaches /v1/admin/*; a per-team owner is rejected (403).
func TestOperatorPrincipalSplit_AdminGate(t *testing.T) {
	s, operatorTok := newA2ATestServer(t) // Init token is now an operator
	ownerTok := mintTeamToken(t, s, "owner", defaultTeamID)

	// Operator reaches the admin endpoint (no live hosts → still authorized,
	// just an empty result; the point is it is not 403).
	if st, body := doReq(t, s, operatorTok, http.MethodGet, "/v1/admin/hosts", nil); st == http.StatusForbidden {
		t.Errorf("operator at /v1/admin/hosts: got 403, want authorized\nbody: %s", body)
	}
	// Per-team owner is refused at the operator gate.
	if st, body := doReq(t, s, ownerTok, http.MethodGet, "/v1/admin/hosts", nil); st != http.StatusForbidden {
		t.Errorf("owner at /v1/admin/hosts: got %d, want 403\nbody: %s", st, body)
	}
}

// TestOperatorPrincipalSplit_OwnerStillOwns: an owner retains owner-level
// reach for its own team (requireOwner admits operator AND owner).
func TestOperatorPrincipalSplit_OwnerStillOwns(t *testing.T) {
	s, _ := newA2ATestServer(t)
	ownerTok := mintTeamToken(t, s, "owner", defaultTeamID)

	// Owner can issue a token for its own team.
	st, body := doReq(t, s, ownerTok, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/tokens",
		map[string]any{"kind": "user", "handle": "alice"})
	if st != http.StatusCreated {
		t.Errorf("owner issuing own-team token: got %d, want 201\nbody: %s", st, body)
	}
}

// TestOperatorBypassesTeamGate_EndToEnd upgrades the W1 unit-level
// operator-bypass test now that `operator` is a real bearer (F-01). An
// operator scoped to `default` reaches another team's data; a per-team
// owner of `default` cannot.
func TestOperatorBypassesTeamGate_EndToEnd(t *testing.T) {
	s, operatorTok := newA2ATestServer(t) // operator scoped to `default`
	if _, err := s.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES ('teamb', 'team-b', ?)`,
		NowUTC()); err != nil {
		t.Fatalf("seed teamb: %v", err)
	}
	ownerTok := mintTeamToken(t, s, "owner", defaultTeamID)

	// Operator transcends the team gate.
	req := httptest.NewRequest(http.MethodGet, "/v1/teams/teamb/projects", nil)
	req.Header.Set("Authorization", "Bearer "+operatorTok)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	if w.Code == http.StatusForbidden {
		t.Errorf("operator at teamb: got 403, want bypass\nbody: %s", w.Body.String())
	}

	// Default-scoped owner is still bound by the gate.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/teams/teamb/projects", nil)
	req2.Header.Set("Authorization", "Bearer "+ownerTok)
	w2 := httptest.NewRecorder()
	s.router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Errorf("default owner at teamb: got %d, want 403\nbody: %s", w2.Code, w2.Body.String())
	}
}
