package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
)

// errNoDocsRoot signals that the project serves no filesystem docs at all
// — either docs_root is unset (the default for most projects) or it fails
// the F-07 containment check. It wraps os.ErrNotExist so the REST callers
// keep mapping it to 404, while get_project_doc can tell this apart from
// "the file you named is missing" and steer the agent to documents.get.
var errNoDocsRoot = fmt.Errorf("project has no docs_root configured: %w", os.ErrNotExist)

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
		return "", errNoDocsRoot
	}
	root, ok := s.boundDocsRoot(docsRoot.String)
	if !ok {
		// F-07 backstop: a docs_root that escapes the hub data root is
		// refused at read time too, so a legacy row (or any future write
		// path that bypassed validation) can't turn the project-doc
		// reader into an arbitrary-file oracle under the hub UID.
		// Treated as "no docs" — fail closed, no info leak — with a
		// warning so the operator can spot the misconfiguration.
		s.log.Warn("docs_root escapes hub data root; refusing to serve",
			"team", team, "project", project, "docs_root", docsRoot.String)
		return "", errNoDocsRoot
	}
	return root, nil
}

// boundDocsRoot expands (~/, relative) and cleans a raw docs_root value
// and confirms the result stays within the hub data root. Returns
// (cleanPath, true) when safe; ("", false) when it escapes (F-07).
// Shared by resolveDocsRoot (the read-time backstop) and the project
// create handler (reject at the door). Project docs live under the hub
// data root; pointing docs_root outside it is what made it a file-read
// oracle. (A configurable allowlist of additional bases could relax
// this later if external docs dirs are ever needed.)
func (s *Server) boundDocsRoot(raw string) (string, bool) {
	root := raw
	// Expand ~ to the user's home so a ~/-relative value still resolves;
	// it must still land within the data root to be served.
	if strings.HasPrefix(root, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			root = filepath.Join(home, root[2:])
		}
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(s.cfg.DataRoot, root)
	}
	root = filepath.Clean(root)
	dataRoot := filepath.Clean(s.cfg.DataRoot)
	if root != dataRoot && !strings.HasPrefix(root, dataRoot+string(os.PathSeparator)) {
		return "", false
	}
	return root, true
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
		s.writeDBErr(w, err)
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
		s.writeDBErr(w, err)
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
		// get_project_doc reads files on disk under the project's
		// docs_root; documents_get reads rows from the documents
		// table. A ULID passed here is the classic confusion (the
		// 2026-05-18 steward incident) — name the sibling tool.
		writeErrHint(w, http.StatusNotFound, "doc not found", Hint{
			HintText: "No file at this path under the project's docs_root. " +
				"If you have a document id (a 26-char ULID), it is not a " +
				"filesystem path — fetch it with documents_get instead.",
			SeeTool: "documents_get",
		})
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
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
