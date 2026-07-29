package datasetmeta

import (
	"math"
	"testing"
)

// Ground truth read off the real mp4s, not off this package's output. Both
// were downloaded from the pinned commits and parsed box-by-box:
//
//	v3.0  videos/observation.images.image/chunk-000/file-000.mp4
//	      296,535 bytes · 440 samples · 88.000s · 220 sync samples
//	v2.1  videos/chunk-000/observation.images.image/episode_000000.mp4
//	       19,686 bytes ·  40 samples ·  8.000s ·  20 sync samples
//
// The mp4s themselves are NOT committed — 300 KB of pixels prove nothing this
// package computes, and the paths and ranges are what it is responsible for.

func TestVideoPathsResolveForBothGenerations(t *testing.T) {
	// The templates differ in directory ORDER, not just in placeholder names,
	// which is why one expansion driven by info.json beats two hardcoded
	// layouts.
	page, err := ReadEpisodes(src(t, fixNyuV30), EpisodeRequest{Offset: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	v30 := page.Episodes[0].Videos["observation.images.image"]
	if want := "videos/observation.images.image/chunk-000/file-000.mp4"; v30.Path != want {
		t.Errorf("v3.0 path = %q, want %q", v30.Path, want)
	}

	page, err = ReadEpisodes(src(t, fixNyuV21), EpisodeRequest{Offset: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	v21 := page.Episodes[0].Videos["observation.images.image"]
	if want := "videos/chunk-000/observation.images.image/episode_000001.mp4"; v21.Path != want {
		t.Errorf("v2.1 path = %q, want %q", v21.Path, want)
	}
}

func TestVideoSlicesAreUniformAcrossGenerations(t *testing.T) {
	// The whole point of synthesizing v2.1's slices: a player asks "where, and
	// which seconds", and must not have to know which generation answered.
	// Episode 1 runs 8.0s-14.0s of the shared v3.0 file and 0.0s-6.0s of its
	// own v2.1 file — different numbers, same 6 seconds of robot.
	v30, err := ReadEpisodes(src(t, fixNyuV30), EpisodeRequest{Offset: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	v21, err := ReadEpisodes(src(t, fixNyuV21), EpisodeRequest{Offset: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	a := v30.Episodes[0].Videos["observation.images.image"]
	b := v21.Episodes[0].Videos["observation.images.image"]
	if a.FromTS != 8 || a.ToTS != 14 {
		t.Errorf("v3.0 slice = [%v,%v], want [8,14]", a.FromTS, a.ToTS)
	}
	if b.FromTS != 0 || b.ToTS != 6 {
		t.Errorf("v2.1 slice = [%v,%v], want [0,6]", b.FromTS, b.ToTS)
	}
	// Same duration is the invariant that survives the layout difference.
	if math.Abs((a.ToTS-a.FromTS)-(b.ToTS-b.FromTS)) > 1e-9 {
		t.Errorf("durations disagree: %v vs %v", a.ToTS-a.FromTS, b.ToTS-b.FromTS)
	}
	if a.Path == b.Path {
		t.Error("the two generations resolved to the same path, so one template was ignored")
	}
}

func TestEveryEpisodeGetsAVideoSlice(t *testing.T) {
	for _, root := range []string{fixNyuV21, fixNyuV30} {
		page, err := ReadEpisodes(src(t, root), EpisodeRequest{Offset: 0, Limit: 14})
		if err != nil {
			t.Fatal(err)
		}
		for _, ep := range page.Episodes {
			s, ok := ep.Videos["observation.images.image"]
			if !ok {
				t.Fatalf("%s episode %d has no video slice", root, ep.Index)
			}
			if s.Path == "" {
				t.Fatalf("%s episode %d has a slice with no path", root, ep.Index)
			}
			// A range that does not cover the episode's own duration would put
			// the player's stop point in the wrong place.
			if math.Abs((s.ToTS-s.FromTS)-ep.DurationSec) > 1e-6 {
				t.Errorf("%s episode %d: slice spans %v, episode is %v",
					root, ep.Index, s.ToTS-s.FromTS, ep.DurationSec)
			}
		}
	}
}

func TestV21EpisodePathsTrackTheChunk(t *testing.T) {
	// v2.1 derives the chunk from the episode index, the way LeRobot does.
	// nyu_rot has one chunk, so the fixture alone can never exercise the
	// divide — a synthetic info proves the arithmetic instead of assuming it.
	info := &Info{
		VideoPath:  "videos/chunk-{episode_chunk:03d}/{video_key}/episode_{episode_index:06d}.mp4",
		ChunksSize: 1000,
		FPS:        10,
		Features:   map[string]InfoFeature{"cam": {DType: "video"}},
	}
	for _, tc := range []struct {
		episode int64
		want    string
	}{
		{0, "videos/chunk-000/cam/episode_000000.mp4"},
		{999, "videos/chunk-000/cam/episode_000999.mp4"},
		{1000, "videos/chunk-001/cam/episode_001000.mp4"},
		{2500, "videos/chunk-002/cam/episode_002500.mp4"},
	} {
		ep := Episode{Index: tc.episode, Length: 20}
		synthesizeVideoSlices(info, &ep)
		if got := ep.Videos["cam"].Path; got != tc.want {
			t.Errorf("episode %d -> %q, want %q", tc.episode, got, tc.want)
		}
	}
}

func TestMultiCameraDatasetsGetOneSliceEach(t *testing.T) {
	// nyu_rot has a single camera, so it cannot catch a resolver that only
	// ever emits the first stream.
	info := &Info{
		VideoPath:  "videos/{video_key}/chunk-{chunk_index:03d}/file-{file_index:03d}.mp4",
		ChunksSize: 1000,
		FPS:        30,
		Features: map[string]InfoFeature{
			"observation.images.up":   {DType: "video"},
			"observation.images.side": {DType: "video"},
			"observation.state":       {DType: "float32"},
		},
	}
	ep := Episode{Index: 3, Length: 60}
	synthesizeVideoSlices(info, &ep)
	if len(ep.Videos) != 2 {
		t.Fatalf("got %d slices, want 2 (the non-video feature must not become one)", len(ep.Videos))
	}
	if got := ep.Videos["observation.images.side"].Path; got != "videos/observation.images.side/chunk-000/file-000.mp4" {
		t.Errorf("side camera -> %q", got)
	}
}

func TestVideoPathRefusesWhatItCannotResolve(t *testing.T) {
	// A stream whose path cannot be resolved is DROPPED, not returned
	// pathless: a slice a player cannot open renders as a broken pane, which
	// is worse than an honest gap.
	info := &Info{
		VideoPath: "videos/{mystery}/{video_key}.mp4",
		FPS:       10,
		Features:  map[string]InfoFeature{"cam": {DType: "video"}},
	}
	ep := Episode{Index: 0, Length: 10}
	synthesizeVideoSlices(info, &ep)
	if len(ep.Videos) != 0 {
		t.Errorf("an unresolvable template produced %v", ep.Videos)
	}

	ep = Episode{Index: 0, Length: 10, Videos: map[string]VideoSlice{"cam": {Chunk: 0, File: 0}}}
	attachVideoPaths(info, &ep)
	if len(ep.Videos) != 0 {
		t.Errorf("attach kept an unresolvable slice: %v", ep.Videos)
	}

	// The template is dataset-supplied, so it is untrusted input on a path.
	esc := &Info{
		VideoPath: "../../{video_key}.mp4",
		FPS:       10,
		Features:  map[string]InfoFeature{"cam": {DType: "video"}},
	}
	ep = Episode{Index: 0, Length: 10}
	synthesizeVideoSlices(esc, &ep)
	if len(ep.Videos) != 0 {
		t.Errorf("an escaping template resolved to %v", ep.Videos)
	}
}

func TestVideoKeysExcludeNonVideoFeatures(t *testing.T) {
	_, info, err := Sniff(src(t, fixNyuV21))
	if err != nil {
		t.Fatal(err)
	}
	keys := videoKeys(info)
	if len(keys) != 1 || keys[0] != "observation.images.image" {
		t.Errorf("video keys = %v", keys)
	}
}

func TestSynthesizedSliceSurvivesAMissingFPS(t *testing.T) {
	// Without fps there is no duration to derive, but the PATH — the part a
	// player cannot guess — must still be right. A [0,0) range falls back to
	// the file's own duration rather than refusing to play.
	info := &Info{
		VideoPath: "videos/chunk-{episode_chunk:03d}/{video_key}/episode_{episode_index:06d}.mp4",
		Features:  map[string]InfoFeature{"cam": {DType: "video"}},
	}
	ep := Episode{Index: 2, Length: 30}
	synthesizeVideoSlices(info, &ep)
	s := ep.Videos["cam"]
	if s.Path == "" {
		t.Fatal("no path without fps")
	}
	if s.FromTS != 0 || s.ToTS != 0 {
		t.Errorf("range = [%v,%v], want [0,0] when fps is unknown", s.FromTS, s.ToTS)
	}
}
