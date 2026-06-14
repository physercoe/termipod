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

// handleGetPolicyKinds returns the parsed `kinds:` block from
// <dataRoot>/team/policy.yaml as JSON. Read-only — the file
// itself is authored by hand and PUT through handlePutPolicy.
// ADR-030 W21 mobile policy viewer consumes this so it doesn't
// need a YAML parser in Dart.
//
// Response shape:
//
//	{
//	  "kinds": {
//	    "deliverable.set_state": {
//	      "default_tier": "project-steward",
//	      "commits": true,
//	      "override_allowed": true,
//	      "quorum": { "project-steward": {"m": 1} }
//	    },
//	    ...
//	  }
//	}
//
// Empty file (or missing file) → 200 with `{"kinds": {}}` so the
// mobile viewer renders an empty-state rather than an error.
func (s *Server) handleGetPolicyKinds(w http.ResponseWriter, r *http.Request) {
	_ = chi.URLParam(r, "team") // tenant-level; single file
	path := filepath.Join(s.cfg.DataRoot, "team", "policy.yaml")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.writeDBErr(w, err)
		return
	}
	var parsed Policy
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &parsed); err != nil {
			writeErr(w, http.StatusInternalServerError,
				"yaml parse: "+err.Error())
			return
		}
	}
	if parsed.Kinds == nil {
		parsed.Kinds = map[string]KindPolicy{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"kinds": parsed.Kinds})
}

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
		s.writeDBErr(w, err)
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
		s.writeDBErr(w, err)
		return
	}
	tmp, err := os.CreateTemp(dir, "policy.yaml.*.tmp")
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after Rename
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		s.writeDBErr(w, err)
		return
	}
	if err := tmp.Close(); err != nil {
		s.writeDBErr(w, err)
		return
	}
	finalPath := filepath.Join(dir, "policy.yaml")
	if err := os.Rename(tmpName, finalPath); err != nil {
		s.writeDBErr(w, err)
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
