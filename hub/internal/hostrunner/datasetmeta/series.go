package datasetmeta

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/parquet-go/parquet-go"
)

// Per-episode feature series (J8 W2) — the numbers under the player's channel
// plots: action and state against time, for ONE episode.
//
// This is the first thing in the package that opens a *data* file rather than
// metadata, and the data-ownership law (blueprint §4) is what shapes it. The
// hub must never hold the series; the host reads its own disk, downsamples,
// and answers a bounded window. So every read here is capped twice over — by
// the row range of a single episode, and by a point budget applied as a stride
// before any value is retained.
//
// Downsampling is decimation (take every Nth frame), not averaging. A plot of
// a robot's joint angles is read for its shape and its extremes; averaging
// would quietly erase the spikes that are the reason anyone opens it.

const (
	// DefaultSeriesPoints is the point budget per channel when a caller does
	// not name one. A strip chart a few hundred pixels wide cannot show more.
	DefaultSeriesPoints = 1000

	// MaxSeriesPoints bounds the point budget regardless of the request.
	MaxSeriesPoints = 5000

	// MaxSeriesFeatures bounds how many features one call returns.
	MaxSeriesFeatures = 24

	// MaxSeriesChannels bounds the total channels across all features. A
	// 7-DoF arm has 7 per feature; the cap exists for the pathological case
	// (a flattened image stored as a numeric feature), not the normal one.
	MaxSeriesChannels = 96

	// MaxEpisodeRows bounds the rows one episode may span. An episode is
	// minutes of robot motion, so this is roughly a day at 100 Hz — high
	// enough never to bite a real dataset, low enough that a corrupt offset
	// pair cannot ask the host to walk a whole file.
	MaxEpisodeRows = 10_000_000
)

// SeriesRequest asks for one episode's numeric channels.
type SeriesRequest struct {
	// Episode is the episode_index, NOT a position in the episodes table.
	Episode int64
	// Features selects feature keys; empty means every numeric feature.
	// Unknown keys are reported in Warnings rather than failing the call —
	// a UI's saved selection outliving a dataset edit is normal.
	Features []string
	// MaxPoints is the per-channel point budget.
	MaxPoints int
}

// Channel is one scalar track: a joint, a gripper, a reward.
type Channel struct {
	// Name comes from info.json's `names` when the dataset labels its
	// channels; otherwise it is empty and the UI shows the index.
	Name   string    `json:"name,omitempty"`
	Values []float64 `json:"values"`
}

// FeatureSeries is one feature's channels for the requested episode.
type FeatureSeries struct {
	Key      string    `json:"key"`
	DType    string    `json:"dtype,omitempty"`
	Channels []Channel `json:"channels"`
}

// SeriesPage is one episode's series, with every cap that shaped it stated.
type SeriesPage struct {
	Episode int64 `json:"episode"`
	// Length is the episode's frame count before downsampling.
	Length int64   `json:"length"`
	FPS    float64 `json:"fps,omitempty"`
	// Stride is the decimation applied: 1 means every frame was kept.
	Stride int `json:"stride"`
	// Points is the number of samples per channel actually returned.
	Points int `json:"points"`
	// Timestamps are seconds from the START OF THE EPISODE, one per point.
	// LeRobot's own `timestamp` column is already episode-relative (it resets
	// to 0 at every episode boundary in both generations), so this needs no
	// rebasing — it is carried through, and only synthesized from the frame
	// number when the column is absent.
	Timestamps []float64       `json:"timestamps"`
	Series     []FeatureSeries `json:"series"`
	// Downsampled says the plot is not frame-exact — a claim a UI must be
	// able to make honestly when a user is looking for a one-frame spike.
	Downsampled bool `json:"downsampled,omitempty"`
	// Truncated says a cap dropped whole features or channels.
	Truncated bool     `json:"truncated,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

// episodeLocation is where one episode's rows live.
type episodeLocation struct {
	// File is the data parquet, relative to the dataset root.
	File string
	// From/To is the half-open row range within it. To < 0 means "to the end
	// of the file", which is how v2.1 (one file per episode) reports itself.
	From int64
	To   int64
	// Length is the episode's declared frame count, 0 when unknown.
	Length int64
}

// ReadSeries returns one episode's numeric channels, downsampled to a budget.
func ReadSeries(src Source, req SeriesRequest) (*SeriesPage, error) {
	format, info, err := Sniff(src)
	if err != nil {
		return nil, err
	}
	if req.Episode < 0 {
		return nil, fmt.Errorf("datasetmeta: episode index must not be negative")
	}
	budget := req.MaxPoints
	page := &SeriesPage{Episode: req.Episode, FPS: info.FPS, Series: []FeatureSeries{}, Timestamps: []float64{}}
	if budget <= 0 {
		budget = DefaultSeriesPoints
	}
	if budget > MaxSeriesPoints {
		budget = MaxSeriesPoints
		page.Truncated = true
	}

	var loc episodeLocation
	switch format {
	case FormatLeRobotV21:
		loc, err = locateEpisodeV21(src, info, req.Episode)
	case FormatLeRobotV30:
		loc, err = locateEpisodeV30(src, info, req.Episode)
	default:
		err = &UnsupportedFormatError{CodebaseVersion: info.CodebaseVersion}
	}
	if err != nil {
		return nil, err
	}

	wanted, missing := selectSeriesFeatures(info, req.Features)
	for _, m := range missing {
		page.Warnings = append(page.Warnings, fmt.Sprintf("feature %q is not a numeric feature of this dataset", m))
	}
	if len(wanted) > MaxSeriesFeatures {
		wanted = wanted[:MaxSeriesFeatures]
		page.Truncated = true
	}

	if err := readSeriesFile(src, loc, wanted, budget, page); err != nil {
		return nil, err
	}
	return page, nil
}

// seriesFeature is one feature the caller asked for, resolved against info.json.
type seriesFeature struct {
	key   string
	dtype string
	names []string
}

// selectSeriesFeatures resolves the requested feature keys, preserving the
// caller's order (a UI's plot order is a choice, not an accident) and falling
// back to every numeric feature, sorted, when nothing was named.
//
// Bookkeeping columns are excluded from the default set: index/episode_index/
// frame_index/task_index are row numbers, and timestamp is the x-axis rather
// than a channel on it. A caller can still ask for one by name — plotting
// frame_index against time is a legitimate way to spot a gap.
func selectSeriesFeatures(info *Info, want []string) ([]seriesFeature, []string) {
	numeric := map[string]InfoFeature{}
	for k, f := range info.Features {
		if isVideoFeature(f) || strings.EqualFold(f.DType, "string") {
			continue
		}
		numeric[k] = f
	}
	if len(want) > 0 {
		var out []seriesFeature
		var missing []string
		for _, k := range want {
			f, ok := numeric[k]
			if !ok {
				missing = append(missing, k)
				continue
			}
			out = append(out, seriesFeature{key: k, dtype: f.DType, names: f.Names})
		}
		return out, missing
	}
	keys := make([]string, 0, len(numeric))
	for k := range numeric {
		if bookkeepingColumns[k] {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]seriesFeature, 0, len(keys))
	for _, k := range keys {
		f := numeric[k]
		out = append(out, seriesFeature{key: k, dtype: f.DType, names: f.Names})
	}
	return out, nil
}

// bookkeepingColumns are row identifiers, not signals. They exist in every
// LeRobot dataset and would crowd out the features anyone opens a plot for.
var bookkeepingColumns = map[string]bool{
	"index":         true,
	"episode_index": true,
	"frame_index":   true,
	"task_index":    true,
	"timestamp":     true,
}

// colTimestamp is the per-frame time column, in seconds from the episode start.
const colTimestamp = "timestamp"

// locateEpisodeV21 resolves an episode to its own parquet file.
//
// v2.1 stores one file per episode, so the whole file IS the episode and no
// offsets are involved. The chunk is derived the way LeRobot derives it —
// episode_index / chunks_size — because nothing in the metadata records it.
func locateEpisodeV21(src Source, info *Info, episode int64) (episodeLocation, error) {
	chunkSize := info.ChunksSize
	if chunkSize <= 0 {
		chunkSize = 1000
	}
	name, err := resolveDataPath(info.DataPath, map[string]int64{
		"episode_chunk": episode / chunkSize,
		"episode_index": episode,
	})
	if err != nil {
		return episodeLocation{}, err
	}
	if !exists(src, name) {
		return episodeLocation{}, fmt.Errorf("datasetmeta: episode %d has no data file at %s", episode, name)
	}
	return episodeLocation{File: name, From: 0, To: -1}, nil
}

// locateEpisodeV30 resolves an episode to its slice of a shared parquet.
func locateEpisodeV30(src Source, info *Info, episode int64) (episodeLocation, error) {
	ep, err := findEpisodeV30(src, info, episode)
	if err != nil {
		return episodeLocation{}, err
	}
	if ep.DataChunk == nil || ep.DataFile == nil || ep.FromIndex == nil || ep.ToIndex == nil {
		return episodeLocation{}, fmt.Errorf(
			"datasetmeta: episode %d has no data offsets in meta/episodes; its series cannot be located", episode)
	}
	from, to := *ep.FromIndex, *ep.ToIndex
	if from < 0 || to < from {
		return episodeLocation{}, fmt.Errorf("datasetmeta: episode %d has an invalid row range [%d,%d)", episode, from, to)
	}
	if to-from > MaxEpisodeRows {
		return episodeLocation{}, fmt.Errorf("datasetmeta: episode %d spans %d rows, over the %d-row cap",
			episode, to-from, MaxEpisodeRows)
	}
	name, err := resolveDataPath(info.DataPath, map[string]int64{
		"chunk_index": *ep.DataChunk,
		"file_index":  *ep.DataFile,
	})
	if err != nil {
		return episodeLocation{}, err
	}
	return episodeLocation{File: name, From: from, To: to, Length: ep.Length}, nil
}

// findEpisodeV30 finds one episode's metadata row by episode_index.
//
// Two passes on purpose. The first scans only the episode_index column to find
// which row group holds the episode and at what position — an episodes file
// carries ~60 columns, nearly all per-episode statistics, so a full-row walk to
// find one row would decode roughly sixty times the bytes. Only the row group
// that matched is then decoded.
//
// The index is not assumed to equal the row position: episodes.parquet is
// written in order today, but "the row at position N is episode N" is an
// assumption about an exporter, and the whole point of reading the column is
// that we do not have to make it.
func findEpisodeV30(src Source, info *Info, episode int64) (Episode, error) {
	shards, err := listEpisodeShards(src)
	if err != nil {
		return Episode{}, err
	}
	for _, shard := range shards {
		found := false
		var ep Episode
		err := func() error {
			pf, closeFn, err := openParquet(src, shard)
			if err != nil {
				return err
			}
			defer closeFn()
			sc := newV30Schema(pf)
			col := sc.col(colEpisodeIndex)
			if col < 0 {
				return fmt.Errorf("datasetmeta: %s has no %q column", shard, colEpisodeIndex)
			}
			for _, rg := range pf.RowGroups() {
				pos, hit := int64(-1), int64(0)
				if err := readInt64Column(rg, col, func(v int64) bool {
					if v == episode {
						pos = hit
						return false
					}
					hit++
					return true
				}); err != nil {
					return err
				}
				if pos < 0 {
					continue
				}
				seen := int64(0)
				page := &EpisodePage{}
				if err := readEpisodeRows(rg, sc, info, pos, 1, &seen, page); err != nil {
					return err
				}
				if len(page.Episodes) == 1 {
					ep, found = page.Episodes[0], true
					return nil
				}
			}
			return nil
		}()
		if err != nil {
			return Episode{}, err
		}
		if found {
			return ep, nil
		}
	}
	return Episode{}, fmt.Errorf("datasetmeta: episode %d is not in this dataset", episode)
}

// resolveDataPath expands info.json's Python format template.
//
// The two generations spell their placeholders differently — v2.1 uses
// {episode_chunk:03d}/{episode_index:06d}, v3.0 {chunk_index:03d}/
// {file_index:03d} — which is exactly why the template is carried verbatim on
// Info and expanded here rather than being hardcoded per generation.
//
// A placeholder with no value is an error, never an empty string: silently
// dropping one yields a path that looks plausible, misses, and reports as "this
// episode has no data file".
func resolveDataPath(tmpl string, vars map[string]int64) (string, error) {
	if strings.TrimSpace(tmpl) == "" {
		return "", fmt.Errorf("datasetmeta: meta/info.json has no data_path template")
	}
	var b strings.Builder
	rest := tmpl
	for {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			b.WriteString(rest)
			break
		}
		close := strings.IndexByte(rest[open:], '}')
		if close < 0 {
			return "", fmt.Errorf("datasetmeta: data_path %q has an unclosed placeholder", tmpl)
		}
		close += open
		b.WriteString(rest[:open])
		name, width := rest[open+1:close], 0
		if colon := strings.IndexByte(name, ':'); colon >= 0 {
			spec := name[colon+1:]
			name = name[:colon]
			// Python's "03d": a zero-pad flag, a width, and the type letter.
			spec = strings.TrimSuffix(spec, "d")
			spec = strings.TrimPrefix(spec, "0")
			if spec != "" {
				n, err := strconv.Atoi(spec)
				if err != nil {
					return "", fmt.Errorf("datasetmeta: data_path %q has an unreadable format spec", tmpl)
				}
				width = n
			}
		}
		v, ok := vars[name]
		if !ok {
			return "", fmt.Errorf("datasetmeta: data_path %q uses unknown placeholder %q", tmpl, name)
		}
		b.WriteString(padInt(v, width))
		rest = rest[close+1:]
	}
	out := b.String()
	// The template comes from the dataset, so it is untrusted input on a path.
	// DirSource refuses an escape as well, but refusing here names the actual
	// problem instead of surfacing as a missing file.
	if strings.HasPrefix(out, "/") || strings.Contains(out, "..") {
		return "", fmt.Errorf("datasetmeta: data_path %q resolves outside the dataset root", tmpl)
	}
	return out, nil
}

// padInt renders n zero-padded to at least width digits.
func padInt(n int64, width int) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	for len(s) < width {
		s = "0" + s
	}
	if neg {
		return "-" + s
	}
	return s
}

// readSeriesFile decodes the requested columns over one episode's row range.
func readSeriesFile(src Source, loc episodeLocation, wanted []seriesFeature, budget int, page *SeriesPage) error {
	pf, closeFn, err := openParquet(src, loc.File)
	if err != nil {
		return err
	}
	defer closeFn()

	from, to := loc.From, loc.To
	if to < 0 {
		to = pf.NumRows()
	}
	if to > pf.NumRows() {
		// The metadata claims rows the file does not have. Clamp and say so:
		// a short read is a real answer about a broken dataset, and refusing
		// outright would hide every other episode in the same file.
		page.Warnings = append(page.Warnings, fmt.Sprintf(
			"episode rows [%d,%d) exceed the %d rows in %s; the tail is missing", from, to, pf.NumRows(), loc.File))
		to = pf.NumRows()
	}
	if from > to {
		from = to
	}
	rows := to - from
	page.Length = rows
	if loc.Length > 0 {
		page.Length = loc.Length
	}

	stride := 1
	if budget > 0 && rows > int64(budget) {
		stride = int((rows + int64(budget) - 1) / int64(budget))
	}
	page.Stride = stride
	page.Downsampled = stride > 1

	index := columnIndex(pf)

	// Timestamps first: they are the x-axis, and a feature read that fails
	// should not leave a page with values and no time to put them on.
	if col, ok := index[colTimestamp]; ok {
		ts, _, err := readNumericColumn(pf, col, from, to, stride, 1)
		if err != nil {
			return err
		}
		if len(ts) == 1 {
			page.Timestamps = ts[0]
		}
	}
	if len(page.Timestamps) == 0 {
		// No timestamp column: synthesize from the frame number. Only correct
		// at a constant frame rate, which is what fps in info.json asserts.
		page.Timestamps = syntheticTimestamps(rows, stride, page.FPS)
		if page.FPS <= 0 {
			page.Warnings = append(page.Warnings,
				"no timestamp column and no fps; the x-axis is frame numbers")
		}
	}
	page.Points = len(page.Timestamps)

	channels := 0
	for _, f := range wanted {
		col, ok := index[f.key]
		if !ok {
			page.Warnings = append(page.Warnings, fmt.Sprintf("feature %q has no column in %s", f.key, loc.File))
			continue
		}
		remaining := MaxSeriesChannels - channels
		if remaining <= 0 {
			page.Truncated = true
			break
		}
		values, capped, err := readNumericColumn(pf, col, from, to, stride, remaining)
		if err != nil {
			return err
		}
		if capped {
			page.Truncated = true
		}
		if len(values) == 0 {
			continue
		}
		channels += len(values)
		fs := FeatureSeries{Key: f.key, DType: f.dtype, Channels: make([]Channel, 0, len(values))}
		for i, vals := range values {
			ch := Channel{Values: vals}
			if i < len(f.names) {
				ch.Name = f.names[i]
			}
			fs.Channels = append(fs.Channels, ch)
		}
		page.Series = append(page.Series, fs)
	}
	return nil
}

// syntheticTimestamps builds the x-axis from frame numbers when the file has
// no timestamp column: seconds when fps is known, frame indices otherwise.
func syntheticTimestamps(rows int64, stride int, fps float64) []float64 {
	if rows <= 0 || stride <= 0 {
		return []float64{}
	}
	out := make([]float64, 0, (rows+int64(stride)-1)/int64(stride))
	for i := int64(0); i < rows; i += int64(stride) {
		if fps > 0 {
			out = append(out, float64(i)/fps)
		} else {
			out = append(out, float64(i))
		}
	}
	return out
}

// columnIndex maps flat column names to their leaf position.
func columnIndex(pf *parquet.File) map[string]int {
	out := map[string]int{}
	for i, leaf := range pf.Schema().Columns() {
		name := leafName(leaf)
		if _, seen := out[name]; !seen {
			out[name] = i
		}
	}
	return out
}

// readNumericColumn decodes one column over a row range, decimated by stride,
// returning one slice per channel.
//
// A scalar feature is one channel; a list feature (`observation.state` with
// shape [7]) is one channel per element. The split comes from the parquet
// REPETITION LEVEL, not from info.json's shape: level 0 opens a new row, any
// higher level continues it. Trusting `shape` instead would mean a file whose
// rows disagree with its declared shape silently interleaves two joints into
// one plot.
//
// maxChannels bounds the width; `capped` reports that it bit.
func readNumericColumn(pf *parquet.File, col int, from, to int64, stride, maxChannels int) (values [][]float64, capped bool, err error) {
	if stride <= 0 {
		stride = 1
	}
	row := int64(-1)
	ch := 0
	keep := false
	for _, rg := range pf.RowGroups() {
		chunks := rg.ColumnChunks()
		if col < 0 || col >= len(chunks) {
			return nil, false, fmt.Errorf("datasetmeta: column %d out of range", col)
		}
		// Whole row groups before the window are skipped without decoding, the
		// same shape as the episodes-table walk.
		if row+rg.NumRows() < from {
			row += rg.NumRows()
			continue
		}
		if row+1 >= to {
			break
		}
		pages := chunks[col].Pages()
		buf := make([]parquet.Value, 512)
		done := false
		for !done {
			page, perr := pages.ReadPage()
			if perr != nil {
				if isEOF(perr) {
					break
				}
				pages.Close()
				return nil, false, perr
			}
			vr := page.Values()
			for {
				n, verr := vr.ReadValues(buf)
				for i := 0; i < n; i++ {
					v := buf[i]
					if v.RepetitionLevel() == 0 {
						row++
						ch = 0
						keep = row >= from && row < to && (row-from)%int64(stride) == 0
						if row >= to {
							done = true
							break
						}
					} else {
						ch++
					}
					if !keep {
						continue
					}
					if ch >= maxChannels {
						capped = true
						continue
					}
					for len(values) <= ch {
						values = append(values, []float64{})
					}
					values[ch] = append(values[ch], numericValue(v))
				}
				if verr != nil || n == 0 {
					break
				}
			}
			if done {
				break
			}
		}
		pages.Close()
		if done {
			break
		}
	}
	return values, capped, nil
}

// numericValue coerces one parquet value to a float.
//
// A null becomes NaN rather than 0: a gap in a recording is not a reading of
// zero, and a plot that draws it as one invents a data point that never
// existed. Booleans become 0/1, which is what `next.done` is for.
func numericValue(v parquet.Value) float64 {
	if v.IsNull() {
		return math.NaN()
	}
	switch v.Kind() {
	case parquet.Boolean:
		if v.Boolean() {
			return 1
		}
		return 0
	case parquet.Int32:
		return float64(v.Int32())
	case parquet.Int64:
		return float64(v.Int64())
	case parquet.Float:
		return float64(v.Float())
	case parquet.Double:
		return v.Double()
	default:
		return math.NaN()
	}
}
