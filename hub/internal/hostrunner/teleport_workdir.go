package hostrunner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Non-worktree workdir transport for session teleport (ADR-057 T2). A session
// without a git worktree has no branch to push; its working directory is itself
// the byte-store that must move. packWorkdir tars the whole directory tree
// (gzip) with a hard compressed-size cap; restoreWorkdir untars it at the
// target-derived path. Worktree sessions never take this path — their files
// ride git (teleport_git.go). This is the T2 non-git complement, kept separate
// from the engine-state bundle (teleport_state.go) so the two byte-stores move
// independently and can be capped / reasoned about on their own terms.

// maxWorkdirBundleBytes caps the COMPRESSED workdir bundle. Maintainer decision
// (ADR-057, 2026-07-23): a non-git workdir teleport refuses larger rather than
// dragging an unbounded tree (node_modules, build output, datasets) through the
// hub. packWorkdir aborts as soon as the gzip output crosses this, so a huge
// tree fails fast without being buffered whole.
const maxWorkdirBundleBytes int64 = 256 << 20 // 256 MiB

// errWorkdirTooLarge is the clean refusal when a workdir's compressed bundle
// exceeds the cap — the orchestrator surfaces it rather than moving a partial
// tree.
type errWorkdirTooLarge struct{ limit int64 }

func (e errWorkdirTooLarge) Error() string {
	return fmt.Sprintf("teleport: workdir bundle exceeds the %d MiB compressed cap (T2 refuses larger)", e.limit>>20)
}

// cappedWriter fails the write once more than `limit` bytes have passed through.
// Wrapping the gzip destination in it bounds packWorkdir's memory to ~the cap
// (plus the file being read) instead of the full uncompressed tree.
type cappedWriter struct {
	w     io.Writer
	limit int64
	n     int64
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.n+int64(len(p)) > c.limit {
		return 0, errWorkdirTooLarge{limit: c.limit}
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// packWorkdir tars `dir`'s whole tree into a gzip-compressed bundle, aborting
// with errWorkdirTooLarge once the compressed output crosses capBytes. Regular
// files and directories are captured; symlinks are skipped (never followed — a
// symlink inside the workdir could otherwise escape it), as are non-regular
// special files. Entry names are workdir-relative and slash-separated so the
// target can untar them under its own re-derived workdir path.
func packWorkdir(dir string, capBytes int64) ([]byte, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("teleport: workdir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("teleport: workdir %s is not a directory", dir)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&cappedWriter{w: &buf, limit: capBytes})
	tw := tar.NewWriter(gz)
	root := filepath.Clean(dir)
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil // the workdir itself is implied by the restore target
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		name := filepath.ToSlash(rel)
		fi, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		mode := fi.Mode()
		switch {
		case mode&fs.ModeSymlink != 0:
			return nil
		case d.IsDir():
			return tw.WriteHeader(&tar.Header{
				Name: name + "/", Typeflag: tar.TypeDir, Mode: 0o700, ModTime: fi.ModTime(),
			})
		case mode.IsRegular():
			if werr := tw.WriteHeader(&tar.Header{
				Name: name, Typeflag: tar.TypeReg,
				Mode: int64(mode.Perm()), Size: fi.Size(), ModTime: fi.ModTime(),
			}); werr != nil {
				return werr
			}
			f, oerr := os.Open(path)
			if oerr != nil {
				return oerr
			}
			_, cerr := io.Copy(tw, f)
			f.Close()
			return cerr
		default:
			return nil // sockets, devices, pipes — nothing to carry
		}
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// restoreWorkdir untars a bundle produced by packWorkdir under `dir` (the
// target-derived workdir), creating it and any parent dirs. Each entry is
// path-traversal checked: a name that resolves outside `dir` (a "../" escape in
// a tampered bundle) is refused. Existing files are overwritten — the bundle is
// authoritative for the session being teleported.
func restoreWorkdir(dir string, bundle []byte) error {
	gz, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		return fmt.Errorf("teleport: open workdir gzip: %w", err)
	}
	defer gz.Close()
	root := filepath.Clean(dir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("teleport: mkdir workdir %s: %w", root, err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, terr := tr.Next()
		if terr == io.EOF {
			break
		}
		if terr != nil {
			return fmt.Errorf("teleport: read workdir tar: %w", terr)
		}
		// Zip-slip guard: filepath.Join cleans the result, so a joined path that
		// stays under root MUST have `root + separator` as a prefix. A tampered
		// "../" name resolves outside and is refused before any file operation.
		dst := filepath.Join(root, filepath.FromSlash(hdr.Name))
		if !strings.HasPrefix(dst, root+string(os.PathSeparator)) {
			return fmt.Errorf("teleport: workdir entry %q escapes the workdir", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o700); err != nil {
				return fmt.Errorf("teleport: mkdir %s: %w", dst, err)
			}
		case tar.TypeReg, tar.TypeRegA: //nolint:staticcheck // TypeRegA for older tars
			if err := writeRestoredFile(dst, os.FileMode(hdr.Mode).Perm(), tr); err != nil {
				return err
			}
		default:
			continue
		}
	}
	return nil
}

// writeRestoredFile writes one tar entry to dst, creating parent dirs. The
// Close error is checked, not ignored: a buffered write can surface a failure
// (e.g. ENOSPC) only at close, and swallowing it would silently truncate a
// restored file.
func writeRestoredFile(dst string, perm os.FileMode, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("teleport: mkdir %s: %w", filepath.Dir(dst), err)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return fmt.Errorf("teleport: create %s: %w", dst, err)
	}
	if _, cerr := io.Copy(f, r); cerr != nil { //nolint:gosec // entry bounded by the tar header size
		_ = f.Close()
		return fmt.Errorf("teleport: write %s: %w", dst, cerr)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("teleport: close %s: %w", dst, err)
	}
	return nil
}
