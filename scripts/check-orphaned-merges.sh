#!/usr/bin/env bash
# check-orphaned-merges.sh — find PRs GitHub calls "merged" whose content
# never reached main.
#
# Why: a **stacked** PR is opened against its parent branch, not main. When
# the parent is merged to main FIRST, the child's later merge lands in a
# branch that main has already stopped listening to. GitHub is not lying —
# the PR *was* merged, into its base — but the code is nowhere in the
# trunk, the PR page is green, the issue checkbox is ticked, and every
# later wedge is built on the assumption that it shipped.
#
# 2026-08-04: three W1 wedges were lost this way, each by under a minute.
#   #504 (B1/B2 -> main)                     merged 03:06:16Z
#   #506 (B6   -> coworking-b-live-apply)    merged 03:06:46Z  — 30s late
#   #508 (lane A -> coworking-b6-...-ring)   merged 03:07:16Z
#   #501 (R1   -> main)                      merged 03:16:53Z
#   #502 (L1   -> vision-r1-approval-cards)  merged 03:17:27Z  — 34s late
# The whole `author_read`/`author_apply` bridge and the AgentEventSource
# abstraction were absent from main while three plans recorded them as
# shipped.
#
# This is a MAINTAINER tool, not a CI lint: it needs network + `gh`, and it
# reports on repository state that an individual PR author neither caused
# nor can fix. Run it after a merge wave — the moment the assumption
# "everything merged is on main" starts carrying weight.
#
# Usage:
#   scripts/check-orphaned-merges.sh [limit]     # default: last 40 merged PRs
#
# Exit 0 when every merged PR's content is on main; 1 when one is not, or
# when one cannot be decided mechanically (see the limit below).

set -u
cd "$(dirname "$0")/.."

LIMIT="${1:-40}"
BASE_REF="origin/main"

if ! command -v gh >/dev/null 2>&1; then
  echo "FAIL: gh CLI not on PATH (this check needs the GitHub API)" >&2
  exit 1
fi

git fetch origin main --quiet 2>/dev/null || true

orphaned=0
undecidable=0

# Only PRs whose base was NOT main can be orphaned — one merged directly to
# main is on main by construction.
while IFS=$'\t' read -r num base oid title; do
  [ -z "${num:-}" ] && continue
  [ "$base" = "main" ] && continue

  # The squash commit itself being an ancestor settles it immediately. It
  # usually is NOT, even for content that did land: when the parent branch
  # is squashed onto main the content arrives under a different sha. So a
  # negative here is a question, not yet an answer.
  if git merge-base --is-ancestor "$oid" "$BASE_REF" 2>/dev/null; then
    continue
  fi

  # Decide by content. Files the PR ADDED are the reliable probe: if the
  # work reached main by any route, its new files are there under their own
  # names. Renames later in history would show up as a false alarm, which
  # is the safe direction for a check nobody runs automatically.
  #
  # `--name-only` (not `--stat`) because --stat elides long paths to
  # `.../tail.go` and every elided path then reads as missing.
  added="$(git show --format= --name-only --diff-filter=A "$oid" 2>/dev/null || true)"

  if [ -z "$added" ]; then
    # A modify-only stacked PR leaves no new filename to look for, and
    # diffing its hunks against a trunk that has moved on is not something
    # this script can honestly decide. Say so rather than pass it.
    echo "?? PR #$num (base: $base) — modify-only, verify by hand: $title"
    undecidable=$((undecidable + 1))
    continue
  fi

  missing=""
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    git cat-file -e "$BASE_REF:$f" 2>/dev/null && continue
    # Absent from main has two causes and only one is a bug. A file that
    # arrived and was later DELETED on purpose leaves a deletion in main's
    # history; a file that never arrived leaves nothing. Without this the
    # check reports #488 forever, because the node_modules symlinks it
    # added were deliberately untracked by #491/#492 (the incident behind
    # lint-no-tracked-node-modules.sh) — and a check that cries wolf is a
    # check nobody reads.
    if [ -n "$(git log --diff-filter=D --format=%h -1 "$BASE_REF" -- "$f" 2>/dev/null)" ]; then
      continue
    fi
    missing="$missing  $f"$'\n'
  done <<<"$added"

  if [ -n "$missing" ]; then
    echo "!! PR #$num (base: $base) is NOT on main: $title"
    printf '%s' "$missing"
    orphaned=$((orphaned + 1))
  fi
done < <(gh pr list --state merged --limit "$LIMIT" \
  --json number,baseRefName,mergeCommit,title \
  --jq '.[] | [.number, .baseRefName, .mergeCommit.oid, .title] | @tsv')

if [ "$orphaned" -gt 0 ] || [ "$undecidable" -gt 0 ]; then
  echo >&2
  echo "check-orphaned-merges: $orphaned orphaned, $undecidable undecidable (of last $LIMIT merged)" >&2
  echo >&2
  echo "To recover one: branch from main, cherry-pick the merge commit, resolve" >&2
  echo "against the trunk it never saw, and open a restore PR against main." >&2
  echo "To prevent one: retarget a stacked PR to main BEFORE merging its parent." >&2
  exit 1
fi

echo "check-orphaned-merges: clean (last $LIMIT merged PRs all reached main)"
