package kimi_code

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// drainN reads up to n lines from ch or fails after timeout.
func drainN(t *testing.T, ch <-chan Line, n int, timeout time.Duration) []Line {
	t.Helper()
	out := make([]Line, 0, n)
	deadline := time.After(timeout)
	for len(out) < n {
		select {
		case l, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed early; got %d lines, want %d", len(out), n)
			}
			out = append(out, l)
		case <-deadline:
			t.Fatalf("timed out after reading %d/%d lines", len(out), n)
		}
	}
	return out
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatalf("write append: %v", err)
	}
}

func TestTailer_StartFromBeginning_ReadsExistingLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wire.jsonl")
	if err := os.WriteFile(path, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tl := &Tailer{Path: path, Mode: StartFromBeginning, PollEvery: 25 * time.Millisecond}
	ch, err := tl.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tl.Stop()

	lines := drainN(t, ch, 3, 1*time.Second)
	got := []string{string(lines[0].Bytes), string(lines[1].Bytes), string(lines[2].Bytes)}
	want := []string{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTailer_StartFromEnd_SkipsExistingThenReadsAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wire.jsonl")
	if err := os.WriteFile(path, []byte("old1\nold2\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tl := &Tailer{Path: path, Mode: StartFromEnd, PollEvery: 25 * time.Millisecond}
	ch, err := tl.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tl.Stop()

	select {
	case l := <-ch:
		t.Fatalf("got line %q while tailer should be waiting at EOF", l.Bytes)
	case <-time.After(75 * time.Millisecond):
	}

	appendLine(t, path, "new1\nnew2\n")
	lines := drainN(t, ch, 2, 1*time.Second)
	if string(lines[0].Bytes) != "new1" || string(lines[1].Bytes) != "new2" {
		t.Errorf("got %q %q, want new1 new2", lines[0].Bytes, lines[1].Bytes)
	}
}

// A partial trailing line (kimi mid-write — wire.jsonl's append/flush
// cadence is unverified per the P4 wedge) must NOT be emitted until its
// newline lands; the completed line then arrives whole.
func TestTailer_PartialTrailingLineHeldUntilComplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wire.jsonl")
	// Seed: one complete line + the first half of a second (no LF).
	if err := os.WriteFile(path, []byte(`{"type":"metadata","protocol_version":"1.4"}`+"\n"+`{"type":"usage.rec`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tl := &Tailer{Path: path, Mode: StartFromBeginning, PollEvery: 25 * time.Millisecond}
	ch, err := tl.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tl.Stop()

	first := drainN(t, ch, 1, 1*time.Second)
	if !strings.Contains(string(first[0].Bytes), "metadata") {
		t.Fatalf("first line = %q", first[0].Bytes)
	}

	// The torn half-line must not surface.
	select {
	case l := <-ch:
		t.Fatalf("partial line emitted prematurely: %q", l.Bytes)
	case <-time.After(150 * time.Millisecond):
	}

	// Complete the line; the WHOLE line arrives (not just the tail).
	appendLine(t, path, `ord","model":"k3","usage":{}}`+"\n")
	second := drainN(t, ch, 1, 1*time.Second)
	got := string(second[0].Bytes)
	if got != `{"type":"usage.record","model":"k3","usage":{}}` {
		t.Fatalf("completed line = %q", got)
	}
}

func TestTailer_HandlesTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wire.jsonl")
	if err := os.WriteFile(path, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tl := &Tailer{Path: path, Mode: StartFromBeginning, PollEvery: 25 * time.Millisecond}
	ch, err := tl.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tl.Stop()

	_ = drainN(t, ch, 3, 1*time.Second)

	// Truncate to a SHORTER body so the size-decrease check trips
	// (defensive: kimi never rotates today, mirroring the claude
	// tailer's guarantee against a future rotation).
	if err := os.WriteFile(path, []byte("z\n"), 0o644); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	lines := drainN(t, ch, 1, 2*time.Second)
	if string(lines[0].Bytes) != "z" {
		t.Errorf("line after truncation = %q, want z", lines[0].Bytes)
	}
}

func TestTailer_StopClosesChannel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wire.jsonl")
	if err := os.WriteFile(path, []byte("only\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tl := &Tailer{Path: path, Mode: StartFromBeginning, PollEvery: 25 * time.Millisecond}
	ch, err := tl.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = drainN(t, ch, 1, 500*time.Millisecond)

	var wg sync.WaitGroup
	wg.Add(1)
	closed := make(chan struct{})
	go func() {
		defer wg.Done()
		for range ch {
		}
		close(closed)
	}()
	tl.Stop()
	select {
	case <-closed:
	case <-time.After(1 * time.Second):
		t.Fatal("channel never closed after Stop")
	}
	wg.Wait()

	// Idempotent Stop.
	tl.Stop()
}

func TestTailer_OpenFailureSurfaces(t *testing.T) {
	tl := &Tailer{Path: "/this/path/does/not/exist", Mode: StartFromBeginning}
	_, err := tl.Start(context.Background())
	if err == nil {
		t.Fatal("Start returned nil on missing file")
	}
	if !strings.Contains(err.Error(), "open") {
		t.Errorf("err = %v; want wrap of open error", err)
	}
}

func TestTailer_UnknownStartModeFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wire.jsonl")
	_ = os.WriteFile(path, []byte("x\n"), 0o644)
	tl := &Tailer{Path: path, Mode: StartMode(99)}
	_, err := tl.Start(context.Background())
	if err == nil {
		t.Fatal("Start returned nil on bad StartMode")
	}
}
