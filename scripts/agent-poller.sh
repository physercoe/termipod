#!/usr/bin/env bash
#
# agent-poller.sh — autonomous builder loop for the multi-agent protocol.
#
# A host-side poller that lets ANY builder agent work the GitHub ticket queue
# without a human typing a prompt each time. It does the cheap, deterministic
# GitHub orchestration in bash (find a ready ticket → claim it → hand the agent
# a standing prompt), so the expensive agent only spends tokens on the actual
# implementation.
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
#
# ---------------------------------------------------------------------------
# Guarantees / safety
# ---------------------------------------------------------------------------
#   * One-in-flight: the agent runs in the FOREGROUND, so the loop naturally
#     serializes — a poller process works at most one ticket at a time.
#   * Won't pile up review: if this handle already has an OPEN PR
#     (branch agent/<handle>/*), the poller waits instead of claiming more.
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
for arg in "$@"; do
  case "$arg" in
    --once)        ONCE=1 ;;
    --dry-run)     DRY_RUN=1 ;;
    --supervised)  SUPERVISED=1 ;;
    --interactive) INTERACTIVE=1 ;;
    -h|--help)     awk 'NR>=2 && /^set -euo pipefail$/{exit} NR>=2' "$0"; exit 0 ;;
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

# Is there already an OPEN PR from this handle's branch namespace? (review queue)
have_open_pr() {
  local n
  n=$(gh pr list "${GH_REPO_ARGS[@]}" --state open --limit 100 \
        --json headRefName \
        --jq "[.[] | select(.headRefName | startswith(\"agent/${AGENT_HANDLE}/\"))] | length")
  [[ "${n:-0}" -gt 0 ]]
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
3. If the change touches lib/l10n/*.arb, FIRST check no other open ticket holds
   the holds:arb baton (gh issue list --label holds:arb --state open). If free,
   add holds:arb to issue #${n}; if held, set ticket:blocked, comment why, stop.
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
EOF
}

# ---- one iteration -------------------------------------------------------
work_one() {
  if have_open_pr; then
    log "handle ${AGENT_HANDLE} already has an open PR — waiting for review, not claiming."
    return 0
  fi

  local n
  n="$(pick_ticket || true)"
  if [[ -z "${n:-}" ]]; then
    log "no ready ticket at tiers [$AGENT_TIERS]."
    return 0
  fi

  local title slug branch
  title="$(gh issue view "$n" "${GH_REPO_ARGS[@]}" --json title --jq .title)"
  slug="$(slugify "$title")"
  branch="agent/${AGENT_HANDLE}/${n}-${slug}"

  if [[ "$DRY_RUN" -eq 1 ]]; then
    log "[dry-run] would claim #${n} ('${title}') → branch ${branch} → run agent."
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
      --body "claiming as ${AGENT_HANDLE}, branch \`${branch}\`. ETA ~30m."

  PROMPT_FILE="$(mktemp -t agent-ticket-${n}.XXXXXX.txt)"
  TICKET_NUMBER="$n"
  TICKET_SLUG="$slug"
  BRANCH="$branch"
  export PROMPT_FILE TICKET_NUMBER TICKET_SLUG BRANCH
  build_prompt "$n" "$slug" "$branch" "$title" > "$PROMPT_FILE"

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
  ( eval "$AGENT_CMD" ) < "$PROMPT_FILE" 2>&1 | tee "$logf"
  local rc=${PIPESTATUS[0]}
  set -e
  rm -f "$PROMPT_FILE"

  if [[ $rc -ne 0 ]]; then
    log "agent exited non-zero (rc=$rc) on #${n}. Leaving the ticket claimed for inspection."
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
