package datasetmeta

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DirSource serves a dataset rooted at a local directory. It is the only
// Source implementation in W1: the replay plan keeps the player local-first
// and defers remote roots to the SSH-forward wedge.
type DirSource struct {
	root string
}

// NewDirSource roots a Source at an absolute or relative local directory.
func NewDirSource(root string) *DirSource {
	return &DirSource{root: filepath.Clean(root)}
}

// Root returns the directory this Source serves.
func (d *DirSource) Root() string { return d.root }

// resolve maps a slash-separated name relative to the root onto a local path,
// refusing anything that escapes.
//
// Same guard as restoreWorkdir's zip-slip check: filepath.Join cleans its
// result, so a name that stays inside must have root+separator as a prefix. A
// dataset root is user-supplied and its meta/ tree can contain symlinks, so
// the check runs on every access rather than once at construction.
func (d *DirSource) resolve(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("datasetmeta: empty path")
	}
	if strings.Contains(name, "\x00") {
		return "", fmt.Errorf("datasetmeta: path %q contains a NUL byte", name)
	}
	p := filepath.Join(d.root, filepath.FromSlash(name))
	if p != d.root && !strings.HasPrefix(p, d.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("datasetmeta: path %q escapes the dataset root", name)
	}
	return p, nil
}

// Open streams a file under the dataset root.
func (d *DirSource) Open(name string) (io.ReadCloser, error) {
	p, err := d.resolve(name)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// OpenReaderAt opens a file for random access and reports its size.
func (d *DirSource) OpenReaderAt(name string) (ReaderAtCloser, int64, error) {
	p, err := d.resolve(name)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	if st.IsDir() {
		f.Close()
		return nil, 0, fmt.Errorf("datasetmeta: %q is a directory", name)
	}
	return f, st.Size(), nil
}

// List returns the entries directly under dir, sorted by name so that callers
// walking chunk-000, chunk-001, … see them in order. Directory order from the
// filesystem is not guaranteed, and episode indices depend on it.
func (d *DirSource) List(dir string) ([]Entry, error) {
	p, err := d.resolve(dir)
	if err != nil {
		return nil, err
	}
	des, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		if len(out) >= MaxMetaDirEntries {
			break
		}
		e := Entry{Name: de.Name(), IsDir: de.IsDir()}
		if fi, err := de.Info(); err == nil {
			e.Size = fi.Size()
			e.ModTime = fi.ModTime()
			// A symlink is reported by its own type here; following one that
			// points outside the root would defeat resolve(). Skip it rather
			// than silently reading through.
			if fi.Mode()&os.ModeSymlink != 0 {
				continue
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// readCapped reads a whole file, refusing rather than truncating past max.
//
// Truncation would be the worse failure: a half-read JSON document fails to
// parse with a syntax error that points at the cap instead of the cause, and a
// half-read stats file would produce numbers that look plausible.
func readCapped(src Source, name string, max int64) ([]byte, error) {
	rc, err := src.Open(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	// Read one byte past the cap so hitting it is distinguishable from a file
	// that happens to be exactly max bytes long.
	b, err := io.ReadAll(io.LimitReader(rc, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("datasetmeta: %s exceeds the %d-byte cap", name, max)
	}
	return b, nil
}

// exists reports whether a file can be opened under the root.
func exists(src Source, name string) bool {
	rc, err := src.Open(name)
	if err != nil {
		return false
	}
	rc.Close()
	return true
}
