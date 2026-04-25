// Hot-editable agent family registry handlers.
//
// The embedded YAML in agentfamilies.familiesFS is the default set; this
// surface lets operators add a Kimi family or override an embedded one
// without rebuilding the hub. Files land in <DataRoot>/agent_families/
// and the in-memory cache invalidates on every successful mutation, so
// the next spawn-mode resolution and the next host-runner probe sweep
// pick up the change immediately.
//
// Permission model: write paths require an authenticated session (the
// auth middleware already gates /v1/teams/{team}/*); fine-grained
// "steward+owner only" gating is a follow-up — the same current state
// applies to /templates today.
//
// Multi-team isolation is post-MVP. The path component is team-scoped
// to match /templates' shape, but storage is hub-wide; the directory
// is shared across teams. See project_post_mvp_agent_families memory.
package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"github.com/termipod/hub/internal/agentfamilies"
)

// agentFamiliesOverlayDir is the canonical location for hot-editable
// override files. Lazy: the directory is created on first PUT so a fresh
// hub install with no overrides keeps DataRoot tidy.
func agentFamiliesOverlayDir(dataRoot string) string {
	if dataRoot == "" {
		return ""
	}
	return filepath.Join(dataRoot, "agent_families")
}

// agentFamilyNameRe gates the URL component before it touches disk.
// Same shape as the template-category guard: lowercase, dash-friendly,
// no slashes or dots so a malicious path like "../foo" can't escape the
// overlay directory.
var agentFamilyNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func safeAgentFamilyName(s string) bool { return agentFamilyNameRe.MatchString(s) }

// allowedModes is the closed set we let through validation. M3 is a
// reserved-but-unimplemented headless mode (blueprint §5.3.1 note); the
// resolver filters it out anyway, but rejecting it on PUT gives a clear
// error rather than a silently-dropped declaration.
var allowedModes = map[string]bool{"M1": true, "M2": true, "M4": true}

// allowedBillings is closed for the same reason — typos in YAML quietly
// became "billing unknown" before, which made the M1+subscription rule
// silently inert. Now a typo gets a 400.
var allowedBillings = map[string]bool{
	"":             true, // optional — no billing constraint
	"api_key":      true,
	"subscription": true,
}

// handleListAgentFamilies returns the merged embedded + overlay list
// with each entry tagged "embedded" / "override" / "custom". Mobile
// renders a chip from the source field so operators see at a glance
// which families are defaults vs. their own additions.
func (s *Server) handleListAgentFamilies(w http.ResponseWriter, r *http.Request) {
	views, err := s.agentFamilies.All()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(views))
	for _, v := range views {
		out = append(out, map[string]any{
			"family":            v.Family.Family,
			"bin":               v.Family.Bin,
			"version_flag":      v.Family.VersionFlag,
			"supports":          v.Family.Supports,
			"incompatibilities": v.Family.Incompatibilities,
			"source":            string(v.Source),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"families": out})
}

// handleGetAgentFamily returns a single family's record. Source still
// distinguishes the three cases — the editor uses it to decide whether
// the YAML field is editable (custom/override) or a read-only preview
// of the embedded default.
func (s *Server) handleGetAgentFamily(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "family")
	if !safeAgentFamilyName(name) {
		writeErr(w, http.StatusBadRequest, "invalid family name")
		return
	}
	views, err := s.agentFamilies.All()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, v := range views {
		if v.Family.Family == name {
			writeJSON(w, http.StatusOK, map[string]any{
				"family":            v.Family.Family,
				"bin":               v.Family.Bin,
				"version_flag":      v.Family.VersionFlag,
				"supports":          v.Family.Supports,
				"incompatibilities": v.Family.Incompatibilities,
				"source":            string(v.Source),
			})
			return
		}
	}
	writeErr(w, http.StatusNotFound, "family not found")
}

// handlePutAgentFamily creates or replaces an override file. Body is
// raw YAML (single Family entry, not the families: wrapper used by the
// embedded file). We strict-parse to catch typos at write time rather
// than letting the loader skip a malformed file silently on next probe.
//
// The path component must match the body's family field — drift between
// URL and body is the kind of mistake that produces ghost entries, and
// the URL is what the UI surfaces as canonical.
func (s *Server) handlePutAgentFamily(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	name := chi.URLParam(r, "family")
	if !safeAgentFamilyName(name) {
		writeErr(w, http.StatusBadRequest, "invalid family name")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<10)) // 8 KiB cap
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	fam, verr := validateFamilyYAML(body)
	if verr != nil {
		writeErr(w, http.StatusBadRequest, verr.Error())
		return
	}
	if fam.Family != name {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf(
			"family in body (%q) must match URL path (%q)", fam.Family, name))
		return
	}

	overlayDir := s.agentFamilies.OverlayDir()
	if overlayDir == "" {
		writeErr(w, http.StatusInternalServerError,
			"server has no DataRoot configured for overlay storage")
		return
	}
	if err := os.MkdirAll(overlayDir, 0o700); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	path := filepath.Join(overlayDir, name+".yaml")
	created := false
	if _, err := os.Stat(path); err != nil && errors.Is(err, os.ErrNotExist) {
		created = true
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.agentFamilies.Invalidate()

	action := "agent_family.updated"
	if created {
		action = "agent_family.created"
	}
	s.recordAudit(r.Context(), team, action, "agent_family",
		name, action+" "+name,
		map[string]any{"family": name, "size": len(body)},
	)

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"family": name, "size": len(body)})
}

// handleDeleteAgentFamily removes an override file. If the family has
// no override (i.e. it's purely embedded), the response is 409 — there
// is no file to delete and we don't support disabling embedded entries
// today. To hide an embedded family, write an override; the disable-
// without-delete flag is a post-MVP follow-up.
func (s *Server) handleDeleteAgentFamily(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	name := chi.URLParam(r, "family")
	if !safeAgentFamilyName(name) {
		writeErr(w, http.StatusBadRequest, "invalid family name")
		return
	}
	overlayDir := s.agentFamilies.OverlayDir()
	if overlayDir == "" {
		writeErr(w, http.StatusInternalServerError,
			"server has no DataRoot configured for overlay storage")
		return
	}
	path := filepath.Join(overlayDir, name+".yaml")
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Distinguish "this family doesn't exist anywhere" (404) from
			// "embedded only, nothing to delete" (409 with hint).
			if _, ok := s.agentFamilies.ByName(name); ok {
				writeErr(w, http.StatusConflict,
					"family is embedded; write an override to change it")
				return
			}
			writeErr(w, http.StatusNotFound, "family not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.agentFamilies.Invalidate()
	s.recordAudit(r.Context(), team, "agent_family.deleted", "agent_family",
		name, "delete "+name,
		map[string]any{"family": name},
	)
	w.WriteHeader(http.StatusNoContent)
}

// validateFamilyYAML strict-parses a single-family override body. Strict
// mode rejects unknown keys so a typo in `verison_flag` becomes a 400
// rather than a silently-ignored field. Returns the parsed Family on
// success — handler still re-checks Family vs. URL.
func validateFamilyYAML(body []byte) (agentfamilies.Family, error) {
	var f agentfamilies.Family
	dec := yaml.NewDecoder(strings.NewReader(string(body)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return f, fmt.Errorf("yaml: %w", err)
	}
	if f.Family == "" {
		return f, errors.New("family is required")
	}
	if !safeAgentFamilyName(f.Family) {
		return f, errors.New("family name must match [a-z0-9][a-z0-9-]{0,31}")
	}
	if f.Bin == "" {
		return f, errors.New("bin is required")
	}
	if len(f.Supports) == 0 {
		return f, errors.New("supports must list at least one mode")
	}
	for _, m := range f.Supports {
		if !allowedModes[m] {
			return f, fmt.Errorf("supports: unknown mode %q (allowed: M1, M2, M4)", m)
		}
	}
	for i, ic := range f.Incompatibilities {
		if !allowedModes[ic.Mode] {
			return f, fmt.Errorf("incompatibilities[%d].mode %q invalid", i, ic.Mode)
		}
		if !allowedBillings[ic.Billing] {
			return f, fmt.Errorf(
				"incompatibilities[%d].billing %q invalid (allowed: api_key, subscription)",
				i, ic.Billing)
		}
	}
	return f, nil
}
