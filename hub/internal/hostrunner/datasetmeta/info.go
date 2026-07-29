package datasetmeta

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Info is meta/info.json. Both generations share most of it; the fields that
// exist in only one are noted.
type Info struct {
	CodebaseVersion string `json:"codebase_version"`
	RobotType       string `json:"robot_type"`
	TotalEpisodes   int64  `json:"total_episodes"`
	TotalFrames     int64  `json:"total_frames"`
	TotalTasks      int64  `json:"total_tasks"`
	// TotalChunks and TotalVideos exist in v2.1 and were dropped in v3.0.
	TotalChunks int64   `json:"total_chunks"`
	TotalVideos int64   `json:"total_videos"`
	ChunksSize  int64   `json:"chunks_size"`
	FPS         float64 `json:"fps"`
	// DataPath and VideoPath are Python format templates, e.g.
	// "data/chunk-{chunk_index:03d}/file-{file_index:03d}.parquet". They are
	// carried verbatim: resolving them is the byte-serving layer's job (W2),
	// and the two generations spell their placeholders differently.
	DataPath  string                 `json:"data_path"`
	VideoPath string                 `json:"video_path"`
	Splits    map[string]string      `json:"splits"`
	Features  map[string]InfoFeature `json:"features"`
}

// InfoFeature is one entry of info.json's `features` map.
type InfoFeature struct {
	DType string       `json:"dtype"`
	Shape []int        `json:"shape"`
	Names featureNames `json:"names"`
	FPS   float64      `json:"fps"`
	Info  *videoInfo   `json:"info"`
}

// videoInfo is the nested `info` block a video-typed feature carries.
type videoInfo struct {
	Height     int     `json:"video.height"`
	Width      int     `json:"video.width"`
	Codec      string  `json:"video.codec"`
	PixFmt     string  `json:"video.pix_fmt"`
	IsDepthMap bool    `json:"video.is_depth_map"`
	FPS        float64 `json:"video.fps"`
	Channels   int     `json:"video.channels"`
	HasAudio   bool    `json:"has_audio"`
}

// featureNames is info.json's `names`, which is three different things in
// files LeRobot actually ships:
//
//	null                                    scalar features (timestamp, index…)
//	["height", "width", "channel"]          a flat list
//	{"motors": ["motor_0", "motor_1", …]}   an object grouping the labels
//
// All three appear in the pinned fixtures, the object form on `action` and
// `observation.state` of a curated dataset. A plain []string field fails to
// unmarshal the object form and takes the whole info.json down with it, so the
// shape is absorbed here and callers see one flat, ordered list.
type featureNames []string

func (n *featureNames) UnmarshalJSON(b []byte) error {
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" || trimmed == "null" {
		*n = nil
		return nil
	}
	// The common two forms first.
	var flat []string
	if err := json.Unmarshal(b, &flat); err == nil {
		*n = flat
		return nil
	}
	var grouped map[string][]string
	if err := json.Unmarshal(b, &grouped); err == nil {
		// Sort the group keys so a multi-group feature yields a stable order;
		// Go map iteration is randomized and this list reaches a digest that
		// gets compared across reads.
		keys := make([]string, 0, len(grouped))
		for k := range grouped {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := []string{}
		for _, k := range keys {
			out = append(out, grouped[k]...)
		}
		*n = out
		return nil
	}
	// Anything else (a list of numbers, a nested object) is not a label list.
	// Names are cosmetic, so drop them rather than failing the whole file.
	*n = nil
	return nil
}

// readInfo loads and parses meta/info.json.
func readInfo(src Source) (*Info, error) {
	b, err := readCapped(src, "meta/info.json", MaxMetaFileBytes)
	if err != nil {
		return nil, fmt.Errorf("datasetmeta: read meta/info.json: %w", err)
	}
	var info Info
	if err := json.Unmarshal(b, &info); err != nil {
		return nil, fmt.Errorf("datasetmeta: parse meta/info.json: %w", err)
	}
	return &info, nil
}

// isVideoFeature reports whether a feature holds frames. LeRobot uses "video"
// for encoded streams and "image" for per-frame image files; both are things
// the player shows in a video pane.
func isVideoFeature(f InfoFeature) bool {
	return f.DType == "video" || f.DType == "image"
}

// split partitions info.json's features into video streams and everything
// else, each sorted by key so a digest is byte-stable across reads.
func (i *Info) split() ([]VideoStream, []Feature) {
	keys := make([]string, 0, len(i.Features))
	for k := range i.Features {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var streams []VideoStream
	var features []Feature
	for _, k := range keys {
		f := i.Features[k]
		if isVideoFeature(f) {
			streams = append(streams, videoStreamOf(k, f))
			continue
		}
		features = append(features, Feature{
			Key:   k,
			DType: f.DType,
			Shape: f.Shape,
			Names: f.Names,
		})
	}
	return streams, features
}

// videoStreamOf builds a VideoStream from one video-typed feature.
//
// Geometry is taken from the nested `info` block when present and falls back
// to `shape`, which is [height, width, channels] for video features. Note that
// a video feature's `names` are axis labels ("height", "width", "channel"),
// not component labels, so they are deliberately not carried onto the stream.
func videoStreamOf(key string, f InfoFeature) VideoStream {
	s := VideoStream{Key: key, Name: trailingSegment(key)}
	if len(f.Shape) >= 3 {
		s.Height, s.Width, s.Channels = f.Shape[0], f.Shape[1], f.Shape[2]
	}
	if f.Info != nil {
		if f.Info.Height > 0 {
			s.Height = f.Info.Height
		}
		if f.Info.Width > 0 {
			s.Width = f.Info.Width
		}
		if f.Info.Channels > 0 {
			s.Channels = f.Info.Channels
		}
		s.Codec = f.Info.Codec
		s.FPS = f.Info.FPS
		s.IsDepth = f.Info.IsDepthMap
		s.HasAudio = f.Info.HasAudio
	}
	if s.FPS == 0 {
		s.FPS = f.FPS
	}
	return s
}

// trailingSegment returns the part of a dotted feature key after the last dot
// ("observation.images.up" -> "up"), which is what a UI labels a video pane.
func trailingSegment(key string) string {
	if i := strings.LastIndex(key, "."); i >= 0 && i+1 < len(key) {
		return key[i+1:]
	}
	return key
}

// envRef derives the opaque provenance handle reserved by replay plan §3.
//
// Only the cheaply derivable case is filled: LeRobot names the embodiment in
// robot_type, so that becomes "lerobot:<robot_type>". The literal "unknown"
// that datasets converted from other formats carry is not an embodiment, and
// recording it would create a fake registry entry that later has to be
// un-picked. No @version is emitted because nothing here knows one; the field
// is opaque and unvalidated by contract.
func (i *Info) envRef() string {
	rt := strings.TrimSpace(i.RobotType)
	if rt == "" || strings.EqualFold(rt, "unknown") {
		return ""
	}
	return "lerobot:" + rt
}
