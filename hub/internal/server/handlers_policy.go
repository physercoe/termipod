package server

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

// maxPolicyBytes caps the incoming policy.yaml size. The real policy file
// is tiny (tiers + approvers + quorum + escalation); 1 MiB is already
// absurdly generous and stops a pathological client from filling the disk.
const maxPolicyBytes = 1 << 20

// handleGetPolicy returns the raw bytes of <dataRoot>/team/policy.yaml.
// Returns an empty 200 body when the file is absent so the mobile editor
// can show a blank canvas rather than an error. Content-Type is
// application/yaml so clients can display it as text.
func (s *Server) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	_ = chi.URLParam(r, "team") // team scoping is tenant-level; single file
	path := filepath.Join(s.cfg.DataRoot, "team", "policy.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handlePutPolicy validates, atomically writes, and hot-reloads policy.yaml.
// A parse failure returns 400 without touching the file on disk — we
// refuse to overwrite a good policy with broken YAML.
func (s *Server) handlePutPolicy(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	r.Body = http.MaxBytesReader(w, r.Body, maxPolicyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "body too large or unreadable")
		return
	}
	// Empty body clears the policy file — permissive default applies.
	var parsed Policy
	if len(body) > 0 {
		if err := yaml.Unmarshal(body, &parsed); err != nil {
			writeErr(w, http.StatusBadRequest, "yaml parse: "+err.Error())
			return
		}
	}

	dir := filepath.Join(s.cfg.DataRoot, "team")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmp, err := os.CreateTemp(dir, "policy.yaml.*.tmp")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after Rename
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tmp.Close(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	finalPath := filepath.Join(dir, "policy.yaml")
	if err := os.Rename(tmpName, finalPath); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	if s.policy != nil {
		s.policy.reload()
	}
	s.recordAudit(r.Context(), team, "policy.edit", "policy", "team",
		"edit team policy.yaml",
		map[string]any{
			"bytes":      len(body),
			"tiers":      len(parsed.Tiers),
			"approvers":  len(parsed.Approvers),
			"quorum":     len(parsed.Quorum),
			"escalation": len(parsed.Escalate),
		},
	)
	w.WriteHeader(http.StatusNoContent)
}
