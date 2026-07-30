package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Backup writes a tar.gz to outPath containing:
//   - hub.db.snapshot — a consistent SQLite snapshot taken with VACUUM INTO
//   - events.db.snapshot / digest.db.snapshot — the GLOBAL event + digest
//     stores (ADR-045 P1), each snapshotted the same way; present only on a
//     P1 deployment that hasn't yet been sharded per team
//   - teams/<team>/{events.db,digest.db}.snapshot — the per-team event +
//     digest shards (ADR-045 P2), each VACUUM-INTO-snapshotted
//   - team/   — templates, policy YAML, agent_families overlay (if present)
//   - blobs/  — content-addressed attached files (if present)
//
// The snapshot uses VACUUM INTO so it's safe to run while hub-server is
// live: SQLite serializes the export against ongoing writes inside a
// single transaction, the archive captures the committed state, and
// new mutations after the snapshot starts go to a later backup.
//
// The three stores are snapshotted independently (no cross-file
// transaction), so a write landing in events.db between the hub.db and
// events.db snapshots is captured by the next backup — the same
// eventual-consistency the live split already has (a derived digest is
// recomputable from the event log via read-repair).
//
// dbPath, dataRoot and outPath must all resolve to absolute paths the
// process can read (and outPath must be writable). dataRoot may be empty
// when the caller doesn't ship blobs/team — the snapshot alone is still
// a usable archive.
func Backup(ctx context.Context, dbPath, dataRoot, outPath string) error {
	if dbPath == "" {
		return errors.New("dbPath required")
	}
	if outPath == "" {
		return errors.New("outPath required")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
		return fmt.Errorf("ensure out dir: %w", err)
	}

	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	// Snapshot each store that exists. hub.db is always present; events.db /
	// digest.db exist only on a split deployment (ADR-045 P1 step 4).
	eventsPath, digestPath := storePathsFor(dbPath)
	stores := []struct{ path, snapName string }{
		{dbPath, "hub.db.snapshot"},
		{eventsPath, "events.db.snapshot"},
		{digestPath, "digest.db.snapshot"},
	}
	for _, st := range stores {
		if st.path != dbPath {
			if _, err := os.Stat(st.path); errors.Is(err, fs.ErrNotExist) {
				continue
			} else if err != nil {
				return fmt.Errorf("stat %s: %w", st.snapName, err)
			}
		}
		if err := snapshotInto(ctx, tw, st.path, st.snapName); err != nil {
			return err
		}
	}
	if dataRoot != "" {
		// Per-team shards (ADR-045 P2): teams/<team>/{events.db,digest.db}. Each
		// is snapshotted with VACUUM INTO (a consistent single file) under
		// teams/<team>/<store>.snapshot, NOT raw-copied — a raw copy of a live
		// WAL database can capture a torn -wal and restore inconsistently.
		if err := snapshotTeamShards(ctx, tw, dataRoot); err != nil {
			return err
		}
		// `team` is the singular templates dir (policy/agent_families overlay);
		// `blobs` is the content-addressed attachment store. Both are
		// raw-copyable: a blob file is written once under its content address
		// and never rewritten in place. `blobs` is no longer *immutable*,
		// though — ADR-061's sweeper unlinks expired `derived` blobs — so
		// addDir tolerates an entry vanishing mid-walk. (`teams`, the shard
		// dir, is handled above via per-store snapshots.)
		//
		// `derived` blobs are excluded (ADR-061 D-8): they are reproducible by
		// definition, and archiving them puts cache bytes somewhere no sweeper
		// can ever reach them — a backup that grows forever with browsing
		// traffic.
		derived := derivedBlobSHAs(ctx, dbPath)
		for _, sub := range []string{"team", "blobs"} {
			abs := filepath.Join(dataRoot, sub)
			if _, err := os.Stat(abs); errors.Is(err, fs.ErrNotExist) {
				continue
			} else if err != nil {
				return fmt.Errorf("stat %s: %w", sub, err)
			}
			var skip func(string) bool
			if sub == "blobs" && len(derived) > 0 {
				skip = func(base string) bool {
					_, ok := derived[base]
					return ok
				}
			}
			if err := addDirSkipping(tw, abs, sub, skip); err != nil {
				return err
			}
		}
	}
	return nil
}

// snapshotTeamShards VACUUM-INTO-snapshots every per-team event + digest store
// under dataRoot/teams/<team>/ into the archive at teams/<team>/<store>.snapshot
// (ADR-045 P2). A missing teams/ dir (a deployment that never ingested) is fine.
func snapshotTeamShards(ctx context.Context, tw *tar.Writer, dataRoot string) error {
	teamsRoot := filepath.Join(dataRoot, "teams")
	ents, err := os.ReadDir(teamsRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read teams dir: %w", err)
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		team := e.Name()
		for _, store := range []string{"events.db", "digest.db"} {
			p := filepath.Join(teamsRoot, team, store)
			if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
				continue
			} else if err != nil {
				return fmt.Errorf("stat %s: %w", p, err)
			}
			snapName := "teams/" + team + "/" + store + ".snapshot"
			if err := snapshotInto(ctx, tw, p, snapName); err != nil {
				return err
			}
		}
	}
	return nil
}

// snapshotInto VACUUMs dbPath into a temp file next to it (same filesystem),
// streams that consistent snapshot into the archive under snapName, and removes
// the temp.
func snapshotInto(ctx context.Context, tw *tar.Writer, dbPath, snapName string) error {
	tmp := dbPath + ".backup-tmp"
	_ = os.Remove(tmp)
	defer os.Remove(tmp)
	if err := vacuumInto(ctx, dbPath, tmp); err != nil {
		return fmt.Errorf("snapshot %s: %w", snapName, err)
	}
	return addFile(tw, tmp, snapName)
}

// derivedBlobSHAs reads the content addresses currently classed `derived`, for
// the backup exclusion in ADR-061 D-8.
//
// Opened standalone, the same way vacuumInto does, so Backup stays a free
// function that needs no live Server.
//
// Every failure yields an empty set rather than an error, and that is a
// deliberate ranking: a backup you cannot take is worse than a backup carrying
// some cache bytes. The two failures worth naming are a pre-0071 database (no
// `class` column, so the query errors — and such a database has no derived blobs
// anyway) and a read that loses a race with the sweeper. It warns so a silent
// degrade is at least a visible one.
//
// The set is read from the live database rather than the snapshot, so a blob
// promoted to `owned` in the moments after this read is missed by *this* archive
// and caught by the next. A derived blob swept in the same window is simply
// already gone.
func derivedBlobSHAs(ctx context.Context, dbPath string) map[string]struct{} {
	out := map[string]struct{}{}
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		slog.Default().Warn("backup: cannot classify blobs; archiving all of them", "err", err)
		return out
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT sha256 FROM blobs WHERE class = 'derived'`)
	if err != nil {
		slog.Default().Warn("backup: cannot classify blobs; archiving all of them", "err", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			slog.Default().Warn("backup: blob classification read failed mid-scan", "err", err)
			return map[string]struct{}{}
		}
		out[sha] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		slog.Default().Warn("backup: blob classification read failed", "err", err)
		return map[string]struct{}{}
	}
	return out
}

// vacuumInto opens dbPath read-only, runs VACUUM INTO 'dst' which writes
// a transactionally-consistent copy without altering the original, and
// closes both connections. Uses a tiny standalone *sql.DB so we don't
// need to coordinate with whatever live server may also have the file
// open — SQLite's lock manager handles the serialization.
func vacuumInto(ctx context.Context, dbPath, dst string) error {
	dsn := dbPath + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	// VACUUM INTO does not accept bound parameters; sanitize by checking
	// for embedded quotes and refusing rather than risking injection
	// against a path the operator controls anyway.
	if strings.ContainsAny(dst, "'\"") {
		return errors.New("dst path may not contain quotes")
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf("VACUUM INTO '%s'", dst))
	return err
}

// Restore extracts a backup archive into dataRoot, then opens the
// restored hub.db so migrations run forward to the current binary's
// schema. Refuses with ErrDataRootNotEmpty when dataRoot already
// contains state, unless force is set — clobbering a non-empty data
// root is the kind of mistake that's hard to undo on a phone-tap.
func Restore(ctx context.Context, archivePath, dataRoot string, force bool) error {
	if archivePath == "" || dataRoot == "" {
		return errors.New("archivePath and dataRoot required")
	}
	if err := os.MkdirAll(dataRoot, 0o700); err != nil {
		return fmt.Errorf("ensure data root: %w", err)
	}
	if !force {
		empty, err := isEffectivelyEmpty(dataRoot)
		if err != nil {
			return err
		}
		if !empty {
			return ErrDataRootNotEmpty
		}
	}

	in, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	dbPath := filepath.Join(dataRoot, "hub.db")
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		// Reject anything that escapes dataRoot via "..", absolute
		// paths, or symlinks. We control the writer, but a tarball
		// arriving from elsewhere is the user's only handle on
		// "what's in this backup", so be paranoid.
		if !safeTarName(hdr.Name) {
			return fmt.Errorf("unsafe entry in archive: %q", hdr.Name)
		}
		dst := filepath.Join(dataRoot, hdr.Name)
		// A *.snapshot entry restores to its path with the suffix stripped:
		// hub.db.snapshot → hub.db, events.db.snapshot → events.db, and the
		// per-team teams/<team>/events.db.snapshot → teams/<team>/events.db
		// (ADR-045 P1/P2). One uniform rule covers global + per-team stores.
		if strings.HasSuffix(hdr.Name, ".snapshot") {
			dst = filepath.Join(dataRoot, strings.TrimSuffix(hdr.Name, ".snapshot"))
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o700); err != nil {
				return fmt.Errorf("mkdir %s: %w", hdr.Name, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return fmt.Errorf("mkdir parent of %s: %w", hdr.Name, err)
			}
			if err := writeFromTar(tr, dst, fileMode(hdr.Mode)); err != nil {
				return err
			}
		default:
			// Skip device files, symlinks, hardlinks — none belong in a
			// hub backup, and accepting them would let a malicious archive
			// place links that escape dataRoot on extraction.
		}
	}

	// Open the restored DB so OpenDB runs migrations forward; this is
	// what makes "restore an old backup on a newer hub-server" tractable.
	db, err := OpenDB(dbPath)
	if err != nil {
		return fmt.Errorf("open restored db: %w", err)
	}
	return db.Close()
}

// ErrDataRootNotEmpty is returned by Restore when the target directory
// already has files and the caller hasn't passed force=true.
var ErrDataRootNotEmpty = errors.New("data root is not empty (pass --force to overwrite)")

func addFile(tw *tar.Writer, src, name string) error {
	f, err := os.Open(src)
	// A file that vanished between the directory read and this open is skipped,
	// not fatal — see the concurrent-removal note on addDir. Every other open
	// error (permissions, I/O) still fails the backup.
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{
		Name:    name,
		Mode:    int64(stat.Mode().Perm()),
		Size:    stat.Size(),
		ModTime: stat.ModTime(),
	}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// addDir tars a directory tree, tolerating entries that disappear underneath
// the walk.
//
// The trees passed here used to be safely raw-copyable because nothing ever
// removed from them. That premise ends with ADR-061: the blob store gains a
// sweeper that unlinks expired `class='derived'` blobs, so `blobs/` is now a
// tree that mutates while a live backup walks it. `filepath.WalkDir` reads a
// directory and *then* stats each entry, so a concurrent unlink surfaces as an
// ENOENT — either in this callback or in addFile's open — and, returned as-is,
// it would fail an entire backup because one cache blob expired at the wrong
// moment.
//
// Only `fs.ErrNotExist` is skipped. A permission or I/O error still fails the
// backup: "some bytes were unreadable" must never be silently downgraded to a
// successful archive.
//
// Truncation mid-copy is deliberately NOT handled, because it cannot happen to
// the trees we pass: blob files are content-addressed and only ever created or
// unlinked, never rewritten in place (`storeBlob`, handlers_blobs.go:32-54).
func addDir(tw *tar.Writer, root, prefix string) error {
	return addDirSkipping(tw, root, prefix, nil)
}

// addDirSkipping is addDir with an exclusion predicate on the file's base name.
//
// The one caller that needs it is `blobs/` (ADR-061 D-8): a `derived` blob is a
// cache entry, reproducible by definition, and archiving one puts cache bytes
// somewhere no sweeper can ever reach them — so a backup would grow forever with
// browsing traffic. Base name is the right key because a blob file *is* its
// content address.
func addDirSkipping(tw *tar.Writer, root, prefix string, skip func(base string) bool) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			// Includes the root itself: every caller already treats a missing
			// directory as "nothing to archive" via its own pre-walk stat, so
			// losing the race with that stat lands in the same place.
			return nil
		}
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entryName := filepath.ToSlash(filepath.Join(prefix, rel))
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			return tw.WriteHeader(&tar.Header{
				Name:     entryName + "/",
				Mode:     0o700,
				Typeflag: tar.TypeDir,
			})
		}
		// Skip anything that isn't a regular file (sockets, FIFOs,
		// symlinks); we don't want them in a hub backup.
		if !d.Type().IsRegular() {
			return nil
		}
		if skip != nil && skip(d.Name()) {
			return nil
		}
		return addFile(tw, path, entryName)
	})
}

func writeFromTar(tr *tar.Reader, dst string, mode os.FileMode) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, tr); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

func safeTarName(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") {
		return false
	}
	cleaned := filepath.ToSlash(filepath.Clean(name))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return false
	}
	return true
}

func fileMode(m int64) os.FileMode {
	if m == 0 {
		return 0o600
	}
	return os.FileMode(m).Perm()
}

// isEffectivelyEmpty returns true when dir contains nothing OR contains
// only entries the operator might have laid down before realizing they
// needed to restore (a fresh hub.db from `init`, an empty team/ tree).
// We conservatively treat any non-empty regular file as "has content",
// matching the user's mental model of "I'd be overwriting work".
func isEffectivelyEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Name() == ".DS_Store" {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if e.IsDir() {
			sub, err := isEffectivelyEmpty(full)
			if err != nil {
				return false, err
			}
			if !sub {
				return false, nil
			}
			continue
		}
		fi, err := os.Stat(full)
		if err != nil {
			return false, err
		}
		if fi.Size() > 0 {
			return false, nil
		}
	}
	return true, nil
}
