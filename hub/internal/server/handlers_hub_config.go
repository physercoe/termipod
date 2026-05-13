// handlers_hub_config.go — owner-gated REST surface for hub-wide
// governance files (ADR-016). MVP exposes the operation-scope
// manifest (`roles.yaml`); same pattern extends to other hub-level
// config files as they appear.
//
// Three operations per file:
//
//   GET    /v1/hub/config/roles  → current effective body (overlay
//                                  if present, embedded otherwise)
//   PUT    /v1/hub/config/roles  → validate-then-swap; on success
//                                  hot-reloads via initRoles()
//   DELETE /v1/hub/config/roles  → removes the on-disk override,
//                                  hot-reloads back to embedded
//                                  default (the "Reset to default"
//                                  affordance mobile exposes)
//
// All three require an owner-kind token. A malformed roles.yaml
// fails the gate closed, so validate-then-swap on PUT keeps a
// last-known-good backup at `<DataRoot>/roles.yaml.bak` — operator
// can recover by restoring the .bak file out of band if the
// validation that hot-reload runs ever proves insufficient.

package server

import (
	"io"
	"net/http"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// handleGetRolesConfig returns the active roles.yaml body. Reads
// the on-disk overlay first; falls back to the embedded default so
// the editor always has something to show even on fresh installs.
func (s *Server) handleGetRolesConfig(w http.ResponseWriter, r *http.Request) {
	if !s.requireOwner(w, r) {
		return
	}
	path := filepath.Join(s.cfg.DataRoot, "roles.yaml")
	body, err := os.ReadFile(path)
	if err == nil {
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	if !os.IsNotExist(err) {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// No overlay on disk — return the embedded default so the mobile
	// editor has a starting point. The user editing this surfaces the
	// embedded body and "Save" will write the overlay for the first
	// time.
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(rolesEmbedFS)
}

// handlePutRolesConfig validates the supplied YAML, writes it to
// `<DataRoot>/roles.yaml`, and hot-reloads the manifest. On parse
// failure the on-disk file is NOT touched — the gate keeps using
// whatever was previously loaded. Returns the freshly-active body
// in the response so the mobile editor can show the canonical
// version (matches the YAML-on-PUT contract `handlers_templates.go`
// uses for prompt/agent templates).
func (s *Server) handlePutRolesConfig(w http.ResponseWriter, r *http.Request) {
	if !s.requireOwner(w, r) {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(body) == 0 {
		writeErr(w, http.StatusBadRequest, "empty body")
		return
	}
	// Validate by parsing the same shape `loadRoles` expects. Catches
	// the common authoring slip (wrong key, bad indent) BEFORE we
	// commit to disk. The deeper "does this manifest deny everything"
	// check is left to `initRoles` after the write — failure there
	// rolls back to the backup.
	var probe rolesFile
	if err := yaml.Unmarshal(body, &probe); err != nil {
		writeErr(w, http.StatusBadRequest, "yaml parse: "+err.Error())
		return
	}
	if probe.Roles == nil || len(probe.Roles) == 0 {
		writeErr(w, http.StatusBadRequest,
			"manifest must declare at least one role under `roles:`")
		return
	}

	dir := s.cfg.DataRoot
	if dir == "" {
		writeErr(w, http.StatusInternalServerError, "data_root not configured")
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	path := filepath.Join(dir, "roles.yaml")
	bak := filepath.Join(dir, "roles.yaml.bak")

	// Snapshot the previous overlay to `.bak` so an operator can
	// recover out-of-band if the new file passes parse but somehow
	// breaks the gate. Best-effort; absence of a prior file is fine.
	if prior, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(bak, prior, 0o644)
	}

	if err := os.WriteFile(path, body, 0o644); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Hot-reload. If this fails, restore the .bak so the gate stays
	// usable. (Failure here is improbable since we parsed above —
	// but the loader's allow_all / pattern compile is a deeper check
	// than yaml.Unmarshal, so it's possible.)
	if err := initRoles(dir); err != nil {
		if prior, rErr := os.ReadFile(bak); rErr == nil {
			_ = os.WriteFile(path, prior, 0o644)
			_ = initRoles(dir)
		}
		writeErr(w, http.StatusBadRequest, "reload manifest: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// handleResetRolesConfig removes the on-disk overlay, hot-reloads
// the embedded default, and returns the embedded body. The mobile
// "Reset to default" affordance calls this.
//
// Idempotent: succeeds whether or not an overlay file currently
// exists. No backup of an existing overlay is taken — the user
// chose to reset, and the embedded default is what the binary
// shipped with anyway.
func (s *Server) handleResetRolesConfig(w http.ResponseWriter, r *http.Request) {
	if !s.requireOwner(w, r) {
		return
	}
	dir := s.cfg.DataRoot
	if dir == "" {
		writeErr(w, http.StatusInternalServerError, "data_root not configured")
		return
	}
	path := filepath.Join(dir, "roles.yaml")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := initRoles(dir); err != nil {
		writeErr(w, http.StatusInternalServerError,
			"reload manifest: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(rolesEmbedFS)
}
