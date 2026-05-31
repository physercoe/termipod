package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/termipod/hub/internal/auth"
)

// mintTeamToken inserts a bearer of the given kind scoped to team and
// returns the plaintext, for driving requests through the real router.
func mintTeamToken(t *testing.T, s *Server, kind, team string) string {
	t.Helper()
	plain := auth.NewToken()
	scope := `{"team":"` + team + `","role":"principal","handle":"h"}`
	if err := auth.InsertToken(context.Background(), s.db, kind, scope,
		plain, NewID(), NowUTC()); err != nil {
		t.Fatalf("mint %s token for %s: %v", kind, team, err)
	}
	return plain
}

// TestTeamGate_CrossTeamForbidden asserts ADR-037 D1: a token scoped to
// `default` is rejected (403) when it addresses another team's path,
// across every legitimate bearer kind, while same-team access is
// admitted (i.e. the gate does not 403 it).
func TestTeamGate_CrossTeamForbidden(t *testing.T) {
	s, _ := newTestServer(t)
	// Second team the default-scoped tokens must not be able to reach.
	if _, err := s.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		"teamb", "team-b", NowUTC()); err != nil {
		t.Fatalf("seed teamb: %v", err)
	}

	for _, kind := range []string{"owner", "user", "host"} {
		tok := mintTeamToken(t, s, kind, defaultTeamID)

		// Cross-team → 403 from the gate.
		cross := httptest.NewRequest(http.MethodGet, "/v1/teams/teamb/projects", nil)
		cross.Header.Set("Authorization", "Bearer "+tok)
		cw := httptest.NewRecorder()
		s.router.ServeHTTP(cw, cross)
		if cw.Code != http.StatusForbidden {
			t.Errorf("kind=%s cross-team: got %d, want 403\nbody: %s",
				kind, cw.Code, cw.Body.String())
		}

		// Same-team → must NOT be 403 (200 list; the point is the gate
		// admits it, not the handler's exact status).
		same := httptest.NewRequest(http.MethodGet, "/v1/teams/"+defaultTeamID+"/projects", nil)
		same.Header.Set("Authorization", "Bearer "+tok)
		sw := httptest.NewRecorder()
		s.router.ServeHTTP(sw, same)
		if sw.Code == http.StatusForbidden {
			t.Errorf("kind=%s same-team: got 403, want admitted\nbody: %s",
				kind, sw.Body.String())
		}
	}
}

// TestTeamGate_NoScopeTeamFailsClosed asserts a token with no team in
// its scope cannot reach any team-scoped route (fail-closed).
func TestTeamGate_NoScopeTeamFailsClosed(t *testing.T) {
	s, _ := newTestServer(t)
	plain := auth.NewToken()
	if err := auth.InsertToken(context.Background(), s.db, "user",
		`{"role":"principal"}`, plain, NewID(), NowUTC()); err != nil {
		t.Fatalf("mint teamless token: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/teams/"+defaultTeamID+"/projects", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("teamless token: got %d, want 403\nbody: %s", w.Code, w.Body.String())
	}
}

// TestTeamGate_OperatorBypass unit-tests the operator branch directly.
// An operator token is not yet a legitimate bearer (the F-01 allowlist
// admits it in W2), so this drives the middleware with a synthetic
// routed context rather than the full HTTP stack. When W2 lands, an
// end-to-end operator-bypass test joins TestTeamGate_CrossTeamForbidden.
func TestTeamGate_OperatorBypass(t *testing.T) {
	s, _ := newTestServer(t)

	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	h := s.teamGate(next)

	// Operator scoped to `default` addressing `teamb` must pass through.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("team", "teamb")
	ctx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	ctx = auth.WithToken(ctx, &auth.Token{
		Kind: "operator", ScopeJSON: `{"team":"` + defaultTeamID + `"}`,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/teams/teamb/x", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !reached || w.Code != http.StatusOK {
		t.Errorf("operator bypass: reached=%v code=%d, want true/200", reached, w.Code)
	}

	// Control: a non-operator with a mismatched team must NOT pass.
	reached = false
	ctx2 := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	ctx2 = auth.WithToken(ctx2, &auth.Token{
		Kind: "user", ScopeJSON: `{"team":"` + defaultTeamID + `"}`,
	})
	req2 := httptest.NewRequest(http.MethodGet, "/v1/teams/teamb/x", nil).WithContext(ctx2)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if reached || w2.Code != http.StatusForbidden {
		t.Errorf("non-operator mismatch: reached=%v code=%d, want false/403", reached, w2.Code)
	}
}
