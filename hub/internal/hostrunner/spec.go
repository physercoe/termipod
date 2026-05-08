package hostrunner

import "gopkg.in/yaml.v3"

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
