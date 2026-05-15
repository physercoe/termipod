package claudecode

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
	path := filepath.Join(dir, "s.jsonl")
	body := "a\nb\nc\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
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
	path := filepath.Join(dir, "s.jsonl")
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

	// No lines available immediately (we started at EOF).
	select {
	case l := <-ch:
		t.Fatalf("got line %q while tailer should be waiting at EOF", l.Bytes)
	case <-time.After(75 * time.Millisecond):
	}

	// Append two lines; both should arrive.
	appendLine(t, path, "new1\nnew2\n")
	lines := drainN(t, ch, 2, 1*time.Second)
	if string(lines[0].Bytes) != "new1" || string(lines[1].Bytes) != "new2" {
		t.Errorf("got %q %q, want new1 new2",
			lines[0].Bytes, lines[1].Bytes)
	}
}

func TestTailer_TrimsTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	// CRLF mid-stream + plain LF: the trimmer should remove both.
	if err := os.WriteFile(path, []byte("crlf\r\nplain\n"), 0o644); err != nil {
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
	lines := drainN(t, ch, 2, 1*time.Second)
	if string(lines[0].Bytes) != "crlf" {
		t.Errorf("line 0 = %q, want crlf", lines[0].Bytes)
	}
	if string(lines[1].Bytes) != "plain" {
		t.Errorf("line 1 = %q, want plain", lines[1].Bytes)
	}
}

func TestTailer_HandlesTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
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

	// Drain the initial 3 lines. After this the tailer is at pos=6
	// (EOF of the 6-byte file).
	_ = drainN(t, ch, 3, 1*time.Second)

	// Truncate the file to a SHORTER body so the size-decrease check
	// trips. claude-code itself never does this in production; the
	// test exists to guarantee we'd survive a hypothetical future
	// rotation that landed a shorter file.
	if err := os.WriteFile(path, []byte("z\n"), 0o644); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	// On the next poll the tailer should see size(2) < pos(6),
	// re-seek to start, and deliver the new line.
	lines := drainN(t, ch, 1, 2*time.Second)
	if string(lines[0].Bytes) != "z" {
		t.Errorf("line after truncation = %q, want z", lines[0].Bytes)
	}
}

func TestTailer_StopClosesChannel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
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

	// Stop should close the channel synchronously.
	var wg sync.WaitGroup
	wg.Add(1)
	closed := make(chan struct{})
	go func() {
		defer wg.Done()
		// Drain remaining lines until close.
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
	path := filepath.Join(dir, "s.jsonl")
	_ = os.WriteFile(path, []byte("x\n"), 0o644)
	tl := &Tailer{Path: path, Mode: StartMode(99)}
	_, err := tl.Start(context.Background())
	if err == nil {
		t.Fatal("Start returned nil on bad StartMode")
	}
}
