package hostrunner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeExec is a minimal Exec replacement so tests don't depend on the
// presence of /bin/sh or specific binaries in CI.
func fakeExec(stdout, stderr string, code int, err error) func(context.Context, string, []string, []string, string) (string, string, int, error) {
	return func(context.Context, string, []string, []string, string) (string, string, int, error) {
		return stdout, stderr, code, err
	}
}

func TestExecutor_Shell_Success(t *testing.T) {
	e := &Executor{Exec: fakeExec("hello\n", "", 0, nil)}
	r := e.Execute(context.Background(), StepSpec{
		Kind:    StepShell,
		Command: "echo",
		Args:    []string{"hello"},
	})
	if r.Err != "" {
		t.Fatalf("unexpected Err=%q", r.Err)
	}
	if r.ExitCode != 0 || r.Stdout != "hello\n" {
		t.Fatalf("got %+v; want exit=0 stdout=hello", r)
	}
	if r.Kind != StepShell {
		t.Fatalf("Kind = %q; want shell", r.Kind)
	}
}

func TestExecutor_Shell_NonZeroExit(t *testing.T) {
	// A non-zero exit is a *successful completion* with a bad exit code —
	// the executor must not treat it as an error.
	e := &Executor{Exec: fakeExec("", "boom\n", 2, nil)}
	r := e.Execute(context.Background(), StepSpec{
		Kind:    StepShell,
		Command: "false",
	})
	if r.Err != "" {
		t.Fatalf("non-zero exit should not produce Err; got %q", r.Err)
	}
	if r.ExitCode != 2 || r.Stderr != "boom\n" {
		t.Fatalf("got %+v; want exit=2 stderr=boom", r)
	}
}

func TestExecutor_Shell_EmptyCommand(t *testing.T) {
	e := &Executor{}
	r := e.Execute(context.Background(), StepSpec{Kind: StepShell})
	if !strings.Contains(r.Err, "empty") {
		t.Fatalf("want 'empty' error; got %q", r.Err)
	}
}

func TestExecutor_Shell_Timeout(t *testing.T) {
	// Simulate a slow command by delaying inside Exec until ctx expires.
	slow := func(ctx context.Context, _ string, _, _ []string, _ string) (string, string, int, error) {
		<-ctx.Done()
		return "", "", -1, ctx.Err()
	}
	e := &Executor{Exec: slow}
	r := e.Execute(context.Background(), StepSpec{
		Kind:    StepShell,
		Command: "sleep",
		Args:    []string{"10"},
		Timeout: 20 * time.Millisecond,
	})
	if !strings.Contains(r.Err, "timeout") {
		t.Fatalf("want timeout error; got %q", r.Err)
	}
}

func TestExecutor_LLMCall_RoutesThroughExec(t *testing.T) {
	// llm_call should share the exec machinery — a canned stream-json
	// response on stdout counts as a successful result.
	stream := `{"type":"result","result":"ok"}` + "\n"
	e := &Executor{Exec: fakeExec(stream, "", 0, nil)}
	r := e.Execute(context.Background(), StepSpec{
		Kind:    StepLLMCall,
		Command: "claude",
		Args:    []string{"-p", "hello", "--output-format", "stream-json"},
	})
	if r.Err != "" || r.ExitCode != 0 {
		t.Fatalf("want ok; got %+v", r)
	}
	if !strings.Contains(r.Stdout, `"type":"result"`) {
		t.Fatalf("stdout = %q; want stream-json", r.Stdout)
	}
}

type fakeMCP struct {
	wantTool   string
	wantParams map[string]any
	resp       map[string]any
	err        error
}

func (f *fakeMCP) CallTool(_ context.Context, tool string, params map[string]any) (map[string]any, error) {
	if tool != f.wantTool {
		return nil, errors.New("unexpected tool: " + tool)
	}
	return f.resp, f.err
}

func TestExecutor_MCPCall_Success(t *testing.T) {
	mcp := &fakeMCP{
		wantTool: "projects.list",
		resp:     map[string]any{"items": []any{"p1", "p2"}},
	}
	e := &Executor{MCP: mcp}
	r := e.Execute(context.Background(), StepSpec{
		Kind:   StepMCPCall,
		Tool:   "projects.list",
		Params: map[string]any{"limit": 10},
	})
	if r.Err != "" {
		t.Fatalf("unexpected Err=%q", r.Err)
	}
	items, _ := r.Output["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v; want 2 entries", items)
	}
}

func TestExecutor_MCPCall_NoCaller(t *testing.T) {
	e := &Executor{}
	r := e.Execute(context.Background(), StepSpec{Kind: StepMCPCall, Tool: "x"})
	if !strings.Contains(r.Err, "no MCPCaller") {
		t.Fatalf("want unconfigured error; got %q", r.Err)
	}
}

func TestExecutor_MCPCall_Propagates(t *testing.T) {
	e := &Executor{MCP: &fakeMCP{wantTool: "t", err: errors.New("forbidden")}}
	r := e.Execute(context.Background(), StepSpec{Kind: StepMCPCall, Tool: "t"})
	if !strings.Contains(r.Err, "forbidden") {
		t.Fatalf("want forbidden error; got %q", r.Err)
	}
}

type fakeWaiter struct {
	choice string
	err    error
}

func (f *fakeWaiter) WaitForDecision(context.Context, string, []string) (string, error) {
	return f.choice, f.err
}

func TestExecutor_HumanDecision_Success(t *testing.T) {
	e := &Executor{Human: &fakeWaiter{choice: "approve"}}
	r := e.Execute(context.Background(), StepSpec{
		Kind:    StepHumanDecision,
		Prompt:  "merge?",
		Choices: []string{"approve", "reject"},
	})
	if r.Err != "" {
		t.Fatalf("unexpected Err=%q", r.Err)
	}
	if r.Output["choice"] != "approve" {
		t.Fatalf("choice = %v; want approve", r.Output["choice"])
	}
}

func TestExecutor_HumanDecision_NoWaiter(t *testing.T) {
	e := &Executor{}
	r := e.Execute(context.Background(), StepSpec{Kind: StepHumanDecision})
	if !strings.Contains(r.Err, "no HumanDecisionWaiter") {
		t.Fatalf("want unconfigured error; got %q", r.Err)
	}
}

func TestExecutor_AgentSpawn_Rejected(t *testing.T) {
	// agent_spawn lives in the driver layer, not the plan executor.
	// The executor must surface this clearly rather than silently no-op.
	e := &Executor{}
	r := e.Execute(context.Background(), StepSpec{Kind: StepAgentSpawn})
	if !strings.Contains(r.Err, "driver") {
		t.Fatalf("want 'driver' in error; got %q", r.Err)
	}
}

func TestExecutor_UnknownKind(t *testing.T) {
	e := &Executor{}
	r := e.Execute(context.Background(), StepSpec{Kind: StepKind("bogus")})
	if !strings.Contains(r.Err, "unknown step kind") {
		t.Fatalf("want unknown-kind error; got %q", r.Err)
	}
}

func TestExecutor_RecordsDuration(t *testing.T) {
	slow := func(context.Context, string, []string, []string, string) (string, string, int, error) {
		time.Sleep(5 * time.Millisecond)
		return "", "", 0, nil
	}
	e := &Executor{Exec: slow}
	r := e.Execute(context.Background(), StepSpec{Kind: StepShell, Command: "x"})
	if r.Duration < 5*time.Millisecond {
		t.Fatalf("Duration = %v; want >= 5ms", r.Duration)
	}
}
