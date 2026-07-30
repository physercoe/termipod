package server

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// blob_sweep_test.go — ADR-061. The rules under test are the ones whose failure
// is silent: a demotion that makes permanent bytes disposable (D-5), a sweep that
// unlinks bytes a live row still names (D-3), and an expiry that starts refusing
// reads (D-3's read semantic).

func blobRow(t *testing.T, s *Server, sha string) (class string, expires *string, ok bool) {
	t.Helper()
	err := s.db.QueryRow(`SELECT class, expires_at FROM blobs WHERE sha256 = ?`, sha).
		Scan(&class, &expires)
	if err != nil {
		return "", nil, false
	}
	return class, expires, true
}

func mustStore(t *testing.T, s *Server, body string, class blobClass, ttl time.Duration) string {
	t.Helper()
	sha, err := s.storeBlob(context.Background(), []byte(body), "application/octet-stream", class, ttl)
	if err != nil {
		t.Fatalf("storeBlob(%s, %v): %v", body, class, err)
	}
	return sha
}

// ---------------------------------------------------------------------------
// D-1 / D-2 — the class is the writer's declaration
// ---------------------------------------------------------------------------

func TestStoreBlob_OwnedIsPermanentAndIsTheDefault(t *testing.T) {
	s, _ := newTestServer(t)
	sha := mustStore(t, s, "permanent bytes", blobOwned, 0)
	class, expires, ok := blobRow(t, s, sha)
	if !ok {
		t.Fatal("no row")
	}
	if class != "owned" || expires != nil {
		t.Fatalf("class=%q expires=%v, want owned with no expiry", class, expires)
	}
	// The shorthand every pre-ADR-061 writer keeps using.
	sha2, err := s.storeOwnedBlob(context.Background(), []byte("more bytes"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if c, e, _ := blobRow(t, s, sha2); c != "owned" || e != nil {
		t.Fatalf("storeOwnedBlob wrote class=%q expires=%v", c, e)
	}
}

// A derived blob with no TTL is refused, never granted an implicit forever —
// silently making something declared disposable permanent is the failure the
// class exists to prevent.
func TestStoreBlob_DerivedRequiresABoundedTTL(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	if _, err := s.storeBlob(ctx, []byte("x"), "", blobDerived, 0); err == nil {
		t.Fatal("a derived blob with no ttl was accepted")
	}
	if _, err := s.storeBlob(ctx, []byte("x"), "", blobDerived, -time.Hour); err == nil {
		t.Fatal("a derived blob with a negative ttl was accepted")
	}
	if _, err := s.storeBlob(ctx, []byte("x"), "", blobDerived, 100*365*24*time.Hour); err == nil {
		t.Fatal("a derived blob past the ttl ceiling was accepted")
	}
	if _, err := s.storeBlob(ctx, []byte("x"), "", blobClass("cacheable"), time.Hour); err == nil {
		t.Fatal("an unknown class was accepted")
	}
}

// The class must never be inferred from size or mime (D-2) — a big blob is still
// owned unless its writer said otherwise.
func TestStoreBlob_ClassIsNotInferredFromTheBytes(t *testing.T) {
	s, _ := newTestServer(t)
	big := make([]byte, 1<<20)
	sha, err := s.storeBlob(context.Background(), big, "video/mp4", blobOwned, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c, _, _ := blobRow(t, s, sha); c != "owned" {
		t.Fatalf("a 1 MiB video was classed %q without its writer saying so", c)
	}
}

// ---------------------------------------------------------------------------
// D-5 — longest lifetime wins on a content-address collision
// ---------------------------------------------------------------------------

// The data-loss bug this rule exists for: an event payload byte-identical to a
// cached export must not be swept when the export's TTL lapses.
func TestStoreBlob_OwnedIsNeverDemotedByADerivedReUpload(t *testing.T) {
	s, _ := newTestServer(t)
	const body = "bytes two features both produce"
	sha := mustStore(t, s, body, blobOwned, 0)
	again := mustStore(t, s, body, blobDerived, time.Minute)
	if again != sha {
		t.Fatal("dedup broke: the same bytes got two addresses")
	}
	class, expires, _ := blobRow(t, s, sha)
	if class != "owned" {
		t.Fatalf("class=%q — an owned blob was demoted to a cache entry", class)
	}
	if expires != nil {
		t.Fatalf("expires_at=%v — an owned blob acquired a deadline", *expires)
	}
}

func TestStoreBlob_DerivedIsPromotedByAnOwnedReUpload(t *testing.T) {
	s, _ := newTestServer(t)
	const body = "cached first, owned second"
	sha := mustStore(t, s, body, blobDerived, time.Minute)
	if c, e, _ := blobRow(t, s, sha); c != "derived" || e == nil {
		t.Fatalf("setup: class=%q expires=%v", c, e)
	}
	mustStore(t, s, body, blobOwned, 0)
	class, expires, _ := blobRow(t, s, sha)
	if class != "owned" {
		t.Fatalf("class=%q, want promotion to owned", class)
	}
	if expires != nil {
		t.Fatalf("expires_at=%v, want the deadline cleared on promotion", *expires)
	}
}

func TestStoreBlob_TwoDerivedUploadsKeepTheLaterExpiry(t *testing.T) {
	s, _ := newTestServer(t)
	const body = "cached twice"
	sha := mustStore(t, s, body, blobDerived, 48*time.Hour)
	_, longer, _ := blobRow(t, s, sha)
	if longer == nil {
		t.Fatal("no expiry after the first write")
	}

	// A shorter TTL must not shorten the lifetime.
	mustStore(t, s, body, blobDerived, time.Minute)
	_, after, _ := blobRow(t, s, sha)
	if after == nil || *after != *longer {
		t.Fatalf("expires_at moved from %v to %v on a shorter re-upload", *longer, after)
	}

	// A longer one must extend it.
	mustStore(t, s, body, blobDerived, 96*time.Hour)
	_, extended, _ := blobRow(t, s, sha)
	if extended == nil || *extended <= *longer {
		t.Fatalf("expires_at %v did not extend past %v", extended, *longer)
	}
}

// ---------------------------------------------------------------------------
// D-3 — the sweep
// ---------------------------------------------------------------------------

func TestSweepExpiredBlobs_RemovesExpiredDerivedRowsAndFiles(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()

	expired := mustStore(t, s, "stale cache", blobDerived, time.Hour)
	fresh := mustStore(t, s, "warm cache", blobDerived, time.Hour)
	owned := mustStore(t, s, "permanent", blobOwned, 0)
	// Back-date only the first one's deadline.
	if _, err := s.writeDB.Exec(`UPDATE blobs SET expires_at = ? WHERE sha256 = ?`,
		time.Now().UTC().Add(-time.Hour).Format(blobTSFormat), expired); err != nil {
		t.Fatal(err)
	}

	if n := s.sweepExpiredBlobsOnce(ctx); n != 1 {
		t.Fatalf("swept %d blobs, want exactly the expired one", n)
	}
	if _, _, ok := blobRow(t, s, expired); ok {
		t.Fatal("the expired row survived")
	}
	if _, err := os.Stat(s.blobPath(expired)); !os.IsNotExist(err) {
		t.Fatalf("the expired file survived: %v", err)
	}
	for _, keep := range []string{fresh, owned} {
		if _, _, ok := blobRow(t, s, keep); !ok {
			t.Fatalf("row %s was swept", keep)
		}
		if _, err := os.Stat(s.blobPath(keep)); err != nil {
			t.Fatalf("file for %s was unlinked: %v", keep, err)
		}
	}
}

// An owned blob with a stray expires_at (only reachable by hand-editing) must
// still be spared: `class='derived'` is part of the predicate precisely so the
// permanent population can never be swept by accident.
func TestSweepExpiredBlobs_NeverTouchesAnOwnedBlob(t *testing.T) {
	s, _ := newTestServer(t)
	sha := mustStore(t, s, "permanent with a stray deadline", blobOwned, 0)
	if _, err := s.writeDB.Exec(`UPDATE blobs SET expires_at = ? WHERE sha256 = ?`,
		time.Now().UTC().Add(-time.Hour).Format(blobTSFormat), sha); err != nil {
		t.Fatal(err)
	}
	if n := s.sweepExpiredBlobsOnce(context.Background()); n != 0 {
		t.Fatalf("swept %d owned blobs", n)
	}
	if _, _, ok := blobRow(t, s, sha); !ok {
		t.Fatal("an owned blob was swept")
	}
}

// The re-export-after-TTL path: a blob whose lifetime was refreshed between the
// scan and the delete must keep BOTH its row and its bytes. The conditional
// DELETE is what makes that true, and gating the unlink on it is what keeps the
// bytes.
func TestSweepOneBlob_ARefreshedExpiryStopsTheDeleteAndTheUnlink(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()

	// Baseline: still eligible at delete time, so it goes. Without this the
	// spared case below would pass against a sweeper that deletes nothing.
	doomed := mustStore(t, s, "genuinely expired", blobDerived, time.Minute)
	cutoffPastIt := time.Now().UTC().Add(2 * time.Minute).Format(blobTSFormat)
	if !s.sweepOneBlob(ctx, doomed, cutoffPastIt) {
		t.Fatal("an eligible blob was not swept")
	}
	if _, _, ok := blobRow(t, s, doomed); ok {
		t.Fatal("reported deleted but the row is still there")
	}

	// The real case: eligible when the scan listed it, refreshed by a concurrent
	// re-upload before the delete ran. Both the row and the bytes must survive.
	sha := mustStore(t, s, "re-exported just in time", blobDerived, time.Minute)
	scanCutoff := time.Now().UTC().Add(2 * time.Minute).Format(blobTSFormat)
	mustStore(t, s, "re-exported just in time", blobDerived, 48*time.Hour) // the refresh

	if s.sweepOneBlob(ctx, sha, scanCutoff) {
		t.Fatal("deleted a blob whose expiry had been extended")
	}
	if _, _, ok := blobRow(t, s, sha); !ok {
		t.Fatal("the refreshed row was deleted")
	}
	if _, err := os.Stat(s.blobPath(sha)); err != nil {
		t.Fatalf("the refreshed blob's bytes were unlinked: %v", err)
	}
}

// A promotion between scan and delete has to have the same effect as a refresh.
func TestSweepOneBlob_APromotionStopsTheDelete(t *testing.T) {
	s, _ := newTestServer(t)
	sha := mustStore(t, s, "promoted mid-sweep", blobDerived, time.Minute)
	cutoff := time.Now().UTC().Add(2 * time.Minute).Format(blobTSFormat)
	mustStore(t, s, "promoted mid-sweep", blobOwned, 0)

	if s.sweepOneBlob(context.Background(), sha, cutoff) {
		t.Fatal("deleted a blob that had been promoted to owned")
	}
	if c, _, ok := blobRow(t, s, sha); !ok || c != "owned" {
		t.Fatalf("row = %q ok=%v", c, ok)
	}
	if _, err := os.Stat(s.blobPath(sha)); err != nil {
		t.Fatalf("a promoted blob's bytes were unlinked: %v", err)
	}
}

// The class guard on the DELETE, not just on the scan.
//
// This test exists because removing `class='derived'` from sweepOneBlob's DELETE
// left every other test passing: the scan's own guard keeps owned blobs out of
// the candidate list, and a promoted blob is additionally protected by its NULL
// expires_at. Both are true and neither covers the case where sweepOneBlob is
// reached with an owned sha that still carries a deadline — which is the shape a
// promotion would take if a future change cleared the class without clearing the
// expiry. Two independent guards is the point; this one holds the second.
func TestSweepOneBlob_RefusesAnOwnedShaEvenWithAStaleDeadline(t *testing.T) {
	s, _ := newTestServer(t)
	sha := mustStore(t, s, "owned, with a deadline it should never have", blobOwned, 0)
	if _, err := s.writeDB.Exec(`UPDATE blobs SET expires_at = ? WHERE sha256 = ?`,
		time.Now().UTC().Add(-time.Hour).Format(blobTSFormat), sha); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().UTC().Format(blobTSFormat)

	if s.sweepOneBlob(context.Background(), sha, cutoff) {
		t.Fatal("swept an owned blob")
	}
	if _, _, ok := blobRow(t, s, sha); !ok {
		t.Fatal("an owned row was deleted")
	}
	if _, err := os.Stat(s.blobPath(sha)); err != nil {
		t.Fatalf("an owned blob's bytes were unlinked: %v", err)
	}
}

// A missing file is tolerated: the row is authoritative, and an already-unlinked
// blob is exactly the state a previous interrupted sweep leaves.
func TestSweepOneBlob_ToleratesAnAlreadyMissingFile(t *testing.T) {
	s, _ := newTestServer(t)
	sha := mustStore(t, s, "bytes gone early", blobDerived, time.Minute)
	if err := os.Remove(s.blobPath(sha)); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().UTC().Add(2 * time.Minute).Format(blobTSFormat)
	if !s.sweepOneBlob(context.Background(), sha, cutoff) {
		t.Fatal("a blob with no file was not swept")
	}
	if _, _, ok := blobRow(t, s, sha); ok {
		t.Fatal("the row survived")
	}
}

// Expiry marks sweep eligibility, not read refusal. Refusing at expires_at would
// buy nothing — the bytes are still there — and would make sweep cadence
// user-visible.
func TestGetBlob_AnExpiredButUnsweptBlobStillServes(t *testing.T) {
	s, token := newA2ATestServer(t)
	sha := mustStore(t, s, "expired but present", blobDerived, time.Minute)
	if _, err := s.writeDB.Exec(`UPDATE blobs SET expires_at = ? WHERE sha256 = ?`,
		time.Now().UTC().Add(-time.Hour).Format(blobTSFormat), sha); err != nil {
		t.Fatal(err)
	}

	code, body := doReq(t, s, token, http.MethodGet, "/v1/blobs/"+sha, nil)
	if code != http.StatusOK {
		t.Fatalf("GET an expired-but-unswept blob = %d, want 200", code)
	}
	if string(body) != "expired but present" {
		t.Fatalf("body = %q", body)
	}

	// Only after the sweep does it 404.
	s.sweepExpiredBlobsOnce(context.Background())
	if code, _ := doReq(t, s, token, http.MethodGet, "/v1/blobs/"+sha, nil); code != http.StatusNotFound {
		t.Fatalf("GET after the sweep = %d, want 404", code)
	}
}

// ---------------------------------------------------------------------------
// D-2 — the wire
// ---------------------------------------------------------------------------

func TestUploadBlob_ClassAndTTLComeFromTheQuery(t *testing.T) {
	s, token := newA2ATestServer(t)

	// No class: owned, exactly as before.
	code, _ := doReq(t, s, token, http.MethodPost, "/v1/blobs", nil)
	if code != http.StatusCreated {
		t.Fatalf("plain upload = %d", code)
	}

	// derived without a ttl is a 400, not an implicit forever.
	for _, q := range []string{
		"?class=derived",
		"?class=derived&ttl_seconds=0",
		"?class=derived&ttl_seconds=-5",
		"?class=derived&ttl_seconds=abc",
		"?class=derived&ttl_seconds=999999999",
		"?class=eternal",
	} {
		if code, _ := doReq(t, s, token, http.MethodPost, "/v1/blobs"+q, nil); code != http.StatusBadRequest {
			t.Fatalf("upload%s = %d, want 400", q, code)
		}
	}
}

// ---------------------------------------------------------------------------
// D-8 — the backup excludes cache bytes
// ---------------------------------------------------------------------------

func TestBackup_ExcludesDerivedBlobsButKeepsOwnedOnes(t *testing.T) {
	s, dir := newTestServer(t)
	owned := mustStore(t, s, "owned attachment", blobOwned, 0)
	derived := mustStore(t, s, "cached export", blobDerived, time.Hour)

	out := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := Backup(context.Background(), filepath.Join(dir, "hub.db"), dir, out); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gunzip the archive: %v", err)
	}
	defer gz.Close()
	raw, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	names := tarNames(t, raw)

	var sawOwned, sawDerived bool
	for _, n := range names {
		if filepath.Base(n) == owned {
			sawOwned = true
		}
		if filepath.Base(n) == derived {
			sawDerived = true
		}
	}
	if !sawOwned {
		t.Fatal("an owned blob was excluded from the backup")
	}
	if sawDerived {
		t.Fatal("a derived blob was archived: cache bytes land where no sweeper can reach them")
	}
}
