// Package tbreader is the metrics.Reader backend for a local
// TensorBoard log directory (§6.5, P3.1).
//
// TensorBoard writers emit events.out.tfevents.<ts>.<host>.<pid>.v2
// files — each a TFRecord-framed stream of serialized tensorflow.Event
// protos. A log directory is one subdirectory per run; tfevents files
// live directly inside that subdirectory.
//
// We decode only the handful of protobuf fields the poller needs
// (Event.wall_time, Event.step, Event.summary, Summary.value, Value.tag,
// Value.simple_value, Value.tensor.float_val) — pulling in the
// google.golang.org/protobuf runtime or the TensorFlow/TensorBoard Go
// shims would be massively out of proportion to what we read.
//
// URI scheme: tb://<run-path>.
package tbreader

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/termipod/hub/internal/hostrunner/metrics"
)

// Scheme is the URI scheme TensorBoard runs use on runs.trackio_run_uri.
const Scheme = "tb"

// Source is the metrics.Reader for a local TensorBoard logdir rooted
// at Root. Construct via New.
type Source struct {
	Root string
}

// New returns a metrics.Reader for the TensorBoard logdir under root.
// Root is typically the operator-supplied --tb-dir value.
func New(root string) *Source { return &Source{Root: root} }

// Scheme implements metrics.Reader.
func (s *Source) Scheme() string { return Scheme }

// Read implements metrics.Reader.
func (s *Source) Read(ctx context.Context, uri string) (map[string]metrics.Series, error) {
	_ = ctx // context cancellation is best-effort in the file walk
	u, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}
	return ReadRun(s.Root, u.RunPath)
}

// URI is a parsed tb_run_uri. The canonical form is:
//
//	tb://<run-path>
//
// where <run-path> is the relative path from the configured TensorBoard
// root to the run's log subdirectory. The run-path may contain nested
// path segments (e.g. "ablation/lr1e-4/seed0").
type URI struct {
	RunPath string
}

// ParseURI accepts tb://<run-path> and returns the parts.
func ParseURI(raw string) (URI, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return URI{}, fmt.Errorf("parse uri: %w", err)
	}
	if u.Scheme != Scheme {
		return URI{}, fmt.Errorf("unsupported scheme %q, want %s://", u.Scheme, Scheme)
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

// DefaultDir returns $TENSORBOARD_LOGDIR if set. There isn't a published
// cross-distro default the way trackio has one, so callers fall back to
// requiring --tb-dir explicitly.
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

// ReadRun walks <root>/<runPath>/, finds tfevents files in lex order,
// decodes each record, and accumulates scalar samples keyed by
// Summary.Value.tag. Returns an empty map (not an error) when the run
// dir doesn't exist yet.
func ReadRun(root, runPath string) (map[string]metrics.Series, error) {
	dir := RunDir(root, runPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]metrics.Series{}, nil
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

	series := map[string]metrics.Series{}
	for _, path := range files {
		if err := readFileInto(path, series); err != nil {
			// A single corrupt file shouldn't blow up the poll; skip.
			continue
		}
	}
	for k, pts := range series {
		series[k] = dedupByStep(pts)
	}
	return series, nil
}

// readFileInto opens one tfevents file, walks its TFRecord stream, and
// folds every scalar into the supplied series map.
func readFileInto(path string, series map[string]metrics.Series) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	for payload, rerr := range Records(f) {
		if rerr != nil {
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
			series[tag] = append(series[tag], metrics.Point{Step: step, Value: v})
		}
	}
	return nil
}

// dedupByStep collapses duplicate steps to the last value seen.
func dedupByStep(pts metrics.Series) metrics.Series {
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
