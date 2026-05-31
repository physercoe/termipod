package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/termipod/hub/internal/auth"
)

// teamGate enforces ADR-037 D1 — the path-team authorization gate. A
// token authenticated for team T may only address resources under
// /v1/teams/T/…. It is a single middleware mounted on the team-scoped
// route group rather than a per-handler check: the per-handler shape is
// exactly what let G1 (any valid bearer can edit the URL to reach any
// team) exist — one missed handler is one hole. One chokepoint closes
// the class.
//
// The data layer already filters every query on the URL team
// (handlers_agents.go etc.); this gate makes that URL team trustworthy
// by binding it to the caller's token scope.
//
//   - Reads the matched route's {team} URL param. With no {team} param
//     the middleware is never mounted, so it only ever sees team-scoped
//     routes; the empty-param branch is a defensive no-op.
//   - operator-kind tokens are team-transcendent (ADR-037 D2) and bypass
//     the gate. The operator kind is not yet a legitimate bearer (it is
//     added to the F-01 allowlist in W2); the bypass is wired here so W2
//     is purely an allowlist change with no gate edit.
//   - Otherwise the token's scope team must equal the path team. A
//     mismatch — or a token with no scope team (fail-closed) — is 403.
func (s *Server) teamGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathTeam := chi.URLParam(r, "team")
		if pathTeam == "" {
			next.ServeHTTP(w, r)
			return
		}
		tok, ok := auth.FromContext(r.Context())
		if !ok || tok == nil {
			// Should be unreachable — the gate is mounted inside the
			// authed group, so auth.Middleware has already attached a
			// token. Fail closed if that invariant ever breaks.
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		if tok.Kind == "operator" {
			next.ServeHTTP(w, r)
			return
		}
		if tok.ScopeTeam() != pathTeam {
			writeErr(w, http.StatusForbidden,
				"token is not scoped to team "+pathTeam)
			return
		}
		next.ServeHTTP(w, r)
	})
}
