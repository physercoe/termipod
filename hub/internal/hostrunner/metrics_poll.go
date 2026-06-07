package hostrunner

import (
	"context"
	"strings"
	"time"

	"github.com/termipod/hub/internal/hostrunner/metrics"
)

// metricsPollLoop drives one metrics.Reader. It's the shared poll
// skeleton for every metric-source backend (trackio, wandb,
// TensorBoard, …): list runs, filter by the reader's URI scheme, read
// each run's series, downsample, and PUT the digest to the hub
// (§6.5, P3.1b). The reader identifies itself via its scheme so the
// loop has no per-vendor branching.
//
// Runs with no matching URI scheme, an unparseable URI, or an empty
// metric set are skipped silently — on the next tick they'll either
// have data or still not, and either way raising an error at the
// runner level would be noise.
func (a *Runner) metricsPollLoop(ctx context.Context, r metrics.Reader, interval time.Duration, maxPoints int) {
	t := time.NewTicker(interval)
	defer t.Stop()
	a.metricsTick(ctx, r, maxPoints)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.metricsTick(ctx, r, maxPoints)
		}
	}
}

// metricsTick is factored out so the loop body is unit-testable without
// waiting on ticker cadence — tests can drive it directly.
func (a *Runner) metricsTick(ctx context.Context, r metrics.Reader, maxPoints int) {
	runs, err := a.Client.ListRunsForHost(ctx, a.HostID)
	if err != nil {
		a.Log.Warn("list runs failed", "scheme", r.Scheme(), "err", err)
		return
	}
	prefix := r.Scheme() + "://"
	for _, run := range runs {
		if !strings.HasPrefix(run.TrackioRunURI, prefix) {
			continue
		}
		err := a.metricsPollOne(ctx, r, run, maxPoints)
		// One failing run shouldn't abort the whole sweep. Report the
		// outcome edge-triggered (see notePollOutcome) so a persistently
		// failing run — or a transient lock that the busy_timeout didn't
		// fully absorb — logs once on the falling edge and once on
		// recovery, never identically every tick.
		a.notePollOutcome(r.Scheme(), run.ID, run.TrackioRunURI, err)
	}
}

// notePollOutcome edge-triggers the metric-poll log. A run that keeps
// failing logs a single WARN on the first failure (the falling edge) and a
// single INFO when it next succeeds (recovery); the steady-state — whether
// healthy or stuck — is silent. This is the same dedup discipline the idle
// detector uses (runner.go tickIdle), applied to the poll loop that the
// tester saw spamming an identical SQLITE_BUSY WARN every interval.
//
// A poll-failure is host-runner operational health, NOT a director- or
// steward-facing signal: it is most often transient (a writer lock) or
// benign (a malformed run URI), so it stays in the local log and is not
// raised as an attention item. Genuinely director-meaningful run health
// surfaces through the run's own alerts digest (pollRunExtras → PutRunAlerts).
func (a *Runner) notePollOutcome(scheme, runID, uri string, err error) {
	key := scheme + "\x00" + runID
	a.pollMu.Lock()
	if a.pollFail == nil {
		a.pollFail = map[string]bool{}
	}
	wasFailing := a.pollFail[key]
	switch {
	case err != nil && !wasFailing:
		a.pollFail[key] = true
	case err == nil && wasFailing:
		delete(a.pollFail, key)
	}
	a.pollMu.Unlock()

	switch {
	case err != nil && !wasFailing:
		a.Log.Warn("metric poll failed",
			"scheme", scheme, "run", runID, "uri", uri, "err", err)
	case err != nil:
		// Still failing — already logged on the falling edge. Keep the
		// detail available at debug for anyone tailing -v, but off the
		// default WARN stream.
		a.Log.Debug("metric poll still failing",
			"scheme", scheme, "run", runID, "err", err)
	case wasFailing:
		a.Log.Info("metric poll recovered", "scheme", scheme, "run", runID)
	}
}

func (a *Runner) metricsPollOne(ctx context.Context, r metrics.Reader, run Run, maxPoints int) error {
	series, err := r.Read(ctx, run.TrackioRunURI)
	if err != nil {
		return err
	}
	if len(series) > 0 {
		if err := a.Client.PutRunMetrics(ctx, run.ID, seriesToWire(series, maxPoints)); err != nil {
			return err
		}
	}
	// Extras (config / system_metrics / alerts) are an OPTIONAL capability —
	// only trackio implements metrics.RunExtras today. Poll them independently
	// of the scalar series so a run that has only config or only alerts still
	// surfaces.
	if rx, ok := r.(metrics.RunExtras); ok {
		if err := a.pollRunExtras(ctx, rx, run, maxPoints); err != nil {
			return err
		}
	}
	return nil
}

// pollRunExtras reads the run's config / system series / alerts and PUTs each
// non-empty digest. Each piece is independent: a missing one is skipped, not an
// error (the reader returns empty for "nothing logged yet").
func (a *Runner) pollRunExtras(ctx context.Context, rx metrics.RunExtras, run Run, maxPoints int) error {
	cfg, err := rx.ReadConfig(ctx, run.TrackioRunURI)
	if err != nil {
		return err
	}
	if len(cfg) > 0 {
		if err := a.Client.PutRunConfig(ctx, run.ID, cfg); err != nil {
			return err
		}
	}

	sys, err := rx.ReadSystemMetrics(ctx, run.TrackioRunURI)
	if err != nil {
		return err
	}
	if len(sys) > 0 {
		if err := a.Client.PutRunSystemMetrics(ctx, run.ID, seriesToWire(sys, maxPoints)); err != nil {
			return err
		}
	}

	alerts, err := rx.ReadAlerts(ctx, run.TrackioRunURI)
	if err != nil {
		return err
	}
	if len(alerts) > 0 {
		if err := a.Client.PutRunAlerts(ctx, run.ID, alerts); err != nil {
			return err
		}
	}
	return nil
}

// seriesToWire downsamples each named series and snapshots its last point into
// the MetricPoints wire shape. last_step / last_value come from the
// un-downsampled series so the mobile headline number matches the source
// tracker's own dashboard, not whichever point survived downsampling. Shared by
// the scalar-metric and system-metric polls.
func seriesToWire(series map[string]metrics.Series, maxPoints int) []MetricPoints {
	out := make([]MetricPoints, 0, len(series))
	for name, pts := range series {
		downs := metrics.Downsample(pts, maxPoints)
		row := MetricPoints{
			Name:        name,
			Points:      toWirePoints(downs),
			SampleCount: int64(len(pts)),
		}
		if len(pts) > 0 {
			last := pts[len(pts)-1]
			step := last.Step
			val := last.Value
			row.LastStep = &step
			row.LastValue = &val
		}
		out = append(out, row)
	}
	return out
}

// toWirePoints converts a metrics.Series to the [[step, value], ...]
// JSON shape PutRunMetrics ships to the hub. The [2]any is
// json.Marshal-friendly and round-trips to the hub's json.RawMessage
// points_json.
func toWirePoints(pts metrics.Series) [][2]any {
	out := make([][2]any, len(pts))
	for i, p := range pts {
		out[i] = [2]any{p.Step, p.Value}
	}
	return out
}
