package hostrunner

import (
	"context"
	"time"

	"github.com/termipod/hub/internal/hostrunner/tbreader"
)

// tbPollLoop polls the local TensorBoard logdir for every run whose
// trackio_host_id points at this host and whose trackio_run_uri is a
// tb:// URL (§6.5, P3.1b — parallel to the trackio loop). The schema
// fields are reused because the hub's digest endpoint doesn't care
// which reader produced the data; the wire format is identical.
//
// Runs with no matching URI scheme are silently skipped — either the
// worker logs through trackio (handled by trackioPollLoop) or the URI
// is unparseable and we'd log a warning on the next sweep anyway.
func (a *Runner) tbPollLoop(ctx context.Context) {
	t := time.NewTicker(a.TensorBoardPollInterval)
	defer t.Stop()
	a.tbTick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.tbTick(ctx)
		}
	}
}

// tbTick is factored out so the loop body is unit-testable without
// waiting on ticker cadence — tb_poll_test drives it directly.
func (a *Runner) tbTick(ctx context.Context) {
	runs, err := a.Client.ListRunsForHost(ctx, a.HostID)
	if err != nil {
		a.Log.Warn("list runs failed (tb)", "err", err)
		return
	}
	for _, r := range runs {
		if r.TrackioRunURI == "" {
			continue
		}
		// Only act on tb:// URIs — trackio:// rows belong to
		// trackioPollLoop even if this loop happens to be running
		// too. ParseURI handles the scheme check; a non-tb URI
		// surfaces as a parse error which we skip silently.
		u, err := tbreader.ParseURI(r.TrackioRunURI)
		if err != nil {
			continue
		}
		if err := a.tbPollOne(ctx, r, u); err != nil {
			a.Log.Warn("tb poll failed",
				"run", r.ID, "uri", r.TrackioRunURI, "err", err)
		}
	}
}

func (a *Runner) tbPollOne(ctx context.Context, r Run, u tbreader.URI) error {
	series, err := tbreader.ReadRun(a.TensorBoardDir, u.RunPath)
	if err != nil {
		return err
	}
	if len(series) == 0 {
		// Worker hasn't logged yet, or the run dir is still empty.
		return nil
	}

	metrics := make([]MetricPoints, 0, len(series))
	for name, pts := range series {
		downs := tbreader.Downsample(pts, a.TensorBoardMaxPoints)
		row := MetricPoints{
			Name:        name,
			Points:      toWirePointsTB(downs),
			SampleCount: int64(len(pts)),
		}
		// last_step / last_value come from the un-downsampled series
		// so the mobile headline matches TensorBoard's own scalar
		// panel, not whichever sample survived the stride.
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

// toWirePointsTB mirrors toWirePoints from the trackio side but
// operates on tbreader.Point. Kept separate so the two readers don't
// have to share a Point type across packages.
func toWirePointsTB(pts []tbreader.Point) [][2]any {
	out := make([][2]any, len(pts))
	for i, p := range pts {
		out[i] = [2]any{p.Step, p.Value}
	}
	return out
}
