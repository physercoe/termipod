package datasetmeta

import (
	"encoding/json"
	"math"
	"sort"
	"unicode/utf8"
)

// rawFeatureStats is one feature's stats block as written by LeRobot. Every
// field is a nested array: scalars arrive as [x], a 7-DoF action as a 7-vector,
// and an image's per-channel stats as [[[r]],[[g]],[[b]]]. They are flattened
// in reading order, which keeps a component's position stable across both
// generations.
type rawFeatureStats struct {
	Min   json.RawMessage `json:"min"`
	Max   json.RawMessage `json:"max"`
	Mean  json.RawMessage `json:"mean"`
	Std   json.RawMessage `json:"std"`
	Count json.RawMessage `json:"count"`
}

// parseFeatureStats decodes one feature's stats block.
func parseFeatureStats(raw json.RawMessage) (FeatureStats, error) {
	var r rawFeatureStats
	if err := json.Unmarshal(raw, &r); err != nil {
		return FeatureStats{}, err
	}
	var fs FeatureStats
	var err error
	if fs.Min, err = flattenNumbers(r.Min); err != nil {
		return FeatureStats{}, err
	}
	if fs.Max, err = flattenNumbers(r.Max); err != nil {
		return FeatureStats{}, err
	}
	if fs.Mean, err = flattenNumbers(r.Mean); err != nil {
		return FeatureStats{}, err
	}
	if fs.Std, err = flattenNumbers(r.Std); err != nil {
		return FeatureStats{}, err
	}
	counts, err := flattenNumbers(r.Count)
	if err != nil {
		return FeatureStats{}, err
	}
	if len(counts) > 0 {
		fs.Count = int64(counts[0])
	}
	return fs, nil
}

// flattenNumbers flattens an arbitrarily nested JSON array of numbers into one
// slice, in reading order. A null or absent value flattens to nil.
func flattenNumbers(raw json.RawMessage) ([]float64, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	var out []float64
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case []any:
			for _, e := range t {
				walk(e)
			}
		case float64:
			out = append(out, t)
		case bool:
			// `next.done` is a bool feature and its stats come back as
			// booleans in some exports; treat them as 0/1 so the feature
			// still folds instead of silently vanishing.
			if t {
				out = append(out, 1)
			} else {
				out = append(out, 0)
			}
		}
	}
	walk(v)
	return out, nil
}

// statsAggregator folds per-episode stats into one dataset-level summary.
//
// This exists because v2.1 ships no dataset-level stats.json — only
// meta/episodes_stats.jsonl. The fold is exact, not an approximation:
// min/max are elementwise extremes, the mean is count-weighted, and the
// standard deviation is recovered from the second moment,
//
//	E[x²] = std² + mean²   per episode, weighted by that episode's count
//	std   = sqrt(E[x²] - mean²)   over the whole dataset
//
// Checked against reality rather than derived and hoped for: folding
// nyu_rot_dataset's v2.1 episodes_stats.jsonl reproduces the same dataset's
// v3.0 meta/stats.json across all 10 features to 2.5e-9 relative — float
// round-off. TestStatsFoldMatchesV30 pins that, and it is a genuinely
// independent check because the two sides share no code: different files,
// different formats, different decoders.
type statsAggregator struct {
	features map[string]*featureAccumulator
}

type featureAccumulator struct {
	min []float64
	max []float64
	// sum1 is Σ(mean_i·c_i) and sum2 is Σ((std_i²+mean_i²)·c_i).
	sum1  []float64
	sum2  []float64
	count int64
}

func newStatsAggregator() *statsAggregator {
	return &statsAggregator{features: map[string]*featureAccumulator{}}
}

func (a *statsAggregator) add(feature string, fs FeatureStats) {
	n := len(fs.Mean)
	if n == 0 || fs.Count <= 0 {
		return
	}
	acc := a.features[feature]
	if acc == nil {
		acc = &featureAccumulator{
			min:  append([]float64(nil), fs.Min...),
			max:  append([]float64(nil), fs.Max...),
			sum1: make([]float64, n),
			sum2: make([]float64, n),
		}
		a.features[feature] = acc
	}
	// A feature whose component count changes mid-dataset is corrupt; fold
	// only the overlap rather than panicking on an index.
	c := float64(fs.Count)
	for i := 0; i < n && i < len(acc.sum1); i++ {
		mean := fs.Mean[i]
		acc.sum1[i] += mean * c
		std := 0.0
		if i < len(fs.Std) {
			std = fs.Std[i]
		}
		acc.sum2[i] += (std*std + mean*mean) * c
	}
	for i := 0; i < len(fs.Min) && i < len(acc.min); i++ {
		if fs.Min[i] < acc.min[i] {
			acc.min[i] = fs.Min[i]
		}
	}
	for i := 0; i < len(fs.Max) && i < len(acc.max); i++ {
		if fs.Max[i] > acc.max[i] {
			acc.max[i] = fs.Max[i]
		}
	}
	acc.count += fs.Count
}

func (a *statsAggregator) result() map[string]FeatureStats {
	if len(a.features) == 0 {
		return nil
	}
	out := make(map[string]FeatureStats, len(a.features))
	for name, acc := range a.features {
		if acc.count <= 0 {
			continue
		}
		total := float64(acc.count)
		mean := make([]float64, len(acc.sum1))
		std := make([]float64, len(acc.sum1))
		for i := range acc.sum1 {
			mean[i] = acc.sum1[i] / total
			// Cancellation can push a zero variance a hair below zero.
			v := acc.sum2[i]/total - mean[i]*mean[i]
			if v < 0 {
				v = 0
			}
			std[i] = math.Sqrt(v)
		}
		out[name] = FeatureStats{
			Min:   acc.min,
			Max:   acc.max,
			Mean:  mean,
			Std:   std,
			Count: acc.count,
		}
	}
	return out
}

// taskSet collects distinct task strings under a cap.
type taskSet struct {
	seen      map[string]struct{}
	truncated bool
}

func newTaskSet() *taskSet { return &taskSet{seen: map[string]struct{}{}} }

func (t *taskSet) add(task string) {
	if task == "" {
		return
	}
	if utf8.RuneCountInString(task) > MaxTaskRunes {
		task = string([]rune(task)[:MaxTaskRunes])
	}
	if _, ok := t.seen[task]; ok {
		return
	}
	if len(t.seen) >= MaxTasks {
		t.truncated = true
		return
	}
	t.seen[task] = struct{}{}
}

// result returns the distinct tasks sorted. Sorting is not cosmetic: the set
// is a Go map, so emitting it in iteration order would make the digest differ
// between two reads of the same unchanged dataset.
func (t *taskSet) result() ([]string, bool) {
	if len(t.seen) == 0 {
		return nil, t.truncated
	}
	out := make([]string, 0, len(t.seen))
	for k := range t.seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, t.truncated
}

// buildLengthHistogram bins episode lengths into at most
// MaxLengthHistogramBuckets equal-width buckets.
//
// Buckets are half-open [From, To) except the last, which is closed so the
// longest episode lands inside the histogram instead of one past its end.
func buildLengthHistogram(lengths []int64) []LengthBucket {
	if len(lengths) == 0 {
		return nil
	}
	lo, hi := lengths[0], lengths[0]
	for _, l := range lengths {
		if l < lo {
			lo = l
		}
		if l > hi {
			hi = l
		}
	}
	if lo == hi {
		// Every episode is the same length — one bucket is the honest answer,
		// and it avoids a zero-width bucket division.
		return []LengthBucket{{From: lo, To: hi, Count: int64(len(lengths))}}
	}
	span := hi - lo + 1
	buckets := int64(MaxLengthHistogramBuckets)
	if span < buckets {
		buckets = span
	}
	width := (span + buckets - 1) / buckets // ceil, so the last bucket covers hi
	out := make([]LengthBucket, 0, buckets)
	for i := int64(0); i < buckets; i++ {
		from := lo + i*width
		if from > hi {
			break
		}
		to := from + width
		if to > hi {
			to = hi
		}
		out = append(out, LengthBucket{From: from, To: to})
	}
	for _, l := range lengths {
		i := (l - lo) / width
		if i >= int64(len(out)) {
			i = int64(len(out)) - 1
		}
		out[i].Count++
	}
	return out
}
