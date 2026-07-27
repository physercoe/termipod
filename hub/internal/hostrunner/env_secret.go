package hostrunner

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/termipod/hub/internal/envseal"
)

// env_secret.go is the host side of ADR-056 D-5: unseal an env-profile secret
// envelope at launch and inject the values via REAL process environment only —
// never the command string, the spawn spec, temp files, or logs. The E1b
// envExportPrefix path (`export K=V && cmd`) is correct for hub-visible plain
// env_vars but FORBIDDEN for secrets: it lands values in `ps`, tmux scrollback,
// and the hub-stored spec. Secrets travel out-of-band: the child's Env slice
// (M1/M2/exec) or `tmux new-window -e K=V` (M4).

// resolveSecretEnv opens this spawn's env_secret_envelope (if any) with the
// host's private identity and returns the secrets as a sorted K=V slice ready
// to append to a process environment (secrets win, so callers append last).
//
// Fail-closed: a spawn that carries an envelope but cannot be unsealed (no host
// identity, wrong key/context, corrupt envelope) returns an error and the
// caller MUST refuse to launch — running the agent without the secrets it
// expects is worse than failing. The error is present/absent-grade and never
// contains secret values (ADR-056 D-4/D-5).
func (a *Runner) resolveSecretEnv(sp Spawn) ([]string, error) {
	if sp.EnvSecretEnvelope == "" {
		return nil, nil
	}
	if a.hostSeed == "" {
		return nil, fmt.Errorf("spawn %s carries env secrets but this host has no identity "+
			"(no state-dir?); cannot unseal", sp.Handle)
	}
	secrets, err := envseal.Open(sp.EnvSecretEnvelope, a.hostSeed, a.Client.Team, a.HostID)
	if err != nil {
		// envseal.Open already collapses every failure to a contents-free error.
		return nil, fmt.Errorf("unseal env secrets for %s: %w", sp.Handle, err)
	}
	return secretKVList(secrets), nil
}

// secretKVList renders a secret map as a sorted []string of "K=V", the shape
// exec.Cmd.Env and `tmux -e` both consume. Sorted for determinism (tests, and
// so a re-launch is byte-stable); no shell escaping — these never touch a shell.
func secretKVList(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}

// secretKeyNames returns just the sorted key names of a K=V slice, for
// present/absent-grade audit logging (ADR-056 D-5: key names, never values).
func secretKeyNames(kv []string) []string {
	names := make([]string, 0, len(kv))
	for _, e := range kv {
		if i := strings.IndexByte(e, '='); i >= 0 {
			names = append(names, e[:i])
		}
	}
	return names
}

// envLauncher is the optional Launcher capability to launch a command with
// extra process environment injected out-of-band (tmux -e), so secret values
// never appear in the command string. Only launchers that run the agent
// process itself in a pane (TmuxLauncher) need it; the M1/M2 tail-view panes
// carry no secrets. A launcher that lacks it is a hard error when secrets are
// present — never a silent drop (launchCmdWithEnv).
type envLauncher interface {
	LaunchCmdEnv(ctx context.Context, sp Spawn, cmd string, env []string) (string, error)
}

// launchCmdWithEnv launches cmd, injecting extraEnv as real process env when
// present. With no extra env it is the plain LaunchCmd. With secrets present it
// REQUIRES an envLauncher — a launcher that can't inject env out-of-band would
// otherwise force secrets into the command string, so it fails instead.
func launchCmdWithEnv(ctx context.Context, l Launcher, sp Spawn, cmd string, extraEnv []string) (string, error) {
	if len(extraEnv) == 0 {
		return l.LaunchCmd(ctx, sp, cmd)
	}
	el, ok := l.(envLauncher)
	if !ok {
		return "", fmt.Errorf("launcher %T cannot inject secret env out-of-band; "+
			"refusing to place secrets in the command string", l)
	}
	return el.LaunchCmdEnv(ctx, sp, cmd, extraEnv)
}
