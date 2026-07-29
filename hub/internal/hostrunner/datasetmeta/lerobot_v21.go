package datasetmeta

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// maxJSONLLine bounds one line of a JSONL metadata file.
//
// bufio.Scanner's default is 64 KiB, which a real episodes_stats.jsonl line
// can exceed once a dataset has many features or high-channel images — the
// failure mode is a silent short read reported as ErrTooLong, i.e. a dataset
// that reads as having fewer episodes than it has. The cap is raised and the
// overflow is turned into an explicit error below.
const maxJSONLLine = 8 << 20

// v2.1 metadata files.
const (
	pathEpisodesJSONL      = "meta/episodes.jsonl"
	pathTasksJSONL         = "meta/tasks.jsonl"
	pathEpisodesStatsJSONL = "meta/episodes_stats.jsonl"
)

// episodeRecordV21 is one line of meta/episodes.jsonl.
type episodeRecordV21 struct {
	EpisodeIndex int64    `json:"episode_index"`
	Tasks        []string `json:"tasks"`
	Length       int64    `json:"length"`
}

// taskRecordV21 is one line of meta/tasks.jsonl.
type taskRecordV21 struct {
	TaskIndex int64  `json:"task_index"`
	Task      string `json:"task"`
}

// episodeStatsRecordV21 is one line of meta/episodes_stats.jsonl.
type episodeStatsRecordV21 struct {
	EpisodeIndex int64                      `json:"episode_index"`
	Stats        map[string]json.RawMessage `json:"stats"`
}

// scanJSONL walks a JSONL file line by line, skipping blank lines. It stops
// early — without error — when fn returns errStopScan.
func scanJSONL(src Source, name string, fn func(line []byte) error) error {
	rc, err := src.Open(name)
	if err != nil {
		return err
	}
	defer rc.Close()
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 64<<10), maxJSONLLine)
	n := 0
	for sc.Scan() {
		line := sc.Bytes()
		n++
		if len(line) == 0 {
			continue
		}
		if err := fn(line); err != nil {
			if errors.Is(err, errStopScan) {
				return nil
			}
			return fmt.Errorf("datasetmeta: %s line %d: %w", name, n, err)
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return fmt.Errorf("datasetmeta: %s has a line longer than the %d-byte cap", name, maxJSONLLine)
		}
		return fmt.Errorf("datasetmeta: read %s: %w", name, err)
	}
	return nil
}

// errStopScan ends a JSONL walk early without reporting a failure.
var errStopScan = errors.New("datasetmeta: stop scan")

// digestV21 fills the generation-specific half of a v2.1 digest.
func digestV21(src Source, info *Info, d *Digest) error {
	// Tasks: meta/tasks.jsonl is the authoritative full list. Collecting them
	// from episodes instead would only ever see the episodes we scanned.
	tasks := newTaskSet()
	if exists(src, pathTasksJSONL) {
		err := scanJSONL(src, pathTasksJSONL, func(line []byte) error {
			var r taskRecordV21
			if err := json.Unmarshal(line, &r); err != nil {
				return err
			}
			tasks.add(r.Task)
			return nil
		})
		if err != nil {
			return err
		}
	} else {
		d.Warnings = append(d.Warnings, "meta/tasks.jsonl is missing; task list omitted")
	}
	d.Tasks, d.TasksTruncated = tasks.result()

	// Episode lengths drive the histogram.
	lengths := make([]int64, 0, 1024)
	err := scanJSONL(src, pathEpisodesJSONL, func(line []byte) error {
		if int64(len(lengths)) >= MaxEpisodeRecords {
			d.EpisodesTruncated = true
			return errStopScan
		}
		var r episodeRecordV21
		if err := json.Unmarshal(line, &r); err != nil {
			return err
		}
		lengths = append(lengths, r.Length)
		return nil
	})
	if err != nil {
		return err
	}
	d.EpisodesScanned = int64(len(lengths))
	d.LengthHistogram = buildLengthHistogram(lengths)

	// v2.1 has no dataset-level stats.json — that file arrives with v3.0. The
	// per-episode stats fold into the same aggregate exactly (see foldStats).
	if exists(src, pathEpisodesStatsJSONL) {
		agg := newStatsAggregator()
		scanned := int64(0)
		err := scanJSONL(src, pathEpisodesStatsJSONL, func(line []byte) error {
			if scanned >= MaxEpisodeRecords {
				d.StatsPartial = true
				return errStopScan
			}
			var r episodeStatsRecordV21
			if err := json.Unmarshal(line, &r); err != nil {
				return err
			}
			for feature, raw := range r.Stats {
				fs, err := parseFeatureStats(raw)
				if err != nil {
					// One unparsable feature must not cost the whole digest.
					continue
				}
				agg.add(feature, fs)
			}
			scanned++
			return nil
		})
		if err != nil {
			return err
		}
		d.Stats = agg.result()
		d.StatsSource = pathEpisodesStatsJSONL
		d.StatsEpisodes = scanned
	} else {
		d.Warnings = append(d.Warnings, "meta/episodes_stats.jsonl is missing; per-feature stats omitted")
	}
	return nil
}

// episodesV21 reads one window of meta/episodes.jsonl.
//
// The file is walked rather than indexed: JSONL has no random access, and the
// alternative — holding a byte offset per episode — is a cache that has to be
// invalidated. Walking is O(offset) but reads only the bytes it skips.
func episodesV21(src Source, info *Info, offset int64, limit int, page *EpisodePage) error {
	fps := info.FPS
	seen := int64(0)
	err := scanJSONL(src, pathEpisodesJSONL, func(line []byte) error {
		if seen >= offset+int64(limit) || seen >= MaxEpisodeRecords {
			return errStopScan
		}
		cur := seen
		seen++
		if cur < offset {
			return nil
		}
		var r episodeRecordV21
		if err := json.Unmarshal(line, &r); err != nil {
			return err
		}
		ep := Episode{Index: r.EpisodeIndex, Length: r.Length, Tasks: r.Tasks}
		if fps > 0 {
			ep.DurationSec = float64(r.Length) / fps
		}
		page.Episodes = append(page.Episodes, ep)
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
