// Package trackio is the metrics.Reader backend for a local trackio
// SQLite store (§6.5, P3.1).
//
// Trackio is wandb-compatible and stores one SQLite file per project
// (`{project}.db`) under TRACKIO_DIR (default ~/.cache/huggingface/trackio).
// Scalar metrics live in a single `metrics` table. Verified against
// gradio-app/trackio main (2026-06-03, trackio/sqlite_storage.py):
//
//	metrics(id INTEGER, run_id TEXT, timestamp TEXT, run_name TEXT,
//	        step INTEGER, metrics TEXT /* JSON like {"loss":1.23,"acc":0.7} */,
//	        log_id TEXT, space_id TEXT)
//
// We read only (run_name, step, metrics) with a COLUMN-SPECIFIC SELECT, so the
// columns upstream has added since this driver shipped (run_id / log_id /
// space_id, plus the sibling system_metrics / configs / traces / alerts tables)
// are forward-compatible and need no change here. run_name + step + metrics have
// kept their names and types; that is the contract this driver depends on.
//
// URI scheme: trackio://<project>/<run_name>. (Upstream stores the DB under a
// *sanitized* project name — alphanumerics, '-' and '_'; a project whose name
// carries other characters would land at a different path than ProjectDBPath
// builds. Run names round-tripped through trackio.init are normally already
// safe, so this is a latent edge, not an observed break.)
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
	"strings"
	"unicode"

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
// project. trackio writes `{safeProject}.db` directly under its root dir,
// where the filename is the *sanitized* project name — see SafeProjectName.
func ProjectDBPath(root, project string) string {
	return filepath.Join(root, SafeProjectName(project)+".db")
}

// SafeProjectName mirrors trackio's own DB-filename sanitization
// (trackio/sqlite_storage.py: get_project_db_filename, verified against
// gradio-app/trackio main 2026-06-03):
//
//	safe = "".join(c for c in project if c.isalnum() or c in ("-","_")).rstrip()
//	if not safe: safe = "default"
//
// We must match it exactly or we'd stat the wrong `.db` and silently report
// the run as empty. Python's str.isalnum is Unicode-aware, so we keep Unicode
// letters/numbers (not just ASCII), plus '-' and '_'; everything else (spaces,
// '/', '.', ':' …) is dropped. An empty result falls back to "default".
func SafeProjectName(project string) string {
	var b strings.Builder
	for _, r := range project {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	// trackio applies .rstrip() after the filter; whitespace is already
	// dropped by the keep-set, so this only matters for fidelity. Kept so the
	// rule reads 1:1 with upstream.
	safe := strings.TrimRightFunc(b.String(), unicode.IsSpace)
	if safe == "" {
		return "default"
	}
	return safe
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

// ReadConfig implements metrics.RunExtras.
func (s *Source) ReadConfig(ctx context.Context, uri string) (map[string]any, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}
	return ReadConfig(ctx, ProjectDBPath(s.Root, u.Project), u.RunName)
}

// ReadSystemMetrics implements metrics.RunExtras.
func (s *Source) ReadSystemMetrics(ctx context.Context, uri string) (map[string]metrics.Series, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}
	return ReadSystemMetrics(ctx, ProjectDBPath(s.Root, u.Project), u.RunName)
}

// ReadAlerts implements metrics.RunExtras.
func (s *Source) ReadAlerts(ctx context.Context, uri string) ([]metrics.Alert, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}
	return ReadAlerts(ctx, ProjectDBPath(s.Root, u.Project), u.RunName)
}

// openRO opens a trackio project DB read-only. A missing file is reported as
// (nil, nil) — the project hasn't been created yet — so callers can return an
// empty result without bubbling an error up the poll loop.
func openRO(dbPath string) (*sql.DB, error) {
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", dbPath, err)
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", dbPath))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dbPath, err)
	}
	return db, nil
}

// isNoSuchTable reports whether err is sqlite's "no such table" — an older
// trackio store (or a brand-new DB that only has the metrics table) may not
// carry every sibling table yet. We treat that as "nothing logged" rather than
// a failure so the poller stays quiet.
func isNoSuchTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

// ReadConfig loads the run's config (hyperparameters) from trackio's `configs`
// table — one JSON row per run. Returns nil (not an error) when the DB or the
// config row is absent.
func ReadConfig(ctx context.Context, dbPath, runName string) (map[string]any, error) {
	db, err := openRO(dbPath)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()

	var payload string
	err = db.QueryRowContext(ctx,
		`SELECT config FROM configs WHERE run_name = ? ORDER BY id DESC LIMIT 1`,
		runName).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if isNoSuchTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query config for %s: %w", runName, err)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		// A corrupt config row shouldn't fail the whole poll.
		return nil, nil
	}
	return obj, nil
}

// ReadSystemMetrics loads the run's system/utilization series from trackio's
// `system_metrics` table. Unlike user metrics these are TIME-keyed (no step),
// so we use a 0-based sample ordinal (rows ordered by timestamp) as the x-axis;
// every numeric key in a row's JSON gets a point at that ordinal. Returns an
// empty map when the DB / table / rows are absent.
func ReadSystemMetrics(ctx context.Context, dbPath, runName string) (map[string]metrics.Series, error) {
	db, err := openRO(dbPath)
	if err != nil || db == nil {
		return map[string]metrics.Series{}, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx,
		`SELECT metrics FROM system_metrics WHERE run_name = ? ORDER BY timestamp ASC, id ASC`,
		runName)
	if isNoSuchTable(err) {
		return map[string]metrics.Series{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query system_metrics for %s: %w", runName, err)
	}
	defer rows.Close()

	series := map[string]metrics.Series{}
	var ordinal int64
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(payload), &obj); err != nil {
			ordinal++
			continue
		}
		for k, v := range obj {
			f, ok := asFloat(v)
			if !ok {
				continue
			}
			series[k] = append(series[k], metrics.Point{Step: ordinal, Value: f})
		}
		ordinal++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return series, nil
}

// ReadAlerts loads the run's alerts from trackio's `alerts` table, oldest
// first. Returns an empty slice when the DB / table / rows are absent.
func ReadAlerts(ctx context.Context, dbPath, runName string) ([]metrics.Alert, error) {
	db, err := openRO(dbPath)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT timestamp, title, COALESCE(text, ''), COALESCE(level, ''),
		       step, COALESCE(alert_id, '')
		FROM alerts WHERE run_name = ? ORDER BY timestamp ASC, id ASC`,
		runName)
	if isNoSuchTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query alerts for %s: %w", runName, err)
	}
	defer rows.Close()

	var out []metrics.Alert
	for rows.Next() {
		var (
			ts, title, text, level, alertID string
			step                            sql.NullInt64
		)
		if err := rows.Scan(&ts, &title, &text, &level, &step, &alertID); err != nil {
			return nil, err
		}
		a := metrics.Alert{TS: ts, Title: title, Text: text, Level: level, AlertID: alertID}
		if step.Valid {
			v := step.Int64
			a.Step = &v
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
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
