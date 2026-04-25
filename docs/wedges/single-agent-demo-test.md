# Test guide: Single-Agent Demo wedge

> **Companion to:** [single-agent-demo.md](single-agent-demo.md)
> **Audience:** the human verifying the wedge — this doc walks you
> through what to do with your hands, what to expect on screen, and
> what "broken" looks like for each acceptance criterion.

This is a manual integration test, not a unit-test plan. The wedge
crosses four processes (mobile, hub, host-runner, claude) and the
interesting bugs only show at the seams, so we test it by driving it.

If you want to test the doc — i.e., decide whether the plan in
single-agent-demo.md is **internally consistent and complete enough**
to act on — read **§0 Doc-review checklist** at the bottom and skip
the rest until something is built.

---

## 0. Prerequisites

You need:

- **One fresh host machine.** A throwaway VM is ideal (Ubuntu 22.04+
  is what the host-runner is tested on). Must be reachable from your
  dev box over SSH.
- **A clean install of TermiPod on a phone or emulator.** Wipe app
  data first if you've been using the app — bootstrap-sheet detection
  keys off "team has zero stewards", which is harder to recreate
  once you've spawned one.
- **An Anthropic API key for Claude.** The steward will run `claude`
  on the host, so the API key needs to be set in the host's
  environment (`ANTHROPIC_API_KEY` exported in the same shell that
  the host-runner inherits). Confirm with `ssh host 'claude --version'`
  before starting.
- **A running hub** with at least one team you own. The team should
  already have at least one channel (the bootstrap-sheet flow attaches
  the steward to the team's default channel — if there is no default,
  the sheet has nowhere to land you).
- **`websocat` or equivalent** for the optional raw-protocol probes
  in §6. Optional, only needed if a step fails and you want to dig.

A quick smoke test that the prereqs are all healthy — run on the host:

```bash
echo '{"type":"user","message":{"role":"user","content":"say hi"}}' \
  | claude --print --output-format stream-json --input-format stream-json \
    --verbose --model claude-opus-4-7
```

You should see one or more `{"type":"assistant",...}` lines stream
back, ending with a `{"type":"result",...}`. If this fails, claude
itself isn't healthy — fix that before testing the wedge.

`--verbose` is required for `--print --output-format stream-json` —
without it, claude rejects the combination. The model id is the
canonical long form `claude-opus-4-7`; the short `opus-4-7` may not
be accepted by every claude-code version.

---

## 1. AC1 — Host registers and `~/hub-work` exists

**What you're testing:** the host-runner registers cleanly with the
hub and `~/hub-work` exists afterwards.

> **Note on framing.** Earlier drafts of this AC described a
> `curl … | bash` one-liner installer. That installer doesn't exist
> yet — host bootstrap today is a token mint on mobile + the manual
> Track A or Track B setup in [docs/hub-host-setup.md](../hub-host-setup.md).
> The W0 mkdir we're acceptance-testing here is what `host-runner`
> does on first start, not what an installer script does.

**Steps:**

1. On the mobile app, open the team you own, go to
   **Settings → Auth**, tap **New token**, choose **kind: host**,
   give it a label, and tap **Issue**. Copy the plaintext from the
   bottom sheet. (When `kind=host`, the sheet also shows a
   ready-to-paste setup snippet — see step 2.)
2. SSH into the fresh host. Follow either Track in
   [docs/hub-host-setup.md](../hub-host-setup.md): Track A
   (foreground in tmux, no sudo) is fastest for the test; Track B
   (systemd) is what production uses. Paste the host token from
   step 1 where the doc says `paste-the-plaintext-token-here`.
3. Once the runner is running (Track A: shell prompt blocked on the
   `host-runner run` line; Track B: `systemctl status
   termipod-host@<user>` shows active), still in the SSH session run:
   ```bash
   ls -ld $HOME/hub-work
   ```

**Expected:**

- `ls -ld $HOME/hub-work` shows a directory, mode `0755` (or stricter),
  owned by your user.
- Within ~15s of the runner starting, the host appears in the mobile
  app's Hosts list with a green "connected" indicator.

**Failure modes to watch for:**

- Runner starts but `~/hub-work` doesn't exist → W0 didn't land. Check
  `hub/internal/hostrunner/runner.go` `defaultStewardWorkdir` plumbing.
- Host appears but indicator stays grey → registration succeeded but
  heartbeats aren't reaching the hub. Check the runner's stdout
  (Track A) or `journalctl -fu termipod-host@<user>` (Track B) for
  non-200 heartbeat responses.
- 401/403 on register → the token isn't `kind=host`, or the team in
  the runner's `--team` flag doesn't match the token's team.

---

## 2. AC2 — Bootstrap sheet auto-presents

**What you're testing:** opening a team that has hosts but no
stewards triggers the bootstrap sheet, and the sheet shows the
expected fields with claude as the only option.

**Steps:**

1. From AC1's state (host connected, no agents). On the mobile app,
   force-quit and reopen so you exercise the cold-start path.
2. Tap into the team.

**Expected:**

- A sheet titled **"Start your steward"** slides up automatically
  within 1s of the team route loading.
- Sheet is non-dismissible: tapping the scrim or swipe-down does
  nothing, and there's no top-right close (×) button.
- Host picker shows your one host pre-selected.
- Backend section shows a single radio: **Claude Code** with subtitle
  `opus-4-7 · stream-json · MCP permission gate` — pre-selected,
  no other options visible.
- **Tool permissions** section shows two radios:
  - **Allow all tools (PC mode)** — pre-selected (default for the
    demo). Subtitle calls out that this auto-approves tool calls,
    same as `--dangerously-skip-permissions`.
  - **Prompt for each tool (attention)** — selectable; subtitle
    warns that it requires the W2 MCP tool registered on this hub
    or claude will hang on the first tool call.
  Selection persists into the spawn body as `permission_mode` and
  is interpolated into the steward template's `cmd` line by the hub
  via the `{{permission_flag}}` variable. To verify after spawn,
  SSH the host and `ps -ef | grep claude` — "Allow all" should
  show `--dangerously-skip-permissions`, "Prompt" should show
  `--permission-prompt-tool mcp__termipod__permission_prompt`.
- Persona seed field is empty with placeholder text.
- Buttons: **Skip for now** and **Start →**.

**Tap "Skip for now"** to confirm the dismissal flow:

- Sheet closes.
- A `steward_bootstrap_dismissed_<teamId>` key is written to
  SharedPreferences with an ISO8601 timestamp.
- Force-quit and reopen the app, navigate back to Projects. The
  sheet should *not* reappear (the flag suppresses it).
- Manually clear the flag (or tap the AppBar "No steward" chip to
  open the sheet manually) to continue with AC3.

**Failure modes:**

- Sheet doesn't appear → check that no agent with `handle=steward`
  is in `running`/`pending` state on this team (the trigger
  condition is "no steward present", not "no agents at all"). A
  `terminated` steward row is fine — that doesn't count as present.
- Also confirm at least one host has `status=online`: the trigger
  requires a connected host, not just a registered one.
- Sheet appears but Backend section is empty or shows codex → W4
  used the wrong template list. v1 should hard-code claude.
- Sheet appears on a team with an existing live steward → trigger
  condition is wrong; should only fire when `stewardPresent` is false.
- Sheet keeps reappearing after Skip → SharedPreferences write
  failed, or the dismissed-flag key prefix doesn't match what
  `_maybeShowBootstrap` reads (`steward_bootstrap_dismissed_`).

---

## 2.5. CLAUDE.md materialization (W1.5)

**What you're testing:** the launcher writes the steward persona
into the workdir before claude starts, so the agent loads its
etiquette/recipes/role from CLAUDE.md the way it would on a
hand-edited project.

**Steps:**

1. Spawn a steward (e.g. via the bootstrap sheet from AC2).
2. SSH into the host and inspect the workdir:
   ```sh
   ls -la ~/hub-work/
   head -20 ~/hub-work/CLAUDE.md
   ```

**Expected:**

- `CLAUDE.md` exists in `~/hub-work/`.
- Its contents start with `# Steward Agent` (the rendered
  template body), and `{{principal.handle}}` placeholders have
  been expanded to the actual handle (e.g. `physercoe`), no
  literal `{{…}}` left.
- Subsequent spawns overwrite the file with the latest rendering
  rather than appending.

**Failure modes:**

- File missing → check `agents.spawn` response on the hub for a
  context_files error, or `host-runner` logs for "write
  context_files" failures. If the spec lacks `context_files:` at
  all, the hub didn't resolve the prompt — verify
  `<dataRoot>/team/templates/prompts/steward.v1.md` exists.
- Literal `{{principal.handle}}` left in the file → hub regex
  wasn't extended to support dotted vars; check
  `tmplVarRe` in `template.go`.
- Wrong handle → token-issue scope JSON probably doesn't carry
  `handle`; principal will fall back to `@principal`.

---

## 3. AC3 — Steward channel renders

**What you're testing:** completing the bootstrap sheet produces a
visible, live transcript in the team's default channel — chat-style,
not raw JSON.

**Steps:**

1. From AC2's sheet, type a short persona seed: `You are terse.`
2. Tap **Start →**. The sheet closes; you land back on Projects.
3. Watch the AppBar **Steward chip** (left of the team switcher).
   Within ~1s of the spawn ack, the chip flips from grey "No
   steward" to filled "Steward".
4. Tap the chip to open `#hub-meta`.

**Expected:**

- The channel app-bar reads `#hub-meta` with a Steward badge pill
  next to it (signals the chat-style renderer is active, not the
  plain-channel fallback).
- Within ~3s of tapping Start, you see a **session header chip** at
  the top of the transcript — something like "Session · opus-4-7"
  rendered from the `session.init` agent_event.
- The first time the steward is given a turn (which may be its own
  init prompt or the first user message — depends on the persona-seed
  decision), an assistant text bubble streams in token-by-token.
  Watch for this carefully: the text should *appear gradually*, not
  pop in fully formed. If it pops in, the streaming pipeline is
  buffering on `\n` and the AC4 streaming check will fail too.
- No raw `{"type":"assistant",...}` JSON should be visible anywhere.

**Failure modes:**

- Steward chip stays grey → spawn never reached `running`. Check
  `GET /v1/teams/<t>/agents` for the steward row's `status`; if
  it's `pending` for >30s, the host-runner didn't pick it up.
- Channel opens with the *plain* renderer (no Steward badge in the
  app-bar, attachment paperclip on the composer) → the steward
  detection in `team_channel_screen.dart` failed. Confirm a steward
  agent with `status=running` (or `pending`) exists in
  `hub.agents`. The fallback is intentional — humans can still type
  in the room — but means the chat transcript won't render.
- Channel is empty / no session chip → host-runner didn't actually
  spawn claude, or stream-json frames aren't reaching the hub. Check
  `${LogDir}/termipod-agent-${ChildID}.log` on the host — if that's
  empty, claude isn't running; if it's full, the driver isn't
  forwarding.
- Bubbles appear but text is whole-paragraph chunks → streaming is
  buffered. Investigate `StdioDriver` line-buffering.
- Raw JSON shows up → AgentFeed isn't handling that frame kind.
  Note which kind leaked (the raw card prints the JSON verbatim)
  and add a case to the `_buildBody` switch.

---

## 4. AC4 — Bidirectional input round-trip

**What you're testing:** typing in the channel composer reaches
claude's stdin and the reply streams back.

**Steps:**

1. From AC3's transcript. In the composer, type:
   `What's the current working directory? Use the bash tool.`
2. Tap send.
3. Stopwatch.

**Expected:**

- Your message renders as a user bubble immediately (optimistic).
- Within ~2s, an assistant text bubble starts streaming OR a tool-call
  card appears (claude may decide to call Bash directly).
- If a tool-call card appears, expand it. The tool name should be
  `Bash`, the command should contain `pwd`, and you should see a
  permission prompt (which is AC5 — see next section). For AC4,
  approve it and confirm a tool-result row appears underneath with
  the path `/home/<user>/hub-work` (or the absolute equivalent).
- The reply text should mention the path is `~/hub-work` or similar.
  If claude reports `/root` or `$HOME` without `hub-work`, **W1's
  workdir plumbing didn't land** — file as bug.

**Failure modes:**

- Send succeeds but no reply ever comes → input frame never reached
  stdin. Check the host-runner's SSE input subscription is connected.
  Tail `${LogDir}/termipod-agent-${ChildID}.log` while typing; you
  should see at least the user frame echoed (claude echoes user
  frames in some output modes).
- Reply comes back but cwd is wrong → W1 workdir bug; see above.
- Long latency (>10s) for first token → likely the API key, not the
  wedge. Confirm with the §0 prereq smoke test.

---

## 5. AC5 + diff-1 — Permission gate round-trip

**What you're testing:** the differentiator. A dangerous tool call
pauses claude, surfaces a phone-side approval card, and resolves
back to claude based on the user's choice.

This is the hardest test. Run it twice — once Approve, once Deny.

### 5a. Approve path

**Steps:**

1. From AC4's state. Type:
   `Create a file called test-approve.txt in this directory.`
2. Tap send.

**Expected:**

- Within ~2s, a **tool-call card** appears in the transcript with
  tool name `Bash` (or `Write`, depending on claude's choice) and a
  preview showing the command/path.
- Inline with the tool-call card, a **permission prompt** appears
  with **Approve** and **Deny** buttons.
- Simultaneously, the mobile **Inbox** badge increments by 1.
- Tap **Approve** on the inline prompt.
- Within ~1s, the prompt collapses, a tool-result row appears under
  the tool-call card showing success, and claude continues with an
  assistant bubble confirming the file was created.
- SSH into the host and run `ls ~/hub-work/test-approve.txt` —
  the file should exist.
- Open the **Inbox**. The corresponding `attention_items` row should
  be marked resolved (greyed out / moved to a "resolved" filter).

### 5b. Deny path

**Steps:**

1. Type: `Delete /etc/hosts.`
2. Tap send.
3. When the permission prompt appears with the dangerous command,
   tap **Deny**.

**Expected:**

- Prompt collapses.
- Claude's next assistant bubble acknowledges the denial — something
  like "I won't do that — the user declined" or "Permission was
  denied, so I'll stop here."
- SSH to host and run `ls /etc/hosts` — file still exists. (Obvious,
  but worth checking that the deny actually blocked the call vs. just
  hiding the prompt.)
- Inbox row marked resolved with the deny outcome visible.

### 5c. Inbox-only path (no inline prompt)

**Steps:**

1. While AC5a/b is running, swipe away the inline prompt (or close
   the channel) before resolving it.
2. Open the **Inbox**.

**Expected:**

- The unresolved attention_item is still there with Approve/Deny
  buttons. Resolving from the inbox produces the same outcome as
  resolving inline.
- Returning to the channel after inbox-resolve shows the resolved
  state in the transcript without needing a refresh.

**Failure modes:**

- No prompt ever appears, claude just runs the tool → MCP
  `permission_prompt` not registered, OR claude isn't using the
  `--permission-prompt-tool` flag. Check the steward's process args
  on the host: `ps -ef | grep claude`.
- Prompt appears in inbox but not inline → SSE subscription on the
  channel screen isn't filtering for this turn's attention_items.
- Approve resolves the inbox row but claude hangs forever → the
  resolution isn't round-tripping back to host-runner. Check
  host-runner logs for the long-poll/SSE waiter.
- Deny works but claude tries again with a different tool → that's
  actually fine; claude is allowed to plan around denials. Just
  confirm each subsequent attempt also gates.

---

## 6. Optional: raw-protocol probes for debugging

If something in §1–§5 fails and the symptom is unclear, these
probes let you peek at each layer without rebuilding.

**6a. See what claude is actually receiving.** On the host:

```bash
sudo strace -p $(pgrep -f 'claude.*stream-json') -e trace=read -s 4096 2>&1 | head -50
```

(Linux only; on macOS use `dtruss`.)

**6b. See what host-runner is publishing.** Tail the agent log:

```bash
tail -F /tmp/termipod-agent-*.log   # path from launch_m2.go
```

**6c. See what the hub is forwarding.** Subscribe to the channel SSE:

```bash
curl -sN -H "Authorization: Bearer $TOKEN" \
  https://hub.example.com/v1/teams/$TEAM/channels/$CHAN/events
```

Each agent_event arrives as a `data:` line. Compare against the
mapping table in single-agent-demo.md §"JSONL → agent_events mapping".

**6d. See what the mobile app is rendering.** The Flutter side has
a dev-only "Raw events" toggle in the channel screen settings (or
should — if it doesn't, file it as a separate task; very useful for
this kind of testing).

---

## 7. Regression checks (don't break what works)

Before declaring the wedge done, walk these one last time:

1. **Existing teams with stewards** still load normally — no rogue
   bootstrap sheet on teams that already have agents.
2. **Spawned children** (M2 path used by ml-worker template) still
   work. Spawn a worker via the existing flow and confirm its log
   tails into a pane as before — W1's workdir change must not break
   the spawned-child path.
3. **M4 fallback agents** (anything without `driving_mode: M2`)
   still launch as tmux panes with the `bash` placeholder. The
   steward template change shouldn't have flipped the global
   default.
4. **Hub host-runner restart** — bounce the host-runner process and
   confirm the steward reconnects (or surfaces a clean error and
   offers a restart affordance — auto-resume is B2, out of scope).

---

## 0. Doc-review checklist

If the wedge isn't built yet but you want to gut-check the plan,
read [single-agent-demo.md](single-agent-demo.md) and ask:

- **Does each AC have a wedge task that produces it?** Map: AC1 → W0,
  AC2 → W4, AC3 → W1+W1.5+W5, AC4 → W1+W1.5+W5, AC5/diff-1 → W2+W5.
- **Is anything assumed to exist that doesn't?** The plan calls out
  `mcp_gateway.go`, `attention_items` (kind=approval_request),
  `EnsureWorktree`, `StdioDriver`. Spot-check at least two by
  grep'ing the repo.
- **Is the claude invocation right?** Cross-reference the
  `--print --output-format stream-json --input-format stream-json --permission-prompt-tool …`
  line against current Claude Code CLI docs (it changes occasionally).
- **Are the open questions the *right* open questions?** Persona seed
  placement, dismissal scope, preview length, permission TTL — none
  of those block AC5. Anything that *would* block AC5 should be
  promoted to a blocker, not left in the open-questions list.
- **Is the estimate honest?** 3.75d for 5 wedges feels tight if W2's
  MCP gateway changes ripple into the hub's attention_items API. If
  you don't already have an `attention_items` resolution endpoint
  that takes `{behavior, updatedInput}`, W2 is probably 1.5d, not
  1.0d.

If those check out, the plan is implementable. If not, push back
before code goes in.
