package server

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	hub "github.com/termipod/hub"
	"github.com/termipod/hub/internal/auth"
	"gopkg.in/yaml.v3"
)

// Templates live on disk at <dataRoot>/team/templates/<category>/*.
// They're seeded from the embedded FS on first init (see init.go) and
// then owned by the user — this handler reads from disk so edits take
// effect without restarting.
//
// Category set is discovered, not hardcoded. The hub ships a set of
// built-in categories under hub.TemplatesFS (agents, prompts, policies,
// projects, …) and the user can extend that on disk. Listing scans
// both; mutations accept any safe-name category and create the
// directory on first PUT. That way a future "tools/" or "schedules/"
// category is a YAML drop, not a Go change.

type templateOut struct {
	Category string `json:"category"` // e.g. "agents", "prompts", "projects"
	Name     string `json:"name"`     // e.g. "steward.v1.yaml"
	Path     string `json:"path"`     // relative to team/templates
	Size     int64  `json:"size"`
	ModTime  string `json:"mod_time"`
}

func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	base := filepath.Join(s.cfg.DataRoot, "team", "templates")
	cats := discoverTemplateCategories(base)
	// Optional ?category= filter — the MCP tool wrappers
	// (templates.agent.list, templates.prompt.list, …) use this to
	// scope their response. Unknown / empty category falls through
	// to the full union.
	if want := r.URL.Query().Get("category"); want != "" {
		filtered := cats[:0:0]
		for _, c := range cats {
			if c == want {
				filtered = append(filtered, c)
				break
			}
		}
		cats = filtered
	}
	out := []templateOut{}
	for _, cat := range cats {
		dir := filepath.Join(base, cat)
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, templateOut{
				Category: cat,
				Name:     e.Name(),
				Path:     filepath.Join(cat, e.Name()),
				Size:     info.Size(),
				ModTime:  info.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00"),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	writeJSON(w, http.StatusOK, out)
}

// discoverTemplateCategories returns the union of category directories
// found on disk under <base> and inside the embedded TemplatesFS. We
// merge the two so a fresh install (no disk dir yet) still reports the
// built-in categories, and a user who creates a new category by PUTting
// the first file gets it picked up automatically. Hidden dotfiles and
// non-directories are skipped to keep the wire output sane.
func discoverTemplateCategories(base string) []string {
	seen := map[string]struct{}{}
	add := func(name string) {
		if !safeCategoryName(name) {
			return
		}
		seen[name] = struct{}{}
	}
	if entries, err := os.ReadDir(base); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			add(e.Name())
		}
	}
	if entries, err := fs.ReadDir(hub.TemplatesFS, "templates"); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			add(e.Name())
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *Server) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	cat := chi.URLParam(r, "category")
	name := chi.URLParam(r, "name")
	if !validCategory(cat) || !safeTemplateName(name) {
		writeErr(w, http.StatusBadRequest, "invalid category or name")
		return
	}
	path, ok := resolveTemplatePath(s.cfg.DataRoot, cat, name)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	body, err := os.ReadFile(path)
	diskMissing := false
	if err != nil {
		if !os.IsNotExist(err) {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Disk overlay missing — fall back to the embedded built-in so
		// callers that depend on bundled templates (most importantly the
		// mobile spawn-steward sheet) keep working when the team data
		// root has drifted (fresh install before init copied the files,
		// data root manually wiped, etc.). 404 only when the name is
		// neither on disk nor in the embedded FS.
		body, err = fs.ReadFile(hub.TemplatesFS, "templates/"+cat+"/"+name)
		if err != nil {
			writeErr(w, http.StatusNotFound, "template not found")
			return
		}
		diskMissing = true
	}
	// merge=1 overlays the disk file onto the embedded built-in so a
	// stale on-disk template (e.g. seeded by an older hub before
	// backend.cmd was added) inherits any keys the user hasn't
	// explicitly set. Skipped when disk is missing (we already serve
	// the embedded copy verbatim — no merge needed and it preserves
	// comments) and when the format isn't a structured map.
	if !diskMissing && r.URL.Query().Get("merge") == "1" {
		if merged, ok := mergeTemplateBody(body, cat, name); ok {
			body = merged
		}
	}
	w.Header().Set("Content-Type", mimeForTemplate(name))
	_, _ = w.Write(body)
}

// mergeTemplateBody overlays the on-disk template onto the embedded
// built-in: embedded acts as the base, disk fills in keys it explicitly
// sets (including empty values). Missing keys in disk fall through to
// embedded. Lists are replaced wholesale — merging arrays is ambiguous
// (concat? union? element-wise?), and a user who sets a list intends
// the final value.
//
// Returns ok=false when there's no embedded built-in for this template,
// when the format isn't YAML/JSON, or when parsing fails. In all those
// cases the caller should fall back to the disk body as-is.
//
// Comments are not preserved through the parse-marshal cycle. The
// editor path doesn't pass merge=1 so user comments survive there;
// merge=1 is for spawn callers that need a complete spec, not for UI
// display.
func mergeTemplateBody(disk []byte, cat, name string) ([]byte, bool) {
	embedded, err := fs.ReadFile(hub.TemplatesFS, "templates/"+cat+"/"+name)
	if err != nil {
		return nil, false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml":
		var diskDoc, embDoc map[string]any
		if err := yaml.Unmarshal(disk, &diskDoc); err != nil || diskDoc == nil {
			return nil, false
		}
		if err := yaml.Unmarshal(embedded, &embDoc); err != nil || embDoc == nil {
			return nil, false
		}
		merged := deepMergeMap(embDoc, diskDoc)
		out, err := yaml.Marshal(merged)
		if err != nil {
			return nil, false
		}
		return out, true
	case ".json":
		var diskDoc, embDoc map[string]any
		if err := json.Unmarshal(disk, &diskDoc); err != nil || diskDoc == nil {
			return nil, false
		}
		if err := json.Unmarshal(embedded, &embDoc); err != nil || embDoc == nil {
			return nil, false
		}
		merged := deepMergeMap(embDoc, diskDoc)
		out, err := json.MarshalIndent(merged, "", "  ")
		if err != nil {
			return nil, false
		}
		return out, true
	default:
		return nil, false
	}
}

// deepMergeMap returns a new map where overlay's keys win over base's,
// recursively merging when both sides hold a map at the same key.
// Non-map collisions (scalars, lists) are replaced wholesale by overlay.
func deepMergeMap(base, overlay map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		if existing, ok := out[k]; ok {
			if bm, bok := existing.(map[string]any); bok {
				if om, ook := v.(map[string]any); ook {
					out[k] = deepMergeMap(bm, om)
					continue
				}
			}
		}
		out[k] = v
	}
	return out
}

// handlePutTemplate writes or overwrites a template file. Body is the raw
// file content (yaml/markdown/json — same MIME we hand back on GET). Use
// PUT for both "create new" and "edit existing": idempotent, no special
// case for missing files. The mobile editor reads → patches → puts.
func (s *Server) handlePutTemplate(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	cat := chi.URLParam(r, "category")
	name := chi.URLParam(r, "name")
	if !validCategory(cat) || !safeTemplateName(name) {
		writeErr(w, http.StatusBadRequest, "invalid category or name")
		return
	}
	if denied := s.checkTemplateSelfModification(r, cat, name); denied != "" {
		writeErr(w, http.StatusForbidden, denied)
		return
	}
	path, ok := resolveTemplatePath(s.cfg.DataRoot, cat, name)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	created := false
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			created = true
		}
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	action := "template.updated"
	if created {
		action = "template.created"
	}
	s.recordAudit(r.Context(), team, action, "template",
		filepath.Join(cat, name), action+" "+filepath.Join(cat, name),
		map[string]any{"category": cat, "name": name, "size": len(body)},
	)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{
		"category": cat, "name": name, "size": len(body),
	})
}

// handleDeleteTemplate removes a template file. The bundled defaults live
// in the embedded FS, so deleting a disk file falls back to the built-in
// on next list/get — there's no "restore" endpoint because re-init does
// the same thing. Refuses to delete a non-existent file (404) so the UI
// can distinguish "already gone" from "never existed".
func (s *Server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	cat := chi.URLParam(r, "category")
	name := chi.URLParam(r, "name")
	if !validCategory(cat) || !safeTemplateName(name) {
		writeErr(w, http.StatusBadRequest, "invalid category or name")
		return
	}
	if denied := s.checkTemplateSelfModification(r, cat, name); denied != "" {
		writeErr(w, http.StatusForbidden, denied)
		return
	}
	path, ok := resolveTemplatePath(s.cfg.DataRoot, cat, name)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "template not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "template.deleted", "template",
		filepath.Join(cat, name), "delete "+filepath.Join(cat, name),
		map[string]any{"category": cat, "name": name},
	)
	w.WriteHeader(http.StatusNoContent)
}

// handleRenameTemplate moves a template within its category. Renames
// across categories are intentionally rejected — categories carry meaning
// (agents vs. prompts vs. policies) and the resolver paths differ. If
// the destination already exists we 409 rather than silently
// overwriting; the caller can DELETE then PUT if they really want that.
func (s *Server) handleRenameTemplate(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	cat := chi.URLParam(r, "category")
	name := chi.URLParam(r, "name")
	if !validCategory(cat) || !safeTemplateName(name) {
		writeErr(w, http.StatusBadRequest, "invalid category or name")
		return
	}
	var body struct {
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).
		Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	if !safeTemplateName(body.NewName) {
		writeErr(w, http.StatusBadRequest, "invalid new_name")
		return
	}
	if body.NewName == name {
		writeJSON(w, http.StatusOK, map[string]any{
			"category": cat, "name": name,
		})
		return
	}
	srcPath, ok := resolveTemplatePath(s.cfg.DataRoot, cat, name)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	dstPath, ok := resolveTemplatePath(s.cfg.DataRoot, cat, body.NewName)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	if _, err := os.Stat(srcPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "template not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := os.Stat(dstPath); err == nil {
		writeErr(w, http.StatusConflict, "destination exists")
		return
	}
	if err := os.Rename(srcPath, dstPath); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "template.renamed", "template",
		filepath.Join(cat, body.NewName),
		"rename "+filepath.Join(cat, name)+" → "+filepath.Join(cat, body.NewName),
		map[string]any{"category": cat, "old_name": name, "new_name": body.NewName},
	)
	writeJSON(w, http.StatusOK, map[string]any{
		"category": cat, "name": body.NewName,
	})
}

// resolveTemplatePath joins dataRoot + category + name and re-checks the
// absolute path stays under team/templates so a name like "..%2Fpasswd"
// can't escape even if validation upstream slips. Mirrors the defence-
// in-depth check already in handleGetTemplate.
func resolveTemplatePath(dataRoot, cat, name string) (string, bool) {
	base := filepath.Join(dataRoot, "team", "templates")
	path := filepath.Join(base, cat, name)
	abs, err := filepath.Abs(path)
	if err != nil || !strings.HasPrefix(abs, base+string(os.PathSeparator)) {
		return "", false
	}
	return path, true
}

// safeCategoryName accepts any non-hidden, separator-free identifier.
// Categories are directories under team/templates, so the same path
// hygiene rules as template names apply: no traversal, no path
// separators, no dotfiles. Adding a new category is a PUT into a name
// that passes this check; the directory is created lazily.
func safeCategoryName(c string) bool { return safeTemplateName(c) }

// validCategory is the legacy alias used by handlers; kept as a thin
// wrapper so reading the call sites still tells you "this is a category
// name". Identical to safeCategoryName today; if we ever reintroduce a
// closed list (e.g. for billing-restricted categories) the guard lives
// here.
func validCategory(c string) bool { return safeCategoryName(c) }

// safeTemplateName rejects path separators, parent refs, and hidden files.
func safeTemplateName(n string) bool {
	if n == "" || strings.ContainsAny(n, `/\`) || strings.HasPrefix(n, ".") {
		return false
	}
	if n == "." || n == ".." {
		return false
	}
	return true
}

func mimeForTemplate(name string) string {
	switch filepath.Ext(name) {
	case ".md":
		return "text/markdown; charset=utf-8"
	case ".yaml", ".yml":
		return "application/yaml; charset=utf-8"
	case ".json":
		return "application/json"
	default:
		return "text/plain; charset=utf-8"
	}
}

// checkTemplateSelfModification enforces ADR-016 D7: an agent cannot
// edit a template whose kind matches its own kind. Returns "" to
// allow; a non-empty error message to deny (caller writes 403).
//
// Only relevant for `agents` and `prompts` categories — those are the
// per-kind templates. `plans` and `projects` aren't keyed on agent
// kind, so the guard is a no-op there.
//
// Non-agent callers (principal, host token) bypass: the director can
// always edit any template via the mobile editor.
func (s *Server) checkTemplateSelfModification(r *http.Request, cat, name string) string {
	if cat != "agents" && cat != "prompts" {
		return ""
	}
	tok, ok := auth.FromContext(r.Context())
	if !ok || tok == nil || tok.Kind != "agent" {
		return ""
	}
	var scope struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(tok.ScopeJSON), &scope); err != nil || scope.AgentID == "" {
		return ""
	}
	var kind string
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT kind FROM agents WHERE id = ?`, scope.AgentID).Scan(&kind); err != nil || kind == "" {
		return ""
	}
	// Strip extension (.yaml / .md / .yml / .json).
	base := name
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	if base == kind {
		return "self-modification guard (ADR-016 D7): agent kind=" + kind + " cannot edit its own template " + cat + "/" + name
	}
	return ""
}
