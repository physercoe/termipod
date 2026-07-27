package selfupdate

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// NewCLIProgress returns an Options.OnProgress callback that renders a
// single self-refreshing status line ("downloading… 4.2/17.1 MB") to w
// so an operator running `self-update` on a slow link sees the download
// advance instead of a silent multi-minute hang. Writes are throttled
// to ~2/sec; a phase change always prints. Pass a DryRun's nil instead —
// dry runs resolve without downloading and produce no output.
func NewCLIProgress(w io.Writer) func(Progress) {
	p := &cliProgress{w: w}
	return p.report
}

type cliProgress struct {
	mu      sync.Mutex
	w       io.Writer
	last    time.Time
	lastPct int
}

func (p *cliProgress) report(pr Progress) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pct := -1
	if pr.Total > 0 {
		pct = int(pr.Done * 100 / pr.Total)
	}
	// Throttle steady-state byte updates; always print phase changes and
	// 100% so the line ends on a complete number.
	if pr.Phase == PhaseDownloading && pct == p.lastPct && time.Since(p.last) < 500*time.Millisecond {
		return
	}
	p.last = time.Now()
	p.lastPct = pct
	switch pr.Phase {
	case PhaseDownloading:
		if pr.Total > 0 {
			fmt.Fprintf(p.w, "\rdownloading… %s / %s (%d%%)   ",
				humanMB(pr.Done), humanMB(pr.Total), pct)
		} else {
			fmt.Fprintf(p.w, "\rdownloading… %s   ", humanMB(pr.Done))
		}
	default:
		fmt.Fprintf(p.w, "\r%s…   \n", pr.Phase)
	}
	if pr.Phase != PhaseDownloading || pct == 100 {
		fmt.Fprintln(p.w)
	}
}

// humanMB renders bytes as "12.3 MB" — the tarballs are always MiB-scale.
func humanMB(b int64) string {
	return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
}
