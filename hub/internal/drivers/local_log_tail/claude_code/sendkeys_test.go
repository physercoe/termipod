package claudecode

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// errFake is a sentinel for tests that need a Run() to fail.
var errFake = errors.New("fake-runner-failure")

// runnerFunc adapts a function to the CmdRunner interface so tests can
// inject per-call behaviour (e.g. "succeed on set-buffer, fail on
// paste-buffer") without growing recordingRunner.
type runnerFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

func (r runnerFunc) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return r(ctx, name, args...)
}

// recordingRunner captures the (name, args) pairs Run was called
// with; tests inspect the captured slice instead of stubbing per-
// command outputs (no command we test here produces meaningful
// stdout).
type recordingRunner struct {
	mu    sync.Mutex
	calls []recordedCall
	err   error // if non-nil, every Run returns it; tests use this for failure paths
}

type recordedCall struct {
	name string
	args []string
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	cp := make([]string, len(args))
	copy(cp, args)
	r.calls = append(r.calls, recordedCall{name: name, args: cp})
	r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	return nil, nil
}

func (r *recordingRunner) snapshot() []recordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedCall, len(r.calls))
	copy(out, r.calls)
	return out
}

func sendkeysAdapter(t *testing.T, pane string) (*Adapter, *recordingRunner) {
	t.Helper()
	a, err := NewAdapter(Config{AgentID: "a", Workdir: "/tmp/x", Poster: &stubPoster{}})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	a.PaneID = pane
	r := &recordingRunner{}
	a.CmdRunner = r
	return a, r
}

func TestHandleInput_TextShortBody(t *testing.T) {
	a, r := sendkeysAdapter(t, "%42")
	if err := a.HandleInput(context.Background(), "text", map[string]any{"body": "hello"}); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	calls := r.snapshot()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2 (literal + Enter)", len(calls))
	}
	if !equalArgs(calls[0], "tmux", "send-keys", "-t", "%42", "-l", "hello") {
		t.Errorf("call 0 = %+v", calls[0])
	}
	if !equalArgs(calls[1], "tmux", "send-keys", "-t", "%42", "Enter") {
		t.Errorf("call 1 = %+v", calls[1])
	}
}

func TestHandleInput_SlashCommandRoutesAsText(t *testing.T) {
	a, r := sendkeysAdapter(t, "%42")
	if err := a.HandleInput(context.Background(), "slash_command", map[string]any{"body": "/clear"}); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	calls := r.snapshot()
	if !equalArgs(calls[0], "tmux", "send-keys", "-t", "%42", "-l", "/clear") {
		t.Errorf("slash_command call 0 = %+v", calls[0])
	}
}

// Multi-line bodies MUST land as ONE atomic submission via tmux's
// named-buffer paste path — set-buffer / paste-buffer -d -r / Enter —
// not as N per-line `send-keys -l + Enter` pairs. The pre-v1.0.658
// per-line path explicitly inserted `send-keys Enter` between every
// line, so an N-line message arrived as N separate turns at claude's
// TUI input. The new path keeps LF as LF on the wire (`-r` flag) and
// only triggers submission with our explicit final Enter. Same fix
// shape as the agy v1.0.652 paste-buffer-`-r` flag.
func TestHandleInput_TextMultilineUsesAtomicPasteBuffer(t *testing.T) {
	a, r := sendkeysAdapter(t, "%42")
	body := "line one\nline two\nline three"
	if err := a.HandleInput(context.Background(), "text", map[string]any{"body": body}); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	calls := r.snapshot()
	if len(calls) != 3 {
		t.Fatalf("multiline calls = %d, want 3 (set-buffer + paste-buffer + Enter); got %+v",
			len(calls), calls)
	}
	if !equalArgs(calls[0], "tmux", "set-buffer", "-b", "ccinput_42", body) {
		t.Errorf("call 0 = %+v; want set-buffer with full body", calls[0])
	}
	// paste-buffer MUST carry -r so tmux doesn't translate the
	// body's internal LF bytes into CR (Enter) keystrokes.
	if !equalArgs(calls[1], "tmux", "paste-buffer", "-b", "ccinput_42", "-d", "-r", "-t", "%42") {
		t.Errorf("call 1 = %+v; want paste-buffer -d -r", calls[1])
	}
	if !equalArgs(calls[2], "tmux", "send-keys", "-t", "%42", "Enter") {
		t.Errorf("call 2 = %+v; want a single trailing Enter", calls[2])
	}
}

// Long single-line bodies (>512 chars, no newlines) take the same
// atomic paste-buffer path. The 512-char cutoff exists because very
// long send-keys -l argv strings hit tmux's max argument length on
// some shells; paste-buffer side-steps that.
func TestHandleInput_TextLongSingleLineUsesPasteBuffer(t *testing.T) {
	a, r := sendkeysAdapter(t, "%42")
	body := strings.Repeat("x", 600)
	if err := a.HandleInput(context.Background(), "text", map[string]any{"body": body}); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	calls := r.snapshot()
	if len(calls) != 3 {
		t.Fatalf("long-single-line calls = %d, want 3 (set-buffer + paste-buffer + Enter)", len(calls))
	}
	if calls[0].name != "tmux" || calls[0].args[0] != "set-buffer" {
		t.Errorf("call 0 = %+v; want set-buffer", calls[0])
	}
	if !equalArgs(calls[1], "tmux", "paste-buffer", "-b", "ccinput_42", "-d", "-r", "-t", "%42") {
		t.Errorf("call 1 = %+v; want paste-buffer -d -r", calls[1])
	}
}

// CRLF bodies (an editor that wrote `\r\n` line endings) MUST also
// take the paste-buffer path — the cheap-path guard tests for both
// `\n` AND `\r` via strings.ContainsAny. Pre-v1.0.658 a CRLF body
// would have fallen through Split(\n) and inserted stray CR bytes
// into each line; the new path leaves them untouched in the buffer.
func TestHandleInput_TextCRLFUsesPasteBuffer(t *testing.T) {
	a, r := sendkeysAdapter(t, "%42")
	if err := a.HandleInput(context.Background(), "text", map[string]any{"body": "alpha\r\nbeta"}); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	calls := r.snapshot()
	if len(calls) != 3 {
		t.Fatalf("crlf calls = %d, want 3", len(calls))
	}
	if calls[0].args[0] != "set-buffer" {
		t.Errorf("call 0 verb = %q; want set-buffer", calls[0].args[0])
	}
}

// On paste-buffer failure the adapter MUST attempt buffer cleanup so a
// stale buffer doesn't survive to the next call (where it would be
// silently overwritten with `-b` collision but at least we tried).
func TestHandleInput_TextMultilineCleansBufferOnPasteFailure(t *testing.T) {
	a, _ := sendkeysAdapter(t, "%42")
	// Custom runner: succeed on set-buffer, fail on paste-buffer,
	// capture every call.
	calls := []recordedCall{}
	a.CmdRunner = runnerFunc(func(_ context.Context, name string, args ...string) ([]byte, error) {
		cp := make([]string, len(args))
		copy(cp, args)
		calls = append(calls, recordedCall{name: name, args: cp})
		if len(args) > 0 && args[0] == "paste-buffer" {
			return nil, errFake
		}
		return nil, nil
	})

	err := a.HandleInput(context.Background(), "text", map[string]any{"body": "a\nb"})
	if err == nil {
		t.Fatal("expected paste-buffer failure to surface as error")
	}
	if !strings.Contains(err.Error(), "paste-buffer") {
		t.Errorf("err = %v; want mention of paste-buffer", err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3 (set-buffer + paste-buffer + delete-buffer cleanup)", len(calls))
	}
	if calls[2].args[0] != "delete-buffer" {
		t.Errorf("call 2 = %+v; want delete-buffer cleanup", calls[2])
	}
}

func TestHandleInput_CancelSendsCtrlC(t *testing.T) {
	a, r := sendkeysAdapter(t, "%42")
	if err := a.HandleInput(context.Background(), "cancel", nil); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	calls := r.snapshot()
	if len(calls) != 1 || !equalArgs(calls[0], "tmux", "send-keys", "-t", "%42", "C-c") {
		t.Errorf("calls = %+v", calls)
	}
}

func TestHandleInput_HardCancelSendsSIGINT(t *testing.T) {
	a, r := sendkeysAdapter(t, "%42")
	a.ClaudePID = 12345
	if err := a.HandleInput(context.Background(), "hard_cancel", nil); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	calls := r.snapshot()
	if len(calls) != 1 || !equalArgs(calls[0], "kill", "-INT", "12345") {
		t.Errorf("calls = %+v, want kill -INT 12345", calls)
	}
}

func TestHandleInput_HardCancelRejectsMissingPID(t *testing.T) {
	a, _ := sendkeysAdapter(t, "%42")
	// ClaudePID is 0 (unset).
	if err := a.HandleInput(context.Background(), "hard_cancel", nil); err == nil {
		t.Fatal("hard_cancel with no PID returned nil error")
	}
}

func TestHandleInput_EscapeAndModeCycle(t *testing.T) {
	a, r := sendkeysAdapter(t, "%42")
	_ = a.HandleInput(context.Background(), "escape", nil)
	_ = a.HandleInput(context.Background(), "mode_cycle", nil)
	calls := r.snapshot()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if !equalArgs(calls[0], "tmux", "send-keys", "-t", "%42", "Escape") {
		t.Errorf("escape call = %+v", calls[0])
	}
	if !equalArgs(calls[1], "tmux", "send-keys", "-t", "%42", "S-Tab") {
		t.Errorf("mode_cycle call = %+v", calls[1])
	}
}

func TestHandleInput_ActionBarSendsNamedKey(t *testing.T) {
	a, r := sendkeysAdapter(t, "%42")
	if err := a.HandleInput(context.Background(), "action_bar", map[string]any{"name": "F2"}); err != nil {
		t.Fatalf("F2: %v", err)
	}
	if err := a.HandleInput(context.Background(), "action_bar", map[string]any{"name": "Up"}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	calls := r.snapshot()
	if !equalArgs(calls[0], "tmux", "send-keys", "-t", "%42", "F2") {
		t.Errorf("call 0 = %+v", calls[0])
	}
	if !equalArgs(calls[1], "tmux", "send-keys", "-t", "%42", "Up") {
		t.Errorf("call 1 = %+v", calls[1])
	}
}

func TestHandleInput_ActionBarRejectsArbitraryKey(t *testing.T) {
	a, _ := sendkeysAdapter(t, "%42")
	if err := a.HandleInput(context.Background(), "action_bar", map[string]any{"name": "; rm -rf /"}); err == nil {
		t.Fatal("arbitrary key string was accepted")
	}
	// Should error before any tmux call is issued.
}

func TestHandleInput_PickOptionFirstOption(t *testing.T) {
	a, r := sendkeysAdapter(t, "%42")
	if err := a.HandleInput(context.Background(), "pick_option", map[string]any{"index": float64(0)}); err != nil {
		t.Fatalf("pick_option 0: %v", err)
	}
	calls := r.snapshot()
	// idx=0 should be just one Enter, no Down keystrokes.
	if len(calls) != 1 || !equalArgs(calls[0], "tmux", "send-keys", "-t", "%42", "Enter") {
		t.Errorf("idx=0 calls = %+v, want one Enter", calls)
	}
}

func TestHandleInput_PickOptionThirdOption(t *testing.T) {
	a, r := sendkeysAdapter(t, "%42")
	if err := a.HandleInput(context.Background(), "pick_option", map[string]any{"index": float64(2)}); err != nil {
		t.Fatalf("pick_option 2: %v", err)
	}
	calls := r.snapshot()
	// idx=2 → Down Down Enter.
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(calls))
	}
	if !equalArgs(calls[0], "tmux", "send-keys", "-t", "%42", "Down") {
		t.Errorf("call 0 = %+v", calls[0])
	}
	if !equalArgs(calls[1], "tmux", "send-keys", "-t", "%42", "Down") {
		t.Errorf("call 1 = %+v", calls[1])
	}
	if !equalArgs(calls[2], "tmux", "send-keys", "-t", "%42", "Enter") {
		t.Errorf("call 2 = %+v", calls[2])
	}
}

func TestHandleInput_PickOptionOutOfRange(t *testing.T) {
	a, _ := sendkeysAdapter(t, "%42")
	if err := a.HandleInput(context.Background(), "pick_option", map[string]any{"index": float64(99)}); err == nil {
		t.Fatal("pick_option index 99 was accepted")
	}
	if err := a.HandleInput(context.Background(), "pick_option", map[string]any{"index": float64(-1)}); err == nil {
		t.Fatal("pick_option index -1 was accepted")
	}
}

func TestHandleInput_ApprovalRejected(t *testing.T) {
	a, _ := sendkeysAdapter(t, "%42")
	err := a.HandleInput(context.Background(), "approval", map[string]any{"decision": "approve"})
	if err == nil {
		t.Fatal("approval input was accepted; must route via MCP permission channel")
	}
	if !strings.Contains(err.Error(), "permission_prompt") {
		t.Errorf("error did not mention permission channel: %v", err)
	}
}

func TestHandleInput_TextMissingBody(t *testing.T) {
	a, _ := sendkeysAdapter(t, "%42")
	if err := a.HandleInput(context.Background(), "text", map[string]any{}); err == nil {
		t.Fatal("text with missing body was accepted")
	}
}

func TestHandleInput_TextMissingPane(t *testing.T) {
	a, _ := sendkeysAdapter(t, "")
	if err := a.HandleInput(context.Background(), "text", map[string]any{"body": "hi"}); err == nil {
		t.Fatal("text with no PaneID was accepted")
	}
}

func TestHandleInput_UnknownKindRejected(t *testing.T) {
	a, _ := sendkeysAdapter(t, "%42")
	if err := a.HandleInput(context.Background(), "telepathy", nil); err == nil {
		t.Fatal("unknown kind was accepted")
	}
}

func equalArgs(got recordedCall, name string, args ...string) bool {
	if got.name != name {
		return false
	}
	if len(got.args) != len(args) {
		return false
	}
	for i := range args {
		if got.args[i] != args[i] {
			return false
		}
	}
	return true
}
