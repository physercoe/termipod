#!/usr/bin/env bash
# lint-no-tracked-node-modules.sh — fail if ANY node_modules path is tracked.
#
# Why: #491 — a commit landed two symlinks (mode 120000) at
# desktop/node_modules and desktop/electron/node_modules pointing at an
# absolute scratch path on the committer's machine. The normal
# node_modules ignore rules do NOT stop a tracked entry at that exact
# path, and the blast radius was real:
#   - `git pull` replaced populated node_modules directories with the
#     dangling symlink (installed deps silently gone, builds broke);
#   - worktrees with a real node_modules directory could not check out
#     main at all ("untracked working tree files would be overwritten").
#
# There is no legitimate reason to track anything under a node_modules
# directory — not a symlink, not a vendored file (vendoring belongs in a
# deliberate location with a deliberate name). So this is a hard ban, not
# a ratchet: any tracked path containing a node_modules segment fails.
#
# Usage:
#   scripts/lint-no-tracked-node-modules.sh   # check (CI mode)
#
# Exit 0 clean; 1 with the offending paths listed.

set -u
cd "$(dirname "$0")/.."

# git ls-files lists every tracked path, symlinks included. Match
# node_modules as a full path segment (start/end anchored), so
# `desktop/node_modules`, `desktop/electron/node_modules`, or anything
# nested beneath such a directory all hit, while a path merely containing
# the substring (e.g. scripts/rebuild-node_modules.sh) does not.
hits="$(git ls-files | grep -E '(^|/)node_modules(/|$)' || true)"

if [ -n "$hits" ]; then
  echo "FAIL: tracked node_modules path(s) detected:" >&2
  echo "$hits" | sed 's/^/  /' >&2
  echo >&2
  echo "Nothing under a node_modules directory may be tracked — remove with:" >&2
  echo "  git rm <path>" >&2
  echo "(See #491 for the incident this guards against.)" >&2
  exit 1
fi

echo "lint-no-tracked-node-modules: clean"
