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
		if err := a.metricsPollOne(ctx, r, run, maxPoints); err != nil {
			// One failing run shouldn't abort the whole sweep.
			a.Log.Warn("metric poll failed",
				"scheme", r.Scheme(), "run", run.ID,
				"uri", run.TrackioRunURI, "err", err)
		}
	}
}

func (a *Runner) metricsPollOne(ctx context.Context, r metrics.Reader, run Run, maxPoints int) error {
	series, err := r.Read(ctx, run.TrackioRunURI)
	if err != nil {
		return err
	}
	if len(series) == 0 {
		// Worker hasn't logged yet. Nothing to push.
		return nil
	}

	out := make([]MetricPoints, 0, len(series))
	for name, pts := range series {
		downs := metrics.Downsample(pts, maxPoints)
		row := MetricPoints{
			Name:        name,
			Points:      toWirePoints(downs),
			SampleCount: int64(len(pts)),
		}
		// last_step / last_value come from the un-downsampled series so
		// the mobile headline number matches the source tracker's own
		// dashboard, not whichever point survived downsampling.
		if len(pts) > 0 {
			last := pts[len(pts)-1]
			step := last.Step
			val := last.Value
			row.LastStep = &step
			row.LastValue = &val
		}
		out = append(out, row)
	}
	return a.Client.PutRunMetrics(ctx, run.ID, out)
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
