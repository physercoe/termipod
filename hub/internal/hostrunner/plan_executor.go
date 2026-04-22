// Host-runner plan-step executor (blueprint §6.2, P1.2).
//
// A plan step is the finest-grained unit of work inside a deterministic
// plan phase. The executor dispatches by `kind` and returns a uniform
// StepResult the hub can persist against the `plan_steps` row. Four
// kinds live here:
//
//   - shell          — run a shell command on the host
//   - llm_call       — one-shot LLM inference (e.g. `claude -p …`)
//   - mcp_call       — invoke one named MCP tool and capture the result
//   - human_decision — block until the hub reports a user choice
//
// The fifth step kind (`agent_spawn`) does not flow through this
// executor; it delegates to P1.1's driver machinery, which owns the
// full agent lifecycle rather than a single call.
//
// No policy gate is enforced here yet — that's layered on by the caller
// before Execute is invoked, since the policy decision may itself need
// hub state (allow-lists, quotas) that host-runner doesn't cache.
package hostrunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"time"
)

// StepKind is the discriminator on plan_steps.kind. Lower-case values
// match the blueprint table §6.2 and the hub's JSON.
type StepKind string

const (
	StepShell         StepKind = "shell"
	StepLLMCall       StepKind = "llm_call"
	StepMCPCall       StepKind = "mcp_call"
	StepHumanDecision StepKind = "human_decision"
	StepAgentSpawn    StepKind = "agent_spawn"
)

// StepSpec is the executor's input — a normalised view of plan_steps
// .spec_json. Fields are optional and kind-specific; handlers pick the
// subset they need.
type StepSpec struct {
	Kind StepKind

	// shell / llm_call
	Command string
	Args    []string
	Env     []string
	Stdin   string
	Timeout time.Duration

	// mcp_call
	Tool   string
	Params map[string]any

	// human_decision
	Prompt  string
	Choices []string
}

// StepResult is the uniform output shape. ExitCode/Stdout/Stderr are
// populated for shell-family kinds; Output carries structured results
// from mcp_call / human_decision. Err is non-empty iff the step failed
// to complete (distinct from a non-zero exit, which is a successful
// execution with a bad exit code).
type StepResult struct {
	Kind     StepKind
	ExitCode int
	Stdout   string
	Stderr   string
	Output   map[string]any
	Err      string
	Duration time.Duration
}

// MCPCaller is the narrow dependency the mcp_call handler needs.
// Injected so tests don't have to stand up a full gateway.
type MCPCaller interface {
	CallTool(ctx context.Context, tool string, params map[string]any) (map[string]any, error)
}

// HumanDecisionWaiter is the narrow dependency the human_decision
// handler needs. Wiring to the hub (polling a decision endpoint or
// subscribing to SSE) is the caller's business; the executor just
// blocks on the chosen implementation.
type HumanDecisionWaiter interface {
	WaitForDecision(ctx context.Context, prompt string, choices []string) (string, error)
}

// Executor holds the wiring for kinds that need external collaborators.
// Nil collaborators are legal but cause the corresponding kind to
// return an "unconfigured" error — fail closed rather than silently
// skip.
type Executor struct {
	MCP   MCPCaller
	Human HumanDecisionWaiter
	Log   *slog.Logger

	// For tests and alternate exec paths (e.g. routing shell through a
	// sandbox). Nil → use os/exec directly.
	Exec func(ctx context.Context, command string, args, env []string, stdin string) (stdout, stderr string, exitCode int, err error)
}

// Execute runs one step and returns its result. The context deadline
// bounds the whole step; per-kind timeouts stack on top (whichever
// fires first wins).
func (e *Executor) Execute(ctx context.Context, spec StepSpec) StepResult {
	if e.Log == nil {
		e.Log = slog.Default()
	}
	start := time.Now()
	var res StepResult
	res.Kind = spec.Kind

	switch spec.Kind {
	case StepShell, StepLLMCall:
		res = e.runExec(ctx, spec)
	case StepMCPCall:
		res = e.runMCP(ctx, spec)
	case StepHumanDecision:
		res = e.runHumanDecision(ctx, spec)
	case StepAgentSpawn:
		res.Err = "agent_spawn steps are handled by the driver, not the plan executor"
	default:
		res.Err = fmt.Sprintf("unknown step kind %q", spec.Kind)
	}
	res.Duration = time.Since(start)
	return res
}

func (e *Executor) runExec(ctx context.Context, spec StepSpec) StepResult {
	res := StepResult{Kind: spec.Kind}
	if spec.Command == "" {
		res.Err = "command is empty"
		return res
	}
	sub := ctx
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		sub, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}
	exec := e.Exec
	if exec == nil {
		exec = defaultExec
	}
	stdout, stderr, code, err := exec(sub, spec.Command, spec.Args, spec.Env, spec.Stdin)
	res.Stdout = stdout
	res.Stderr = stderr
	res.ExitCode = code
	if err != nil {
		// Context expiry surfaces as a completion error, not an exit code.
		if errors.Is(err, context.DeadlineExceeded) {
			res.Err = "timeout: " + err.Error()
		} else if errors.Is(err, context.Canceled) {
			res.Err = "cancelled: " + err.Error()
		} else {
			res.Err = err.Error()
		}
	}
	return res
}

func (e *Executor) runMCP(ctx context.Context, spec StepSpec) StepResult {
	res := StepResult{Kind: spec.Kind}
	if e.MCP == nil {
		res.Err = "mcp_call: executor has no MCPCaller configured"
		return res
	}
	if spec.Tool == "" {
		res.Err = "mcp_call: tool is empty"
		return res
	}
	out, err := e.MCP.CallTool(ctx, spec.Tool, spec.Params)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.Output = out
	return res
}

func (e *Executor) runHumanDecision(ctx context.Context, spec StepSpec) StepResult {
	res := StepResult{Kind: spec.Kind}
	if e.Human == nil {
		res.Err = "human_decision: executor has no HumanDecisionWaiter configured"
		return res
	}
	choice, err := e.Human.WaitForDecision(ctx, spec.Prompt, spec.Choices)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.Output = map[string]any{"choice": choice}
	return res
}

// defaultExec runs command via os/exec. Stdin is piped in if non-empty;
// stdout/stderr are captured to separate buffers. Exit code is 0 on
// success, the process's exit code on a clean non-zero exit, or -1
// when the process couldn't be started / was killed by a signal.
func defaultExec(ctx context.Context, command string, args, env []string, stdin string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	if len(env) > 0 {
		cmd.Env = env
	}
	if stdin != "" {
		cmd.Stdin = bytes.NewReader([]byte(stdin))
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && errors.As(err, &exitErr) {
		return outBuf.String(), errBuf.String(), exitErr.ExitCode(), nil
	}
	if err != nil {
		return outBuf.String(), errBuf.String(), -1, err
	}
	return outBuf.String(), errBuf.String(), 0, nil
}
