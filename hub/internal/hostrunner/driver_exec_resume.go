// Gemini exec-per-turn-with-resume driver — ADR-013.
//
// Unlike StdioDriver (claude, persistent stdio) and AppServerDriver
// (codex, persistent JSON-RPC daemon), this driver owns *zero*
// subprocesses at rest. Each Input call spawns a fresh
// `gemini -p <text> --output-format stream-json [--resume <UUID>]
// [--yolo]` subprocess, reads JSONL stdout through the gemini frame
// profile, and waits for the process to exit. Multi-turn coherence
// is preserved via `--resume <UUID>`: the first turn's `init` event
// carries `session_id` (PR #14504, Dec 2025), the driver latches it,
// and every subsequent turn threads it back through argv.
//
// Process model:
//   - At rest: no child process running; only the in-memory
//     ResumeSessionID held by the driver.
//   - During Input: exactly one subprocess is alive. Stop() can
//     SIGTERM it, escalating to SIGKILL after KillGrace.
//   - Multiple concurrent Inputs serialize via runMu (gemini's own
//     session storage is not concurrent-write-safe across processes
//     for the same UUID).
//
// What this driver does *not* do:
//   - Per-tool-call approval gates. Gemini-cli has no in-stream
//     approval event (ADR-013 D4); the steward routes risky
//     decisions through `request_approval` MCP tool itself.
//   - Streaming-delta rendering. The driver special-cases
//     {role:assistant, delta:true} frames in streamFrames: it
//     accumulates the per-chunk content into a per-turn buffer and
//     emits cumulative `kind: text` events with `partial: true` and a
//     turn-local message_id. The mobile transcript's
//     _collapseStreamingPartials then folds the chain into a single
//     bubble that grows. The frame profile YAML doesn't have a rule
//     for delta:true assistant frames — accumulation is stateful and
//     the profile evaluator is stateless by design.
package hostrunner

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/termipod/hub/internal/agentfamilies"
)

// GeminiCmd is the narrow os/exec surface the driver actually uses.
// Declared as an interface so tests can wire a fake that emits canned
// JSONL on stdout — production uses *exec.Cmd via execGeminiCmd.
type GeminiCmd interface {
	StdoutPipe() (io.ReadCloser, error)
	Start() error
	Wait() error
	// Kill sends SIGTERM (best-effort) so Stop can unblock a running
	// turn. Implementations MUST be safe to call after Wait has
	// returned (no-op).
	Kill() error
	// Args returns the resolved argv (for assertions in tests). The
	// production wrapper records what was passed to exec.Command.
	Args() []string
}

// ExecResumeDriver implements ADR-013. Field set mirrors AppServerDriver
// where possible so the launch path can construct either by family.
type ExecResumeDriver struct {
	AgentID string
	Handle  string
	Poster  AgentEventPoster

	// Bin is the gemini binary path (resolved via exec.LookPath
	// before construction by launch_m2). Empty rejects every Input.
	Bin string

	// Workdir is the spawned process's cwd. Stewards run inside the
	// per-spawn worktree so .gemini/settings.json (slice 5) and any
	// project-scoped session storage land alongside the agent's
	// other state.
	Workdir string

	// Env is the full environment passed to the subprocess. Hub-side
	// secrets (TERMIPOD_HUB_TOKEN, etc.) ride through here. nil
	// inherits the parent process's environment.
	Env []string

	// Yolo, when true, adds --yolo to the argv. ADR-013 D4: gemini
	// has no in-stream approval gate, so stewards typically run
	// auto-approve and route principal-level decisions through
	// request_approval MCP. Production stewards default this to true.
	Yolo bool

	// FrameProfile drives JSONL → agent_events translation. Required —
	// without a profile we'd emit kind=raw for every line, which is
	// not how the steward expects to see its transcript.
	FrameProfile *agentfamilies.FrameProfile

	// CommandBuilder constructs a fresh command per turn. Production
	// uses execCommandBuilder (exec.CommandContext); tests inject a
	// fake that pipes a canned JSONL corpus into stdout. Required.
	CommandBuilder func(ctx context.Context, name string, args ...string) GeminiCmd

	// CallTimeout caps how long any one turn (one subprocess
	// invocation) can run before the driver kills it. Zero relies on
	// the caller's ctx alone.
	CallTimeout time.Duration

	// KillGrace is the SIGTERM → SIGKILL escalation window. Default 5s
	// when zero, matching driver_stdio's claude behavior.
	KillGrace time.Duration

	Log *slog.Logger

	mu      sync.Mutex
	started bool
	stopped bool

	// runMu serializes turns. Gemini's session storage on disk is not
	// safe for concurrent writes against the same UUID; the steward's
	// turn-at-a-time cadence already serializes at the principal
	// level, but we belt-and-brace it here.
	runMu sync.Mutex

	// activeMu/active tracks the in-flight subprocess so Stop can
	// signal it. Set under runMu; Stop reads under activeMu without
	// holding runMu so it can interrupt.
	activeMu sync.Mutex
	active   GeminiCmd

	// resumeMu/resumeSessionID is the captured init.session_id. The
	// hub persists it to agents.thread_id_json so cross-restart resume
	// works without in-memory state. Reconcile sets it back on the
	// driver when it rehydrates an existing agent.
	resumeMu        sync.RWMutex
	resumeSessionID string

	// ADR-021 W2.4 — next-turn argv override slots. Input("set_mode")
	// and Input("set_model") write here; runTurn consumes the value
	// when building argv and clears the slot. One-shot semantics by
	// design: sticky model/mode is a follow-up wedge, and an absent
	// override falls through to whatever the rendered backend.cmd
	// (and W2.3's respawn-with-spec-mutation path) already carries.
	nextTurnMu    sync.Mutex
	nextTurnMode  string
	nextTurnModel string

	shutdownCh chan struct{}
}

// Start emits lifecycle.started. No subprocess is launched here —
// per ADR-013 D7 the first spawn happens on the first Input call.
func (d *ExecResumeDriver) Start(parent context.Context) error {
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return nil
	}
	d.started = true
	d.shutdownCh = make(chan struct{})
	d.mu.Unlock()

	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.KillGrace == 0 {
		d.KillGrace = 5 * time.Second
	}
	if d.Bin == "" {
		return fmt.Errorf("exec-resume driver: Bin is empty (gemini not on PATH at construction?)")
	}
	if d.FrameProfile == nil {
		return fmt.Errorf("exec-resume driver: FrameProfile is nil")
	}
	if d.CommandBuilder == nil {
		return fmt.Errorf("exec-resume driver: CommandBuilder is nil")
	}

	_ = d.Poster.PostAgentEvent(parent, d.AgentID, "lifecycle", "system",
		map[string]any{"phase": "started", "mode": "M2", "engine": "gemini-exec-resume"})
	return nil
}

// Stop kills any in-flight subprocess and emits lifecycle.stopped.
// Idempotent.
func (d *ExecResumeDriver) Stop() {
	d.mu.Lock()
	if d.stopped || !d.started {
		d.mu.Unlock()
		return
	}
	d.stopped = true
	if d.shutdownCh != nil {
		close(d.shutdownCh)
	}
	d.mu.Unlock()

	d.activeMu.Lock()
	cmd := d.active
	d.activeMu.Unlock()
	if cmd != nil {
		_ = cmd.Kill()
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = d.Poster.PostAgentEvent(shutCtx, d.AgentID, "lifecycle", "system",
		map[string]any{"phase": "stopped", "mode": "M2", "engine": "gemini-exec-resume"})
}

// SessionID returns the captured gemini session_id (empty before the
// first init event). Hub callers persist it on agents.thread_id_json
// as the resume cursor — same role codex's threadID plays.
func (d *ExecResumeDriver) SessionID() string {
	d.resumeMu.RLock()
	defer d.resumeMu.RUnlock()
	return d.resumeSessionID
}

// SetResumeSessionID seeds the driver with a previously-captured
// session UUID. Used on host-runner restart when reconcile rehydrates
// an existing agent: the hub reads agents.thread_id_json, the driver
// gets the UUID set here, and the next Input call argv will include
// --resume <UUID> as if the conversation never stopped.
func (d *ExecResumeDriver) SetResumeSessionID(id string) {
	d.resumeMu.Lock()
	d.resumeSessionID = id
	d.resumeMu.Unlock()
}

// Input dispatches a hub-side input event to a fresh gemini subprocess.
// Implements the Inputter interface. ADR-013 maps:
//
//   - text             → fresh `gemini -p <text> ...` spawn
//   - attention_reply  → same, with the rendered reply text (no
//                        permission_prompt branch — gemini doesn't
//                        support that kind, ADR-013 D4)
//   - cancel           → SIGTERM the active subprocess if any
//
// approval / answer (legacy stream-json shapes) are not used —
// gemini stewards route principal decisions through request_approval
// MCP, not through the driver.
func (d *ExecResumeDriver) Input(ctx context.Context, kind string, payload map[string]any) error {
	switch kind {
	case "text":
		body, _ := payload["body"].(string)
		// ADR-021 W4.5 — gemini's exec-per-turn argv (`gemini -p
		// "<text>"`) has no inline-image affordance, so any images
		// the hub forwarded get stripped here and a kind=system warn
		// event lands on the transcript so the principal sees why
		// their attachment didn't reach the model. Switching to M1
		// (--acp) is the path forward for multimodal turns; the
		// warning text says so.
		//
		// artifact-type-registry W7.2 — PDF/audio/video share the same
		// strip-and-warn path; gemini exec-per-turn argv accepts no
		// multimodal attachments at all.
		dropped := 0
		dropped += len(extractImageInputs(payload))
		dropped += len(extractAttachmentInputs(payload, "pdfs"))
		dropped += len(extractAttachmentInputs(payload, "audios"))
		dropped += len(extractAttachmentInputs(payload, "videos"))
		if dropped > 0 {
			_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "system", "agent",
				map[string]any{
					"reason":  "gemini exec-per-turn has no inline multimodal support — switch to gemini --acp (M1) for multimodal turns",
					"engine":  "gemini-exec",
					"dropped": dropped,
				})
		}
		if body == "" {
			return fmt.Errorf("exec-resume driver: text input missing body")
		}
		return d.runTurn(ctx, body)
	case "attention_reply":
		// gemini has no permission_prompt (ADR-013 D4) — only the
		// turn-based vendor-neutral kinds (approval_request, select,
		// help_request) reach this driver. If the hub somehow
		// dispatches a permission_prompt reply to a gemini agent,
		// reject loudly: there's no JSON-RPC id to reply on (gemini
		// never raised one) and silently turning it into a user-text
		// turn would produce a confusing transcript and might re-run
		// the original turn against the wrong shape.
		if k, _ := payload["kind"].(string); k == "permission_prompt" {
			return fmt.Errorf("exec-resume driver: permission_prompt is unsupported on gemini-cli (ADR-013 D4); use request_approval instead")
		}
		body := formatAttentionReplyText(payload)
		if body == "" {
			return fmt.Errorf("exec-resume driver: attention_reply produced no text")
		}
		return d.runTurn(ctx, body)
	case "cancel":
		d.activeMu.Lock()
		cmd := d.active
		d.activeMu.Unlock()
		if cmd == nil {
			return nil // nothing to cancel
		}
		return cmd.Kill()
	case "approval", "answer":
		return fmt.Errorf("exec-resume driver: %q input shape not used by gemini (use attention_reply / request_approval MCP)", kind)
	case "set_mode":
		// ADR-021 W2.4 — stash the override; next runTurn consumes it.
		// The hub-side router (W2.1) only forwards set_mode to this
		// driver when the family declares per_turn_argv for the agent's
		// driving_mode, so reaching here implies the family agreed.
		modeID, _ := payload["mode_id"].(string)
		if modeID == "" {
			return fmt.Errorf("exec-resume driver: set_mode missing mode_id")
		}
		d.nextTurnMu.Lock()
		d.nextTurnMode = modeID
		d.nextTurnMu.Unlock()
		return nil
	case "set_model":
		// ADR-021 W2.4 — same shape as set_mode for the model picker.
		modelID, _ := payload["model_id"].(string)
		if modelID == "" {
			return fmt.Errorf("exec-resume driver: set_model missing model_id")
		}
		d.nextTurnMu.Lock()
		d.nextTurnModel = modelID
		d.nextTurnMu.Unlock()
		return nil
	default:
		return fmt.Errorf("exec-resume driver: unsupported input kind %q", kind)
	}
}

// runTurn spawns one subprocess, streams its stdout through the frame
// profile, and waits for it to exit. Returns once the process has
// exited (or been killed). Multiple Input calls serialize via runMu.
func (d *ExecResumeDriver) runTurn(parent context.Context, prompt string) error {
	d.runMu.Lock()
	defer d.runMu.Unlock()

	// Build argv. --resume goes BEFORE -p so flag parsing sees them
	// in the order gemini expects (positional -p value last).
	//
	// --skip-trust is required for gemini-cli@0.41+ to run headlessly
	// from any folder not on the user's interactive trust list. The
	// hub-work workdir is intentionally where the agent operates, so
	// trust is intent here — adding the flag belt-and-suspenders with
	// the GEMINI_CLI_TRUST_WORKSPACE env so a stripped-env exec or a
	// future env-name change can't silently break the turn.
	args := []string{"--output-format", "stream-json", "--skip-trust"}
	if uuid := d.SessionID(); uuid != "" {
		args = append(args, "--resume", uuid)
	}

	// ADR-021 W2.4 — consume one-shot mode/model overrides set via
	// Input("set_mode")/Input("set_model"). Spliced before --yolo / -p
	// so flag parsing sees them in the order gemini-cli expects.
	// Cleared atomically with the read so a Stop+respawn racing with
	// runTurn doesn't apply the same override twice.
	d.nextTurnMu.Lock()
	mode := d.nextTurnMode
	model := d.nextTurnModel
	d.nextTurnMode = ""
	d.nextTurnModel = ""
	d.nextTurnMu.Unlock()
	if model != "" {
		args = append(args, "--model", model)
	}
	if mode != "" {
		// gemini-cli@0.41.x exposes the picker concept as
		// `--approval-mode <default|auto-edit|yolo>`; the older `--yolo`
		// is now a shortcut for --approval-mode=yolo. We pass the id
		// verbatim so a future ACP-side mode addition (`plan`, etc.)
		// flows through without a driver edit. Validation belongs in
		// the picker UI.
		args = append(args, "--approval-mode", mode)
	} else if d.Yolo {
		// Skip the legacy --yolo flag when the picker explicitly chose a
		// different mode for this turn — the new --approval-mode wins.
		args = append(args, "--yolo")
	}
	args = append(args, "-p", prompt)

	turnCtx, cancel := context.WithCancel(parent)
	defer cancel()
	if d.CallTimeout > 0 {
		turnCtx, cancel = context.WithTimeout(parent, d.CallTimeout)
		defer cancel()
	}

	cmd := d.CommandBuilder(turnCtx, d.Bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("exec-resume driver: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("exec-resume driver: start %s: %w", d.Bin, err)
	}

	d.activeMu.Lock()
	d.active = cmd
	d.activeMu.Unlock()
	defer func() {
		d.activeMu.Lock()
		d.active = nil
		d.activeMu.Unlock()
	}()

	// Watchdog goroutine — escalate to SIGKILL after KillGrace if the
	// turn context cancels (Stop called, or CallTimeout elapsed) but
	// the subprocess is still alive past the SIGTERM grace window.
	// cmd.Kill is the SIGTERM; *exec.Cmd will SIGKILL itself when its
	// context cancels via CommandContext, but we belt-and-brace by
	// firing Kill explicitly so fakes that don't honor context still
	// die.
	doneCh := make(chan struct{})
	go func() {
		select {
		case <-turnCtx.Done():
			_ = cmd.Kill()
			// CommandContext kills with SIGKILL on its own timeline; we
			// don't need to escalate manually for the production path.
		case <-d.shutdownCh:
			_ = cmd.Kill()
		case <-doneCh:
		}
	}()

	d.streamFrames(parent, stdout)
	close(doneCh)

	waitErr := cmd.Wait()
	// A clean cancel (Stop or ctx) shouldn't surface as an error to
	// the caller — the lifecycle.stopped event already records it.
	if waitErr != nil && parent.Err() == nil && turnCtx.Err() == nil {
		// Best-effort note in the transcript so debugging non-zero
		// exits is possible without re-running.
		_ = d.Poster.PostAgentEvent(parent, d.AgentID, "system", "agent",
			map[string]any{
				"kind":   "gemini_exit_nonzero",
				"err":    waitErr.Error(),
				"args":   cmd.Args(),
			})
		return fmt.Errorf("exec-resume driver: gemini exited: %w", waitErr)
	}
	return nil
}

// streamFrames reads JSONL from the subprocess stdout, runs each frame
// through the gemini frame profile, and posts the resulting events.
// Latches the session_id on the first init event so subsequent turns
// thread `--resume <UUID>` into argv. Returns on EOF (process exit) or
// scanner error.
func (d *ExecResumeDriver) streamFrames(ctx context.Context, stdout io.Reader) {
	captureFile := openCaptureFile(d.AgentID, d.Log)
	if captureFile != nil {
		defer captureFile.Close()
	}
	// Per-turn assistant accumulator. gemini-cli@0.41 emits assistant
	// text exclusively as delta=true chunks where each chunk's content
	// is incremental (not cumulative). To get one transcript bubble
	// that grows instead of N separate bubbles, we accumulate chunks
	// here and emit cumulative `text` events tagged
	// {partial: true, message_id: <turn id>} so the mobile renderer's
	// _collapseStreamingPartials chain logic folds them. msgID is
	// generated lazily on the first delta so non-text turns (pure
	// tool_use) don't allocate.
	var assistantBuf []byte
	var assistantMsgID string
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), streamJSONBufferSize)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if captureFile != nil {
			_, _ = captureFile.Write(append(line, '\n'))
		}
		var frame map[string]any
		if err := json.Unmarshal(line, &frame); err != nil {
			_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "raw", "agent",
				map[string]any{"text": string(line)})
			continue
		}
		frameType, _ := frame["type"].(string)
		// Latch session_id on init. The frame profile also publishes
		// a session.init event with this same id; capturing here is
		// what threads --resume into the *next* turn's argv.
		if frameType == "init" {
			if sid, ok := frame["session_id"].(string); ok && sid != "" {
				d.resumeMu.Lock()
				if d.resumeSessionID == "" || d.resumeSessionID != sid {
					d.resumeSessionID = sid
				}
				d.resumeMu.Unlock()
			}
		}
		// Assistant delta accumulator: append content + emit cumulative
		// partial text. Bypass the profile path for these frames — the
		// YAML evaluator is stateless and can't accumulate. Errors
		// extracting fields fall through silently because gemini's
		// delta frame shape is well-known.
		if frameType == "message" {
			role, _ := frame["role"].(string)
			delta, _ := frame["delta"].(bool)
			if role == "assistant" && delta {
				chunk, _ := frame["content"].(string)
				if chunk != "" {
					if assistantMsgID == "" {
						assistantMsgID = newMessageID()
					}
					assistantBuf = append(assistantBuf, chunk...)
					payload := map[string]any{
						"text":       string(assistantBuf),
						"message_id": assistantMsgID,
						"partial":    true,
					}
					if ts, ok := frame["timestamp"].(string); ok {
						payload["timestamp"] = ts
					}
					_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "text", "agent", payload)
				}
				continue
			}
		}
		evts := ApplyProfile(frame, d.FrameProfile)
		for _, e := range evts {
			if e.Kind == "turn.result" {
				normalizeGeminiByModel(e.Payload)
			}
			_ = d.Poster.PostAgentEvent(ctx, d.AgentID, e.Kind, e.Producer, e.Payload)
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		d.Log.Debug("exec-resume read error", "agent", d.AgentID, "err", err)
	}
}

// normalizeGeminiByModel rewrites gemini's per-model stats keys
// (input_tokens / output_tokens / cached) into the canonical hub names
// (input / output / cache_read) the mobile telemetry aggregator
// (_ModelTokens.add in lib/widgets/agent_feed.dart) reads. The original
// gemini-named fields are preserved alongside so the raw `stats` block
// under turn.result.stats stays lossless for future telemetry rules.
//
// Why driver, not frame profile: the YAML profile evaluator is
// stateless and has no map-iter construct, so it can lift `by_model:
// $.stats.models` verbatim but can't walk the map to rename child keys.
// Doing the rewrite here keeps the gemini-specific knowledge inside the
// gemini driver and out of the mobile renderer.
func normalizeGeminiByModel(payload map[string]any) {
	bm, ok := payload["by_model"].(map[string]any)
	if !ok {
		return
	}
	for _, v := range bm {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if _, has := m["input"]; !has {
			if it, ok := m["input_tokens"]; ok {
				m["input"] = it
			}
		}
		if _, has := m["output"]; !has {
			if ot, ok := m["output_tokens"]; ok {
				m["output"] = ot
			}
		}
		if _, has := m["cache_read"]; !has {
			if c, ok := m["cached"]; ok {
				m["cache_read"] = c
			}
		}
	}
}

// newMessageID returns a 16-hex-char id for tagging streaming chunks
// from a single turn. Random per call so chunks from turn N don't
// collide with chunks from turn N+1 (which would coalesce into the
// same bubble). crypto/rand because the id is user-visible (anchors
// the mobile collapse chain) — collisions across turns would silently
// merge transcripts.
func newMessageID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback: use a timestamp-based id. Non-crypto-random is
		// fine here — the id only needs to be unique within one
		// driver process's lifetime, not unguessable.
		return fmt.Sprintf("gemini-%d", time.Now().UnixNano())
	}
	return "gemini-" + hex.EncodeToString(b[:])
}

// execGeminiCmd wraps *exec.Cmd to satisfy the GeminiCmd interface.
// Production launch_m2 wires CommandBuilder to ExecCommandBuilder
// below; tests substitute their own fake.
type execGeminiCmd struct {
	cmd  *exec.Cmd
	args []string
}

func (c *execGeminiCmd) StdoutPipe() (io.ReadCloser, error) { return c.cmd.StdoutPipe() }
func (c *execGeminiCmd) Start() error                       { return c.cmd.Start() }
func (c *execGeminiCmd) Wait() error                        { return c.cmd.Wait() }
func (c *execGeminiCmd) Args() []string                     { return c.args }
func (c *execGeminiCmd) Kill() error {
	if c.cmd.Process == nil {
		return nil
	}
	return c.cmd.Process.Kill()
}

// ExecCommandBuilder returns the production CommandBuilder factory.
// workdir and env are baked into the closure at construction so the
// driver doesn't need to know how its environment is assembled.
func ExecCommandBuilder(workdir string, env []string) func(ctx context.Context, name string, args ...string) GeminiCmd {
	return func(ctx context.Context, name string, args ...string) GeminiCmd {
		c := exec.CommandContext(ctx, name, args...)
		c.Dir = workdir
		c.Env = env
		return &execGeminiCmd{cmd: c, args: append([]string{name}, args...)}
	}
}
