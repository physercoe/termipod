// Package wandb reads metric history out of a local wandb offline-run
// directory and downsamples each curve for the hub digest (§6.5, P3.1).
//
// wandb's offline mode writes one run directory per run under WANDB_DIR
// (commonly ./wandb). Each run directory (e.g. `run-20260423_123456-abc123`)
// contains a `files/wandb-history.jsonl` file. Each line is a JSON object
// describing one logged step, typically of the form:
//
//	{"_step": 0, "_timestamp": 1700000000, "loss": 2.5, "acc": 0.1}
//
// Keys starting with `_` are wandb metadata. `_step` is the authoritative
// training step. Non-underscore scalar-numeric keys are user-logged
// metrics — everything else (strings, nested objects, arrays for
// histograms/images, null) is skipped silently, same as the trackio
// reader does.
//
// This package exposes the minimum surface host-runner's poller needs: a
// URI parser, a run-directory resolver, and a ReadRun call that returns
// every scalar series in canonical [step, value] form.
//
// Blueprint §4 data-ownership law: the hub never stores bulk time-series,
// so the reader stays host-local. Downsampling happens here before we
// PUT the digest, not on the hub.
package wandb

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
)

// Point is one sample in a metric series. Step is the training step,
// Value is the scalar logged against that step. Non-numeric JSON entries
// (strings, objects, arrays) are skipped silently — they're not sparkline
// material.
type Point struct {
	Step  int64
	Value float64
}

// URI is a parsed wandb_run_uri. The canonical form is:
//
//	wandb://<project>/<run-dir>
//
// where <project> is the top-level wandb subdirectory name (typically the
// wandb project slug) and <run-dir> is the run identifier directory name
// (e.g. `run-20260423_123456-abc123`). The worker agent writes this
// string onto runs.trackio_run_uri when it initialises wandb in offline
// mode; host-runner round-trips it here.
type URI struct {
	Project string
	RunDir  string
}

// ParseURI accepts wandb://<project>/<run-dir> and returns the parts.
// Unknown schemes and empty components are errors — the poller skips
// runs whose URI it cannot parse rather than synthesizing defaults.
func ParseURI(raw string) (URI, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return URI{}, fmt.Errorf("parse uri: %w", err)
	}
	if u.Scheme != "wandb" {
		return URI{}, fmt.Errorf("unsupported scheme %q, want wandb://", u.Scheme)
	}
	project := u.Host
	// u.Path starts with "/"; strip it so "/run-123" → "run-123".
	runDir := ""
	if len(u.Path) > 0 && u.Path[0] == '/' {
		runDir = u.Path[1:]
	} else {
		runDir = u.Path
	}
	if project == "" || runDir == "" {
		return URI{}, fmt.Errorf("wandb uri requires <project>/<run-dir>: %q", raw)
	}
	return URI{Project: project, RunDir: runDir}, nil
}

// DefaultDir returns the directory wandb uses when WANDB_DIR is unset
// in the process environment. wandb's own default is the caller's cwd
// (`./wandb`), which is useless from the host-runner's perspective since
// it has no notion of the worker's cwd — prefer passing --wandb-dir
// explicitly. Returns "" when WANDB_DIR is unset so callers can detect
// the "no default available" case and require an explicit flag.
func DefaultDir() string {
	return os.Getenv("WANDB_DIR")
}

// RunHistoryPath returns the absolute path of the wandb-history.jsonl
// file for a project/run-dir pair under root.
//
// Layout: <root>/<run-dir>/files/wandb-history.jsonl
//
// Project is accepted for symmetry with the trackio reader but is not
// part of the on-disk layout wandb offline runs use — each run-dir is
// self-contained under the root. Callers should still pass the project
// parsed from the URI so future layout shifts can be absorbed here
// without touching the poller.
func RunHistoryPath(root, runDir string) string {
	return filepath.Join(root, runDir, "files", "wandb-history.jsonl")
}

// ReadRun loads every scalar metric series for one run from the wandb
// offline-mode history file. The returned map is keyed by metric name
// (every non-underscore JSON key with a numeric scalar value); values
// are sorted ascending by step and deduplicated on step (last write
// wins, mirroring wandb's own resume semantics).
//
// A run with no recorded steps returns an empty map, not an error —
// callers should treat that as "poll again later" rather than a failure.
// A missing history file is likewise non-fatal: the worker hasn't
// flushed its first step yet.
func ReadRun(ctx context.Context, historyPath string) (map[string][]Point, error) {
	f, err := os.Open(historyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Run dir exists but worker hasn't logged yet (or wandb layout
			// differs). Return empty rather than bubble an error up the
			// poll loop.
			return map[string][]Point{}, nil
		}
		return nil, fmt.Errorf("open %s: %w", historyPath, err)
	}
	defer f.Close()

	series := map[string][]Point{}

	scanner := bufio.NewScanner(f)
	// wandb lines can carry large histogram payloads — bump the buffer
	// from the 64KiB default so we don't choke on them (we skip the
	// field itself, but the scanner still has to read the whole line).
	buf := make([]byte, 0, 1<<16)
	scanner.Buffer(buf, 1<<24) // up to 16 MiB per line

	lineNo := 0
	for scanner.Scan() {
		// Cooperate with cancellation on long files.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal(line, &obj); err != nil {
			// A single corrupt row shouldn't blow up the whole poll; skip.
			continue
		}
		stepRaw, ok := obj["_step"]
		if !ok {
			continue
		}
		step, ok := asInt(stepRaw)
		if !ok {
			continue
		}
		for k, v := range obj {
			// Skip wandb metadata (everything underscore-prefixed) and the
			// step field we already consumed.
			if len(k) == 0 || k[0] == '_' {
				continue
			}
			f, ok := asFloat(v)
			if !ok {
				continue
			}
			series[k] = append(series[k], Point{Step: step, Value: f})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", historyPath, err)
	}

	for k, pts := range series {
		series[k] = dedupByStep(pts)
	}
	return series, nil
}

// asFloat coerces JSON scalars to float64. json.Unmarshal into map[string]any
// decodes numbers as float64, bools as bool; we accept only numbers and
// finite bools (true=1, false=0). Strings / arrays / null / nested objects
// return false.
func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

// asInt coerces a JSON scalar to int64 for the _step field. wandb writes
// _step as an integer, but json.Unmarshal into any produces float64 — so
// accept both shapes.
func asInt(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case int:
		return int64(x), true
	case int64:
		return x, true
	default:
		return 0, false
	}
}

// dedupByStep collapses duplicate steps to the last value seen. Input
// must already be sorted by step ascending. wandb resume/rewind can
// cause the same _step to appear more than once — take the most recent.
func dedupByStep(pts []Point) []Point {
	if len(pts) == 0 {
		return pts
	}
	// Stable-sort guarantees the last duplicate wins when we rewrite.
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
