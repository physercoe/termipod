package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Per-host admin control verbs (ADR-028 Phase 5 / plan W22). The
// fleet-wide /v1/admin/fleet/* routes drive every live host at once;
// these single-host routes back the per-row buttons in the mobile
// Admin pane. All owner-scope, all write the same audit rows as the
// fleet path because they share stopOneHost / updateOneHost.

// getHostRow resolves one host's id/team/name. The bool is false when
// no such host exists, so callers can answer 404 without conflating a
// missing host with a DB error.
func (s *Server) getHostRow(ctx context.Context, hostID string) (hostRow, bool, error) {
	var h hostRow
	err := s.db.QueryRowContext(ctx,
		`SELECT id, team_id, COALESCE(name, '') FROM hosts WHERE id = ?`,
		hostID).Scan(&h.id, &h.teamID, &h.name)
	switch {
	case err == nil:
		return h, true, nil
	case err.Error() == "sql: no rows in result set":
		return hostRow{}, false, nil
	default:
		return hostRow{}, false, err
	}
}

// handleAdminHostShutdown is POST /v1/admin/hosts/{host}/shutdown —
// owner-scope. Fires host.shutdown (exit 0) at one host.
func (s *Server) handleAdminHostShutdown(w http.ResponseWriter, r *http.Request) {
	s.adminHostStopVerb(w, r, "host.shutdown")
}

// handleAdminHostRestart is POST /v1/admin/hosts/{host}/restart —
// owner-scope. Fires host.restart (exit 75) at one host.
func (s *Server) handleAdminHostRestart(w http.ResponseWriter, r *http.Request) {
	s.adminHostStopVerb(w, r, "host.restart")
}

// adminHostStopVerb is the shared per-host shutdown / restart handler.
// It resolves the host (404 on a typo'd id), then delegates to
// stopOneHost so the session-stop + audit behaviour matches the fleet
// path exactly.
func (s *Server) adminHostStopVerb(w http.ResponseWriter, r *http.Request, verb string) {
	if !s.requireOwner(w, r) {
		return
	}
	host := chi.URLParam(r, "host")
	if host == "" {
		writeErr(w, http.StatusBadRequest, "host id required")
		return
	}
	h, found, err := s.getHostRow(r.Context(), host)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	var in AdminFleetShutdownRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&in)
	}
	if in.Reason == "" {
		in.Reason = strings.TrimPrefix(verb, "host.") + "-host"
	}
	writeJSON(w, http.StatusOK, s.stopOneHost(r.Context(), verb, h, in))
}

// handleAdminHostUpdate is POST /v1/admin/hosts/{host}/update —
// owner-scope. Fires host.update at one host (fetch + verify + install
// + exit 75). Delegates to updateOneHost so the audit row matches the
// fleet path.
func (s *Server) handleAdminHostUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireOwner(w, r) {
		return
	}
	host := chi.URLParam(r, "host")
	if host == "" {
		writeErr(w, http.StatusBadRequest, "host id required")
		return
	}
	h, found, err := s.getHostRow(r.Context(), host)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	var in AdminFleetUpdateRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&in)
	}
	if in.Reason == "" {
		in.Reason = "update-host"
	}
	writeJSON(w, http.StatusOK, s.updateOneHost(r.Context(), h, in))
}
