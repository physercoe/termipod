# Engine resume recipes — how each engine reattaches to a prior session

> **Type:** reference
> **Status:** Current (2026-08-08)
> **Audience:** contributors (humans + AI agent maintainers)
> **Last verified vs code:** `1accc790` — table transcribed from herdr
> `src/agent_resume.rs` @ `6f311498` and pinned by
> `hub/internal/resumerecipes/recipes_test.go`; claude / codex / agy rows
> re-verified by running each binary's `--help` on a host (2026-08-08)

**TL;DR.** When an agent is respawned, it should pick up its prior
conversation rather than cold-start. Every engine spells that differently
— `claude --resume <id>`, `codex resume <id>`, `agy --conversation <id>`,
`copilot --resume=<id>` — and two of ours carry the cursor at the protocol
level instead of on the command line. That knowledge is data:
[`hub/internal/resumerecipes/recipes.yaml`](../../hub/internal/resumerecipes/recipes.yaml).
It is data and not Go because the hub is not the only consumer — the
desktop Companion's local agent service reads the same file from
TypeScript. Plan: [pane-state-manifests](../plans/pane-state-manifests.md)
N1.

## 1. Two tables, and why they are separate

`engines:` is a per-CLI recipe: binary, flag or subcommand, which kinds of
session reference it accepts. It describes the world, including engines we
cannot spawn.

`families:` maps **our** registered agent families (the value in
`agents.kind` for a worker) onto a resume **mechanism**. This is the half
that governs behaviour, and it is deliberately conservative: an `engine:`
here asserts that our family and that recipe are the same binary. Where
that is unverified the field is empty and callers get nothing rather than
a guess — because a wrong resume command does not error, it silently
starts a fresh conversation, which looks like the agent forgot everything.

## 2. Mechanisms

| Mechanism | What carries the cursor | Families |
|---|---|---|
| `argv` | The engine's own flag, spliced into `backend.cmd` before respawn | claude-code, antigravity |
| `acp_session_load` | ACP `session/load` at the protocol level | gemini-cli, kimi-code-ts, kimi-code |
| `appserver_thread_resume` | codex app-server `thread/resume` JSON-RPC | codex |

The last two share one wire shape: a top-level `resume_session_id` field
in the spawn spec, which `ACPDriver` reads as `session/load` and
`AppServerDriver` as `thread/resume`. One field, two protocol surfaces.

Two rows are easy to misread:

- **gemini-cli has an argv recipe but is not an `argv` family.** The hub
  injects the ACP field; the M2 exec-per-turn driver separately threads
  `--resume <UUID>` into each turn from a cursor it captured itself
  (`driver_exec_resume.go`). The recipe documents driver-internal
  behaviour and feeds the desktop; treating it as a spawn-time splice
  would rewrite a command the hub has never rewritten.
- **codex names its CLI recipe without using it.** `codex resume <id>` is
  real and probed, but the live path is `thread/resume`. The recipe is
  there for the spawn-per-session fallback rung that vision-parity L4
  describes.

## 3. Verification grades

Recorded per row, because a vendored recipe that is wrong fails silently
and averaging the confidence across the file would hide that.

| Grade | Meaning | Rows |
|---|---|---|
| `probe` | The binary was run on a host and its help output confirmed the flag | claude, codex, agy |
| `in-tree` | No binary here, but this repo already ships and tests the recipe | gemini |
| `vendored` | Transcribed from herdr only; we have not run the binary | the other 13 |

`TestProbeGradeIsOnlyForBinariesWeRan` fails if a row is promoted to
`probe` without being one of the three actually run. Downgrading is never
needed; promoting requires running the binary and saying so in the row's
`note`.

## 4. Session references

A reference is either an `id` or an absolute `path` (only `pi` and `omp`
accept a path). Both are screened before use:

- non-empty, no control characters
- ids ≤ 512 bytes; paths ≤ 4096 bytes and absolute

Lengths are **bytes**, not characters — a TypeScript reader using
`String.length` counts UTF-16 units and will disagree on any non-ASCII id.
The fixture pins that boundary explicitly.

## 5. Shell safety

The hub does not exec the resume command. It splices it into a spawn
spec's `backend.cmd`, which tmux runs **through a shell** — and the
session id arrives verbatim from the engine's own `session.init` payload.
That makes it attacker-influenced data in a shell context.

Values are therefore quoted when they need it, and only when they need it:
anything outside `[A-Za-z0-9_@%+=:,./-]` is wrapped in single quotes with
embedded quotes rendered as the four-character `'\''` splice. Ordinary
ids — UUIDs, hyphenated slugs, paths — come through byte-identical to what
the hub produced before this table existed, so adopting it changed no
command that was already safe. A value that cannot be made safe by quoting
(a control character, over-length) is refused, and the respawn cold-starts
— the same fallback every other failure in that path already takes.

## 6. Adding or changing a recipe

1. Edit `recipes.yaml`. Adding an engine is a row; adding a family is a
   row and a mechanism.
2. If it is a new vendored row, add it to the upstream pin in
   `recipes_test.go` so a future re-vendor diffs cleanly.
3. Regenerate the shared fixture:
   `go test ./internal/resumerecipes/ -run Fixture -update-resume-fixture`.
   A stale fixture fails the suite — that failure IS the signal that any
   TypeScript reader pinned to it now disagrees with Go.
4. Do not promote a `vendored` row to `probe` without running the binary.

## 7. References

- [`plans/pane-state-manifests.md`](../plans/pane-state-manifests.md) — N1,
  the wedge that built this.
- [`discussions/herdr-runtime-borrows.md`](../discussions/herdr-runtime-borrows.md)
  §4 — the borrow, and what else came with it.
- [`plans/desktop-companion-vision-parity.md`](../plans/desktop-companion-vision-parity.md)
  — L3/L4, the TypeScript consumer this file exists to serve.
- [ADR-014](../decisions/014-claude-code-resume-cursor.md) — claude's
  `--resume` continuity;
  [ADR-021](../decisions/021-acp-capability-surface.md) — the ACP
  `session/load` path;
  [ADR-035](../decisions/035-antigravity-engine-m4-locallogtail.md) D8 —
  agy's `--conversation`.
