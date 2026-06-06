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
	"os"
	"path/filepath"
	"strings"
)

// Backup writes a tar.gz to outPath containing:
//   - hub.db.snapshot — a consistent SQLite snapshot taken with VACUUM INTO
//   - events.db.snapshot / digest.db.snapshot — the event + digest stores
//     (ADR-045 P1), each snapshotted the same way; absent for an un-split DB
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
		// `teams` holds the per-team event/digest shards (ADR-045 P2 —
		// teams/<team>/{events.db,digest.db}); back them up as a subtree so a
		// running per-team hub's transcripts survive a snapshot. (`team` is the
		// singular templates dir — a different path.)
		for _, sub := range []string{"team", "teams", "blobs"} {
			abs := filepath.Join(dataRoot, sub)
			if _, err := os.Stat(abs); errors.Is(err, fs.ErrNotExist) {
				continue
			} else if err != nil {
				return fmt.Errorf("stat %s: %w", sub, err)
			}
			if err := addDir(tw, abs, sub); err != nil {
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
		switch hdr.Name {
		case "hub.db.snapshot":
			dst = dbPath
		case "events.db.snapshot":
			dst = filepath.Join(dataRoot, "events.db")
		case "digest.db.snapshot":
			dst = filepath.Join(dataRoot, "digest.db")
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

func addDir(tw *tar.Writer, root, prefix string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
