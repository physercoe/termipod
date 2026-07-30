package server

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"time"
)

// blob_sweep.go — expiry-only deletion for the blob store (ADR-061 D-3).
//
// Deletion happens here and nowhere else. There is deliberately no
// `DELETE /v1/blobs/{sha}`, and that is a safety property rather than an
// omission: the store is deduped by content address, so a caller deleting
// "their" blob may be deleting the only copy of bytes another row owns — an
// uploaded image and an exported artifact that happen to be byte-identical are
// one row. Expiry-only deletion makes that class of accident unreachable.
//
// Read semantics, stated so callers do not invent them: expiry marks sweep
// eligibility, not read refusal. An expired-but-not-yet-swept `derived` blob
// still serves on GET; a caller sees 404 only once this has actually run.
// Refusing reads at expires_at would buy nothing — the bytes are still there —
// and would turn sweep cadence into user-visible behaviour.

// blobSweepBatch bounds one pass. A pass takes a per-sha lock and does a
// filesystem unlink per blob, so an unbounded batch after a long outage would
// hold the writer pool and the striped locks for as long as the backlog takes.
// The next tick continues; nothing is lost by draining slowly.
const blobSweepBatch = 500

// blobSweepInterval is the loop cadence, operator-tunable via
// HUB_BLOB_SWEEP_INTERVAL (a Go duration). Five minutes matches the store
// maintenance loop this is patterned on: a TTL is measured in hours, so the
// cadence only has to be small relative to that.
func blobSweepInterval() time.Duration {
	d := 5 * time.Minute
	if v := os.Getenv("HUB_BLOB_SWEEP_INTERVAL"); v != "" {
		if p, err := time.ParseDuration(v); err == nil && p > 0 {
			d = p
		}
	}
	return d
}

// runBlobSweep loops until ctx is cancelled. Started from Start() unless
// HUB_BLOB_SWEEP_DISABLE is set — the same shape as runStoreMaintenance
// (ADR-045 D4), rather than a new scheduler.
func (s *Server) runBlobSweep(ctx context.Context) {
	t := time.NewTicker(blobSweepInterval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweepExpiredBlobsOnce(ctx)
		}
	}
}

// sweepExpiredBlobsOnce deletes one batch of expired derived blobs and returns
// how many it removed.
//
// Three constraints, each from code that already exists:
//
//   - The file to unlink is `blobPath(sha)`, RECOMPUTED — never the row's stored
//     `scope_path`, which is an absolute path captured at write time and would
//     name something else if DataRoot ever moved. (Reads trust that column
//     today; that is a separate pre-existing fragility, not one to widen here.)
//   - Delete the ROW FIRST, then the file, and tolerate a missing file. The
//     reverse order leaves a row promising bytes that are gone, which reads as
//     corruption; this order leaves at worst an orphan file, which the next write
//     of the same content silently reuses.
//   - Hold the per-sha lock across both. Without it a concurrent re-upload of
//     identical bytes can see the file, skip the write, insert its row, and then
//     lose the bytes to this unlink.
//
// The DELETE re-checks the expiry predicate rather than trusting the id read a
// moment ago, so a blob whose lifetime was extended between the scan and the
// delete (D-5's collision rule refreshing it, or a promotion to `owned`) is left
// alone — and, because the unlink is gated on that DELETE having matched, its
// bytes are left alone too.
func (s *Server) sweepExpiredBlobsOnce(ctx context.Context) int {
	cutoff := time.Now().UTC().Format(blobTSFormat)
	rows, err := s.db.QueryContext(ctx, `
		SELECT sha256 FROM blobs
		 WHERE expires_at IS NOT NULL AND expires_at < ? AND class = 'derived'
		 ORDER BY expires_at
		 LIMIT ?`, cutoff, blobSweepBatch)
	if err != nil {
		s.log.Warn("blob sweep: scan failed", "err", err)
		return 0
	}
	shas := []string{}
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			rows.Close()
			s.log.Warn("blob sweep: scan failed", "err", err)
			return 0
		}
		shas = append(shas, sha)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		s.log.Warn("blob sweep: scan failed", "err", err)
		return 0
	}

	deleted := 0
	for _, sha := range shas {
		select {
		case <-ctx.Done():
			return deleted
		default:
		}
		if s.sweepOneBlob(ctx, sha, cutoff) {
			deleted++
		}
	}
	if deleted > 0 {
		s.log.Info("blob sweep: removed expired derived blobs", "count", deleted)
	}
	return deleted
}

// sweepOneBlob removes one blob under its content-address lock. Returns whether
// the row was actually deleted.
func (s *Server) sweepOneBlob(ctx context.Context, sha, cutoff string) bool {
	unlock := s.lockBlob(sha)
	defer unlock()

	res, err := s.writeDB.ExecContext(ctx, `
		DELETE FROM blobs
		 WHERE sha256 = ? AND class = 'derived'
		   AND expires_at IS NOT NULL AND expires_at < ?`, sha, cutoff)
	if err != nil {
		s.log.Warn("blob sweep: delete row failed", "sha", sha, "err", err)
		return false
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		// The blob was promoted or its expiry extended between the scan and now.
		// Not deleting the file is the whole point of gating on this.
		return false
	}
	if err := os.Remove(s.blobPath(sha)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		// The row is already gone, so the blob is expired either way; an orphan
		// file is the benign residue this ordering was chosen to prefer, and the
		// next write of the same content reuses it.
		s.log.Warn("blob sweep: unlink failed; leaving an orphan file",
			"sha", sha, "err", err)
	}
	return true
}
