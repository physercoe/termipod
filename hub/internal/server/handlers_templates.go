package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Templates live on disk at <dataRoot>/team/templates/{agents,prompts,policies}/*.
// They're seeded from the embedded FS on first init (see init.go) and then
// owned by the user — this handler reads from disk so edits take effect
// without restarting.

type templateOut struct {
	Category string `json:"category"` // agents | prompts | policies
	Name     string `json:"name"`     // e.g. "steward.v1.yaml"
	Path     string `json:"path"`     // relative to team/templates
	Size     int64  `json:"size"`
	ModTime  string `json:"mod_time"`
}

var templateCategories = []string{"agents", "prompts", "policies"}

func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	base := filepath.Join(s.cfg.DataRoot, "team", "templates")
	out := []templateOut{}
	for _, cat := range templateCategories {
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
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, "template not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", mimeForTemplate(name))
	_, _ = w.Write(body)
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

func validCategory(c string) bool {
	for _, x := range templateCategories {
		if x == c {
			return true
		}
	}
	return false
}

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
