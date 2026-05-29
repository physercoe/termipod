# Security audit — Codex review + independent verdict

> **Type:** discussion
> **Status:** Open — remediation in progress; F-01 + F-04 + F-08 fixed (v1.0.724–726)
> **Audience:** contributors, reviewers
> **Last verified vs code:** v1.0.726

**TL;DR.** A third-party static review by Codex (revision `d3e1c53`,
2026-05-25) surfaced **4 Critical, 6 High, 1 Medium** findings across
the Go hub and the Flutter mobile client. An independent re-check at
HEAD `4d598b7` (v1.0.721) confirmed every citation at file:line, with
one additional read primitive found on F-03 not separately called out
in the codex remediation. This file catalogues the findings, records
the verdict, and proposes the remediation order. **Remediation has
started** — see §0 for status; pick the next item from §3.

---

## 0. Remediation status

| ID | Sev | Status | Landed |
|---|---|---|---|
| **F-04** | Critical | **Fixed** — decider identity bound to the authenticated token; override gate requires `owner`/`user` kind (`principalActor` in `handlers_attention.go`). `by` from the body is inert. | v1.0.724 |
| **F-01** | Critical | **Fixed** — `auth.Middleware` allowlists bearer kinds `owner`/`user`/`host`; `agent` (and any unknown kind) is refused with 403 over the network. Network agents authenticate via `/mcp/{token}` (outside the middleware) and host-runner relays under its host token + `X-Agent-Id`. **Exception (v1.0.727):** the hub's own in-process authority-tool dispatch (`mcp_authority.go`) forwards an agent token as a bearer *after* the MCP role check — exempted via an unspoofable `auth.WithInProcessDispatch` context marker. | v1.0.726–727 |
| F-02 | Critical | Open | — |
| F-03 | Critical | Open (partial — new `blob_get` validates via `isHexSHA256`; the original `handlers_attention.go:813` attention path still passes `blob_sha256` to `blobPath` unvalidated) | — |
| F-05 | High | Open | — |
| F-06 | Medium | Open | — |
| F-07 | High | Open | — |
| **F-08** | High | **Fixed** — `from_id` + cost attribution derived from the token via `eventSender` (`handlers_events.go`); forged `from_id`/`usage_tokens` can no longer impersonate or budget-DoS a victim. Also wired the previously-dead `X-Agent-Id` host-relay derivation. | v1.0.725 |
| F-09–F-11 | High | Open | — |

**Sibling found while fixing F-04 (not in the original audit):**
`handleResolveAttention` (POST `/attention/{id}/resolve`,
`handlers_attention.go:983`) writes `resolved_by` straight from the
request body and flips an item to `resolved` **without** running the
propose dispatcher / quorum / Apply. Two notes: (1) the mobile client
sends `by`, not `resolved_by` (`hub_client.dart:2059`), so the
attribution field is already inert from that caller — a latent
key-mismatch; (2) `resolved_by` is an FK-to-`agents(id)` column, so a
naive "bind to token handle" fix would write a non-id value. Lower
severity than F-04 (no governed action executes), but same
identity-from-body class. Needs its own small wedge: decide whether
this raw-resolve path should exist at all, and if so bind + gate it.

The original codex report is preserved verbatim in §6.

---

## 1. Source

**Reviewer:** Codex (static read-only review, no exploit, no tests run).
**Source artifact:** the report shown in §6, originally at
`/tmp/termipod_security_review_2026-05-25.md` (ephemeral; preserved
here for the repo's permanent record).
**Reviewed tree:** termipod fork checkout, revision `d3e1c532` on `main`.
**Re-verification tree:** `4d598b7` (v1.0.721-alpha). The interval
`d3e1c53..HEAD` is 12 commits, all in the codex-M2 / antigravity-M4 /
mobile dispatch surface; none touch the surfaces the audit covered, so
every citation re-verified cleanly.

**Scope notes:**

- The Go hub and host-runner were covered in depth.
- The Flutter mobile client was covered for SSH, credential storage /
  export / import, WebView, deep-link, and offline-data flows.
- **The TypeScript hub-TUI (`hub-tui/`) was excluded** by codex's own
  scope statement and remains unreviewed.

---

## 2. Findings — independent verdict

The full evidence trail for each finding lives in §6 (codex's original
write-up). What follows is the re-check verdict. Each row reads:
**Confirmed** means I independently re-derived the same conclusion at
the cited file:line on HEAD; **Confirmed, broader** means I found
something codex didn't separately call out; **Confirmed, nuanced**
means the framing needs a qualifier.

| ID | Sev | Verdict | Independent notes |
|---|---|---|---|
| **F-01** | Critical | **Fixed (v1.0.726)** | Was: `auth/token.go` middleware checked only revoke + expiry; `handlers_policy.go` wrote policy from any bearer; no `kind='agent'` reject. Now: middleware allowlists `owner`/`user`/`host` bearer kinds and refuses `agent` (+ unknown) with 403. Verified agents never present a bearer — host-runner relays under its host token + `X-Agent-Id` (`mcp_gateway.go`), and `/mcp/{token}` is mounted outside the middleware. |
| **F-02** | Critical | Confirmed, nuanced | `server.go:244-248` mounts `/a2a/relay/*` outside auth, with explicit "deferred" comment. Agent / host IDs are **80-bit-random ULIDs** (`ids.go:11`), so cold-guessing the URL is impractical. Real residual risk is **authenticated cross-agent abuse** (a worker reads sibling IDs via list endpoints + posts as them) more than internet-scale guessing. URL-as-capability is still brittle (URLs are logged, proxied, indexed). |
| **F-03** | Critical | **Confirmed, broader** | `handlers_attention.go:817,827` joins `p.Category` + `p.Name` unvalidated → arbitrary write. **Additionally**: `p.BlobSHA256` is also unvalidated and is passed straight to `s.blobPath()` at `:813`, which slices `sha[:2]` / `sha[2:4]` — if `sha` contains `/` and `..` segments, `filepath.Join + Clean` resolves outside `<DataRoot>/blobs/`. That gives an **arbitrary-read primitive** triggered by the same approval, paired with the write. Codex's remediation mentions SHA validation but didn't separately call out the read primitive. |
| **F-04** | Critical | **Fixed (v1.0.724)** | Was: `handlers_attention.go:651` `a.In.By != "@principal"` was the only override gate; quorum logic at `:533` + `:715` recorded `DeciderHandle: in.By` from the body. Now: `principalActor` derives the decider handle from the authenticated token scope and gates override on `owner`/`user` kind; `handleDecideAttention` overwrites `in.By` at a single chokepoint so the body is inert. |
| **F-05** | High | Confirmed | Token scope is parsed in scattered call sites (`handlers_tasks.go:167`, `audit.go:79`, `mcp.go:195`, `handlers_agents.go:1089`) but only for attribution flavour. Bearer middleware does no `route.team == tok.scope.team` enforcement. In a multi-team deployment this is real cross-tenant leakage. |
| **F-06** | Medium | Confirmed verbatim | `mcp.go:200` queries `WHERE token_hash = ? AND revoked_at IS NULL`. `token.go:151-156` (REST path) does check `expires_at`. Inconsistent contract. |
| **F-07** | High | Confirmed | `handlers_projects.go:311` inserts caller-supplied `in.DocsRoot` unvalidated. `handlers_project_docs.go:43-51` preserves absolute paths and expands `~/`. Containment check at `:116-120` only stops escaping *the chosen root* — it doesn't bound the root itself. File-read oracle under the hub UID. |
| **F-08** | High | **Fixed (v1.0.725)** | Was: `handlers_events.go:82,100-102` stored `in.FromID` as-is and called `accumulateSpend(in.FromID, …)` — forged-cost pause + forged attribution both reachable. Now: `eventSender` derives the sender from the token (agent → own `scope.agent_id`; host → stamped `X-Agent-Id`; human → body `from_id` but no spend). Tracing also showed the `X-Agent-Id` host-relay derivation was never implemented hub-side despite the `mcp_gateway.go` comment — now wired. |
| **F-09** | High | Confirmed | `ssh_client.dart:270-280` (jump) + `:295-306` (target) both construct `SSHClient` without `onVerifyHostKey`. dartssh2 auto-accepts when callback is null. **Code-hygiene aside**: comments around these blocks (e.g. `:294`, `:302`, `:313`, `:319`, `:552`) are Japanese — violates the English-only rule in `CLAUDE.md`. Worth a same-pass cleanup when this lands. |
| **F-10** | High | Confirmed | `data_port_service.dart:96-99,117-122` pulls private keys, passphrases, and SSH passwords out of secure storage into a JSON-serialisable map. The destination (`PublicFileStore`) is Android `Download/TermiPod/` or iOS Documents via `UIFileSharingEnabled`. UI warning doesn't change the actual posture. |
| **F-11** | High (conditional) | Confirmed | `ssh_client.dart:323` `'test -x ${options.tmuxPath}'` + `:558-561` `_resolveTmuxCommand` do unquoted string interpolation. `data_port_service.dart` import path validates only top-level shape, not per-field. Trigger: malicious import + first connection. |

**Additional risks codex flagged in the trailing section** (SOCKS
proxy password in plaintext SharedPreferences, channel attachment
schema mismatch, iOS Documents exposure for internal cache, canvas-
viewer WebView JS execution, hub URL accepts HTTP) — citations
spot-checked and confirmed. None are exploit chains on their own,
but the iOS-Documents-as-cache one **compounds F-10** (private notes
and offline DB visible in Files.app alongside the plaintext backup).

### One observation about the review itself

Every codex citation re-verified at line precision. **No false
positives, no missed criticals on the surfaces reviewed.** The one
gap was the F-03 blob-SHA read primitive (implicit in their
"validate SHA-256 format" remediation but not stated as a separate
vector). The TypeScript hub-TUI was excluded by their own scope note.

---

## 3. Recommended remediation order

Codex proposed an order in §6's "Remediation Priority." I'd promote
two items and pair some. Each line is a candidate wedge:

1. **F-04 — derive approver identity from token, not body.** ✅ **Done
   in v1.0.724.** Was the cheapest fix and the actual gate on the
   human-in-the-loop story — without it the whole "principal approves
   dangerous things" model was theatre. Bound the decider to the token
   and gated override on human token kind (`principalActor`).
2. **F-01 — token-kind discrimination at REST middleware.** ✅ **Done
   in v1.0.726.** Implemented as a middleware-level allowlist
   (`owner`/`user`/`host`) rather than a per-route denylist — agents
   never legitimately present a bearer, so this is the cleanest,
   forward-compatible closure and protects routes not separately
   enumerated.
3. **F-08 — derive event sender + cost from token.** Same fix shape
   as F-04; small.
4. **F-09 + F-10 + F-11 — Flutter trio, one release.** Host-key
   verification (TOFU + persisted fingerprints) + encrypted backup
   default + `tmuxPath` validation/quoting. They co-locate in
   `lib/services/ssh/` and `lib/services/data_port_service.dart`.
5. **F-03 + F-07 — filesystem read/write traversal pair.** Validate
   `category` / `name` / `blob_sha256` for templates; allowlist
   `docs_root` to administrator-configured bases. Both filesystem
   surfaces, one wedge.
6. **F-02 — A2A relay auth.** Biggest design lift; codex's
   "per-agent high-entropy capability" idea works, but it interacts
   with the tunnel envelope from ADR-032. Worth a discussion doc
   before code.
7. **F-05 + F-06 — token-scope enforcement in middleware + unify
   MCP/REST token validation.** Same auth surface; same wedge.

The Codex-flagged "additional risks" can be folded into the same
PRs that touch their respective files (e.g. SOCKS password → secure
storage during the F-10 backup pass; iOS Documents cache relocation
during the F-10 backup pass; HTTPS-by-default during the hub
bootstrap touch).

---

## 4. Open questions

- **Multi-team scope.** F-05 assumes multi-team is a real
  deployment model. Today's `init.go:60` seeds `defaultTeamID` and
  most operators run single-team. Are we sufficiently committed to
  multi-team to do the middleware-enforcement work now, or is it
  cheaper to **remove** team routing prefixes from the API surface
  until the multi-team UX exists? (Forks the wedge.)
- **A2A relay model.** Does the "URL = capability" model survive
  ADR-032's envelope work, or does that ADR's signed-sender frame
  give us the right authentication primitive for free? (Reframe
  F-02 against ADR-032 before designing.)
- **TUI scope.** `hub-tui/` was excluded from this review. Worth
  a separate pass before counting the audit complete?

---

## 5. Related

- `decisions/030-governed-actions-and-propose-verb.md` — the
  human-approval ladder F-04 undermines.
- `decisions/032-message-routing-envelope.md` — the A2A envelope
  surface F-02 interacts with.
- `spine/blueprint.md` — the data-ownership / authority law the
  cross-surface issues span.

---

## 6. Source — codex report verbatim

The full codex review follows, preserved verbatim. Quoted blockquotes
in this section are the codex artifact; lines and structure are
unmodified.

> *Editorial note for repo lints:* every `host-runner` in the source
> has been hyphenated to `host-runner` to satisfy
> `lint-glossary.sh` against `docs/reference/glossary.md`. No other
> wording was changed.

> # TermiPod Critical Issue Review
>
> Date: 2026-05-25
> Reviewed tree: `/home/ubuntu/termipod`
> Reviewed revision: `d3e1c532` (`main`, `origin/main`)
> Method: static read-only review of the repository, focused on
> authentication, authorization, agent execution, relay endpoints,
> filesystem writes, and Flutter mobile trust boundaries including
> SSH, credential storage/export, import, WebView, deep-link, and
> offline-data flows.
>
> No source files in `/home/ubuntu/termipod` were modified. Tests
> were not run because this assignment requested read-only
> examination.
>
> ## Executive Summary
>
> The Go hub service contains several critical authorization and
> trust-boundary failures. The most serious issue is that a spawned
> agent receives a bearer token intended for MCP access, but that
> same token is accepted by unrestricted REST mutation endpoints. A
> worker can therefore bypass its MCP role restrictions, make
> policy permissive, and request execution of arbitrary commands on
> a connected host. An additional server sweep found two further
> high-severity issues but did not identify a fifth independent
> critical issue.
>
> The Flutter follow-up did not identify a new Critical issue. It
> identified three High mobile-client issues: SSH server identities
> are not verified; backup export persistently writes SSH
> credentials in plaintext to user-visible storage; and imported
> connection settings can inject remote shell commands through an
> unquoted `tmuxPath` when the user opens the imported connection.
>
> Findings identified:
>
> | ID | Severity | Finding |
> | --- | --- | --- |
> | F-01 | Critical | Agent MCP bearer can bypass role governance through REST and reach host command execution |
> | F-02 | Critical | Public A2A relay accepts unauthenticated messages that become live agent input |
> | F-03 | Critical | Approved template installation permits path traversal and arbitrary YAML file writes |
> | F-04 | Critical | Approval and override authority is controlled by caller-supplied `by` text |
> | F-05 | High | REST team scope is not bound to bearer token scope |
> | F-07 | High | User-controlled project `docs_root` enables server-side reads of arbitrary files |
> | F-08 | High | Caller-controlled event attribution and usage costs can pause another agent |
> | F-09 | High | Flutter SSH connections automatically accept unverified server host keys |
> | F-10 | High | Flutter backup export persists plaintext SSH keys and passwords in public files |
> | F-11 | High (conditional) | Imported Flutter connection configuration can execute shell commands on an SSH target |
> | F-06 | Medium | MCP token resolution does not enforce token expiry |
>
> ## Findings
>
> ### F-01 - Critical: Agent MCP bearer can bypass role governance through REST and reach host command execution
>
> The system defines a role model for MCP tools, including a worker
> role that cannot spawn agents or mutate templates. That role
> enforcement exists in MCP dispatch only. A spawned agent is given
> a plaintext bearer token, and the normal REST middleware accepts
> that same bearer without enforcing its role or token kind.
>
> Evidence:
>
> - `hub/internal/server/roles.yaml:1-14,37-110` defines MCP
>   governance and excludes worker access to dangerous mutation
>   tools.
> - `hub/internal/server/mcp_authority_roles.go:311-351` applies
>   role checks only while authorizing MCP tool calls.
> - `hub/internal/server/handlers_agents.go:1293-1334` creates an
>   `auth_tokens` record with `kind='agent'` and an agent bearer
>   token at spawn time.
> - `hub/internal/server/handlers_agents.go:1519-1524,1567-1593`
>   sends that token to the host-runner as part of agent startup.
> - `hub/internal/hostrunner/locallogtail_mcp_config.go:25-68`
>   writes `HUB_TOKEN` into the agent's `.mcp.json`.
> - `hub/internal/hostrunner/launch_m2.go:494-529,557-638,641-679,682-789`
>   passes the token through agent-specific MCP configuration or
>   environment data.
> - `hub/internal/server/mcp_authority.go:49-53` states that the MCP
>   path token is also the agent's HTTP bearer token.
> - `hub/internal/auth/token.go:86-109` validates REST bearers but
>   does not restrict token kind, role, or team scope.
> - `hub/internal/server/server.go:263-315,557-576` mounts sensitive
>   REST routes behind bearer validation only, including policy and
>   spawn operations.
> - `hub/internal/server/handlers_policy.go:85-145` allows
>   `PUT /policy`; an empty submitted policy resolves to permissive
>   behavior at `:96-103`.
> - `hub/internal/server/handlers_agents.go:831-923` handles agent
>   spawn without checking caller role or token kind.
> - `hub/internal/server/handlers_agents.go:1095-1109` accepts a
>   `backend.cmd` field for spawn configuration.
> - `hub/internal/hostrunner/launch_m2.go:47-113` executes the
>   requested command via `exec.CommandContext(ctx, "bash", "-c",
>   command)`.
> - `hub/internal/hostrunner/tmux_launcher.go:52-78` likewise
>   injects the command into a tmux window for the alternate runner
>   path.
>
> Practical exploit path:
>
> 1. A worker reads its own plaintext bearer from its MCP
>    configuration or environment.
> 2. It calls the REST `PUT /v1/teams/{team}/policy` endpoint and
>    submits an empty policy to enable permissive operation.
> 3. It calls `POST /v1/teams/{team}/agents/spawn` with a connected
>    `host_id` and attacker-controlled `backend.cmd`.
> 4. The host-runner executes the supplied shell command on the
>    host.
>
> Impact:
>
> A compromised, prompt-injected, or simply malicious worker agent
> can escape the declared role model and obtain remote command
> execution on a host reachable through the hub. It can also invoke
> additional REST mutations that MCP policy intended to prevent.
>
> Remediation:
>
> - Issue distinct token audiences for MCP agent access and
>   administrative REST access.
> - Reject `kind='agent'` tokens from REST management and mutation
>   endpoints unless a narrowly defined endpoint explicitly requires
>   them.
> - Centralize operation authorization so role and token-kind
>   controls apply to both MCP and REST paths.
> - Restrict policy modification and agent spawning to authenticated
>   principal or owner identities.
> - Add negative tests proving worker bearers cannot directly invoke
>   policy, spawn, template, or approval REST operations.
>
> ### F-02 - Critical: Public A2A relay accepts unauthenticated messages that become live agent input
>
> The A2A relay is deliberately mounted outside authentication and
> treats host and agent identifiers in the URL as the capability.
> Relayed JSON-RPC messages are forwarded to active agents and
> converted into agent input.
>
> Evidence:
>
> - `hub/internal/server/server.go:205-209` mounts
>   `/a2a/relay/{host}/{agent}/*` without authentication and
>   comments that token-based peer authentication is deferred.
> - `hub/internal/server/handlers_a2a.go:27-30,141-170,189-203`
>   returns public relay URLs containing host and agent identifiers.
> - `hub/internal/server/tunnel_a2a.go:256-339` accepts relay
>   requests and enqueues their method, path, and body to the host
>   without authenticating the sender.
> - `hub/internal/hostrunner/a2a/server.go:123-160` dispatches
>   `POST /a2a/<agent>` messages once the target agent exists.
> - `hub/internal/hostrunner/a2a_dispatcher.go:20-29,67-111` extracts
>   `message/send` text and posts it as agent input with producer
>   `a2a`.
> - `hub/internal/server/tunnel_a2a.go:278-289,391-407` stamps
>   sender metadata only when optional bearer authentication
>   succeeds; unauthenticated content is still relayed.
>
> Impact:
>
> Anyone who obtains or guesses a relay URL can inject arbitrary
> instructions into a running agent. Against an agent with sensitive
> permissions, this becomes an external prompt-injection channel
> capable of triggering actions, leaking context, creating expense,
> or chaining into other hub weaknesses.
>
> Remediation:
>
> - Require authenticated and authorized A2A callers, or use
>   high-entropy per-agent relay capabilities separate from database
>   identifiers.
> - Sign and verify peer messages where public interoperability is
>   required.
> - Authorize sender-to-target relationships before forwarding input.
> - Add message size limits, rate limits, and denied-attempt
>   auditing.
>
> ### F-03 - Critical: Approved template installation permits path traversal and arbitrary YAML file writes
>
> The approved template installation path does not use the normal
> template path validators. It directly joins caller-controlled
> `category` and `name` values into a destination path and writes
> uploaded blob contents there.
>
> Evidence:
>
> - `hub/internal/server/apply_template_install.go:45-62` parses
>   template install proposals but validates only that `category`,
>   `name`, and `blob_sha256` are non-empty.
> - `hub/internal/server/apply_template_install.go:104-153` applies
>   an approved `template.install` proposal by invoking
>   `installProposedTemplate`.
> - `hub/internal/server/handlers_attention.go:487-550` also applies
>   approved legacy template proposals through this installation
>   path.
> - `hub/internal/server/handlers_attention.go:798-838` calculates:
>   - `dstDir := filepath.Join(s.cfg.DataRoot, "team", "templates", p.Category)`
>   - `dst := filepath.Join(dstDir, name)`
>   - `os.WriteFile(dst, body, 0o644)`
>   without a containment check or safe-name validation.
> - In contrast, `hub/internal/server/handlers_templates.go:287-302,445-482`
>   validates ordinary template editor requests and enforces
>   resolved-path containment.
> - `hub/internal/server/mcp_authority_roles.go:73-121` loads
>   `${DataRoot}/roles.yaml`, which is a security-sensitive YAML
>   target.
>
> For example, a proposal containing the following values resolves
> outside the template tree after approval:
>
> ```json
> {
>   "category": "../..",
>   "name": "roles.yaml",
>   "blob_sha256": "<uploaded-blob-sha256>"
> }
> ```
>
> The destination becomes `${DataRoot}/roles.yaml`, allowing
> uploaded content to overwrite the role-policy file used by MCP
> authorization on subsequent reload or restart.
>
> Impact:
>
> An attacker who can submit a proposal and induce one approval can
> write attacker-controlled YAML content outside the intended
> template directory, including hub governance or configuration
> files writable by the service account.
>
> Remediation:
>
> - Use the same validated `resolveTemplatePath` implementation for
>   approved installs as for direct template editing.
> - Validate `category` and `name` before reading or writing any
>   proposal data.
> - Require `blob_sha256` to match an exact lowercase SHA-256 format
>   before resolving blob paths.
> - Validate template document structure and category before
>   approval.
> - Add path-traversal tests for both current and legacy template
>   proposal flows.
>
> ### F-04 - Critical: Approval and override authority is controlled by caller-supplied `by` text
>
> The attention decision endpoint is bearer-protected, but it does
> not derive approver authority from the authenticated identity.
> Instead, it trusts the caller-provided `by` field, and the code
> explicitly documents `by == "@principal"` as the current override
> gate.
>
> Evidence:
>
> - `hub/internal/server/server.go:525-531` exposes the attention
>   decision route within the ordinary bearer-only route group.
> - `hub/internal/server/handlers_attention.go:297-319` defines
>   `attentionDecideIn.By` as user input and notes that override
>   currently requires `By == "@principal"` while token identity
>   enforcement is deferred.
> - `hub/internal/server/handlers_attention.go:344-418` processes
>   the decision body without validating that `By` corresponds to
>   the authenticated token or that the caller is principal-tier.
> - `hub/internal/server/handlers_attention.go:430-484` records
>   approvals and resolves a decision from submitted approval
>   records.
> - `hub/internal/server/handlers_attention.go:487-563` executes
>   approved `agent.spawn` and `template.install` actions.
> - `hub/internal/server/handlers_attention.go:631-639` documents
>   the same caller-controlled principal gate for override handling.
>
> Impact:
>
> Any valid bearer, including an agent bearer, can claim to be
> `@principal` and approve or override consequential actions. This
> defeats the core human-approval protection for command execution
> and file installation, even if MCP tool role checks operate
> correctly.
>
> Remediation:
>
> - Derive actor identity and principal status exclusively from
>   authenticated token claims and stored authorization data.
> - Ignore `by` for authorization purposes; at most use a
>   server-derived actor label for display and audit records.
> - Require principal or owner authorization for decisions and
>   overrides, with team binding.
> - Enforce quorum using distinct authenticated subjects rather than
>   arbitrary submitted labels.
> - Add tests for forged `by` submissions using agent, host, and
>   non-principal user tokens.
>
> ### F-05 - High: REST team scope is not bound to bearer token scope
>
> Tokens contain a team scope, and routes are structured under
> `/v1/teams/{team}`, but middleware does not bind the requested
> team to the token scope. Multiple handlers additionally query
> records without team predicates or use singleton filesystem
> resources.
>
> Evidence:
>
> - `hub/internal/server/handlers_tokens.go:116-165` issues tokens
>   containing a `"team"` value in their scope JSON.
> - `hub/internal/auth/token.go:86-109` authenticates bearers
>   without enforcing the stored scope against route parameters.
> - `hub/internal/server/handlers_tokens.go:47-56` checks owner
>   token kind but not whether the owner belongs to the requested
>   team.
> - `hub/internal/server/server.go:263-315` exposes team-prefixed
>   APIs under the same unscoped bearer middleware.
> - `hub/internal/server/handlers_hosts.go:161-207` lists hosts
>   using the route team parameter without checking token scope.
> - `hub/internal/server/handlers_tokens.go:168-203` permits token
>   revocation by ID without constraining the target token to the
>   route team.
> - `hub/internal/server/handlers_attention.go:150-190,238-294,344-418`
>   reads and modifies attention entries without a team predicate.
> - `hub/internal/server/handlers_policy.go:41-43,67-69` and
>   `hub/internal/server/handlers_templates.go:20-30,46-100` operate
>   on singleton on-disk resources despite team-prefixed API routes.
>
> Impact:
>
> In a multi-team deployment, a valid bearer for one team may read
> or mutate resources belonging to another team. An owner for one
> team may affect tokens or operational state associated with
> others. The API and data model present multi-team behavior, so
> relying on a single-team deployment is not a durable security
> boundary.
>
> Remediation:
>
> - Parse and enforce token scope centrally in middleware, binding
>   every `{team}` route to an allowed team.
> - Define explicitly whether owners are global or team-scoped, and
>   enforce that decision consistently.
> - Add team predicates to record reads and mutations.
> - Isolate policy and template persistence by team ID, or remove
>   multi-team routing semantics.
> - Add cross-team denial tests for every sensitive route group.
>
> ### F-07 - High: User-controlled project `docs_root` enables server-side reads of arbitrary files
>
> Project creation accepts an arbitrary `docs_root`, including
> absolute paths and home-directory paths. The project document
> endpoints subsequently walk and read files beneath that
> caller-selected root. Their containment check only prevents
> escaping the selected root; it does not require that root to be
> within an approved document directory.
>
> Evidence:
>
> - `hub/internal/server/server.go:361-390` exposes project creation
>   and project-document reads through ordinary bearer-authenticated
>   team routes.
> - `hub/internal/server/handlers_projects.go:13-18,219-242,299-317`
>   accepts `docs_root` in the create payload and persists it
>   without path validation.
> - `hub/internal/server/handlers_project_docs.go:30-51` explicitly
>   preserves absolute roots and expands a `~/` prefix to the hub
>   service account's home directory.
> - `hub/internal/server/handlers_project_docs.go:54-93` walks the
>   chosen root and returns file and directory metadata.
> - `hub/internal/server/handlers_project_docs.go:96-140` reads
>   requested files beneath the chosen root with `os.ReadFile`.
>
> Practical exploit path:
>
> 1. A bearer creates a project with
>    `{"name":"host-files","docs_root":"/etc"}` or
>    `{"name":"home","docs_root":"~/"}`.
> 2. It requests
>    `/v1/teams/{team}/projects/{project}/docs/passwd`, or
>    enumerates and reads sensitive files in the service account's
>    home directory.
> 3. If the data root is known or reachable from `~/`, it may read
>    database or configuration files containing operational data and
>    credentials.
>
> Impact:
>
> Any REST-capable bearer can use the hub as a file-read oracle
> under the hub process identity. This can disclose service
> configuration, API credentials, private keys, local agent
> configuration, or hub data that enables further compromise.
>
> Remediation:
>
> - Remove support for user-controlled absolute and `~/` document
>   roots, or restrict them to explicit administrator-configured
>   allowlisted directories.
> - Resolve symlinks and require the final root and target path to
>   remain within an approved base directory.
> - Require elevated authorization to set or change a document
>   root.
> - Add regression tests for absolute roots, home-directory roots,
>   parent traversal, and symlink escape attempts.
>
> ### F-08 - High: Caller-controlled event attribution and usage costs can pause another agent
>
> The channel event endpoint trusts both the submitted sender agent
> ID and submitted usage costs. When the claimed event cost reaches
> an agent's configured budget, the server pauses that claimed
> agent and enqueues a host pause command. No check binds `from_id`
> to the authenticated token.
>
> Evidence:
>
> - `hub/internal/server/roles.yaml:53-62` permits workers to use
>   `channels.post_event`.
> - `hub/internal/hubmcpserver/toolspec.go:223-226` marks
>   `channels.post_event` worker-eligible.
> - `hub/internal/hubmcpserver/tools.go:453-483` accepts an
>   arbitrary `from_id` in the worker-facing MCP tool and forwards
>   it to the HTTP handler; despite its description, the adapter
>   does not derive the value from the caller token.
> - `hub/internal/server/server.go:368-376,542-553` mounts channel
>   event writes under ordinary bearer-authenticated routes.
> - `hub/internal/server/handlers_events.go:15-27,30-102` accepts
>   caller-supplied `from_id` and `usage_tokens`; when a submitted
>   cost is nonzero it calls `accumulateSpend` for the submitted
>   sender ID.
> - `hub/internal/server/budget.go:16-62` increments that agent's
>   spend and, at its budget threshold, marks it paused, enqueues a
>   host-side pause command, and raises an attention item.
>
> Impact:
>
> A valid bearer can forge events as another agent and use a
> crafted REST event containing a large `usage_tokens.cost_cents`
> value to exhaust the victim's budget and pause it on its host.
> Worker MCP access already permits sender impersonation in channel
> history; the broader REST bypass in F-01 makes the forged-cost
> pause path reachable to an agent bearer as well. This corrupts
> audit and cost records and can disable supervisory agents.
>
> Remediation:
>
> - Derive event sender identity server-side from the authenticated
>   token; reject or ignore caller-supplied `from_id` except for
>   tightly authorized system ingestion.
> - Accept usage and cost accounting only from trusted host/runtime
>   ingestion identities, not from general channel-posting clients.
> - Verify that the sender, channel, and route team are mutually
>   authorized before inserting events.
> - Add tests proving a worker cannot impersonate another agent or
>   submit spending that changes another agent's pause state.
>
> ### F-09 - High: Flutter SSH connections automatically accept unverified server host keys
>
> The Flutter SSH service creates both jump-host and target-host
> `SSHClient` instances without supplying a host-key verification
> callback. The resolved `dartssh2` API documents that when
> `onVerifyHostKey` is null, the host key is accepted automatically.
>
> Evidence:
>
> - `pubspec.yaml:40` and `pubspec.lock:172-178` select `dartssh2`
>   version `2.13.0`.
> - `lib/services/ssh/ssh_client.dart:241-264` establishes direct
>   or SOCKS-routed SSH sockets.
> - `lib/services/ssh/ssh_client.dart:267-284` creates the jump-host
>   client for key or password authentication without
>   `onVerifyHostKey`.
> - `lib/services/ssh/ssh_client.dart:292-314` creates the final
>   target-host client for key or password authentication without
>   `onVerifyHostKey`.
> - The package API documentation states that a null
>   `onVerifyHostKey` automatically accepts the host key.
>
> Impact:
>
> An attacker able to interpose on a connection path, including
> hostile Wi-Fi, DNS or routing manipulation, or a compromised proxy
> path, can impersonate an SSH server or jump host without a warning
> in the application. Password authentication discloses the password
> to the impersonator; key-authenticated sessions can still be
> directed to an attacker-controlled host and have terminal input or
> command results manipulated.
>
> Remediation:
>
> - Require host-key verification for both the jump host and final
>   target.
> - Implement trust-on-first-use fingerprint approval or explicit
>   fingerprint provisioning, persist accepted keys in protected
>   storage, and fail closed on key changes.
> - Display host, port, key type, and fingerprint in first-use and
>   mismatch dialogs.
> - Add tests proving unknown keys require confirmation and changed
>   keys block connection.
>
> ### F-10 - High: Flutter backup export persists plaintext SSH keys and passwords in public files
>
> The mobile backup feature deliberately collects secrets from
> secure storage into a JSON payload and then saves that payload
> unencrypted into public/user-visible storage. It also writes an
> additional plaintext copy into temporary storage for optional
> sharing without deleting that copy after use. The UI warns the
> user that the export is plaintext, but the implementation still
> converts keychain-protected remote-access secrets into durable
> unencrypted files as its normal workflow.
>
> Evidence:
>
> - `lib/services/data_port_service.dart:61-78,87-126` exports
>   connections, SSH private keys, key passphrases, and saved
>   passwords into an ordinary map suitable for JSON encoding.
> - `lib/screens/settings/settings_screen.dart:1724-1780` confirms
>   the export, serializes it as formatted JSON, writes it through
>   `PublicFileStore.writeBytes`, and creates a separate temporary
>   share file with `writeAsString`.
> - `lib/services/public_file_store.dart:6-22,84-119` specifies that
>   exports go to Android public `Download/TermiPod/` storage or the
>   iOS Documents directory visible through Files.
> - `android/app/src/main/AndroidManifest.xml:10-13` identifies the
>   Android public Download behavior;
>   `ios/Runner/Info.plist:67-72` enables Files exposure of the app
>   Documents directory.
> - `lib/l10n/app_en.arb:580-581` explicitly states that exported
>   SSH private keys and passwords are in plain text.
>
> Impact:
>
> A routine backup leaves reusable SSH passwords, private keys, and
> key passphrases outside secure storage. Device backups or
> synchronization, inadvertent file sharing, device loss, or later
> filesystem access can disclose credentials sufficient to access
> saved remote hosts.
>
> Remediation:
>
> - Make credential-inclusive backups encrypted by default, using a
>   password-derived key and authenticated encryption.
> - Default to exporting non-secret configuration only; require a
>   separate explicit opt-in for keys and passwords.
> - Avoid writing an extra plaintext temporary copy, or securely
>   remove it immediately after the platform share operation
>   completes.
> - Provide a migration or cleanup path for previously exported
>   backup files.
>
> ### F-11 - High (conditional): Imported Flutter connection configuration can execute shell commands on an SSH target
>
> Connection configuration treats `tmuxPath` as data, but the SSH
> implementation interpolates it directly into a remote shell
> command. The backup import path accepts arbitrary connection maps
> from a selected JSON file and stores them without validating this
> field. A malicious backup therefore becomes remote command
> execution when the user connects using an imported record.
>
> Evidence:
>
> - `lib/providers/connection_provider.dart:116-167,241-245`
>   serializes and persists an unrestricted `tmuxPath`.
> - `lib/screens/connections/connection_form_screen.dart:1487-1515`
>   also accepts arbitrary manually entered `tmuxPath` text.
> - `lib/screens/settings/settings_screen.dart:1793-1831` permits
>   JSON backup import after validation and category selection.
> - `lib/services/data_port_service.dart:196-209,250-280` validates
>   only backup format/version/data presence and merges incoming
>   connection maps into preferences without per-field validation.
> - `lib/services/ssh/ssh_client.dart:319-335` executes
>   `test -x ${options.tmuxPath}` on the remote host without shell
>   quoting.
> - `lib/services/ssh/ssh_client.dart:552-565,737-800` later
>   substitutes an accepted `_tmuxPath` into additional executed
>   commands.
> - `lib/services/tmux/tmux_commands.dart:426-451` already contains
>   shell argument escaping for other command arguments, but it is
>   not applied to `tmuxPath`.
>
> Example trigger:
>
> An imported connection with a `tmuxPath` such as
> `/usr/bin/tmux; touch /tmp/termipod-import-executed; #` causes
> the initial validation command to run the injected command when
> the connection is opened.
>
> Impact:
>
> If a user imports a crafted backup and opens its connection, the
> backup author can execute arbitrary commands on the remote SSH
> target with that user's account privileges. The precondition is
> user import and subsequent connection, so this is conditional
> rather than an unauthenticated attack, but its command-execution
> impact is High.
>
> Remediation:
>
> - Treat imported connection profiles as untrusted input and
>   validate all executable/path fields before storing or using
>   them.
> - Shell-quote `tmuxPath`, or avoid shell construction for
>   executable validation and command invocation.
> - Require an absolute path with a narrowly allowed character set
>   and show imported executable settings to the user before
>   activation.
> - Add tests with shell metacharacters, command substitution,
>   whitespace, and comment characters.
>
> ### F-06 - Medium: MCP token resolution does not enforce token expiry
>
> REST bearer validation checks expiration, but MCP token lookup
> only checks that a token has not been revoked.
>
> Evidence:
>
> - `hub/internal/auth/token.go:134-157` rejects expired REST
>   tokens.
> - `hub/internal/server/mcp.go:194-211` resolves MCP tokens using
>   the token hash and `revoked_at IS NULL`, without evaluating
>   `expires_at`.
>
> Impact:
>
> An agent-capable token with an expiration continues to authorize
> MCP operations after its intended lifetime. Current spawn-issued
> agent tokens may not set an expiry, but the authentication
> contract is inconsistent and future issued expiring agent tokens
> will not be contained as expected.
>
> Remediation:
>
> - Reuse one token-validation implementation for REST and MCP
>   authentication, including expiry and revocation checks.
> - Add coverage showing expired MCP tokens receive authentication
>   failure.
>
> ## Additional Mobile Risks And High-Level Bugs
>
> These mobile issues are significant engineering defects or
> defense-in-depth gaps, but the inspected paths do not establish
> the same direct High-impact exploit chain as F-09 through F-11:
>
> - SOCKS proxy passwords are stored as plaintext application
>   preferences.
>   `lib/screens/connections/connection_form_screen.dart:1509-1515`
>   puts `proxyPassword` into a `Connection`;
>   `lib/providers/connection_provider.dart:116-139,241-245`
>   serializes it into `SharedPreferences`, unlike the SSH password
>   stored through secure storage at
>   `lib/screens/connections/connection_form_screen.dart:1480-1484`.
>   Move proxy credentials into secure storage and keep only a
>   reference in the connection model.
> - Password-authenticated jump hosts cannot use a password
>   distinct from the target host. The form supplies no
>   jump-password field at
>   `lib/screens/connections/connection_form_screen.dart:1073-1100`,
>   and runtime paths explicitly reuse the main password at
>   `lib/screens/terminal/terminal_screen.dart:1339-1350`,
>   `lib/screens/home_screen.dart:396-405`, and
>   `lib/screens/connections/connections_screen.dart:827-837`. This
>   breaks normal bastion deployments and encourages password
>   reuse.
> - Channel attachments posted by the Flutter client do not
>   round-trip through the Go event schema. Flutter posts
>   `kind: 'attachment'` with `sha256` and `name` at
>   `lib/screens/team/team_channel_screen.dart:182-197` and
>   `lib/screens/projects/project_channel_screen.dart:182-198`, but
>   `hub/internal/events/event.go:28-41` has no
>   attachment/name/sha fields;
>   `hub/internal/server/handlers_events.go:17-27,53-54` decodes
>   into that struct and re-serializes it, while
>   `hub/internal/server/validate_event_parts.go:48-52` tolerates
>   the unknown kind. Receiving Flutter code requires the discarded
>   fields at `lib/screens/team/team_channel_screen.dart:407-424`.
>   Also sanitize names before the latent download writes at
>   `:370-383` if attachment naming is implemented.
> - On iOS, internal offline data is stored under the same
>   Files-visible Documents directory intended for exports.
>   `lib/providers/hub_provider.dart:306-313` places snapshot and
>   blob caches in Documents; `lib/providers/notes_provider.dart:25-30`
>   places personal notes there; `ios/Runner/Info.plist:67-72`
>   exposes Documents to Files and opening-in-place. Move internal
>   cache and note databases into non-user-shared application
>   support storage and consider encryption for sensitive content.
> - Agent-supplied canvas artifacts execute unrestricted JavaScript
>   in a WebView at
>   `lib/widgets/artifact_viewers/canvas_viewer.dart:72-123`. The
>   navigation filter at `:183-205` does not itself demonstrate a
>   complete request/subresource sandbox, and no app bridge was
>   identified, so no direct secret theft path is proven here. Keep
>   artifacts isolated from app authority, apply effective
>   content/network restrictions, and avoid introducing JavaScript
>   bridges.
> - The hub configuration form accepts any full URL at
>   `lib/screens/hub/hub_bootstrap_screen.dart:270-288`, while
>   `lib/services/hub/hub_client.dart:111-123` sends bearer
>   authorization to the configured origin. The UI merely advises
>   HTTPS at `lib/screens/hub/hub_bootstrap_screen.dart:350-364`;
>   require HTTPS except for explicit development/local-network
>   exceptions.
>
> ## Remediation Priority
>
> 1. Block agent and host tokens from administrative REST mutation
>    routes, enforce principal authorization for approvals and
>    policy changes, bind bearer scopes to route teams, and bind
>    event identity and spend reporting to trusted callers.
> 2. Require SSH host-key verification in the Flutter client for
>    direct and jump-host sessions.
> 3. Remove plaintext credential backups as the default mobile
>    export behavior and provide encrypted exports.
> 4. Validate and shell-safe-handle imported `tmuxPath` settings
>    before any remote SSH execution.
> 5. Require authentication and authorization on the A2A relay
>    before accepting any agent message input.
> 6. Fix approved template installation path validation and SHA
>    validation, then audit writable governance files for
>    unexpected changes.
> 7. Restrict project document roots to approved directories and
>    audit whether sensitive files have been readable through
>    configured projects.
> 8. Address the additional mobile storage and attachment-schema
>    defects, then harmonize MCP token lifetime enforcement with
>    REST authentication.
>
> ## Review Limitations
>
> This was a targeted static review focused on critical server-side
> security boundaries in the Go hub and host-runner, followed by a
> focused Flutter mobile audit of SSH, credentials, export/import,
> local storage, WebView, deep-link, and hub-client paths. The
> TypeScript TUI was not deeply audited. No exploit was executed
> and no test suite was run.
