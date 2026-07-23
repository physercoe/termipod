package kimi_code

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	locallogtail "github.com/termipod/hub/internal/drivers/local_log_tail"
)

// Config carries the runtime dependencies the adapter needs from the
// launch glue. Held read-only after Start.
type Config struct {
	// AgentID namespaces posted events (the termipod agent id, NOT
	// kimi's agents/<id> name).
	AgentID string
	// Workdir is the directory `kimi` was launched in — the key into
	// workspaces.json's cwd → wd_* mapping (pathresolver).
	Workdir string
	// StoreHome overrides the kimi session-store root. Zero = read
	// $KIMI_CODE_HOME / ~/.kimi-code at Start (StoreHome()).
	StoreHome string
	// Engine names the engine family for session.init + usage payloads
	// ("kimi-code" / "kimi-code-ts" in production; configurable so
	// tests can vary). Empty defaults to "kimi-code" in NewMapper.
	Engine string
	// EngineVersion is the `kimi --version` output, resolved by the
	// launch glue via agentfamilies.VersionFlag + runVersion (mirrors
	// the antigravity pattern). Optional; empty hides the row.
	EngineVersion string
	// PermissionMode is the launch-derived permission posture ("yolo"
	// when --yolo is on backend.cmd, else "interactive"). Surfaced
	// verbatim on session.init.
	PermissionMode string
	// Poster publishes AgentEvents to the hub.
	Poster locallogtail.EventPoster
	// Log is optional; defaults to slog.Default().
	Log *slog.Logger
}

// Adapter implements locallogtail.Adapter for Kimi Code CLI. It
// composes: pathresolver (workspaces.json → wd → session dir) →
// per-agent Tailer (append-follow, partial-line tolerant) → Mapper →
// Poster. Input is routed via tmux send-keys (sendkeys.go). kimi has
// no host-runner hook surface, so OnHook is a benign no-op (mirrors
// the antigravity adapter).
type Adapter struct {
	Config

	// PaneID is the tmux pane id kimi runs in (resolved by the launch
	// glue). Required before HandleInput can send-keys.
	PaneID string
	// CmdRunner overrides the exec-backed runner in tests.
	CmdRunner CmdRunner
	// SessionDir is resolved at Start (the <store>/sessions/<wd>/<session_*>
	// dir this spawn minted). Exposed for tests + forensics.
	SessionDir string
	// SessionWaitTimeout caps how long Start's pipeline waits for the
	// session dir to appear. 0 → 10 minutes (kimi mints the session at
	// process start, typically within seconds; the generous cap covers
	// a slow first-run auth/workspace flow).
	SessionWaitTimeout time.Duration

	mu      sync.Mutex
	started bool
	stopped bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	// tailers tracks the live per-agent wire tails (agentID → Tailer)
	// so Stop can drain them deterministically.
	tailers map[string]*Tailer
	// disabled records agent tails stopped by the protocol gate so the
	// watch loop doesn't re-attach them on the next poll.
	disabled map[string]bool
}

// NewAdapter validates mandatory config so the launch glue can fall
// back to PaneDriver without leaking a half-built struct.
func NewAdapter(cfg Config) (*Adapter, error) {
	if cfg.AgentID == "" {
		return nil, fmt.Errorf("kimi-code adapter: AgentID required")
	}
	if cfg.Workdir == "" {
		return nil, fmt.Errorf("kimi-code adapter: Workdir required")
	}
	if cfg.Poster == nil {
		return nil, fmt.Errorf("kimi-code adapter: Poster required")
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Adapter{Config: cfg}, nil
}

// Start launches the asynchronous resolver+tail pipeline and returns
// immediately, mirroring the antigravity adapter's async-Start
// rationale: the driver flips the agent to `running` once Start
// returns, and a slow kimi cold-start (or a workspace that needs a
// first-run dialog) must not deadlock the runner. Failures inside the
// pipeline surface as `system` notice events — the pane is still live,
// only the wire-derived events are missing.
func (a *Adapter) Start(parent context.Context) error {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return nil
	}
	a.started = true
	a.tailers = map[string]*Tailer{}
	a.disabled = map[string]bool{}
	ctx, cancel := context.WithCancel(parent)
	a.cancel = cancel
	a.mu.Unlock()

	a.wg.Add(1)
	go a.resolveAndRun(ctx)
	return nil
}

// resolveAndRun is the async pipeline kicked off by Start: resolve the
// session dir → post session.init → watch the agents/ tree for wire
// files (main + subagents) and run a tail+map loop per agent until
// the context fires.
func (a *Adapter) resolveAndRun(ctx context.Context) {
	defer a.wg.Done()

	// Captured before any waiting so the since-cutoff in WaitForSession
	// distinguishes "kimi minted this session for our spawn" from prior
	// sessions in the same cwd (mirrors the antigravity adapter's
	// launchTime rationale).
	launchTime := time.Now()

	storeHome := a.StoreHome
	if storeHome == "" {
		sh, err := StoreHome()
		if err != nil {
			a.noteFailure(ctx, "resolve store home", err)
			return
		}
		storeHome = sh
	}

	waitTimeout := a.SessionWaitTimeout
	if waitTimeout <= 0 {
		waitTimeout = 10 * time.Minute
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, waitTimeout)
	defer cancelWait()

	sessionDir, err := WaitForSession(waitCtx, storeHome, a.Workdir, 0, launchTime.Add(-time.Second))
	if err != nil {
		a.noteFailure(ctx, "resolve session dir", err)
		return
	}
	a.mu.Lock()
	a.SessionDir = sessionDir
	a.mu.Unlock()

	// Launch-time session metadata. session_id is the load-bearing
	// field (ADR-035 D8 / ADR-014 pattern — the hub lifts it into
	// sessions.engine_session_id); the engine/version/cwd/
	// permission_mode fields feed the mobile session-details sheet,
	// mirroring the antigravity adapter's buildLaunchTimeSessionInit.
	_ = a.Poster.PostAgentEvent(ctx, a.AgentID, "session.init", "agent",
		a.buildLaunchTimeSessionInit(filepath.Base(sessionDir)))

	a.Log.Info("kimi-code adapter started",
		"agent_id", a.AgentID,
		"workdir", a.Workdir,
		"session_dir", sessionDir,
		"store_home", storeHome)

	a.watchAgents(ctx, sessionDir)
}

// watchAgents polls the session's agents/ tree for wire.jsonl files
// and attaches a tail per newly-seen agent (main first, then subagents
// as kimi spawns them mid-session). Runs until ctx fires.
func (a *Adapter) watchAgents(ctx context.Context, sessionDir string) {
	attached := map[string]bool{}
	scan := func() bool {
		wires, err := ListAgentWireFiles(sessionDir)
		if err != nil {
			a.Log.Warn("kimi-code adapter: agents scan failed",
				"agent_id", a.AgentID, "err", err)
			return true // keep polling; transient fs hiccup
		}
		// Attach main first when several appear in the same poll so the
		// transcript's causal order (delegation → subagent activity) is
		// preserved.
		order := make([]string, 0, len(wires))
		if _, ok := wires["main"]; ok {
			order = append(order, "main")
		}
		for id := range wires {
			if id != "main" {
				order = append(order, id)
			}
		}
		for _, id := range order {
			if attached[id] {
				continue
			}
			a.mu.Lock()
			disabled := a.disabled[id]
			a.mu.Unlock()
			if disabled {
				continue
			}
			attached[id] = true
			parent := a.resolveParent(ctx, sessionDir, id)
			a.wg.Add(1)
			go a.tailAgent(ctx, id, parent, wires[id])
		}
		return true
	}

	// Attach whatever already exists, then poll for late arrivals
	// (subagents mint their wire files mid-turn).
	scan()
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			scan()
		}
	}
}

// resolveParent reads the subagent's parentAgentId from state.json.
// state.json lags the subagent's wire dir by a beat (kimi creates the
// agent dir, then flushes the tree), so retry briefly before giving up
// ("" parent — events still flow, just without the tree edge).
func (a *Adapter) resolveParent(ctx context.Context, sessionDir, agentID string) string {
	if agentID == "main" {
		return ""
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		parents, err := ReadAgentParents(sessionDir)
		if err == nil {
			return parents[agentID]
		}
		if !errors.Is(err, ErrNoState) || time.Now().After(deadline) {
			if !errors.Is(err, ErrNoState) {
				a.Log.Warn("kimi-code adapter: read state.json failed",
					"agent_id", a.AgentID, "err", err)
			}
			return ""
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// tailAgent runs one agent's wire tail: open, then map every complete
// line until the file stops, the context fires, or the protocol gate
// rejects the stream.
func (a *Adapter) tailAgent(ctx context.Context, agentID, parentID, wirePath string) {
	defer a.wg.Done()

	tailer := &Tailer{Path: wirePath, Mode: StartFromBeginning}
	lines, err := tailer.Start(ctx)
	if err != nil {
		a.Log.Warn("kimi-code adapter: tail open failed",
			"agent_id", a.AgentID, "kimi_agent", agentID, "err", err)
		return
	}
	a.mu.Lock()
	a.tailers[agentID] = tailer
	a.mu.Unlock()
	defer func() {
		tailer.Stop()
		a.mu.Lock()
		delete(a.tailers, agentID)
		a.mu.Unlock()
	}()

	if agentID != "main" {
		// Mark the delegation point in the transcript so the subagent's
		// inner activity that follows reads as attached to the main
		// agent's Agent tool.call. Producer system keeps it off the
		// clients' busy-inference path (same treatment as lifecycle).
		_ = a.Poster.PostAgentEvent(ctx, a.AgentID, "system", "system",
			map[string]any{
				"subtype":         "subagent_attached",
				"kimi_agent_id":   agentID,
				"parent_agent_id": parentID,
				"text": fmt.Sprintf("subagent %s attached (parent %s) — inner activity follows",
					agentID, orUnknown(parentID)),
			})
	}

	mapper := NewMapper(agentID, parentID, a.Engine)
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			events, merr := mapper.MapLine(line.Bytes)
			if merr != nil {
				if errors.Is(merr, ErrUnsupportedProtocol) {
					// Protocol gate (plan §6 P4: "the adapter must gate
					// on the wire `metadata` protocol version"). Stop
					// this agent's structured tail, post a notice, and
					// mark the agent disabled so the watch loop doesn't
					// re-attach. The pane itself stays live — the user
					// keeps the raw terminal transcript.
					a.mu.Lock()
					a.disabled[agentID] = true
					a.mu.Unlock()
					a.Log.Warn("kimi-code adapter: unsupported wire protocol; structured tail disabled",
						"agent_id", a.AgentID, "kimi_agent", agentID, "err", merr)
					_ = a.Poster.PostAgentEvent(ctx, a.AgentID, "system", "system",
						map[string]any{
							"subtype": "kimi_wire_unsupported_protocol",
							"text": fmt.Sprintf(
								"kimi wire protocol unsupported (%v). Structured transcript disabled — the pane is still live.",
								merr),
						})
					return
				}
				// A torn/unknown line: log + drop so one bad write
				// doesn't take down the tail (mirrors the antigravity
				// runLoop's mapper-error policy).
				a.Log.Warn("kimi-code adapter: mapper error; dropping line",
					"agent_id", a.AgentID, "kimi_agent", agentID, "err", merr)
				continue
			}
			for _, ev := range events {
				if err := a.Poster.PostAgentEvent(ctx, a.AgentID, ev.Kind, ev.Producer, ev.Payload); err != nil {
					a.Log.Debug("kimi-code adapter: post failed",
						"agent_id", a.AgentID, "kind", ev.Kind, "err", err)
				}
			}
		}
	}
}

// noteFailure logs and surfaces a soft failure: the pane is still live,
// but wire-derived events won't flow for this session. Mirrors the
// antigravity adapter's noteFailure.
func (a *Adapter) noteFailure(ctx context.Context, phase string, err error) {
	a.Log.Warn("kimi-code adapter: async pipeline aborted",
		"agent_id", a.AgentID, "phase", phase, "err", err)
	_ = a.Poster.PostAgentEvent(ctx, a.AgentID, "system", "system",
		map[string]any{
			"text": fmt.Sprintf(
				"kimi wire tail unavailable (%s: %v). "+
					"The pane is still live — type to interact via tmux.",
				phase, err),
		})
}

// Stop tears down the adapter. Idempotent; waits for the watch loop +
// per-agent tails to drain so no PostAgentEvent fires after Stop
// returns.
func (a *Adapter) Stop() {
	a.mu.Lock()
	if a.stopped || !a.started {
		a.mu.Unlock()
		return
	}
	a.stopped = true
	cancel := a.cancel
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	a.wg.Wait()
}

// OnHook is a no-op: kimi has no host-runner hook surface (no
// permission-prompt-tool — that is claude-code-specific, ADR-027 W5b).
// Returning {} keeps the locallogtail.Adapter contract satisfied
// (mirrors the antigravity adapter).
func (a *Adapter) OnHook(_ context.Context, _ string, _ map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}

// buildLaunchTimeSessionInit assembles the session.init payload posted
// once the session resolves. session_id is always present; the other
// fields are added only when populated so mobile's section-gating
// hides absent rows cleanly (mirrors the antigravity precedent).
func (a *Adapter) buildLaunchTimeSessionInit(sessionID string) map[string]any {
	payload := map[string]any{"session_id": sessionID}
	if a.Engine != "" {
		payload["engine"] = a.Engine
	}
	if a.EngineVersion != "" {
		payload["version"] = a.EngineVersion
	}
	if a.Workdir != "" {
		payload["cwd"] = a.Workdir
	}
	if a.PermissionMode != "" {
		payload["permission_mode"] = a.PermissionMode
	}
	return payload
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// Compile-time assertion: *Adapter satisfies locallogtail.Adapter.
var _ locallogtail.Adapter = (*Adapter)(nil)
