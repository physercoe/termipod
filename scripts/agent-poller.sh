#!/usr/bin/env bash
#
# agent-poller.sh — autonomous builder loop for the multi-agent protocol.
#
# A host-side poller that lets ANY builder agent work the GitHub ticket queue
# without a human typing a prompt each time. It does the cheap, deterministic
# GitHub orchestration in bash and drives the FULL ticket lifecycle: claim a
# ready ticket → hand the agent a build prompt → on a ticket:changes bounce,
# hand the agent a feedback-round prompt to fix the same PR in place → wait
# while in review → and only take new work once the PR is gone (merged). So the
# expensive agent only spends tokens on the actual implementation + revisions.
#
# It is VENDOR-AGNOSTIC by design (ADR-049 D-9): no model or CLI name appears
# here. You plug in your agent via $AGENT_CMD. A "builder" is a RUNTIME + a
# MODEL — e.g. a CLI that is its own runtime, or a generic agent runtime driving
# a cheap model through a compatible API. The poller does not care which.
#
# See: docs/how-to/agent-collaboration.md, AGENTS.md, ADR-049.
#
# ---------------------------------------------------------------------------
# Configuration (environment variables)
# ---------------------------------------------------------------------------
#   AGENT_HANDLE   (required) Your attribution handle, e.g. "builder-1".
#                  Should match `git config user.name`. Used in the branch
#                  name (agent/<handle>/<N>-...) and the claim comment — the
#                  protocol's source of truth for who holds a ticket.
#
#   AGENT_CMD      (required) The command that runs ONE headless agent session.
#                  The ticket prompt is provided BOTH on stdin AND in the file
#                  $PROMPT_FILE (env var, exported before AGENT_CMD runs). It is
#                  evaluated with `eval`, so you may reference $PROMPT_FILE,
#                  $TICKET_NUMBER, $TICKET_SLUG, and $BRANCH. Examples:
#
#                    # a CLI that is its own runtime, reads the prompt as an arg:
#                    AGENT_CMD='<agent-cli> exec "$(cat "$PROMPT_FILE")"'
#
#                    # a generic agent runtime in non-interactive/headless mode,
#                    # pointed at a cheap model via its provider env vars
#                    # (set those in the same shell, NOT here):
#                    AGENT_CMD='<runtime> -p "$(cat "$PROMPT_FILE")" --headless-no-prompt-flag'
#
#                  NOTE — sandboxed runtimes. A builder must edit files, run
#                  scripts, and reach the network (git push, gh). If your
#                  runtime sandboxes command execution (e.g. a per-command
#                  bubblewrap/seccomp jail), it can fail before startup on
#                  restricted hosts — a telltale is a network-namespace error
#                  like "bwrap: loopback: Failed RTM_NEWADDR: Operation not
#                  permitted". On a TRUSTED builder host, pass the runtime's
#                  own "bypass sandbox + auto-approve" flag in AGENT_CMD (this
#                  poller never sandboxes anything itself — it just runs
#                  AGENT_CMD). The flag name is runtime-specific; check the
#                  runtime's `--help`. Run the builder as a NON-root user.
#
#   AGENT_INTERACTIVE_CMD
#                  (required only for --interactive take-over mode) The command
#                  that launches the runtime's INTERACTIVE TUI seeded with the
#                  ticket prompt. Pass the prompt as an ARGUMENT (not stdin) so
#                  the TTY stays free for you to type — e.g.
#                    AGENT_INTERACTIVE_CMD='<agent-cli> "$(cat "$PROMPT_FILE")"'
#                  Do NOT add a headless/-p/exec flag here; this one is meant to
#                  drop you into the live agent so you can take over.
#
#   TMUX_SESSION   (optional) tmux session name for --interactive runs.
#                  Default: agent-<handle>. One window per ticket (t<N>).
#
#   AGENT_TIERS    (optional) Comma list of tiers this builder is cleared for,
#                  in preference order. Default: "mechanical".
#                  e.g. "mechanical" or "mechanical,medium".
#
#   POLL_INTERVAL  (optional) Idle gap between queue checks when there is no
#                  work. Accepts a unit suffix — 5m, 15m, 2h — or bare seconds
#                  (300 == 300s). Default 120 (2m). NOTE this is the gap BETWEEN
#                  tickets: while the agent is running a ticket the poller blocks
#                  on it, so the interval only applies once the queue is idle.
#
#   LOG_DIR        (optional) Directory for a per-ticket transcript of the
#                  agent's output (also streamed live to the terminal). Default
#                  $TMPDIR/agent-poller-logs. Read these when a run blocks/hangs.
#
#   REPO           (optional) owner/name. Default: the repo `gh` infers here.
#
# ---------------------------------------------------------------------------
# Flags
# ---------------------------------------------------------------------------
#   --once        Do a single iteration (claim + run at most one ticket) and exit.
#   --dry-run     Print what it would do; never relabel, comment, branch, or run
#                 the agent. Safe for a first look at the queue.
#   --supervised  Ask for y/N confirmation before claiming + running each ticket,
#                 so you can watch and step in before every run. Needs a TTY.
#   --interactive Take-over mode: claim the ticket, then launch the runtime's
#                 INTERACTIVE TUI (AGENT_INTERACTIVE_CMD) seeded with the prompt
#                 inside a tmux session on this host. Attach to watch AND type
#                 into the agent; the poller blocks until the agent exits, then
#                 moves on. Needs tmux + AGENT_INTERACTIVE_CMD.
#   --warm        (alias --stay) Single warm session per ticket: hand the agent a
#   --stay        prompt that tells it to open the PR and then STAY — watch its
#                 own PR, service ticket:changes rounds IN THE SAME session
#                 (warm context, no cold re-load), and exit only when the PR
#                 merges. The poller blocks on that one long agent run for the
#                 whole lifecycle. Needs a wait/monitor-capable agent CLI (it
#                 sleeps between PR polls). The cold default (no flag) instead
#                 runs a fresh agent session per round — cheaper + crash-resilient
#                 but re-loads context each round. (If a warm agent dies leaving
#                 an open PR, the normal per-round dispatch recovers it.)
#                 Incompatible with --interactive.
#
# ---------------------------------------------------------------------------
# Guarantees / safety
# ---------------------------------------------------------------------------
#   * One-in-flight: the agent runs in the FOREGROUND, so the loop naturally
#     serializes — a poller process works at most one ticket at a time.
#   * Full ticket lifecycle: a builder carries ONE ticket from claim → PR →
#     review → merge before taking anything new. Each pass, if this handle has
#     an OPEN PR (branch agent/<handle>/*), it dispatches on the ticket state:
#       - ticket:changes  → run the agent on the EXISTING branch to address the
#         maintainer's review (a feedback round — fix in place, do not branch
#         fresh, do not claim new work). The agent flips it back to
#         ticket:in-review when it has pushed the fix.
#       - ticket:blocked  → the agent escalated a judgment call; WAIT (re-running
#         would re-hit the same wall). The maintainer resolves it by flipping to
#         ticket:changes (re-engage the builder) or ticket:in-review.
#       - otherwise (in-review) → wait; the ball is in the maintainer's court.
#     Only with NO open PR does it claim the next ready ticket. The loop never
#     deadlocks — at worst it idle-waits on a state only the maintainer can move.
#   * Cold (default) vs --warm: cold runs a fresh agent session per round; --warm
#     keeps ONE session alive across the whole lifecycle (see Flags). Either way
#     the per-round dispatch above holds — under --warm it also recovers a warm
#     agent that died leaving an open PR.
#   * Never merges. Merging is the maintainer's sole action (ADR-049 D-7).
#   * Baton: this poller does NOT manage the holds:arb baton itself — the agent
#     does, per AGENTS.md §6 (it checks the baton before opening an ARB PR).
#     Foreground serialization keeps a single host to one in-flight ticket;
#     cross-host ARB safety still rides on the agent's baton check.
#
# ---------------------------------------------------------------------------
# Watching live / intervening
# ---------------------------------------------------------------------------
#   The agent runs in the FOREGROUND, so its output streams to this terminal
#   live (and to a per-ticket file under $LOG_DIR). To watch and be able to
#   step in:
#     * Run the poller inside a terminal multiplexer on the BUILDER host, e.g.
#         tmux new -s builder 'POLL_INTERVAL=5m bash scripts/agent-poller.sh'
#       then `tmux attach -t builder` to watch, Ctrl-b d to detach (it keeps
#       running), and Ctrl-C in the pane to abort the current run.
#     * Use --supervised for a y/N confirmation before each run.
#     * Use --once for a single, fully-watched ticket.
#
#   TRUE TAKE-OVER (--interactive). For full hands-on control, run with
#   --interactive and set AGENT_INTERACTIVE_CMD to the runtime's interactive
#   launch. The poller claims the ticket, opens the live agent TUI in a tmux
#   window (session $TMUX_SESSION, default agent-<handle>), and blocks until it
#   exits. From another terminal on the builder host:
#       tmux attach -t agent-<handle>      # watch AND type into the agent
#       Ctrl-b d                            # detach, leave it running
#       tmux kill-window -t agent-<handle>:t<N>   # abort a single ticket
#   When the agent exits (you finish, or it opens its PR) the window stays open
#   for review and the poller advances to the next ticket. Example one-shot:
#       AGENT_INTERACTIVE_CMD='<agent-cli> "$(cat "$PROMPT_FILE")"' \
#         bash scripts/agent-poller.sh --interactive --once
#
#   (Run tmux on the builder host — not on a host whose tmux session you are
#   already living inside.)
#
set -euo pipefail

# ---- args ----------------------------------------------------------------
ONCE=0
DRY_RUN=0
SUPERVISED=0
INTERACTIVE=0
WARM=0
for arg in "$@"; do
  case "$arg" in
    --once)         ONCE=1 ;;
    --dry-run)      DRY_RUN=1 ;;
    --supervised)   SUPERVISED=1 ;;
    --interactive)  INTERACTIVE=1 ;;
    --warm|--stay)  WARM=1 ;;
    -h|--help)      awk 'NR>=2 && /^set -euo pipefail$/{exit} NR>=2' "$0"; exit 0 ;;
    *) echo "unknown flag: $arg" >&2; exit 2 ;;
  esac
done

# ---- config / preflight --------------------------------------------------
: "${AGENT_HANDLE:?set AGENT_HANDLE to your builder handle (e.g. builder-1)}"
AGENT_TIERS="${AGENT_TIERS:-mechanical}"
POLL_INTERVAL="${POLL_INTERVAL:-120}"
LOG_DIR="${LOG_DIR:-${TMPDIR:-/tmp}/agent-poller-logs}"

# Accept 5m / 15m / 2h / 300 / 300s for POLL_INTERVAL → seconds.
to_seconds() {
  case "$1" in
    *h) echo $(( ${1%h} * 3600 )) ;;
    *m) echo $(( ${1%m} * 60 )) ;;
    *s) echo "${1%s}" ;;
    *)  echo "$1" ;;
  esac
}
POLL_SECS="$(to_seconds "$POLL_INTERVAL")"
[[ "$POLL_SECS" =~ ^[0-9]+$ ]] || { echo "POLL_INTERVAL invalid: '$POLL_INTERVAL' (use 5m, 15m, 2h, or seconds)" >&2; exit 2; }
mkdir -p "$LOG_DIR"

TMUX_SESSION="${TMUX_SESSION:-agent-${AGENT_HANDLE}}"
if [[ "$INTERACTIVE" -eq 1 ]]; then
  [[ "$WARM" -eq 1 ]] && { echo "--warm and --interactive are incompatible (warm keeps ONE headless session alive across rounds; interactive is a human take-over)" >&2; exit 2; }
  command -v tmux >/dev/null || { echo "--interactive needs tmux on this host" >&2; exit 1; }
  : "${AGENT_INTERACTIVE_CMD:?--interactive needs AGENT_INTERACTIVE_CMD (interactive launch, prompt as an arg — see header)}"
else
  : "${AGENT_CMD:?set AGENT_CMD to your headless agent invocation (see header)}"
fi

GH_REPO_ARGS=()
if [[ -n "${REPO:-}" ]]; then GH_REPO_ARGS=(--repo "$REPO"); fi

log() { printf '%s  %s\n' "$(date -u +%H:%M:%S)" "$*"; }

command -v gh  >/dev/null || { echo "gh not found" >&2; exit 1; }
command -v jq  >/dev/null || { echo "jq not found" >&2; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "gh not authenticated — run 'gh auth login'" >&2; exit 1; }

# Attribution sanity check (non-fatal): the commit identity should be the handle.
git_name="$(git config user.name || true)"
if [[ "$git_name" != "$AGENT_HANDLE" ]]; then
  log "WARN: git config user.name='$git_name' != AGENT_HANDLE='$AGENT_HANDLE'."
  log "      Set:  git config user.name '$AGENT_HANDLE' && git config user.email '$AGENT_HANDLE@users.noreply.github.com'"
fi

log "poller up — handle=$AGENT_HANDLE tiers=[$AGENT_TIERS] interval=${POLL_SECS}s log_dir=$LOG_DIR supervised=$SUPERVISED dry_run=$DRY_RUN once=$ONCE"

# ---- helpers -------------------------------------------------------------

# This handle's in-flight OPEN PR, if any. Echoes "PR<TAB>ISSUE<TAB>BRANCH"
# (one line) or nothing. ISSUE is parsed from the branch name
# (agent/<handle>/<N>-slug) so it works even before the "Closes #N" link
# resolves. This is the ticket's open work unit — while it exists, the poller
# never claims a new ticket (the "one open PR per handle" rule).
find_my_open_pr() {
  local row pr branch issue
  row=$(gh pr list "${GH_REPO_ARGS[@]}" --state open --limit 100 \
        --json number,headRefName \
        --jq "[.[] | select(.headRefName | startswith(\"agent/${AGENT_HANDLE}/\"))]
              | sort_by(.number) | .[0]
              | select(. != null) | \"\(.number)\t\(.headRefName)\"" 2>/dev/null || true)
  [[ -z "$row" ]] && return 0
  pr=$(printf '%s' "$row" | cut -f1)
  branch=$(printf '%s' "$row" | cut -f2)
  issue=$(printf '%s' "$branch" | sed -E "s#^agent/${AGENT_HANDLE}/([0-9]+)-.*#\1#")
  [[ "$issue" =~ ^[0-9]+$ ]] || issue=""
  printf '%s\t%s\t%s\n' "$pr" "$issue" "$branch"
}

# True if issue $1 currently carries label $2.
issue_has_label() {
  local n="$1" want="$2" hit
  hit=$(gh issue view "$n" "${GH_REPO_ARGS[@]}" --json labels \
        --jq "any(.labels[]; .name==\"$want\")" 2>/dev/null || echo false)
  [[ "$hit" == "true" ]]
}

# Print the number of the first ready ticket matching a cleared tier, or empty.
# Preference order follows AGENT_TIERS.
pick_ticket() {
  local ready_json
  ready_json=$(gh issue list "${GH_REPO_ARGS[@]}" --state open \
      --label ticket:ready --limit 100 \
      --json number,title,labels)
  IFS=',' read -ra tiers <<< "$AGENT_TIERS"
  local tier
  for tier in "${tiers[@]}"; do
    tier="$(echo "$tier" | tr -d '[:space:]')"
    [[ -z "$tier" ]] && continue
    echo "$ready_json" | jq -r --arg t "tier:$tier" '
      [ .[] | select(.labels | map(.name) | index($t)) ] | sort_by(.number) | .[0].number // empty
    ' | head -1 | grep -E '^[0-9]+$' && return 0
  done
  return 0
}

# Slug from an issue title: lowercase, alnum→-, collapse, first ~6 words.
slugify() {
  echo "$1" \
    | tr '[:upper:]' '[:lower:]' \
    | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//' \
    | cut -d- -f1-6
}

# In --warm mode, the addendum that tells the agent to keep ONE session alive
# across the whole lifecycle (open PR → watch → service changes in place →
# repeat until merged → exit). Empty unless WARM=1. Needs a wait/monitor-capable
# agent CLI. Issue number passed as $1.
warm_note() {
  local n="$1"
  [[ "${WARM:-0}" -eq 1 ]] || return 0
  cat <<EOF

WARM MODE — stay in THIS single session for the whole lifecycle of issue #${n}.
After you open the PR and set ticket:in-review, DO NOT EXIT. Watch the PR until
it is merged or closed, using your wait/monitor tooling to sleep between polls:
  * Poll periodically (~60s):
      gh pr view <your-PR-number> --json state,reviewDecision
      gh issue view ${n} --json labels
  * If issue #${n} gets ticket:changes (or the PR gets a CHANGES_REQUESTED
    review), address EVERY review point on the SAME branch, push, re-verify CI
    is all-green, then flip ticket:changes -> ticket:in-review and resume
    watching. Re-acquire the holds:arb baton + rebase first if you touch ARB.
  * When the PR is MERGED or closed, you are DONE — exit 0.
  * If a change needs a judgment call you cannot make, set ticket:blocked,
    comment specifically, and exit; the maintainer will re-engage. NEVER merge.
EOF
}

# Build the standing prompt handed to the agent for ticket N.
build_prompt() {
  local n="$1" slug="$2" branch="$3" title="$4"
  cat <<EOF
You are a BUILDER named "${AGENT_HANDLE}" in this repository's multi-agent
workflow. Read AGENTS.md and docs/how-to/agent-collaboration.md first, then
CLAUDE.md for repo conventions.

GitHub issue #${n} ("${title}") has ALREADY been claimed for you and labeled
ticket:claimed — do NOT re-claim it. Your job is to implement it end to end:

1. Branch off the latest main:  git checkout main && git pull && git checkout -b ${branch}
2. Implement EXACTLY per issue #${n}'s spec. Follow the reference PR it cites,
   file for file. Do not expand scope.
3. If the change touches lib/l10n/*.arb, acquire the holds:arb baton FIRST:
   run 'gh issue list --label holds:arb --state open'.
     - If FREE: add holds:arb to issue #${n}, then RE-CHECK the list to confirm
       you are the SOLE holder. If another ticket also holds it (a claim race),
       the LOWEST issue number keeps it; a higher one removes its own holds:arb
       and falls to the "held" case below.
     - If HELD by another ticket: do NOT block — a busy baton is TRANSIENT (it
       frees when the holder merges or parks). Reset this ticket to ticket:ready
       (gh issue edit ${n} --add-label ticket:ready --remove-label ticket:claimed),
       make NO code changes, and stop. The poller retries it on a later pass once
       the baton is free. ticket:blocked is ONLY for a genuine judgment call —
       NEVER for a busy baton.
4. Self-verify: run the gate the spec names (e.g. bash scripts/lint-arb.sh),
   push, wait for CI, and confirm 'gh pr checks <PR>' shows EVERY row 'pass'
   (do not trust the --watch exit code).
5. Open a PR with body "Closes #${n}", set the issue to ticket:in-review, and
   request review from the maintainer. NEVER merge — that is the maintainer's.
6. If anything is ambiguous (vocabulary axis, ICU/placeholder trap, spec vs
   code mismatch), set ticket:blocked, comment your specific question, and stop.
   Do not guess on judgment calls.

Commit as your configured git identity ("${AGENT_HANDLE}") and add a
Co-Authored-By trailer. English only in code, comments, and docs.
$(warm_note "$n")
EOF
}

# Build the prompt for a ticket:changes FEEDBACK ROUND — fix the existing PR
# in place, do not branch fresh, do not claim new work.
build_resume_prompt() {
  local n="$1" branch="$2" pr="$3" title="$4"
  cat <<EOF
You are BUILDER "${AGENT_HANDLE}". Your OPEN pull request #${pr} for issue
#${n} ("${title}") was sent back: the maintainer set ticket:changes and
requested changes. This is a FEEDBACK ROUND in the ticket's lifecycle — do
NOT claim a new ticket, do NOT open a new PR. Fix THIS PR in place:

1. Check out your existing branch (do NOT create a new one):
     git fetch origin && git checkout ${branch} && git pull --ff-only
   If the change touches lib/l10n/*.arb and you don't already hold the
   holds:arb baton, re-acquire it: if free, add it to #${n} and re-check you
   are the sole holder (on a race the LOWEST issue number keeps it). If it is
   HELD by another ticket, do NOT block — leave this ticket ticket:changes,
   make no changes, and stop; the poller retries when the baton frees (a busy
   baton is transient). Rebase on origin/main before pushing.
2. Read EVERY review comment and address each point — do not expand scope:
     gh pr view ${pr} --comments
     gh api repos/{owner}/{repo}/pulls/${pr}/reviews   --jq '.[] | .user.login + ": " + .body'
     gh api repos/{owner}/{repo}/pulls/${pr}/comments  --jq '.[] | .path + ": " + .body'
3. Self-verify: run the gate the spec names (e.g. bash scripts/lint-arb.sh),
   push to the SAME branch, wait for CI, and confirm 'gh pr checks ${pr}'
   shows EVERY row 'pass' (do not trust the --watch exit code).
4. Hand it back for re-review:
     gh issue edit ${n} --add-label ticket:in-review --remove-label ticket:changes
   Remove ticket:changes from PR #${pr} too if present, and comment a short
   summary of what you changed. NEVER merge — that is the maintainer's.
5. If a requested change needs a judgment call you cannot make (vocabulary
   axis, ICU/placeholder trap, spec-vs-code conflict), set ticket:blocked,
   comment your specific question, and stop. Do not guess.

Read AGENTS.md and docs/how-to/agent-collaboration.md (§9) for the rules.
$(warm_note "$n")
EOF
}

# ---- agent execution (shared by fresh claim + changes round) -------------
# Runs ONE agent session for ticket $1 (slug $2) on branch $3. The caller has
# already written + exported $PROMPT_FILE. Sets the global AGENT_RC. The agent
# runs in the FOREGROUND, so the loop serializes to one in-flight ticket.
AGENT_RC=0
run_agent() {
  local n="$1" slug="$2" branch="$3"
  TICKET_NUMBER="$n"; TICKET_SLUG="$slug"; BRANCH="$branch"
  export PROMPT_FILE TICKET_NUMBER TICKET_SLUG BRANCH
  AGENT_RC=0
  local stamp; stamp="$(date -u +%Y%m%dT%H%M%SZ)"

  if [[ "$INTERACTIVE" -eq 1 ]]; then
    # Take-over mode: launch the runtime's interactive TUI in a tmux window,
    # seeded with the prompt; block until the agent exits. Attach any time to
    # watch AND type into it. Completion is signalled by an rc-file the wrapper
    # writes on agent exit (race-free, unlike `tmux wait-for`).
    local win="t${n}"
    local rcfile="${LOG_DIR}/ticket-${n}-${stamp}.rc"
    local wrapper="${LOG_DIR}/ticket-${n}-${stamp}.wrapper.sh"
    {
      printf '#!/usr/bin/env bash\n'
      printf 'export PROMPT_FILE=%q TICKET_NUMBER=%q TICKET_SLUG=%q BRANCH=%q RC_FILE=%q\n' \
        "$PROMPT_FILE" "$n" "$slug" "$branch" "$rcfile"
      printf 'export AGENT_INTERACTIVE_CMD=%q\n' "$AGENT_INTERACTIVE_CMD"
      printf 'eval "$AGENT_INTERACTIVE_CMD"; rc=$?\n'
      printf 'echo "$rc" > "$RC_FILE"\n'
      printf 'echo; echo "[poller] agent exited rc=$rc — window kept for review; type exit to close."\n'
      printf 'exec bash\n'
    } > "$wrapper"
    chmod +x "$wrapper"

    if tmux has-session -t "$TMUX_SESSION" 2>/dev/null; then
      tmux new-window  -t "$TMUX_SESSION" -n "$win" "bash $wrapper"
    else
      tmux new-session -d -s "$TMUX_SESSION" -n "$win" "bash $wrapper"
    fi
    log "interactive #${n} live — attach:  tmux attach -t ${TMUX_SESSION}  (window ${win}). Waiting for the agent to exit..."
    while [[ ! -f "$rcfile" ]] && tmux has-session -t "$TMUX_SESSION" 2>/dev/null; do
      sleep 3
    done
    rm -f "$PROMPT_FILE"
    if [[ -f "$rcfile" ]]; then
      log "interactive #${n}: agent exited (rc=$(cat "$rcfile")). Review the tmux window; poller advancing."
    else
      log "interactive #${n}: tmux session gone before agent exit (aborted?). Poller advancing."
    fi
    return 0
  fi

  # Headless mode: run in the foreground, stream + capture a transcript.
  local logf="${LOG_DIR}/ticket-${n}-${stamp}.log"
  log "handing ticket #${n} to the agent (foreground; transcript → ${logf})..."
  set +e
  local agent_timeout="${AGENT_TIMEOUT:-40m}"
  if [[ "$agent_timeout" != "0" ]] && command -v timeout >/dev/null 2>&1; then
    # Cap a single agent run. A headless agent that doesn't terminate (e.g.
    # one blocked on a background task it never reaps) otherwise holds the
    # FOREGROUND poller — and the whole ticket queue — hostage indefinitely.
    # SIGTERM at the deadline, SIGKILL 30s later. AGENT_TIMEOUT=0 disables;
    # tune it above your slowest legitimate run. AGENT_CMD is inherited from
    # the environment, so the inner shell sees it (+ PROMPT_FILE et al.).
    export AGENT_CMD
    timeout --kill-after=30s "$agent_timeout" bash -c 'eval "$AGENT_CMD"' \
        < "$PROMPT_FILE" 2>&1 | tee "$logf"
    AGENT_RC=${PIPESTATUS[0]}
    if [[ "$AGENT_RC" -eq 124 || "$AGENT_RC" -eq 137 ]]; then
      log "WARN: agent on #${n} hit the ${agent_timeout} timeout (rc=$AGENT_RC) — killed. Leaving the ticket for inspection; poller continuing."
    fi
  else
    ( eval "$AGENT_CMD" ) < "$PROMPT_FILE" 2>&1 | tee "$logf"
    AGENT_RC=${PIPESTATUS[0]}
  fi
  set -e
  rm -f "$PROMPT_FILE"
  return 0
}

# ---- one iteration -------------------------------------------------------
# The ticket lifecycle, as a loop. Each pass:
#   * IN-FLIGHT PR + ticket:changes → run the agent on the EXISTING branch to
#     address the maintainer's review (feedback round). No new claim.
#   * IN-FLIGHT PR + in-review       → wait; the ball is in the maintainer's
#     court (the agent already did its part).
#   * NO in-flight PR                → claim the next ready ticket and run the
#     agent fresh.
# Because the agent runs in the foreground and there is at most one open PR per
# handle, a builder carries a single ticket from claim → PR → changes → merge
# before taking anything new.
work_one() {
  local row pr issue branch
  row="$(find_my_open_pr)"
  if [[ -n "$row" ]]; then
    IFS=$'\t' read -r pr issue branch <<< "$row"
    if [[ -n "$issue" ]] && issue_has_label "$issue" "ticket:changes"; then
      local rtitle
      rtitle="$(gh issue view "$issue" "${GH_REPO_ARGS[@]}" --json title --jq .title 2>/dev/null || echo "ticket #$issue")"
      if [[ "$DRY_RUN" -eq 1 ]]; then
        log "[dry-run] would service ticket:changes on PR #${pr} (issue #${issue}) on branch ${branch}."
        return 0
      fi
      if [[ "$SUPERVISED" -eq 1 ]]; then
        printf '%s  service changes on PR #%s (issue #%s)? [y/N] ' "$(date -u +%H:%M:%S)" "$pr" "$issue" > /dev/tty
        local reply=""; read -r reply < /dev/tty || true
        [[ "$reply" =~ ^[Yy] ]] || { log "skipped changes round on #${issue} by operator."; return 0; }
      fi
      log "servicing ticket:changes on PR #${pr} (issue #${issue}) — feedback round, not claiming new work."
      PROMPT_FILE="$(mktemp -t agent-changes-${issue}.XXXXXX.txt)"
      build_resume_prompt "$issue" "$branch" "$pr" "$rtitle" > "$PROMPT_FILE"
      run_agent "$issue" "$(slugify "$rtitle")" "$branch"
      if [[ "$AGENT_RC" -eq 0 ]] && issue_has_label "$issue" "ticket:changes"; then
        if issue_has_label "$issue" "ticket:blocked"; then
          log "agent finished #${issue} (rc=0) but escalated to ticket:blocked — leaving for the maintainer."
        else
          # The agent fixed the PR but didn't flip the label (cheap models
          # botch multi-step gh ops). The POLLER owns this deterministic
          # transition — otherwise work_one re-services ticket:changes every
          # iteration, re-running the agent on an already-fixed PR and never
          # advancing to the next ticket (the livelock that gated the queue).
          log "agent finished #${issue} (rc=0) but left ticket:changes set — poller flipping → ticket:in-review."
          gh issue edit "$issue" "${GH_REPO_ARGS[@]}" \
              --remove-label ticket:changes --add-label ticket:in-review 2>/dev/null || true
        fi
      fi
    elif [[ -n "$issue" ]] && issue_has_label "$issue" "ticket:blocked"; then
      # The agent escalated a judgment call. Only the maintainer can resolve it;
      # re-running the agent would just re-hit the same wall. Wait — and because
      # of one-open-PR-per-handle this handle makes no further progress until the
      # maintainer flips it (to ticket:changes to re-engage the builder, or
      # ticket:in-review if they resolved it on the PR themselves).
      log "PR #${pr} (issue #${issue}) is BLOCKED — agent escalated to the maintainer; waiting (flip to ticket:changes to re-engage). Not claiming new work."
    else
      log "PR #${pr} (issue #${issue:-?}) is in review — waiting for the maintainer; not claiming new work."
    fi
    return 0
  fi

  # No in-flight PR → claim the next ready ticket.
  local n
  n="$(pick_ticket || true)"
  if [[ -z "${n:-}" ]]; then
    log "no ready ticket at tiers [$AGENT_TIERS]."
    return 0
  fi

  local title slug fbranch
  title="$(gh issue view "$n" "${GH_REPO_ARGS[@]}" --json title --jq .title)"
  slug="$(slugify "$title")"
  fbranch="agent/${AGENT_HANDLE}/${n}-${slug}"

  if [[ "$DRY_RUN" -eq 1 ]]; then
    log "[dry-run] would claim #${n} ('${title}') → branch ${fbranch} → run agent."
    return 0
  fi

  if [[ "$SUPERVISED" -eq 1 ]]; then
    printf '%s  claim and run #%s ("%s")? [y/N] ' "$(date -u +%H:%M:%S)" "$n" "$title" > /dev/tty
    local reply=""; read -r reply < /dev/tty || true
    if [[ ! "$reply" =~ ^[Yy] ]]; then
      log "skipped #${n} by operator."
      return 0
    fi
  fi

  log "claiming #${n} ('${title}') as ${AGENT_HANDLE}."
  gh issue edit "$n" "${GH_REPO_ARGS[@]}" \
      --add-label ticket:claimed --remove-label ticket:ready
  gh issue comment "$n" "${GH_REPO_ARGS[@]}" \
      --body "claiming as ${AGENT_HANDLE}, branch \`${fbranch}\`. ETA ~30m."

  PROMPT_FILE="$(mktemp -t agent-ticket-${n}.XXXXXX.txt)"
  build_prompt "$n" "$slug" "$fbranch" "$title" > "$PROMPT_FILE"
  run_agent "$n" "$slug" "$fbranch"

  if [[ "$AGENT_RC" -ne 0 ]]; then
    log "agent exited non-zero (rc=$AGENT_RC) on #${n}. Leaving the ticket claimed for inspection."
  else
    log "agent finished #${n} (rc=0). Maintainer review is next; poller will wait while the PR is open."
  fi
  return 0
}

# ---- loop ----------------------------------------------------------------
if [[ "$ONCE" -eq 1 ]]; then
  work_one
  exit 0
fi

while true; do
  work_one || log "iteration error (continuing)."
  sleep "$POLL_SECS"
done
