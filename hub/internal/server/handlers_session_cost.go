package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/termipod/hub/internal/pricing"
)

// pricingSessionCost is the thin glue between the pricing package and
// the rest of the server. Lives here (not in handlers_sessions.go) so
// the pricing import surface stays one file deep and tests that want
// to stub costs can replace this single function via build tags or a
// vars-only override later.
func pricingSessionCost(ctx context.Context, s *Server, team, sessionID string) (pricing.Result, error) {
	// Usage events live in the event store (ADR-045 P1), sharded per team
	// (P2) — pricing reads agent_events, so it takes the team's event reader,
	// not the control pool.
	er, err := s.eventsReader(team)
	if err != nil {
		return pricing.Result{}, err
	}
	return pricing.SessionCost(ctx, er, s.pricing, sessionID)
}

// sessionCostOut is the wire shape of GET /v1/teams/{team}/sessions/{session}/cost
// (ADR-036 D8 chip 2 + tooltip). Per-model breakdown is the "Usage by
// model" tooltip rows; missing_models powers the "rates not configured
// for: …" disclaimer; snapshot_date + origin tell the user how fresh
// (and from which tier) their pricing table is.
type sessionCostOut struct {
	SessionID    string                 `json:"session_id"`
	TotalUSD     float64                `json:"total_usd"`
	Breakdown    map[string]float64     `json:"breakdown_by_model,omitempty"`
	Tokens       map[string]tokenCounts `json:"tokens_by_model,omitempty"`
	Missing      []string               `json:"missing_models,omitempty"`
	SnapshotDate string                 `json:"snapshot_date,omitempty"`
	Origin       string                 `json:"origin,omitempty"`
	// Imputed is always true today — every value in this payload is
	// derived from a public-API rate sheet, NOT from per-token billing
	// data. Mobile surfaces this as a tooltip disclaimer so users on
	// subscription plans don't mistake the chip for an actual bill.
	Imputed bool `json:"imputed"`
}

type tokenCounts struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

// handleGetSessionCost answers GET /v1/teams/{team}/sessions/{session}/cost
// with the imputed per-model cost breakdown. Team membership is enforced
// the same way as the sibling session endpoints — caller's auth token
// must scope to the team and the session row must belong to that team.
//
// Returns:
//   - 200 with sessionCostOut on success (including for sessions whose
//     usage events sum to zero — UI prefers a $0.00 chip over a 404).
//   - 404 when the session row is absent for this team.
//   - 500 on database / aggregation failure.
//
// Per ADR-036 D9, callers MUST self-gate the chip on the response — a
// 200 with TotalUSD=0 AND empty Breakdown means "no priced usage in
// this session"; the chip should hide rather than render "$0.00" which
// would suggest a free session when the truth is "we can't price the
// models you used."
func (s *Server) handleGetSessionCost(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "session")

	// Existence + team-membership probe. Mirrors handleGetSession's
	// shape rather than calling it (we don't need the full sessionOut
	// just to confirm the row belongs to the caller's team).
	var rowID string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT id FROM sessions WHERE team_id = ? AND id = ?`,
		team, id).Scan(&rowID)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}

	if s.pricing == nil {
		// Server constructed without a pricing loader (test code path).
		// Return an empty-but-valid shape so the chip self-gates blank.
		writeJSON(w, http.StatusOK, sessionCostOut{
			SessionID: id, Imputed: true,
		})
		return
	}

	res, err := pricingSessionCost(r.Context(), s, team, id)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}

	out := sessionCostOut{
		SessionID:    id,
		TotalUSD:     res.TotalUSD,
		Breakdown:    res.Breakdown,
		Missing:      res.Missing,
		SnapshotDate: res.SnapshotDate,
		Origin:       string(res.Origin),
		Imputed:      true,
	}
	if len(res.Tokens) > 0 {
		out.Tokens = make(map[string]tokenCounts, len(res.Tokens))
		for model, tc := range res.Tokens {
			out.Tokens[model] = tokenCounts{
				Input:      tc.Input,
				Output:     tc.Output,
				CacheRead:  tc.CacheRead,
				CacheWrite: tc.CacheWrite,
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}
