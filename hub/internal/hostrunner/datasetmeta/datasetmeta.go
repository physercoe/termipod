// Package datasetmeta reads the metadata of a robot-learning dataset that
// lives on a host and folds it into a digest small enough for the hub to own.
//
// Blueprint §4 (the data-ownership law) draws the line this package sits on:
// the hub stores the digest row, the host keeps the bytes. Nothing here opens
// a frame, a parquet data shard or an mp4 — only the `meta/` tree, and only
// under explicit caps, so registering a 50k-episode dataset cannot turn into
// an unbounded read (the J3 no-uncapped-reads anchor). Where a cap bites, the
// result says so in a field rather than quietly returning less.
//
// # LeRobot generations
//
// A LeRobot root is marked by meta/info.json's `codebase_version`. Two
// generations are supported, and they differ in more than layout:
//
//	v2.1  meta/{info.json, episodes.jsonl, tasks.jsonl, episodes_stats.jsonl}
//	      One parquet + one mp4 per episode per camera, so an episode is a
//	      whole file. All metadata is JSON — standard library only. There is
//	      no dataset-level stats.json; the per-episode stats fold into one
//	      (see stats.go).
//
//	v3.0  meta/{info.json, stats.json, tasks.parquet, episodes/chunk-*/file-*.parquet}
//	      Many episodes share one parquet/mp4, so an episode is a *slice*:
//	      dataset_from_index/dataset_to_index locate its rows, and per-video
//	      from_timestamp/to_timestamp locate its seconds inside a concatenated
//	      mp4. Episode metadata is itself parquet, so reading it needs a
//	      parquet decoder.
//
// An unrecognized codebase_version is reported as *UnsupportedFormatError with
// the version string intact, and never parsed on a best-effort basis: a silent
// partial parse of an unknown generation would present invented numbers as
// dataset facts.
package datasetmeta

import (
	"fmt"
	"io"
	"time"
)

// DigestSchemaVersion is the shape version of Digest as shipped to the hub.
// Bump it whenever a field's meaning changes so stored digests refold instead
// of being read under the wrong contract (the ADR-038 precedent).
const DigestSchemaVersion = 1

// Format identifies a dataset layout. The string is stored on the hub row and
// shown in the UI, so it is part of the wire contract.
type Format string

const (
	// FormatLeRobotV21 is LeRobot codebase_version v2.1 (JSON/JSONL metadata,
	// one file per episode).
	FormatLeRobotV21 Format = "lerobot_v2.1"
	// FormatLeRobotV30 is LeRobot codebase_version v3.0 (parquet metadata,
	// many episodes per file, episodes located by offset).
	FormatLeRobotV30 Format = "lerobot_v3.0"
)

// UnsupportedFormatError reports a dataset root this package refuses to parse.
// CodebaseVersion carries whatever the file claimed — including the empty
// string — so the UI can say *which* version it does not know rather than a
// bare "unsupported".
type UnsupportedFormatError struct {
	CodebaseVersion string
	Reason          string
}

func (e *UnsupportedFormatError) Error() string {
	v := e.CodebaseVersion
	if v == "" {
		v = "(absent)"
	}
	if e.Reason != "" {
		return fmt.Sprintf("datasetmeta: unsupported dataset format %s: %s", v, e.Reason)
	}
	return fmt.Sprintf("datasetmeta: unsupported dataset format %s", v)
}

// Caps. Every read this package performs is bounded by one of these. They are
// deliberately generous — the point is to make a pathological or hostile root
// impossible to turn into an OOM, not to second-guess a real dataset.
const (
	// MaxMetaFileBytes bounds a single small JSON metadata file read whole
	// into memory (info.json, stats.json, tasks.jsonl).
	MaxMetaFileBytes = 32 << 20

	// MaxEpisodeRecords bounds how many episode records any one call will
	// walk, for both the digest fold and a listing page.
	MaxEpisodeRecords = 200_000

	// MaxTasks bounds the distinct task strings carried on a digest. A
	// dataset may legitimately have more; the digest is a summary, and
	// Digest.TasksTruncated says when it stopped.
	MaxTasks = 256

	// MaxTaskRunes bounds one task string. Instructions are sentences; a
	// megabyte-long one is a corrupt file, not a task.
	MaxTaskRunes = 512

	// MaxEpisodePageLimit bounds one ReadEpisodes page regardless of what
	// the caller asked for.
	MaxEpisodePageLimit = 1000

	// DefaultEpisodePageLimit is used when a request asks for no specific
	// page size.
	DefaultEpisodePageLimit = 200

	// MaxLengthHistogramBuckets bounds the episode-length histogram.
	MaxLengthHistogramBuckets = 24

	// MaxMetaDirEntries bounds a directory listing under meta/.
	MaxMetaDirEntries = 10_000
)

// ReaderAtCloser is random access plus release. The parquet decoder seeks
// within a file rather than reading it end to end, so v3.0 metadata is opened
// this way and a large episodes shard never lands in memory whole.
type ReaderAtCloser interface {
	io.ReaderAt
	io.Closer
}

// Entry is one directory entry under a dataset root.
type Entry struct {
	Name    string
	Size    int64
	ModTime time.Time
	IsDir   bool
}

// Source is the byte-access surface this package needs from a dataset root.
// Names are always slash-separated and relative to the root ("meta/info.json").
//
// DirSource is the local implementation. Remote roots (SFTP, hub) implement
// the same interface once the SSH-forward wedge lands — that is the only
// reason this is an interface rather than a path string.
type Source interface {
	// Open streams a file for sequential reading.
	Open(name string) (io.ReadCloser, error)
	// OpenReaderAt opens a file for random access and reports its size.
	OpenReaderAt(name string) (ReaderAtCloser, int64, error)
	// List returns the entries directly under a directory, not recursively.
	// A missing directory is an error; an empty one is an empty slice.
	List(dir string) ([]Entry, error)
}

// VideoStream is one video-typed feature: a camera, or anything else stored as
// frames (a depth map is a video stream here too).
//
// Named VideoStream, not Stream: in this codebase "stream" already means an
// agent's wire output (stream-json, the M4 local-stream tap, the card stream),
// and a bare Stream type would collide with it.
type VideoStream struct {
	// Key is the full feature key, e.g. "observation.images.up".
	Key string `json:"key"`
	// Name is the trailing segment, e.g. "up" — what the UI labels a pane.
	Name     string  `json:"name"`
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
	Channels int     `json:"channels,omitempty"`
	Codec    string  `json:"codec,omitempty"`
	FPS      float64 `json:"fps,omitempty"`
	IsDepth  bool    `json:"is_depth,omitempty"`
	HasAudio bool    `json:"has_audio,omitempty"`
}

// Feature is one non-video feature: action, observation.state, a reward, an
// index column.
type Feature struct {
	Key   string `json:"key"`
	DType string `json:"dtype"`
	// Shape is the per-frame shape. Dimensionality comes from here and never
	// from len(Names): real files carry `"names": null` on scalar features,
	// so a names-derived dimension would read as zero.
	Shape []int `json:"shape,omitempty"`
	// Names are the per-component labels ("shoulder_pan.pos", …) when the
	// exporter supplied them. Frequently null.
	Names []string `json:"names,omitempty"`
}

// FeatureStats is the min/max/mean/std/count summary for one feature,
// flattened across components (an image feature's per-channel stats arrive
// nested and are flattened in reading order).
type FeatureStats struct {
	Min   []float64 `json:"min,omitempty"`
	Max   []float64 `json:"max,omitempty"`
	Mean  []float64 `json:"mean,omitempty"`
	Std   []float64 `json:"std,omitempty"`
	Count int64     `json:"count,omitempty"`
}

// LengthBucket is one bar of the episode-length histogram.
type LengthBucket struct {
	// From and To are frame counts; To is exclusive except on the last
	// bucket, which is closed so the longest episode falls inside it.
	From  int64 `json:"from"`
	To    int64 `json:"to"`
	Count int64 `json:"count"`
}

// Digest is the hub-owned summary of a dataset. It is metadata about
// metadata: everything here is derived from the `meta/` tree alone.
type Digest struct {
	SchemaVersion   int     `json:"schema_version"`
	Format          Format  `json:"format"`
	CodebaseVersion string  `json:"codebase_version"`
	RobotType       string  `json:"robot_type,omitempty"`
	FPS             float64 `json:"fps,omitempty"`

	TotalEpisodes int64 `json:"total_episodes"`
	TotalFrames   int64 `json:"total_frames"`
	TotalTasks    int64 `json:"total_tasks"`
	// DurationSec is TotalFrames/FPS — the dataset's wall-clock extent. Zero
	// when fps is absent or zero rather than a division by zero.
	DurationSec float64 `json:"duration_sec,omitempty"`

	VideoStreams []VideoStream `json:"video_streams,omitempty"`
	Features     []Feature     `json:"features,omitempty"`

	// Tasks are the distinct instruction strings, sorted, capped at MaxTasks.
	Tasks          []string `json:"tasks,omitempty"`
	TasksTruncated bool     `json:"tasks_truncated,omitempty"`

	Stats map[string]FeatureStats `json:"stats,omitempty"`
	// StatsSource records which file the stats came from, because the two
	// generations answer differently: v3.0 reads meta/stats.json directly,
	// v2.1 has no such file and folds meta/episodes_stats.jsonl. A reader
	// comparing two digests needs to know which it is looking at.
	StatsSource string `json:"stats_source,omitempty"`
	// StatsEpisodes is how many episodes contributed to a folded stat. It
	// equals TotalEpisodes unless a cap bit.
	StatsEpisodes int64 `json:"stats_episodes,omitempty"`
	// StatsPartial is set when the fold stopped early, so a consumer does not
	// present a capped aggregate as the whole dataset's.
	StatsPartial bool `json:"stats_partial,omitempty"`

	LengthHistogram []LengthBucket `json:"length_histogram,omitempty"`
	// EpisodesScanned is how many episode records the length histogram and
	// task set were built from.
	EpisodesScanned int64 `json:"episodes_scanned,omitempty"`
	// EpisodesTruncated is set when MaxEpisodeRecords bit during that scan.
	EpisodesTruncated bool `json:"episodes_truncated,omitempty"`

	// EnvRef is the opaque "family:env_id@version" provenance handle reserved
	// by the replay plan §3. Unvalidated and usually empty; W1 fills it only
	// where info.json makes it cheaply derivable.
	EnvRef string `json:"env_ref,omitempty"`

	// Warnings are non-fatal oddities worth showing (a missing optional file,
	// a feature whose stats did not parse). A dataset that produced warnings
	// still has a usable digest.
	Warnings []string `json:"warnings,omitempty"`
}

// Episode is one row of the episodes table. Location fields are the v3.0
// answer to "where are this episode's bytes"; in v2.1 an episode is its own
// file and they stay nil.
type Episode struct {
	Index       int64    `json:"index"`
	Length      int64    `json:"length"`
	DurationSec float64  `json:"duration_sec,omitempty"`
	Tasks       []string `json:"tasks,omitempty"`

	// DataChunk/DataFile identify the shared parquet holding this episode,
	// and FromIndex/ToIndex its half-open row range within it.
	DataChunk *int64 `json:"data_chunk,omitempty"`
	DataFile  *int64 `json:"data_file,omitempty"`
	FromIndex *int64 `json:"from_index,omitempty"`
	ToIndex   *int64 `json:"to_index,omitempty"`

	// Videos maps a video feature key to this episode's slice of the shared
	// mp4. W2 turns these into playable ranges.
	Videos map[string]VideoSlice `json:"videos,omitempty"`
}

// VideoSlice locates one episode inside one shared video file.
type VideoSlice struct {
	Chunk  int64   `json:"chunk"`
	File   int64   `json:"file"`
	FromTS float64 `json:"from_ts"`
	ToTS   float64 `json:"to_ts"`
}

// EpisodeRequest is a windowed listing request. Offset is an episode index
// position within the dataset's ordering, not a byte offset.
type EpisodeRequest struct {
	Offset int64
	Limit  int
}

// EpisodePage is one window of the episodes table, with the caps that shaped
// it stated rather than implied.
type EpisodePage struct {
	Episodes []Episode `json:"episodes"`
	Offset   int64     `json:"offset"`
	// Total is the dataset's episode count as declared by info.json, so a UI
	// can size a scrollbar without walking every record.
	Total int64 `json:"total"`
	// Limit is the page size actually applied after clamping.
	Limit int `json:"limit"`
	// Truncated is set when the caller's limit was clamped down.
	Truncated bool `json:"truncated,omitempty"`
}

// Fingerprint is the cheap staleness token for a dataset's meta/ tree: a
// stat-only summary that costs no parsing. The hub stores it beside the
// digest and re-stats on open, which is what drives the "digest may be stale —
// Refresh" hint (replay plan decision #4: manual refresh, never a background
// crawl).
type Fingerprint struct {
	Files      int    `json:"files"`
	Bytes      int64  `json:"bytes"`
	MaxModTime string `json:"max_mod_time,omitempty"`
}

// Sniff identifies the dataset layout at a root by reading meta/info.json.
// It returns the parsed info alongside the format so a caller that goes on to
// build a digest does not read the file twice.
func Sniff(src Source) (Format, *Info, error) {
	info, err := readInfo(src)
	if err != nil {
		return "", nil, err
	}
	format, err := formatOf(info.CodebaseVersion)
	if err != nil {
		return "", nil, err
	}
	return format, info, nil
}

// formatOf maps a codebase_version onto a supported layout.
//
// The match is exact on the versions we have actually read files from. A
// prefix or "greater than" rule would be worse than useless here: it would
// make an unreleased v4 silently parse as v3.0 and emit confident numbers
// from a layout nobody has checked.
func formatOf(codebaseVersion string) (Format, error) {
	switch codebaseVersion {
	case "v2.1":
		return FormatLeRobotV21, nil
	case "v3.0":
		return FormatLeRobotV30, nil
	case "":
		return "", &UnsupportedFormatError{Reason: "meta/info.json has no codebase_version"}
	default:
		return "", &UnsupportedFormatError{CodebaseVersion: codebaseVersion}
	}
}

// ReadDigest folds a dataset root into a Digest.
func ReadDigest(src Source) (*Digest, error) {
	format, info, err := Sniff(src)
	if err != nil {
		return nil, err
	}
	d := &Digest{
		SchemaVersion:   DigestSchemaVersion,
		Format:          format,
		CodebaseVersion: info.CodebaseVersion,
		RobotType:       info.RobotType,
		FPS:             info.FPS,
		TotalEpisodes:   info.TotalEpisodes,
		TotalFrames:     info.TotalFrames,
		TotalTasks:      info.TotalTasks,
		EnvRef:          info.envRef(),
	}
	if info.FPS > 0 {
		d.DurationSec = float64(info.TotalFrames) / info.FPS
	}
	d.VideoStreams, d.Features = info.split()

	switch format {
	case FormatLeRobotV21:
		err = digestV21(src, info, d)
	case FormatLeRobotV30:
		err = digestV30(src, info, d)
	default:
		err = &UnsupportedFormatError{CodebaseVersion: info.CodebaseVersion}
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

// ReadEpisodes returns one window of a dataset's episodes table.
func ReadEpisodes(src Source, req EpisodeRequest) (*EpisodePage, error) {
	format, info, err := Sniff(src)
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	truncated := false
	if limit <= 0 {
		limit = DefaultEpisodePageLimit
	}
	if limit > MaxEpisodePageLimit {
		limit = MaxEpisodePageLimit
		truncated = true
	}
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	page := &EpisodePage{
		Offset:    offset,
		Limit:     limit,
		Total:     info.TotalEpisodes,
		Truncated: truncated,
		Episodes:  []Episode{},
	}
	switch format {
	case FormatLeRobotV21:
		err = episodesV21(src, info, offset, limit, page)
	case FormatLeRobotV30:
		err = episodesV30(src, info, offset, limit, page)
	default:
		err = &UnsupportedFormatError{CodebaseVersion: info.CodebaseVersion}
	}
	if err != nil {
		return nil, err
	}
	return page, nil
}

// ReadFingerprint stats the meta/ tree without parsing anything.
func ReadFingerprint(src Source) (Fingerprint, error) {
	var fp Fingerprint
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		// meta/ nests at most one level deep in the layouts we support
		// (meta/episodes/chunk-NNN/); the bound stops a symlink loop or a
		// pathological tree from turning a stat into a crawl.
		if depth > 4 {
			return nil
		}
		entries, err := src.List(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir {
				if err := walk(dir+"/"+e.Name, depth+1); err != nil {
					return err
				}
				continue
			}
			fp.Files++
			fp.Bytes += e.Size
			if ts := e.ModTime.UTC().Format(time.RFC3339); ts > fp.MaxModTime {
				fp.MaxModTime = ts
			}
			if fp.Files >= MaxMetaDirEntries {
				return nil
			}
		}
		return nil
	}
	if err := walk("meta", 0); err != nil {
		return Fingerprint{}, err
	}
	return fp, nil
}
