package trackio

// Downsample reduces a scalar series to at most max points using uniform
// stride sampling, always keeping the first and last points. It's the
// cheap, predictable strategy — LTTB would preserve visual features
// better, but sparkline rendering on mobile cares more about correct
// endpoints and bounded row size than perceptual fidelity.
//
// Constraints:
//   - max <= 0 is treated as "no limit" (the full series is returned)
//   - max == 1 returns the last point (the final observation is the one
//     users care about most for a sparkline)
//   - duplicate steps should already be folded by dedupByStep
//
// Input is assumed sorted by step ascending. The returned slice is a
// fresh allocation so callers can safely mutate it.
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
	// Pick max-1 evenly spaced indices from [0, n-2], then always append
	// the final point. Rounding so the first sample is pts[0].
	last := n - 1
	inner := max - 1 // how many samples before the forced final
	for i := 0; i < inner; i++ {
		// idx spans [0, last) — multiply before divide to avoid truncating
		// to zero when inner >> last.
		idx := (i * last) / inner
		out = append(out, pts[idx])
	}
	out = append(out, pts[last])
	return out
}
