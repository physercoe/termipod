package hostrunner

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"log/slog"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/termipod/hub/internal/hostrunner/tbreader"
)

// ---- tiny tfevents encoder (test-only) ----
//
// Builds a valid TFRecord-framed tensorflow.Event stream from hand.
// Only the fields the reader decodes are emitted. Keeps the test
// self-contained so we don't depend on tbreader's internal test
// helpers (those are in package tbreader and not visible here).

var tbCastagnoli = crc32.MakeTable(crc32.Castagnoli)

func tbMaskedCRC(b []byte) uint32 {
	c := crc32.Checksum(b, tbCastagnoli)
	return ((c >> 15) | (c << 17)) + 0xa282ead8
}

func tbWriteRecord(w io.Writer, payload []byte) {
	var hdr [12]byte
	binary.LittleEndian.PutUint64(hdr[0:8], uint64(len(payload)))
	binary.LittleEndian.PutUint32(hdr[8:12], tbMaskedCRC(hdr[0:8]))
	_, _ = w.Write(hdr[:])
	_, _ = w.Write(payload)
	var tail [4]byte
	binary.LittleEndian.PutUint32(tail[:], tbMaskedCRC(payload))
	_, _ = w.Write(tail[:])
}

func pbVarintTB(v uint64) []byte {
	var out []byte
	for v >= 0x80 {
		out = append(out, byte(v)|0x80)
		v >>= 7
	}
	return append(out, byte(v))
}

func pbTagTB(field, wire int) []byte {
	return pbVarintTB(uint64(field)<<3 | uint64(wire))
}

func pbLenDelimTB(field int, body []byte) []byte {
	out := pbTagTB(field, 2)
	out = append(out, pbVarintTB(uint64(len(body)))...)
	return append(out, body...)
}

func pbFixed32TB(field int, v uint32) []byte {
	out := pbTagTB(field, 5)
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return append(out, b[:]...)
}

func pbFixed64TB(field int, v uint64) []byte {
	out := pbTagTB(field, 1)
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return append(out, b[:]...)
}

// buildScalarEvent packages one {step, tag, value} sample into an
// encoded Event.
func buildScalarEvent(step int64, tag string, value float32) []byte {
	// Summary.Value: tag=1 string, simple_value=2 fixed32 float
	val := pbLenDelimTB(1, []byte(tag))
	val = append(val, pbFixed32TB(2, math.Float32bits(value))...)

	// Summary: value=1 repeated length-delimited
	summary := pbLenDelimTB(1, val)

	// Event: wall_time=1 double, step=2 varint, summary=5 message
	var ev []byte
	ev = append(ev, pbFixed64TB(1, math.Float64bits(1.0))...)
	ev = append(ev, pbTagTB(2, 0)...)
	ev = append(ev, pbVarintTB(uint64(step))...)
	ev = append(ev, pbLenDelimTB(5, summary)...)
	return ev
}

// mustSeedTensorBoard writes a synthetic tfevents file at
// {root}/{runPath}/events.out.tfevents.1.host.1.v2 with the supplied
// (step, value) pairs all logged under a single tag "loss".
func mustSeedTensorBoard(t *testing.T, root, runPath string, points [][2]any) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(runPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var buf bytes.Buffer

	// file_version record (first event in every tfevents file).
	var fv []byte
	fv = append(fv, pbFixed64TB(1, math.Float64bits(0.0))...)
	fv = append(fv, pbLenDelimTB(3, []byte("brain.Event:2"))...)
	tbWriteRecord(&buf, fv)

	for _, p := range points {
		step := p[0].(int64)
		v, _ := toFloat32(p[1])
		tbWriteRecord(&buf, buildScalarEvent(step, "loss", v))
	}
	path := filepath.Join(dir, "events.out.tfevents.1.host.1.v2")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func toFloat32(v any) (float32, bool) {
	switch x := v.(type) {
	case float32:
		return x, true
	case float64:
		return float32(x), true
	case int:
		return float32(x), true
	case int64:
		return float32(x), true
	}
	return 0, false
}

// ---- tests ----

func TestTBTick_PushesDigestForMatchingRun(t *testing.T) {
	dir := t.TempDir()
	mustSeedTensorBoard(t, dir, "run-a", [][2]any{
		{int64(0), 2.5},
		{int64(50), 1.8},
		{int64(100), 1.23},
	})

	fake := &fakeHub{
		runs: []Run{{
			ID:            "run-42",
			TrackioHostID: "host-x",
			TrackioRunURI: "tb://run-a",
			Status:        "running",
		}},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	r := &Runner{
		Client:               NewClient(srv.URL, "t", "default"),
		HostID:               "host-x",
		Log:                  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r.metricsTick(context.Background(), tbreader.New(dir), 100)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	got := fake.puts["run-42"]
	if len(got) != 1 || got[0].Name != "loss" {
		t.Fatalf("puts[run-42] = %+v, want one loss series", got)
	}
	if got[0].SampleCount != 3 {
		t.Errorf("sample_count = %d, want 3", got[0].SampleCount)
	}
	if got[0].LastStep == nil || *got[0].LastStep != 100 {
		t.Errorf("last_step = %v, want 100", got[0].LastStep)
	}
	if got[0].LastValue == nil {
		t.Fatalf("last_value missing")
	}
	// tfevents simple_value is float32 on the wire, so the round-trip
	// loses a few bits. Accept within epsilon.
	if math.Abs(*got[0].LastValue-1.23) > 1e-5 {
		t.Errorf("last_value = %v, want ~1.23", *got[0].LastValue)
	}
	if len(got[0].Points) != 3 {
		t.Errorf("points len = %d, want 3 (under max)", len(got[0].Points))
	}
}

func TestTBTick_SkipsRunsWithNonTBURI(t *testing.T) {
	dir := t.TempDir()
	// trackio:// URI — this loop must not touch it.
	fake := &fakeHub{
		runs: []Run{
			{ID: "run-tk", TrackioHostID: "host-x",
				TrackioRunURI: "trackio://nano/run-a", Status: "running"},
		},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	r := &Runner{
		Client:               NewClient(srv.URL, "t", "default"),
		HostID:               "host-x",
		Log:                  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r.metricsTick(context.Background(), tbreader.New(dir), 100)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.puts) != 0 {
		t.Errorf("put %d runs, want 0 (non-tb URI should be skipped)", len(fake.puts))
	}
}

func TestTBTick_SkipsRunsWithEmptySeries(t *testing.T) {
	dir := t.TempDir()
	// No run dir created — ReadRun returns empty, tick should not PUT.
	fake := &fakeHub{
		runs: []Run{
			{ID: "run-1", TrackioHostID: "host-x", TrackioRunURI: "tb://run-a"},
		},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	r := &Runner{
		Client:               NewClient(srv.URL, "t", "default"),
		HostID:               "host-x",
		Log:                  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r.metricsTick(context.Background(), tbreader.New(dir), 100)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.puts) != 0 {
		t.Errorf("put %d runs, want 0 (empty series should not PUT)", len(fake.puts))
	}
}
