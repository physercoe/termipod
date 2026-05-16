# 028. Host control via the tunnel + a CLI ops surface

> **Type:** decision
> **Status:** Proposed (2026-05-16) — D-1 through D-6 locked in the 2026-05-16 design conversation; awaiting Phase 1 implementation
> **Audience:** contributors · operators
> **Last verified vs code:** v1.0.608-alpha

**TL;DR.** Promote the A2A long-poll tunnel
(`/v1/teams/{team}/a2a/tunnel/next`+`/responses`) into a host RPC
bus by adding a `kind` discriminator to its payload, and use that
bus to deliver control verbs (`host.shutdown`, `host.update`,
`host.restart`) from `hub-server` subcommands to host-runners.
Restart-after-exit is delegated to the existing systemd units
(already shipped at `hub/deploy/systemd/`) by keeping
`Restart=on-failure` and splitting exit codes: **exit 0 = true
shutdown (systemd does NOT respawn)**, **exit 75 (`EX_TEMPFAIL`)
= bounce (systemd respawns, picks up whatever binary is at the
install path)**. Hub-server itself stays up across host-fleet
verbs — `hub-server self-update` is a separate path with its own
exit-75. Hub-side, the per-session work uses the same
`stopSessionInternal` helper the mobile "Stop" button backs, so
sessions transition cleanly from `active` → `paused` (resumable
via the existing endpoint) and the audit trail matches what a
manual mobile stop-each-session would produce. Self-update
fetches **per-binary** release artifacts
(`termipod-host-runner-*` / `termipod-hub-server-*`) from
`physercoe/termipod` on GitHub by default (configurable via
`--upstream-repo`), verified by SHA256; the trust boundary
doesn't widen because the same party already controls
`scp`-to-host today. Ship CLI subcommands first (shutdown →
update → restart → ops fleet of doctor/version/hosts/logs/agents/
db/tokens) and then a mobile Admin pane (owner-scope, long-press
confirmation, audit-logged) in Phase 5. The full discussion is at
[discussions/host-control-and-cli-surface.md](../discussions/host-control-and-cli-surface.md);
the work is in
[plans/hub-host-control-cli.md](../plans/hub-host-control-cli.md).

## Context

Operating Termipod across multiple host-runners today is manual:
to upgrade host-runner the operator SSHes to each host, kills the
process, copies the new binary, and starts it back up. This is
fine at one host and painful at three. The user asked whether
hub-server could grow a `shutdown-all` subcommand that closes
every active steward on every host-runner, then closes the
host-runners themselves so a fresh binary can be installed
without the SSH-loop.

The conversation surfaced four interlocking design questions
(captured in the linked discussion):

1. What channel carries the "please shut down" command from hub
   to host? Today's tunnel is A2A-only.
2. After host-runner exits, what brings it back? systemd /
   launchd / tmux / nothing each have different shapes.
3. Can the hub owner drive this from the mobile app?
4. Once we have a CLI, what other ops subcommands does
   well-grounded practice expect alongside shutdown / update /
   restart?

This ADR records the decisions; the discussion records the
alternatives we considered.

## Decisions

### D-1. Extend the existing A2A tunnel into a host RPC bus

Add a `kind` field to the queued-request payload at the
`/v1/teams/{team}/a2a/tunnel/next` boundary. Today's relay
requests get `kind:"a2a"` (or absence-of-kind, for backward
compat); new control verbs get `kind:"host.<verb>"`. Host-runner
switches on `kind` in `hub/internal/hostrunner/runner.go` and
dispatches to per-verb handlers.

**Why not a sibling control channel.** A parallel
`/v1/teams/{team}/control/tunnel/{next,responses}` pair was
considered. It's the right shape if A2A relay throughput ever
head-of-lines control traffic, but control traffic is sparse
(shutdown, restart, update, periodic self-update polls) and
adding a second long-poll loop in host-runner is mechanical when
we need it. Single channel for now; revisit if metrics show
contention.

**Auth.** A2A relay is mounted outside the auth middleware
(per `server.go:172-173`) because A2A v0.3 declares the URL path
as the capability. Control verbs are different — they're
hub-internal and must be owner-gated. Authentication leans on the
existing transport: host-runner's long-poll already authenticates
with its install token over TLS, and the response stream from hub
to host inherits that channel's trust. **For MVP we do NOT add a
separate HMAC signature on the control payload** — the threat
model (compromised hub binary signing its own forged verbs) is
identical to the threat that an attacker already has the install
token. Defense-in-depth via payload signing is parked as a future
hardening if the threat model changes. Relay path is untouched;
only the `kind:"host.*"` branch is owner-gated at the dispatcher.

**Versioning.** The `kind` field is opaque; unknown kinds return
`{error: "unknown_verb", verb: "<name>", host_version: "<v>"}`
rather than 500ing. This is the seam that lets us roll out new
verbs without flag-day upgrading every host.

### D-2. systemd is the auto-restart mechanism; exit code splits intent

The repo already ships systemd units with `Restart=on-failure`.
Keep that policy. Two exit codes encode two distinct operator
intents, giving us "true shutdown" and "bounce" semantics from
one systemd unit:

| Subcommand | Verb on host | host-runner exits | systemd behavior | Hub-side state |
|---|---|---|---|---|
| `shutdown-all` | `host.shutdown` | **code 0** | does **not** respawn — host stays DOWN | Hub stays up; operator brings hosts back manually with `systemctl start termipod-host@<id>` after dropping new binary if upgrading |
| `update-all` | `host.update` (writes new binary first, then exits) | **code 75** (`EX_TEMPFAIL`) | respawns → picks up new binary at install path | Hub stays up; hub self-updates last as a separate step |
| `restart-all` | `host.restart` | **code 75** | respawns → same binary | Hub stays up; clears bad state, no upgrade |

The verb handler logs the reason before exit so journald has the
audit trail. The audit-log row's `reason` field distinguishes
`update` from `restart` even though they share an exit code.

**Hub-side work is at the session layer, not the agent layer.**
For each active session on each affected host, the orchestrator
calls the same internal helper that backs the mobile "Stop"
button (`stopSessionInternal`, extracted in plan W2.5): session
goes to `paused`, current agent goes to `terminated`, MCP bearer
is revoked, audit rows are written. After all sessions on a host
are stopped, the `host.*` verb fires to clean up stragglers and
exit the runner. This means sessions are resumable after the
restart/update via the existing
`POST /v1/teams/{team}/sessions/{id}/resume` path (live since
v1.0.349) — user state survives the fleet bounce.

**Why session-layer.** The user-facing operation is "stop this
conversation, I'll come back later." Terminating the underlying
agent process is implementation, not intent. The lifecycle test
verifies this distinction: after `restart-all`, sessions that
were `active` show as `paused` (not `deleted`), and resuming them
yields a new agent on the same `engine_session_id`.

**Hub-server is out of the host-fleet exit loop.** Hub stays up
across `shutdown-all`, `update-all`, and `restart-all`. To upgrade
hub itself, the operator runs `hub-server self-update` (Phase 2)
which writes the new bytes and exits 75 — hub's own systemd unit
(`termipod-hub.service`, also `Restart=on-failure`) then picks up
the new binary. The two systemd units stay independent.

Operator-initiated `systemctl stop` exits 0; the unit doesn't
respawn. `systemctl stop` remains a true off switch.

**Why not `Restart=always`.** Simpler in code but loses the
operator's "true off" lever — `systemctl stop` would race the
respawn unless the unit is also masked. Net: lose operator
control, gain nothing the exit-code split doesn't already
provide.

**macOS launchd.** Deferred. The CLI itself is portable; only
the supervisor unit is missing. When a macOS host operator
appears we ship a `KeepAlive={SuccessfulExit=false}` plist as a
follow-up.

**Bare-process and tmux.** Documented as "operator runs without
supervisor → shutdown-all is shutdown only; manual restart
required." A helper script
(`scripts/host-runner-tmux-supervisor.sh` — a `while true; do …;
done` wrapper) ships as an opt-in for users who want hands-off
restart without systemd.

### D-3. CLI before mobile admin

Phase 1-4 land all the ops verbs as `hub-server <verb>`
subcommands. Phase 5 adds the mobile Admin pane that calls the
same REST endpoints (`/v1/admin/host/*` mounted alongside the
existing routes). Owner-scope only, long-press-to-confirm
gesture, audit-logged.

**Why this order.** Three reasons:

- CLI is easier to validate remotely, debug from journald, and
  rollback. Once the verbs are stable the mobile pane is mostly
  plumbing — no new logic, just a UI for the same endpoints.
- The audit log + owner-scope gate are the load-bearing safety
  rails; they need to be right before any tap-to-nuke UI exists.
- The mobile rollout is bundled with confirmation-gesture UX
  decisions that aren't on the critical path for the operator
  pain point.

### D-4. Self-update via GitHub releases + SHA256

Self-update fetches the release artifact from
**`physercoe/termipod`** on GitHub (the fork's release stream) by
default. Operators can override via `--upstream-repo
<owner>/<name>` on the CLI or `release_source: <owner>/<name>` in
the hub config file for forks running their own release pipeline.
The flow verifies SHA256 against a `SHA256SUMS` file in the same
release, atomic-renames into `argv[0]`, and exits 75 so systemd
picks up the new binary.

**Per-binary artifact split.** Today `release.yml` produces one
tarball per platform that bundles BOTH `hub-server` and
`host-runner` together. For self-update, each binary should fetch
only the bytes it actually needs. The release pipeline ships
**eight tarballs per release tag** instead of four:

```
termipod-hub-server-vX.Y.Z-{linux,darwin}-{amd64,arm64}.tar.gz
termipod-host-runner-vX.Y.Z-{linux,darwin}-{amd64,arm64}.tar.gz
SHA256SUMS                                  (entries for all 8)
```

Same version tag for both binaries — **D-6 lockstep
preserved**, the split is purely about download size and
surgical self-update. `host-runner self-update` resolves to the
`termipod-host-runner-*` pattern; `hub-server self-update`
resolves to `termipod-hub-server-*`. The CI cost is ~10 lines of
`release.yml` (split the tar step into two passes).

| Tier | Channel | Trust | Effort |
|---|---|---|---|
| **Chosen** | GitHub releases + SHA256 | TLS to github.com + checksum | ~200 LOC |
| Future | + cosign/minisign signature | Embedded pubkey verify | +~100 LOC |
| Far future | Private artifact server + mTLS | Operator-managed CA | +~400 LOC |

**Trust framing.** "Anyone with GitHub release write access can
push a malicious binary that auto-installs on every host" sounds
alarming until you compare with status quo: the same party
currently SCPs whatever bytes they want to those same hosts. We
are not widening the trust boundary, only automating the bytes
inside it. The upgrade path to cosign signing is a known
follow-up; no protocol churn at the tunnel.

**Resolution order for `--version`.** Explicit version > `--channel
stable` (latest non-prerelease tag) > `--channel alpha` (latest
tag including prerelease, which is today's `vX.Y.Z-alpha` pattern).

### D-5. Subcommand inventory

The phased plan ships these subcommands:

| Phase | Subcommand | Notes |
|---|---|---|
| 1 | `hub-server shutdown-all [--no-wait] [--force-kill]` | Terminates all live agents per host, fires `host.shutdown` verb (host-runner exits 0 → systemd does not respawn). Hub stays up. `--no-wait` skips per-host ack timeout; `--force-kill` SIGKILLs agents instead of SIGTERM+grace. |
| 2 | `host-runner self-update`, `hub-server self-update`, `hub-server update-all` | Self-update is the primitive (`--upstream-repo` defaults to `physercoe/termipod`). `update-all` orchestrates fleet-wide via `host.update` verb (host-runner writes new binary, exits 75); hub self-updates last as a separate step. |
| 3 | `hub-server restart-all` | Fires `host.restart` verb (exit 75, same binary). For "bounce to clear bad state". |
| 4 | `hub-server doctor`, `version`, `hosts ls`, `hosts ping`, `logs tail`, `agents kill`, `db vacuum`, `db migrate`, `tokens rotate`; `host-runner doctor` | Operator quality-of-life; each is a discrete, opt-in wedge. |
| 5 | Mobile Admin pane | Read-write surface for the above, owner-scope. |

Explicitly **not** shipping:

- `hub-server restart` (per-host) — operator can `systemctl restart`
  directly; the only restart that needs orchestration is
  fleet-wide.
- `hub-server daemonize` — systemd/launchd own this.

### D-6. Cross-version compatibility policy

Normal operating mode is **lockstep**: `update-all` upgrades the
whole fleet (hosts + hub) in one operator action. The version
skew during an `update-all` rollout (some hosts at vN, some at
vN+1, hub at vN+1) is transient — order of minutes. To keep that
window safe without ceremony, three rules:

1. **Minor-version compat is mandatory.** Within an `update-all`
   window — i.e. across consecutive minor versions — the A2A
   relay payload, the agent_event shape, and the existing
   `kind:"a2a"` tunnel payload stay backward-compatible. A vN
   host talking to a vN+1 hub (or vice versa) does not crash and
   does not corrupt state.
2. **New control verbs may land in any minor release.** Old hosts
   that don't recognize `kind:"host.<new>"` return the typed
   `unknown_verb` error from D-1. Hub-side callers degrade
   gracefully (e.g. `update-all` against a too-old host gets a
   clear "this host can't self-update, please SSH and upgrade
   manually" error).
3. **Major-version bumps may break compatibility.** When we ever
   cut a v2.0, the release notes ship an explicit migration step
   (typically: `shutdown-all` → manual replace → manual start).
   `update-all` refuses to bridge a major-version boundary
   without `--allow-major-upgrade`.

**Why not stricter protocol negotiation.** Per-verb version
fields, capability handshakes, etc. are common in
enterprise-grade RPC frameworks. The cost is non-trivial
infrastructure and a constant tax on every new verb. For a fleet
where lockstep is the normal mode and the skew window is minutes
not weeks, the typed-error fallback is sufficient. Revisit if we
ever support fleets that intentionally stay out of lockstep.

**Practical consequence.** Engineers shipping a new minor version
must check: does this change A2A payload shape, agent_event
shape, or `kind:"a2a"` request layout? If yes, it's either a
backward-compatible additive change (new optional field) or it
goes in a major release with a migration step. The CI lint
`scripts/lint-openapi.sh` already checks payload schema for the
REST API; an analogous check for tunnel payloads is a Phase 1
follow-up.

## Alternatives considered

### A-1. Push binaries over the tunnel rather than pull from GitHub

The shape would be: `hub-server update-all` ships the binary
bytes through the tunnel; host-runner writes them to disk.
Rejected because:

- Tunnel payload size balloons (host-runner binary is tens of MB).
- The hub becomes a binary cache it doesn't need to be.
- Trust is no better — the hub operator and the GitHub release
  publisher are the same party today.

If we ever want to support air-gapped operators, a separate
"mirror release" feature is cleaner than overloading the tunnel.

### A-2. SSH-based control (no tunnel verb)

Hub-server could SSH to each host using a per-host SSH key and
run shell commands. Rejected because:

- It's the model `Termipod` is designed to **replace** —
  host-runner exists so operators don't need persistent SSH
  reachability to hosts behind NAT.
- Adds an SSH key-management story (rotation, revocation,
  per-host knownhosts) that the tunnel makes unnecessary.

The tunnel is already proven and load-bearing for A2A; extending
it costs less than adding a parallel SSH plane.

### A-3. `Restart=always` + no exit-code encoding

Considered for D-2. Rejected because it removes the operator's
ability to stop a host via `systemctl stop` without also masking
the unit. The exit-code split costs ~5 lines and preserves the
lever.

### A-4. Cosign signing in Phase 2

Considered as the self-update default rather than a follow-up.
Rejected because cosign signing introduces a key-management
operational burden (where does the key live, how is it rotated,
who has access) that isn't on the critical path for the
operator pain point this ADR addresses. Documented as the
explicit next step; the SHA256+TLS tier is honest about its
trust model.

## Consequences

### Positive

- One command (`hub-server update-all`) replaces "SSH to N hosts
  and `scp`+`systemctl restart`". This is the operator pain
  point we set out to fix.
- The tunnel as host RPC bus generalizes: future control verbs
  (`host.config_reload`, `host.cert_rotate`, `host.diagnose`) ride
  the same channel with no new endpoints.
- CLI surface gives operators predictable, scriptable, audit-
  trailed access to the system, which is also useful for
  incident response.
- Mobile Admin pane (Phase 5) makes the hub operable from a
  phone, which matches the product positioning ("operate from
  your phone").
- Audit log entries for every admin action close a forensic gap
  ("who ran shutdown-all at 3am").

### Negative / accepted

- The A2A tunnel is now load-bearing for two different traffic
  classes (relay + control). If control verbs ever become chatty
  we have to split the channel; the migration is mechanical but
  not free.
- One-poll-window latency floor on control verbs (~30s worst
  case). Acceptable; documented.
- Self-update's trust model is "you trust your GitHub release
  publisher" until cosign lands. We name this in operator docs.
- Tmux-without-supervisor users get a worse experience than
  systemd users on `shutdown-all`. Documented; opt-in helper
  script provided.

## Open follow-ups

- Cosign signing of release artifacts (upgrade path from D-4).
- macOS launchd plist (deferred; ship when a macOS operator
  needs it).
- Tunnel `kind` versioning hardening — today's plan is
  "unknown kind returns typed error" (D-6). If we ever ship
  breaking schema changes per verb we need explicit per-verb
  version fields. Not on the critical path.
- Tunnel-payload schema lint (D-6 follow-up) — analogous to
  `scripts/lint-openapi.sh` for REST. Catches accidental
  backward-incompatible changes during minor-version
  development.
- Drain-vs-force-kill is partially covered by the two-flag
  split: `--no-wait` (skip ack) and `--force-kill` (SIGKILL
  vs SIGTERM+grace). A future `--drain` flag that lets agents
  finish their current turn before terminate is still parked
  as Phase 1.5 polish.
- Defense-in-depth HMAC signature on control payloads — parked
  per D-1; revisit if the threat model changes.

## References

- Discussion that produced this ADR:
  [discussions/host-control-and-cli-surface.md](../discussions/host-control-and-cli-surface.md)
- Execution plan:
  [plans/hub-host-control-cli.md](../plans/hub-host-control-cli.md)
- Tunnel endpoints today:
  `hub/internal/server/server.go:220-221`,
  `hub/internal/hostrunner/client.go:404-447` (`NextTunnelRequest`)
- Existing systemd units:
  `hub/deploy/systemd/termipod-hub.service`,
  `hub/deploy/systemd/termipod-host@.service`
- Related ADRs:
  ADR-003 (A2A relay required),
  ADR-005 (owner authority model),
  ADR-017 (layered stewards — accountability for the agents
  shutdown-all terminates),
  ADR-025 (project steward accountability — same accountability
  model is what mobile Admin pane will reuse).
