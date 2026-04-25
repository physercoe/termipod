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
