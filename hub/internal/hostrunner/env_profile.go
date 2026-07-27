package hostrunner

import (
	"regexp"
	"sort"
	"strings"
)

// env_profile.go — host-runner side of the env-profiles plan (E1). The hub
// resolves an attached env_profiles row and materializes its plain env_vars
// into the spawn_spec_yaml (SpawnSpec.EnvVars); here we turn those into an
// `export …` clause spliced into the launch command so the agent process — and
// (from E1c) any setup script — start with the profile's environment.
//
// Injected at the single shared launch chokepoint every driving mode uses:
// `cd <workdir> && <cmd>` becomes `cd <workdir> && export … && <cmd>` (M1, M2
// persistent, and the three M4 tmux-pane launchers). The gemini-cli
// exec-per-turn path carries the same vars through the driver's Env slice
// instead — it never reaches the shared string.

// envVarNameRe mirrors the hub-side validator (handlers_env_profiles.go): a
// portable POSIX environment-variable name. The hub already rejects malformed
// keys at the CRUD boundary; we re-check here so a hand-crafted spawn_spec_yaml
// (or a future non-hub producer) can never inject a shell metacharacter through
// a bare, unescaped key.
var envVarNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// envExportPrefix builds a `export K=V … && ` clause that sets the profile's
// env_vars in the launch shell. Returns "" when there is nothing to export, so
// callers can splice it unconditionally without altering the command otherwise.
//
// Keys are emitted in sorted order (deterministic command string → stable logs
// and reproducible tests). Values are shell-escaped; keys that don't match the
// POSIX name shape are dropped defensively (they can't have come from the hub,
// which validates, so dropping is the safe response to a tampered spec).
func envExportPrefix(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if !envVarNameRe.MatchString(k) {
			continue
		}
		parts = append(parts, k+"="+shellEscape(env[k]))
	}
	if len(parts) == 0 {
		return ""
	}
	return "export " + strings.Join(parts, " ") + " && "
}

// envKVList renders the profile's env_vars as a sorted []string of `K=V`
// entries, the shape exec.Cmd.Env / driver Env slices want. Used by the
// gemini-cli exec-per-turn path, which sets env on the process rather than
// through a shell `export`. Malformed keys are dropped, matching
// envExportPrefix. Values are NOT shell-escaped here — exec.Cmd.Env passes each
// entry verbatim to the child, no shell in between.
func envKVList(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if !envVarNameRe.MatchString(k) {
			continue
		}
		out = append(out, k+"="+env[k])
	}
	return out
}
