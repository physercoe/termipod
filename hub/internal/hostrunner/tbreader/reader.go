// Package tbreader reads scalar metric history out of a local TensorBoard
// log directory (tfevents files) and downsamples each curve for the hub
// digest (§6.5, P3.1).
//
// TensorBoard writers emit events.out.tfevents.<ts>.<host>.<pid>.v2 files
// — each a TFRecord-framed stream of serialized tensorflow.Event protos.
// A log directory is one subdirectory per run; tfevents files live
// directly inside that subdirectory.
//
// The reader stays host-local so bulk time-series never leaves the GPU
// box (blueprint §4 data-ownership law). Downsampling and the PUT happen
// here before the digest is shipped to the hub.
//
// We decode only the handful of protobuf fields the poller needs
// (Event.wall_time, Event.step, Event.summary, Summary.value, Value.tag,
// Value.simple_value, Value.tensor.float_val) — pulling in the
// google.golang.org/protobuf runtime or the TensorFlow/TensorBoard Go
// shims would be massively out of proportion to what we read.
package tbreader

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Point is one sample in a metric series. Mirrors trackio.Point so the
// downsampler shape stays consistent across reader packages.
type Point struct {
	Step  int64
	Value float64
}

// URI is a parsed tb_run_uri. The canonical form is:
//
//	tb://<run-path>
//
// where <run-path> is the relative path from the configured TensorBoard
// root (see Runner.TensorBoardDir) to the run's log subdirectory. The
// run-path may contain nested path segments — TensorBoard allows
// arbitrary subdirectory nesting per run (e.g. "ablation/lr1e-4/seed0").
type URI struct {
	RunPath string
}

// ParseURI accepts tb://<run-path> and returns the parts. Unknown
// schemes and empty run paths are errors; the poller skips runs whose
// URI it cannot parse rather than synthesizing defaults.
func ParseURI(raw string) (URI, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return URI{}, fmt.Errorf("parse uri: %w", err)
	}
	if u.Scheme != "tb" {
		return URI{}, fmt.Errorf("unsupported scheme %q, want tb://", u.Scheme)
	}
	// For tb://a/b/c, url.Parse lands "a" in Host and "/b/c" in Path.
	// Recombine into a single relative path.
	var parts []string
	if u.Host != "" {
		parts = append(parts, u.Host)
	}
	if u.Path != "" {
		trimmed := strings.TrimPrefix(u.Path, "/")
		if trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	runPath := strings.Join(parts, "/")
	if runPath == "" {
		return URI{}, fmt.Errorf("tb uri requires a <run-path>: %q", raw)
	}
	// Reject any ".." segment outright — the poller resolves this
	// relative to --tb-dir and we don't want a crafted URI to escape
	// that root, even transiently via normalisation. Absolute paths
	// (leading "/") are also rejected; a run-path is always relative.
	if strings.HasPrefix(runPath, "/") {
		return URI{}, fmt.Errorf("tb uri run-path must be relative: %q", raw)
	}
	for _, seg := range strings.Split(runPath, "/") {
		if seg == ".." {
			return URI{}, fmt.Errorf("tb uri run-path escapes root: %q", raw)
		}
	}
	return URI{RunPath: filepath.ToSlash(filepath.Clean(runPath))}, nil
}

// DefaultDir is the directory TensorBoard conventionally uses when no
// --logdir is passed. There isn't a published cross-distro default the
// way trackio has one, so we fall back to "" and let the operator
// configure --tb-dir explicitly.
func DefaultDir() string {
	if d := os.Getenv("TENSORBOARD_LOGDIR"); d != "" {
		return d
	}
	return ""
}

// RunDir joins the configured root with the parsed run-path. Caller
// should guard against traversal via ParseURI.
func RunDir(root, runPath string) string {
	return filepath.Join(root, filepath.FromSlash(runPath))
}

// ReadRun walks <root>/<runPath>/, finds tfevents files in lex order
// (TB itself orders by creation via the timestamp embedded in the
// filename, which is lex-safe), decodes each record, and accumulates
// scalar samples keyed by Value.tag.
//
// The walk is non-recursive: TB convention places tfevents files
// directly inside the run directory, and nested runs are separate
// entries in the hub digest. Returns an empty map (not an error) when
// the run dir doesn't exist yet — the worker may not have started
// logging.
//
// Points are sorted ascending by step and deduplicated on step
// (last-write wins) to match the trackio reader's contract.
func ReadRun(root, runPath string) (map[string][]Point, error) {
	dir := RunDir(root, runPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string][]Point{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "events.out.tfevents.") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	sort.Strings(files)

	series := map[string][]Point{}
	for _, path := range files {
		if err := readFileInto(path, series); err != nil {
			// A single corrupt file shouldn't blow up the poll; skip
			// and log via the caller. Returning the error here would
			// stall every metric in a run that happens to have one bad
			// file alongside good ones.
			continue
		}
	}
	for k, pts := range series {
		series[k] = dedupByStep(pts)
	}
	return series, nil
}

// readFileInto opens one tfevents file, walks its TFRecord stream, and
// folds every scalar into the supplied series map. Callers accumulate
// across files (one run directory may rotate through multiple tfevents
// files over a long training job).
func readFileInto(path string, series map[string][]Point) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	for payload, rerr := range Records(f) {
		if rerr != nil {
			// Treat truncated tail records as end-of-stream; anything
			// else bubbles up and gets swallowed by ReadRun so one bad
			// file doesn't kill the whole run.
			if errors.Is(rerr, errTruncated) {
				return nil
			}
			return rerr
		}
		step, scalars, perr := parseEvent(payload)
		if perr != nil || len(scalars) == 0 {
			continue
		}
		for tag, v := range scalars {
			series[tag] = append(series[tag], Point{Step: step, Value: v})
		}
	}
	return nil
}

// dedupByStep collapses duplicate steps to the last value seen. Sort is
// stable so a tie means the later-appended sample wins — matches the
// trackio reader exactly.
func dedupByStep(pts []Point) []Point {
	if len(pts) == 0 {
		return pts
	}
	sort.SliceStable(pts, func(i, j int) bool { return pts[i].Step < pts[j].Step })
	out := pts[:0]
	for i, p := range pts {
		if i > 0 && out[len(out)-1].Step == p.Step {
			out[len(out)-1] = p
			continue
		}
		out = append(out, p)
	}
	return out
}
