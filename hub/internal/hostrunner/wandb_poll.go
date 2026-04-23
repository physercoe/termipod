package hostrunner

import (
	"context"
	"strings"
	"time"

	"github.com/termipod/hub/internal/hostrunner/wandb"
)

// wandbPollLoop polls the local wandb offline-run tree for every run
// whose trackio_host_id points at this host and whose trackio_run_uri
// carries the `wandb://` scheme, downsamples each metric series, and
// PUTs the digest to the hub (§6.5, P3.1b).
//
// This loop is independent of trackioPollLoop: each reader is fully
// self-contained and a shared MetricReader interface is a follow-up
// merge pass. Runs without a wandb:// URI, an unparseable URI, or an
// empty metric set are skipped silently — on the next tick they'll
// either have data or still not, and either way raising an error at
// the runner level would be noise.
func (a *Runner) wandbPollLoop(ctx context.Context) {
	t := time.NewTicker(a.WandbPollInterval)
	defer t.Stop()
	a.wandbTick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.wandbTick(ctx)
		}
	}
}

// wandbTick is factored out so the loop body is unit-testable without
// waiting on ticker cadence — tests can drive it directly.
func (a *Runner) wandbTick(ctx context.Context) {
	runs, err := a.Client.ListRunsForHost(ctx, a.HostID)
	if err != nil {
		a.Log.Warn("list runs failed", "err", err)
		return
	}
	for _, r := range runs {
		if !strings.HasPrefix(r.TrackioRunURI, "wandb://") {
			// Either trackio://, some other scheme, or empty — not our run.
			continue
		}
		if err := a.wandbPollOne(ctx, r); err != nil {
			// One failing run shouldn't abort the whole sweep; log and move on.
			a.Log.Warn("wandb poll failed",
				"run", r.ID, "uri", r.TrackioRunURI, "err", err)
		}
	}
}

func (a *Runner) wandbPollOne(ctx context.Context, r Run) error {
	u, err := wandb.ParseURI(r.TrackioRunURI)
	if err != nil {
		return err
	}
	historyPath := wandb.RunHistoryPath(a.WandbDir, u.RunDir)
	series, err := wandb.ReadRun(ctx, historyPath)
	if err != nil {
		return err
	}
	if len(series) == 0 {
		// Worker hasn't logged yet. Nothing to push.
		return nil
	}

	metrics := make([]MetricPoints, 0, len(series))
	for name, pts := range series {
		downs := wandb.Downsample(pts, a.WandbMaxPoints)
		row := MetricPoints{
			Name:        name,
			Points:      wandbToWirePoints(downs),
			SampleCount: int64(len(pts)),
		}
		// last_step / last_value come from the un-downsampled series so
		// mobile can render a headline number that matches what wandb's
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

// wandbToWirePoints converts []wandb.Point to the [[step, value], ...]
// JSON shape PutRunMetrics ships to the hub. Kept distinct from the
// trackio helper so the two readers stay independent per the constraint
// that no shared MetricReader interface exists yet.
func wandbToWirePoints(pts []wandb.Point) [][2]any {
	out := make([][2]any, len(pts))
	for i, p := range pts {
		out[i] = [2]any{p.Step, p.Value}
	}
	return out
}
