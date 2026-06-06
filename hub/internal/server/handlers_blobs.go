package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
)

// Blob storage is content-addressed: sha256 is the primary key, the bytes
// live on disk at <dataRoot>/blobs/<aa>/<bb>/<sha256>. Same hash = dedup.

const maxBlobBytes = 25 * 1024 * 1024 // 25 MiB per blob (plan §14)

func (s *Server) blobPath(sha string) string {
	return filepath.Join(s.cfg.DataRoot, "blobs", sha[:2], sha[2:4], sha)
}

// storeBlob content-addresses body into the blob store: writes the bytes to
// <dataRoot>/blobs/<aa>/<bb>/<sha> (skipped if already present — same hash =
// same bytes) and records the row (INSERT OR IGNORE). Returns the sha256.
// Shared by the upload endpoint and the agent-event payload externalizer
// (payload_externalize.go).
func (s *Server) storeBlob(ctx context.Context, body []byte, mime string) (string, error) {
	if mime == "" {
		mime = "application/octet-stream"
	}
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	path := s.blobPath(sha)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			return "", err
		}
	}
	if _, err := s.writeDB.ExecContext(ctx, `
		INSERT OR IGNORE INTO blobs (sha256, scope_path, size, mime, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		sha, path, len(body), mime, NowUTC()); err != nil {
		return "", err
	}
	return sha, nil
}

func (s *Server) handleUploadBlob(w http.ResponseWriter, r *http.Request) {
	mime := r.Header.Get("Content-Type")
	if mime == "" {
		mime = "application/octet-stream"
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBlobBytes))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, err.Error())
		return
	}
	sha, err := s.storeBlob(r.Context(), body, mime)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"sha256": sha,
		"size":   len(body),
		"mime":   mime,
	})
}

func (s *Server) handleGetBlob(w http.ResponseWriter, r *http.Request) {
	sha := chi.URLParam(r, "sha")
	var path, mime string
	var size int64
	err := s.db.QueryRowContext(r.Context(),
		`SELECT scope_path, size, mime FROM blobs WHERE sha256 = ?`, sha).
		Scan(&path, &size, &mime)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "blob not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", mime)
	http.ServeContent(w, r, sha, time.Time{}, f)
}
