package datasetmeta

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/parquet-go/parquet-go"
)

// v3.0 metadata files.
const (
	pathStatsJSON    = "meta/stats.json"
	pathTasksParquet = "meta/tasks.parquet"
	dirEpisodesMeta  = "meta/episodes"
)

// Column names inside meta/episodes/**.parquet.
//
// These read like paths but are FLAT column names that happen to contain
// slashes: the schema leaf for "data/chunk_index" is a single element, not a
// "data" group holding "chunk_index". Treating the slash as nesting would look
// right and find nothing.
const (
	colEpisodeIndex     = "episode_index"
	colLength           = "length"
	colTasks            = "tasks"
	colDatasetFromIndex = "dataset_from_index"
	colDatasetToIndex   = "dataset_to_index"
	colDataChunkIndex   = "data/chunk_index"
	colDataFileIndex    = "data/file_index"
	videoColPrefix      = "videos/"
)

// leafName maps a parquet schema leaf path onto the flat column name.
//
// A repeated (list) column arrives as ["tasks", "list", "element"] — the
// LIST-annotated group's two synthetic levels. Everything else is a single
// element. Both collapse to the name the file's author wrote.
func leafName(path []string) string {
	if len(path) >= 2 && path[1] == "list" {
		return path[0]
	}
	return strings.Join(path, "/")
}

// v30Schema is the column index for one episodes-metadata parquet file.
type v30Schema struct {
	byName map[string]int
	videos map[string]v30VideoCols
}

// v30VideoCols are the four columns locating one video stream's slice.
type v30VideoCols struct {
	chunk  int
	file   int
	fromTS int
	toTS   int
}

func newV30Schema(pf *parquet.File) *v30Schema {
	s := &v30Schema{byName: map[string]int{}, videos: map[string]v30VideoCols{}}
	for i, leaf := range pf.Schema().Columns() {
		name := leafName(leaf)
		if _, ok := s.byName[name]; !ok {
			s.byName[name] = i
		}
	}
	// videos/<feature key>/<field>; the key itself contains dots but never a
	// slash, so the field is whatever follows the LAST separator.
	for name, idx := range s.byName {
		if !strings.HasPrefix(name, videoColPrefix) {
			continue
		}
		rest := name[len(videoColPrefix):]
		cut := strings.LastIndex(rest, "/")
		if cut <= 0 {
			continue
		}
		key, field := rest[:cut], rest[cut+1:]
		v, ok := s.videos[key]
		if !ok {
			v = v30VideoCols{chunk: -1, file: -1, fromTS: -1, toTS: -1}
		}
		switch field {
		case "chunk_index":
			v.chunk = idx
		case "file_index":
			v.file = idx
		case "from_timestamp":
			v.fromTS = idx
		case "to_timestamp":
			v.toTS = idx
		default:
			continue
		}
		s.videos[key] = v
	}
	return s
}

func (s *v30Schema) col(name string) int {
	if i, ok := s.byName[name]; ok {
		return i
	}
	return -1
}

// listEpisodeShards returns the episode-metadata parquet files in dataset
// order. Episode indices run across files, so the walk must be sorted:
// DirSource.List sorts within a directory, and chunk/file names are
// zero-padded, so lexical order is numeric order here.
func listEpisodeShards(src Source) ([]string, error) {
	chunks, err := src.List(dirEpisodesMeta)
	if err != nil {
		return nil, fmt.Errorf("datasetmeta: list %s: %w", dirEpisodesMeta, err)
	}
	var out []string
	for _, c := range chunks {
		if !c.IsDir {
			// A flat layout (meta/episodes/file-000.parquet) is not something
			// the pinned fixtures show, but accepting it costs nothing.
			if strings.HasSuffix(c.Name, ".parquet") {
				out = append(out, dirEpisodesMeta+"/"+c.Name)
			}
			continue
		}
		files, err := src.List(dirEpisodesMeta + "/" + c.Name)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if !f.IsDir && strings.HasSuffix(f.Name, ".parquet") {
				out = append(out, dirEpisodesMeta+"/"+c.Name+"/"+f.Name)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("datasetmeta: no episode metadata parquet under %s", dirEpisodesMeta)
	}
	sort.Strings(out)
	return out, nil
}

// openParquet opens one parquet file for random access.
func openParquet(src Source, name string) (*parquet.File, func() error, error) {
	rc, size, err := src.OpenReaderAt(name)
	if err != nil {
		return nil, nil, err
	}
	pf, err := parquet.OpenFile(rc, size)
	if err != nil {
		rc.Close()
		return nil, nil, fmt.Errorf("datasetmeta: open %s: %w", name, err)
	}
	return pf, rc.Close, nil
}

// digestV30 fills the generation-specific half of a v3.0 digest.
func digestV30(src Source, info *Info, d *Digest) error {
	// v3.0 ships dataset-level stats directly; no fold needed.
	if exists(src, pathStatsJSON) {
		b, err := readCapped(src, pathStatsJSON, MaxMetaFileBytes)
		if err != nil {
			return err
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(b, &raw); err != nil {
			return fmt.Errorf("datasetmeta: parse %s: %w", pathStatsJSON, err)
		}
		stats := make(map[string]FeatureStats, len(raw))
		for feature, block := range raw {
			fs, err := parseFeatureStats(block)
			if err != nil {
				continue
			}
			stats[feature] = fs
		}
		if len(stats) > 0 {
			d.Stats = stats
			d.StatsSource = pathStatsJSON
			d.StatsEpisodes = info.TotalEpisodes
		}
	} else {
		d.Warnings = append(d.Warnings, "meta/stats.json is missing; per-feature stats omitted")
	}

	// Tasks come from meta/tasks.parquet.
	tasks, err := readTasksParquet(src)
	if err != nil {
		d.Warnings = append(d.Warnings, "meta/tasks.parquet unreadable; task list omitted")
	} else {
		set := newTaskSet()
		for _, t := range tasks {
			set.add(t)
		}
		d.Tasks, d.TasksTruncated = set.result()
	}

	// Episode lengths drive the histogram. Only the `length` column is read —
	// the file also carries per-episode stats for every feature, which is the
	// bulk of its bytes and none of its use here.
	shards, err := listEpisodeShards(src)
	if err != nil {
		return err
	}
	lengths := make([]int64, 0, 1024)
	for _, shard := range shards {
		if int64(len(lengths)) >= MaxEpisodeRecords {
			d.EpisodesTruncated = true
			break
		}
		err := func() error {
			pf, closeFn, err := openParquet(src, shard)
			if err != nil {
				return err
			}
			defer closeFn()
			sc := newV30Schema(pf)
			col := sc.col(colLength)
			if col < 0 {
				return fmt.Errorf("datasetmeta: %s has no %q column", shard, colLength)
			}
			for _, rg := range pf.RowGroups() {
				if int64(len(lengths)) >= MaxEpisodeRecords {
					d.EpisodesTruncated = true
					return nil
				}
				err := readInt64Column(rg, col, func(v int64) bool {
					lengths = append(lengths, v)
					return int64(len(lengths)) < MaxEpisodeRecords
				})
				if err != nil {
					return err
				}
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	d.EpisodesScanned = int64(len(lengths))
	d.LengthHistogram = buildLengthHistogram(lengths)
	return nil
}

// readInt64Column streams one INT64 column of a row group, stopping when fn
// returns false.
//
// Column-at-a-time rather than row-at-a-time on purpose: an episodes-metadata
// file carries ~60 columns, nearly all of them per-episode statistics, so
// reading rows to get `length` would decode roughly sixty times the bytes.
func readInt64Column(rg parquet.RowGroup, col int, fn func(int64) bool) error {
	chunks := rg.ColumnChunks()
	if col < 0 || col >= len(chunks) {
		return fmt.Errorf("datasetmeta: column %d out of range", col)
	}
	pages := chunks[col].Pages()
	defer pages.Close()
	buf := make([]parquet.Value, 512)
	for {
		page, err := pages.ReadPage()
		if err != nil {
			// io.EOF ends the chunk; parquet-go wraps it plainly.
			if isEOF(err) {
				return nil
			}
			return err
		}
		vr := page.Values()
		for {
			n, err := vr.ReadValues(buf)
			for i := 0; i < n; i++ {
				if buf[i].IsNull() {
					continue
				}
				if !fn(buf[i].Int64()) {
					return nil
				}
			}
			if err != nil {
				if isEOF(err) {
					break
				}
				return err
			}
			if n == 0 {
				break
			}
		}
	}
}

// taskColumnNames are the spellings of the task-string column, in precedence
// order. The name is not stable across exporters: the curated lerobot/*
// datasets were written through a pandas path that leaves the string in the
// index column "__index_level_0__", while a dataset recorded with a newer
// LeRobot names it "task". Both are live in the wild.
var taskColumnNames = []string{"task", "__index_level_0__"}

// pickTaskColumn chooses the column holding the task string.
//
// A known name always wins over position. The positional fallback — first
// column that is not the task index — exists so an exporter that invents a
// third spelling still yields tasks, but it is a guess, and a guess must never
// outrank a name we actually recognise: a file carrying both "task" and some
// decoy column would otherwise resolve by column order.
//
// Returns -1 when there is no candidate at all.
func pickTaskColumn(names []string) int {
	for _, want := range taskColumnNames {
		for i, n := range names {
			if n == want {
				return i
			}
		}
	}
	for i, n := range names {
		if n != "task_index" {
			return i
		}
	}
	return -1
}

// readTasksParquet reads meta/tasks.parquet into a task list.
func readTasksParquet(src Source) ([]string, error) {
	if !exists(src, pathTasksParquet) {
		return nil, fmt.Errorf("datasetmeta: %s is missing", pathTasksParquet)
	}
	pf, closeFn, err := openParquet(src, pathTasksParquet)
	if err != nil {
		return nil, err
	}
	defer closeFn()

	cols := pf.Schema().Columns()
	names := make([]string, len(cols))
	for i, leaf := range cols {
		names[i] = leafName(leaf)
	}
	col := pickTaskColumn(names)
	if col < 0 {
		return nil, fmt.Errorf("datasetmeta: %s has no task column", pathTasksParquet)
	}

	var out []string
	for _, rg := range pf.RowGroups() {
		chunks := rg.ColumnChunks()
		if col >= len(chunks) {
			continue
		}
		pages := chunks[col].Pages()
		buf := make([]parquet.Value, 256)
		for {
			page, err := pages.ReadPage()
			if err != nil {
				if isEOF(err) {
					break
				}
				pages.Close()
				return nil, err
			}
			vr := page.Values()
			done := false
			for !done {
				n, err := vr.ReadValues(buf)
				for i := 0; i < n; i++ {
					if buf[i].IsNull() {
						continue
					}
					out = append(out, string(buf[i].ByteArray()))
					if len(out) >= MaxTasks {
						done = true
						break
					}
				}
				if err != nil || n == 0 {
					break
				}
			}
			if done {
				break
			}
		}
		pages.Close()
		if len(out) >= MaxTasks {
			break
		}
	}
	return out, nil
}

// episodesV30 reads one window of the episodes table across shards.
func episodesV30(src Source, info *Info, offset int64, limit int, page *EpisodePage) error {
	shards, err := listEpisodeShards(src)
	if err != nil {
		return err
	}
	seen := int64(0)
	for _, shard := range shards {
		if len(page.Episodes) >= limit {
			return nil
		}
		err := func() error {
			pf, closeFn, err := openParquet(src, shard)
			if err != nil {
				return err
			}
			defer closeFn()
			sc := newV30Schema(pf)
			for _, rg := range pf.RowGroups() {
				rows := rg.NumRows()
				// Whole row groups before the window are skipped without
				// decoding a single value.
				if seen+rows <= offset {
					seen += rows
					continue
				}
				if len(page.Episodes) >= limit {
					return nil
				}
				if err := readEpisodeRows(rg, sc, info, offset, limit, &seen, page); err != nil {
					return err
				}
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

// readEpisodeRows decodes rows of one row group into the page.
func readEpisodeRows(rg parquet.RowGroup, sc *v30Schema, info *Info, offset int64, limit int, seen *int64, page *EpisodePage) error {
	rows := rg.Rows()
	defer rows.Close()
	buf := make([]parquet.Row, 64)
	for {
		n, err := rows.ReadRows(buf)
		for i := 0; i < n; i++ {
			cur := *seen
			*seen++
			if cur < offset {
				continue
			}
			if len(page.Episodes) >= limit {
				return nil
			}
			ep := decodeEpisodeRow(buf[i], sc, info.FPS)
			// The episode metadata says WHICH file and WHEN inside it; the
			// path template says WHERE. A player needs both.
			attachVideoPaths(info, &ep)
			page.Episodes = append(page.Episodes, ep)
		}
		if err != nil {
			if isEOF(err) {
				return nil
			}
			return err
		}
		if n == 0 {
			return nil
		}
	}
}

// decodeEpisodeRow turns one parquet row into an Episode.
func decodeEpisodeRow(row parquet.Row, sc *v30Schema, fps float64) Episode {
	var ep Episode
	// A row is a flat list of values tagged with their column; a repeated
	// column contributes one value per element, which is how `tasks` yields
	// several strings for one episode.
	get := func(col int) (parquet.Value, bool) {
		for _, v := range row {
			if v.Column() == col && !v.IsNull() {
				return v, true
			}
		}
		return parquet.Value{}, false
	}
	if v, ok := get(sc.col(colEpisodeIndex)); ok {
		ep.Index = v.Int64()
	}
	if v, ok := get(sc.col(colLength)); ok {
		ep.Length = v.Int64()
		if fps > 0 {
			ep.DurationSec = float64(ep.Length) / fps
		}
	}
	if c := sc.col(colTasks); c >= 0 {
		for _, v := range row {
			if v.Column() == c && !v.IsNull() {
				ep.Tasks = append(ep.Tasks, string(v.ByteArray()))
			}
		}
	}
	setInt := func(col int, dst **int64) {
		if v, ok := get(col); ok {
			n := v.Int64()
			*dst = &n
		}
	}
	setInt(sc.col(colDataChunkIndex), &ep.DataChunk)
	setInt(sc.col(colDataFileIndex), &ep.DataFile)
	setInt(sc.col(colDatasetFromIndex), &ep.FromIndex)
	setInt(sc.col(colDatasetToIndex), &ep.ToIndex)

	for key, cols := range sc.videos {
		var slice VideoSlice
		any := false
		if v, ok := get(cols.chunk); ok {
			slice.Chunk = v.Int64()
			any = true
		}
		if v, ok := get(cols.file); ok {
			slice.File = v.Int64()
			any = true
		}
		if v, ok := get(cols.fromTS); ok {
			slice.FromTS = v.Double()
			any = true
		}
		if v, ok := get(cols.toTS); ok {
			slice.ToTS = v.Double()
			any = true
		}
		if any {
			if ep.Videos == nil {
				ep.Videos = map[string]VideoSlice{}
			}
			ep.Videos[key] = slice
		}
	}
	return ep
}

// isEOF reports whether err ends a page or value stream.
func isEOF(err error) bool { return errors.Is(err, io.EOF) }
