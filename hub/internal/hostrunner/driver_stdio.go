// M2 (structured stdio) driver — blueprint §5.3.1.
//
// Reads a newline-delimited JSON stream from the agent's stdout (Claude
// Code's `--output-format stream-json` is the canonical producer) and
// translates each frame into one or more `agent_events`. No control-plane
// back-channel is wired here: user→agent input handling lands with P1.8's
// input route + SSE subscription; this pass is agent→hub only.
//
// Producer attribution:
//   - lifecycle events (started/stopped) are producer=system.
//   - frames originating in the model's output (text / tool calls / tool
//     results / results / errors) are producer=agent.
package hostrunner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/termipod/hub/internal/agentfamilies"
)

// StdioDriver implements M2. It owns a reader against the child's stdout;
// production code also holds the *exec.Cmd so Stop can signal it, but the
// driver is transport-agnostic — tests wire an io.Pipe directly.
type StdioDriver struct {
	AgentID string
	Poster  AgentEventPoster
	Stdout  io.Reader // child stdout (line-delimited stream-json)
	// Stdin, if non-nil, is the child's stdin. Input (blueprint P1.8)
	// writes a stream-json user frame here per event. Drivers wired
	// without Stdin simply reject Input calls so the router logs the
	// gap instead of silently dropping user messages.
	Stdin io.Writer
	// Closer, if non-nil, is invoked by Stop to unblock a blocked reader
	// (typically the child's stdin + a kill on the *exec.Cmd).
	Closer func()
	Log    *slog.Logger

	// FrameTranslator selects which translator runs for each frame:
	// "" / "legacy" (default) → hardcoded legacyTranslate;
	// "profile" → data-driven ApplyProfile, legacy not invoked;
	// "both" → ApplyProfile authoritative, legacy in shadow with
	// divergence logging. Sourced from the family entry in
	// agent_families.yaml at driver construction. ADR-010 Phase 1.6.
	FrameTranslator string
	// FrameProfile is the per-engine translation rules used when
	// FrameTranslator is "profile" or "both". Nil means "no profile
	// authored" — translate() falls through to legacy with a warning
	// rather than silently dropping events.
	FrameProfile *agentfamilies.FrameProfile

	mu      sync.Mutex
	started bool
	stopped bool
	wg      sync.WaitGroup
	inputMu sync.Mutex // serializes concurrent Input calls
}

// Start emits lifecycle.started and launches the reader goroutine. Returns
// immediately; frame translation happens in the background.
func (d *StdioDriver) Start(parent context.Context) error {
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return nil
	}
	d.started = true
	d.mu.Unlock()

	if d.Log == nil {
		d.Log = slog.Default()
	}

	_ = d.Poster.PostAgentEvent(parent, d.AgentID, "lifecycle", "system",
		map[string]any{"phase": "started", "mode": "M2"})

	d.wg.Add(1)
	go d.readLoop(parent)
	return nil
}

// Stop signals the reader to unwind (via Closer, if provided), waits for
// it to drain, then emits lifecycle.stopped. Idempotent.
func (d *StdioDriver) Stop() {
	d.mu.Lock()
	if d.stopped || !d.started {
		d.mu.Unlock()
		return
	}
	d.stopped = true
	closer := d.Closer
	d.mu.Unlock()

	if closer != nil {
		closer()
	}
	d.wg.Wait()

	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = d.Poster.PostAgentEvent(shutCtx, d.AgentID, "lifecycle", "system",
		map[string]any{"phase": "stopped", "mode": "M2"})
}

// streamJSONBufferSize is the max frame size the scanner will accept. Tool
// inputs and results can be large; 1 MiB keeps us comfortably above typical
// Claude Code output without unbounded growth on a malformed stream.
const streamJSONBufferSize = 1 << 20

func (d *StdioDriver) readLoop(ctx context.Context) {
	defer d.wg.Done()
	// Optional frame capture: when HUB_STREAM_DEBUG_DIR is set, every
	// raw stream-json line gets appended to <dir>/<agent_id>.jsonl
	// before translation. Used by ADR-010 Phase 1.5 to grow the
	// frame-profile parity corpus from real claude-code traffic — the
	// operator runs an agent, copies the resulting JSONL into the
	// repo's testdata directory, and the parity test starts diffing
	// against it. Best-effort: capture failures don't interrupt the
	// real translation path.
	captureFile := openCaptureFile(d.AgentID, d.Log)
	if captureFile != nil {
		defer captureFile.Close()
	}
	sc := bufio.NewScanner(d.Stdout)
	sc.Buffer(make([]byte, 64*1024), streamJSONBufferSize)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if captureFile != nil {
			if _, err := captureFile.Write(append(line, '\n')); err != nil {
				d.Log.Debug("stream capture write failed",
					"agent", d.AgentID, "err", err)
			}
		}
		var frame map[string]any
		if err := json.Unmarshal(line, &frame); err != nil {
			// Non-JSON line: pass through as raw so the app sees *something*
			// instead of silently dropping bytes.
			_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "raw", "agent",
				map[string]any{"text": string(line)})
			continue
		}
		d.translate(ctx, frame)
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		d.Log.Debug("stdio read error", "agent", d.AgentID, "err", err)
	}
}

// openCaptureFile returns an append-mode writer at
// <HUB_STREAM_DEBUG_DIR>/<agent_id>.jsonl when the env var is set,
// or nil when capture is disabled. Logs and returns nil on error so
// translation continues unaffected.
func openCaptureFile(agentID string, log *slog.Logger) *os.File {
	dir := os.Getenv("HUB_STREAM_DEBUG_DIR")
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Debug("stream capture dir create failed", "dir", dir, "err", err)
		return nil
	}
	path := filepath.Join(dir, agentID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Debug("stream capture file open failed", "path", path, "err", err)
		return nil
	}
	log.Info("stream capture active", "agent", agentID, "path", path)
	return f
}

// translate dispatches one stream-json frame through whichever
// translator the driver is configured with (ADR-010, Phase 1.6).
// Three modes:
//
//   - ""/"legacy" — the hardcoded translator (legacyTranslate). v1
//     default; what the host-runner has shipped since M2 landed.
//   - "profile"  — only the data-driven ApplyProfile fires. Used
//     once Phase 2's parity canary holds at zero divergences.
//   - "both"     — profile is authoritative (its events write to
//     the DB); legacy runs in shadow and any divergence is logged.
//     Operator-flippable per family during the canary window.
//
// FrameProfile must be non-nil for "profile" / "both" mode; if it's
// nil we fall through to legacy with a one-time warning so a
// misconfigured family doesn't silently lose events.
func (d *StdioDriver) translate(ctx context.Context, frame map[string]any) {
	mode := d.FrameTranslator
	if mode == "" {
		mode = "legacy"
	}
	switch mode {
	case "profile", "both":
		if d.FrameProfile == nil {
			d.Log.Warn("frame_translator set but no profile loaded; falling back to legacy",
				"agent", d.AgentID, "mode", mode)
			d.legacyTranslate(ctx, frame)
			return
		}
		profileEvents := ApplyProfile(frame, d.FrameProfile)
		if mode == "both" {
			// Shadow-run the legacy translator into a capture buffer
			// so we can diff. No DB writes from the shadow path —
			// only profile events become real agent_event rows.
			cap := &capturingPoster{}
			shadow := &StdioDriver{
				AgentID: d.AgentID,
				Poster:  cap,
				Log:     d.Log,
				// FrameTranslator/FrameProfile intentionally omitted —
				// shadow always runs the legacy path.
			}
			shadow.legacyTranslate(ctx, frame)
			if diff := DiffEvents(cap.events, profileEvents, ParityIgnoreFields); diff != "" {
				d.Log.Warn("frame_translator divergence",
					"agent", d.AgentID,
					"diff", diff,
				)
			}
		}
		for _, e := range profileEvents {
			_ = d.Poster.PostAgentEvent(ctx, d.AgentID, e.Kind, e.Producer, e.Payload)
		}
	default:
		d.legacyTranslate(ctx, frame)
	}
}

// legacyTranslate maps a single stream-json frame to zero or more
// agent_events using the original hardcoded field paths. Retained as
// the v1 default + as the shadow translator for "both" mode's
// divergence logging. Phase 2 (post-canary) deletes this function.
//
// The normalization contract here is the load-bearing abstraction
// between drivers and the mobile UI: claude's stream-json is one
// dialect, codex/gemini-cli speak others, but the *typed* event kinds we
// emit are stable. Mobile renders by event kind — adding a new agent
// kind means writing a new driver, not a new screen.
//
// Canonical kinds (see docs/wedges/steward-ux-fixes.md "Driver schema"):
//
//	session.init  {session_id, model, cwd, permission_mode, tools,
//	               mcp_servers, slash_commands, agents, skills,
//	               plugins, version, output_style, fast_mode_state}
//	text          {text}
//	tool_call     {id, name, input}
//	tool_result   {tool_use_id, content, is_error}
//	usage         {message_id, model, input_tokens, output_tokens,
//	               cache_read, cache_create, service_tier}
//	rate_limit    {window, status, resets_at, overage_disabled,
//	               is_using_overage, overage_status, reason}
//	turn.result   {cost_usd, duration_ms, num_turns, terminal_reason,
//	               permission_denials, by_model{...}, fast_mode_state}
//	completion    *(deprecated alias, kept for one release)*
//	error         passthrough
//	system        passthrough non-init system frames
//	raw           anything we don't recognize
//
// Unknown `type` values are forwarded as producer=agent kind="raw" so
// the hub keeps a complete transcript even when the schema drifts —
// other drivers can lift fields they care about into typed kinds in
// their own translate()s.
func (d *StdioDriver) legacyTranslate(ctx context.Context, frame map[string]any) {
	typ, _ := frame["type"].(string)
	switch typ {
	case "system":
		// system/init: lift the rich blob claude emits into a stable
		// schema. Fields not present in the source frame come through
		// as nil — the mobile renderer dispatches on presence, so a
		// codex driver that doesn't surface mcp_servers just leaves
		// the field absent and that section absents itself.
		sub, _ := frame["subtype"].(string)
		// Recent claude-code SDK versions wrap rate_limit_event under
		// type=system,subtype=rate_limit_event instead of emitting it
		// as a bare top-level type. Fall through to the same handler
		// so the telemetry strip keeps lighting up regardless of which
		// shape the spawned agent uses.
		if sub == "rate_limit_event" {
			d.translateRateLimit(ctx, frame)
			return
		}
		if sub == "init" {
			payload := map[string]any{
				"session_id":      frame["session_id"],
				"model":           frame["model"],
				"cwd":             frame["cwd"],
				"permission_mode": firstNonNil(frame["permissionMode"], frame["permission_mode"]),
				"tools":           frame["tools"],
				"mcp_servers":     firstNonNil(frame["mcp_servers"], frame["mcpServers"]),
				"slash_commands":  firstNonNil(frame["slash_commands"], frame["slashCommands"]),
				"agents":          frame["agents"],
				"skills":          frame["skills"],
				"plugins":         frame["plugins"],
				"version":         firstNonNil(frame["claude_code_version"], frame["version"]),
				"output_style":    firstNonNil(frame["output_style"], frame["outputStyle"]),
				"fast_mode_state": firstNonNil(frame["fast_mode_state"], frame["fastModeState"]),
			}
			_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "session.init", "agent", payload)
			return
		}
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "system", "agent", frame)

	case "assistant":
		// Walk content blocks, then surface the per-message usage as
		// its own typed event (linked back via message_id) so the
		// mobile telemetry strip doesn't have to peek inside text
		// payloads to count tokens.
		msg, _ := frame["message"].(map[string]any)
		messageID, _ := msg["id"].(string)
		blocks, _ := msg["content"].([]any)
		for _, b := range blocks {
			block, _ := b.(map[string]any)
			bt, _ := block["type"].(string)
			switch bt {
			case "text":
				text, _ := block["text"].(string)
				if text == "" {
					continue
				}
				out := map[string]any{"text": text}
				if messageID != "" {
					out["message_id"] = messageID
				}
				_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "text", "agent", out)
			case "tool_use":
				_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "tool_call", "agent",
					map[string]any{
						"id":    block["id"],
						"name":  block["name"],
						"input": block["input"],
					})
			default:
				_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "raw", "agent", block)
			}
		}
		if usage, ok := msg["usage"].(map[string]any); ok {
			out := map[string]any{
				"input_tokens":  usage["input_tokens"],
				"output_tokens": usage["output_tokens"],
				"cache_read":    firstNonNil(usage["cache_read_input_tokens"], usage["cache_read"]),
				"cache_create":  firstNonNil(usage["cache_creation_input_tokens"], usage["cache_create"]),
				"service_tier":  usage["service_tier"],
			}
			if messageID != "" {
				out["message_id"] = messageID
			}
			if model, ok := msg["model"].(string); ok && model != "" {
				out["model"] = model
			}
			_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "usage", "agent", out)
		}

	case "user":
		// In stream-json, `user` frames carry tool_result blocks (the
		// agent's own view of tool outputs). Plain user-text frames are
		// our own input being echoed back; skip those to avoid a loop.
		msg, _ := frame["message"].(map[string]any)
		blocks, _ := msg["content"].([]any)
		for _, b := range blocks {
			block, _ := b.(map[string]any)
			bt, _ := block["type"].(string)
			if bt != "tool_result" {
				continue
			}
			_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "tool_result", "agent",
				map[string]any{
					"tool_use_id": block["tool_use_id"],
					"content":     block["content"],
					"is_error":    block["is_error"],
				})
		}

	case "rate_limit_event":
		d.translateRateLimit(ctx, frame)

	case "result":
		// turn.result is the canonical kind. We also emit "completion"
		// with the whole frame for one release so older mobile builds
		// keep working — drop the alias once telemetry shows no caller
		// depends on it.
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "turn.result", "agent",
			normalizeTurnResult(frame))
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "completion", "agent", frame)

	case "error":
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "error", "agent", frame)

	default:
		// Unknown frame type — forward verbatim so the app can decide.
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "raw", "agent", frame)
	}
}

// translateRateLimit handles all shapes claude-code has shipped for
// the rate-limit signal:
//
//	older SDKs:    {"type":"rate_limit_event", <fields...>}
//	mid-SDKs:      {"type":"system", "subtype":"rate_limit_event", <fields...>}
//	current SDKs:  {"type":"rate_limit_event", "rate_limit_info":{<fields...>}}
//
// The current shape nests the actual fields under `rate_limit_info`,
// so peek there first and merge into the lookup namespace; otherwise
// the same translator falls back to the flat layout. Window naming
// also differs (rateLimitType vs rate_limit_type vs five_hour) — keep
// it as-is and let the mobile humanizer label it.
func (d *StdioDriver) translateRateLimit(ctx context.Context, frame map[string]any) {
	src := frame
	if nested, ok := frame["rate_limit_info"].(map[string]any); ok {
		src = nested
	} else if nested, ok := frame["rateLimitInfo"].(map[string]any); ok {
		src = nested
	}
	win, _ := firstNonNil(src["rateLimitType"], src["rate_limit_type"]).(string)
	_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "rate_limit", "agent",
		map[string]any{
			"window":           win,
			"status":           src["status"],
			"resets_at":        firstNonNil(src["resetsAt"], src["resets_at"]),
			"overage_status":   firstNonNil(src["overageStatus"], src["overage_status"]),
			"overage_disabled": firstNonNil(src["overageDisabledReason"], src["overage_disabled_reason"]) != nil,
			"is_using_overage": firstNonNil(src["isUsingOverage"], src["is_using_overage"]),
			"reason":           firstNonNil(src["overageDisabledReason"], src["overage_disabled_reason"]),
		})
}

// normalizeTurnResult lifts claude's `result` frame into the canonical
// turn.result shape. Anything missing in the source comes through as
// nil — the mobile renderer treats absent fields as "this driver
// doesn't surface that signal" and absents the matching UI bit.
func normalizeTurnResult(frame map[string]any) map[string]any {
	out := map[string]any{
		"cost_usd":           firstNonNil(frame["total_cost_usd"], frame["cost_usd"]),
		"duration_ms":        frame["duration_ms"],
		"num_turns":          frame["num_turns"],
		"terminal_reason":    firstNonNil(frame["terminal_reason"], frame["subtype"]),
		"permission_denials": frame["permission_denials"],
		"fast_mode_state":    firstNonNil(frame["fast_mode_state"], frame["fastModeState"]),
	}
	// modelUsage is keyed by model name in claude's payload: {<model>:
	// {inputTokens, outputTokens, cacheReadInputTokens, …}}. Pass it
	// through as `by_model` after normalizing the inner keys to snake_case
	// so callers don't have to learn both shapes.
	if mu, ok := firstNonNil(frame["modelUsage"], frame["model_usage"]).(map[string]any); ok {
		byModel := map[string]any{}
		for model, raw := range mu {
			inner, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			byModel[model] = map[string]any{
				"input":              firstNonNil(inner["inputTokens"], inner["input_tokens"]),
				"output":             firstNonNil(inner["outputTokens"], inner["output_tokens"]),
				"cache_read":         firstNonNil(inner["cacheReadInputTokens"], inner["cache_read_input_tokens"]),
				"cache_create":       firstNonNil(inner["cacheCreationInputTokens"], inner["cache_creation_input_tokens"]),
				"cost_usd":           firstNonNil(inner["costUSD"], inner["cost_usd"]),
				"context_window":     firstNonNil(inner["contextWindow"], inner["context_window"]),
				"max_output_tokens":  firstNonNil(inner["maxOutputTokens"], inner["max_output_tokens"]),
			}
		}
		out["by_model"] = byModel
	}
	return out
}

// firstNonNil returns the first argument that's not nil. Used to
// accept multiple casings for the same field — claude-code stream-json
// is inconsistent about camelCase vs snake_case across versions, and
// callers shouldn't have to know which we're talking to.
func firstNonNil(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

// Input implements Inputter for M2. text/cancel/approval/attach translate
// into the shapes Claude Code's stream-json reader accepts on stdin:
//   - text:     a "user" frame with content=[{type:"text", text:body}]
//   - approval: a "user" frame with content=[{type:"tool_result", …}] so
//               an approval for a pending tool_use clears through the
//               same channel the agent emitted the call on.
//   - cancel:   a "user" frame with a plain-text "cancel: <reason>" body.
//               Claude Code's CLI has no first-class cancel over stdio;
//               an explicit hub-level kill is handled by Stop() elsewhere.
//   - attach:   tool_result with a reference to the attached document id.
//
// Missing Stdin is a configuration error, not a runtime one — we surface
// it so the router can flag the agent instead of buffering forever.
func (d *StdioDriver) Input(ctx context.Context, kind string, payload map[string]any) error {
	if d.Stdin == nil {
		return fmt.Errorf("stdio driver: stdin not wired")
	}
	frame, err := buildStreamJSONInputFrame(kind, payload)
	if err != nil {
		return err
	}
	d.inputMu.Lock()
	defer d.inputMu.Unlock()
	// Context cancellation doesn't preempt the Write; a wedged child is
	// the operator's problem to kill via Stop. The lock keeps two
	// concurrent Input calls from interleaving bytes on stdin.
	_, werr := d.Stdin.Write(frame)
	return werr
}

// buildStreamJSONInputFrame produces the JSON-line bytes (with trailing
// newline) that Claude Code expects for a user-side message. Factored out
// so tests don't need a live pipe to assert on the wire shape.
func buildStreamJSONInputFrame(kind string, payload map[string]any) ([]byte, error) {
	var content []map[string]any
	switch kind {
	case "text":
		body, _ := payload["body"].(string)
		if body == "" {
			return nil, fmt.Errorf("stdio driver: text input missing body")
		}
		content = []map[string]any{{"type": "text", "text": body}}
	case "approval":
		reqID, _ := payload["request_id"].(string)
		decision, _ := payload["decision"].(string)
		if reqID == "" || decision == "" {
			return nil, fmt.Errorf("stdio driver: approval missing request_id/decision")
		}
		note, _ := payload["note"].(string)
		text := decision
		if note != "" {
			text = decision + ": " + note
		}
		content = []map[string]any{{
			"type":        "tool_result",
			"tool_use_id": reqID,
			"content":     text,
			"is_error":    decision == "deny",
		}}
	case "answer":
		// Inline reply to an AskUserQuestion (and similar) tool call —
		// content is the user's answer verbatim, surfaced to the agent
		// as a clean tool_result. Carved off `approval` so the agent
		// doesn't have to peel a "decision: note" prefix off the body.
		reqID, _ := payload["request_id"].(string)
		body, _ := payload["body"].(string)
		if reqID == "" || body == "" {
			return nil, fmt.Errorf("stdio driver: answer missing request_id/body")
		}
		content = []map[string]any{{
			"type":        "tool_result",
			"tool_use_id": reqID,
			"content":     body,
			"is_error":    false,
		}}
	case "cancel":
		reason, _ := payload["reason"].(string)
		if reason == "" {
			reason = "user requested cancel"
		}
		content = []map[string]any{{"type": "text", "text": "cancel: " + reason}}
	case "attach":
		docID, _ := payload["document_id"].(string)
		if docID == "" {
			return nil, fmt.Errorf("stdio driver: attach missing document_id")
		}
		content = []map[string]any{{
			"type": "text",
			"text": "[attach] document_id=" + docID,
		}}
	default:
		return nil, fmt.Errorf("stdio driver: unsupported input kind %q", kind)
	}
	frame := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": content,
		},
	}
	b, err := json.Marshal(frame)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
