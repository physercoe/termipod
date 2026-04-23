package tbreader

// Downsample reduces a scalar series to at most max points using
// uniform stride sampling, always keeping the first and last points.
// Copied verbatim from the trackio reader's downsampler — the
// hub-side contract (≤100 points, endpoints preserved, first-and-last
// for max==1 returns the last) is identical regardless of the source
// reader. We duplicate the code rather than depend on trackio so the
// tbreader has no cross-package data-structure coupling.
//
// Constraints:
//   - max <= 0 is treated as "no limit" (the full series is returned)
//   - max == 1 returns the last point
//   - input is assumed sorted by step ascending with duplicates folded
//
// The returned slice is a fresh allocation so callers can safely
// mutate it.
func Downsample(pts []Point, max int) []Point {
	if max <= 0 || len(pts) <= max {
		out := make([]Point, len(pts))
		copy(out, pts)
		return out
	}
	if max == 1 {
		return []Point{pts[len(pts)-1]}
	}

	n := len(pts)
	out := make([]Point, 0, max)
	last := n - 1
	inner := max - 1
	for i := 0; i < inner; i++ {
		idx := (i * last) / inner
		out = append(out, pts[idx])
	}
	out = append(out, pts[last])
	return out
}
