# Host control and the hub CLI surface

> **Type:** discussion
> **Status:** Resolved (2026-05-16) — open questions D-1 through D-6 locked in the 2026-05-16 design follow-up; see [decisions/028-host-control-via-tunnel-and-cli.md](../decisions/028-host-control-via-tunnel-and-cli.md). Phase 1 implementation tracked in [plans/hub-host-control-cli.md](../plans/hub-host-control-cli.md).
> **Audience:** principal · contributors · operators
> **Last verified vs code:** v1.0.608-alpha

**TL;DR.** Today operating Termipod across a fleet of hosts requires
SSH'ing to each box to stop agents and replace the host-runner
binary. We want a single command on the hub that closes every
active steward on every host-runner, shuts the host-runners down,
and lets the operator (or a service manager) bring the new version
back up. This doc records the four design discussions that led to
ADR-028 and `plans/hub-host-control-cli.md`: (1) what a "tunnel
verb" means and whether the current tunnel can carry control
traffic, (2) how the host-runner gets auto-restarted after a clean
shutdown (systemd / launchd / tmux / nothing), (3) whether the hub
owner can drive this from the mobile app, and (4) which other CLI
subcommands well-grounded ops practice expects on a hub like this.

---

## 1. Why this came up

The operator workflow today, to upgrade host-runner on N hosts:

1. SSH to each host.
2. Stop the host-runner process (kills its child agents abruptly,
   may leave engine PTYs dangling).
3. `scp` or `wget` the new binary.
4. Start host-runner again.
5. Reconnect mobile, hope every steward picked up correctly.

This is fine at N=1 (the maintainer's own box) but it's the kind of
thing that compounds badly. The user asked: can hub-server gain a
`shutdown-all` subcommand so this becomes "one command, then
reinstall, then it's back"?

Two derivative questions surfaced almost immediately:

- For the "then it's back" half to be hands-off, **something has
  to restart host-runner after the shutdown**. What plays that
  role?
- The natural next step is `update-all` — push the new binary
  rather than the operator copying it manually. Is that worth
  bundling in?

---

## 2. The tunnel — what we have and what "tunnel verb" means

The hub↔host-runner channel today is a long-polled inverse-RPC
pair, all under the A2A path:

- `GET /v1/teams/{team}/a2a/tunnel/next` — host-runner blocks here;
  hub responds when there's work or when the poll timeout expires.
- `POST /v1/teams/{team}/a2a/tunnel/responses` — host-runner POSTs
  the result back; hub correlates by request id and unblocks the
  original A2A caller (per
  `hub/internal/server/server.go:220-221`).

Only one **verb** rides this channel — relay an A2A JSON-RPC body
to a local agent and return its response. By "verb" we mean the
discriminator field in the queued-request payload that tells
host-runner *what kind of thing to do*; today the only `kind` is
implicit (the request is always an A2A relay).

Pulling more verbs through the same long-poll has two shapes:

- **Extend in place.** Add a `kind` field to the queued-request
  schema and a switch in `hub/internal/hostrunner/runner.go` that
  routes `kind:"a2a"` to today's relay path and new kinds
  (`kind:"host.shutdown"`, `kind:"host.self_update"`, etc.) to new
  handlers. One channel, one auth boundary, no new endpoints.
- **Add a sibling control channel.** Spin a parallel pair
  (`/v1/teams/{team}/control/tunnel/next` + `/responses`) so A2A
  throughput and control traffic don't share a queue. Heavier but
  lets control plane sit behind a stricter scope (owner-only)
  without touching A2A's looser auth (it's currently mounted
  outside the auth middleware — `server.go:172-173`).

We prefer **extend in place** for Phase 1: control traffic is
sparse (shutdown, restart, update), it's gated at the dispatcher
not the transport, and one channel keeps the operational model
simple. If we later find queue-head-of-line blocking against A2A
relay throughput, the sibling channel is an easy follow-up — the
host-runner already manages one long-poll loop, a second is
mechanical.

**The latency floor.** Long-poll cycles today are ~30s (per
`NextTunnelRequest`). Worst case "fire shutdown → host actually
exits" is one poll window. Acceptable for ops verbs; we just note
it in the docs so an operator who expects sub-second response
isn't surprised.

**Scope reframing.** Today's A2A tunnel is described internally
as "A2A transport". Once it carries control verbs it's "host RPC
bus". This is a deliberate scope promotion and is the load-bearing
reason ADR-028 exists rather than just a quiet schema edit.

---

## 3. Auto-restart — systemd, launchd, tmux, nothing

For `shutdown-all` to be useful in the upgrade flow, *something*
has to bring host-runner back up after it exits. Four options:

### 3.1 systemd (Linux, what's shipped)

The repo ships two units already:

- `hub/deploy/systemd/termipod-hub.service`
- `hub/deploy/systemd/termipod-host@.service`

Both use `Restart=on-failure` with a small `RestartSec`. The
service manager forks the binary, watches for SIGCHLD, and the
`Restart=` policy decides whether to respawn:

| Restart policy | Behavior on clean exit (code 0) | Behavior on non-zero exit / signal |
|---|---|---|
| `no` | nothing | nothing |
| `on-failure` (current) | nothing | respawn |
| `always` | respawn | respawn |
| `on-success` | respawn | nothing |

`Restart=on-failure` is the principled default — `systemctl stop`
remains a true off switch. **Resolved:** we use exit code **75**
(`EX_TEMPFAIL`) for "intentional bounce / update" and exit code
**0** for "true shutdown, stay down". With `on-failure`, exit 0
does not respawn → operator-driven shutdown stays down; exit 75
respawns → host comes back with whatever binary is at the install
path. This gives us **shutdown-all** and **update-all/restart-all**
semantics from one systemd unit configuration, no `Restart=` flips
required.

Alternative: switch to `Restart=always`. Simpler in code but
removes the operator's "true off" lever — `systemctl stop` would
race the respawn unless the operator also masks the unit. Lose
the lever, gain nothing the exit-code approach doesn't already
give us.

**Decision (carried into ADR-028 D-2):** keep `on-failure`;
encode intent in exit code. Three subcommands map to two exit
codes: `shutdown-all` → exit 0 (host stays down, operator brings
back manually); `update-all` → exit 75 (writes new binary first,
systemd respawns with new bytes); `restart-all` → exit 75 (no
new binary, just clears state).

### 3.2 launchd (macOS)

The macOS analog is a launchd plist with `KeepAlive=true` (any
exit triggers respawn) or `KeepAlive={SuccessfulExit=false}` (the
on-failure analog). We don't ship plists yet; macOS host-runner is
currently run manually. ADR-028 adds a follow-up to ship a
plist when there's a macOS user who needs it; for the first
release the auto-restart story is Linux-only.

### 3.3 tmux

tmux is **not** a supervisor. If the command in a pane exits, the
pane shows the exit status and that's it. The common workaround
is a shell wrapper:

```bash
while true; do host-runner run …; sleep 1; done
```

This works but:

- Crash-loops just keep going with no health surface.
- Logs are owned by tmux, not journald or stdout-redirected files.
- No structured way to ask "is host-runner up right now?"

For users who run host-runner inside a tmux session
(common during dev), `shutdown-all` will leave the host dead until
they reattach and re-run. We document this and add a tiny
`scripts/host-runner-tmux-supervisor.sh` (the while-loop above) as
an opt-in for users who don't want systemd but do want
hands-off restart. Mainline production assumes systemd.

### 3.4 Nothing

If the operator runs host-runner directly under a login shell with
no supervisor, `shutdown-all` is "shutdown only" — they'll need to
manually restart. We document this and treat it as an explicit
operator choice, not a bug. The CLI gains a `--no-restart-required`
flag that fails fast when `--auto-restart` is implied (e.g. by
`update-all`) and no supervisor is detected.

---

## 4. Self-update — distribution and trust

The user's second ask was: can we also add `update` so the operator
doesn't have to `scp` the new binary? The mechanics are
straightforward — fetch, verify, atomic rename, exit non-zero — but
the *distribution channel* is a real decision because every
host-runner will install whatever bytes that channel hands them.

Three tiers:

| Tier | Channel | Trust model | Effort |
|---|---|---|---|
| **Cheap** | GitHub releases + SHA256 file | TLS to github.com + checksum match | ~200 LOC |
| **Better** | Cosign/minisign signature on artifacts | Embedded pubkey verifies signature | ~300 LOC + key mgmt |
| **Enterprise** | Private artifact server + mTLS + signed manifest | Operator-managed CA | ~600 LOC + infra |

For alpha and the immediate roadmap, **cheap** is the right
trade-off. "Anyone with GitHub release write access on the repo
can push a malicious binary" reduces to "the maintainer can push a
malicious binary on themselves" — which is the status quo today
when they `scp` to their own hosts. We're not making the trust
boundary worse; we're just automating the bytes-moving step inside
the existing boundary.

**Resolved release source.** Default repo is
**`physercoe/termipod`** (this fork's release stream).
Configurable via `--upstream-repo <owner>/<name>` on the CLI or
`release_source:` in the hub config. Normal operators don't need
to set anything; fork operators running their own release pipeline
can point at theirs.

We name this explicitly so when the project grows multi-maintainer
or external operators, the upgrade path to **better** is clear:
add cosign signing in CI, embed the pubkey in host-runner, gate
self-update on verify. No protocol churn at the tunnel.

**Atomic-rename + exit-nonzero pattern.** The self-update flow is:

1. Resolve target version (`--version` flag or `--channel=stable`
   resolves to latest matching tag via the GitHub API).
2. Download artifact to `<install_dir>/.host-runner.new`.
3. Verify SHA256 against `SHA256SUMS` from the same release.
4. `os.Rename()` to `argv[0]` (atomic on the same filesystem).
5. Exit non-zero so the supervisor picks up the new binary.

If any step fails, the old binary is untouched and the process
keeps running. The atomic rename is the critical safety property
— there's no window where the file is half-written and `exec`
trips on a corrupt ELF header.

---

## 5. Mobile control plane

The user asked: could the hub owner control this from the mobile
app? Mechanically yes — mobile speaks the bearer-authed hub REST
API, and any new admin endpoint is callable. The auth model
already distinguishes the install token (owner) from member tokens
(per `hub/internal/auth/`).

The trade-off is **risk surface**. "Tap to nuke all hosts" is the
obvious failure mode if a fat-finger tap reaches the admin
button. Three guards keep that survivable:

1. **Owner-only scope** on the endpoints. Member tokens get 403.
2. **Confirmation gesture** in the UI — long-press + slide, not a
   single tap. Same model used in the lifecycle-test step that
   already triggers terminate.
3. **Audit log entry** on every admin action so the operator can
   trace "who clicked what" after the fact (the `audit_events`
   table is already there; we add `action=host.shutdown`,
   `action=host.update`, etc.).

The shape: a new Admin tab in the mobile app, visible only when
the active bearer is owner-scope. Inside it:

- Per-host status row (green/red, last poll, version).
- Buttons: "Shutdown host", "Update host", "Restart host".
- A "Shutdown all" / "Update all" / "Restart all" pair at the
  top, each behind the long-press gesture.
- A read-only log of recent admin actions pulled from
  `audit_events`.

We ship CLI first (Phase 1-4) and then the mobile admin pane
(Phase 5). The reason for ordering: CLI is easier to validate
remotely, debug from logs, and rollback. Once the verbs are stable
the mobile pane is mostly plumbing.

---

## 6. Other subcommands well-grounded ops practice expects

The conversation surfaced these as "things any hub-server operator
will reach for in real incidents". Curated, not exhaustive — the
ones not listed here either don't pay rent yet (auth schemes,
multi-region) or are duplicates of REST endpoints we already have:

| Subcommand | What it does | Why it matters |
|---|---|---|
| `hub-server doctor` | Preflight: DB writable, port free, certs valid, host-runners reachable, disk space. | Run before every install/upgrade; catches "you forgot to open the port" cheaply. |
| `hub-server version [--remote]` | Embed git SHA + build date. `--remote` queries each host-runner. | Version skew is the #1 cause of "weird ACP errors" after partial upgrades. |
| `hub-server hosts ls` | Live reachability + last-seen-poll. | Separate from `hosts.list` REST (which is for app consumption). |
| `hub-server hosts ping <id>` | Round-trip a control verb. | Confirms the tunnel is alive end-to-end, not just that the row exists. |
| `hub-server logs tail [--host <id>]` | Multiplexed tail of hub + host-runner logs. | Saves an SSH for the common "what's it doing right now" question. |
| `hub-server agents kill --all` / `kill <id>` | Emergency stop without the full shutdown flow. | Already in REST as terminate; a sub-second CLI wrapper is worth it. |
| `hub-server db vacuum` | Compaction for `events` / `audit_events`. | These tables grow unbounded; vacuum is the only mitigation today. |
| `hub-server db migrate` | Explicit migration step. | Currently implicit in `serve`; explicit is friendlier when migrations get longer. |
| `hub-server tokens rotate` | Rotate install token, broadcast over tunnel, revoke old after ack. | Avoids re-onboarding every host on token rotation. |
| `host-runner doctor` | Host-side preflight: can it reach hub, engines on PATH, MCP catalog parsable. | Mirror of hub-server doctor on the receiving end. |

Pushed back on:

- `restart` — composed from `shutdown-all` + supervisor restart;
  the separate subcommand is sugar but doesn't pay rent.
- `daemonize` — let systemd/launchd own that. Re-inventing it in
  Go is a maintenance burden with no payoff.

---

## 7. Resolved decisions — summary

The 2026-05-16 design follow-up locked these (full text in
[ADR-028](../decisions/028-host-control-via-tunnel-and-cli.md)):

- **D-1.** Extend the existing A2A tunnel with a `kind` field;
  no sibling control channel for MVP. No HMAC signature on
  control payloads — rely on the authenticated long-poll over TLS.
- **D-2.** Exit code split: **0 = true shutdown** (systemd does
  not respawn), **75 (`EX_TEMPFAIL`) = bounce** (systemd
  respawns). `shutdown-all` uses 0; `update-all` and
  `restart-all` use 75. **Hub-side work is at the session
  layer** — orchestrator calls the same `stopSessionInternal`
  helper that backs mobile-Stop today (session → `paused`,
  agent → `terminated`, MCP bearer revoked). Sessions are
  resumable after the fleet bounce via the existing resume
  endpoint.
- **D-3.** CLI subcommands ship first (Phases 1-4); mobile Admin
  pane in Phase 5.
- **D-4.** Self-update from `physercoe/termipod` by default;
  configurable via `--upstream-repo`. SHA256 verification, no
  cosign for MVP. **Per-binary release artifacts:**
  `termipod-host-runner-*` and `termipod-hub-server-*` are
  separate tarballs under the same release tag — host-runner
  self-update downloads only what it needs.
- **D-5.** Subcommand split: `shutdown-all` + `update-all` +
  `restart-all` are distinct commands with distinct semantics
  (not aliases). Flags `--no-wait` (skip ack timeout) and
  `--force-kill` (SIGKILL agents instead of SIGTERM+grace) are
  separate, not merged under `--force`.
- **D-6.** Cross-version compat: minor-version bumps must be
  backward-compatible across the tunnel + A2A payload + agent_event
  shape. New control verbs may land any minor release; unknown
  `kind` returns typed error. Major-version bumps may break;
  require `--allow-major-upgrade`.

## 8. Open questions parked for later

- **Force kill vs graceful drain.** The two-flag split
  (`--no-wait` + `--force-kill`) covers the "skip ack" and
  "no grace period" axes. A future `--drain` flag that lets
  agents finish their current turn before terminate is still
  parked as Phase 1.5 polish.
- **macOS plist.** Deferred until there's a macOS host operator
  who needs it. The CLI itself is portable; only the supervisor
  unit file is missing.
- **Multi-team operator.** Today the install token gates a
  single team. If we ever ship multi-tenant hub installs, "shutdown
  all" needs to either be per-team or super-admin scoped. Out of
  scope for this round; called out so the verb design doesn't
  paint into a corner.
- **Tunnel-payload schema lint.** Analogous to
  `scripts/lint-openapi.sh` for REST. Catches accidental
  backward-incompatible changes during minor-version development.
  Phase 1 follow-up.

---

## 9. Status — links forward

- ADR: [decisions/028-host-control-via-tunnel-and-cli.md](../decisions/028-host-control-via-tunnel-and-cli.md) (Proposed; D-1–D-6 locked)
- Plan: [plans/hub-host-control-cli.md](../plans/hub-host-control-cli.md) (Proposed, 5 phases)
- This discussion is Resolved as of 2026-05-16 follow-up; flips
  to "Resolved + Phase 1 shipped" after Phase 1 lands.
