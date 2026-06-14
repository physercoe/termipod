package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/termipod/hub/internal/auth"
)

// TestRequireOwnerOrSteward pins the #75 gate: the human director
// (owner/operator) and steward-role agents pass; worker agents, host tokens,
// non-owner humans, and anonymous callers are rejected with 403. The
// steward/worker scope role short-circuits resolveAgentRole, so no DB needed.
func TestRequireOwnerOrSteward(t *testing.T) {
	s := &Server{}
	cases := []struct {
		name string
		tok  *auth.Token
		want bool
	}{
		{"owner", &auth.Token{Kind: "owner"}, true},
		{"operator", &auth.Token{Kind: "operator"}, true},
		{"steward agent", &auth.Token{Kind: "agent", ScopeJSON: `{"role":"steward","agent_id":"a1"}`}, true},
		{"worker agent", &auth.Token{Kind: "agent", ScopeJSON: `{"role":"worker","agent_id":"a2"}`}, false},
		{"host", &auth.Token{Kind: "host"}, false},
		{"non-owner user", &auth.Token{Kind: "user"}, false},
		{"no token", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/x", nil)
			if c.tok != nil {
				r = r.WithContext(auth.WithToken(r.Context(), c.tok))
			}
			w := httptest.NewRecorder()
			got := s.requireOwnerOrSteward(w, r)
			if got != c.want {
				t.Fatalf("requireOwnerOrSteward = %v, want %v (status %d)", got, c.want, w.Code)
			}
			if !c.want && w.Code != http.StatusForbidden {
				t.Fatalf("rejection should write 403, got %d", w.Code)
			}
		})
	}
}
