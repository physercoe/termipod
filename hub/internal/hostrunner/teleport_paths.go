package hostrunner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Heterogeneous-host path portability for teleport (ADR-057 D-6, T1). Two hosts
// need not share a home directory or an absolute layout: a worktree at
// `/home/alice/hub-work/…` on the source may need to become
// `/data/agents/hub-work/…` on the target. Teleport carries the source's
// host-owned paths in a **home-relative** form (the same `~/…` convention
// DeriveWorkdir already uses) and the target re-anchors them against its own
// $HOME. Paths that don't live under the source's home can't be remapped
// portably (an operator-pinned absolute outside home) — they travel verbatim and
// fail loudly on the target if it lacks that exact path, which is the
// documented T1 edge (a per-host path registry is T3).

// homeRelativePath rewrites an absolute path that lives under `home` into the
// portable `~/<rel>` form. A path not under home (or an already-relative path)
// is returned unchanged — the caller then sends it verbatim, accepting that a
// heterogeneous target may not have it. `home` is passed explicitly (not
// os.UserHomeDir) so the conversion is deterministic and testable.
func homeRelativePath(abs, home string) string {
	if home == "" || !filepath.IsAbs(abs) {
		return abs
	}
	cleanHome := filepath.Clean(home)
	cleanAbs := filepath.Clean(abs)
	if cleanAbs == cleanHome {
		return "~"
	}
	prefix := cleanHome + string(filepath.Separator)
	if !strings.HasPrefix(cleanAbs, prefix) {
		return abs // not under home — not portably remappable
	}
	rel := strings.TrimPrefix(cleanAbs, prefix)
	return "~/" + rel
}

// expandHomeWith is the home-injectable form of expandHome (launch_m2.go): it
// resolves a leading `~`/`~/` against the supplied home rather than
// os.UserHomeDir(), so the teleport target re-anchors a portable path against
// ITS home. A non-tilde path is returned unchanged.
func expandHomeWith(p, home string) (string, error) {
	if p == "" || p[0] != '~' {
		return p, nil
	}
	if len(p) > 1 && p[1] != '/' {
		return p, nil // `~user` unsupported — leave as-is
	}
	if home == "" {
		return "", fmt.Errorf("teleport: target HOME unresolved for %q", p)
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}

// targetHome resolves the home the teleport target re-anchors portable paths
// against. Overridable via a test/host hook; production uses os.UserHomeDir.
func targetHome() (string, error) {
	return os.UserHomeDir()
}
