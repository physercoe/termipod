package hostrunner

import (
	"context"
	"time"

	"github.com/termipod/hub/internal/hostrunner/trackio"
)

// trackioPollLoop polls the local trackio SQLite store for every run
// whose trackio_host_id points at this host, downsamples each metric
// series, and PUTs the digest to the hub (§6.5, P3.1b).
//
// Runs with no trackio_run_uri, an unparseable URI, or an empty metric
// set are skipped silently — on the next tick they'll either have data
// or still not, and either way raising an error at the runner level
// would be noise.
func (a *Runner) trackioPollLoop(ctx context.Context) {
	t := time.NewTicker(a.TrackioPollInterval)
	defer t.Stop()
	a.trackioTick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.trackioTick(ctx)
		}
	}
}

// trackioTick is factored out so the loop body is unit-testable without
// waiting on ticker cadence — runner_test can drive it directly.
func (a *Runner) trackioTick(ctx context.Context) {
	runs, err := a.Client.ListRunsForHost(ctx, a.HostID)
	if err != nil {
		a.Log.Warn("list runs failed", "err", err)
		return
	}
	for _, r := range runs {
		if r.TrackioRunURI == "" {
			continue
		}
		if err := a.trackioPollOne(ctx, r); err != nil {
			// One failing run shouldn't abort the whole sweep; log and move on.
			a.Log.Warn("trackio poll failed",
				"run", r.ID, "uri", r.TrackioRunURI, "err", err)
		}
	}
}

func (a *Runner) trackioPollOne(ctx context.Context, r Run) error {
	u, err := trackio.ParseURI(r.TrackioRunURI)
	if err != nil {
		return err
	}
	dbPath := trackio.ProjectDBPath(a.TrackioDir, u.Project)
	series, err := trackio.ReadRun(ctx, dbPath, u.RunName)
	if err != nil {
		return err
	}
	if len(series) == 0 {
		// Worker hasn't logged yet. Nothing to push.
		return nil
	}

	metrics := make([]MetricPoints, 0, len(series))
	for name, pts := range series {
		downs := trackio.Downsample(pts, a.TrackioMaxPoints)
		row := MetricPoints{
			Name:        name,
			Points:      toWirePoints(downs),
			SampleCount: int64(len(pts)),
		}
		// last_step / last_value come from the un-downsampled series so
		// mobile can render a headline number that matches what trackio's
		// own dashboard shows, not whichever point survived downsampling.
		if len(pts) > 0 {
			last := pts[len(pts)-1]
			step := last.Step
			val := last.Value
			row.LastStep = &step
			row.LastValue = &val
		}
		metrics = append(metrics, row)
	}
	return a.Client.PutRunMetrics(ctx, r.ID, metrics)
}

// toWirePoints converts []trackio.Point to the [[step, value], ...]
// JSON shape PutRunMetrics ships to the hub. The [2]any is json.Marshal
// friendly and round-trips to the hub's json.RawMessage points_json.
func toWirePoints(pts []trackio.Point) [][2]any {
	out := make([][2]any, len(pts))
	for i, p := range pts {
		out[i] = [2]any{p.Step, p.Value}
	}
	return out
}
