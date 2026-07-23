# Environment profiles + host-to-host session teleport — two control-plane primitives

> **Type:** plan
> **Status:** Draft — for maintainer review
> **Audience:** contributors, maintainer
> **Last verified vs code:** main @ `c97c522c`, 2026-07-23

**TL;DR.** Two gaps surfaced by the cloud-agent comparison (Claude Code on the
web / Codex cloud), grounded in the current codebase:

1. **Environment profiles** — Claude/Codex web treat *setup script + env vars
   + secrets + network policy* as a first-class, reusable entity attached to
   tasks. termipod has only the zero-knowledge vault (a personal password
   manager, not wired to spawns): templates/families/spawn-spec have **no
   env or setup fields at all**, nothing but the hub-minted MCP token is
   injected into spawns, and the egress proxy does URL-masking only. This
   plan adds a team-scoped `env_profiles` entity whose *secret values never
   leave the vault* — profiles carry **references**, resolved client-side and
   delivered to the target host as a per-spawn encrypted envelope
   (hub-blind, honoring forbidden-pattern #15).
2. **Session teleport** — Claude's `--teleport` moves a session + its branch
   between environments. termipod's resume primitive already does
   "terminate + respawn + keep session row + splice engine cursor" — but
   hard-pinned to the *same* host (`handlers_sessions.go:691`), with the
   engine session store stranded on the source host's disk. This plan adds
   host-to-host teleport: a hub endpoint that re-targets the existing
   resume machinery, plus an **engine-state bundle** (per-family declared
   state paths, tar → hub blob → target host) so cross-host resume doesn't
   cold-start, and branch/workdir handoff for the worktree and non-worktree
   cases.

Companion to [`agent-transcript-redesign.md`](agent-transcript-redesign.md)
(merged) and the project/task board plan (PR #364).

---

## Part 0 — Grounding (what exists today, verified)

### 0.1. Secrets / spawn config / network policy

**Vault = zero-knowledge blind blob (ADR-052).** Hub stores ciphertext only
(`handlers_vault.go:76-408`: `key_vaults` + per-device wrapped keys +
recovery envelope, migrations `0061`/`0065`); all crypto lives client-side
(`desktop/vault-wasm`, `electron/src/ipc/keychain.ts` safeStorage). Bundle
contents (`desktop/src/vault/bundle.ts:24-37`): connections, SSH keys,
passwords, generic items, sync config. Generic item types
(`desktop/src/state/vaultItems.ts:23-35`) include **`env`** (dotenv/rc blob)
and **`script`** (setup snippet, executable via `script_run`) — present but
**not wired to anything**.

**Injection into spawns: essentially absent.** Only the hub-minted per-agent
MCP bearer token (`handlers_agents.go:1468`) and a best-effort codex
`auth.json` copy (`launch_m2.go:890-900`). Everything else is inherited from
the host user's home/process env; `auditAuthEnv`
(`hub/cmd/host-runner/main.go:404-424`) merely logs presence.

**No env/setup fields anywhere.** Spawn request (`handlers_agents.go:687+`),
spawn spec (`hostrunner/spec.go:13-80`: backend cmd/workdir, worktree,
context_files, resume_session_id — no env, no hooks), templates
(`hub/templates/agents/*.yaml` — the only "env" is an inline cmd prefix like
`cmd: "CODEX_HOME=.codex codex"`), families (`agent_families.yaml` — schema
grep for env/setup: zero). Hardcoded per-engine env only:
`GEMINI_CLI_TRUST_WORKSPACE` (`launch_m1.go:89-94`, `launch_m2.go:350`).
The docs flag the gap themselves:
`docs/discussions/multi-agent-harness-landscape.md:627-630` — *"no
per-project hook for 'copy these files / run this script before the agent
starts.'"*

**Network policy: absent.** `egress_proxy.go:1-151` is a loopback reverse
proxy whose sole purpose is masking the hub URL from agents — no allowlist,
no per-agent rules, no traffic logging (header comment is explicit). Real
egress/sandbox policy is deferred post-MVP
(`docs/reference/permission-model.md:98`).

**Reusable building blocks:** `context_files` materialization
(`launch_m2.go:532-562`, path-escape-guarded), `DeriveWorkdir`
(`spec.go:139-210`), vault `env`/`script` item types, `script_run` executor
(`electron/src/ipc/script.ts:99`), the egress-proxy loopback injection point
(`runner.go:570-697`), per-family MCP config writers (`launch_m2.go:620-633`
+ materializers).

### 0.2. Hosts / sessions / resume

**Host = `(team, name)` + bearer + liveness + capability probe** (migrations
`0001`/`0008`; registration `runner.go:243-270`; heartbeat 10s, offline after
90s `host_sweep.go`). Capabilities map family → {installed, version,
supports} + static HostInfo (`capabilities.go:26-37`). **No host identity
keys** beyond the bearer token.

**Sessions are hub-global; host placement is an attribute of the current
agent** (`sessions` has no host_id; resolution via
`current_agent_id → agents.host_id`). Transcript is host-independent
(`agent_events.session_id`) — **portability of conversation history is
free**.

**Resume = the teleport-shaped primitive, host-pinned.**
`resumePausedSession` (`handlers_sessions.go:583-739`): read dead agent's
`handle, kind, host_id, project_id`, splice engine cursor per kind
(`resume_splice.go`: claude `--resume`, ACP `session/load` for
gemini/kimi/kimi-ts, codex `thread/resume`, antigravity `--conversation`),
respawn **pinned to the same `HostID`** (`:686-698`).
`respawn_with_spec_mutation.go` repeats the pattern for mode/model switches.
Engine cursors live in `sessions.engine_session_id`; engine session stores
are **host-local disk files** (`~/.claude/projects`, `.gemini/sessions`,
codex thread store, kimi `~/.kimi-code/sessions`) — ADR-014:171-178 documents
the single-host assumption.

**Absent for mobility:** no `sessions.host_id`, no move endpoint, no
workdir/git sync (worktree source repo is a host-local path; branch
`hub/<handle>` implied only inside `spawn_spec_yaml`), no file-transfer
between hosts (`/v1/blobs` exists, `client.go:312-320`, but nothing
workdir-shaped rides it), no failover docs. Spawn targeting itself is
host-flexible (`checkSpawnHostReachable`, pull-based delivery per host) —
only the resume path is pinned.

## Part 1 — Environment profiles (first-class `env_profiles`)

### Design

**Entity (hub, team-scoped):** `env_profiles(id, team_id, name, description,
setup_script, env_vars_json, secret_refs_json, network_policy_json,
created_at, updated_at, deleted_at)`.

- `setup_script` — bash run in the workdir before the agent cmd. Hub-visible
  (not secret; secrets don't belong in scripts).
- `env_vars` — plain `KEY=value` map. Hub-visible.
- `secret_refs` — **references, not values**: `[{key: "OPENAI_API_KEY",
  vault_item: "openai-prod"}]`, pointing at items in the team's
  zero-knowledge vault. The hub never sees the values.
- `network_policy` — `{mode: "open"|"allowlist"|"offline", allowlist: [...]}`.
  Declarative in E1; enforcement lands with the egress-proxy rules work
  (cross-link `permission-model.md:98`).

**Secret delivery honoring forbidden-pattern #15 (the ADR-worthy piece).**
The vault threat model says the hub never holds usable secrets. So:

1. **Host device keys (new).** Host-runner generates an X25519 keypair at
   registration; the public key rides `capabilities_json`. Same shape as
   ADR-052's per-device vault-key wrapping (D-4), extended to hosts.
2. **Envelope per spawn.** When a spawn references a profile with
   `secret_refs`, the *client* (which holds the vault key) resolves the refs,
   builds `{KEY: value}`, and seals it to the **target host's public key**.
   The envelope is stored on the spawn row as opaque ciphertext — the hub
   carries ciphertext it cannot decrypt, exactly the pattern ADR-052 D-5
   already legitimizes.
3. **Host injection.** Host-runner unseals at launch, exports into the child
   env (never written to disk; never logged — `auditAuthEnv`-style
   present/absent logging only), scrubs on exit.

**Consumption at spawn.** `spawn_spec` gains `env_profile_id` +
`env_profile_rev` (**snapshot semantics**: the spawn pins the profile
version, so later profile edits don't mutate running history) and
`env_secret_envelope`. Merge order for env: profile `env_vars` < template
cmd-prefix tricks (unchanged) < engine-hardcoded vars (existing) < sealed
secrets (win). Setup script: `spec.setup_script` + failure policy
(`fail` default — don't start the agent on a broken env; `continue`
opt-in), executed after `DeriveWorkdir` + worktree creation, before cmd.

**Attach points.** Project `config_yaml` gains `env_profile_id` (inherits to
all spawns in the project); spawn sheet gets an override picker; templates
may name a default profile. Profile CRUD UI: desktop Settings section +
mobile management screen; "import from vault env item" shortcut (the
existing `env`/`script` vault item types finally get a consumer).

### Wedges (Part 1)

- **E1 — Entity + plain env + setup script.** Migration, REST CRUD, spec
  fields, host-runner env merge + setup-script execution, snapshot semantics.
  No secrets yet (`secret_refs` accepted, ignored with a loud log line).
- **E2 — Attach points + UI.** Project config field, spawn-sheet picker,
  desktop/mobile profile management, vault-env-item import.
- **E3 — Secret refs with host envelopes (needs its own ADR).** Host key
  enrollment via capabilities, client-side seal, host-side unseal+inject.
- **E4 — Network policy enforcement.** Egress-proxy per-agent allowlist
  rules + offline mode (env scrubbing); jointly designed with the deferred
  sandbox work (`permission-model.md:98`).

## Part 2 — Session teleport between hub-registered hosts

### Design

**New endpoint** `POST /v1/teams/{team}/sessions/{id}/teleport
{target_host_id}`. Teleport = the existing `resumePausedSession` pattern
re-targeted, in one orchestrated flow:

1. **Validate.** Session active or paused; target host online
   (`checkSpawnHostReachable`), capability supports the agent's family
   (`capabilities_json`), and — for worktree sessions — the target confirms
   the source repo is reachable (a new lightweight host precheck verb).
2. **Hand off the workdir state.**
   - *Worktree sessions (T1)*: source host commits WIP onto `hub/<handle>`
     and pushes to the shared remote; target clones/fetches and
     `git worktree add`s the same branch. (Hosts sharing no remote → relay
     bundle through a hub blob; T3.)
   - *Non-worktree (T2)*: tar the derived workdir (size-capped, excludes
     `.git` objects already on the branch where applicable) → hub blob →
     target untars into its own `DeriveWorkdir` path.
3. **Hand off the engine state (the anti-cold-start piece).** Families
   declare `state_paths` globs in `agent_families.yaml` (kimi(-ts):
   `~/.kimi-code/sessions/<wd>/<session>`; claude:
   `~/.claude/projects/<slug>/<id>.jsonl`; gemini: `.gemini/sessions`;
   codex: thread store). Source host tars the matched files (**engine-state
   bundle**) → hub blob → target restores them to the same logical
   locations, remapping the workdir path segment where the engine embeds it
   (kimi's `wd_<cwd>_<hash>` dir naming makes this mechanical; claude's
   project slug likewise). The cursor in `sessions.engine_session_id` then
   resolves on the target exactly as it did on the source.
4. **Swap.** Enqueue graceful `terminate` on the source **only after** the
   target spawn reports ready (terminate-after-verify); splice the cursor
   via the existing `resume_splice.go`; session row flips to the new agent
   (same session, `agent_ids_json` accumulates); spec's
   `spawn_spec_yaml` re-derives workdir/host fields. Failure before the swap
   leaves the source session untouched.
5. **Degraded fallback.** If a family has no `state_paths` declared (or the
   bundle is missing/corrupt), cold-start on the target with a
   client-generated session digest dropped as a context file — universal,
   lossy, explicit in the UI.

**UX.** Teleport action on the session (desktop session menu + mobile
session details): target-host picker filtered by capability/reachability;
progress phases (`packing → transferring → spawning → verifying →
terminating old`); the transcript never leaves the hub, so the conversation
view is continuous across the move.

**Not in scope (T3, recorded):** auto-failover on host-offline (needs a
policy decision: which sessions are worth moving, and where), host drain
mode, live mid-turn migration, hub-side bare-repo relay for hosts sharing
no remote.

### Wedges (Part 2)

- **T1 — Teleport for worktree sessions + engine-state bundles for
  kimi-code(-ts) and claude-code.** Endpoint + orchestration + branch
  push/pull handoff + `state_paths` for the two best-understood stores +
  terminate-after-verify. Desktop action first.
- **T2 — Non-worktree sessions.** Workdir tar relay via blobs; remaining
  families' `state_paths`; mobile action.
- **T3 — Failover/drain/relay polish.** Host-offline auto-teleport policy,
  drain mode, bare-repo relay, telemetry.

## Security & ADR considerations

- **New ADR required (E3):** extends ADR-052's per-device wrapping to hosts;
  affirms hub-blindness (envelopes + engine-state blobs are ciphertext or
  transcript-equivalent user data the hub already carries).
- **Engine-state bundles contain conversation content** — same sensitivity
  as `agent_events`, which the hub already stores. Flagged explicitly; teams
  with stricter models can opt teleport out per team policy.
- **Workdir tars may contain secrets the agent wrote** — size caps,
  no-logging rule, blob TTL on successful teleport, optional
  seal-to-target-host encryption reusing E3's host keys (recommended once E3
  lands).
- **Setup scripts run as the host user** — same trust level as the agent
  cmd itself; no new privilege. Failure policy defaults to fail-closed.

## Open questions for the maintainer

1. **Profile scoping**: team-wide only, or also per-project overrides of
   individual keys (project `env_vars` merged over profile)? Proposal: team
   profiles + project-level `env_profile_id` attachment only; per-key
   overrides via a second profile.
2. **secret_refs resolution authority**: only the desktop (vault-key holder)
   can author a spawn with secrets — is mobile expected to resolve refs too
   (it has vault access via the same bundle)? Proposal: yes, both clients;
   the hub-side spawn API rejects `secret_refs` it can't see resolved into
   an envelope.
3. **Engine-state blob sensitivity**: acceptable as hub-visible user data
   (transcript-equivalent), or must T1 wait for E3 host-key sealing?
   Proposal: ship hub-visible with the explicit flag; seal in T3 via E3.
4. **Teleport of `paused` sessions only, or also active (mid-idle)?**
   Proposal: active-but-idle allowed (terminate-after-verify covers it);
   mid-turn refused with a busy error.
5. **Non-git workdirs in T2**: tar size cap (proposal: 256 MB compressed,
   refuse larger with a clear error) — or stream via a host-to-host channel
   where reachable? Proposal: cap + clear error; NAT makes direct channels
   the exception.
