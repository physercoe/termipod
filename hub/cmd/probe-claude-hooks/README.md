# claude-code hook probe — on-device validation for ADR-027

> **Type:** how-to
> **Status:** Active (2026-05-15)
> **Audience:** contributors running the M4 / LocalLogTailDriver validation on a real claude-code install
> **Last verified vs code:** claude-code 2.1.129

**TL;DR.** Two files (`hook-probe.sh` + `settings.local.json`) that
let you observe — without writing any hub or host-runner code —
which claude-code hook events fire in your real session, what JSON
payload each carries, and whether `--dangerously-skip-permissions`
suppresses any of them. Outputs land as one file per hook
invocation under `/tmp/cc-hook-probe/`, queryable with `jq`. Used
to answer the open questions in [`docs/decisions/027-local-log-tail-driver.md`](../../../docs/decisions/027-local-log-tail-driver.md)
before committing to the hook-driven event surface.

This is a **research probe**, not production code. It runs as
shell `type:"command"` hooks (no MCP, no hub HTTP) so it can be
copied to any host with claude-code installed and exercised
standalone.

---

## What it captures

For each of the 9 hooks claude-code 2.1.129 emits (`PreToolUse`,
`PostToolUse`, `Notification`, `UserPromptSubmit`, `Stop`,
`SubagentStop`, `PreCompact`, `SessionStart`, `SessionEnd`), the
probe writes the raw JSON payload to
`/tmp/cc-hook-probe/<Event>-<nanoseconds>.json`. Each file is the
hook's stdin verbatim, plus an `_event` + `_ts` metadata wrapper.

Exit-0, no-stdout-decision: claude-code is not gated by the probe.
The session runs normally; the probe just logs.

---

## Setup on the test machine

### 1. Copy the probe to a known location

```bash
mkdir -p ~/cc-hook-probe
cp hook-probe.sh ~/cc-hook-probe/
chmod +x ~/cc-hook-probe/hook-probe.sh
```

### 2. Choose a test workdir + install settings.local.json

Pick a scratch directory (or use an existing project — the
settings file is project-scoped):

```bash
mkdir -p ~/cc-hook-probe-workdir/.claude
cp settings.local.json ~/cc-hook-probe-workdir/.claude/
```

Verify `$HOME` expands inside the hook command. On default bash:

```bash
echo "Hook command will resolve to: bash $HOME/cc-hook-probe/hook-probe.sh PreToolUse"
```

If `$HOME` doesn't expand in your shell, **edit
`settings.local.json` and substitute the absolute path**.

### 3. Verify `jq` is installed

```bash
jq --version          # need jq for both the probe script and the inspection commands
```

If missing: `apt-get install jq` (Debian/Ubuntu) or `brew install jq` (macOS).

### 4. Reset the log directory before each scenario

```bash
rm -rf /tmp/cc-hook-probe/
```

This keeps each scenario's artefacts cleanly separated.

---

## Smoke test (run this first)

Open the test workdir in claude-code:

```bash
cd ~/cc-hook-probe-workdir
claude --dangerously-skip-permissions
```

In the claude session, type a trivial prompt:

```
ls
```

Wait for claude to finish. Exit the session (`/exit` or Ctrl+D).

Inspect:

```bash
ls /tmp/cc-hook-probe/
jq -r '._event' /tmp/cc-hook-probe/*.json | sort | uniq -c
```

You should see at least: `SessionStart`, `UserPromptSubmit`,
`PreToolUse`, `PostToolUse`, `Stop`, `Notification`, `SessionEnd`.
If you see zero files, the hooks aren't loading — verify:

- `~/cc-hook-probe-workdir/.claude/settings.local.json` exists
- claude-code version is 2.1.x (hooks landed there)
- The hook script path in settings.local.json is correct
- Run `bash ~/cc-hook-probe/hook-probe.sh smoke <<< '{}'` directly and confirm it writes to /tmp/cc-hook-probe/

---

## Scenarios to run

Run each scenario in a **fresh log directory** (`rm -rf /tmp/cc-hook-probe/`)
so the artefacts stay separable. Save each scenario's output for
inspection (`tar czf scenario-N.tar.gz /tmp/cc-hook-probe/`).

### Scenario A — Tool call in bypass-permissions mode

**Setup:** `claude --dangerously-skip-permissions`

**Steps:**
1. Prompt: `Read the file ~/.bashrc and tell me how many lines it has.`
2. Let claude finish.
3. Exit.

**Expected:** `SessionStart`, `UserPromptSubmit`, multiple
`PreToolUse` + `PostToolUse` (Read, Bash, etc.), `Stop`,
`Notification` (idle), `SessionEnd`. **No `permission_prompt`
attention** in any backend (bypass mode skips the gate).

**Verify:**
```bash
jq -r '._event + " -- " + (.tool_name // .message // "")' /tmp/cc-hook-probe/*.json | sort | head -20
```

**Key question this answers:** does `PreToolUse` fire in bypass mode? (Expected: yes.)

### Scenario B — Plan mode + ExitPlanMode

**Setup:** `claude --dangerously-skip-permissions`

**Steps:**
1. Press `Shift+Tab` to cycle to **plan mode**.
2. Prompt: `Plan a refactor that renames variable foo to bar in src/main.go.`
3. Claude builds a plan, then calls `ExitPlanMode` to ask you to approve.
4. **DO NOT approve yet** — first observe the TUI prompt that appears.
5. Approve the plan.
6. Let claude run.
7. Exit.

**Expected:** Among the captured hooks, look for:
- `PreToolUse` with `tool_name: "ExitPlanMode"` — **inspect its `tool_input` to see if the plan text is in the payload**.
- A `Notification` with `message` mentioning "approval for the plan".
- Whatever fires when you tap "Approve" in the TUI (probably nothing — `PostToolUse{tool_name:"ExitPlanMode"}` after approval).

**Verify:**
```bash
jq . /tmp/cc-hook-probe/PreToolUse-*.json | grep -A2 ExitPlanMode | head -40
jq -r '.message // empty' /tmp/cc-hook-probe/Notification-*.json | sort -u
```

**Key question this answers:** Q1 (plan-mode prompts in bypass) + Q2 (does `tool_input.plan` ship in the hook payload, or just in JSONL?).

### Scenario C — Mode cycling mid-session via Shift+Tab

**Setup:** `claude` (default mode, NOT --skip)

**Steps:**
1. Prompt: `Make a trivial change to /tmp/test-file.txt`
2. When claude tries Edit, you'll see the approval prompt — pick **Yes**.
3. Press `Shift+Tab` to switch to **accept-edits** mode.
4. Prompt: `Add a second line.` → Edit auto-approves now.
5. Press `Shift+Tab` to **bypass** mode.
6. Prompt: `Run ls /tmp/` → tool auto-approves.
7. Exit.

**Expected:** `PreToolUse` should fire for every tool call regardless
of mode. **No hook fires for the mode-switch keypress itself**
(it's a user UI action, not a claude event). The mode change shows
up in the claude-code JSONL transcript as a `permission-mode` event,
not as a hook.

**Verify:**
```bash
# How many PreToolUse fired:
ls /tmp/cc-hook-probe/PreToolUse-*.json | wc -l
# Mode-related hook events (expected: zero — mode switch is not a hook):
grep -l 'mode_change\|mode_switch\|permission-mode' /tmp/cc-hook-probe/*.json
```

**Key question this answers:** can we rely solely on hooks for the
mode-change signal? (Expected: no — must read JSONL `permission-mode`
events alongside.)

### Scenario D — Notification message vocabulary

**Setup:** `claude --dangerously-skip-permissions`

**Steps:**
1. Run several distinct interaction patterns:
   - Idle wait (don't type for ~30s after claude finishes)
   - Long bash command (`sleep 5`) that may trigger "still working" UI
   - Trigger a subagent: `Use the Task tool to investigate the structure of /tmp.`
2. Exit.

**Expected:** Several `Notification` files, each with a distinct
`message` string. We want the **full vocabulary** of messages
2.1.x emits, so we can pin the message → dialog_type routing table.

**Verify:**
```bash
jq -r '.message' /tmp/cc-hook-probe/Notification-*.json | sort -u
```

**Key question this answers:** Q4 (what are the actual
`Notification.message` strings?).

### Scenario E — Compaction

**Setup:** `claude` (any mode)

**Steps:**
1. In claude, type `/compact` to manually trigger compaction.
2. Wait for compaction to finish.
3. Exit.

**Verify:**
```bash
jq . /tmp/cc-hook-probe/PreCompact-*.json
```

**Key question this answers:** what fields does `PreCompact`
carry? Does `trigger` distinguish manual vs auto?

### Scenario F — Subagent termination

**Setup:** `claude --dangerously-skip-permissions`

**Steps:**
1. Prompt: `Use the Task tool to summarize the file /etc/os-release. Then tell me the result.`
2. The subagent fires; wait for it to finish.
3. Exit.

**Verify:**
```bash
jq . /tmp/cc-hook-probe/SubagentStop-*.json
```

**Key question this answers:** Q6 (does `SubagentStop` carry
`agent_id` and `agent_type` reliably?).

### Scenario G — `mcp_tool`-type hook with long park (optional, separate run)

This requires a running MCP server with a tool that deliberately
takes a long time to return — out of scope for this initial
probe. Defer until the hub-side MCP tool handlers are scaffolded.

---

## What to report back

After running scenarios A–F, post a summary to the discussion
([`docs/discussions/local-log-tail-m4-replacement.md`](../../../docs/discussions/local-log-tail-m4-replacement.md))
or a follow-up PR. The minimum useful report is:

```
$ ls /tmp/cc-hook-probe/ | cut -d'-' -f1 | sort | uniq -c
       N PreToolUse
       N PostToolUse
       N Notification
       ...

# Full Notification message vocabulary observed:
$ jq -r '.message' /tmp/cc-hook-probe/Notification-*.json | sort -u
...

# ExitPlanMode tool_input shape:
$ jq '.tool_input | keys' /tmp/cc-hook-probe/PreToolUse-*.json \
    | grep -B1 plan

# Anomalies, missing hooks, or surprising payloads:
- (describe)
```

A tarball of `/tmp/cc-hook-probe/` from each scenario is the
most-evidence-per-byte attachment.

---

## Cleanup

```bash
rm -rf /tmp/cc-hook-probe/                  # log files
rm  ~/cc-hook-probe-workdir/.claude/settings.local.json   # if you don't want hooks anymore
```

The probe writes nothing else outside `/tmp/cc-hook-probe/` and the
chosen settings file.

---

## Notes + gotchas

- **The probe never blocks claude-code.** Each hook returns `{}` to
  stdout + exit 0. Even if the script fails to write the log file,
  the hook still returns success — claude-code proceeds.
- **Timeout is 5s for most hooks, 30s for `PreCompact`.** The
  script itself returns in <50ms; longer values are just headroom.
- **Hooks fire EVEN with `--dangerously-skip-permissions`.** That
  flag bypasses permission *gates*, not the hook *event stream*.
- **No data leaves the test machine.** All files are local under
  `/tmp/cc-hook-probe/` and the chosen workdir.
- **Multiple claude sessions on the same machine** will interleave
  files in `/tmp/cc-hook-probe/`. To isolate, set
  `CC_HOOK_PROBE_DIR=/tmp/cc-hook-probe/session-A` in the hook
  command, or `rm -rf` between sessions.
- **`type: "command"` hooks run a fresh bash per call** — they're
  observation-only and stateless. The `mcp_tool` variant (the real
  M4 driver target) reuses the per-spawn MCP UDS and is faster /
  more capable, but requires the hub-side handlers to exist first.
- **`$CLAUDE_PROJECT_DIR`** is the documented env var pointing to
  the project root inside hook commands. We use `$HOME` here to
  decouple the probe location from the test workdir; either works.

---

## References

- [decisions/027-local-log-tail-driver.md](../../../docs/decisions/027-local-log-tail-driver.md) — the ADR this probe validates
- [discussions/local-log-tail-m4-replacement.md](../../../docs/discussions/local-log-tail-m4-replacement.md) — design rationale + open questions list
- [plans/local-log-tail-claude-code-adapter.md](../../../docs/plans/local-log-tail-claude-code-adapter.md) — adapter spec
- claude-code public hook docs: `code.claude.com/docs/en/hooks` (canonical entry; redirected from older `docs.claude.com` path)
- Sister probe (JSONL schema validation): [`hub/cmd/probe-claude-jsonl/`](../probe-claude-jsonl/)
