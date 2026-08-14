# Run a local agent session (no hub)

> **Type:** how-to
> **Status:** Current (2026-08-14)
> **Audience:** directors · contributors
> **Last verified vs code:** desktop `main` (vision-parity L3a + L3b) against
> claude-code 2.1.220

**TL;DR.** The desktop can run a claude session itself — the engine child
lives in Electron main, not on a host, and no hub is involved. Open a
workspace folder, press **+ Local session** in the Companion, and talk to it.
By default it can read that folder and nothing else.

---

## When you want this

A hub agent is the right thing for fleet work: remote hosts, teleport, durable
cross-device state, the attention queue. A **local session** is for the
opposite case — you are sitting at the machine with the files on it, and you
want an assistant beside your work without standing anything up.

The two are the same conversation to the Companion. The transcript, the folds,
the composer and the approval cards are identical; only the producer differs
(plan decision D-7 — the hub is an option, never a prerequisite).

## Starting one

1. Open a workspace folder (Author's file tree, or **Open folder**). The
   folder becomes the session's working directory — this is refused rather
   than defaulted, because the working directory decides which files the
   session can read and guessing it would scope an agent somewhere you never
   chose.
2. Open the Companion in the assistant dock.
3. Press **+ Local session**. It appears in the picker under *On this
   machine* and is selected for you.

If the button is absent, this build has no local driver — the browser build
has no service at all, and `localagent_families` returns an empty list rather
than offering something that would fail on click.

## What it is allowed to do

A session launches with a **tool posture**, which is an allowlist of the
engine's built-in tools:

| Posture | Tools | Use |
|---|---|---|
| `converse` | none | pure conversation; it cannot touch the machine |
| `read_local` | `Read`, `Glob`, `Grep` | **the default** — reads the workspace, no writes, no execution, no network |
| `unrestricted` | whatever the engine offers | full co-working; a caller has to name it explicitly |

The posture is recorded on the session's `lifecycle` row, so a transcript
states what the agent was permitted to do instead of leaving you to infer it
from what it happened to call.

### Why an allowlist and not a permission mode

This is worth reading before you assume the familiar flag works here.

The hub gates tool calls with
`--permission-prompt-tool mcp__<ns>__permission_prompt`, which routes each
request into the attention queue. A local session has no hub, so that flag has
no target — and the obvious substitutes do not do what their names suggest.
Measured against claude-code 2.1.220 driving
`--print --output-format stream-json`:

- no flag → the child **runs Bash** without asking; no control frame is
  emitted and `permission_denials` comes back empty.
- `--permission-mode manual` → **runs Bash**.
- `--permission-mode plan` → **runs Bash**. Plan mode is not a safety boundary
  in `--print`; there is no interactive channel for it to hold a plan against.
- `--tools Read,Glob,Grep` → Bash is absent from the session entirely. The
  model reports it unavailable and calls nothing.

So the tool list is the only lever that gates a non-interactive child, and an
allowlist is used rather than a denylist: a denylist fails open for every tool
a future claude adds, and "the engine grew a capability" must not silently
widen what a local session can do to your machine.

## What a local session does not have

Degraded to absence, never to a stub that quietly no-ops (D-4):

- **No attention queue.** That is a hub table with its own decide route. R1's
  approval cards still work — they use the direct input verbs — but there is
  no cross-agent queue for a local session to appear in.
- **No fleet, teleport, remote hosts or cross-device persistence.**
- **No context ring on the first turn.** stream-json's per-message `usage`
  block carries token counts and no window, so the denominator is learned from
  the engine's own `by_model` number at the end of a turn. Turn one has no
  ring; every turn after it does. (Absent beats wrong.)
- **No fleet-wide visibility.** A local session exists on this machine only. It
  is not on the hub, so it does not appear on your phone, on another desktop, or
  in anyone else's Fleet view.

## Closing the app and coming back

Quit the app and your local sessions are still there. Reopen the Companion and
they are in the picker under *On this machine*, marked **from a previous run**.
Select one and the whole conversation is there to read. Type, and it answers
knowing everything you said before.

That takes two independent things, and it is worth knowing they are separate
because they fail separately:

| | What restores it | What it costs you if it breaks |
|---|---|---|
| The **transcript** — what was said | our own append-only log, one JSONL file per session under the app's data directory | you see a blank window |
| The **memory** — what the agent knows | claude's native `--resume <id>` | the agent silently forgets and answers as a stranger |

Native resume alone is not enough, and this is the measured part: resuming under
`--print --output-format stream-json` restores the model's context faithfully —
a codeword set before a restart comes back — but the child emits **no replay
frames at all**. It remembers; it does not re-narrate. A rebind on resume alone
would hand you an empty transcript backed by an agent that secretly knows
things, which is worse than starting fresh, because you cannot see what it is
about to act on.

Some details you may notice:

- **Reattaching is lazy.** Opening the app does not start engine children for
  every old session — that would spend tokens because you launched an app.
  The session reattaches when you send the first message.
- **A resumed session says so.** The transcript carries a second "started" row
  marked `resumed`, so a reader can tell a reattached conversation from one
  that restarted cold.
- **A failed reattach is loud.** If the engine cannot find the conversation it
  exits with `No conversation found with session ID`, which lands in the
  transcript as an error row. It does not quietly start a fresh conversation.
- **Old transcripts are trimmed, not kept forever.** A session's file is
  compacted to its recent window once it passes ~32 MB. Scrolling far enough
  back gets you a fresh snapshot rather than a hole.
- **Removing a session deletes its transcript** from disk, so it does not come
  back at the next launch.

## Which config root it uses

`CLAUDE_CONFIG_DIR` relocates claude's entire config home. The service
resolves it once — an explicit per-session override, then the ambient
variable, then `~/.claude` — and passes that root to the child explicitly, so
the root we believe it is using is the root it uses. Getting this wrong is
silent, which is why it is resolved rather than inherited.

The plan treats this root as **per-account, not per-machine**: a director with
work and personal logins has more than one. The root a session was created
against is recorded with the session and reused when it reattaches — resuming
against the wrong root finds no conversation at all, so it is not left to
whatever the environment happens to say at the time.

Enumerating *all* your roots, to list the claude sessions on this machine that
the Companion did not start, is the session catalog — a separate wedge (L3c).

## Verifying it end to end

There is a test that spawns the real binary rather than a fake:

```bash
cd desktop/electron
TERMIPOD_LOCAL_ENGINE_E2E=1 npm test
```

It asserts a full turn — `session.init`, the engine's session id, the model's
reply as typed `text`, the turn boundaries, a dense `seq`, and **no `raw`
rows** (a `raw` row means the engine emitted a frame shape the vendored
profile does not model, which is the drift this would otherwise discover in
production).

A second test proves the restart, which is the part no fixture can vouch for:
it sets a codeword, throws the service away, builds a fresh one over the same
data directory, and checks both halves separately — that the transcript reloads
with its numbering intact, and that the rebound engine can still say the
codeword back.

Both are opt-in because they run real model turns and spend tokens; CI skips
them, and so does a plain `npm test`.

## When something is wrong

- **The button is disabled** — no workspace folder is open.
- **"local agent service unavailable"** — `agent_families.generated.json` is
  missing or unreadable. It is generated from the hub's YAML; regenerate with
  `go test ./internal/agentfamilies/ -run Artifact -update-families-artifact`.
- **The session starts and never answers** — check the transcript for a
  `lifecycle` row with `expected: false`, which is a child that exited on its
  own (an auth failure lands here). `stderr` from the engine is logged as a
  distinct `error` row rather than mixed into the agent's own output.
- **The agent says a tool is unavailable** — that is the posture doing its
  job. Start a session with a wider one if you meant to.
- **An old session is missing from the picker** — its transcript file could not
  be read, and the service refuses to reopen a session whose history is gone
  rather than showing you an empty one that claims to be it.
- **It reattached but does not remember** — look for an `error` row containing
  `No conversation found with session ID`. The engine's own copy of the
  conversation is gone (its config root changed, or claude pruned it); ours is
  still readable, but the agent is starting fresh from here.

## See also

- [`plans/desktop-companion-vision-parity.md`](../plans/desktop-companion-vision-parity.md)
  — lane L, and the L3a / L3b / L3c split
- [`discussions/companion-vision-and-kimi-web-bar.md`](../discussions/companion-vision-and-kimi-web-bar.md)
  §9/§11 — why the desktop builds a service for claude at all
- [`reference/engine-resume-recipes.md`](../reference/engine-resume-recipes.md)
  — the resume table a rebind reads its `--resume` flags from
