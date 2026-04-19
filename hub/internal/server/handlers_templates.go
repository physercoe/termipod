package server

import (
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
	path := filepath.Join(s.cfg.DataRoot, "team", "templates", cat, name)
	// Defence in depth: ensure resolved path stays under the templates dir.
	base := filepath.Join(s.cfg.DataRoot, "team", "templates")
	abs, err := filepath.Abs(path)
	if err != nil || !strings.HasPrefix(abs, base+string(os.PathSeparator)) {
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
