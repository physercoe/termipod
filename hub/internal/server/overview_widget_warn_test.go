package server

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// The template walker re-runs on every list-projects / list-templates /
// project-detail call. Pre-v1.0.650 it re-logged the same
// "unknown overview_widget" warning on every walk, which the user saw
// as multi-line-per-minute spam in journalctl. The once-per-key
// suppression keeps the first warning (so the operator still notices)
// and silences subsequent identical ones.
func TestWarnOverviewWidgetOnce_DedupesByPair(t *testing.T) {
	// Reset the dedup state so this test isn't sensitive to prior calls.
	overviewWidgetWarnMu.Lock()
	overviewWidgetWarnSeen = map[string]struct{}{}
	overviewWidgetWarnMu.Unlock()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	warnOverviewWidgetOnce("ablation-sweep", "sweep_compare")
	warnOverviewWidgetOnce("ablation-sweep", "sweep_compare")
	warnOverviewWidgetOnce("ablation-sweep", "sweep_compare")

	out := buf.String()
	if n := strings.Count(out, "unknown overview_widget"); n != 1 {
		t.Errorf("want 1 warning for repeat (template,widget); got %d\nlog:\n%s", n, out)
	}
}

// Distinct (template, widget) pairs each get their own warning — the
// dedup is per-key, not global. Otherwise a different stale template
// would land silently.
func TestWarnOverviewWidgetOnce_DistinctKeysEachWarn(t *testing.T) {
	overviewWidgetWarnMu.Lock()
	overviewWidgetWarnSeen = map[string]struct{}{}
	overviewWidgetWarnMu.Unlock()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	warnOverviewWidgetOnce("ablation-sweep", "sweep_compare")
	warnOverviewWidgetOnce("benchmark-comparison", "sweep_compare")
	warnOverviewWidgetOnce("ablation-sweep", "different_unknown")

	out := buf.String()
	if n := strings.Count(out, "unknown overview_widget"); n != 3 {
		t.Errorf("want 3 distinct warnings; got %d\nlog:\n%s", n, out)
	}
}
