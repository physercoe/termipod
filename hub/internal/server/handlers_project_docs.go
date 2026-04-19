package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Project docs are the shared, human-authored context for a project
// (plan §10A "shared tier"). Agents pull them lazily via the MCP tool
// get_project_doc(path); this is the HTTP surface behind that.
//
// The project row carries docs_root, which is resolved relative to the hub
// data root if not absolute. Requests outside that root are rejected.

type docEntry struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
	IsDir   bool   `json:"is_dir,omitempty"`
}

func (s *Server) resolveDocsRoot(ctx context.Context, team, project string) (string, error) {
	var docsRoot sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT docs_root FROM projects WHERE team_id = ? AND id = ?`, team, project).
		Scan(&docsRoot)
	if err != nil {
		return "", err
	}
	if !docsRoot.Valid || docsRoot.String == "" {
		return "", os.ErrNotExist
	}
	root := docsRoot.String
	// Expand ~ to the user's home so templates can use ~/docs/proj-x.
	if strings.HasPrefix(root, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			root = filepath.Join(home, root[2:])
		}
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(s.cfg.DataRoot, root)
	}
	return filepath.Clean(root), nil
}

func (s *Server) handleListProjectDocs(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	root, err := s.resolveDocsRoot(r.Context(), team, project)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusOK, []docEntry{})
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := []docEntry{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, docEntry{
			Path:    rel,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00"),
			IsDir:   d.IsDir(),
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetProjectDoc(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	rel := chi.URLParam(r, "*") // catch-all after /docs/

	root, err := s.resolveDocsRoot(r.Context(), team, project)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if errors.Is(err, os.ErrNotExist) {
		writeErr(w, http.StatusNotFound, "project has no docs_root")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	target := filepath.Join(root, filepath.Clean("/"+rel))
	// Containment check: resolved target must remain inside root.
	if !strings.HasPrefix(target+string(os.PathSeparator), root+string(os.PathSeparator)) &&
		target != root {
		writeErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	body, err := os.ReadFile(target)
	if errors.Is(err, os.ErrNotExist) {
		writeErr(w, http.StatusNotFound, "doc not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", mimeForDoc(target))
	_, _ = w.Write(body)
}

func mimeForDoc(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/yaml; charset=utf-8"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
