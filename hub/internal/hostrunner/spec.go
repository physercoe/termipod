package hostrunner

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SpawnSpec is the subset of spawn_spec_yaml host-runner needs to wire up a
// pane. The canonical schema lives in the plan doc; we only decode the keys
// we actually use here to stay forward-compatible with future fields.
type SpawnSpec struct {
	// ProjectID / ChannelID bind the agent to a channel so that tap-script
	// markers (<<mcp:post_message …>>) can be forwarded as hub events without
	// the tap needing its own MCP token.
	ProjectID string `yaml:"project_id"`
	ChannelID string `yaml:"channel_id"`

	// FallbackModes is the template's preferred runtime fallback ladder.
	// The hub-side mode resolver consumes this list at spawn-creation time
	// to pick a single primary mode; host-runner re-parses it so a launch
	// failure (M1 ACP handshake, M2 stdio start) can drop one rung at a
	// time instead of straight to M4. Honored only when entries name modes
	// the host actually supports — invalid entries are silently skipped.
	FallbackModes []string `yaml:"fallback_modes"`

	// Backend is a loose catch-all for launcher hints; TmuxLauncher reads
	// `backend.cmd` when present, falling back to its DefaultCmd otherwise.
	// DefaultWorkdir, when set, is the cwd the M2 launcher cd's into before
	// spawning the agent process. Leading `~` is expanded against $HOME.
	Backend struct {
		Cmd            string `yaml:"cmd"`
		DefaultWorkdir string `yaml:"default_workdir"`
	} `yaml:"backend"`

	// Worktree optionally requests a git worktree before the pane launches.
	// Repo is the source checkout; Branch is the branch name (created if
	// absent); Base is the starting point for a new branch (default HEAD).
	// Path comes from the Spawn itself (WorktreePath) — kept out of the YAML
	// so the hub can pre-validate uniqueness.
	Worktree struct {
		Repo   string `yaml:"repo"`
		Branch string `yaml:"branch"`
		Base   string `yaml:"base"`
	} `yaml:"worktree"`

	// ContextFiles is a map of relative-filename → file contents that the
	// launcher writes into the agent's workdir before spawning. The hub
	// inlines CLAUDE.md (resolved from the template's `prompt:` field)
	// here so Claude Code sees its persona on startup. Keys must be
	// simple filenames or shallow relative paths — leading `/`, `..`, or
	// absolute paths are rejected by the launcher.
	ContextFiles map[string]string `yaml:"context_files"`

	// ResumeSessionID is the engine-side cursor captured from a prior
	// session.init event (sessions.engine_session_id, ADR-014 + ADR-021
	// W1.1). Two driver paths consume it via the same field:
	//   - ACPDriver (gemini-cli, kimi-code): calls session/load with
	//     this id instead of session/new so the daemon reattaches to
	//     its prior conversation.
	//   - AppServerDriver (codex): converts this into the
	//     `thread/resume` JSON-RPC method's `threadId` param so codex
	//     reattaches to its prior thread (upstream `codex-rs/
	//     app-server-protocol/src/protocol/common.rs:457`). v1.0.716.
	// claude-code's --resume flag is spliced directly into backend.cmd
	// via spliceClaudeResume (different YAML site). antigravity uses
	// --conversation similarly via spliceAntigravityResume.
	ResumeSessionID string `yaml:"resume_session_id"`

	// AuthMethod is the steward-template-declared override for the ACP
	// `authenticate` method id (ADR-021 D3 / W1.4). Empty falls through
	// to the family default (Family.DefaultAuthMethod) and finally to
	// the first non-interactive method in the agent's `authMethods`
	// list. Service-account / shared-host deployments override this
	// (e.g. `auth_method: gemini-api-key`) when the family default
	// (oauth-personal for gemini-cli) doesn't match how the host caches
	// credentials.
	AuthMethod string `yaml:"auth_method"`
}

// ParseSpec tolerates empty input and returns a zero-valued spec so callers
// can treat "no YAML" and "YAML with no fields we care about" identically.
func ParseSpec(yamlText string) (SpawnSpec, error) {
	var s SpawnSpec
	if yamlText == "" {
		return s, nil
	}
	if err := yaml.Unmarshal([]byte(yamlText), &s); err != nil {
		return s, err
	}
	return s, nil
}

// teamWorkRoot is the per-team root under which every *derived* agent
// workdir lives: `~/hub-work/<team>`. The team segment is the on-host
// isolation boundary (ADR-037 D6) — two teams sharing a host never
// share a mutable workdir subtree. An empty team yields the legacy
// `~/hub-work` root, preserved for back-compat with demo spawns and
// any caller that doesn't carry a team. The host-runner is a
// single-team process (`--team`), so the team threads in from
// `Client.Team` at the launch call sites.
func teamWorkRoot(team string) string {
	if team == "" {
		return filepath.Join("~", "hub-work")
	}
	return filepath.Join("~", "hub-work", team)
}

// ensureTeamWorkRoot creates `~/hub-work/<team>` with 0o700 so the OS
// denies cross-team reads at the filesystem layer (ADR-037 D6 MVP
// guard). It returns the expanded absolute team root. An empty team is
// a no-op (legacy spawns) returning "".
//
// 0o700 is the concrete, OS-enforced guard *when teams run under
// distinct OS users* (the per-team-user deployment); under a single
// shared uid it still walls the fleet's work area off from other OS
// users on the box, and lays the path a per-team-user spawn keys on.
// True cross-team isolation under one uid (per-team OS users / sandbox)
// is the documented hardening follow-up — ADR-037 D6 residual risk.
//
// Inner project/handle dirs are created 0o755 by the launchers; the
// 0o700 team root above them is the traversal boundary, so the looser
// inner perms don't widen access.
func ensureTeamWorkRoot(team string) (string, error) {
	if team == "" {
		return "", nil
	}
	root, err := expandHome(teamWorkRoot(team))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return root, nil
}

// DeriveWorkdir resolves the agent workdir from
// (team, defaultWorkdir, projectID, handle, childID). The returned path
// is tilde-prefixed when derived; callers expand `~` themselves via
// expandHome. Empty result means the legacy "no workdir, run from
// host-runner's cwd" path applies — preserved for back-compat with
// demo templates that ship without context_files / mcp_token.
//
// Precedence:
//
//  1. defaultWorkdir non-empty
//     The template's explicit `backend.default_workdir` wins. This is
//     an operator-pinned absolute path, so the team segment is NOT
//     injected — the operator chose it deliberately.
//
//  2. projectID non-empty
//     Project-bound derivation: `~/hub-work/<team>/<pid[:8]>/<handle>`
//     — workers in the same project share a folder root, sibling
//     handles don't collide across projects (ADR-025 W6), and the
//     team segment (ADR-037 D6) keeps two teams off each other's
//     subtree on a shared host. The 8-char pid prefix matches how the
//     hub prints project ids elsewhere.
//
//  3. needsWorkdir is true (caller will materialise context_files
//     or mcp_token)
//     Project-less derivation: `~/hub-work/<team>/_team/<handle>` —
//     gives team-scoped stewards (codex/general/etc spawned outside
//     any project) a stable, per-handle workdir without forcing every
//     such template to ship an explicit `default_workdir`. The
//     underscore-prefixed `_team` namespace avoids collision with
//     real 8-char project ids (which are hex, never start with `_`).
//     Pre-fix, this case fell through to the empty-workdir branch
//     below and then errored out at the writeContextFiles /
//     writeMCPConfig guard — the codex M2 smoke-failure reported
//     on v1.0.709.
//
//  4. neither defaultWorkdir nor projectID, needsWorkdir false
//     Empty — legacy single-host demo path. Agent runs from
//     host-runner's cwd; no `cd` is prefixed to the cmd.
//
// `handle` falls back to `childID` when empty so the derived path
// always has a stable last segment. An empty `team` collapses the
// team segment (legacy `~/hub-work/…`), so pre-W5 callers and demo
// spawns keep their old paths.
func DeriveWorkdir(team, defaultWorkdir, projectID, handle, childID string, needsWorkdir bool) string {
	if defaultWorkdir != "" {
		return defaultWorkdir
	}
	root := teamWorkRoot(team)
	h := handle
	if h == "" {
		h = childID
	}
	if projectID != "" {
		pid := projectID
		if len(pid) > 8 {
			pid = pid[:8]
		}
		return filepath.Join(root, pid, h)
	}
	if needsWorkdir {
		// Tolerate a missing handle/childID by leaving the workdir
		// empty rather than collapsing every project-less spawn into
		// the same `_team` directory. The caller's
		// writeContextFiles/writeMCPConfig guard will then surface
		// the misconfiguration cleanly.
		if h == "" {
			return ""
		}
		return filepath.Join(root, "_team", h)
	}
	return ""
}
