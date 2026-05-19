package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/termipod/hub/internal/auth"
)

// Owner-scope database maintenance (ADR-028 Phase 5 / plan W22). The
// Phase 4 `hub-server db vacuum` CLI runs offline against the sqlite
// file; this endpoint VACUUMs the LIVE hub so the mobile Admin pane
// can reclaim space without taking the daemon down.

// AdminDBVacuumResponse reports the database size around the VACUUM.
// Sizes are derived from page_count × page_size, which tracks the main
// database file (the WAL is checkpointed as part of VACUUM).
type AdminDBVacuumResponse struct {
	BytesBefore int64 `json:"bytes_before"`
	BytesAfter  int64 `json:"bytes_after"`
	Reclaimed   int64 `json:"reclaimed"`
}

// dbSizeBytes returns the on-disk size of the main database, computed
// from the sqlite page pragmas. A pragma read failure yields 0 rather
// than an error — the size is reporting-only and must never fail the
// VACUUM around it.
func (s *Server) dbSizeBytes(ctx context.Context) int64 {
	var pageCount, pageSize int64
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0
	}
	return pageCount * pageSize
}

// handleAdminDBVacuum is POST /v1/admin/db/vacuum — owner-scope.
// Runs VACUUM on the live database (a whole-database rebuild — sqlite
// has no per-table form) and reports the bytes reclaimed.
func (s *Server) handleAdminDBVacuum(w http.ResponseWriter, r *http.Request) {
	if !s.requireOwner(w, r) {
		return
	}
	before := s.dbSizeBytes(r.Context())
	// VACUUM cannot run inside a transaction; a bare Exec satisfies that.
	if _, err := s.db.ExecContext(r.Context(), "VACUUM"); err != nil {
		writeErr(w, http.StatusInternalServerError, "vacuum: "+err.Error())
		return
	}
	after := s.dbSizeBytes(r.Context())
	out := AdminDBVacuumResponse{
		BytesBefore: before,
		BytesAfter:  after,
		Reclaimed:   before - after,
	}
	s.recordAudit(r.Context(), callerTeam(r), "db.vacuum", "hub", "",
		"vacuum hub database", map[string]any{
			"bytes_before": out.BytesBefore,
			"bytes_after":  out.BytesAfter,
			"reclaimed":    out.Reclaimed,
		})
	writeJSON(w, http.StatusOK, out)
}

// callerTeam resolves the team of the request's auth token, falling
// back to "default". Hub-wide admin actions (db.vacuum) still need a
// team to hang their audit row on; the owner's own team is the
// natural choice.
func callerTeam(r *http.Request) string {
	tok, ok := auth.FromContext(r.Context())
	if !ok || tok == nil {
		return "default"
	}
	var sc struct {
		Team string `json:"team"`
	}
	_ = json.Unmarshal([]byte(tok.ScopeJSON), &sc)
	return firstNonEmpty(sc.Team, "default")
}
