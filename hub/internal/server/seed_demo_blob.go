// Package server — content-addressed blob helper shared by every
// seed-demo path (lifecycle today; other shapes if/when they land).
//
// Historic note: this file is what's left of `seed_demo.go` after the
// legacy `--shape ablation` seed was retired in v1.0.507
// (plans/multi-run-experiment-phase.md, W4). The lifecycle seed reuses
// `insertDemoBlob` for every artifact body it writes to disk, so we
// keep the helper in the package even though its original caller is
// gone.
package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

// insertDemoBlob writes bytes to the content-addressed blob store rooted
// at dataRoot (same layout the real POST /v1/blobs handler uses) and
// upserts the blobs table row. Safe to call multiple times with the
// same bytes — disk write is skipped when the file already exists and
// the INSERT is `OR IGNORE`.
func insertDemoBlob(ctx context.Context, tx *sql.Tx, dataRoot string, data []byte, mime string, now string) (string, error) {
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	path := filepath.Join(dataRoot, "blobs", sha[:2], sha[2:4], sha)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return "", err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO blobs (sha256, scope_path, size, mime, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		sha, path, len(data), mime, now); err != nil {
		return "", err
	}
	return sha, nil
}
