package hostrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	hub "github.com/termipod/hub"
	"github.com/termipod/hub/internal/agentfamilies"
	"github.com/termipod/hub/internal/hostrunner/a2a"
	"github.com/termipod/hub/internal/hostrunner/tbreader"
	"github.com/termipod/hub/internal/hostrunner/trackio"
	"github.com/termipod/hub/internal/hostrunner/wandb"
)

// defaultStewardWorkdir is the directory the built-in steward template
// expects under `backend.default_workdir` (templates/agents/steward.v1.yaml).
// host-runner ensures it exists at startup so the M2 launcher's `cd`
// into it can't fail with ENOENT on a fresh host. This is the
// in-runner equivalent of "the host installer mkdir's it once" —
// idempotent, runs regardless of which install track was used.
const defaultStewardWorkdir = "hub-work"

// Runner is the long-running host-runner loop. It registers the host with the
// hub (first boot), heartbeats every HeartbeatInterval, and polls every
// PollInterval for pending spawns on this host. For each spawn it calls
// Launcher.Launch, then PATCHes the child agent to status=running with the
// tmux pane id.
//
// The hub is the source of truth: host-runner carries no local state across
// restarts, it just re-derives the world from /v1/teams/.../agents/spawns.
type Runner struct {
	Client   *Client
	HostName string
	HostID   string // filled in on Start() if empty
	Launcher Launcher
	Log      *slog.Logger

	HeartbeatInterval    time.Duration
	PollInterval         time.Duration
	ProbeInterval        time.Duration // 0 = 15 min
	IdleThreshold        time.Duration // 0 = use default from NewIdleDetector
	A2ADirectoryInterval time.Duration // 0 = 30s; ignored if A2AAddr is empty

	// MetricsPollInterval is the per-backend scrape cadence for the
	// metrics.Reader loops (trackio, wandb, TensorBoard, …). 0 = 20s.
	MetricsPollInterval time.Duration

	// MetricsMaxPoints caps the per-metric sample count uploaded to the
	// hub from every metrics.Reader backend. 0 falls back to 100 — the
	// blueprint default (§6.5).
	MetricsMaxPoints int

	// TrackioDir, when non-empty, enables the trackio metric-digest
	// backend: the runner periodically reads each run's {project}.db
	// under this directory and PUTs the downsampled digest to the hub.
	// Set to trackio.DefaultDir() in the common case; leave empty to
	// disable this backend.
	TrackioDir string

	// WandbDir, when non-empty, enables the wandb offline-mode metric-
	// digest backend: the runner periodically reads each run's
	// files/wandb-history.jsonl under this directory. Runs are routed
	// to this backend by the `wandb://` URI scheme.
	WandbDir string

	// TensorBoardDir, when non-empty, enables the TensorBoard tfevents
	// metric-digest backend: the runner walks <TensorBoardDir>/<run-path>
	// for each run whose trackio_run_uri is `tb://<run-path>`.
	TensorBoardDir string

	// StateDir, when non-empty, caches host_id after the first register so
	// a restart skips the register round-trip. Keyed by (hub, team, name).
	StateDir string

	// A2AAddr is the bind address for the host-runner A2A server (§5.4, P3.2).
	// Empty disables the server. ":0" picks a free port.
	A2AAddr string

	// A2APublicURL, if set, is the base URL advertised in agent-cards. Use
	// this when the bind address is not reachable from peers (e.g. behind
	// a reverse tunnel). Falls back to the request Host header otherwise.
	A2APublicURL string

	// EgressProxyAddr is the bind address for the in-process reverse
	// proxy that masks the hub URL from spawned agents. Empty disables
	// the proxy and `.mcp.json` carries the real hub URL (legacy
	// behavior). Default in main.go is 127.0.0.1:41825 — uncommon
	// 5-digit port to avoid clashing with anything an operator is
	// likely to already run.
	EgressProxyAddr string
	egressProxy     *egressProxy

	idle      *IdleDetector
	panes     map[string]paneState    // keyed by agent id
	tailers   map[string]*Tailer      // keyed by agent id
	drivers   map[string]Driver       // keyed by agent id (P1.1)
	worktrees map[string]WorktreeSpec // keyed by agent id
	inputs    *InputRouter            // P1.8 — dispatches producer=user events
	// a2aDisp owns A2A task correlation. Created in Start when A2AAddr
	// is set so driver output events can be harvested into a2a task
	// history. Nil disables the tap (events flow to the hub unchanged).
	a2aDisp *a2aHubDispatcher
	// agentPoster is the AgentEventPoster drivers are handed. When a2aDisp
	// is non-nil this is a tap that mirrors events into the correlator;
	// otherwise it points straight at Client.
	agentPoster AgentEventPoster
	// templates indexes templates/agents/*.yaml by kind so the launcher
	// and A2A card serving can pull per-kind backend.cmd and skills from
	// data rather than code. Initialized in defaults() off the embedded
	// TemplatesFS; overridable by tests.
	templates *agentTemplates

	// hostInfo is probed once at startup (OS / arch / cpu / mem /
	// kernel / hostname). Attached to every capabilities push so the
	// hub mobile detail screen always carries the static facts about
	// the box. Cached because /proc/meminfo + uname don't change while
	// we're up — re-probing each tick would be pure overhead.
	hostInfo *HostInfo
}

func (a *Runner) defaults() {
	if a.HeartbeatInterval == 0 {
		a.HeartbeatInterval = 10 * time.Second
	}
	if a.PollInterval == 0 {
		a.PollInterval = 3 * time.Second
	}
	if a.ProbeInterval == 0 {
		a.ProbeInterval = 15 * time.Minute
	}
	if a.A2ADirectoryInterval == 0 {
		a.A2ADirectoryInterval = 30 * time.Second
	}
	if a.MetricsPollInterval == 0 {
		a.MetricsPollInterval = 20 * time.Second
	}
	if a.MetricsMaxPoints == 0 {
		a.MetricsMaxPoints = 100
	}
	if a.Log == nil {
		a.Log = slog.Default()
	}
	if a.Launcher == nil {
		a.Launcher = StubLauncher{Log: a.Log}
	}
	if a.idle == nil {
		a.idle = NewIdleDetector(a.IdleThreshold)
	}
	if a.templates == nil {
		// Built-in agent templates ship with the binary via
		// hub.TemplatesFS; loadAgentTemplates parses them once. A YAML-
		// level error in one template does not fail the loader — the
		// offending template just has no skills / backend cmd.
		tpl, err := loadAgentTemplates(hub.TemplatesFS, "templates/agents")
		if err != nil {
			a.Log.Warn("agent-templates loader init failed; running with empty index", "err", err)
			a.templates = &agentTemplates{}
		} else {
			a.templates = tpl
		}
	}
	if a.panes == nil {
		a.panes = map[string]paneState{}
	}
	if a.tailers == nil {
		a.tailers = map[string]*Tailer{}
	}
	if a.drivers == nil {
		a.drivers = map[string]Driver{}
	}
	if a.worktrees == nil {
		a.worktrees = map[string]WorktreeSpec{}
	}
	if a.inputs == nil {
		a.inputs = NewInputRouter(a.Client, a.Log)
	}
	// Default agentPoster to the raw client; Start swaps in the a2a
	// tap if the A2A server is enabled.
	if a.agentPoster == nil {
		a.agentPoster = a.Client
	}
}

// ensureDefaultWorkdir creates ~/hub-work on first boot if absent. The
// steward template's default_workdir points here; without it, the M2
// launcher's `cd` would fail before claude ever started. Errors are
// logged but non-fatal — a misconfigured user can still spawn agents
// whose templates use a different workdir, and we'd rather see the
// claude-side error than block startup on a directory we can't create.
func (a *Runner) ensureDefaultWorkdir() {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		a.Log.Warn("ensureDefaultWorkdir: HOME unresolved; skipping mkdir", "err", err)
		return
	}
	dir := filepath.Join(home, defaultStewardWorkdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.Log.Warn("ensureDefaultWorkdir: mkdir failed; agents using this default_workdir will error on spawn", "dir", dir, "err", err)
		return
	}
	a.Log.Debug("ensureDefaultWorkdir: ready", "dir", dir)
}

// Start registers the host (if HostID is empty) and runs until ctx is done.
func (a *Runner) Start(ctx context.Context) error {
	a.defaults()
	a.ensureDefaultWorkdir()

	// Probe static host info once at startup. Memory/kernel/CPU don't
	// change while we're up, and the periodic capabilities sweep is
	// hot enough that re-reading these each tick would be wasted work.
	hi := ProbeHostInfo(ctx)
	a.hostInfo = &hi
	a.Log.Info("host-info probed",
		"os", hi.OS, "arch", hi.Arch,
		"cpu", hi.CPUCount, "mem_gib", hi.MemBytes/(1<<30),
		"kernel", hi.Kernel)

	if a.HostID == "" && a.StateDir != "" {
		if id, ok := loadStateEntry(a.StateDir, a.Client.BaseURL, a.Client.Team, a.HostName); ok {
			a.HostID = id
			a.Log.Info("host-id loaded from state", "host_id", id, "name", a.HostName)
		}
	}
	if a.HostID == "" {
		id, err := a.Client.RegisterHost(ctx, a.HostName, nil)
		if err != nil {
			return err
		}
		a.HostID = id
		a.Log.Info("host registered", "host_id", id, "name", a.HostName)
		if a.StateDir != "" {
			if err := saveStateEntry(a.StateDir, a.Client.BaseURL, a.Client.Team, a.HostName, id); err != nil {
				a.Log.Warn("save host-id failed", "err", err)
			}
		}
	}

	// Sanity-check the dependencies an operator must have on PATH for
	// agents to actually work end-to-end. Missing binaries don't stop
	// the runner — agents just fail later with confusing errors —
	// so we warn loudly at boot instead. host-runner is now a multicall
	// binary that also handles `hub-mcp-bridge`; operators set the
	// symlink at install time per docs/hub-host-setup.md §4.
	for _, bin := range []string{"hub-mcp-bridge", "tmux"} {
		if _, err := exec.LookPath(bin); err != nil {
			a.Log.Warn("required binary missing from PATH",
				"bin", bin,
				"hint", "agents that depend on this will fail (e.g. claude-code session.init reports MCP server failed); install per docs/hub-host-setup.md §4")
		}
	}

	// Egress proxy: in-process reverse proxy bound to localhost so
	// spawned agents see "hub is on 127.0.0.1:NNNN" in `.mcp.json`
	// instead of the public hub URL. Disabled when EgressProxyAddr
	// is empty (legacy behavior — `.mcp.json` carries the real URL).
	if a.EgressProxyAddr != "" {
		ep, err := startEgressProxy(ctx, a.EgressProxyAddr, a.Client.BaseURL, a.Log)
		if err != nil {
			a.Log.Warn("egress-proxy disabled (bind failed); .mcp.json will use the real hub URL",
				"addr", a.EgressProxyAddr, "err", err)
		} else {
			a.egressProxy = ep
			defer func() {
				shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = a.egressProxy.shutdown(shutCtx)
			}()
		}
	}

	hb := time.NewTicker(a.HeartbeatInterval)
	defer hb.Stop()
	poll := time.NewTicker(a.PollInterval)
	defer poll.Stop()

	// Capability probing runs on its own goroutine so a slow --version call
	// can't stall heartbeats. Push on change only; the hub stores the last
	// payload verbatim and we want to avoid write amplification.
	go a.probeLoop(ctx)

	// Kick off an immediate poll so bootstrap isn't delayed by the first tick.
	a.tickPoll(ctx)
	a.tickCommands(ctx)

	if a.A2AAddr != "" {
		// Create the dispatcher first so driver poster wrapping below
		// can tap its OnAgentEvent for response harvesting.
		a.a2aDisp = newA2AHubDispatcher(a.Client)
		a.agentPoster = newA2APosterTap(a.Client, a.a2aDisp)
		srv := &a2a.Server{
			PublicURL:  a.A2APublicURL,
			Source:     a.a2aSource,
			Dispatcher: a.a2aDisp,
			Log:        a.Log,
		}
		addr, err := srv.Listen(ctx, a.A2AAddr)
		if err != nil {
			a.Log.Warn("a2a server failed to listen", "addr", a.A2AAddr, "err", err)
		} else {
			a.Log.Info("a2a server listening", "addr", addr)
		}
		// Publish cards to the hub directory so the steward can discover
		// agents by handle across hosts.
		go a.a2aDirectoryLoop(ctx)
		// Open the reverse tunnel so NAT'd hosts can receive relayed A2A
		// requests via the hub. Dispatches come straight into the local
		// a2a.Server.Handler() so relayed calls hit the exact same routes
		// a direct peer would.
		go a2a.RunTunnel(ctx, a.Client, a.HostID, srv.Handler(), a.Log)
	}

	if a.TrackioDir != "" {
		go a.metricsPollLoop(ctx, trackio.New(a.TrackioDir), a.MetricsPollInterval, a.MetricsMaxPoints)
	}
	if a.WandbDir != "" {
		go a.metricsPollLoop(ctx, wandb.New(a.WandbDir), a.MetricsPollInterval, a.MetricsMaxPoints)
	}
	if a.TensorBoardDir != "" {
		go a.metricsPollLoop(ctx, tbreader.New(a.TensorBoardDir), a.MetricsPollInterval, a.MetricsMaxPoints)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-hb.C:
			if err := a.Client.Heartbeat(ctx, a.HostID); err != nil {
				a.Log.Warn("heartbeat failed", "err", err)
			}
		case <-poll.C:
			a.tickPoll(ctx)
			a.tickCommands(ctx)
			a.tickReconcile(ctx)
			a.tickIdle(ctx)
		}
	}
}

// tickIdle captures each running pane, runs the idle detector, and raises
// an attention item per stuck pane (deduped by hash inside Inspect).
// Errors on a single pane are logged and skipped — one bad pane shouldn't
// prevent us from watching the others.
func (a *Runner) tickIdle(ctx context.Context) {
	agents, err := a.Client.ListRunningAgents(ctx, a.HostID)
	if err != nil {
		return
	}
	now := time.Now()
	seen := map[string]struct{}{}
	for _, ag := range agents {
		seen[ag.ID] = struct{}{}
		if ag.PaneID == "" || ag.PauseState == "paused" {
			continue
		}
		text, err := runTmux(ctx, "capture-pane", "-p", "-J", "-t", ag.PaneID)
		if err != nil {
			a.Log.Debug("capture for idle failed", "agent", ag.ID, "err", err)
			continue
		}
		prev := a.panes[ag.ID]
		next, raise := a.idle.Inspect(text, prev, now)
		a.panes[ag.ID] = next
		if raise {
			tail := tailLines(text, 5)
			_, _ = a.Client.PostAttention(ctx, AttentionIn{
				ScopeKind: "team",
				Kind:      "idle",
				Summary:   "agent idle at prompt: " + firstLine(tail),
				Severity:  "minor",
			})
			a.Log.Info("idle attention raised", "agent", ag.ID, "handle", ag.Handle)
		}
	}
	// Drop state for panes that no longer report running — on respawn the hash
	// would spuriously match the stale entry and suppress a legit idle alert.
	for id := range a.panes {
		if _, ok := seen[id]; !ok {
			delete(a.panes, id)
		}
	}
	// Same for tailers: once an agent is no longer in the running set, its
	// FIFO + pipe-pane should be torn down so we don't leak fds / tmp files.
	for id := range a.tailers {
		if _, ok := seen[id]; !ok {
			a.stopTailer(id)
		}
	}
	// Driver teardown is owned by tickReconcile, which uses
	// ListHostAgents and only stops drivers on terminal status. tickIdle's
	// running-set view would tear down freshly-spawned M2 agents during
	// the brief pending → running window before reconcile flips them.
	// Drop worktree bookkeeping for agents the hub no longer considers
	// running. Anything the terminate handler missed (e.g. host-runner
	// killed before cleanup) will be re-synced by `git worktree prune`
	// on the next terminate for that repo.
	for id := range a.worktrees {
		if _, ok := seen[id]; !ok {
			delete(a.worktrees, id)
		}
	}
}

func (a *Runner) tickCommands(ctx context.Context) {
	cmds, err := a.Client.ListPendingCommands(ctx, a.HostID)
	if err != nil {
		a.Log.Warn("list commands failed", "err", err)
		return
	}
	for _, c := range cmds {
		a.runCommand(ctx, c)
	}
}

func (a *Runner) tickPoll(ctx context.Context) {
	spawns, err := a.Client.ListPendingSpawns(ctx, a.HostID)
	if err != nil {
		a.Log.Warn("list pending failed", "err", err)
		return
	}
	for _, sp := range spawns {
		a.launchOne(ctx, sp)
	}
}

func (a *Runner) launchOne(ctx context.Context, sp Spawn) {
	// Parse the spec up front; ChannelID binding (tailer) and Worktree are
	// both optional and we want to reason about them together.
	spec, err := ParseSpec(sp.SpawnSpec)
	if err != nil {
		a.Log.Warn("parse spawn spec failed", "handle", sp.Handle, "err", err)
		// Fall through — a bad YAML shouldn't block the pane itself; the
		// spec-derived features (markers, worktree) just won't be wired.
	}

	// Ensure the worktree exists before we launch — the backend process
	// will almost certainly cd into it, so having it ready avoids a race
	// where the first command runs in a non-existent dir.
	if sp.WorktreePath != "" && spec.Worktree.Repo != "" {
		wt := WorktreeSpec{
			Repo:   spec.Worktree.Repo,
			Path:   sp.WorktreePath,
			Branch: spec.Worktree.Branch,
			Base:   spec.Worktree.Base,
		}
		created, werr := EnsureWorktree(ctx, wt)
		if werr != nil {
			a.Log.Warn("worktree ensure failed",
				"handle", sp.Handle, "path", wt.Path, "err", werr)
			// Continue: the pane still launches; the operator can retry
			// by hand. Aborting here would leave the agent in pending
			// forever, which is worse than "missing worktree".
		} else if created {
			a.Log.Info("worktree created",
				"handle", sp.Handle, "path", wt.Path, "branch", wt.Branch)
		}
		a.worktrees[sp.ChildID] = wt
	}

	// Dispatch by resolved driving mode. The hub is authoritative — it
	// resolves mode against host capabilities + billing before the spawn
	// lands here. An empty Mode means "hub had no opinion" (opt-in
	// column) and we default to M4 so unmigrated clients keep working.
	//
	// Runtime fallback chain: hub-side resolution checks host capabilities,
	// but doesn't catch *runtime* launch failures (M1 ACP handshake stall on
	// expired creds, M2 stdio start that exits before the first frame). Walk
	// the spec's fallback_modes list one rung at a time so a transient
	// failure on a higher mode lands on the next-best mode rather than
	// straight on M4. M4 always works, so it remains the final rung.
	primary := sp.Mode
	if primary == "" {
		primary = "M4"
	}
	candidates := []string{primary}
	for _, m := range spec.FallbackModes {
		if m == primary || m == "" {
			continue
		}
		candidates = append(candidates, m)
	}
	if len(candidates) == 0 || candidates[len(candidates)-1] != "M4" {
		candidates = append(candidates, "M4")
	}

	var pane string
	var drv Driver
	mode := primary
	for _, cand := range candidates {
		mode = cand
		switch cand {
		case "M2":
			// Prefer the egress-proxy URL when the proxy is up — that way
			// the agent's .mcp.json points at 127.0.0.1:NNNN instead of
			// the public hub URL. Falls back to the real URL when the
			// proxy is disabled or its bind failed at start.
			hubURLForAgent := a.Client.BaseURL
			if a.egressProxy != nil {
				hubURLForAgent = a.egressProxy.LocalURL
			}
			res, m2err := launchM2(ctx, M2LaunchConfig{
				Spawn:    sp,
				Launcher: a.Launcher,
				Client:   a.agentPoster,
				HubURL:   hubURLForAgent,
			})
			if m2err != nil {
				a.Log.Warn("M2 launch failed; trying next fallback",
					"handle", sp.Handle, "err", m2err)
				continue
			}
			pane = res.PaneID
			drv = res.Driver
		case "M1":
			// M1 = ACP daemon. Spawn the engine in `--acp` mode, wire
			// ACPDriver to its stdio. On handshake failure the next
			// fallback in the template's fallback_modes list takes over
			// (typically M2 → M4); a missing list collapses to M4.
			hubURLForAgent := a.Client.BaseURL
			if a.egressProxy != nil {
				hubURLForAgent = a.egressProxy.LocalURL
			}
			res, m1err := launchM1(ctx, M1LaunchConfig{
				Spawn:    sp,
				Launcher: a.Launcher,
				Client:   a.agentPoster,
				HubURL:   hubURLForAgent,
			})
			if m1err != nil {
				a.Log.Warn("M1 launch failed; trying next fallback",
					"handle", sp.Handle, "err", m1err)
				continue
			}
			pane = res.PaneID
			drv = res.Driver
		case "M4":
			// M4 path is built below outside the loop — fall through so
			// the existing M4 launcher constructs the PaneDriver. break
			// out of the for loop because no higher mode will be tried
			// after M4.
		default:
			a.Log.Warn("unknown driving mode; skipping",
				"handle", sp.Handle, "mode", cand)
			continue
		}
		// A successful M1/M2 launch sets drv; if it's still nil we landed
		// on M4 and let the M4 block below build the pane driver.
		if drv != nil || cand == "M4" {
			break
		}
	}

	if mode == "M4" && drv == nil {
		// Resolve the pane command data-first: a spec-level backend.cmd
		// wins over the per-kind template default, which wins over the
		// launcher's built-in placeholder. Keeping this ladder in the
		// runner (not in TmuxLauncher) lets alternate launchers reuse
		// the same policy without reimplementing it.
		cmd := spec.Backend.Cmd
		if cmd == "" {
			cmd = a.templates.BackendCmd(sp.Kind)
		}
		var p string
		var err error
		if cmd != "" {
			p, err = a.Launcher.LaunchCmd(ctx, sp, cmd)
		} else {
			// Falling all the way through to the launcher's "no backend
			// configured" placeholder almost always means the spawn YAML
			// reached the runner with an empty `backend.cmd` AND no
			// matching per-kind template. Surface both inputs so an
			// operator can tell *which* layer dropped the cmd: a stale
			// team-local template, a renderer that produced empty output,
			// or a kind-keyed lookup that doesn't have the spawn's kind.
			a.Log.Warn("no backend.cmd in spawn spec or template; using launcher default",
				"handle", sp.Handle, "kind", sp.Kind,
				"spec_backend_cmd_empty", spec.Backend.Cmd == "",
				"template_kinds_known", a.templates != nil)
			p, err = a.Launcher.Launch(ctx, sp)
		}
		if err != nil {
			a.Log.Error("launch failed", "handle", sp.Handle, "err", err)
			status := "failed"
			_ = a.Client.PatchAgent(ctx, sp.ChildID, AgentPatch{Status: &status})
			return
		}
		pane = p
		drv = &PaneDriver{
			AgentID: sp.ChildID,
			PaneID:  pane,
			Poster:  a.agentPoster,
			Log:     a.Log,
		}
	}

	// Record the pane id but leave status='pending'. The reconcile tick
	// owns the pending → running transition: it flips only once tmux
	// reports a non-shell foreground command, so a pane whose CLI failed
	// to start is correctly distinguished from a working one.
	//
	// Exec-per-turn drivers (gemini-cli, ADR-013) have no pane to anchor
	// — there's no long-running process for tmux to watch. Reconcile
	// gates on PaneID != "" and would skip these forever, leaving the
	// agent stuck pending. Patch status="running" here directly: the
	// driver Start() succeeded above, so the agent IS live by every
	// definition this mode supports.
	if pane != "" {
		if err := a.Client.PatchAgent(ctx, sp.ChildID, AgentPatch{
			PaneID: &pane,
		}); err != nil {
			a.Log.Error("patch agent failed", "handle", sp.Handle, "err", err)
			return
		}
	} else if drv != nil {
		running := "running"
		if err := a.Client.PatchAgent(ctx, sp.ChildID, AgentPatch{
			Status: &running,
		}); err != nil {
			a.Log.Error("patch agent status failed", "handle", sp.Handle, "err", err)
			return
		}
	}
	a.Log.Info("agent pane created", "handle", sp.Handle, "pane", pane, "mode", mode)

	// M2's driver is already Start()ed inside launchM2; M4's is not — keep
	// the start conditional so we don't double-emit lifecycle.started.
	if _, ok := drv.(*PaneDriver); ok {
		if err := drv.Start(ctx); err != nil {
			a.Log.Warn("driver start failed", "agent", sp.ChildID, "err", err)
			drv = nil
		}
	}
	if drv != nil {
		a.drivers[sp.ChildID] = drv
		// Attach the input router if this driver speaks Inputter. Missing
		// interface just means no user→agent routing for this mode, which
		// is fine — Stop will still be called normally.
		if inp, ok := drv.(Inputter); ok {
			a.inputs.Attach(ctx, sp.ChildID, inp, 0)
		}
	}

	// Best-effort: bring up a marker tailer if the spawn spec bound the
	// agent to a project/channel. Missing IDs leave the pane visible in
	// tmux without event forwarding — the backend can still speak MCP
	// directly if that's configured elsewhere. Spec was already parsed at
	// the top of launchOne; reuse it.
	if spec.ChannelID == "" || spec.ProjectID == "" {
		return
	}
	t := &Tailer{
		AgentID:   sp.ChildID,
		PaneID:    pane,
		ProjectID: spec.ProjectID,
		ChannelID: spec.ChannelID,
		Client:    a.Client,
		Log:       a.Log,
	}
	if err := t.Start(ctx); err != nil {
		a.Log.Warn("tailer start failed", "agent", sp.ChildID, "err", err)
		return
	}
	a.tailers[sp.ChildID] = t
}

// probeLoop runs an initial capability probe and re-probes every
// ProbeInterval. A PUT is issued only when the hash changes, so the hub
// sees one write per genuine binary install/upgrade rather than one per tick.
//
// Each probe sweep fetches the family registry from the hub so a hot edit
// on mobile (e.g. adding a `kimi` family) propagates to capabilities on
// the next tick without restarting host-runner. If the hub is briefly
// unreachable, the embedded YAML is used as a fallback so a transient
// network blip doesn't blank the host's capabilities.
func (a *Runner) probeLoop(ctx context.Context) {
	var lastHash string
	push := func() {
		caps := ProbeWithFamilies(ctx, a.fetchFamilies(ctx))
		caps.Host = a.hostInfo
		h := caps.Hash()
		if h == lastHash {
			return
		}
		if err := a.Client.PutCapabilities(ctx, a.HostID, caps); err != nil {
			a.Log.Warn("capability push failed", "err", err)
			return
		}
		lastHash = h
		a.Log.Info("capabilities published", "host", a.HostName, "hash", h[:12])
	}
	push()
	t := time.NewTicker(a.ProbeInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			push()
		}
	}
}

// fetchFamilies pulls the merged family list from the hub, falling back
// to the embedded YAML on error. Logged at warn — hub unreachable is the
// expected case during a network blip; a longer outage is visible from
// the heartbeat metric independently.
func (a *Runner) fetchFamilies(ctx context.Context) []agentfamilies.Family {
	hubFams, err := a.Client.ListAgentFamilies(ctx)
	if err != nil {
		a.Log.Warn("hub family fetch failed; using embedded fallback", "err", err)
		fams, _ := agentfamilies.All()
		return fams
	}
	out := make([]agentfamilies.Family, 0, len(hubFams))
	for _, f := range hubFams {
		incompat := make([]agentfamilies.Incompat, 0, len(f.Incompatibilities))
		for _, ic := range f.Incompatibilities {
			incompat = append(incompat, agentfamilies.Incompat{
				Mode: ic.Mode, Billing: ic.Billing, Reason: ic.Reason,
			})
		}
		out = append(out, agentfamilies.Family{
			Family:            f.Family,
			Bin:               f.Bin,
			VersionFlag:       f.VersionFlag,
			Supports:          f.Supports,
			Incompatibilities: incompat,
			DefaultAuthMethod: f.DefaultAuthMethod,
		})
	}
	return out
}

// stopTailer tears down marker forwarding for an agent that has left the
// running set (terminated, stale, or reassigned to another host). Callers
// must hold no lock — Stop is idempotent.
func (a *Runner) stopTailer(agentID string) {
	t, ok := a.tailers[agentID]
	if !ok {
		return
	}
	t.Stop()
	delete(a.tailers, agentID)
}

// stopDriver tears down the M4 driver (or whichever mode is wired) for an
// agent leaving the running set. Emits lifecycle.stopped as a side effect.
func (a *Runner) stopDriver(agentID string) {
	d, ok := a.drivers[agentID]
	if !ok {
		return
	}
	// Detach the input router first so no more Input calls land on a
	// driver that's about to Stop. Detach blocks until the router's
	// goroutine drains, which is the ordering we want.
	if a.inputs != nil {
		a.inputs.Detach(agentID)
	}
	d.Stop()
	delete(a.drivers, agentID)
}

// a2aSource adapts ListRunningAgents into the a2a.AgentSource callback.
// Called once per A2A HTTP request, so agent churn surfaces immediately.
// Skills come from the agent's template (ag.Kind → templates index) so the
// runner stays domain-agnostic; adding a new agent kind is a YAML drop.
func (a *Runner) a2aSource(ctx context.Context) ([]a2a.AgentInfo, error) {
	agents, err := a.Client.ListRunningAgents(ctx, a.HostID)
	if err != nil {
		return nil, err
	}
	out := make([]a2a.AgentInfo, 0, len(agents))
	for _, ag := range agents {
		out = append(out, a2a.AgentInfo{
			ID:     ag.ID,
			Handle: ag.Handle,
			Skills: a.templates.Skills(ag.Kind),
		})
	}
	return out, nil
}

// a2aDirectoryLoop pushes this host's agent cards to the hub directory
// on startup and every A2ADirectoryInterval thereafter. Change-detected
// by payload hash so idle hosts don't generate write amplification.
func (a *Runner) a2aDirectoryLoop(ctx context.Context) {
	var lastHash string
	push := func() {
		cards, err := a.buildA2ACards(ctx)
		if err != nil {
			a.Log.Warn("a2a directory build failed", "err", err)
			return
		}
		h := hashCards(cards)
		if h == lastHash {
			return
		}
		if err := a.Client.PutA2ACards(ctx, a.HostID, cards); err != nil {
			a.Log.Warn("a2a directory push failed", "err", err)
			return
		}
		lastHash = h
		a.Log.Info("a2a cards published", "count", len(cards), "hash", h[:12])
	}
	push()
	t := time.NewTicker(a.A2ADirectoryInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			push()
		}
	}
}

// buildA2ACards constructs A2A card entries for every running agent on this
// host. The card URL uses A2APublicURL when set; otherwise it falls back to
// the bind address, which is OK for hub-local tests and wrong for NAT'd
// hosts — the hub-side relay will rewrite it regardless.
func (a *Runner) buildA2ACards(ctx context.Context) ([]A2ACardEntry, error) {
	agents, err := a.Client.ListRunningAgents(ctx, a.HostID)
	if err != nil {
		return nil, err
	}
	base := a.A2APublicURL
	if base == "" {
		base = "http://" + a.A2AAddr
	}
	base = strings.TrimRight(base, "/")
	out := make([]A2ACardEntry, 0, len(agents))
	for _, ag := range agents {
		card := a2a.AgentCard{
			ProtocolVersion:    a2a.ProtocolVersion,
			Name:               ag.Handle,
			URL:                fmt.Sprintf("%s/a2a/%s", base, ag.ID),
			Version:            "1.0.0",
			Capabilities:       a2a.Capabilities{Streaming: false},
			DefaultInputModes:  []string{"text/plain"},
			DefaultOutputModes: []string{"text/plain"},
			Skills:             a.templates.Skills(ag.Kind),
		}
		body, err := json.Marshal(card)
		if err != nil {
			return nil, err
		}
		out = append(out, A2ACardEntry{
			AgentID: ag.ID,
			Handle:  ag.Handle,
			Card:    json.RawMessage(body),
		})
	}
	return out, nil
}

func hashCards(cards []A2ACardEntry) string {
	h := sha256.New()
	for _, c := range cards {
		_, _ = h.Write([]byte(c.AgentID))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(c.Handle))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(c.Card)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
