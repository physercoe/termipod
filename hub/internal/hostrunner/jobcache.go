package hostrunner

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// jobcache.go — the host-local artifact store for detached jobs (ADR-058 §4).
//
// One root shared by every job kind, laid out as
// `~/.termipod/hostrunner/jobcache/<kind>/<command-id>/`. Artifacts stay on the
// host: the hub owns names, events and metadata, hosts own bytes (blueprint
// data-ownership law), and a multi-GB .rrd routed through hub disk would break
// that law for no gain — the local-first Replay consumer wants a local path.
//
// The <command-id> directory, not the individual file, is the unit of
// eviction. A job may write an artifact plus a log beside it, and deleting half
// of that leaves an artifact whose path the hub still advertises as valid.
//
// Recency is approximated by the newest mtime in a job's directory, not by
// atime: relatime (and noatime) make atime unreliable on the exact filesystems
// datasets live on, so a real read-triggered LRU is not available to us. Two
// consequences worth naming:
//
//   - It is really least-recently-*produced-or-touched*. A .rrd the director
//     re-opens for the tenth time looks as cold as one never opened, because
//     opening it is invisible to us. touchJobDir exists so the code paths that
//     *do* know about a use (reporting a result, resolving a cached artifact)
//     can say so.
//   - It pins in-flight work for free. A job writing a 4 GiB export keeps its
//     directory's mtime fresh the whole time, so the pin window below covers a
//     running job and a just-reported artifact under one rule, with no separate
//     in-flight bookkeeping to get out of sync.
const (
	// jobCacheDefaultCap is the total-bytes ceiling for the cache (ADR-058 §4).
	jobCacheDefaultCap int64 = 20 << 30 // 20 GiB

	// jobCachePinWindow protects recently-written directories from eviction:
	// long enough that a just-reported artifact survives the desktop's poll →
	// open round-trip, short enough that a full cache still drains. In-flight
	// jobs are covered by the same rule (see the mtime note above).
	jobCachePinWindow = 15 * time.Minute
)

// jobCache is the cache root plus its eviction policy. Zero Cap/Pin fall back
// to the defaults above.
type jobCache struct {
	Root string
	Cap  int64
	Pin  time.Duration
	Log  *slog.Logger
}

// defaultJobCacheRoot is `~/.termipod/hostrunner/jobcache`. An unresolvable
// HOME returns an error rather than falling back to a relative path or /tmp:
// writing multi-GB artifacts somewhere unexpected is worse than refusing the
// job with a clear reason.
func defaultJobCacheRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("job cache: HOME unresolved: %w", err)
	}
	return filepath.Join(home, ".termipod", "hostrunner", "jobcache"), nil
}

func (c *jobCache) cap() int64 {
	if c.Cap > 0 {
		return c.Cap
	}
	return jobCacheDefaultCap
}

func (c *jobCache) pin() time.Duration {
	if c.Pin > 0 {
		return c.Pin
	}
	return jobCachePinWindow
}

func (c *jobCache) log() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.Default()
}

// safeSegment rejects anything that could escape the cache root when joined
// into a path. Both inputs are already constrained upstream — kind comes from
// the hostjobs allowlist, command id is hub-generated — so this is defence in
// depth at the boundary that actually creates directories, not a substitute for
// validating args (ADR-058 §1 sends path-shaped args through resolveDataPath).
func safeSegment(s string) error {
	if s == "" {
		return fmt.Errorf("empty path segment")
	}
	for _, r := range s {
		ok := r == '-' || r == '_' || r == '.' ||
			(r >= '0' && r <= '9') ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z')
		if !ok {
			return fmt.Errorf("path segment %q contains %q", s, r)
		}
	}
	if s == "." || s == ".." {
		return fmt.Errorf("path segment %q traverses", s)
	}
	return nil
}

// jobDir creates and returns the directory a job of this kind may write to. It
// is the only writable location a job is given (ADR-058 §1).
func (c *jobCache) jobDir(kind, commandID string) (string, error) {
	if c == nil || c.Root == "" {
		return "", fmt.Errorf("job cache is not configured on this host-runner")
	}
	if err := safeSegment(kind); err != nil {
		return "", fmt.Errorf("job cache: %w", err)
	}
	if err := safeSegment(commandID); err != nil {
		return "", fmt.Errorf("job cache: %w", err)
	}
	dir := filepath.Join(c.Root, kind, commandID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("job cache: mkdir %s: %w", dir, err)
	}
	return dir, nil
}

// touchJobDir refreshes a job directory's recency so the next eviction pass
// treats it as warm. Best-effort: a cache is allowed to lose a touch.
func (c *jobCache) touchJobDir(dir string) {
	if c == nil || dir == "" {
		return
	}
	now := time.Now()
	if err := os.Chtimes(dir, now, now); err != nil {
		c.log().Debug("job cache: touch failed", "dir", dir, "err", err)
	}
}

// cacheEntry is one job directory's footprint.
type cacheEntry struct {
	dir   string
	size  int64
	mtime time.Time
}

// scan measures every `<kind>/<command-id>` directory under the root. Stray
// files that are not laid out that way are ignored rather than deleted: this
// cache shares a parent with the host-runner's state dir, and a policy of
// removing anything unrecognised is how a cache eats data it did not create.
func (c *jobCache) scan() ([]cacheEntry, int64, error) {
	kinds, err := os.ReadDir(c.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil // nothing cached yet
		}
		return nil, 0, err
	}
	var (
		out   []cacheEntry
		total int64
	)
	for _, k := range kinds {
		if !k.IsDir() {
			continue
		}
		jobs, err := os.ReadDir(filepath.Join(c.Root, k.Name()))
		if err != nil {
			c.log().Debug("job cache: read kind dir failed", "kind", k.Name(), "err", err)
			continue
		}
		for _, j := range jobs {
			if !j.IsDir() {
				continue
			}
			dir := filepath.Join(c.Root, k.Name(), j.Name())
			e, err := measureDir(dir)
			if err != nil {
				c.log().Debug("job cache: measure failed", "dir", dir, "err", err)
				continue
			}
			out = append(out, e)
			total += e.size
		}
	}
	return out, total, nil
}

// measureDir sums a job directory's bytes and takes its newest mtime — the
// directory's own included, so touchJobDir works on an empty or unchanged tree.
func measureDir(dir string) (cacheEntry, error) {
	e := cacheEntry{dir: dir}
	if fi, err := os.Stat(dir); err == nil {
		e.mtime = fi.ModTime()
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Tolerate an entry vanishing under the walk: this tree is a cache,
			// a concurrent eviction or a job's own cleanup is expected, and a
			// missing file must not fail the measurement of everything else.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		fi, err := d.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if fi.ModTime().After(e.mtime) {
			e.mtime = fi.ModTime()
		}
		if d.IsDir() {
			return nil
		}
		if fi.Mode().IsRegular() {
			e.size += fi.Size()
		}
		return nil
	})
	return e, err
}

// evict enforces the byte cap, coldest job directory first. It is best-effort
// and never returns an error to a caller: failing to make room must not fail
// the job that asked for room. Returns the bytes freed, for tests and logs.
func (c *jobCache) evict() int64 {
	if c == nil || c.Root == "" {
		return 0
	}
	entries, total, err := c.scan()
	if err != nil {
		c.log().Warn("job cache: scan failed; skipping eviction", "root", c.Root, "err", err)
		return 0
	}
	limit := c.cap()
	if total <= limit {
		return 0
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mtime.Before(entries[j].mtime) })

	var (
		freed  int64
		pinned int
		now    = time.Now()
		window = c.pin()
	)
	for _, e := range entries {
		if total <= limit {
			break
		}
		if now.Sub(e.mtime) < window {
			// Running or just-reported. Skipping it can leave the cache over
			// cap; that is the correct trade — deleting an artifact whose path
			// the hub just handed a caller turns a full disk into a broken
			// feature.
			pinned++
			continue
		}
		if err := os.RemoveAll(e.dir); err != nil {
			c.log().Warn("job cache: evict failed", "dir", e.dir, "err", err)
			continue
		}
		total -= e.size
		freed += e.size
		c.log().Info("job cache: evicted", "dir", e.dir, "bytes", e.size)
	}
	if total > limit {
		c.log().Warn("job cache: still over cap after eviction",
			"root", c.Root, "bytes", total, "cap", limit, "pinned_dirs", pinned)
	}
	return freed
}
