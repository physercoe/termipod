package hostrunner

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// Q1 — pane-input hardening (pane-state-manifests plan). The generic
// PaneDriver serves every engine nobody wrote an M4 adapter for, and it sent
// a multi-line body with one `send-keys -l`, so the pane submitted at the
// first newline and the rest arrived as separate turns. The per-engine
// adapters were fixed for exactly this; this is the same fix for the
// fallback path.

// recordingTmux captures the tmux argv the driver issues.
type recordingTmux struct {
	mu    sync.Mutex
	calls [][]string
	err   error
}

func (r *recordingTmux) run(_ context.Context, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string(nil), args...))
	return r.err
}

func (r *recordingTmux) snapshot() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.calls))
	copy(out, r.calls)
	return out
}

func (r *recordingTmux) subcommands() []string {
	var names []string
	for _, c := range r.snapshot() {
		if len(c) > 0 {
			names = append(names, c[0])
		}
	}
	return names
}

// newRecordingDriver wires both seams so nothing can reach a real tmux
// server. On a developer box that server is the one the human is sitting in.
func newRecordingDriver() (*PaneDriver, *recordingTmux, *[]recordedSend, *sync.Mutex) {
	var mu sync.Mutex
	sends := &[]recordedSend{}
	tm := &recordingTmux{}
	d := &PaneDriver{
		AgentID: "agent-q1",
		PaneID:  "%42",
		Poster:  &fakePoster{},
		Tmux:    tm.run,
		SendKeys: func(_ context.Context, _, text string, literal bool) error {
			mu.Lock()
			*sends = append(*sends, recordedSend{text, literal})
			mu.Unlock()
			return nil
		},
		PasteSettle: time.Nanosecond, // don't sleep in tests
	}
	return d, tm, sends, &mu
}

// TestPaneInput_MultiLineBodyArrivesAsOneBlock is the defect this wedge
// closes. Before: one `send-keys -l` with embedded newlines, which the pane
// submits at the first one. After: set-buffer + paste-buffer + a single
// explicit Enter.
func TestPaneInput_MultiLineBodyArrivesAsOneBlock(t *testing.T) {
	d, tm, sends, mu := newRecordingDriver()
	body := "line one\nline two\nline three"

	if err := d.Input(context.Background(), "text", map[string]any{"body": body}); err != nil {
		t.Fatalf("Input: %v", err)
	}

	calls := tm.snapshot()
	if got := tm.subcommands(); len(got) != 2 || got[0] != "set-buffer" || got[1] != "paste-buffer" {
		t.Fatalf("tmux calls = %v, want [set-buffer paste-buffer]", got)
	}
	// The body goes into the buffer whole — newlines intact, not split.
	if calls[0][len(calls[0])-1] != body {
		t.Errorf("set-buffer payload = %q, want the body verbatim", calls[0][len(calls[0])-1])
	}
	// Exactly ONE Enter, ours, after the paste. Any more and the pane got
	// multiple submissions, which is the bug.
	mu.Lock()
	got := append([]recordedSend(nil), *sends...)
	mu.Unlock()
	if len(got) != 1 || got[0].text != "Enter" || got[0].literal {
		t.Fatalf("sends = %+v, want exactly one non-literal Enter", got)
	}
}

// TestPaneInput_PasteCarriesTheLoadBearingFlags — `-r` stops tmux rewriting
// each LF to a CR keystroke (the multi-submission bug in another costume),
// `-d` drops the buffer so concurrent inputs don't stack, and `-p` brackets
// the paste only if the app asked for bracketed paste (tmux(1)).
func TestPaneInput_PasteCarriesTheLoadBearingFlags(t *testing.T) {
	d, tm, _, _ := newRecordingDriver()
	if err := d.Input(context.Background(), "text", map[string]any{"body": "a\nb"}); err != nil {
		t.Fatalf("Input: %v", err)
	}
	paste := tm.snapshot()[1]
	joined := strings.Join(paste, " ")
	for _, flag := range []string{"-r", "-d", "-p"} {
		if !strings.Contains(joined, " "+flag+" ") && !strings.HasSuffix(joined, " "+flag) {
			t.Errorf("paste-buffer missing %s: %q", flag, joined)
		}
	}
	if !strings.Contains(joined, "-t %42") {
		t.Errorf("paste-buffer not targeted at the pane: %q", joined)
	}
}

// TestPaneInput_LongSingleLineAlsoPastes — the threshold is size OR
// newlines, matching the adapters, so a long prompt is not fed through
// send-keys a character at a time.
func TestPaneInput_LongSingleLineAlsoPastes(t *testing.T) {
	d, tm, _, _ := newRecordingDriver()
	body := strings.Repeat("x", paneInputInlineMax+1)
	if err := d.Input(context.Background(), "text", map[string]any{"body": body}); err != nil {
		t.Fatalf("Input: %v", err)
	}
	if got := tm.subcommands(); len(got) != 2 {
		t.Fatalf("tmux calls = %v, want the paste path", got)
	}
}

// TestPaneInput_CRLFBodyPastes — a body from an editor that writes \r\n must
// not take the cheap path either; the guard tests for both.
func TestPaneInput_CRLFBodyPastes(t *testing.T) {
	d, tm, _, _ := newRecordingDriver()
	if err := d.Input(context.Background(), "text", map[string]any{"body": "a\r\nb"}); err != nil {
		t.Fatalf("Input: %v", err)
	}
	if got := tm.subcommands(); len(got) != 2 {
		t.Fatalf("CRLF body took the send-keys path: %v", got)
	}
}

// TestPaneInput_ShortSingleLineKeepsTheCheapPath — the no-regression half.
// Ordinary short input must still be two send-keys calls and touch no
// buffer, or every existing pane interaction changes shape.
func TestPaneInput_ShortSingleLineKeepsTheCheapPath(t *testing.T) {
	d, tm, sends, mu := newRecordingDriver()
	if err := d.Input(context.Background(), "text", map[string]any{"body": "ls -la"}); err != nil {
		t.Fatalf("Input: %v", err)
	}
	if got := tm.subcommands(); len(got) != 0 {
		t.Errorf("short body used tmux buffers: %v", got)
	}
	mu.Lock()
	got := append([]recordedSend(nil), *sends...)
	mu.Unlock()
	want := []recordedSend{{"ls -la", true}, {"Enter", false}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("sends = %+v, want %+v", got, want)
	}
}

// TestPaneInput_MultiLineApprovalNoteAlsoPastes — an approval note is
// operator-authored free text and can carry a newline as easily as a prompt.
// It used to be concatenated into the same single send-keys.
func TestPaneInput_MultiLineApprovalNoteAlsoPastes(t *testing.T) {
	d, tm, _, _ := newRecordingDriver()
	err := d.Input(context.Background(), "approval", map[string]any{
		"decision": "allow", "note": "because\nof this",
	})
	if err != nil {
		t.Fatalf("Input: %v", err)
	}
	if got := tm.subcommands(); len(got) != 2 {
		t.Fatalf("multi-line approval note took the send-keys path: %v", got)
	}
}

// TestPaneInput_PasteFailureDeletesTheBuffer — a failed paste must not leave
// a buffer behind for the next input to inherit and paste twice.
func TestPaneInput_PasteFailureDeletesTheBuffer(t *testing.T) {
	d, tm, _, _ := newRecordingDriver()
	tm.err = context.DeadlineExceeded
	if err := d.Input(context.Background(), "text", map[string]any{"body": "a\nb"}); err == nil {
		t.Fatal("want an error when set-buffer fails")
	}
	// set-buffer failed, so we never reached paste and have nothing to clean.
	if got := tm.subcommands(); len(got) != 1 || got[0] != "set-buffer" {
		t.Fatalf("calls = %v, want to stop at the failed set-buffer", got)
	}

	// Now fail only the paste.
	d2, tm2, _, _ := newRecordingDriver()
	tm2.err = nil
	var n int
	d2.Tmux = func(ctx context.Context, args ...string) error {
		n++
		if err := tm2.run(ctx, args...); err != nil {
			return err
		}
		if args[0] == "paste-buffer" {
			return context.DeadlineExceeded
		}
		return nil
	}
	if err := d2.Input(context.Background(), "text", map[string]any{"body": "a\nb"}); err == nil {
		t.Fatal("want an error when paste-buffer fails")
	}
	got := tm2.subcommands()
	if len(got) != 3 || got[2] != "delete-buffer" {
		t.Errorf("calls = %v, want a delete-buffer cleanup after the failed paste", got)
	}
}

// TestPaneInput_BufferNameIsDerivedFromThePane — two panes must not share a
// buffer name, or concurrent inputs clobber each other. tmux buffer names are
// [A-Za-z0-9_-], and pane ids arrive as `%NN`.
func TestPaneInput_BufferNameIsDerivedFromThePane(t *testing.T) {
	names := map[string]string{}
	for _, pane := range []string{"%42", "%7", "session:1.0"} {
		d, tm, _, _ := newRecordingDriver()
		d.PaneID = pane
		if err := d.Input(context.Background(), "text", map[string]any{"body": "a\nb"}); err != nil {
			t.Fatalf("%s: %v", pane, err)
		}
		set := tm.snapshot()[0]
		name := set[2] // set-buffer -b <name> <body>
		if strings.ContainsAny(name, "%:.") {
			t.Errorf("pane %q produced buffer name %q with characters tmux rejects", pane, name)
		}
		if prev, dup := names[name]; dup {
			t.Errorf("panes %q and %q share buffer name %q", prev, pane, name)
		}
		names[name] = pane
	}
}

// TestPaneInput_StubbedSendKeysWithoutTmuxRefuses — a harness that stubs one
// seam and not the other must NOT fall through to a real `tmux` exec. This is
// the guard that keeps a unit test off the developer's own tmux server.
func TestPaneInput_StubbedSendKeysWithoutTmuxRefuses(t *testing.T) {
	d := &PaneDriver{
		AgentID: "agent-halfstub",
		PaneID:  "%1",
		Poster:  &fakePoster{},
		SendKeys: func(context.Context, string, string, bool) error {
			return nil
		},
		PasteSettle: time.Nanosecond,
	}
	err := d.Input(context.Background(), "text", map[string]any{"body": "a\nb"})
	if err == nil {
		t.Fatal("want a refusal, not a real tmux exec")
	}
	if !strings.Contains(err.Error(), "SendKeys seam") {
		t.Errorf("error should name the half-stubbed seam; got %v", err)
	}
}

// TestPaneInput_SettleDelayIsObserved — the pause exists so the TUI ingests
// the paste before being asked to submit. Assert it is actually waited on
// rather than being a field nobody reads.
func TestPaneInput_SettleDelayIsObserved(t *testing.T) {
	d, _, _, _ := newRecordingDriver()
	d.PasteSettle = 40 * time.Millisecond
	start := time.Now()
	if err := d.Input(context.Background(), "text", map[string]any{"body": "a\nb"}); err != nil {
		t.Fatalf("Input: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Errorf("Input returned after %v, want at least the 40ms settle", elapsed)
	}
}

// TestPaneInput_SettleDelayHonoursContextCancellation — the wait must not
// outlive its context, or a shutdown blocks on every queued input.
func TestPaneInput_SettleDelayHonoursContextCancellation(t *testing.T) {
	d, _, _, _ := newRecordingDriver()
	d.PasteSettle = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := d.Input(ctx, "text", map[string]any{"body": "a\nb"}); err == nil {
		t.Fatal("want the context error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waited %v on a cancelled context", elapsed)
	}
}
