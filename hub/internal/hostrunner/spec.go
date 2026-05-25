package hostrunner

import (
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
	// W1.1). When set on an ACP-capable spawn, ACPDriver calls
	// session/load with this id instead of session/new so the agent
	// reattaches to its prior conversation. Other launch paths (M2/M4)
	// ignore this field — claude's --resume flag is spliced directly
	// into backend.cmd, and gemini's exec-per-turn driver captures its
	// own cursor from per-turn init frames.
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

// DeriveWorkdir resolves the agent workdir from
// (defaultWorkdir, projectID, handle, childID). The returned path is
// tilde-prefixed when derived; callers expand `~` themselves via
// expandHome. Empty result means the legacy "no workdir, run from
// host-runner's cwd" path applies — preserved for back-compat with
// demo templates that ship without context_files / mcp_token.
//
// Precedence:
//
//  1. defaultWorkdir non-empty
//     The template's explicit `backend.default_workdir` wins.
//
//  2. projectID non-empty
//     Project-bound derivation: `~/hub-work/<pid[:8]>/<handle>` —
//     workers in the same project share a folder root, sibling
//     handles don't collide across projects (ADR-025 W6). The
//     8-char pid prefix matches how the hub prints project ids
//     elsewhere.
//
//  3. needsWorkdir is true (caller will materialise context_files
//     or mcp_token)
//     Project-less derivation: `~/hub-work/_team/<handle>` — gives
//     team-scoped stewards (codex/general/etc spawned outside any
//     project) a stable, per-handle workdir without forcing every
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
// always has a stable last segment.
func DeriveWorkdir(defaultWorkdir, projectID, handle, childID string, needsWorkdir bool) string {
	if defaultWorkdir != "" {
		return defaultWorkdir
	}
	h := handle
	if h == "" {
		h = childID
	}
	if projectID != "" {
		pid := projectID
		if len(pid) > 8 {
			pid = pid[:8]
		}
		return filepath.Join("~", "hub-work", pid, h)
	}
	if needsWorkdir {
		// Tolerate a missing handle/childID by leaving the workdir
		// empty rather than collapsing every project-less spawn into
		// the same `~/hub-work/_team` directory. The caller's
		// writeContextFiles/writeMCPConfig guard will then surface
		// the misconfiguration cleanly.
		if h == "" {
			return ""
		}
		return filepath.Join("~", "hub-work", "_team", h)
	}
	return ""
}
