// Package wandb is the metrics.Reader backend for a local wandb
// offline-run directory (§6.5, P3.1).
//
// wandb's offline mode writes one run directory per run under WANDB_DIR
// (commonly ./wandb). Each run directory (e.g. `run-20260423_123456-abc123`)
// contains a `files/wandb-history.jsonl` file. Each line is a JSON
// object describing one logged step, typically of the form:
//
//	{"_step": 0, "_timestamp": 1700000000, "loss": 2.5, "acc": 0.1}
//
// Keys starting with `_` are wandb metadata. `_step` is the
// authoritative training step. Non-underscore scalar-numeric keys are
// user-logged metrics — everything else (strings, nested objects,
// arrays for histograms/images, null) is skipped silently.
//
// URI scheme: wandb://<project>/<run-dir>.
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

	"github.com/termipod/hub/internal/hostrunner/metrics"
)

// Scheme is the URI scheme wandb runs use on runs.trackio_run_uri.
const Scheme = "wandb"

// Source is the metrics.Reader for a local wandb offline-run tree rooted
// at Root. Construct via New; the zero value will report empty series
// for every run (open fails silently at the file level).
type Source struct {
	Root string
}

// New returns a metrics.Reader for the wandb tree under root. Root is
// typically the operator-supplied --wandb-dir value or $WANDB_DIR.
func New(root string) *Source { return &Source{Root: root} }

// Scheme implements metrics.Reader.
func (s *Source) Scheme() string { return Scheme }

// Read implements metrics.Reader.
func (s *Source) Read(ctx context.Context, uri string) (map[string]metrics.Series, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}
	return ReadRun(ctx, RunHistoryPath(s.Root, u.RunDir))
}

// URI is a parsed wandb_run_uri. The canonical form is:
//
//	wandb://<project>/<run-dir>
//
// where <project> is the top-level wandb subdirectory name (typically
// the wandb project slug) and <run-dir> is the run identifier directory
// name (e.g. `run-20260423_123456-abc123`).
type URI struct {
	Project string
	RunDir  string
}

// ParseURI accepts wandb://<project>/<run-dir> and returns the parts.
func ParseURI(raw string) (URI, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return URI{}, fmt.Errorf("parse uri: %w", err)
	}
	if u.Scheme != Scheme {
		return URI{}, fmt.Errorf("unsupported scheme %q, want %s://", u.Scheme, Scheme)
	}
	project := u.Host
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

// DefaultDir returns $WANDB_DIR if set. wandb's own default is the
// caller's cwd (`./wandb`), which is useless from the host-runner's
// perspective — prefer passing --wandb-dir explicitly. Returns "" when
// WANDB_DIR is unset so callers can detect "no default available".
func DefaultDir() string {
	return os.Getenv("WANDB_DIR")
}

// RunHistoryPath returns the absolute path of the wandb-history.jsonl
// file for a run-dir under root.
//
// Layout: <root>/<run-dir>/files/wandb-history.jsonl
func RunHistoryPath(root, runDir string) string {
	return filepath.Join(root, runDir, "files", "wandb-history.jsonl")
}

// ReadRun loads every scalar metric series for one run from the wandb
// offline-mode history file. A run with no recorded steps returns an
// empty map, not an error — callers treat that as "poll again later".
// A missing history file is likewise non-fatal.
func ReadRun(ctx context.Context, historyPath string) (map[string]metrics.Series, error) {
	f, err := os.Open(historyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]metrics.Series{}, nil
		}
		return nil, fmt.Errorf("open %s: %w", historyPath, err)
	}
	defer f.Close()

	series := map[string]metrics.Series{}

	scanner := bufio.NewScanner(f)
	// wandb lines can carry large histogram payloads — bump the buffer
	// from the 64KiB default so we don't choke on them (we skip the
	// field itself, but the scanner still has to read the whole line).
	buf := make([]byte, 0, 1<<16)
	scanner.Buffer(buf, 1<<24) // up to 16 MiB per line

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
			// Skip wandb metadata (everything underscore-prefixed) and
			// the step field we already consumed.
			if len(k) == 0 || k[0] == '_' {
				continue
			}
			f, ok := asFloat(v)
			if !ok {
				continue
			}
			series[k] = append(series[k], metrics.Point{Step: step, Value: f})
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
