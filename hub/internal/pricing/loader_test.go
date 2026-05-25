package pricing

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorderWarner is a Warner that captures audit dispatches for
// assertion. Concurrency-safe so loader tests that touch goroutines
// stay correct under -race.
type recorderWarner struct {
	mu      sync.Mutex
	entries []warnEntry
}

type warnEntry struct {
	Action  string
	Summary string
	Meta    map[string]any
}

func (r *recorderWarner) record(action, summary string, meta map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, warnEntry{Action: action, Summary: summary, Meta: meta})
}

func (r *recorderWarner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

func (r *recorderWarner) actions() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.entries))
	for i, e := range r.entries {
		out[i] = e.Action
	}
	return out
}

// TestEmbeddedDefaultParses guards the //go:embed YAML — a build
// shipping a malformed embedded default would panic at first chip
// render. This test catches it at CI time.
func TestEmbeddedDefaultParses(t *testing.T) {
	t.Setenv(envOverridePath, "")
	t.Setenv(envHubData, t.TempDir()) // ensure no override file exists
	loader := NewLoader(nil)
	tbl := loader.Resolve()
	if tbl == nil {
		t.Fatal("Resolve returned nil for embedded default")
	}
	if tbl.Origin != OriginEmbedded {
		t.Errorf("Origin = %q, want %q", tbl.Origin, OriginEmbedded)
	}
	if tbl.SnapshotDate == "" {
		t.Error("embedded SnapshotDate must be set in YAML header")
	}
	if _, err := tbl.RateFor("claude-opus-4-7"); err != nil {
		t.Errorf("embedded must list claude-opus-4-7: %v", err)
	}
	if _, err := tbl.RateFor("claude-sonnet-4-6"); err != nil {
		t.Errorf("embedded must list claude-sonnet-4-6: %v", err)
	}
}

// TestEnvOverridePathWins exercises tier 1 of the three-tier
// resolution (env var override of the on-disk path).
func TestEnvOverridePathWins(t *testing.T) {
	dir := t.TempDir()
	overridePath := filepath.Join(dir, "custom-pricing.yaml")
	writeYAML(t, overridePath, sampleYAML("2026-09-09", "claude-test-1", 1.0, 2.0, 0.1, 0.2))
	t.Setenv(envOverridePath, overridePath)

	loader := NewLoader(nil).WithHubData(t.TempDir())
	tbl := loader.Resolve()
	if tbl.Origin != OriginOperator {
		t.Errorf("Origin = %q, want %q", tbl.Origin, OriginOperator)
	}
	if tbl.SnapshotDate != "2026-09-09" {
		t.Errorf("SnapshotDate = %q, want 2026-09-09", tbl.SnapshotDate)
	}
	r, err := tbl.RateFor("claude-test-1")
	if err != nil {
		t.Fatalf("RateFor: %v", err)
	}
	if r.InputPerMillion != 1.0 {
		t.Errorf("InputPerMillion = %v, want 1.0", r.InputPerMillion)
	}
}

// TestDefaultDiskPath exercises tier 2: env unset, file present at
// `$HUB_DATA/pricing/claude.yaml`.
func TestDefaultDiskPath(t *testing.T) {
	hubData := t.TempDir()
	path := filepath.Join(hubData, "pricing", "claude.yaml")
	writeYAML(t, path, sampleYAML("2026-08-08", "claude-disk", 5.0, 25.0, 0.5, 6.0))
	t.Setenv(envOverridePath, "")
	t.Setenv(envHubData, hubData)

	loader := NewLoader(nil)
	tbl := loader.Resolve()
	if tbl.Origin != OriginOperator {
		t.Fatalf("Origin = %q, want %q (resolved %q)",
			tbl.Origin, OriginOperator, loader.resolvedPath())
	}
	if tbl.SnapshotDate != "2026-08-08" {
		t.Errorf("SnapshotDate = %q, want 2026-08-08", tbl.SnapshotDate)
	}
}

// TestEmbeddedFallbackWhenOverrideMissing — neither tier 1 nor tier 2
// path resolves; loader returns embedded with origin set accordingly.
func TestEmbeddedFallbackWhenOverrideMissing(t *testing.T) {
	t.Setenv(envOverridePath, "")
	t.Setenv(envHubData, t.TempDir()) // empty dir, no pricing/claude.yaml

	loader := NewLoader(nil)
	tbl := loader.Resolve()
	if tbl.Origin != OriginEmbedded {
		t.Errorf("Origin = %q, want %q", tbl.Origin, OriginEmbedded)
	}
}

// TestParseErrorWarnsAndFallsThrough — an unparseable override file
// must (a) NOT crash the chip and (b) warn-audit. The chip must
// render with the embedded rates.
func TestParseErrorWarnsAndFallsThrough(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(path, []byte("not: valid: yaml: [oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envOverridePath, path)
	t.Setenv(envHubData, t.TempDir())

	rec := &recorderWarner{}
	loader := NewLoader(rec.record)
	tbl := loader.Resolve()
	if tbl.Origin != OriginEmbedded {
		t.Errorf("Origin = %q, want fall-through to %q", tbl.Origin, OriginEmbedded)
	}
	if rec.count() != 1 {
		t.Fatalf("warn count = %d, want 1; actions = %v", rec.count(), rec.actions())
	}
	if got := rec.actions()[0]; got != "pricing.config_error" {
		t.Errorf("warn action = %q, want pricing.config_error", got)
	}
}

// TestValidationFailureWarnsAndFallsThrough — file parses but fails
// Validate (empty models map). Same fall-through behaviour as a yaml
// parse error.
func TestValidationFailureWarnsAndFallsThrough(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nsnapshot_date: 2026-01-01\nmodels: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envOverridePath, path)
	t.Setenv(envHubData, t.TempDir())

	rec := &recorderWarner{}
	loader := NewLoader(rec.record)
	tbl := loader.Resolve()
	if tbl.Origin != OriginEmbedded {
		t.Errorf("Origin = %q, want fall-through to %q", tbl.Origin, OriginEmbedded)
	}
	if rec.count() != 1 {
		t.Fatalf("warn count = %d, want 1", rec.count())
	}
	if got := rec.actions()[0]; got != "pricing.config_error" {
		t.Errorf("warn action = %q, want pricing.config_error", got)
	}
	if !strings.Contains(rec.entries[0].Summary, "empty.yaml") {
		t.Errorf("warn summary %q lacks file path", rec.entries[0].Summary)
	}
}

// TestMtimeReload — operator edits the file mid-process; the next
// Resolve picks up the new content without restart.
func TestMtimeReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pricing.yaml")
	writeYAML(t, path, sampleYAML("2026-01-01", "claude-A", 1.0, 2.0, 0.1, 0.2))
	t.Setenv(envOverridePath, path)
	t.Setenv(envHubData, t.TempDir())

	loader := NewLoader(nil)
	first := loader.Resolve()
	if first.SnapshotDate != "2026-01-01" {
		t.Fatalf("first SnapshotDate = %q", first.SnapshotDate)
	}

	// Bump mtime to a clearly newer value AND rewrite content. Some
	// filesystems have 1s mtime granularity, so we explicitly stamp
	// the future mtime via Chtimes rather than relying on the write.
	writeYAML(t, path, sampleYAML("2026-12-31", "claude-A", 9.0, 9.0, 0.9, 0.9))
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	second := loader.Resolve()
	if second.SnapshotDate != "2026-12-31" {
		t.Errorf("second SnapshotDate = %q, want 2026-12-31", second.SnapshotDate)
	}
	r, _ := second.RateFor("claude-A")
	if r.InputPerMillion != 9.0 {
		t.Errorf("after reload InputPerMillion = %v, want 9.0", r.InputPerMillion)
	}
}

// TestMtimeUnchangedCachedReturn — same mtime → same pointer returned.
// Loader must not re-parse the file on every Resolve call (the chip
// renders every few seconds; repeated YAML decode would be wasteful).
func TestMtimeUnchangedCachedReturn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pricing.yaml")
	writeYAML(t, path, sampleYAML("2026-01-01", "claude-X", 1.0, 2.0, 0.1, 0.2))
	t.Setenv(envOverridePath, path)
	t.Setenv(envHubData, t.TempDir())

	loader := NewLoader(nil)
	first := loader.Resolve()
	second := loader.Resolve()
	if first != second {
		t.Errorf("Resolve returned a fresh pointer on unchanged mtime: %p vs %p", first, second)
	}
}

// TestUnknownModelReturnsErrUnknownModel — model not in table maps to
// the package-level sentinel, not nil rate.
func TestUnknownModelReturnsErrUnknownModel(t *testing.T) {
	loader := NewLoader(nil).WithHubData(t.TempDir())
	t.Setenv(envOverridePath, "")
	tbl := loader.Resolve()
	if _, err := tbl.RateFor("claude-future-99"); err != ErrUnknownModel {
		t.Errorf("RateFor unknown = %v, want ErrUnknownModel", err)
	}
}

// --- helpers -----------------------------------------------------------

func writeYAML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// sampleYAML emits a valid pricing YAML with one model entry — enough
// to exercise the loader without coupling tests to the embedded
// claude_default.yaml's specific model list.
func sampleYAML(date, model string, in, out, cr, cw float64) string {
	var sb strings.Builder
	sb.WriteString("version: 1\n")
	sb.WriteString("snapshot_date: " + date + "\n")
	sb.WriteString("source_url: https://example.com/pricing\n")
	sb.WriteString("models:\n")
	sb.WriteString("  " + model + ":\n")
	sb.WriteString("    input_per_million: " + ftoa(in) + "\n")
	sb.WriteString("    output_per_million: " + ftoa(out) + "\n")
	sb.WriteString("    cache_read_per_million: " + ftoa(cr) + "\n")
	sb.WriteString("    cache_write_per_million: " + ftoa(cw) + "\n")
	return sb.String()
}

func ftoa(f float64) string {
	// 6 decimal places is plenty for token-level rates; trim the
	// trailing zeros so the YAML stays readable.
	s := strconv.FormatFloat(f, 'f', 6, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}
