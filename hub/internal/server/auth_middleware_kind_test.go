package server

import (
	"net/http"
	"strings"
	"testing"
)

// F-01 — the bearer middleware allowlists human (owner|user) and deputy
// (host) token kinds. An agent token presented as a REST bearer is
// refused before any handler runs; agents authenticate via /mcp/{token}
// (mounted outside the middleware). This is the single backstop that
// keeps a stolen/misused agent token off the privileged REST surface
// (policy / template / spawn / admin), independent of whether each
// handler also has its own gate.
func TestBearerMiddleware_AllowlistsByKind(t *testing.T) {
	s, ownerTok := newA2ATestServer(t)
	seedEventChannel(t, s, "chan-mw")

	const middlewareMsg = "not permitted for bearer auth"
	// A benign authed route: listing channel events. Owner/host pass the
	// middleware (whatever the handler then returns); an agent bearer is
	// rejected by the middleware itself.
	path := "/v1/teams/" + defaultTeamID + "/channels/chan-mw/events"

	t.Run("agent refused at middleware", func(t *testing.T) {
		tok := mintToken(t, s, "agent", map[string]any{
			"team": defaultTeamID, "role": "worker", "agent_id": "a-1", "handle": "a",
		})
		status, body := doReq(t, s, tok, http.MethodGet, path, nil)
		if status != http.StatusForbidden {
			t.Fatalf("agent bearer = %d; want 403", status)
		}
		if !strings.Contains(string(body), middlewareMsg) {
			t.Errorf("expected middleware rejection, got: %s", string(body))
		}
	})

	for _, kind := range []string{"owner", "user", "host"} {
		t.Run(kind+" passes middleware", func(t *testing.T) {
			tok := ownerTok
			if kind != "owner" {
				tok = mintToken(t, s, kind, map[string]any{
					"team": defaultTeamID, "role": "member", "handle": kind,
				})
			}
			_, body := doReq(t, s, tok, http.MethodGet, path, nil)
			if strings.Contains(string(body), middlewareMsg) {
				t.Errorf("%s bearer should pass the middleware, got: %s", kind, string(body))
			}
		})
	}
}
