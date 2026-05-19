# Hub + host control CLI — phased rollout

> **Type:** plan
> **Status:** Complete (2026-05-19) — all five phases shipped (v1.0.611-alpha, v1.0.634-alpha, v1.0.635-alpha, v1.0.636-alpha). ADR-028 captures the locked decisions; §7 lists the out-of-scope follow-ups.
> **Audience:** contributors
> **Last verified vs code:** v1.0.636-alpha

**TL;DR.** Add ops subcommands to `hub-server` and `host-runner`
so the operator can take the whole fleet down, push a new
version, and bring it back up without SSHing to each host. Five
phases in strict order — shutdown → update → restart → ops fleet
→ mobile admin pane. Each phase is independently shippable and
self-contained; later phases build on the verb schema from
Phase 1 but don't require it to land first. The motivating
discussion is at
[discussions/host-control-and-cli-surface.md](../discussions/host-control-and-cli-surface.md);
the locked decisions are in
[decisions/028-host-control-via-tunnel-and-cli.md](../decisions/028-host-control-via-tunnel-and-cli.md).

---

## 1. Phase order, summarized

| Phase | Status | Ship | Exit code | Approx LOC | Depends on |
|---|---|---|---|---|---|
| 1 | ✅ shipped v1.0.611 | `shutdown-all` + tunnel `kind` field + `host.shutdown` verb + `stopSessionInternal` helper (shared with mobile-Stop) | 0 (stays down) | ~360 | — |
| 2 | ✅ shipped v1.0.634 | Per-binary release split (W5.5) + `self-update` (both binaries, default `physercoe/termipod`) + `update-all` + `host.update` verb | 75 (respawn new binary) | ~420 | Phase 1 verb schema |
| 3 | ✅ shipped v1.0.634 | `restart-all` + `host.restart` verb | 75 (respawn same binary) | ~80 | Phase 1 |
| 4 | ✅ shipped v1.0.635 | doctor / version / hosts ls/ping / logs tail / agents kill / db vacuum / db migrate / tokens rotate; host-runner doctor | — | ~9 wedges | independent |
| 5 | ✅ shipped v1.0.636 | Mobile Admin pane (Flutter) — AppBar action on `HubDetailScreen` + `ConfirmActionTile` + audit query screen + per-host/db/audit admin endpoints | — | ~700 | Phases 1-4 endpoints |

Phases 1-4 are CLI-only and can ship in any order after 1. Phase
5 is gated on the REST endpoints being stable.

---

## 2. Phase 1 — shutdown-all

> **Status: shipped v1.0.611-alpha** (commit `83170b0`). W1-W5 all
> landed. The wedge text below is kept as the historical spec —
> three things shipped differently from the W1/W3 sketch:
>
> - **W1 — the `kind` switch is in the `hostrunner/a2a` package,
>   not `runner.go`.** `a2a/tunnel.go`'s `RunTunnel` switches on
>   `TunnelEnvelope.Kind`; `host.*` routes through a
>   `HostVerbHandler`. The per-verb dispatcher is
>   `hostrunner/host_verbs.go` `handleHostVerb` (`switch verb` →
>   `case "shutdown"`).
> - **W3 — the per-host orchestration is hub-side, not in the CLI.**
>   It lives in `handleAdminFleetShutdown`
>   (`POST /v1/admin/fleet/shutdown`, owner-gated via
>   `requireOwner`); `cmd/hub-server/shutdown_all.go` is a thin REST
>   client over that endpoint. This is deliberate — Phase 5's
>   mobile pane reuses the same hub logic. `enqueueHostVerb`
>   (`tunnel_a2a.go`) pushes the verb onto the host's tunnel queue.
> - Only `/v1/admin/fleet/shutdown` exists; the per-host
>   `/v1/admin/host/{id}/*` routes are still Phase 5 (W22).

### 2.1 Goal

Operator runs `hub-server shutdown-all`. Result:

- Every active session on every host gets **stopped** — the same
  operation the mobile "Stop" button performs today: session goes
  to `paused`, current agent goes to `terminated`, MCP bearer
  revoked. SIGTERM + grace by default; `--force-kill` SIGKILLs
  the agent process immediately.
- Every host-runner exits with **code 0**. Per ADR-028 D-2,
  systemd `Restart=on-failure` does **not** respawn on clean
  exit, so hosts stay DOWN.
- **Hub-server stays up.** Per ADR-028 D-2, hub is not in the
  host-fleet exit loop. The operator brings hosts back via
  `systemctl start termipod-host@<id>` (typically after dropping
  a new binary if upgrading), or pairs `shutdown-all` with
  Phase 2's `update-all` for a hands-off upgrade.
- Audit log gains two row classes:
  `action=session.stop` per active session (the user-facing
  operation) and `action=host.shutdown` per host (the operator
  operation). Per-agent `agent.terminate` rows are the existing
  side-effect from the mobile path and remain unchanged.
- Sessions stay in `paused` and can be resumed (via mobile or
  `POST /v1/teams/{team}/sessions/{id}/resume`) once hosts are
  back up — same resume path that's been live since v1.0.349.

### 2.2 Wedges

**W1. Tunnel kind discriminator (~80 LOC).**
- Extend the queued-request payload at
  `/v1/teams/{team}/a2a/tunnel/next` with `kind: "a2a" | "host.<verb>"`.
  Absence-of-kind reads as `"a2a"` for backward compat.
- Add a switch in `hub/internal/hostrunner/runner.go` that
  routes `kind:"a2a"` to today's relay and `kind:"host.*"` to a
  new `handleHostVerb` dispatcher (initially has one case).
- Unknown kinds return
  `{error:"unknown_verb", verb:<name>, host_version:<v>}` and a
  4xx; no 500.
- Unit test: dispatcher routes correctly for both kinds + the
  unknown-verb error shape.

**W2. host.shutdown verb (~80 LOC).**
- Handler in host-runner: receive verb, SIGTERM+grace (or
  SIGKILL if `force_kill:true`) any remaining live processes in
  its registry as a cleanup pass, log reason to journald,
  `os.Exit(0)`.
- The primary per-session stop work happens hub-side in W3
  (writes DB, revokes tokens, etc.); the host verb's job is
  just to terminate stragglers and bring the host down.
- No HMAC signature verification (per ADR-028 D-1, MVP relies on
  the authenticated long-poll's TLS channel).

**W2.5. Extract `stopSessionInternal` helper (~60 LOC).**
- The body of `handlePatchAgent` at `handlers_agents.go:316-340`
  (status flip to `terminated`, session flip to `paused`, MCP
  bearer revoke, host command enqueue, audit-row write) is the
  load-bearing "stop a session" path. Extract into
  `func (s *Server) stopSessionInternal(ctx, team, sessionID, opts)`
  returning the affected agent ID and any error.
- Mobile-PATCH path keeps calling this helper for parity.
- `opts.ForceKill bool` propagates SIGKILL through to the
  enqueued host command.
- Adds a `session.stop` audit row in addition to the existing
  `agent.terminate` row, so the activity feed surfaces the
  user-facing operation cleanly.

**W3. hub-server shutdown-all subcommand (~120 LOC).**
- Add `shutdown-all` case to the switch in
  `hub/cmd/hub-server/main.go:39`.
- For each host in `hosts.list` (live only):
  1. Enumerate the host's active sessions
     (`status='active' AND host_id=?`).
  2. Call `stopSessionInternal(ctx, team, session_id, opts)` for
     each — same code path as mobile Stop, same audit trail.
  3. Fire `host.shutdown` verb via the tunnel queue, wait for
     ack with a 60s timeout (cleanup pass + host exits).
- Record an `audit_events` row per host on success
  (`action=host.shutdown`).
- After all hosts ack (or timeout), **hub-server returns to the
  caller without exiting**. Per ADR-028 D-2, hub stays up.
- `--no-wait` skips the per-host ack timeout (useful when a host
  is unresponsive but we still want to fire and move on).
- `--force-kill` is propagated through `stopSessionInternal.opts`
  and into the verb payload so host-runner SIGKILLs agents
  instead of SIGTERM+grace.

**W4. systemd unit verification (~10 LOC).**
- Confirm `Restart=on-failure` is honored as expected: exit 0
  does not respawn, exit 75 does respawn.
- Document the exit-code contract (0 = true shutdown, 75 = bounce)
  in a comment block at the top of `hub/cmd/host-runner/main.go`
  and `hub/cmd/hub-server/main.go`.
- Add an entry to `docs/how-to/install-host-runner.md` describing
  the supervisor expectation.

**W5. Lifecycle test scenario (~30 LOC of doc).**
- Add Scenario 25 to `docs/how-to/test-steward-lifecycle.md`:
  spawn 2 stewards across 2 hosts → run `hub-server shutdown-all`
  → confirm exit codes, audit rows, and that hosts **stay down**
  (systemd respects exit 0). Bring hosts back with
  `systemctl start` and verify they reconnect cleanly.

### 2.3 Acceptance

- Every active session on every host transitions to `paused`
  within 60s of `shutdown-all` return; every agent on those
  sessions transitions to `terminated`.
- Hub-server stays running after the subcommand completes.
- Audit log shows one `session.stop` row per stopped session,
  one `agent.terminate` row per terminated agent, and one
  `host.shutdown` row per host — same shape mobile-Stop +
  fleet-shutdown would produce together.
- Sessions are resumable via the existing
  `POST /v1/teams/{team}/sessions/{id}/resume` once hosts are
  back up.
- Hosts stay down (systemd does NOT respawn) until operator runs
  `systemctl start`.
- `systemctl stop termipod-host@<id>` still works as a true off
  switch.

---

## 3. Phase 2 — self-update + update-all

> **Status: shipped v1.0.634-alpha** (2026-05-19). W5.5–W10. What
> shipped:
>
> - **W5.5** — `release.yml` now emits 8 per-binary tarballs +
>   `SHA256SUMS` (commit `5b44c31`).
> - **W6** — `hub/internal/selfupdate` package + `host-runner
>   self-update` (`b68636e`).
> - **W7** — `hub-server self-update` (`40a2f60`).
> - **W8** — the `host.update` verb (`21aa328`).
> - **W9** — `POST /v1/admin/fleet/update` + `update-all` CLI
>   (`c0a3cef`).
> - **W10** — Scenarios 26–27 in `test-steward-lifecycle.md`.
>
> Two notes vs the W6–W9 sketch below: the GitHub fetch +
> verify + atomic-replace primitive lives in one shared
> `selfupdate` package (W6/W7 are thin wrappers, as W7 planned);
> and the hub's own self-update during `update-all` runs on a
> delayed goroutine inside the `fleet/update` handler so the HTTP
> response posts before the daemon exits 75 — there is no separate
> hub-exit RPC.

### 3.1 Goal

Operator runs `hub-server update-all --version vX.Y.Z` (or
`--channel stable`). Result:

- Every host-runner downloads the matching release artifact from
  `physercoe/termipod` on GitHub (or the configured
  `--upstream-repo`), verifies SHA256, atomic-renames into its
  install path, exits **75**.
- Systemd respawns each with the new binary.
- Hub-server self-updates last (same flow; its own systemd unit
  picks up the new binary).
- Audit log gains rows for each self-update (`action=host.update`,
  meta carries from-version and to-version).

### 3.2 Wedges

**W5.5. Split release artifacts per binary (~20 LOC of YAML).**
- Today `release.yml`'s "Build hub-server + host-runner binaries"
  step produces one tarball per platform that bundles BOTH
  binaries. Self-update would download ~30MB to extract one
  ~15MB binary it needs.
- Refactor the for-target loop to produce **two tarballs per
  platform**, one per binary:
  `termipod-hub-server-vX.Y.Z-<os>-<arch>.tar.gz` and
  `termipod-host-runner-vX.Y.Z-<os>-<arch>.tar.gz`.
- Generate a single `SHA256SUMS` file containing entries for all
  8 artifacts (4 platforms × 2 binaries). Same release tag for
  both — D-6 lockstep preserved; the split is purely about
  download size and surgical self-update.
- Update the `gh release create` asset list and the release-note
  template to list each artifact with its purpose ("for hub
  hosts" vs "for runner hosts").
- This wedge ships before W6/W7 because self-update depends on
  the per-binary artifact pattern.

**W6. `host-runner self-update` subcommand (~200 LOC).**
- New case in `hub/cmd/host-runner/main.go` switch.
- Resolves version: explicit `--version` > `--channel stable`
  (latest non-prerelease) > `--channel alpha`.
- Resolves release source: `--upstream-repo <owner>/<name>` flag
  > `release_source:` field in hub config > default
  `physercoe/termipod`.
- Fetches the **host-runner-only** release artifact from GitHub
  (`https://api.github.com/repos/<owner>/<repo>/releases/tags/<tag>`),
  pattern `termipod-host-runner-<tag>-<os>-<arch>.tar.gz` per
  W5.5.
- Downloads tarball + `SHA256SUMS` to `<install_dir>/.host-runner.new.tgz`
  and `.SHA256SUMS`.
- Verifies SHA256 against the entry in `SHA256SUMS` matching the
  host-runner-only artifact name.
- Extracts the single `host-runner` binary, `os.Rename()` into
  `argv[0]`.
- Exits **75** so systemd's `Restart=on-failure` respawns with
  the new binary.
- If any step fails, exits 1 (a generic failure code that does
  trigger respawn — same binary, host doesn't go dark) and
  leaves the old binary untouched.

**W7. `hub-server self-update` subcommand (~50 LOC).**
- Same shape; fetches the **hub-server-only** artifact
  (`termipod-hub-server-<tag>-<os>-<arch>.tar.gz`).
- Install path defaults to `argv[0]` for hub.
- Shared code in `hub/internal/selfupdate/` parameterized by
  binary name; both subcommands call the same primitive with
  different artifact-name prefixes.
- Exits 75 — hub's own `termipod-hub.service` respawns it.

**W8. `host.update` tunnel verb (~80 LOC).**
- Host-runner verb handler: invoke the same self-update routine
  used by the standalone subcommand; success means exit 75
  (systemd respawns).
- Reports progress milestones (downloading, verifying, renaming)
  back via the tunnel response so `update-all` can show per-host
  status.

**W9. `hub-server update-all` orchestrator (~80 LOC).**
- For each host: fire `host.update` verb, stream progress.
- Errors fail-fast per host (the operator can re-run targeted at
  just that host).
- After all hosts succeed, hub-server runs its own self-update
  and exits 75 (hub's systemd unit respawns with new binary).
- `--target=hosts | hub | both` (default `both`). Hub-only
  upgrades skip the host fan-out entirely and just do a hub
  self-update.
- `--dry-run` prints what would happen without acting.
- `--upstream-repo <owner>/<name>` overrides the default; flag
  is propagated into the per-host `host.update` payload so the
  host fetches from the same source as the operator intended.

**W10. Lifecycle test scenarios.**
- Scenario 26: self-update happy path — 2 hosts, update-all from
  vN to vN+1, verify both hosts respawn with new version and
  agent registry is restored.
- Scenario 27: SHA mismatch — corrupt the download path, verify
  rollback (old binary stays) and audit row says `error=sha256_mismatch`.

### 3.3 Acceptance

- `update-all` upgrades the fleet end-to-end with no SSH.
- Failed verify never replaces the binary.
- Audit log has from/to-version rows per host.

---

## 4. Phase 3 — restart-all

> **Status: shipped v1.0.634-alpha** (2026-05-19). W11–W12.
> `host.shutdown` and `host.restart` share `handleHostExit`
> (the exit code is the only difference); `handleAdminFleetShutdown`
> and `handleAdminFleetRestart` share `fleetStopVerb`; the
> `shutdown-all` / `restart-all` CLIs share `runFleetStop`. Scenario
> 28 is in `test-steward-lifecycle.md`.

### 4.1 Goal

Operator runs `hub-server restart-all`. Equivalent to
`shutdown-all` but the supervisor immediately brings each
host-runner back up with the *current* binary (no upgrade
implied).

### 4.2 Wedges

**W11. `host.restart` verb + `restart-all` subcommand (~80 LOC).**
- Verb handler in host-runner: same termination as `host.shutdown`
  but exits **75** instead of 0, so systemd respawns with the
  same binary.
- `hub-server restart-all` mirrors `shutdown-all` but fires
  `host.restart` instead of `host.shutdown`.
- Audit row uses `action=host.restart` to distinguish from a
  true shutdown.

**W12. Lifecycle test scenario.**
- Scenario 28: restart-all preserves the agent registry; agents
  that were live before are reattached after (where the engine
  supports it via session_id). Verify hosts respawn within ~5s
  of exit (systemd `RestartSec=3s`).

### 4.3 Acceptance

- Fleet restart with one command; agents come back where engine
  supports resume.

---

## 5. Phase 4 — ops fleet

Each wedge is independent; ship in any order based on operator
demand.

> **Status (2026-05-19):** all 9 wedges shipped as **v1.0.635-alpha**.
> New surface added by the phase: a
> read-side `host.ping` tunnel verb + a `host.token_rotate` verb; the
> owner-gated `/v1/admin/hosts`, `/v1/admin/agents`,
> `/v1/admin/agents/{id}/kill`, `/v1/admin/hosts/{id}/ping`, and
> `/v1/admin/tokens/rotate` endpoints; and the `hub-server`
> subcommands `doctor` / `version` / `hosts` / `agents` / `db` /
> `logs` / `tokens rotate` plus `host-runner doctor`. Host-runner now
> persists a rotated bearer to its state dir so a rotation survives a
> restart. See each wedge for divergences from the original sketch.

**✅ W13. `hub-server doctor` (~80 LOC).**
- Preflight: DB writable, listen port free, certs valid,
  host-runners reachable, disk space > 1GB free, journald
  forwarding configured.
- Output: green/red per check + remediation hint.

**✅ W14. `hub-server version [--remote]` (~30 LOC).**
- Embed git SHA + build date via `-ldflags`.
- `--remote` fans out to each host and reports the version
  string they're running. Use a new lightweight verb
  `host.version` (or piggyback on `hosts ping`).

**✅ W15. `hub-server hosts ls` + `hosts ping <id>` (~80 LOC).**
- `ls`: live reachability + last-seen-poll + version. Plain text
  + `--json` for scripting.
- `ping`: round-trip a `host.ping` verb (returns timestamp);
  confirms tunnel end-to-end.

**✅ W16. `hub-server logs tail` (~50 LOC).**
- Tails the **local** hub journald unit (`journalctl -u
  termipod-hub.service`); `--lines` / `--follow` / `--unit`.
- *Divergence from the original sketch:* the `--host` fan-out and
  the `host.logs.tail` streaming verb were dropped. The tunnel is
  request/response — live streaming would be a protocol extension —
  and a host's logs are read on the host. Cross-host log streaming
  over the tunnel is out of scope (noted in §7).

**✅ W17. `hub-server agents kill --all` / `kill <id>` (~40 LOC).**
- Owner-gated `GET /v1/admin/agents` (live by default, `?all=1` for
  terminal rows) + `POST /v1/admin/agents/{id}/kill`. The kill path
  flips the agent to `terminated` and runs the shared
  `applyAgentTerminationEffects` — extracted from `handlePatchAgent`
  so a CLI kill and a mobile Stop produce an identical audit trail.
- `agents kill --all` iterates the live list; idempotent on an
  already-terminal agent. `agents ls` exposes the list directly.

**✅ W18. `hub-server db vacuum` (~30 LOC).**
- Runs sqlite `VACUUM` (a whole-database rebuild — sqlite has no
  per-table form) and reports the `events` / `audit_events` row
  counts plus the file size before and after.

**✅ W19. `hub-server db migrate` (~50 LOC).**
- Promotes the migration step into an explicit preflight call:
  `db migrate` applies pending migrations and reports the
  golang-migrate version (dirty state exits 1).
- *Divergence:* `serve` keeps auto-migrating (it always has, via
  `OpenDB`); the `--no-migrate` opt-out was dropped — it would
  need a `server.New` refactor and the value is marginal.

**✅ W20. `hub-server tokens rotate` (~260 LOC).**
- `POST /v1/admin/tokens/rotate` (owner-scope): issue a new host
  token (scope templated off an existing one), broadcast it via the
  `host.token_rotate` verb, then revoke the prior host tokens —
  but **only once every live host has acked**, so an un-acked host
  keeps working. `--force-revoke` overrides for recovery.
- Brick-safety: host-runner persists the rotated bearer to its
  state dir (`host-runner.json`) and prefers it over `--token` on
  startup, so a restart does not re-authenticate with the revoked
  token. The verb refuses (no ack) if it has no state dir to
  persist into. The host swaps the live bearer before acking, so
  the ack itself rides the new token.
- *Larger than the ~100-LOC sketch:* the persistence path
  (`Client` token swap under a mutex, state-file token field,
  startup resolution) is the bulk of the wedge.

**✅ W21. `host-runner doctor` (~60 LOC).**
- Host-side preflight: hub reachable, install token valid,
  required engines on PATH (`claude` / `codex` / `gemini` /
  `kimi-code`), MCP catalog parses, scratch dir writable.

### 5.1 Acceptance

- Each wedge is shippable in isolation.
- All subcommands honor `--json` for scripting.
- Audit log gains a row for any write-side action (tokens rotate,
  agents kill, db vacuum); read-side commands don't write to
  audit.

---

## 6. Phase 5 — Mobile Admin pane

### 6.1 Goal

The hub owner can drive Phases 1-4 from the mobile app.

### 6.2 Wedges

**W22. Admin REST endpoints (~120 LOC).** ✅ shipped v1.0.636.
- `fleet/shutdown`/`update`/`restart` and `tokens/rotate` already
  shipped in Phases 1-4. W22 added the remaining routes under
  `/v1/admin/`: `hosts/{host}/shutdown`, `hosts/{host}/restart`,
  `hosts/{host}/update`, `db/vacuum`, and a cross-team audit query
  `GET /v1/admin/audit` (action-prefix match — the team-scoped
  `/v1/teams/{team}/audit` only matches `action` exactly).
- *Divergence from the sketch:* route prefix is `hosts/{host}/…`
  (plural, `{host}` param) to match the Phase 4
  `hosts/{host}/ping` route, not the `host/{id}/…` of the sketch.
- The per-host shutdown/restart/update handlers delegate to the
  refactored `stopOneHost` / `updateOneHost` helpers extracted
  from the fleet handlers — so a per-host action produces the
  identical session-stop + `audit_events` trail as the fleet path.
- All owner-scope (`requireOwner`, the Phase 1 gate). Member
  tokens get 403.

**W23. Mobile Admin pane (~400 LOC Flutter).** ✅ shipped v1.0.636.
- New `lib/screens/admin/admin_screen.dart` and supporting
  widgets.
- **Reached from a second AppBar action on `HubDetailScreen`**
  (`lib/screens/hosts/hub_detail_screen.dart`), sitting next to
  the existing "Hub config (owner)" `Icons.tune` button — *not*
  a sixth bottom-nav tab (the five-tab IA is fixed) and *not* a
  whoami pre-probe. This matches the established owner-action
  idiom: the button shows unconditionally and the `AdminScreen`
  itself surfaces the 403 ("requires an owner-kind token"), the
  same way `HubRolesConfigScreen` does. Governance config and
  fleet admin share one owner audience and one entry surface.
- Per-host status row pulling from `/v1/admin/hosts` (extends
  Phase 4 W15 endpoint).
- Buttons per row + fleet-wide buttons. **Long-press + slide**
  to confirm destructive actions; single tap shows a status
  hint only.
- Audit-log strip at the bottom listing the last 50 admin
  actions pulled from `audit_events?action=host.*`.

**W24. Confirmation gesture widget (~80 LOC Flutter).** ✅ shipped v1.0.636.
- `lib/screens/admin/confirm_action_tile.dart` —
  `ConfirmActionTile` requires long-press + horizontal slide to
  fire. A progress fill grows under the tile during the slide;
  release before the threshold and nothing happens. A plain tap
  surfaces the gesture hint via SnackBar — it never fires.
- Used for every fleet-wide and per-host action in W23.

**W25. Audit-log query screen (~100 LOC Flutter).** ✅ shipped v1.0.636.
- `lib/screens/admin/admin_audit_screen.dart` — filterable view
  of `audit_events` over `GET /v1/admin/audit`.
- Filters: action prefix, target_kind, time window, actor handle.
- Reached from the Admin pane's AppBar and its audit strip.
- *Divergence:* the "surfaces from the Activity tab too" idea is
  deferred — the Activity tab is team-scoped and member-visible,
  whereas this screen is the owner-scope cross-team query. Wiring
  it into Activity would need a scope split; left as a follow-up.

**W26. Lifecycle test scenario.** ✅ shipped v1.0.636.
- Scenario 29 in `test-steward-lifecycle.md`: the mobile admin
  happy path plus the plain-tap guard, the offline-host disable,
  the audit trail, and the member-token 403 negative check.

### 6.3 Acceptance

- Owner can shutdown / update / restart the fleet from mobile.
- No member-scope token can hit `/v1/admin/*`.
- Every admin action shows in the audit log with the operator's
  identity.

---

## 7. Open follow-ups (not in this plan)

- **Cosign signing** for self-update artifacts (upgrade D-4 to
  the "better" tier).
- **macOS launchd plist** for non-systemd hosts.
- **`--drain` flag** on shutdown-all (wait for in-flight tool
  calls).
- **Multi-team admin scope** if hub ever hosts multiple teams.
- **`hub-server doctor --remote`** that runs doctor on every
  host too (Phase 4.5 polish).
- **Cross-host log streaming** — `logs tail --host <id>` over a
  streaming tunnel verb. W16 shipped local-only; this needs the
  request/response tunnel to gain a streaming/chunked mode.
- **Audit screen on the Activity tab** — W25's audit query screen
  is owner-scope and cross-team; surfacing it from the
  (team-scoped, member-visible) Activity tab needs a scope split.
- **`serve --no-migrate`** — the Phase 4 W19 opt-out, still
  deferred (would need a `server.New` refactor).

## 8. Status forward-links

- ADR: [decisions/028-host-control-via-tunnel-and-cli.md](../decisions/028-host-control-via-tunnel-and-cli.md)
- Discussion: [discussions/host-control-and-cli-surface.md](../discussions/host-control-and-cli-surface.md)
- Tunnel reference (today): `hub/internal/server/server.go:220-221`
- Systemd units: `hub/deploy/systemd/`
