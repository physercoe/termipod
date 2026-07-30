package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// Blob storage is content-addressed: sha256 is the primary key, the bytes
// live on disk at <dataRoot>/blobs/<aa>/<bb>/<sha256>. Same hash = dedup.

const maxBlobBytes = 25 * 1024 * 1024 // 25 MiB per blob (plan §14)

// blobTSFormat is the layout for `expires_at`: the zero-padded variant the other
// sweeps use, NOT NowUTC's RFC3339Nano.
//
// Padding is load-bearing here in a way it is not elsewhere. RFC3339Nano trims
// trailing zeros from the fraction, and two such strings do not compare
// correctly as text ("…00.5Z" sorts after "…00.5001Z", because 'Z' > '0') — and
// this column is compared as text twice: against the sweeper's cutoff, and
// against itself by the MAX() in D-5's collision rule. A fixed-width fraction
// makes lexical order agree with chronological order.
const blobTSFormat = "2006-01-02T15:04:05.000000000Z07:00"

func (s *Server) blobPath(sha string) string {
	return filepath.Join(s.cfg.DataRoot, "blobs", sha[:2], sha[2:4], sha)
}

// blobClass is a blob's declared lifetime (ADR-061 D-1). Declared by the writer,
// never inferred from size, mime or endpoint.
type blobClass string

const (
	// blobOwned is permanent: some other row is the referrer. Exactly today's
	// semantics, which is why it is the column default.
	blobOwned blobClass = "owned"
	// blobDerived is a cache entry — reproducible from inputs the hub still has.
	// Requires a TTL.
	blobDerived blobClass = "derived"
)

// blobMaxTTL is the ceiling on a derived blob's lifetime, operator-tunable via
// HUB_BLOB_MAX_TTL (a Go duration). Seven days is the right order for an export
// cache: long enough that re-opening yesterday's episode is free, short enough
// that a week of browsing does not become permanent.
func blobMaxTTL() time.Duration {
	d := 7 * 24 * time.Hour
	if v := os.Getenv("HUB_BLOB_MAX_TTL"); v != "" {
		if p, err := time.ParseDuration(v); err == nil && p > 0 {
			d = p
		}
	}
	return d
}

// blobLockStripes is the width of the lock array below. A collision between two
// unrelated shas costs a little serialization and nothing else, so this only has
// to be wide enough that the common case does not contend.
const blobLockStripes = 64

// lockBlob serializes the (file, row) pair for one content address, and returns
// the unlock.
//
// This exists for one specific race (ADR-061 D-3). storeBlob is stat-file →
// skip-write → upsert-row; the sweeper is delete-row → unlink-file. A re-upload
// of identical bytes concurrent with the sweep of that same sha — which is
// exactly the re-export-after-TTL path this feature creates — can see the file,
// skip the write, insert its row, and then have the sweeper unlink the bytes
// underneath it: a row promising bytes that are gone, which reads as corruption
// rather than expiry.
//
// A grace window does not close it and neither does re-stat-after-upsert: the
// sweeper's unlink can always land after the writer's last look. Striping by the
// content address itself is free — the sha is already a uniform hash.
func (s *Server) lockBlob(sha string) func() {
	var stripe uint8
	if len(sha) >= 2 {
		if b, err := hex.DecodeString(sha[:2]); err == nil && len(b) == 1 {
			stripe = b[0]
		}
	}
	m := &s.blobLocks[int(stripe)%blobLockStripes]
	m.Lock()
	return m.Unlock
}

// storeBlob content-addresses body into the blob store: writes the bytes to
// <dataRoot>/blobs/<aa>/<bb>/<sha> (skipped if already present — same hash =
// same bytes) and records the row. Returns the sha256.
//
// class and ttl are the writer's declaration (ADR-061 D-2). A `derived` blob
// must carry a positive ttl; `owned` ignores it.
//
// On a content-address collision the longest lifetime wins (D-5), which is the
// part of this that is easy to get wrong: two features can legitimately produce
// byte-identical content, and without the rule an event payload that happens to
// match a cached export would be swept when the export's TTL lapsed — data loss
// presenting as corruption.
func (s *Server) storeBlob(ctx context.Context, body []byte, mime string, class blobClass, ttl time.Duration) (string, error) {
	if mime == "" {
		mime = "application/octet-stream"
	}
	switch class {
	case blobOwned:
		ttl = 0
	case blobDerived:
		if ttl <= 0 {
			return "", errors.New("a derived blob requires a positive ttl")
		}
		if max := blobMaxTTL(); ttl > max {
			return "", fmt.Errorf("ttl %s exceeds the %s ceiling", ttl, max)
		}
	default:
		return "", fmt.Errorf("unknown blob class %q", class)
	}

	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	path := s.blobPath(sha)

	unlock := s.lockBlob(sha)
	defer unlock()

	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			return "", err
		}
	}

	var expires any
	if class == blobDerived {
		expires = time.Now().UTC().Add(ttl).Format(blobTSFormat)
	}
	// The CASE arms are D-5, in SQL:
	//   owned  × anything → owned, no expiry (never demote)
	//   derived × derived → the later expiry wins
	// A NULL expires_at on either side of the derived branch cannot happen (the
	// validation above forbids it), but if it ever did it would mean "no expiry",
	// so it wins — the safe direction is always to keep the bytes.
	if _, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO blobs (sha256, scope_path, size, mime, created_at, class, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(sha256) DO UPDATE SET
			class = CASE
				WHEN blobs.class = 'owned' OR excluded.class = 'owned' THEN 'owned'
				ELSE 'derived' END,
			expires_at = CASE
				WHEN blobs.class = 'owned' OR excluded.class = 'owned' THEN NULL
				WHEN blobs.expires_at IS NULL OR excluded.expires_at IS NULL THEN NULL
				ELSE MAX(blobs.expires_at, excluded.expires_at) END`,
		sha, path, len(body), mime, NowUTC(), string(class), expires); err != nil {
		return "", err
	}
	return sha, nil
}

// storeOwnedBlob is the permanent-blob shorthand — the semantics every writer
// had before ADR-061.
func (s *Server) storeOwnedBlob(ctx context.Context, body []byte, mime string) (string, error) {
	return s.storeBlob(ctx, body, mime, blobOwned, 0)
}

func (s *Server) handleUploadBlob(w http.ResponseWriter, r *http.Request) {
	mime := r.Header.Get("Content-Type")
	if mime == "" {
		mime = "application/octet-stream"
	}
	// Absent `class` means owned, so every existing caller is unchanged (D-2).
	class := blobOwned
	var ttl time.Duration
	switch q := r.URL.Query().Get("class"); q {
	case "", string(blobOwned):
	case string(blobDerived):
		class = blobDerived
		raw := r.URL.Query().Get("ttl_seconds")
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			// A derived upload with no TTL is a 400, not an implicit forever:
			// silently granting eternity to something declared disposable is the
			// exact failure the class was added to prevent.
			writeErr(w, http.StatusBadRequest, "class=derived requires a positive ttl_seconds")
			return
		}
		ttl = time.Duration(n) * time.Second
		if max := blobMaxTTL(); ttl > max {
			writeErr(w, http.StatusBadRequest,
				fmt.Sprintf("ttl_seconds exceeds the %s ceiling", max))
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "class must be owned or derived")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBlobBytes))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, err.Error())
		return
	}
	// Every caller-supplied value was validated above, so anything storeBlob
	// returns now is the store's problem — disk, or the database — and must not
	// be reported as a 400.
	sha, err := s.storeBlob(r.Context(), body, mime, class, ttl)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"sha256": sha,
		"size":   len(body),
		"mime":   mime,
		"class":  string(class),
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
		s.writeDBErr(w, err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", mime)
	http.ServeContent(w, r, sha, time.Time{}, f)
}
