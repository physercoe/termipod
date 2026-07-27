package hostrunner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// setup_script.go — host-runner side of the env-profiles plan (E1c). When an
// attached env profile carries a setup_script, the hub materializes it into the
// spawn_spec_yaml (SpawnSpec.SetupScript); here we run it once in the workdir,
// after the env exports and before the agent cmd.
//
// Why a file, not inline: the script is arbitrary multi-line bash, and the
// launch command string travels three different ways — `bash -c` (M1/M2), tmux
// `new-window <cmd>` → `sh -c` (M4), and potentially tmux send-keys (typed). A
// script inlined into that string would need to survive all three without a
// stray newline or quote breaking the framing. Writing the script to a temp
// file and putting only a newline-free `bash '<path>' && ` fragment in the
// command string sidesteps every escaping hazard — the fragment is plain POSIX
// sh, safe under `bash -c`, `sh -c`, and send-keys alike.
//
// The script runs as a subshell (`bash <file>`), so vars it exports do NOT
// propagate to the agent (EnvVars is the channel for that); a `set -e` / exit
// inside it can't kill the launch shell. It runs with cwd = the workdir (the
// preceding `cd` in the command), so relative paths resolve against the repo.

// setupScriptPrefix writes the spec's setup_script to a temp file (0700) and
// returns a command fragment to splice between the env exports and the agent
// cmd, plus the file path (for callers — e.g. the gemini exec path — that run
// the script themselves rather than through the shared shell string).
//
//   - empty script            → "", "", nil (no-op; caller splices nothing)
//   - failurePolicy "continue" → `{ bash '<path>' || true; } && ` (agent runs
//     even if setup fails)
//   - otherwise (fail-closed)  → `bash '<path>' && ` (setup non-zero → agent
//     never starts; the `&&` short-circuits)
//
// The temp file is intentionally left in place: the launch is fire-and-forget
// (the tmux pane / child reads it asynchronously), the file is tiny, and it
// lives in the OS temp dir (never the workdir, so a git worktree can't pick it
// up). tag identifies the owning agent for debuggability.
func setupScriptPrefix(script, failurePolicy, tag string) (prefix, path string, err error) {
	if script == "" {
		return "", "", nil
	}
	path, err = writeSetupScriptFile(script, tag)
	if err != nil {
		return "", "", err
	}
	run := "bash " + shellEscape(path)
	if failurePolicy == "continue" {
		// Ignore setup failure but still guard the following `&&` chain.
		return "{ " + run + " || true; } && ", path, nil
	}
	return run + " && ", path, nil
}

// writeSetupScriptFile materializes the script into a private temp file. Mode
// 0600 (0700 dir default) — the launch shell reads it as the same host user, no
// one else needs it.
func writeSetupScriptFile(script, tag string) (string, error) {
	f, err := os.CreateTemp("", "termipod-setup-"+sanitizeTempTag(tag)+"-*.sh")
	if err != nil {
		return "", fmt.Errorf("create setup script temp: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(script); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("write setup script: %w", err)
	}
	return f.Name(), nil
}

// runSetupScriptOnce writes and executes the setup script synchronously, for
// launch paths with no persistent shell to splice a fragment into (the
// gemini-cli exec-per-turn driver). Runs `bash <file>` with cwd = workdir and
// the given env. A non-zero exit is an error only when failurePolicy is not
// "continue" (fail-closed default). Empty script is a no-op.
func runSetupScriptOnce(ctx context.Context, script, failurePolicy, workdir string, env []string, tag string) error {
	if script == "" {
		return nil
	}
	path, err := writeSetupScriptFile(script, tag)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "bash", path)
	cmd.Dir = workdir
	cmd.Env = env
	if runErr := cmd.Run(); runErr != nil {
		if failurePolicy == "continue" {
			return nil
		}
		return fmt.Errorf("setup script failed: %w", runErr)
	}
	return nil
}

// sanitizeTempTag keeps a temp-name fragment to a safe charset so an odd
// ChildID can't inject path separators or glob chars into the temp filename.
func sanitizeTempTag(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return "agent"
	}
	return string(out)
}
