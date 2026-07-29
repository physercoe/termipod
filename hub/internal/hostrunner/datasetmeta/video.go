package datasetmeta

import (
	"fmt"
	"sort"
	"strings"
)

// Video locations (J8 W2d) — turning an episode into something playable.
//
// The player's job is "show me episode 7 of this camera", and the two
// generations answer it very differently: v2.1 gives each episode its own mp4,
// while v3.0 packs many into one and locates each by a timestamp range. Both
// are normalized here into the SAME VideoSlice — a path plus [from, to) — so
// nothing downstream has to branch on codebase_version.
//
// **No extraction, and no ffmpeg.** Measured against the pinned fixtures, a
// real LeRobot mp4 carries a keyframe every 0.4s (220 sync samples in a 440
// frame, 88-second v3.0 file — every second frame at 5 fps). A player seeking
// to an episode start therefore decodes at most one extra frame, which makes
// cutting the clip out server-side pure cost: a dependency, a temp file and a
// copy, to save nothing.
//
// That measurement also retires the plan's decision #1, which assumed
// host-side `ffmpeg -ss/-to -c copy` and worried whether episodes begin on
// keyframes. In these fixtures they do — but only because every episode length
// is even and keyframes fall on even frames, which is arithmetic, not a format
// guarantee. Seeking is robust to the odd-length case that would have broken a
// stream-copy cut, so the question stops mattering.

// videoPathVars builds the template variables for one video file.
//
// The generations disagree on both the placeholder names and the directory
// order:
//
//	v2.1  videos/chunk-{episode_chunk:03d}/{video_key}/episode_{episode_index:06d}.mp4
//	v3.0  videos/{video_key}/chunk-{chunk_index:03d}/file-{file_index:03d}.mp4
//
// Supplying every spelling lets one expansion serve both. An unknown
// placeholder still fails loudly — resolveDataPath refuses rather than
// substituting a blank.
func videoPathVars(episode, chunkSize, chunk, file int64) map[string]int64 {
	if chunkSize <= 0 {
		chunkSize = 1000
	}
	return map[string]int64{
		"episode_index": episode,
		"episode_chunk": episode / chunkSize,
		"chunk_index":   chunk,
		"file_index":    file,
	}
}

// resolveVideoPath expands info.json's video_path for one stream.
//
// video_key is substituted textually, before the numeric expansion, because it
// is a feature key ("observation.images.up") rather than a number — the
// template's only non-numeric placeholder.
func resolveVideoPath(tmpl, key string, vars map[string]int64) (string, error) {
	if tmpl == "" {
		return "", fmt.Errorf("datasetmeta: meta/info.json has no video_path template")
	}
	return resolveDataPath(substituteKey(tmpl, key), vars)
}

// substituteKey replaces the one textual placeholder in a video_path template.
func substituteKey(tmpl, key string) string {
	return strings.ReplaceAll(tmpl, "{video_key}", key)
}

// videoKeys returns the dataset's video-typed feature keys, sorted so a
// listing is stable across reads.
func videoKeys(info *Info) []string {
	var keys []string
	for k, f := range info.Features {
		if isVideoFeature(f) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// attachVideoPaths resolves a path onto each of an episode's video slices.
//
// v3.0 only: the slices already exist, carrying the chunk/file/timestamps read
// from the episode metadata, and this fills in where the bytes are. A stream
// whose path cannot be resolved is dropped rather than returned pathless — a
// slice a player cannot open is worse than an absent one, because it renders
// as a broken pane instead of an honest gap.
func attachVideoPaths(info *Info, ep *Episode) {
	if len(ep.Videos) == 0 {
		return
	}
	for key, slice := range ep.Videos {
		path, err := resolveVideoPath(info.VideoPath, key,
			videoPathVars(ep.Index, info.ChunksSize, slice.Chunk, slice.File))
		if err != nil {
			delete(ep.Videos, key)
			continue
		}
		slice.Path = path
		ep.Videos[key] = slice
	}
}

// synthesizeVideoSlices builds an episode's video slices for v2.1.
//
// v2.1 records no per-video metadata at all: one file per episode per camera,
// named by the path template, covering the whole episode. So the slice is
// derived rather than read — [0, duration) of its own file — which is what
// makes v2.1 and v3.0 look identical to a player.
//
// duration comes from the episode's frame count and the dataset fps. When fps
// is missing the range stays [0,0) and the player falls back to the file's own
// duration; the path, which is the part it cannot guess, is still correct.
func synthesizeVideoSlices(info *Info, ep *Episode) {
	keys := videoKeys(info)
	if len(keys) == 0 {
		return
	}
	dur := 0.0
	if info.FPS > 0 {
		dur = float64(ep.Length) / info.FPS
	}
	for _, key := range keys {
		path, err := resolveVideoPath(info.VideoPath, key,
			videoPathVars(ep.Index, info.ChunksSize, ep.Index/maxInt64(info.ChunksSize, 1), 0))
		if err != nil {
			continue
		}
		if ep.Videos == nil {
			ep.Videos = map[string]VideoSlice{}
		}
		ep.Videos[key] = VideoSlice{Path: path, FromTS: 0, ToTS: dur}
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
