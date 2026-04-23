package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

type attentionIn struct {
	ScopeKind   string   `json:"scope_kind"`            // 'team' | 'project' | 'channel'
	ScopeID     string   `json:"scope_id,omitempty"`
	ProjectID   string   `json:"project_id,omitempty"`
	Kind        string   `json:"kind"`                  // 'decision' | 'approval' | 'idle' | ...
	Summary     string   `json:"summary"`
	Severity    string   `json:"severity,omitempty"`
	RefEventID  string   `json:"ref_event_id,omitempty"`
	RefTaskID   string   `json:"ref_task_id,omitempty"`
	Assignees   []string `json:"assignees,omitempty"`
}

type attentionOut struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"project_id,omitempty"`
	ScopeKind   string          `json:"scope_kind"`
	ScopeID     string          `json:"scope_id,omitempty"`
	Kind        string          `json:"kind"`
	Summary     string          `json:"summary"`
	Severity    string          `json:"severity"`
	RefEventID  string          `json:"ref_event_id,omitempty"`
	RefTaskID   string          `json:"ref_task_id,omitempty"`
	Assignees   json.RawMessage `json:"assignees"`
	Decisions   json.RawMessage `json:"decisions"`
	Escalation  json.RawMessage `json:"escalation_history"`
	Status      string          `json:"status"`
	CreatedAt   string          `json:"created_at"`
	ResolvedAt  *string         `json:"resolved_at,omitempty"`
	ResolvedBy  string          `json:"resolved_by,omitempty"`
}

func (s *Server) handleCreateAttention(w http.ResponseWriter, r *http.Request) {
	var in attentionIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.ScopeKind == "" || in.Kind == "" || in.Summary == "" {
		writeErr(w, http.StatusBadRequest, "scope_kind, kind, summary required")
		return
	}
	severity := in.Severity
	if severity == "" {
		severity = "minor"
	}
	assignees, _ := json.Marshal(coalesceStrings(in.Assignees))
	id := NewID()
	now := NowUTC()
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			ref_event_id, ref_task_id, summary, severity,
			current_assignees_json, status, created_at
		) VALUES (?, NULLIF(?, ''), ?, NULLIF(?, ''), ?,
		          NULLIF(?, ''), NULLIF(?, ''), ?, ?,
		          ?, 'open', ?)`,
		id, in.ProjectID, in.ScopeKind, in.ScopeID, in.Kind,
		in.RefEventID, in.RefTaskID, in.Summary, severity,
		string(assignees), now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "created_at": now})
}

func (s *Server) handleListAttention(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	scope := r.URL.Query().Get("scope")
	q := `
		SELECT id, COALESCE(project_id, ''), scope_kind, COALESCE(scope_id, ''), kind,
		       COALESCE(ref_event_id, ''), COALESCE(ref_task_id, ''),
		       summary, severity,
		       current_assignees_json, decisions_json, escalation_history_json,
		       status, created_at, resolved_at, COALESCE(resolved_by, '')
		FROM attention_items WHERE status = ?`
	args := []any{status}
	if scope != "" {
		q += " AND scope_kind = ?"
		args = append(args, scope)
	}
	q += " ORDER BY created_at DESC LIMIT 200"
	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []attentionOut{}
	for rows.Next() {
		var a attentionOut
		var assignees, decisions, esc string
		var resolvedAt sql.NullString
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.ScopeKind, &a.ScopeID, &a.Kind,
			&a.RefEventID, &a.RefTaskID, &a.Summary, &a.Severity,
			&assignees, &decisions, &esc, &a.Status, &a.CreatedAt,
			&resolvedAt, &a.ResolvedBy); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.Assignees = json.RawMessage(assignees)
		a.Decisions = json.RawMessage(decisions)
		a.Escalation = json.RawMessage(esc)
		if resolvedAt.Valid {
			a.ResolvedAt = &resolvedAt.String
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, out)
}

type attentionDecideIn struct {
	Decision string `json:"decision"` // 'approve' | 'reject'
	By       string `json:"by,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type attentionDecideOut struct {
	AttentionID string          `json:"attention_id"`
	Decision    string          `json:"decision"`
	Resolved    bool            `json:"resolved"`
	Executed    json.RawMessage `json:"executed,omitempty"` // populated when an approve triggers an action
}

// handleDecideAttention records an approve/reject on an attention_item and
// resolves it once the tier's quorum is reached. Quorum is looked up via
// s.policy.QuorumFor(tier); a tier of "" or a missing `quorum` entry both
// fall through to 1, which preserves the previous single-approver behavior.
// A reject always resolves (veto-wins); approvals accumulate in
// decisions_json until the threshold is hit. When an approve_-resolved
// attention has a pending_payload, this handler executes it (currently:
// spawn, template_proposal) so the caller can observe the downstream effect
// in a single call.
//
// Concurrency note: two simultaneous approvals can both read approves=N-1
// and each write approves=N without noticing the other. That's tolerable
// today — the net effect is one executed action, not two, and the duplicate
// decision row is visible in the trail. Tightening this needs a CAS on the
// status column; deferred until we have >1 active approver per tier.
func (s *Server) handleDecideAttention(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "id")
	var in attentionDecideIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Decision != "approve" && in.Decision != "reject" {
		writeErr(w, http.StatusBadRequest, "decision must be approve|reject")
		return
	}

	var kind, tier, payload, decisions, status, scopeID string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT kind, COALESCE(tier, ''), COALESCE(pending_payload_json, ''),
		       decisions_json, status, COALESCE(scope_id, '')
		FROM attention_items WHERE id = ?`, id).
		Scan(&kind, &tier, &payload, &decisions, &status, &scopeID)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "attention not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if status != "open" {
		writeErr(w, http.StatusConflict, "attention already resolved")
		return
	}

	// Append the decision to decisions_json.
	var list []map[string]any
	_ = json.Unmarshal([]byte(decisions), &list)
	now := NowUTC()
	list = append(list, map[string]any{
		"at":       now,
		"by":       in.By,
		"decision": in.Decision,
		"reason":   in.Reason,
	})
	newDecisions, _ := json.Marshal(list)

	// Policy-driven quorum: count approves including the one we just
	// appended, compare against the tier threshold. A reject always
	// resolves so a single vetoer can halt the action. When the threshold
	// isn't yet met, persist the decision and leave status='open' so
	// further approvers can weigh in.
	approves := 0
	for _, d := range list {
		if s, _ := d["decision"].(string); s == "approve" {
			approves++
		}
	}
	need := s.policy.QuorumFor(tier)
	resolved := in.Decision == "reject" || approves >= need

	// resolved_by has a FK to agents(id); in.By is a handle used only for
	// the decision trail, so it lands in decisions_json, not the FK column.
	if resolved {
		_, err = s.db.ExecContext(r.Context(), `
			UPDATE attention_items SET
				decisions_json = ?,
				status = 'resolved',
				resolved_at = ?
			WHERE id = ?`, string(newDecisions), now, id)
	} else {
		_, err = s.db.ExecContext(r.Context(), `
			UPDATE attention_items SET
				decisions_json = ?
			WHERE id = ?`, string(newDecisions), id)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := attentionDecideOut{AttentionID: id, Decision: in.Decision, Resolved: resolved}
	if resolved && in.Decision == "approve" && kind == "approval_request" && payload != "" {
		// Dispatch the gated action. Today we only understand spawn payloads.
		var sp spawnIn
		if err := json.Unmarshal([]byte(payload), &sp); err == nil && sp.ChildHandle != "" {
			result, _, err := s.DoSpawn(r.Context(), team, sp)
			if err == nil {
				b, _ := json.Marshal(map[string]any{
					"kind":      "spawn",
					"spawn_id":  result.SpawnID,
					"agent_id":  result.AgentID,
					"spawned_at": result.SpawnedAt,
				})
				out.Executed = b
			} else {
				b, _ := json.Marshal(map[string]any{
					"kind":  "spawn",
					"error": err.Error(),
				})
				out.Executed = b
			}
		}
	}
	if resolved && in.Decision == "approve" && kind == "template_proposal" && payload != "" {
		// Install the proposed template body to team/templates/<cat>/<name>.
		// Reviewer's approval is the authorization; we copy the blob, not the
		// request content, so the on-disk file is byte-identical to what was
		// reviewed even if the blob store is the source of truth.
		installed, installErr := s.installProposedTemplate(payload)
		if installErr == nil {
			out.Executed = installed
		} else {
			b, _ := json.Marshal(map[string]any{
				"kind":  "template_install",
				"error": installErr.Error(),
			})
			out.Executed = b
		}
	}
	s.recordAudit(r.Context(), team, "attention.decide", "attention", id,
		in.Decision+" attention ("+kind+")",
		map[string]any{
			"decision": in.Decision,
			"kind":     kind,
			"tier":     tier,
			"by":       in.By,
			"reason":   in.Reason,
		})
	writeJSON(w, http.StatusOK, out)
}

// installProposedTemplate reads the proposed blob and writes it to the
// team's templates/<category>/<name> path. Returns the JSON-encoded result
// so it can be surfaced to the reviewer.
func (s *Server) installProposedTemplate(payload string) ([]byte, error) {
	var p struct {
		Category   string `json:"category"`
		Name       string `json:"name"`
		BlobSHA256 string `json:"blob_sha256"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	if p.Category == "" || p.Name == "" || p.BlobSHA256 == "" {
		return nil, errors.New("payload missing category/name/blob_sha256")
	}
	body, err := os.ReadFile(s.blobPath(p.BlobSHA256))
	if err != nil {
		return nil, fmt.Errorf("read blob: %w", err)
	}
	dstDir := filepath.Join(s.cfg.DataRoot, "team", "templates", p.Category)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, err
	}
	// Trailing .yaml keeps the file discoverable by listTemplates; the
	// agent's proposed "name" already includes a version suffix like v1.
	name := p.Name
	if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
		name += ".yaml"
	}
	dst := filepath.Join(dstDir, name)
	if err := os.WriteFile(dst, body, 0o644); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"kind":     "template_install",
		"category": p.Category,
		"name":     p.Name,
		"path":     dst,
		"bytes":    len(body),
	})
}

type attentionResolveIn struct {
	ResolvedBy string          `json:"resolved_by,omitempty"`
	Decision   json.RawMessage `json:"decision,omitempty"`
}

func (s *Server) handleResolveAttention(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var in attentionResolveIn
	_ = json.NewDecoder(r.Body).Decode(&in)

	now := NowUTC()
	res, err := s.db.ExecContext(r.Context(), `
		UPDATE attention_items SET
			status = 'resolved',
			resolved_at = ?,
			resolved_by = NULLIF(?, '')
		WHERE id = ? AND status = 'open'`,
		now, in.ResolvedBy, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "item not found or already resolved")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
