package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/termipod/hub/internal/auth"
)

// Owner-scope host-token rotation for the ops CLI (ADR-028 plan W20).
// POST /v1/admin/tokens/rotate issues a fresh host bearer, broadcasts it
// to every live host via the host.token_rotate verb, and — once every
// host has confirmed adoption — revokes the prior host tokens. The
// brick-safe ordering (ack before revoke) lives here and in the host
// verb handler.

// AdminTokenRotateRequest is the wire shape for the rotate orchestrator.
type AdminTokenRotateRequest struct {
	// ForceRevoke revokes the old tokens even when a host failed to ack
	// — recovery mode for when a host is known-dead and is blocking the
	// rotation. The dead host will need a fresh token to come back.
	ForceRevoke bool   `json:"force_revoke,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// AdminTokenRotateHostResult is the per-host adoption outcome.
type AdminTokenRotateHostResult struct {
	HostID   string `json:"host_id"`
	TeamID   string `json:"team_id"`
	HostName string `json:"host_name,omitempty"`
	Acked    bool   `json:"acked"`
	Error    string `json:"error,omitempty"`
}

// AdminTokenRotateResponse is the synchronous summary the CLI prints.
// NewToken is the plaintext — shown once, like `tokens issue`.
type AdminTokenRotateResponse struct {
	NewTokenID   string                       `json:"new_token_id"`
	NewToken     string                       `json:"new_token"`
	Hosts        []AdminTokenRotateHostResult `json:"hosts"`
	OldRevoked   bool                         `json:"old_tokens_revoked"`
	RevokedCount int                          `json:"revoked_count"`
	Note         string                       `json:"note,omitempty"`
}

// handleAdminTokensRotate is POST /v1/admin/tokens/rotate — owner-scope.
func (s *Server) handleAdminTokensRotate(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(w, r) {
		return
	}
	var in AdminTokenRotateRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&in)
	}
	if in.Reason == "" {
		in.Reason = "tokens-rotate"
	}

	// Template the new token's scope off an existing host token so it
	// carries exactly the scope working hosts already authenticate with
	// — no guessing at the team / role shape.
	var templateScope string
	switch err := s.db.QueryRowContext(r.Context(),
		`SELECT scope_json FROM auth_tokens
		  WHERE kind = 'host' AND revoked_at IS NULL
		  ORDER BY created_at DESC LIMIT 1`).Scan(&templateScope); {
	case err == nil:
		// found
	case err.Error() == "sql: no rows in result set":
		writeErr(w, http.StatusBadRequest,
			"no active host token to rotate — issue one with `tokens issue --kind host` first")
		return
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Issue the replacement token.
	plain := auth.NewToken()
	newID := NewID()
	if err := auth.InsertToken(r.Context(), s.db, "host", templateScope,
		plain, newID, NowUTC()); err != nil {
		writeErr(w, http.StatusInternalServerError, "issue new token: "+err.Error())
		return
	}

	out := AdminTokenRotateResponse{
		NewTokenID: newID,
		NewToken:   plain,
		Hosts:      []AdminTokenRotateHostResult{},
	}

	// Broadcast host.token_rotate to every live host.
	hosts, err := s.listLiveHosts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	allAcked := true
	for _, h := range hosts {
		res := AdminTokenRotateHostResult{HostID: h.id, TeamID: h.teamID, HostName: h.name}
		payload, _ := json.Marshal(map[string]any{"token": plain, "reason": in.Reason})
		ackCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		resp, verbErr := s.tunnel.enqueueHostVerb(ackCtx, h.id, "host.token_rotate", payload)
		cancel()
		switch {
		case verbErr != nil:
			res.Error = "verb: " + verbErr.Error()
		case resp == nil:
			res.Error = "verb: no response"
		case resp.Status >= 200 && resp.Status < 300:
			res.Acked = true
		default:
			res.Error = fmt.Sprintf("verb: status %d", resp.Status)
		}
		if !res.Acked {
			allAcked = false
		}
		out.Hosts = append(out.Hosts, res)
	}

	// Revoke the old tokens only once every live host has demonstrably
	// adopted the new one — otherwise an un-acked host keeps working on
	// its old token. force_revoke overrides for recovery.
	switch {
	case len(hosts) == 0 && !in.ForceRevoke:
		out.Note = "no live hosts — new token issued but old tokens were NOT " +
			"revoked (nothing confirmed the new one). Re-run when hosts are up, " +
			"or pass force_revoke."
	case !allAcked && !in.ForceRevoke:
		out.Note = "one or more hosts did not ack — old tokens were NOT revoked " +
			"so those hosts keep working. Fix them and re-run, or pass force_revoke."
	default:
		rev, rerr := s.db.ExecContext(r.Context(),
			`UPDATE auth_tokens SET revoked_at = ?
			  WHERE kind = 'host' AND id != ? AND revoked_at IS NULL`,
			NowUTC(), newID)
		if rerr != nil {
			writeErr(w, http.StatusInternalServerError, "revoke old tokens: "+rerr.Error())
			return
		}
		n, _ := rev.RowsAffected()
		out.OldRevoked = true
		out.RevokedCount = int(n)
	}

	// Audit — never log the plaintext.
	var sc struct {
		Team string `json:"team"`
	}
	_ = json.Unmarshal([]byte(templateScope), &sc)
	auditTeam := firstNonEmpty(sc.Team, "default")
	s.recordAudit(r.Context(), auditTeam, "token.rotate", "token", newID,
		"rotate host token", map[string]any{
			"reason":        in.Reason,
			"hosts_total":   len(hosts),
			"old_revoked":   out.OldRevoked,
			"revoked_count": out.RevokedCount,
			"force_revoke":  in.ForceRevoke,
			"new_token_id":  newID,
		})

	writeJSON(w, http.StatusOK, out)
}
