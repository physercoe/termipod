// Package trackio is the metrics.Reader backend for a local trackio
// SQLite store (§6.5, P3.1).
//
// Trackio is wandb-compatible and stores one SQLite file per project
// under TRACKIO_DIR (default ~/.cache/huggingface/trackio). Its schema
// — as of https://huggingface.co/docs/trackio/storage_schema — keeps
// scalar metrics in a single table:
//
//	metrics(id INTEGER, timestamp TEXT, run_name TEXT, step INTEGER,
//	        metrics TEXT /* JSON blob like {"loss":1.23,"acc":0.7} */)
//
// URI scheme: trackio://<project>/<run_name>.
package trackio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"

	"github.com/termipod/hub/internal/hostrunner/metrics"

	// modernc.org/sqlite is CGO-free and already pulled in by the hub.
	_ "modernc.org/sqlite"
)

// Scheme is the URI scheme trackio runs use on runs.trackio_run_uri.
const Scheme = "trackio"

// Source is the metrics.Reader for a local trackio store rooted at Root.
// The zero value is not usable — construct via New.
type Source struct {
	Root string
}

// New returns a metrics.Reader for the trackio store under root. Root
// is typically trackio.DefaultDir() or the operator-supplied
// --trackio-dir value; passing "" produces a reader that will report
// every run as empty (the project DB stat fails), which keeps the
// poller silent on unconfigured hosts.
func New(root string) *Source { return &Source{Root: root} }

// Scheme implements metrics.Reader.
func (s *Source) Scheme() string { return Scheme }

// Read implements metrics.Reader.
func (s *Source) Read(ctx context.Context, uri string) (map[string]metrics.Series, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}
	return ReadRun(ctx, ProjectDBPath(s.Root, u.Project), u.RunName)
}

// URI is a parsed trackio_run_uri. The canonical form is:
//
//	trackio://<project>/<run_name>
//
// The worker agent writes this string onto runs.trackio_run_uri when it
// calls trackio.init; host-runner round-trips it here.
type URI struct {
	Project string
	RunName string
}

// ParseURI accepts trackio://<project>/<run> and returns the parts.
// Unknown schemes and empty components are errors — the poller skips
// runs whose URI it cannot parse rather than synthesizing defaults.
func ParseURI(raw string) (URI, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return URI{}, fmt.Errorf("parse uri: %w", err)
	}
	if u.Scheme != Scheme {
		return URI{}, fmt.Errorf("unsupported scheme %q, want %s://", u.Scheme, Scheme)
	}
	project := u.Host
	// u.Path starts with "/"; strip it so "/run-1" → "run-1".
	runName := ""
	if len(u.Path) > 0 && u.Path[0] == '/' {
		runName = u.Path[1:]
	} else {
		runName = u.Path
	}
	if project == "" || runName == "" {
		return URI{}, fmt.Errorf("trackio uri requires <project>/<run>: %q", raw)
	}
	return URI{Project: project, RunName: runName}, nil
}

// DefaultDir returns the directory trackio uses when TRACKIO_DIR is
// unset. Matches the published default of ~/.cache/huggingface/trackio.
// Returns "" when HOME is unresolvable.
func DefaultDir() string {
	if d := os.Getenv("TRACKIO_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".cache", "huggingface", "trackio")
}

// ProjectDBPath returns the absolute SQLite path trackio uses for a
// project. trackio writes `{project}.db` directly under its root dir.
func ProjectDBPath(root, project string) string {
	return filepath.Join(root, project+".db")
}

// ReadRun loads every scalar metric series for one run from the trackio
// SQLite store. The returned map is keyed by metric name (the JSON key
// inside metrics.metrics); values are sorted ascending by step and
// deduplicated on step (last write wins, matching trackio's own upsert
// semantics).
//
// A run with no recorded steps returns an empty map, not an error —
// callers should treat that as "poll again later" rather than a failure.
func ReadRun(ctx context.Context, dbPath string, runName string) (map[string]metrics.Series, error) {
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Project DB hasn't been created yet (worker hasn't logged).
			// Return empty rather than bubble an error up the poll loop.
			return map[string]metrics.Series{}, nil
		}
		return nil, fmt.Errorf("stat %s: %w", dbPath, err)
	}

	// mode=ro keeps us honest — we never want the poller to accidentally
	// mutate the trackio store. immutable=1 would skip the WAL, but the
	// worker is actively writing, so read-only + shared is correct.
	dsn := fmt.Sprintf("file:%s?mode=ro", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx,
		`SELECT step, metrics FROM metrics WHERE run_name = ? ORDER BY step ASC`,
		runName)
	if err != nil {
		return nil, fmt.Errorf("query metrics for %s: %w", runName, err)
	}
	defer rows.Close()

	// Per-metric ordered collectors. Using metrics.Series preserves the
	// ORDER BY; the dedup pass at the end folds steps that were logged
	// more than once.
	series := map[string]metrics.Series{}
	for rows.Next() {
		var step int64
		var payload string
		if err := rows.Scan(&step, &payload); err != nil {
			return nil, err
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(payload), &obj); err != nil {
			// A single corrupt row shouldn't blow up the whole poll; skip.
			continue
		}
		for k, v := range obj {
			f, ok := asFloat(v)
			if !ok {
				continue
			}
			series[k] = append(series[k], metrics.Point{Step: step, Value: f})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for k, pts := range series {
		series[k] = dedupByStep(pts)
	}
	return series, nil
}

// asFloat coerces JSON scalars to float64. json.Unmarshal into map[string]any
// decodes numbers as float64, bools as bool; we accept only numbers and
// finite bools (true=1, false=0). Strings / arrays / null return false.
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

// dedupByStep collapses duplicate steps to the last value seen. Input
// must already be sorted by step ascending. Trackio permits multiple
// rows with the same (run_name, step) — take the most recent.
func dedupByStep(pts metrics.Series) metrics.Series {
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
