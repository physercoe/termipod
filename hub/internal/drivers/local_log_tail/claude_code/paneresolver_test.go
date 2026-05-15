package claudecode

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// scriptedRunner returns canned outputs for known (name, args)
// signatures. The signature is built as `name args...` joined by
// single spaces. Tests register expected commands + their stdout;
// unregistered commands return an error so a test that triggers an
// unexpected exec call fails loudly.
type scriptedRunner struct {
	scripts map[string]string
	errs    map[string]error
}

func newScriptedRunner() *scriptedRunner {
	return &scriptedRunner{scripts: map[string]string{}, errs: map[string]error{}}
}

func (r *scriptedRunner) expect(out string, name string, args ...string) {
	r.scripts[r.key(name, args...)] = out
}

func (r *scriptedRunner) expectErr(err error, name string, args ...string) {
	r.errs[r.key(name, args...)] = err
}

func (r *scriptedRunner) key(name string, args ...string) string {
	return name + " " + strings.Join(args, " ")
}

func (r *scriptedRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	k := r.key(name, args...)
	if err, ok := r.errs[k]; ok {
		return nil, err
	}
	out, ok := r.scripts[k]
	if !ok {
		return nil, fmt.Errorf("scriptedRunner: unexpected command %q", k)
	}
	return []byte(out), nil
}

func TestResolvePane_SinglePane(t *testing.T) {
	r := newScriptedRunner()
	r.expect("12345\n", "ps", "-o", "ppid=", "-p", "12399")
	r.expect("12345 %42 1 1234567890\n", "tmux",
		"list-panes", "-aF",
		"#{pane_pid} #{pane_id} #{pane_active} #{session_activity}")
	got, err := ResolvePane(context.Background(), 12399, r)
	if err != nil {
		t.Fatalf("ResolvePane: %v", err)
	}
	if got != "%42" {
		t.Errorf("paneID = %q, want %%42", got)
	}
}

func TestResolvePane_DisambiguatesByActiveThenActivity(t *testing.T) {
	r := newScriptedRunner()
	r.expect("9999\n", "ps", "-o", "ppid=", "-p", "10000")
	// 3 candidates with parent pid 9999:
	//   %1: not active, activity 1000
	//   %2: active,     activity 500
	//   %3: not active, activity 2000  (newer than %1)
	// Expected pick: %2 (active wins).
	tmuxOut := strings.Join([]string{
		"9999 %1 0 1000",
		"9999 %2 1 500",
		"9999 %3 0 2000",
		"7777 %4 1 9999", // sibling with wrong pane_pid; ignored
	}, "\n")
	r.expect(tmuxOut+"\n", "tmux",
		"list-panes", "-aF",
		"#{pane_pid} #{pane_id} #{pane_active} #{session_activity}")
	got, err := ResolvePane(context.Background(), 10000, r)
	if err != nil {
		t.Fatalf("ResolvePane: %v", err)
	}
	if got != "%2" {
		t.Errorf("disambiguated = %q, want %%2 (active row)", got)
	}
}

func TestResolvePane_DisambiguatesByActivityWhenAllInactive(t *testing.T) {
	r := newScriptedRunner()
	r.expect("9999\n", "ps", "-o", "ppid=", "-p", "10000")
	tmuxOut := strings.Join([]string{
		"9999 %1 0 1000",
		"9999 %2 0 2000",
		"9999 %3 0 1500",
	}, "\n")
	r.expect(tmuxOut+"\n", "tmux",
		"list-panes", "-aF",
		"#{pane_pid} #{pane_id} #{pane_active} #{session_activity}")
	got, _ := ResolvePane(context.Background(), 10000, r)
	if got != "%2" {
		t.Errorf("disambiguated = %q, want %%2 (newest activity)", got)
	}
}

func TestResolvePane_NoMatchingPane(t *testing.T) {
	r := newScriptedRunner()
	r.expect("9999\n", "ps", "-o", "ppid=", "-p", "10000")
	r.expect("7777 %1 1 100\n", "tmux",
		"list-panes", "-aF",
		"#{pane_pid} #{pane_id} #{pane_active} #{session_activity}")
	_, err := ResolvePane(context.Background(), 10000, r)
	if err == nil {
		t.Fatal("ResolvePane returned nil error when no pane matches")
	}
}

func TestResolvePane_InvalidPID(t *testing.T) {
	if _, err := ResolvePane(context.Background(), 0, nil); err == nil {
		t.Error("want error for pid 0")
	}
	if _, err := ResolvePane(context.Background(), -5, nil); err == nil {
		t.Error("want error for negative pid")
	}
}

func TestResolvePane_PSError(t *testing.T) {
	r := newScriptedRunner()
	r.expectErr(fmt.Errorf("no such process"), "ps", "-o", "ppid=", "-p", "12399")
	_, err := ResolvePane(context.Background(), 12399, r)
	if err == nil {
		t.Fatal("want error when ps fails")
	}
}

func TestResolvePane_ParentZero(t *testing.T) {
	r := newScriptedRunner()
	r.expect("0\n", "ps", "-o", "ppid=", "-p", "12399")
	_, err := ResolvePane(context.Background(), 12399, r)
	if err == nil {
		t.Fatal("want error when parent pid is 0")
	}
}

func TestResolvePane_TmuxError(t *testing.T) {
	r := newScriptedRunner()
	r.expect("12345\n", "ps", "-o", "ppid=", "-p", "12399")
	r.expectErr(fmt.Errorf("tmux unreachable"), "tmux",
		"list-panes", "-aF",
		"#{pane_pid} #{pane_id} #{pane_active} #{session_activity}")
	_, err := ResolvePane(context.Background(), 12399, r)
	if err == nil {
		t.Fatal("want error when tmux fails")
	}
}
