# Changelog

> **Type:** reference
> **Status:** Current (2026-05-29)
> **Audience:** contributors, operators
> **Last verified vs code:** v1.0.742

**TL;DR.** Append-only record of what shipped in each tagged release.
One section per version, newest first. Format follows
[Keep a Changelog](https://keepachangelog.com/) — Added / Changed /
Fixed / Deprecated / Removed / Security. Entries link to the commit
or PR for forensic detail.

This complements:
- `roadmap.md` — current focus and Now/Next/Later view
- `decisions/` — append-only ADRs for architectural choices
- Git tag annotations — short-form release notes per tag

History before v1.0.280 lives in git log only. The active-development
arc starts at v1.0.280 (steward sessions soft-delete + agent-identity
binding). Seed entries prior to that are in
[`#earlier-history`](#earlier-history) below.

---

## v1.0.742-alpha — 2026-05-30

**hub_client split W7 — `HostsApi` extracted (no behavior change).**

Seventh wedge of `docs/plans/hub-client-split.md`.

### Added
- `HostsApi` (`hosts_api.dart`) — `listHosts`/`listHostsCached`,
  `deleteHost`, `updateHostSSHHint`, `updateHostCapabilities`
  (cherry-picked from the collections + host-lifecycle + host-mutations
  regions).
- `HubClient` `hosts` getter + five delegators.

---

## v1.0.741-alpha — 2026-05-30

**hub_client split W6 — `AdminApi` extracted (no behavior change).**

Sixth wedge of `docs/plans/hub-client-split.md`. The operator admin/ops
surface (ADR-028 Phase 5) is a contiguous, self-contained block.

### Added
- `AdminApi` (`admin_api.dart`) — per-host + fleet shutdown/restart/
  update, `adminDbVacuum`, `adminRotateTokens`, `adminListAudit`, and the
  team `policy.yaml` editor (`getPolicy`/`getPolicyKinds`/`putPolicy`).
- `HubClient` `admin` getter + thirteen delegators.

Note: this is the `AdminApi` split; the original plan numbered W6 as
`HostsApi` (host lists/lifecycle/mutations), which becomes W7. The admin
block was cleaved first because it is contiguous and lower-risk.

## v1.0.740-alpha — 2026-05-30

**hub_client split W5 — `AttentionApi` extracted (no behavior change).**

Fifth wedge of `docs/plans/hub-client-split.md`. The attention queue's
list + actions are now one cohesive client, cherry-picked from three
separate banner regions.

### Added
- `AttentionApi` (`attention_api.dart`) — `listAttention`/
  `listAttentionCached` (+ the static `_attentionQuery` helper),
  `decideAttention`, `getAttentionContext`, `resolveAttention`.
- `HubClient` `attention` getter + five delegators.

## v1.0.739-alpha — 2026-05-30

**hub_client split W4 — `EventsApi` extracted (no behavior change).**

Fourth wedge of `docs/plans/hub-client-split.md`. All Server-Sent-Events
streaming now lives in one place.

### Added
- `EventsApi` (`events_api.dart`) — `streamEvents`, `streamTeamEvents`,
  and `streamAgentEvents`, plus the private SSE frame machinery
  (`_streamPath`/`_extractData`). `streamAgentEvents` was pulled out of
  the agent-events section because it shares the same `_streamPath`
  helper; keeping all three stream methods together is the cohesive cut.
- `HubClient` `events` sub-client getter + delegators for the three
  stream methods.

## v1.0.738-alpha — 2026-05-30

**hub_client split W3 — `BlobsApi` + `SearchApi` extracted (no behavior
change).**

Third wedge of `docs/plans/hub-client-split.md`. Two small, isolated
domains validated against the transport seam.

### Added
- `BlobsApi` (`blobs_api.dart`) — `uploadBlob`/`downloadBlob`/
  `downloadBlobCached`, reaching the transport's raw `open`/`readJson`
  and the `blobCache` for offline-safe rendering.
- `SearchApi` (`search_api.dart`) — `searchEvents` (FTS5) +
  `listAuditEvents`/`listAuditEventsCached`.
- `HubClient` `blobs` and `search` sub-client getters + one-line
  delegators for the six moved methods.

### Removed
- Dropped the now-orphaned `import 'dart:async'` from `hub_client.dart`
  (its only user, `unawaited`, moved to `BlobsApi`).

## v1.0.737-alpha — 2026-05-30

**hub_client split W2 — `SystemApi` extracted (no behavior change).**

Second wedge of `docs/plans/hub-client-split.md`. New
`lib/services/hub/system_api.dart` owns the hub-level (non-entity)
endpoints; `HubClient` delegates to it, so call sites are unchanged.

### Added
- `SystemApi` — probe/stats (`getInfo`/`verifyAuth`/`getHubStats`), the
  insights aggregator (`getInsights`/`getInsightsCached` +
  `_insightsScopeQuery`), owner-only tokens (`listTokens`/`issueToken`/
  `revokeToken`), and hub-wide governance config (`getHubRolesConfig`/
  `putHubRolesConfig`/`resetHubRolesConfig`).
- `HubTransport.listJson` — promoted the generic GET→list-of-maps helper
  (was `HubClient._listJson`, 30 call sites) so every sub-client shares
  one wrapper; `HubClient._listJson` is now a shim forwarding to it.

### Changed
- `HubClient` gained a `system` sub-client getter and one-line delegators
  for the twelve moved methods; the private `_insightsScopeQuery` helper
  moved into `SystemApi` (no leftover reference).

## v1.0.736-alpha — 2026-05-30

**hub_client split W1 — `HubTransport` extracted (no behavior change).**

First wedge of `docs/plans/hub-client-split.md` (R1 of the monolith
refactor). New `lib/services/hub/hub_transport.dart` owns the shared
HTTP + cache plumbing every `HubClient` method routes through; the
God-class is being peeled into per-domain sub-clients in later wedges.

### Added
- `HubTransport` — a public transport class holding the `HttpClient`,
  the two optional caches (`snapshotCache`/`blobCache`), and public verb
  + decode plumbing (`get`/`post`/`patch`/`put`/`delete`, raw
  `open`/`readJson`, `invalidate`, `decodeListMaps`/`decodeMap`,
  `cacheHubKey`). Sub-clients will be injected with it.

### Changed
- `HubConfig` and `HubApiError` moved into `hub_transport.dart` and are
  re-exported from `hub_client.dart`, so every existing
  `import '.../hub_client.dart'` (incl. the `show HubApiError` test)
  resolves unchanged.
- `HubClient` now holds a `HubTransport`; its private `_get`/`_post`/…
  plumbing became thin shims forwarding to it, and `snapshotCache`/
  `blobCache` became forwarding getters/setters so the provider wiring
  in `hub_provider.dart` is untouched. The 208 domain method bodies are
  byte-for-byte unchanged.

## v1.0.735-alpha — 2026-05-29

**agent_feed split W6 (final) — event card extracted; agent_feed.dart is
now the container (no behavior change).**

Seventh and last wedge of `docs/plans/agent-feed-split.md`. New
`lib/widgets/agent_feed/event_card.dart` holds the per-event transcript
card — the residue the other five cluster wedges peeled around.

### Changed
- Moved `AgentEventCard` (stays public — the feed container builds it)
  plus its event-card-only privates `_AgentEventCardState`, `_CardHeader`,
  the markdown body, `_kv`/`_mono`/`_textBody`/`_fmtDuration`, and the
  diff trio `_DiffView`/`_DiffLine`/`_DiffKind`.
- The card composes the already-extracted clusters via import:
  `FoldableToolCall` (tool_renderers), `ApprovalCard`/`AskUserQuestionCard`
  (approval_cards), `CollapsibleMono`/`feedJsonPretty` (feed_render),
  `envelopeSenderLabel`/`renderAttentionReplyText` (feed_reducer), and the
  markdown builders.
- Dropped six imports from the container that were only the event card's
  (`flutter/services.dart`, `flutter_markdown`, `markdown` as `md`,
  `markdown_builders.dart`, `agent_feed/approval_cards.dart`,
  `agent_feed/tool_renderers.dart`).
- Restored `AgentEventCard`'s class doc comment, inadvertently removed
  with the interaction block in W5 (v1.0.734).
- `agent_feed.dart` shrinks ~2,749 → ~1,574 LOC — **6,196 → 1,574 overall
  across W0–W6 (−75%)**; it is now the feed container (`_AgentFeedState`:
  subscribe/ingest/scroll/build + side-effects). It sits above the plan's
  aspirational ≤1,100 target because `_AgentFeedState` is a single
  cohesive stateful class, not a further peelable cluster; `event_card.dart`
  is 1,207 LOC, marginally over the 1,200 soft ceiling. Both are flagged
  in the plan as possible separate follow-ups, not part of this split.

## v1.0.734-alpha — 2026-05-29

**agent_feed split W5 — interaction + approval clusters extracted (no
behavior change).**

Sixth wedge of `docs/plans/agent-feed-split.md`, shipped as one PR (the
two clusters are independent class relocations — the one-thing rule is
honored by *kind* of change, not file count). Two new libraries:
`lib/widgets/agent_feed/interaction_cards.dart` (the inline
attention-item surfaces) and `lib/widgets/agent_feed/approval_cards.dart`
(the in-event approval-request + AskUserQuestion cards).

### Changed
- Moved to `interaction_cards.dart`: `PendingPermissionPrompts` +
  `PendingSelections` (was `_…`; the only cross-library symbols — the
  container mounts them — so the only two made public), plus the
  interaction-only privates `_PermissionPromptCard`(+State),
  `_PlanApprovalBody`, `_CompactionBody`, `_SelectionCard`(+State).
- Moved to `approval_cards.dart`: `ApprovalCard` + `AskUserQuestionCard`
  (was `_…`; the event card renders them, so public), plus the
  approval-only privates `_ApprovalCardState`, `_ApprovalOption`,
  `_DecisionChip`, `_AskUserQuestionCardState`, `_AskOption`.
- The two byte-identical `_payloadOf` statics (one each on
  `_PendingPermissionPrompts`/`_PendingSelections`) folded into a single
  private top-level helper in `interaction_cards.dart` — single-cluster,
  so it stays private per the split's lazy/cross-cluster rule.
- Both clusters read `CollapsibleMono` + `feedJsonPretty` from
  `feed_render` via import; no reducer dependency.
- `agent_feed.dart` shrinks ~3,875 → ~2,749 LOC. Pure rearrangement;
  the now-unused `dart:convert` import (its only use was the moved
  `_payloadOf`) was dropped from the container.

## v1.0.733-alpha — 2026-05-29

**agent_feed split W4 — tool_renderers cluster extracted (no behavior
change).**

Fifth wedge of `docs/plans/agent-feed-split.md`. New
`lib/widgets/agent_feed/tool_renderers.dart` holds the foldable
tool_call card and its inline tool_result body.

### Changed
- Moved `FoldableToolCall` (was `_FoldableToolCall`; the only
  cross-library symbol — used by the event card — so the only one made
  public) plus tool-call-only privates `_FoldableToolCallState`,
  `_ToolKvLine`, `_StatusPill`, `_ToolResultInline`(+State).
- `toolIconFor` was a `static` on `AgentEventCard` whose **sole caller**
  was `_FoldableToolCallState`; it moves in as the private top-level
  `_toolIconFor` rather than forcing a back-import of the container
  (which would create an import cycle). Reads `CollapsibleMono` +
  `feedJsonPretty` from `feed_render` via import.
- `_DiffView`/`_DiffLine`/`_DiffKind` stay (event_card-only → W6).
- `agent_feed.dart` shrinks ~4,233 → ~3,874 LOC. Pure rearrangement.

## v1.0.732-alpha — 2026-05-29

**agent_feed split W3 — telemetry_strip cluster extracted (no behavior
change).**

Fourth wedge of `docs/plans/agent-feed-split.md`. New
`lib/widgets/agent_feed/telemetry_strip.dart` holds the cost/token/
rate-limit strip cluster.

### Changed
- Moved `TelemetryStrip` (was `_TelemetryStrip`; the only cross-library
  symbol, so the only one made public) plus its telemetry-only privates
  `_TelemetryTile`, `_fmtTokens`, `_humanWindow`, `_shortModelName`,
  `_fmtCountdown`, `_rateLimitColor`, `_resetIn`. The cluster reads
  `ModelTokens` (from `feed_render`) and four reducer formatters (from
  `feed_reducer`) via import; the container still computes the inputs and
  passes them as constructor args.
- `agent_feed.dart` shrinks ~4,851 → ~4,233 LOC (−618). Pure
  rearrangement; the rate-limits test is reducer-only (no widget ref).

## v1.0.731-alpha — 2026-05-29

**agent_feed split W2 — feed_misc chrome widgets extracted (no behavior
change).**

Third wedge of `docs/plans/agent-feed-split.md`. New
`lib/widgets/agent_feed/feed_misc.dart` holds the three small
container-only chrome widgets, promoted to public:
`OfflineBanner` (← `_OfflineBanner`), `VerboseToggleChip`
(← `_VerboseToggleChip`), `NewEventsPill` (← `_NewEventsPill`).

### Changed
- These are used solely by `_AgentFeedState`; the move shrinks the
  monolith (it is not a shared layer). `agent_feed.dart` imports the new
  library. ~5,040 → ~4,851 LOC. Pure rearrangement; no test referenced
  the moved widgets.

## v1.0.730-alpha — 2026-05-29

**agent_feed split W1 — shared render layer extracted (no behavior change).**

Second wedge of `docs/plans/agent-feed-split.md`. New
`lib/widgets/agent_feed/feed_render.dart` (Layer 1) holds the feed's
genuinely **cross-cluster** render primitives.

### Changed
- New `feed_render.dart`: `feedJsonPretty` (was `AgentEventCard._jsonPretty`;
  used by event_card + approval + interaction + tool_renderers),
  `CollapsibleMono` (was `_CollapsibleMono`; used by all five card
  clusters), `ModelTokens` (was `_ModelTokens`; used by the container's
  per-model aggregation **and** the telemetry strip). Private names
  promoted to public; `agent_feed.dart` imports the new library.
- `agent_feed.dart` shrinks ~5,170 → ~5,040 LOC. Removing `_ModelTokens`
  also re-attached the `_TelemetryStrip` doc comment to its class.
- **Scope refined from the plan after precise call-site analysis:** only
  the three symbols above are cross-cluster. The rest the plan listed
  (`_kv`/`_mono`/`_textBody`, `_DiffView`/`_DiffLine`/`_DiffKind`,
  `_StatusPill`, `_fmtTokens`/`_humanWindow`, `_ToolKvLine`) are
  single-cluster and now migrate with their cluster's own wedge (W3/W4/
  W6); the `_payloadOf` dedup defers to W5 (intra-cluster). This avoids
  premature generalization and keeps the layering clean. Pure
  rearrangement.

## v1.0.729-alpha — 2026-05-29

**agent_feed split W0 — reducer layer extracted (no behavior change).**

First wedge of `docs/plans/agent-feed-split.md` (R2A of
`docs/discussions/monolith-refactor.md`). The pure reducer/formatter/
classification layer of `lib/widgets/agent_feed.dart` (the repo's
largest file) moves to a new `lib/widgets/agent_feed/feed_reducer.dart`
free-function library.

### Changed
- New `lib/widgets/agent_feed/feed_reducer.dart` (Layer 0): the
  already-top-level reducers (`kAgentTurnActiveKinds`,
  `kAgentFeedAlwaysHiddenKinds`, `agentEventIsReplay`,
  `agentEventReplayKey`, `rateLimitsFromEvents`, `processCostFromEvents`,
  `latestStatusLinePayload`, `modeModelStateFromEvents`, the rate-limit/
  cost/envelope formatters, …) plus eleven classifier methods lifted out
  of `_AgentFeedState` as pure free functions (`agentIsBusy`,
  `isHiddenInFeed`, `isGatedToolName`, `isVerboseOnly`,
  `isCumulativeUsage`, `collapseStreamingPartials`,
  `latestSessionInitPayload`, `isIdleDropSignature`, `stringList`).
  Instance-field reads became parameters (`_events`/`_verbose` → args).
- `agent_feed.dart` imports and **re-exports** `feed_reducer.dart`, so
  the ten `test/widgets/agent_feed_*` reducer tests resolve unchanged —
  they are the behavior-preservation proof for the move.
- `agent_feed.dart` shrinks 6,196 → ~5,170 LOC. No widget logic changed;
  `_latestModeModelData`/`_modeModelSig` stay in `_AgentFeedState`
  (widget-coupled). Pure rearrangement.

## v1.0.728-alpha — 2026-05-29

**Filesystem path-traversal closed on template install and project docs
(security-audit F-03 + F-07).**

### Security

- **F-03 — `installProposedTemplate` now validates every component that
  becomes a filesystem path.** `category` and `name` are checked with
  the existing `safeCategoryName` / `safeTemplateName` guards (no
  separators, parent refs, or hidden-file prefixes) and `blob_sha256`
  with `isHexSHA256` (exactly 64 hex chars). Previously all three were
  unvalidated: `category`/`name` could `../`-escape `team/templates`
  (arbitrary write), and a crafted `blob_sha256` containing `/` and
  `..` survived `blobPath`'s `sha[:2]`/`sha[2:4]` slicing to read an
  arbitrary file (arbitrary read). This is the single chokepoint for
  both the ADR-030 `template.install` apply path and the legacy
  `template_proposal` alias.
- **F-07 — project `docs_root` is bounded to the hub data root.** The
  new `boundDocsRoot` helper expands and cleans the value and confirms
  it stays within `DataRoot`; the project-create handler rejects an
  escaping `docs_root` with 400, and `resolveDocsRoot` refuses one at
  read time (so legacy rows can't serve files either). Previously an
  absolute or `~/`-expanded `docs_root` made `get_project_doc` /
  `list_project_docs` an arbitrary-file read oracle under the hub UID;
  the prior containment check only bounded paths *within* the root, not
  the root itself. (Operators needing docs outside the data root would
  add a configurable allowlist of bases — noted as future work.)

## v1.0.727-alpha — 2026-05-29

**Fix: F-01 bearer-kind allowlist must exempt the hub's in-process
authority-tool dispatch.**

### Fixed

- v1.0.726's F-01 reject broke the rich-authority MCP tools (e.g.
  `templates.agent.create`) for agents. Those tools dispatch in-process
  by forwarding the agent's token as an HTTP bearer through the hub's
  own routes (`mcp_authority.go` `chiRouterTransport`), *after* the MCP
  role check has already authorized the call — so the bearer middleware
  saw an `agent` kind and returned 403. The middleware now exempts
  requests marked `auth.WithInProcessDispatch` (a Go context value the
  transport sets; unspoofable from the network), so network agent
  bearers stay rejected while the trusted, already-role-checked
  self-call is allowed. Regression-covered by `TestTemplateAuthoring_W2`.

## v1.0.726-alpha — 2026-05-29

**Agent-kind tokens are refused as REST bearers at the auth middleware
(security-audit F-01).**

### Security

- **`auth.Middleware` now allowlists bearer token kinds: `owner`,
  `user`, `host`.** An `agent` token (or any future kind) presented as
  an `Authorization: Bearer` credential is rejected with 403 before any
  handler runs. Previously the middleware checked only revocation +
  expiry, so an agent token could reach privileged routes
  (policy write, template install, spawn) that lacked their own
  per-handler gate. This is safe because agents never legitimately use
  bearer auth: the host-runner relays all agent operations under its
  own host token + `X-Agent-Id`, and agent tokens authenticate via
  `/mcp/{token}`, which is mounted outside this middleware. The
  allowlist fails closed for unknown kinds. See
  `docs/discussions/security-audit.md` §F-01.

## v1.0.725-alpha — 2026-05-29

**Channel-event sender + cost attribution are bound to the
authenticated token, not the request body (security-audit F-08).**

### Security

- **`handlePostEvent` no longer trusts `from_id` from the body.** It
  was written straight into the event row and passed to
  `accumulateSpend`, so any token could (a) impersonate another agent's
  `from_id` in a channel and (b) forge a `usage_tokens` cost block
  against a victim agent to drive it over budget and trigger the
  auto-pause — a denial-of-service on a sibling agent. The new
  `eventSender` helper derives the sender from the caller's token: an
  `agent` token is bound to its own `scope.agent_id` (body ignored); a
  `host` token (the deputy relaying for its agents) is trusted via the
  `X-Agent-Id` header host-runner already stamps; `owner`/`user`
  (humans) keep their supplied `from_id` for chat provenance but never
  accrue spend. See `docs/discussions/security-audit.md` §F-08.

### Fixed

- **Per-agent event attribution via `X-Agent-Id` is now actually
  implemented hub-side.** `mcp_gateway.go` stamped the header and a
  comment claimed "the hub derives identity from X-Agent-Id," but no
  hub handler read it — channel-event `from_id` was whatever the body
  said. `eventSender` closes that producer→consumer gap.

## v1.0.724-alpha — 2026-05-29

**Principal-override identity is bound to the authenticated token, not
the request body (security-audit F-04).**

### Security

- **Attention decide/override authority no longer reads the caller's
  identity from the request body.** Previously `handleAttentionOverride`
  gated the ADR-030 W9 principal-override path on `by == "@principal"`
  taken from the JSON payload, and the regular `/decide` path recorded
  `by` from the body into `decisions_json`, the `attention.decide`
  audit row, and `ProposeApplyContext.DeciderHandle`. Any agent could
  set `by:"@principal"` to override a resolved decision or forge the
  decider attribution. The decider handle is now derived from the
  authenticated token's scope at a single chokepoint in
  `handleDecideAttention`, and the override gate requires the token's
  kind to be `owner` or `user` (humans) via the new `principalActor`
  helper — `agent`/`host` tokens are refused with 403. A caller-supplied
  `by` is now inert. See `docs/discussions/security-audit.md` §F-04.

**Newly-found sibling (not yet fixed):** `handleResolveAttention`
(POST `/attention/{id}/resolve`) writes `resolved_by` from the body and
resolves an item without running the propose dispatcher. The mobile
client already sends `by` (not `resolved_by`), so the field is inert
from that caller, and `resolved_by` is an FK-to-`agents(id)` column —
the fix needs its own small investigation. Tracked in security-audit.md.

## v1.0.723-alpha — 2026-05-27

**Cross-host blob read path closed end-to-end at the agent layer.**

Pre-v1.0.723 a worker on host A could `attach` bytes into the hub
blob store and reference them by sha, but a worker on host B had no
way to fetch them: there was no agent-facing read tool, and the A2A
inbound dispatcher (`a2a_dispatcher.go:258`) explicitly stripped
non-text parts ("for the MVP we only consume text"). Mobile consumed
the bytes via `/v1/blobs/<sha>` directly; agents could not. This
wedge closes that gap.

### Added

- **`blob_get` native MCP tool** (`mcp_more.go:mcpGetBlob`,
  `native_tools.go`) — read the bytes of a blob by sha256 or by
  full URI (`blob:sha256/<hex>` / `hub-blob://<hex>`). Returns
  `{sha256, size, mime, content_base64}` — the same shape `attach`
  produces, inverted. `WorkerEligible: true`, `TierTrivial`. Three
  distinct error paths: -32602 for malformed sha shape, -32000
  "blob not found" for missing rows, -32000 "blob row exists but
  bytes missing on disk" for the half-state operators see when a
  partial restore loses files but keeps DB rows.

### Changed

- **A2A inbound dispatcher renders file + data parts as text.**
  `extractTextParts` → `renderInboundParts`
  (`a2a_dispatcher.go:253-…`). File parts render as
  `[file: <uri> (<mime>, <size> bytes)]` so the receiving agent
  has the URI in its prompt and can call `blob_get` to fetch the
  bytes. Inline-bytes file parts are summarized (no base64 in
  prompt) with a nudge to `attach + reference by sha` instead.
  Data parts render as `[data: <compact-json>]` truncated to 512
  chars. Handles both the A2A v0.3 nested `file: {...}` shape and
  the looser flat form some peers and fixtures use.
- **`attach` tool description rewritten** to remove the dead "or
  path (server reads — only blessed paths)" claim — the
  implementation has only ever supported `content_base64`. The
  rewrite explains that path-based attaching is the pane-marker
  surface (`<<mcp:attach {"path": "..."}>>`), a different control
  channel from the MCP tool.

### Tests

- `TestMCP_BlobGet_RoundtripsAttachedBytes` — full attach → get
  byte-equality round-trip including non-printable bytes.
- `TestMCP_BlobGet_AcceptsBlobURIForm` — both URI schemes resolve
  to the same sha.
- `TestMCP_BlobGet_RejectsMalformedInputs` — 6 sub-cases (no args,
  empty sha, wrong length, uppercase, non-hex, unknown scheme) all
  surface -32602 not -32000.
- `TestMCP_BlobGet_NotFoundReturnsClearError` — error echoes the
  sha so callers can confirm they passed the right value.
- `TestMCP_BlobGet_MissingFileSurfacedSeparately` — the half-state
  carries a distinct message.
- `TestA2AHubDispatcher_RendersFilePartsAsText` — 4 sub-cases
  covering flat, nested, bare-uri, and data-part shapes.
- `TestA2AHubDispatcher_InlineBytesSummarized` — inline-bytes
  files don't leak base64 into the prompt.
- `TestA2AHubDispatcher_UnrenderableKindsReturnErrDispatch`
  replaces the obsolete `…EmptyPartsReturnsErrDispatch` (which
  pinned the lifted text-only gate).

### Surfaced by

[discussions/multi-agent-harness-landscape.md](discussions/multi-agent-harness-landscape.md)
§5.0 — verifying "can two stewards on two hosts share a file" against
the codebase exposed the half-implemented state. A1 in the same doc
proposed the close.

---

## v1.0.722-alpha — 2026-05-26

**Raw-SSH disconnect no longer collapses to a black screen.**

User-initiated disconnect from the terminal screen's overflow menu
tears down the `SSHClient`, which closes the persistent shell
channel, which fires `RawPtyBackend.shellExited` *synchronously
inside the `await`*. The pre-fix listener
(`terminal_screen.dart:785-788`) re-entered `_disconnect()`; both
calls eventually reached `Navigator.pop(context)`, the original
popped the terminal route, and the recursive call then popped one
route further — `HomeScreen` itself — leaving the user staring at
the empty layer beneath the `IndexedStack`.

### Fixed
- **mobile / raw-SSH terminal:** `_disconnect()` is now
  non-reentrant (`bool _isDisconnecting` flag) and cancels
  `_shellExitedSub` before awaiting the SSH teardown, so the
  shell-exited stream cannot reach the listener at all during our
  own pop. The listener also short-circuits on `_isDisconnecting`
  as belt-and-braces against any future emit path. Repro chain:
  open Raw shell from Hosts → back to Hosts (green dot stays
  because `SshNotifier` holds the keep-alive) → reopen → overflow
  → Disconnect → fix lands user on Hosts instead of a black screen.
  Same class as v1.0.717: a stream listener firing inside its own
  termination await, with an asymmetric-recovery navigation cost.

### Notes
- No new tests — the bug surface is a four-line guard in a 4 000-LOC
  widget; building a useful end-to-end harness (mock `SshNotifier`,
  mock `RawPtyBackend`, mock `Navigator`) wasn't justified for the
  guard's complexity. Manual smoke is the verification path.
- Codex's 2026-05-25 security review
  ([`discussions/security-audit.md`](discussions/security-audit.md))
  also touched the SSH path — F-09, F-10, F-11 — but is unrelated
  to this fix. Those land separately.

---

## v1.0.721-alpha — 2026-05-26

**Consumer-side dispatch contracts — Fix A + Fix B from
[`discussions/consumer-side-dispatch-contracts.md`](discussions/consumer-side-dispatch-contracts.md).**
Structural fix for the four-in-48h multi-consumer-dispatch
fails-open recurrences (v1.0.667 / v1.0.699 / v1.0.717 / v1.0.720).
Replaces the mobile busy-inference denylist with an allowlist
(`default = idle`, opt-in for turn-active) and lands a cross-cutting
contract test that fails CI on any unclassified producer-emitted
event kind.

### Fix A — allowlist invert for `_isAgentBusy`

- **`lib/widgets/agent_feed.dart`** — removed
  `kAgentBusyInferenceSkipKinds` (4-entry denylist that grew from
  v1.0.667 / 699 / 717). Replaced with
  `kAgentTurnActiveKinds` (5-entry allowlist: `text`, `tool_call`,
  `tool_call_update`, `thought`, `plan`).
- **`_isAgentBusy`** — the walker now only flips to `true` for
  kinds in the allowlist. Any other agent-produced kind passes
  through. Terminal kinds (`turn.result`, `completion`,
  `session.init`, `lifecycle.{exited,stopped}`) still short-circuit
  to `false` via explicit branches.
- Failure-mode inversion: a missing-from-allowlist kind makes a
  busy agent appear idle (cancel button hides). The pre-v1.0.721
  inverse — a stuck cancel button that did nothing — was strictly
  worse UX. "Cancel button hidden" recovers on the next
  text/tool_call within a tick.

### Fix B — kind-classification contract test

- **`test/widgets/agent_feed_kind_classification_test.dart`** (new)
  — at test-time, regex-scans every `PostAgentEvent(...)` literal
  in `hub/**.go` + every `emit.kind:` in `agent_families.yaml`,
  then asserts each collected kind is classified in exactly one of:
  - `kAgentTurnActiveKinds` (signals motion), or
  - `kAgentFeedAlwaysHiddenKinds` (chip-source only), or
  - `_kKindsExplicitlyIgnoredByBusyInference` (test-local allowlist
    with per-kind rationale comments).
- Producer adding a new kind without classifying it fails this
  test before merge. The failure message points to the discussion
  doc + the three sets the contributor can add to.
- 18 kinds collected from the current tree; all 18 classified.
- Cross-set discipline asserted (turn-active ∩ always-hidden = ∅;
  turn-active ∩ explicit-ignore = ∅).

### Updated tests

- **`test/widgets/agent_feed_status_line_test.dart`** — pre-fix
  tests asserted membership in `kAgentBusyInferenceSkipKinds`
  (the denylist). Inverted to assert non-membership in
  `kAgentTurnActiveKinds`. Existing claude-code precedence + cross-
  set discipline tests preserved.

### Notes

- **Coverage:** Fix A retroactively fixes the four historical
  denylist additions — they're no longer needed because the four
  kinds (`usage`, `rate_limit`, `status_line`, `raw`) are simply
  absent from the allowlist. v1.0.720's chip-strip-reducer fix
  (different dispatch shape, branch chain) stays in place; Fix B's
  contract test prevents the chip-strip class of recurrence
  symmetrically.
- **Fix C (single source-of-truth kind registry) deferred** per
  the discussion doc's recommendation: right end state, wrong next
  step.
- **Hub-side dispatch sites** (audit subject kinds, attention
  card kinds, route prefixes) are NOT covered by this contract
  test — they share the `kind` literal pattern but live in
  different column-sets. The discussion doc §7 raises this as an
  open question; the tight PostAgentEvent + YAML emit regex
  pair is the conservative scoping.
- **Engine-agnostic by construction:** new producer-side engines
  inherit the contract. The next status_line-emitting engine
  works on mobile without a per-engine code path; the next
  pre-turn-active kind requires explicit consumer-side
  classification.

### References

- [`discussions/consumer-side-dispatch-contracts.md`](discussions/consumer-side-dispatch-contracts.md)
  — design rationale, three-fix sketch, recommendation.
- Changelog entries the inversion retroactively addresses:
  v1.0.667 / v1.0.699 / v1.0.717 / v1.0.720.

## v1.0.720-alpha — 2026-05-26

**Mobile chip strip now reads `status_line` events.** v1.0.719 wired
the antigravity statusLine pipeline end-to-end (16 status_line events
flowing per the dev-host hub DB inspection on session
`01KSHDVRQQ…`), but mobile rendered no chips. Smoking gun: the
chip-strip reducer at `lib/widgets/agent_feed.dart:1600-1688` walks
events for `kind == 'turn.result'` / `'usage'` / `'rate_limit'` —
**`'status_line'` was never in the dispatch.** Antigravity emits
ONLY `status_line` events for token + context-window data (no
transcript-derived `usage` events; agy's transcript doesn't carry
token counts — statusLine is the authoritative source per the
antigravity statusLine research §2.4). So the chip strip stayed
blank for antigravity stewards even though hub-side data was
correctly flowing.

### Fixed

- **`lib/widgets/agent_feed.dart`** — added an `else if (kind ==
  'status_line')` branch alongside the existing claude-code per-
  message and codex cumulative branches. Extracts from the nested
  `context_window.current_usage` block (shape-identical to claude-
  code's top-level `usage` block per research §2.4) plus
  `context_window.context_window_size`. Same `perMessageInput` /
  `perMessageCacheRead` / `perMessageCacheCreate` / `latestContextWindow`
  variables the claude-code branch writes — the downstream chip
  strip / token-flow pill renders identically for antigravity with
  no per-engine branching at the render layer. Model name extraction
  also handles `{id, display_name}` (antigravity's shape; same as
  claude-code's status_line model field).

### Class kin

Recurrence #4 in 48h of `feedback_engine_dispatch_sweep` — producer
emits an event kind; consumer's per-kind dispatch list misses it.
Same class as v1.0.667 (`usage` → busy-skip), v1.0.699
(`status_line` → busy-skip), v1.0.717 (`raw` → busy-skip), and now
v1.0.720 (`status_line` → chip-strip dispatch). The pattern's
strong enough that a discussion doc on durable consumer-side
contracts is overdue (see follow-up below).

### Notes

- claude-code stewards are unaffected: the M4 adapter emits BOTH
  `usage` events (from JSONL) AND `status_line` events. The
  `usage` branch above wins on latest-write semantics — the new
  branch is additive and degrades cleanly.
- Codex stewards likewise unaffected: codex emits cumulative
  `usage` events (`_isCumulativeUsage` shape); the new branch fires
  for any `status_line` codex emits but `latestContextWindow` is
  set via the `usage` branch path and that's the chip's authoritative
  source on codex.
- Engine-agnostic by construction: `status_line` from any engine
  with the same payload shape (`payload.context_window.current_usage.*`)
  feeds the same chip without further work.

### Follow-up

A discussion doc on durable consumer-side dispatch contracts is
warranted given 4 instances in 48h. Pattern: every kind that
producers emit should be classified in ONE pinned set (turn-active /
chip-only / always-hidden / etc.); consumers register against the
set, not against ad-hoc kind names. Defer; not blocking this fix.

## v1.0.719-alpha — 2026-05-26

**Antigravity statusLine pipeline (G1+G2 of the statusLine research).**
The per-spawn UDS gateway is now wired for antigravity spawns and
`installAntigravityStatusLine` installs a managed `statusLine` block
into agy's host-global `~/.gemini/antigravity-cli/settings.json`. Each
fire of agy's statusLine command (state-diff cadence — verified live
on 2026-05-26: zero fires during a 139s idle window) lands on the
gateway, gets deduped (1s identical-payload window), and posts an
`AgentEvent{kind:"status_line", producer:"agent", payload:<verbatim>}`
to the hub — the same shape mobile chip reducers already consume for
claude-code (ADR-036 D3/D4). No mobile change in this wedge; the
chips light up automatically the moment hub-side events flow.

### Added

- **`hub/internal/hostrunner/statusline_install_antigravity.go`** —
  new `installAntigravityStatusLine(udsSocket, hostRunnerExe)`. Mirrors
  the claude-code `installClaudeStatusLine` shape (atomic-rename,
  wrap-and-passthrough preserves operator-set commands under
  `_termipod_wrapped_command`, `_termipod_managed` marker) but targets
  agy's host-global settings.json instead of claude's per-workdir
  `.claude/settings.local.json`. Sets the agy-specific
  `enabled: true` field alongside the command (host-verified: agy
  ships `{"type":"","command":"","enabled":true}` as the empty
  default; without `enabled: true` the install is silently inert).
- **`antigravity.Adapter.OnStatusLine(ctx, payload)`** — implements
  `hostrunner.StatusLineSink`. Caches the latest snapshot under
  `mu`; gateway posts the verbatim AgentEvent before invoking the
  sink, so mobile gets the snapshot regardless of what the sink
  does. Today the cache is write-only; a future G2 wedge will read
  it on a session.init re-post to override model/version/cwd from
  statusLine-sourced fields (mirroring the claude-code precedence
  rules at `drivers/local_log_tail/claude_code/adapter.go:160-180`).
- **`antigravity.Adapter.LatestStatusLine()`** — returns a defensive
  copy of the cached snapshot, or `nil` if none received. Useful
  for tests + the future override consumer.
- **`hub/internal/hostrunner/statusline_install_antigravity_test.go`**
  — 5 tests pinning: fresh install, re-install over managed block
  preserves sibling keys (`enableTelemetry`, `trustedWorkspaces`),
  operator-config wrapping, prior-wrap preservation across
  re-install, and composition with `preTrustWorkspaceAntigravity`
  (both functions write the same file; neither must clobber the
  other's keys).
- **3 new sink tests in `antigravity/adapter_test.go`** —
  `OnStatusLine_CachesLatest`, `OnStatusLine_NilPayloadStoredAsEmpty`
  (the gateway feeds `{}` on a malformed shim post; we store as empty
  map so readers can distinguish "fired with no fields" from "never
  fired"), and `LatestStatusLine_ReturnsCopy` (poisoning the snapshot
  doesn't leak).

### Changed

- **`hub/internal/hostrunner/launch_m4_antigravity.go`** — runs
  `installAntigravityStatusLine` after `preTrustWorkspaceAntigravity`
  (both write the same file; the order matches the install→spawn
  ordering agy caches at). After tmux launch, starts a per-spawn UDS
  gateway via `StartGateway` (mirrors claude-code's
  `launch_m4_locallogtail.go:258` — the gateway code already
  handles statusLine dedup + hub post). Wires `gw.StatusLineSink =
  adapter`. Returns `Gateway: gw` + `HostRunnerExe: hostRunnerExe`
  in the launch result (the M4LocalLogTailLaunchResult struct
  already supported these fields). Best-effort: a nil
  `GatewayHubClient` falls back to pre-v1.0.719 behaviour
  (no statusLine pipeline; useful for tests).
- **`hub/internal/hostrunner/runner.go`** — passes
  `GatewayHubClient: a.Client` to `launchM4Antigravity` (1-line
  change; mirrors the claude-code arm above).

### Notes

- **Why a host-global file works.** agy caches `statusLine.command`
  at process boot (host-verified by the `/statusline delete` requires
  "reset" string in the binary; agy doesn't re-read settings.json
  per fire). So the install→spawn ordering on each new spawn — write
  file → tmux launch boots agy → agy caches OUR command — gives each
  concurrent spawn its own effective statusLine even on the shared
  file. This is the B1 resolution from the research doc, symmetric
  with ADR-035 D7 (which chose host-global MCP config over per-spawn
  HOME for the same reason agy's OAuth lives in `~/.gemini`).
- **No mobile change in this wedge.** The mobile chip reducers
  (context fill ring, 200K alarm tile, rate-limit-style chips —
  written for ADR-036 D4) consume `payload.context_window`,
  `payload.exceeds_200k_tokens`, etc. — fields the antigravity
  statusLine payload provides (live probe verified 2026-05-26,
  research doc §2.3 + §2.4). They start flowing the moment the
  AgentEvent flows.
- **The G2 session.init field-override layer is deferred.** Today
  the `LatestStatusLine` cache is write-only. The original G2
  scope ("adapter consumes status_line + overrides session.init
  fields") is split: G1 (install + sink wiring + cache) ships in
  this version; G2-extended (in-process consumer that re-emits
  session.init with model/version overrides) becomes a follow-up
  wedge if/when on-device smoke shows mobile needs the refresh
  channel.

### References

- [`discussions/antigravity-statusline-research.md`](discussions/antigravity-statusline-research.md)
  §6 G1 + G2 — sizing and design choices.
- [`drivers/local_log_tail/claude_code/adapter.go:160-180`](../hub/internal/drivers/local_log_tail/claude_code/adapter.go) —
  the in-process override precedence pattern the future G2 wedge
  will mirror.
- ADR-036 D3/D4 — the cross-engine `kind:"status_line"` AgentEvent
  contract this wedge piggybacks on.

## v1.0.718-alpha — 2026-05-26

**Antigravity session-details parity (G3 of the statusLine research).**
Mobile's session-details sheet now renders the engine identity rows
(engine / model / version / cwd / permission_mode / session_id) for
antigravity stewards instead of leaving them blank. Mirrors the codex
v1.0.715 contract; ships the first wedge of the antigravity statusLine
research's G1–G6 plan
([`discussions/antigravity-statusline-research.md`](discussions/antigravity-statusline-research.md)
§6 G3). Hub-only structural work plus a one-arm mobile colour-map
extension; no statusLine install pipeline yet — G1 + G2 land next.

### Added

- **`antigravity.Config.{Engine, PermissionMode, EngineVersion}`** —
  launch-time fields the adapter folds into the initial `session.init`
  payload. `Workdir` was already present (it's the pathresolver key)
  and now also surfaces as `cwd` on the payload.
- **`Adapter.buildLaunchTimeSessionInit()`** — pulled the payload
  assembly into a helper so tests pin field selection without
  spinning the whole `resolveAndRun` pipeline. Empty fields drop
  out (blank > wrong; mobile section-gating relies on the
  `isEmpty` check).
- **`launch_m4_antigravity.go::permissionModeFromCmd()`** — token-
  matches `--dangerously-skip-permissions` on backend.cmd and
  returns the verbatim flag-derived label
  (`"dangerously-skip-permissions"`) or `"interactive"` (default —
  agy raises an arrow-nav menu at every tool gate). Whole-token
  match so a literal flag inside a `--print "..."` body doesn't
  false-positive.
- **EngineVersion resolution** — mirrors the codex pattern at
  `launch_m2.go:417-422`: `agentfamilies.ByName(kind).VersionFlag` +
  `exec.LookPath` + `runVersion`. Resolves to `1.0.2` on the dev
  host. Best-effort — a missing binary just leaves the field empty.

### Changed

- **`lib/widgets/session_details_sheet.dart::_permModeColor`** —
  extended for antigravity's flag-derived vocabulary.
  `"interactive"` → green (safest; user-gated). `"dangerously-skip-
  permissions"` → red (same risk class as claude-code's
  `"bypassPermissions"` and codex's `"never"`). The two existing
  vocabularies (claude-code + codex) are unchanged; antigravity's
  strings sit alongside them in the same switch. Per the research
  doc's recommendation, the raw flag-derived strings are preferred
  over a translated alias for grep affinity on the hub side.

### Tests

- `TestAdapter_BuildLaunchTimeSessionInit_PopulatesAllFields`,
  `TestAdapter_BuildLaunchTimeSessionInit_SkipsEmptyFields`,
  `TestAdapter_BuildLaunchTimeSessionInit_PermissionModeInteractive`
  in `internal/drivers/local_log_tail/antigravity/adapter_test.go` —
  pin the field selection contract + the blank-fields-drop discipline
  + the default `"interactive"` mode.
- `TestPermissionModeFromCmd_Antigravity` in
  `internal/hostrunner/launch_m4_antigravity_test.go` — 6 cases
  covering bare invocation, mid-argv flag, sandbox-without-skip,
  empty cmd, and the whole-token-match boundary that prevents
  false-positives from prompt bodies containing the literal flag
  string.

### Notes

- Field source comments mirror the codex `emitSessionInit` precedent
  at `driver_appserver.go:1548-1611`; the consumer shape (mobile)
  is identical across engines, so adding a new engine row to the
  session-details sheet is now a YAML+constructor diff, not a
  Flutter change.
- The G3 wedge is statusLine-independent — the
  `Adapter.buildLaunchTimeSessionInit` payload contains only
  launch-time fields; G1 + G2 will later add a status_line-sourced
  override layer (mirroring the claude-code M4 adapter precedence
  rules at `adapter.go:160-180`).

## v1.0.717-alpha — 2026-05-26

**Codex resume no longer pins busy(cancel) forever.** On the v1.0.716
on-device smoke, a resumed codex session displayed the red Cancel
button immediately after handshake and tapping it produced no visible
response. Reproduced and confirmed from the dev-host hub DB
(`/var/lib/termipod-hub/hub.db`, session `01KSH9EW…`, new agent
`01KSH9GG…`).

### The producer

After handshake, codex emits three notifications without a profile
rule — `thread/goal/cleared`, `remoteControl/status/changed`,
`configWarning` (bubblewrap-missing). These land via ApplyProfile's
intentional forward-compat catch-all (`agent_families.yaml:782-787`)
as `kind: raw`. The newest in the tail
(`thread/goal/cleared` at `04:43:28.287Z`) sits **above** the
authoritative `session.init` (which would have made inference idle).

### The consumer (the actual bug)

Mobile's `_isAgentBusy()` walks events newest-first, skipping a known
list (`usage`, `rate_limit`, `status_line`, plus `system`/`user`
producer rules). **`raw` was not in the skip list**, so the walker
returned `true` at the very first `raw` event encountered — never
reaching `session.init`. Cancel button was correctly *shown* per the
inference, but with no active turn `turn/interrupt` was a no-op
(no `turnId` → malformed RPC per upstream codex), so nothing closed
the busy state.

### Class kin

Same multi-consumer-dispatch-fails-open class as v1.0.667 (`usage`
added to skip list) and v1.0.699 (`status_line` added). Producer
(driver / profile catch-all) emits a new pre-turn-active event kind;
consumer's skip list misses it. The codex frame profile docs are
explicit that `raw` is forward-compat housekeeping
(`agent_families.yaml:782-787`) — by definition NOT a turn-active
signal.

### Fixed

- **`lib/widgets/agent_feed.dart:96`** — add `'raw'` to
  `kAgentBusyInferenceSkipKinds`. One-line change, mirrors the
  v1.0.667 + v1.0.699 fix pattern. Extended the inline comment in
  `_isAgentBusy()` to cite this fix alongside the prior two.
- **`test/widgets/agent_feed_status_line_test.dart`** — new test
  pinning `raw` membership with verbatim citation of the DB tail
  that proved the bug; mirrors the v1.0.667/v1.0.699 test shape.

### References

- DB inspection helper: `/tmp/inspect-resume-bug.go`, used against a
  read-only snapshot of `/var/lib/termipod-hub/hub.db`. The
  inspector + the captured tail rows are quoted in this commit's
  message for forensics.
- Hub-side cancel handler at `hub/internal/hostrunner/
  driver_appserver.go:531-553` is left as-is — it correctly checks
  `d.TurnID()` and the no-active-turn path is a legitimate no-op.
  Once mobile stops falsely showing the Cancel button, the
  no-turn-to-cancel scenario won't be reachable through the UI.

## v1.0.716-alpha — 2026-05-26

**Codex M2 resume fixed end-to-end.** Stopping a codex steward and
resuming it now reattaches to the prior thread instead of cold-
starting with a fresh `session_id`. Three connected gaps closed
in one wedge:

### The three breaks

1. **`handlers_sessions.go:627`** (`POST /sessions/{id}/resume`)
   switch arm didn't include `case "codex":`. Captured
   `engine_session_id` was discarded; the new spawn rendered with
   no `resume_session_id` field.
2. **`respawn_with_spec_mutation.go:129`** (mode/model swap respawn
   path) had the same switch-arm gap. Both resume entry points
   were equally broken.
3. **`launch_m2.go`** codex branch never wired
   `spec.ResumeSessionID` → `AppServerDriver.ResumeThreadID`. Even
   if the YAML field had been spliced, the driver would have
   ignored it and called `thread/start` anyway.

End-to-end before this wedge: hub captures `thread.id` correctly
(via v1.0.715's `session.init` emission) → respawn switch drops it
on the floor → launch_m2 wouldn't have read it anyway → codex
gets a fresh `thread/start` → mobile shows a new session id.

### Fix

- Both switch arms gain `"codex"`, sharing the `spliceACPResume`
  call site (already the right shape: top-level
  `resume_session_id` YAML field). Codex and the ACP family
  (`gemini-cli`, `kimi-code`) share the SpawnSpec field; the
  protocol-level translation diverges driver-side.
- `launch_m2.go` codex branch now sets `ResumeThreadID:
  spec.ResumeSessionID`. Empty stays empty → `thread/start`.
  Non-empty → `thread/resume` with `params.threadId` set per
  upstream `codex-rs/app-server-protocol/src/protocol/common.rs:457`.
- `SpawnSpec.ResumeSessionID` + `spliceACPResume` doc comments
  updated to acknowledge codex as the second consumer (the
  function name keeps its ACP-historical prefix; renaming would
  touch every call site without behaviour change).

### New test

- `TestAppServerDriver_ResumeThreadID_CallsThreadResume` — pins
  the driver-side contract: non-empty `ResumeThreadID` triggers
  `thread/resume` with the prior thread id; `thread/start` is NOT
  called; `ThreadID()` reflects the resumed value.

### Caveat

Codex's `thread/resume` semantics are upstream-controlled. If
codex's app-server can't find the prior thread (e.g. the
`~/.codex/sessions/.../rollout-*.jsonl` was deleted between the
stop and the resume), it returns an error and the handshake
fails. v1.0.716 doesn't add a "resume fell through to start"
fallback — that's a deliberate follow-up gate, because silently
cold-starting on resume-failure is the original bug. We surface
the failure to the operator instead.

## v1.0.715-alpha — 2026-05-26

**Codex M2 session-details sheet parity with claude-code M4.** The
mobile session-details bottom sheet at
`lib/widgets/session_details_sheet.dart` reads `engine` / `model` /
`version` / `permission_mode` / `cwd` / `session_id` from a
`session.init` event payload to populate the AGENT + WORKDIR
sections. Claude-code's M4 LocalLogTail adapter has posted this
event since the engine landed; codex M2 didn't, so codex stewards
showed a near-empty sheet on the principal device.

### Added

- **`AppServerDriver.emitSessionInit`** in
  `hub/internal/hostrunner/driver_appserver.go`. Posts
  `kind: session.init, producer: agent` after `thread/start` returns,
  carrying `engine`, `model`, `version`, `cwd`, `permission_mode`,
  `session_id`, `model_provider`, `service_tier`. All fields omitted
  when empty so the mobile sheet's per-row `isEmpty` gating hides
  absent fields cleanly.

- **Launch-time metadata fields** on `AppServerDriver`: `Engine`,
  `Workdir`, `PermissionMode`, `EngineVersion`. Populated from
  `launch_m2.go` via the existing `agentfamilies.ByName` +
  `runVersion` capability probe. These are fallbacks; the
  authoritative source is the `thread/start` response which carries
  codex's post-merge view of `cwd` + `approvalPolicy` (the operator
  may have a project-level `.codex/config.toml` overriding our
  `codexApprovalPolicy()` env-default).

- **`approvalPolicyToString` helper** that flattens codex's
  `AskForApproval` JSON (kebab-case string for unit variants,
  object for the experimental `granular` variant per upstream
  `codex-rs/app-server-protocol/src/protocol/v2/shared.rs:162-179`)
  into a 1-line label. Granular variants surface the sub-policy
  key as the label (full sub-policy detail isn't useful in a chip
  pill; operators reading the verbose-mode payload still see it).

- **Mobile `_permModeColor` extended** for codex's permission-mode
  vocabulary at `lib/widgets/session_details_sheet.dart:805`.
  Semantic equivalence across engines:
  - green (`success`): claude `default`/`plan`; codex `on-request`/`untrusted`
  - amber (`warning`): claude `acceptEdits`; codex `on-failure`
  - red (`error`): claude `bypassPermissions`; codex `never`
  - muted: granular + anything new we haven't mapped

- **4 driver tests** covering: full response with all fields,
  launch-time fallback when response omits cwd + approvalPolicy,
  the approvalPolicyToString helper (4 unit + 2 granular + 2 junk
  cases), and the empty-input suppression guard.

### Why the response supersedes launch-time fields

Codex's `thread/start` resolves `cwd` after its own tilde-expansion
+ project-root logic; resolves `approvalPolicy` after merging
operator config layers (env-default ← user config ← project
config). The host-runner-side `expandedWorkdir` + `codexApprovalPolicy()`
captures the launch-time intent but not the post-merge truth — they
diverge whenever an operator runs codex in a directory with a
project-level `.codex/config.toml`. Always preferring the response
keeps the sheet honest.

### Citations

- `codex-rs/app-server-protocol/src/protocol/v2/thread.rs:195-220` —
  `ThreadStartResponse` shape (model, cwd, approvalPolicy fields).
- `codex-rs/app-server-protocol/src/protocol/v2/shared.rs:162` —
  `AskForApproval` enum wire shape (kebab-case + granular variant).
- `lib/widgets/session_details_sheet.dart:305-345` — mobile
  consumer reading `payload.engine/model/version/permission_mode/cwd/session_id`.
- `hub/internal/drivers/local_log_tail/claude_code/adapter.go:421` —
  the claude-code precedent this session.init mirrors.

## v1.0.714-alpha — 2026-05-26

**Wedge F follow-up — drop the misrouted `kind: rate_limit` profile
rule for codex.** v1.0.713 on-device smoke surfaced a duplicate
chip: alongside the new ADR-036 5h + 7d tiles (driven by Wedge F's
`status_line` emission), a literal **"300"** tile appeared with a
long-press tooltip "Rate-limit window (300)".

### Root cause

The pre-existing codex profile rule at `agentfamilies/agent_families.
yaml` translated `account/rateLimits/updated` to `kind: rate_limit`
with `window: $.params.rateLimits.primary.windowDurationMins` —
codex's wire field is **numeric minutes** (e.g. 300 = 5h). Mobile's
legacy single-window `rate_limit` chip at `agent_feed.dart:5668-5699`
was designed for claude's stream-json `rate_limit_event` SDK frame,
which carries `window` as a **string label** like `"five_hour"` /
`"seven_day"`. The Dart code does `(rl['window'] ?? '').toString()`
then renders via `_humanWindow(...)` — for claude's string it
becomes "5h"; for codex's number it becomes the literal "300".

The misrouting predated Wedge F (the rule landed when codex M2 first
shipped), but was invisible because no one had a long-enough codex
session for the notification to fire on the principal device. v1.0.713
introduced the proper engine-agnostic `status_line` chip pair beside
the legacy chip — making the misroute visible.

### Fixed

- Removed the codex profile rule for `account/rateLimits/updated`.
  Wedge F's `AppServerDriver.emitRateLimitsStatusLine` is now the
  sole translation path and produces only the engine-agnostic
  `status_line` shape mobile's ADR-036 chip pair consumes.
- Unmatched notifications fall through to ApplyProfile's
  `kind: raw` default — verbose-mode debugging audit trail intact;
  the legacy `rate_limit` chip stays dormant for codex.
- `TestProfile_Codex_TranslatesAppServerNotifications` wantKinds:
  `account/rateLimits/updated` → `raw` (was `rate_limit`).
- `TestAppServerDriver_TranslateRateLimitsUpdated_EmitsStatusLine`:
  inverted the `rate_limit` assertion (now expects ABSENT) and
  added a `raw` fallback assertion.

### Why mobile didn't change

The legacy single-window `rate_limit` chip stays for claude's
stream-json `rate_limit_event` frames (older claude SDK builds
that don't ship statusLine). Removing the codex profile rule is
enough — claude's chip behaviour is untouched.

## v1.0.713-alpha — 2026-05-26

**Codex M2 gains rate-limit chip parity with claude-code M4 statusLine
(ADR-036 Phase B chip contract).** Wedge F from
[`discussions/codex-m2-app-server-surface-audit.md`](discussions/codex-m2-app-server-surface-audit.md)
§5-F lands hub-only — mobile chips render without any Flutter change.

### Added

- **`AppServerDriver.emitRateLimitsStatusLine`** — translates codex's
  upstream `RateLimitSnapshot {primary, secondary}` wire shape
  (`codex-rs/app-server-protocol/src/protocol/v2/account.rs:257-353`)
  into the engine-agnostic ADR-036 mobile-chip contract
  `payload.rate_limits.{five_hour, seven_day}.{used_percentage,
  resets_at}`. Posted as `kind: status_line, producer: agent` — same
  event shape claude-code's statusLine shim emits, so the existing
  mobile chip reducers (`rateLimitsFromEvents` in
  `lib/widgets/agent_feed.dart:125`) consume it uniformly.

- **Handshake-time pull** at `driver_appserver.go::handshake`. After
  `thread/start` returns, calls `account/rateLimits/read` (upstream
  `common.rs:923`) and emits a `status_line` snapshot if the response
  carries non-null windows. Means mobile has chip data BEFORE codex's
  first `account/rateLimits/updated` push — that notification can
  take many minutes (or never fire at all if usage stays well under
  the windows).

- **Notification-time emission** at `translateNotification`. An
  `account/rateLimits/updated` frame (upstream `common.rs:1517`) now
  triggers an additional `status_line` event alongside the existing
  profile-driven `kind: rate_limit` event (dual-emit, symmetric with
  claude-code's `rate_limit_event` + statusLine dual path). Both
  kinds are in mobile's `kAgentFeedAlwaysHiddenKinds` set
  (`agent_feed.dart:74-79`) so neither clutters the transcript.

- **7 driver tests** covering the full snapshot, primary-only,
  both-null suppression, nil-snapshot guard, the notification path,
  the handshake-time pull, and `-32601` graceful degradation when
  the codex build doesn't implement the read method yet.

### Why no shim install (unlike v1.0.696-698 claude-code statusLine)

Codex's app-server protocol exposes `RateLimitSnapshot` as a
first-class JSON-RPC notification on the same stdio pipe
`AppServerDriver` already reads. The whole v1.0.696-698 stack
(statusLine shim binary + UDS gateway + `~/.claude/settings.local.
json` wrap-and-passthrough) was load-bearing for claude-code only
because that engine's sole data export is a shell command claude
invokes every ~10s. Codex was designed app-server-first; this
wedge is ~150 LOC + 8 tests instead of ~760 LOC + 16 tests.

### Fixed

- `hub/internal/hostrunner/testdata/profiles/codex/corpus.jsonl`
  `account/rateLimits/updated` frame's `resetsAt` is now numeric
  Unix-epoch seconds (was an ISO 8601 string — wrong per upstream
  `codex-rs/protocol/src/protocol.rs:2025` which documents
  `resets_at: Option<i64>` as "Unix timestamp (seconds since
  epoch)"). The malformed fixture didn't break any existing
  assertions but would have masked the wedge's wire-shape
  expectations.

- Test scaffolding: `fakeAppServer` now responds with JSON-RPC
  `-32601 Method not found` for unregistered request methods (was
  silently dropping them, which would have hung the new
  handshake-side `account/rateLimits/read` call until
  `CallTimeout`). Matches real codex behaviour.

### Citations

- ADR-036 mobile-chip contract example payload:
  [`decisions/036-claude-code-statusline-telemetry.md:100-103`](decisions/036-claude-code-statusline-telemetry.md)
- Wedge sizing + scope:
  [`discussions/codex-m2-app-server-surface-audit.md`](discussions/codex-m2-app-server-surface-audit.md) §5-F

## v1.0.712-alpha — 2026-05-25

**Two codex M2 follow-ups from the v1.0.711 on-device smoke: bypass-
mode MCP tool calls no longer raise an attention card, and the
mobile context-window-fill indicator uses the per-turn token count
instead of the cumulative-session one.**

### Fixed

- **Bypass-mode (`approval_policy=never`) MCP tool calls still
  raised an attention card.** With v1.0.711's bridge fix wired,
  every codex MCP tool call (e.g. `projects_list`, `documents_get`)
  surfaces a permission_prompt card via the
  `mcpServer/elicitation/request` codex sends as a wrapper gate.
  But the per-spawn `config.toml` already writes
  `approval_policy = "never"` (the production default per
  `codexApprovalPolicy` in `hub/internal/hostrunner/launch_m2.go`),
  which is the explicit operator opt-in to "the hub is the trust
  boundary, don't double-gate". Codex still sends the elicitation
  regardless of that setting — distinct mechanism from its shell /
  file-change approval gates.
  Fix: `AppServerDriver` gains `AutoAcceptMCPToolCalls bool`, set
  by `launchM2` when `codexApprovalPolicy() == "never"`. In
  `handleServerRequest`, an `mcpServer/elicitation/request` tagged
  with `_meta.codex_approval_kind="mcp_tool_call"` is auto-accepted
  with the same `{action: accept, content: {}, _meta:
  {persist: session}}` shape `resolvePendingApproval` would write on
  a principal-approve — no card, one compact
  `appserver_mcp_tool_call_auto_accepted` system event per call for
  the audit trail. Real form-fill elicitations (non-empty
  `requestedSchema.properties`) and approval-shaped methods
  (commandExecution / fileChange / permissions) are NOT affected —
  those still raise cards in bypass mode, on the principle that
  shell + file boundaries are too load-bearing for a global opt-out.
  Tests:
  `TestAppServerDriver_AutoAcceptMCPToolCallsBypassesAttention`
  pins the auto-accept response shape + the
  `appserver_mcp_tool_call_auto_accepted` audit;
  `TestAppServerDriver_BypassDoesNotSkipFormFillElicitation` locks
  the scoping invariant against a refactor that broadens the
  auto-accept too far.
- **Mobile context-window-fill indicator showed cumulative tokens
  instead of per-turn.** `hub/internal/agentfamilies/agent_families.yaml`
  mapped `$.params.tokenUsage.total.totalTokens` (cumulative across
  the whole session) onto the `total_tokens` field; mobile's
  `_buildTelemetryStrip` reducer in `lib/widgets/agent_feed.dart`
  read that into `latestContextUsed`. Long codex sessions then
  displayed inflated context-fill ratios (e.g. ~65% on a session
  whose actual fill was ~7%) because each turn re-sends the full
  conversation as input and `total.*` grows boundlessly. The
  codex frame already carries the right number under
  `tokenUsage.last.totalTokens` — the per-turn snapshot.
  Fix: extended the profile rule to also emit `last_input_tokens`,
  `last_output_tokens`, `last_cached_input_tokens`,
  `last_total_tokens`, `last_reasoning_tokens` alongside the
  cumulative `total_tokens` (cumulative kept for cost / per-engine
  token aggregation, which is correctly cumulative). The mobile
  reducer now prefers `last_total_tokens` for context fill and
  falls back to `total_tokens` for pre-v1.0.712 events on disk.
  `TestProfile_Codex_DistilledFrames`'s
  `thread/tokenUsage/updated` case extended to assert both the
  cumulative + per-turn keys land in the payload.

### Notes

The codex frame profile was carrying both shapes; only the lift
rules ignored `.last`. The corpus fixture
(`testdata/profiles/codex/corpus.jsonl`) already exercised the
combined frame — its comment got a one-line update to record the
new "both lifted" contract.

---

## v1.0.711-alpha — 2026-05-25

**Codex MCP-tool-call gates no longer auto-decline as
"user rejected MCP tool call" whenever the host-runner runs
with A2A enabled (the production default).**

### Fixed

- **`a2aPosterTap` masked the `AttentionPoster` surface of its
  inner `*Client`.** `runner.go:311` wraps the agentPoster in
  an `a2aPosterTap` whenever the host's A2A server is enabled
  (every production deployment). The wrapper proxied
  `PostAgentEvent` but had no `PostAttention` method. The codex
  launch path at `launch_m2.go:405` then tried
  `cfg.Client.(AttentionPoster)` and silently failed the cast,
  leaving `AppServerDriver.Attention = nil`. Every
  server-initiated request — including the
  `mcpServer/elicitation/request` codex uses to gate MCP tool
  calls (ADR-012 D3) — hit the no-bridge branch and
  auto-declined with `{action: "decline"}`. Codex relayed that
  to the agent as "user rejected MCP tool call". The principal
  never saw a gate.
  Fix: `(*a2aPosterTap).PostAttention` now delegates to the
  inner client when it implements `AttentionPoster` (production
  `*Client` always does); inner-doesn't-implement returns a
  clean error so the driver's existing
  `appserver_attention_post_failed` audit path captures it and
  declines through the right per-method shape rather than
  panicking. Two new tests
  (`hub/internal/hostrunner/a2a_dispatcher_test.go::TestA2APosterTap_ImplementsAttentionPoster`
  and the inner-without-AttentionPoster fallback) lock the
  cast + delegate so a future refactor can't re-mask the
  surface.

### Why this is small

One method added, two tests. The bug was a single missing
interface implementation; the audit + decline machinery on
both sides of the bridge was already in place — just never
exercised because the cast failed before any call could fire.
The reasoning trail in the codex_attention_kind discriminator,
the rmcp shape mapping, and the principal-decision→codex-decision
mapping are all unchanged from v1.0.710.

---

## v1.0.710-alpha — 2026-05-25

**Two on-device hotfixes from v1.0.708 smoke: envelope `from:` label
follows the YAML, and codex-steward M2 spawns without a project_id
can now launch.**

### Fixed

- **Mobile transcript `from:` label stayed stale after editing
  `roles.principal` in the envelope YAML.** v1.0.708 wired hot-reload
  for the engine-facing prose (`payload.rendered_text`) but the
  mobile feed renders the `[from: <label>]` row via a parallel
  hardcoded Dart map at `lib/widgets/agent_feed.dart:553-566`
  (`_envelopeRoleLabel`). Two sources of truth: edits reached the
  engine, not the UI. Fix: hub stamps an extra
  `payload.from_label` field via a new
  `Templates.RenderSender(role, handle)` public surface
  (`hub/internal/envelope/types.go`) and a sibling
  `Server.renderEnvelopeSenderLabel(env)` helper
  (`hub/internal/server/input_envelope.go`). The same operator
  template resolution that drives `rendered_text` now drives the
  mobile-facing label in lockstep. Mobile prefers `from_label` from
  the payload via a new `envelopeSenderLabel(role, handle,
  fromLabel)` helper and falls back to the static map for legacy
  events (pre-v1.0.710 rows on disk + A2A relay paths that don't
  pass through this hub handler). Tests:
  `hub/internal/envelope/types_test.go::TestRenderSender` covers
  the cascade; the two existing
  `TestPostAgentInput_RenderedText*` tests now also assert
  `from_label`; `test/widgets/envelope_sender_label_test.dart`
  covers the mobile precedence + fallbacks.
- **Codex (and any) steward spawned outside a project errored with
  `context_files set but backend.default_workdir is empty`.** The M2
  workdir derivation at `hub/internal/hostrunner/launch_m2.go:198`
  required `cfg.Spawn.ProjectID != ""` to land at
  `~/hub-work/<pid8>/<handle>`; without a project, the workdir
  stayed empty and the `writeContextFiles` guard rejected the
  spawn. The codex-steward template
  (`hub/templates/agents/steward.codex.v1.yaml`) intentionally
  omits `default_workdir` with the comment "auto-derives from
  spawn" — that contract only held for project-bound spawns. Fix:
  extracted a shared `DeriveWorkdir(defaultWorkdir, projectID,
  handle, childID, needsWorkdir)` in `spec.go` with a new
  third-rung fallback `~/hub-work/_team/<handle>` when neither a
  default nor a project_id is set AND the spawn would otherwise
  need a workdir (`context_files` non-empty or `mcp_token`
  set). Applied in both launch_m1.go and launch_m2.go for
  symmetry. The legacy "no workdir, run from host-runner's cwd"
  path is preserved for back-compat with demo templates that have
  no context/mcp materialisation. Tests:
  `hub/internal/hostrunner/spec_test.go::TestDeriveWorkdir` locks
  the four-rung precedence;
  `TestLaunchM2_DerivesTeamWorkdirWhenNoProjectID` reproduces the
  codex-steward smoke shape end-to-end.

### Why these landed together

Both came from v1.0.708 on-device smoke and are mobile-visible
operator-template regressions (one in the rendering pipeline, one
in the spawn pipeline). Issue 1 is the natural follow-up to the
ADR-032 D-10 hot-reload wedge (mobile was overlooked); issue 2 is
a class-of-bug fix in the workdir derivation that shared roots
with ADR-025 W6's project-id derivation rule. Shipping in one
wedge keeps the on-device repro narrative coherent.

---

## v1.0.709-alpha — 2026-05-25

**Two on-device hotfixes from v1.0.708 smoke testing: tmux session
cache no longer wiped on cold start, and the "stuck spinner on
network switch" deadlock when opening a host row is gone.**

### Fixed

- **Active-sessions cache wiped on every cold start.** The
  remembered-last-tmux-session feature
  (`activeSessionsProvider` →
  `lib/providers/active_session_provider.dart`) had a load-time
  prune that filtered out rows referencing a "missing" connection
  and then `_saveToStorage()`'d the filtered result. But
  `connectionsProvider` is itself an async-load `Notifier`; the two
  load orders race on every cold start. When the active-sessions
  load completes first, `connectionsProvider.connections` is still
  empty — every persisted row reads as orphaned and the entire
  cache gets overwritten with `[]`. From the user's POV the cache
  appeared never to persist; on the next session-open the terminal
  fell back to `sessions.first.name` instead of the
  last-attached one.
  Fix is two invariants: (1) the load path is read-only with
  respect to disk (delete-time `removeSessionsForConnection` in
  `connections_screen.dart`/`connection_form_screen.dart` is now
  the only authoritative cleanup path); (2) the in-memory prune is
  gated on `connState.isLoading == false`, so a cold-start race
  that fires the load before connections settles keeps every
  persisted row visible. A regression test under
  `test/providers/active_session_provider_test.dart` pre-seeds
  `active_sessions` with the `connections` key intentionally
  missing and asserts the disk file survives the load.
- **Stuck "connecting" spinner after a transient network switch
  (wifi↔cellular, captive-portal handoff, suspend/resume).** The
  Hosts row → `TerminalScreen` path reuses the keep-alive SSH
  client cached by `SshNotifier` when `alreadyLive == true`. If the
  underlying TCP socket was half-open (e.g. the device just changed
  networks), the first `sshClient.exec(TmuxCommands.version())`
  inside `_setupTmuxBackend` had no `timeout:` argument and parked
  forever on `Future.wait([stdoutCompleter, stderrCompleter])`,
  wedging both `_isConnecting=true` (so the spinner stayed) AND
  every later exec via the `_withExecLock` chain — the only
  recovery was a force-kill of the app. The keep-alive watchdog
  did eventually fire its own 3s timeout and dispose the client,
  but whether the orphaned in-flight exec's stream completers ever
  got errored was dartssh2-implementation-dependent.
  Three-part fix in `lib/screens/terminal/terminal_screen.dart`:
  (1) pre-flight the cached client with
  `exec('echo p', timeout: Duration(seconds: 3))` before trusting
  `alreadyLive` — failure → dispose and dial fresh; (2) bound the
  version-probe and setup-time tree-refresh with `timeout: 10s`
  each; (3) `_refreshSessionTree` gains a default 15s ceiling
  (was unbounded) so timer-driven refreshes can never deadlock the
  exec lock either. Setup-path now uses `surfaceErrors: true` so
  a tree-fetch failure propagates to `_connectAndSetup`'s catch
  (snackbar + reset spinner) instead of falling through to the
  "create new termipod-* session" recovery branch on a possibly-
  dead socket.

### Why these landed together

Both are mobile-only TmuxBackend regressions surfaced by on-device
smoke testing of the v1.0.708 release. Each fix is independent
and small (the cache fix is ~3 LOC of removed-write + ~3 LOC of
new gate; the SSH fix is ~30 LOC across three call sites), and
they share zero overlap — but they share the same diagnostic
session, the same on-device repro path (open a host row that was
last attached before a network change), and both are
preconditions for the user trusting the "remember my session"
experience. Shipping in one wedge keeps the commit history
faithful to the investigation.

---

## v1.0.708-alpha — 2026-05-25

**Configurable envelope templates with mobile-editable hot-reload
(ADR-032 D-10).** The prose the engine sees on every wrapped turn —
the bracketed header `[<kind> from <sender>]`, the role labels, the
per-`reply_via` instruction — was hardcoded Go since v1.0.648.
Iterating the wording required a rebuild + redeploy. Now lives in
an operator-editable YAML at
`<HUB_DATA>/team/templates/envelope/active.yaml` with mtime-driven
hot-reload.

The override path is already part of the existing `/templates`
REST surface — mobile's `TemplateEditorScreen` lists + edits it
zero-code-changes-needed, same as every other operator template.

### Added

- **`hub/internal/envelope/` package** — three-tier loader mirroring
  the pricing pattern. `Templates.Validate()` compiles every YAML
  string into a `text/template.Template` once (Option
  `missingkey=zero` so a typo'd variable renders empty rather than
  erroring). `Templates.Render(envelope.Message)` produces the
  engine-facing prose. `DeriveReplyVia(kind, transport)` is the same
  closed-enum mapping the host-runner uses, exposed here so the
  envelope package stays import-clean.
- **`hub/templates/envelope/active.yaml`** — the seeded operator
  override (auto-discovered by `writeBuiltinTemplates`, mobile-
  editable via the existing template surface). Byte-equivalent to
  today's hardcoded prose so v1.0.708 ships with zero observable
  wording change.
- **Server-side rendering** — `s.envelope *envelope.Loader`
  constructed in `server.New()` and bound to `cfg.DataRoot` via
  `WithHubData`. `composeTextInputEnvelope` calls a new
  `renderEnvelopeForDriver` that resolves the loader and stamps
  `payload.rendered_text` alongside the structured envelope keys.
- **Host-runner pass-through** — `inboundEnvelope.RenderedText`
  added; `renderInboundEnvelope` prefers it when present, falls
  through to the existing hardcoded `renderEnvelopeTurn` otherwise
  (defence-in-depth: a missing / busted operator template can never
  block a turn).

### Changed

- **ADR-032 amended with D-10** (Envelope rendering is hot-loadable
  template config). Documents the host-runner → hub render move,
  why the closed enums stay code-defined, and the three-tier
  graceful-degradation contract.

### Tests

- **`hub/internal/envelope/types_test.go`** — 16 cases: Validate
  acceptance + four rejection paths (unknown schema version, empty
  frame, empty fallbacks, malformed template string), Render
  smoke across principal-directive / A2A-peer-steward /
  system-notification, empty-kind fallback, unknown-role fall-
  through to default, unknown-role + no default fall to bare
  handle, no-handle fall to empty-handle fallback, leading-@
  normalisation, missing reply_via collapses cleanly,
  DeriveReplyVia closed-enum table.
- **`hub/internal/envelope/loader_test.go`** — 9 cases: embedded
  origin when no override, embedded-template-compiles-and-renders
  build invariant, operator override precedence, env-override
  path precedence, parse-error fall-through with audit warning,
  validation-error fall-through, second-call cache identity,
  mtime-driven hot-reload, permission-denied audit, nil-warner
  safety.
- **`hub/internal/server/handlers_agent_input_test.go`** —
  TestPostAgentInput_RenderedTextStamped (2 sub-cases: regular
  text stamps rendered_text containing header/body/reply line;
  raw slash command omits rendered_text entirely).
  TestPostAgentInput_RenderedTextHonoursOperatorOverride —
  end-to-end smoke: write an override on disk, post a text, assert
  the rendered_text picks up the override's frame + role label.
- **`hub/internal/server/server.go`** pricing loader gains
  `WithHubData(cfg.DataRoot)` alongside the new envelope loader
  for symmetry; both now track the server's data root rather than
  relying on `$HUB_DATA` being identical in every deploy.

### Source

- `hub/internal/envelope/{types,loader}.go` + `templates/envelope/active.yaml`
- `hub/internal/server/{server,input_envelope,handlers_agent_input}.go`
- `hub/internal/hostrunner/input_envelope.go`
- `docs/decisions/032-message-routing-envelope.md` (D-10 amendment)

---

## v1.0.707-alpha — 2026-05-25

**Slash-command inputs bypass the principal-directive envelope.**

The ADR-032 message envelope (`[directive from the principal]\n
<body>\n\nReply in this chat…`) is right for prose turns — it
names the sender, the kind, and the reply mechanism — but on
engine-control slash commands (`/clear`, `/compact`, `/model …`,
`/effort xhigh`) it's actively wrong:

  - claude-code parses slash commands ONLY when they're the very
    first token of the turn. The `[directive from the principal]`
    header preempts that recognition, so the engine reads the
    whole turn as prose and ignores the control intent.
  - On the transcript, "directive from the principal" reads as a
    work instruction to the agent. A control op is not a
    directive — it's an op on the engine itself.

### Added

- **`raw: true` flag on `POST /v1/.../agents/<id>/input`** (hub).
  Effective only for `kind: text`, ignored for `producer: a2a`
  (peer messages always carry their envelope per ADR-032 D-1).
  When set, the hub stamps `payload.text = body` with no
  `from / to / kind / thread` keys, so the host-runner's
  `renderInboundEnvelope` no-envelope fallback returns the body
  verbatim to the driver. `payload.raw: true` is preserved on the
  row so mobile feed can render the bubble without the misleading
  "from: principal · kind: directive" header rows.
- **`raw` parameter on `HubClient.postAgentInput`** (mobile).
  Plumbed through both compose entry points: the main `_send` and
  the snippet picker's `_sendSnippetImmediately`. Default null
  preserves the existing envelope behaviour.
- **`isSlashCommandBody(String)`** — new top-level shape gate in
  `lib/widgets/agent_compose.dart`. Accepts `/<name>[<space>args]`
  where `<name>` starts with a letter and contains
  `[A-Za-z0-9_-]`. Rejects path-like values (`/etc/hosts`),
  markdown bullets (`/ - item`), bare slashes, and prose with a
  slash embedded mid-sentence. Strips leading/trailing whitespace
  defensively. False positives are worse than false negatives
  (misfire makes a real directive look like a slash command); the
  shape gate is narrow on purpose.

### Changed

- **Mobile compose wires `isSlashCommandBody` → `raw: true`.** Both
  the main `_send` path (typed `/clear` in the compose box) and
  `_sendSnippetImmediately` (snippet picker's send button) flip
  the flag when the body matches the slash-command shape AND no
  attachments ride alongside (a slash command with an image makes
  no sense).
- **Mobile feed suppresses envelope rendering for raw inputs.**
  `_inputTextBody` checks `payload.raw == true` and hides the
  `from` / `kind` rows — they'd be misleading (no envelope is on
  the wire) and a slash command is self-describing.

### Tests

- `hub/internal/server/handlers_agent_input_test.go` —
  `TestPostAgentInput_RawSlashCommand_BypassesEnvelope` covers 4
  sub-cases:
  - `raw: true` strips every envelope key (`from`/`to`/`kind`/
    `thread` all absent) and stores `payload.text = body` +
    `payload.raw = true`.
  - `raw: false` preserves the existing envelope-composing path
    (regression-pin against a default flip).
  - `raw` omitted is equivalent to `raw: false`.
  - `producer: a2a` ignores the flag — peer envelopes are
    sacrosanct per ADR-032 D-1.
- `test/widgets/agent_compose_slash_command_test.dart` — 9 cases
  on `isSlashCommandBody`: canonical commands, args, multi-line,
  whitespace tolerance, prose rejection, path-like rejection,
  bullet rejection, bare-slash rejection, kebab/snake-case
  command names.

### Source

- `hub/internal/server/handlers_agent_input.go` — `Raw bool`
  field on `agentInputIn`; raw-branch in the `kind == "text"`
  block stamps `payload.text` + `payload.raw` without invoking
  `composeTextInputEnvelope`.
- `lib/services/hub/hub_client.dart` — `raw` param on
  `postAgentInput`.
- `lib/widgets/agent_compose.dart` — `isSlashCommandBody` helper,
  flag wired into both send paths.
- `lib/widgets/agent_feed.dart` — `_inputTextBody` skips the
  envelope rows when `payload.raw == true`.

---

## v1.0.706-alpha — 2026-05-25

**ADR-036 Phase B polish wave 2 — three on-device-smoke fixes.**

### Changed

- **One combined cost chip on the telemetry strip** (was three:
  turn.result-summed, statusLine-live, hub-imputed). Headline shows
  the most-live number available — process (statusLine cost) when
  present, else turn-aggregated (turn.result). Sub-line carries
  `N turn[s]` per the on-device ask. Long-press tooltip surfaces
  whichever scopes ALSO have a value as cross-check lines, plus the
  per-model breakdown when the hub-side detail loaded. The chips
  used to diverge 10-30% within a single turn (different scopes,
  legitimate but confusing); the tooltip explains the relationship
  without eating glance budget. Engines that report no cost still
  get the legacy "N turns" forward-progress tile.

### Added

- **SESSION STATE section on the session-details sheet** (opened by
  tapping the AppBar engine chip). Four new live mutable fields
  sourced from the latest status_line frame, each row self-gates on
  presence:
  - `effort` — claude's depth-of-thought knob (low / medium /
    high / xhigh), tinted warmer as it climbs so an unusually
    expensive run is visible at a glance.
  - `thinking` — on/off (claude's thinking-mode toggle).
  - `fast mode` — on/off.
  - `style` (the existing AGENT row) now prefers status_line's
    `output_style.name` over session.init's spawn-time capture
    when both are present, so a mid-session `/style` toggle
    actually surfaces.
  Git branch deferred — claude's statusLine doesn't ship it; if the
  user pushes for it, hub-side enrichment from `worktree_path` is
  the natural next step (separate wedge).
- **`onStatusLineChanged` callback on `AgentFeed`.** Mirrors the
  existing `onSessionNameHint` shape (post-frame deferred fire,
  identity-equality dedupe). SessionChatScreen tracks the latest
  payload in `_latestStatusLine` and threads it through
  `SessionInitChip` → `showSessionDetailsSheet`.
- **New top-level reducer `latestStatusLinePayload(events)`** —
  walks newest-last, returns the first `status_line` event's
  payload as `Map<String, dynamic>?`. `@visibleForTesting` for
  pinned-shape regression coverage.

### Changed (continued)

- **`tool_result` cards default-folded.** Two paths:
  - **Orphan tool_result** (no matching parent tool_call in
    scope — the parent fold contract doesn't apply). The
    `_AgentEventCard` now defaults `_collapsed = true` for this
    kind. Failed results (`payload.is_error == true`) auto-expand
    so the diagnostic is visible without a tap.
  - **`_ToolResultInline`** (rendered INSIDE a tool_call card when
    that card is itself expanded). Converted from stateless to
    stateful; the body hides behind a "result · N lines" header
    that doubles as the tap target. Same auto-expand-on-error
    contract.

### Source

- `lib/widgets/agent_feed.dart` — combined cost-tile builder
  (~80 LOC), `latestStatusLinePayload` reducer (~20 LOC),
  `_maybeFireStatusLineChanged` (~15 LOC), `onStatusLineChanged`
  param, `_AgentEventCardState._defaultCollapsedForKind`,
  `_ToolResultInline` → `_ToolResultInlineState` rewrite (~95 LOC).
- `lib/widgets/session_details_sheet.dart` — `statusLine` param on
  `showSessionDetailsSheet` + `SessionInitChip` + sheet; SESSION
  STATE section + four `_statusLineXxx()` accessors +
  `_effortColor` (~80 LOC).
- `lib/screens/sessions/sessions_screen.dart` — `_latestStatusLine`
  field, `onStatusLineChanged` callback wire, `statusLine` param
  threaded into SessionInitChip.

---

## v1.0.705-alpha — 2026-05-25

**ADR-036 Phase B polish — `session_name_hint` persisted + surfaces
on the session list.** Second of two v1.0.703 on-device-smoke fixes.
The session list page no longer renders `(untitled session)` for
rows the user hasn't renamed — claude-code's auto-derived
`session_name` now persists across the hub→mobile boundary and
slots into a three-tier precedence ladder shared by every
session-title surface.

### Added

- **Hub: `sessions.session_name_hint` column.** Migration `0046`
  adds a nullable TEXT column to the sessions table. Populated by a
  new `captureSessionNameHint` hook fired on every `agent_events`
  insert — mirrors `captureEngineSessionID`'s gate
  (kind=`status_line`, producer=`agent`) and writes the latest
  non-empty `payload.session_name`. Empty / missing values are a
  no-op so the column survives the `/clear`-then-fresh-conversation
  window without blinking through `(untitled session)` on the list.
- **Hub: `session_name_hint` field on `sessionOut`.** Included in
  both LIST and GET responses so mobile can render the fallback
  without an extra round-trip. Search results extended to also
  carry the field alongside `session_title`.
- **Mobile: `sessionDisplayTitle(Map)` shared helper.** New file
  `lib/services/hub/session_display.dart` formalises the precedence
  `user title > session_name_hint > '(untitled session)'`. Trims
  defensively + tolerates missing keys (forward-roll safe for older
  hub payloads). Used by sessions list row, me-page recent card,
  and resume-into-chat title pass.

### Changed

- **Session list / me-page recent / search results all use the new
  precedence.** Old call sites in
  `lib/screens/sessions/sessions_screen.dart` (2 sites),
  `lib/screens/me/me_screen.dart`, and
  `lib/screens/sessions/search_screen.dart` rewritten to call the
  shared helper (search inlines because its row uses
  `session_title` keys instead of `title`).

### Tests

- `hub/internal/server/handlers_sessions_test.go` —
  `TestSessions_CapturesSessionNameHint` covers 7 cases:
  non-status_line event doesn't touch the column, first non-empty
  status_line writes, second-non-empty overwrites (latest-wins),
  empty `session_name` is a no-op (preserves prior value across
  `/clear`), missing key entirely is a no-op (codex/gemini), GET
  /sessions surfaces the column, LIST /sessions surfaces the column.
- `test/services/hub/session_display_test.dart` — 6 cases on the
  shared mobile helper: user title wins, hint fallback, placeholder
  fallback, missing-key tolerance, whitespace-trim, null coercion.

### Source

- `hub/migrations/0046_sessions_name_hint.up.sql` + `.down.sql`
- `hub/internal/server/handlers_sessions.go` — `captureSessionNameHint`,
  `sessionOut.SessionNameHint`, list+get SELECT extensions.
- `hub/internal/server/handlers_agent_events.go` — call into
  `captureSessionNameHint` from `handlePostAgentEvent`.
- `hub/internal/server/handlers_search_sessions.go` — search SELECT
  + output map extended with `session_name_hint`.
- `lib/services/hub/session_display.dart` (new)
- `lib/screens/sessions/sessions_screen.dart`,
  `lib/screens/me/me_screen.dart`,
  `lib/screens/sessions/search_screen.dart` — call sites.

---

## v1.0.704-alpha — 2026-05-25

**ADR-036 Phase B polish — compact rate-limit subline.** First of two
v1.0.703 on-device-smoke fixes. The 5h / 7d rate-limit chips no longer
emit the verbose `resets Mon 19:00` form on the sub-line; the
countdown is now compact (`43m`, `3h43m`, `3d19h`) at every horizon
and the absolute wall-clock moved into the long-press tooltip.

### Changed

- **`formatRateLimitResetsAt` compact contract.** Rewrote the W5
  formatter to always emit a single-unit-pair countdown (no `in` /
  `resets` prefix). Ladder: `<1m` / `Xm` / `Xh` / `XhYm` / `Xd` /
  `XdYh`. Width-bounded at 6 chars (`13d23h`) so both rate-limit
  tiles keep aligned baselines in the strip's `Row`. Past timestamps
  still render `now`; defensive null/0/>14d inputs still return
  empty.
- **Rate-limit chip tooltip carries the absolute reset wall-clock.**
  New companion `formatRateLimitResetsAtAbsolute` emits the
  `Mon 03:00` form; the tile composer splices `Resets in: <compact>
  (<absolute>)` into the existing tooltip. Flutter's `Tooltip`
  widget activates on long-press on mobile — same gesture the user
  expected.

### Tests

- `test/widgets/agent_feed_rate_limits_test.dart` extended to 24
  tests (up from 19). New cases pin the compact ladder at every
  unit boundary (`5m` / `1h` / `23h59m` / `1d` / `3d19h` / `14d`),
  a regression-pin that no output starts with `in ` / `resets `,
  and a 3-test group for the new absolute companion (format
  shape + device-local TZ rendering + defensive-inputs gate).

### Source

- `lib/widgets/agent_feed.dart` rewrites `formatRateLimitResetsAt`,
  adds `formatRateLimitResetsAtAbsolute`, and weaves the absolute
  string into the rate-limit tile's tooltip.

---

## v1.0.703-alpha — 2026-05-25

**ADR-036 Phase B W6 — `exceeds_200k_tokens` alarm + `session_name`
fallback. PHASE B COMPLETE.** The final two status_line-sourced
surfaces ship together; ADR-036's mobile-side delivery now mirrors
the hub-side fields one-for-one.

### Added

- **W6 hard-cap alarm tile.** Red `200K cap · consider /clear`
  tile rendered as the LEADING tile on the telemetry strip when
  the latest status_line frame carries `exceeds_200k_tokens: true`.
  Self-gates on false/null/missing. Reducer
  `exceeds200kFromEvents` is latest-wins (so `/clear` clears the
  alarm on the very next status_line frame; the tile doesn't
  stick).
- **W6 session-name fallback.** New `onSessionNameHint` callback
  on `AgentFeed`; reducer `sessionNameFromEvents` extracts the
  latest non-empty `session_name` from status_line.
  SessionChatScreen's new `_sessionNameHint` state field feeds a
  computed `_effectiveTitle()` getter: user-set title wins;
  otherwise claude's auto-derived hint; otherwise the
  `(untitled session)` placeholder. NEVER persisted to the hub —
  the hint is purely a display courtesy that re-reads fresh from
  status_line every render so `/clear`'s new session can show
  its own new label without state leaking from the prior
  conversation.

### Tests

- `test/widgets/agent_feed_w6_test.dart` — 15 tests across 2
  groups:
  - **`exceeds200kFromEvents` (7):** null-on-no-statusLine,
    null-on-no-field, explicit true, explicit false (distinct from
    null), latest-wins-on-flap (true → false on `/clear`),
    non-bool-treated-as-absent, empty list.
  - **`sessionNameFromEvents` (8):** null-on-no-statusLine, null-
    on-no-field, verbatim string return, empty-string-normalized-
    to-null, latest-wins on rename, walk-past-interleaved, non-
    String-treated-as-absent, empty list.

### Architecture notes

- The alarm tile shows red and goes LEADING-LEFT (vs the rate-
  limits chips that sit further right) — same hierarchy reasoning
  as "first-scan attention": the user reads left-to-right, and the
  hard-cap is the only state where they MUST act before the next
  turn. Other tiles are observational.
- The name-hint precedence (user > claude > placeholder) handles
  the natural workflow: a user can rename mid-session and the hub
  title locks in, claude's slowly-evolving label stays as a
  display fallback for unnamed sessions, and `(untitled session)`
  surfaces only when neither source has anything.
- Hint deferred-fire pattern mirrors `_maybeFireModeModelChanged`:
  detection in build(), post-frame callback schedule, parent
  setState is safe. Tracks `_lastSessionNameHintSet` so a flap
  through null (e.g. `name → null on /clear → newname` after
  claude re-derives) still surfaces both transitions.

### Plan status

- **ADR-036 PHASE B COMPLETE.** All 7 mobile surfaces shipped:
  W4-b (hub pricing infrastructure) · W4-a (process cost chip) ·
  W4-c (session cost chip) · W5 (rate_limits chip pair) · W6
  (200K alarm + session_name fallback). Plan total: 9 wedges
  across 8 commits (v1.0.696–v1.0.703); ~5,840 LOC; ~117 tests.

## v1.0.702-alpha — 2026-05-25

**ADR-036 Phase B W5: rate_limits chip pair from status_line lands
on the agent telemetry strip.** Surfaces the Anthropic plan headroom
that was previously invisible — 5-hour rolling window + 7-day
rolling window, both color-tiered (green / amber at ≥80% / red at
≥95%), with relative-or-absolute reset countdowns.

### Added

- **W5 reducer (`rateLimitsFromEvents`).** Latest-wins over
  `status_line.payload.rate_limits`. Returns the verbatim wire shape
  `{five_hour:{used_percentage,resets_at}, seven_day:{...}}` so the
  renderer reads the original keys (less indirection). Either
  sub-block may be absent on a given frame — the renderer self-gates
  per window.
- **Reset-time formatter (`formatRateLimitResetsAt`).** Renders
  `resets_at` Unix-epoch-seconds in device-local TZ per ADR-036 D7:
  - Relative `"in 4h 38m"` for horizons under 3h
  - Absolute `"resets Mon 03:00"` (zero-padded HH:MM, 3-letter
    weekday) otherwise
  - `"in <1m"` for sub-minute horizons (vs `"in 0m"`)
  - `"now"` for past timestamps (window already reset; next
    status_line frame refreshes the value)
  - Empty string for null/zero/negative + sanity-bounded reject
    of horizons > 14 days out (misinterpreted-unit guard)
- **Alarm-tier function (`rateLimitAlarmTier`).** Pure (pct → color
  + severity label). Plan spec: ≥80% amber, ≥95% red, else green.
  Returns a `(Color color, String severity)` record so the tile
  caller writes the tier just once.
- **Two rate-limit tiles in `_TelemetryStrip`.** Each tile shows
  `5h  24%` / `7d  33%` label + reset-or-window-label sub-line.
  Colored per its own window's alarm tier (the 5h can be red while
  the 7d is still green — that's actually the diagnostic story).
  Tooltip explains: which Anthropic window, current %, severity
  tier interpretation, resets timestamp, source-key reference.

### Distinct from the legacy rate_limit chip

The agent feed already had a single-window rate-limit tile reading
from claude's stream-json `rate_limit_event` (which fires only
when a limit hits — `status: limited|warn|allowed`). That stays.
The new pair from `status_line.rate_limits` is the ambient view —
both windows on every refresh, present even when no event has
fired. They render side-by-side; the legacy tile shows when claude
emits one, the new pair is always there once statusLine is wired.

### Tests

- `test/widgets/agent_feed_rate_limits_test.dart` — 19 tests across
  3 groups:
  - **`rateLimitsFromEvents` (6):** null-on-no-statusLine, null-on-
    no-rate_limits-block, verbatim wire-shape preservation, latest-
    wins, walks-past-interleaved, empty list.
  - **`formatRateLimitResetsAt` (7):** null/zero/negative → empty,
    past → "now", 3 relative-band points, exact-3h-boundary tips
    into absolute branch, device-local TZ verified via
    constructed-then-back-from-epoch, 14-day sanity-bound, sub-
    minute "in <1m".
  - **`rateLimitAlarmTier` (6):** < 80% → green (incl. 79.9
    boundary), 80–94.999% → amber (incl. exact-80 boundary),
    ≥ 95% → red, null → green, color-vs-severity invariant.

### Architecture notes

- Picked the 3h relative/absolute threshold based on a director's
  scan attention: any horizon under ~3h is "I might hit this
  during my current task"; past 3h is "schedule something for
  reset day" — different cognitive frames. The 3h cutoff is
  documented in the formatter constant so a future tweak is one
  line.
- Both windows' tiles render even when only one window's data is
  present on a given frame (the other tile suppresses). This is
  the right shape for the "statusLine carries some frames with
  partial data" defensive case — no per-tile dependency on the
  sibling.
- The probe-verified `resets_at` field is Unix-epoch seconds
  (10-digit values like 1779640200 — year 2026 in seconds; the
  same magnitude in ms would be 13 digits and lead with 17_796_…).
  Formatter assumes seconds; future engines that ship ms would
  need a magnitude-detect branch (out of scope; document in the
  reducer when antigravity ships its own statusLine equivalent).

### Plan status

- ADR-036 Phase B W4-a/b/c + W5 complete. **W6 only remains**:
  `exceeds_200k_tokens` AppBar alarm + `session_name` sticky-
  header fallback. ~120 LOC + ~5 tests. Both flutter, neither
  depends on the other.

## v1.0.701-alpha — 2026-05-25

**ADR-036 Phase B W4-a + W4-c (paired): mobile cost-chip pair lands
on the agent telemetry strip.** Companion to v1.0.700's hub-side
pricing infrastructure. The two chips ship together because they're
designed to be read together — process vs session cross-check tells
the director what mode the agent is in (fresh vs resumed vs mid-/clear).

### Added

- **W4-a: process-cost chip** (`lib/widgets/agent_feed.dart`,
  ~75 LOC). Reducer over `status_line` events
  (`processCostFromEvents`): latest-wins on
  `payload.cost.total_cost_usd`. Renders as `$X.XXXX · process` in
  the existing `_TelemetryStrip`. Self-gates blank when no
  status_line frame has carried a cost block yet (cold-open race,
  older claude versions, operator removed the install). A real
  `cost: 0` (fresh respawn, no turn yet) renders as `$0.0000` — the
  zero-vs-null distinction matters per ADR-036 D9.
- **W4-c: session-cost chip** (~115 LOC). Polls
  `GET /v1/teams/{team}/sessions/{session}/cost` on a 15s timer
  (matches statusLine cadence; longer would feel laggy on mid-turn
  watching, shorter is wasted polling). Renders as `$Y.YYYY · session`
  in cyan to distinguish from the green process tile. Tooltip
  composer (`buildSessionCostTooltipFromDetail`) surfaces per-model
  USD + token breakdown, snapshot_date + origin tier, and any
  unpriced models seen this session.
- **`hubClient.getSessionCost(id)`**
  (`lib/services/hub/hub_client.dart`). Thin wrapper around the W4-b
  endpoint; returns null on any error so the chip self-gates rather
  than blocking the strip on a transient hub blip.
- **Pair-aware tooltips.** When BOTH chips are visible, each
  tooltip cross-references the other ("Pair: process vs session —
  see the … chip to its left/right for the …"). Single-chip
  fallback is silent on the pair language.

### Lifecycle plumbing

- `_AgentFeedState.didUpdateWidget` now nukes the cached cost on
  sessionId change so a session swap doesn't briefly show the prior
  conversation's number under the new id. Timer restarts rooted at
  the new id; the immediate first-poll lights the chip without
  waiting a full interval.
- `dispose` cancels the new timer alongside the existing
  reconnect/banner timers — same pattern, no new lifecycle shape.
- In-flight responses are dropped when `widget.sessionId` flips
  mid-fetch (prevents the prior session's payload landing on the
  new chip).

### Tests

- `test/widgets/agent_feed_cost_chips_test.dart` — 14 tests across 3
  groups:
  - **`processCostFromEvents` (7):** null on no status_line, null on
    status_line without cost block, latest-wins across multiple
    snapshots, walks past interleaved events, zero-vs-null
    discipline, int-vs-double JSON-decode tolerance, empty-list
    handling.
  - **`buildSessionCostTooltipFromDetail` (6):** disclaimer line
    present + correct, 4-decimal precision, per-model breakdown
    sorted + with token annotations + omitting cache-zero rows,
    snapshot_date + origin tier rendered, missing_models surfaced,
    pair-context line appears iff pair=true.
  - **Cross-chip pair semantics (1):** the two reducers produce
    independent non-null values without conflict.

### Architecture notes

- The existing `totalCostUsd` chip (codex / antigravity legacy from
  `turn.result.cost_usd`) stays — it serves engines whose status_line
  channel doesn't exist. For claude-code, that old chip stays $0
  and the new pair takes the cost story. ADR-036 D9 "no chip is
  load-bearing" holds.
- Chip ordering in the strip reads as `[legacy cost] [process]
  [session] [tokens] [context-window] [rate-limits]` — cost cluster
  first so the dollars land in the first scan.
- The 15s poll cadence is the trade-off point. A future optimization:
  trigger an immediate refresh on each `usage` event arrival
  (consumer would learn that a new token-bearing event landed and
  the session total moved) — deferred until a user notices the
  current cadence isn't snappy enough.

### Plan status

- ADR-036 Phase B half-shipped: cost-chip pair (W4-a/b/c) complete.
  W5 (`rate_limits` row + alarm tier, ~250 LOC + ~10 tests) and W6
  (`exceeds_200k_tokens` alarm + `session_name` sticky-header
  fallback, ~120 LOC + ~5 tests) remain — both Flutter-only, neither
  depends on the other.

## v1.0.700-alpha — 2026-05-25

**ADR-036 Phase B W4-b: hub-side pricing infrastructure for the
session-cost chip.** First hub-only wedge of Phase B. Lays the
foundation that W4-c (mobile session chip) depends on — no mobile
work in this release; chips still render at v1.0.699 fidelity.

### Added

- **New package `hub/internal/pricing/`** (~380 LOC) implementing the
  three-tier resolution pattern from ADR-036 D10:
  - **Tier 1 — operator override.** Default path
    `$HUB_DATA/pricing/claude.yaml` (env `TERMIPOD_PRICING_CLAUDE`
    overrides). mtime-stat per render — no fsnotify goroutine; the
    chip cadence (few seconds) makes per-call stat negligible.
    Parse-error or validation-failure falls through to tier 2 and
    emits a `pricing.config_error` warn-log row (operator-visible
    via `hub-server` logs; full audit-row integration deferred until
    hub-global audit channel exists).
  - **Tier 2 — embedded default** via `//go:embed claude_default.yaml`.
    Snapshot-dated YAML header (`snapshot_date: 2026-05-25`) so a
    chip-tooltip can surface "rates as of …" to users. Ships with
    `claude-opus-4-7` ($15/$75/$1.50/$18.75 per 1M) and
    `claude-sonnet-4-6` ($3/$15/$0.30/$3.75 per 1M); `claude-haiku-4-5`
    held back as a commented sample until verified, so haiku
    sessions degrade blank per ADR-036 D9 rather than risk wrong.
  - **Tier 3 — per-model fallback.** Unknown model in both tiers →
    contribution dropped from total, model id surfaced via
    `Result.Missing` so the chip self-gates and the server can warn
    on first-sighting.

- **`pricing.SessionCost(ctx, db, loader, sessionID)`** — aggregates
  `agent_events.kind='usage'` rows for the session, sums tokens per
  model (input / output / cache_read / cache_write), and applies the
  resolved table. Tolerant of (a) usage events missing the `model`
  field (pre-W2 historical rows — dropped silently), (b) unknown
  models in the table (Missing list), and (c) sessions with no usage
  events at all (returns zero-valued Result, no error).

- **New endpoint `GET /v1/teams/{team}/sessions/{session}/cost`** —
  rich response shape (`session_cost_usd_imputed.handlers_session_cost.go`):
  `{total_usd, breakdown_by_model, tokens_by_model, missing_models,
  snapshot_date, origin, imputed: true}`. `imputed: true` is always
  set so mobile can render the subscription-disclaimer tooltip even
  when the chip degrades to zero. 404 on unknown session id (not
  500). Will be consumed by W4-c's session-cost chip.

- **`session_cost_usd_imputed` field on `GET /sessions/{id}`** —
  scalar total inlined on the parent GET so the chip lights up
  without a second round-trip on first paint. nil (omitted)
  when the session has zero priced usage; populated otherwise.
  NOT exposed on `LIST /sessions` (would impose O(N) sessions
  × O(M) events per list call; deferred to a future wedge if the
  agents list ever surfaces the chip in-row).

### Tests

- `hub/internal/pricing/loader_test.go` (9 tests): embedded-default
  CI guard; env-override path wins; default-disk path wins; embedded
  fallback when override missing; parse-error + validation-error
  warn+fall-through; mtime reload; unchanged-mtime cache return;
  `ErrUnknownModel` for unknown ids.
- `hub/internal/pricing/compute_test.go` (8 tests including a 7-case
  table-driven `TestCostFromTokens`): per-class arithmetic; empty
  session; single-message attribution; multi-message per-model
  aggregation; unknown-model degradation; cross-session isolation;
  kind-filter (only `usage` counts); nil-guard; missing-model
  payload tolerance.
- `hub/internal/server/handlers_session_cost_test.go` (4 tests):
  empty-session 200 with embedded snapshot_date; with-usage-events
  integration vs the real embedded rates; 404 on unknown session
  id; inline scalar field present iff session has priced usage.

### Architecture

- **Sister to ADR-027 hooks-install.** Where hooks-install owns a
  per-workdir config field (settings.local.json's `hooks`), pricing
  owns a hub-global config field that's *operator-tunable without
  restart*. Different file-ownership shapes; same merge-don't-clobber
  + hot-reload discipline. See
  [feedback_hot_loadable_config_with_embedded_default](memory)
  for the generalizable pattern.
- **No claude-specific code in `internal/pricing/`** — the package
  takes model ids verbatim and the YAML schema is engine-neutral.
  When a future engine ships a USD-pricing-relevant model id, drop
  a sibling YAML (e.g. `gemini_default.yaml`) and add an engine
  selector to the loader. Out-of-scope for W4-b.

### Plan status

- ADR-036 plan W4-b ships green. Mobile work (W4-a + W4-c paired,
  designed to be read together) is next — gated on principal user
  acceptance of the hub side rather than another wedge ship.

## v1.0.699-alpha — 2026-05-25

**ADR-036 Phase A.5: mobile feed-bubble + busy-state regression fix
for `status_line` events.** Phase A's hub-side wedges (v1.0.696-698)
put the new `status_line` AgentEvent on the wire but the mobile
consumer's per-kind dispatch lists weren't swept — the new kind
fell through both gates, with two visible cold-open regressions:

1. **Raw JSON snapshot rendered as a feed bubble.** ADR-036 D4
   explicitly forbids this — `status_line` is a periodic-snapshot
   reducer-source for chips, not a transcript bubble. `~10s` cadence
   would also have flooded the transcript with ~360 frames/hour on
   long-running agents.
2. **Busy-state pinned to true on cold-open.** The first
   `status_line` fires within seconds of spawn (before any user
   turn), and `_isAgentBusy` fell through to its default
   "agent-side event = turn-active" branch. With no
   `turn.result` / `session.init` / `completion` to clear it, the
   spawn stuck at busy(cancel) until the first real assistant
   message. Same regression class as v1.0.667 (when `usage` was
   pinning the busy pill after every turn.result).

### Added

- `lib/widgets/agent_feed.dart` — two `@visibleForTesting`
  top-level sets, `kAgentFeedAlwaysHiddenKinds` (which kinds are
  reducer-source / chip-source rather than transcript-source) and
  `kAgentBusyInferenceSkipKinds` (which kinds the busy-state
  inference walks past instead of treating as turn-active). Both
  now include `status_line`. The previous inline `if (kind == 'x'
  || kind == 'y')` chains have been refactored to `.contains()`
  checks against these sets so the kind-list contracts are
  testable as data instead of buried in widget internals.
- `test/widgets/agent_feed_status_line_test.dart` — pins the two
  set contracts (status_line membership; pre-ADR-036 kinds still
  hidden; non-membership of transcript-first kinds like text /
  thought / tool_call; cross-set symmetry).

### Fixed

- Cold-open status_line no longer renders as a JSON bubble in the
  agent feed.
- Cold-open status_line no longer pins the busy pill / cancel
  button to true before the first user turn.

### Notes

- Phase A is now functionally complete: hub-side (v1.0.696-698) +
  mobile consumer fix (v1.0.699). Phase B (chip additions for
  cost / effort / thinking / fast_mode / rate_limits /
  exceeds_200k_tokens / session_name) remains the next ship arc.
- The status_line events still accumulate in the mobile event
  list (we don't drop them at fetch time). Wedge note for Phase B:
  if storage churn matters on long-running agents, gate the fetch
  at the hub side or coalesce by latest-per-session.

---

## v1.0.698-alpha — 2026-05-24

**ADR-036 Phase A W3: session_id rotation handler — fixes /clear
blindness in the M4 LocalLogTail adapter.** Hub-only wedge. Closes
out Phase A. Pre-W3 a `/clear` within a running claude process
minted a new session_id + JSONL file; the adapter kept tailing the
OLD JSONL until respawn (a latent bug that existed regardless of
any chip work). statusLine carries both `session_id` and
`transcript_path`, so we can re-point the tailer in-band and
re-emit `session.init` so mobile renders the post-/clear conversation
as a new conversation correctly.

### Added

- `hub/internal/drivers/local_log_tail/claude_code/adapter.go` —
  Adapter gains `pendingTranscriptPath` + `pendingSessionID` fields
  (pendingMu-guarded). `OnStatusLine` gained 4 new responsibilities
  beyond W2's cache-update: (a) extract `session_id` and
  `transcript_path` from each statusLine frame, (b) gate rotation on
  three preconditions (engineSessionID resolved, session_id changed,
  transcript_path non-empty), (c) stamp pending fields, (d) call
  `tailer.Stop()` to close the lines channel cleanly.

### Changed

- `resolveAndRun` (adapter.go) — refactored from a one-shot pipeline
  into a session-loop. Each iteration tails one JSONL from start to
  exit; after `runLoop` returns, the loop checks
  `pendingTranscriptPath` and either re-enters with the new path
  (and `tailMode = StartFromBeginning` so we catch any content
  claude wrote between minting the file and our rotation handler
  running) or exits if no rotation is pending. The
  `sessionInitSent` guard is reset on rotation so the next usage
  event re-emits `session.init` with the new session_id — mobile
  re-renders chips on session.init, which is the right semantic for
  "new conversation".

### Tests

- `hub/internal/drivers/local_log_tail/claude_code/adapter_statusline_test.go`
  (3 new cases, 8 total in file):
  - **Rotation happy path** — first session.init carries
    sess-rot-1; statusLine fires with sess-rot-2 + new
    transcript_path; appending to the new JSONL produces a SECOND
    session.init carrying sess-rot-2 (proves the tailer re-pointed
    AND sessionInitSent reset).
  - **No rotation on same session_id** — steady-state statusLine
    refreshes (every ~10s during the same conversation) MUST NOT
    churn the tailer. `pendingTranscriptPath` stays empty.
  - **No rotation before first resolution** — statusLine frames
    arriving during the launch race (engineSessionID still empty)
    don't trigger rotation. Latest-frame cache is still updated for
    W2's field overrides.

### Plan status

**[ADR-036 plan](plans/claude-code-statusline-as-telemetry.md) Phase
A COMPLETE.** Hub-side ships three wedges (W1+W2+W3, v1.0.696-698).
Phase B (mobile chips for cost, effort, thinking, fast_mode,
rate_limits, exceeds_200k_tokens, session_name) is the next ship
arc, separately scoped. Phase A alone earns its keep: kills the
context-window heuristic as the primary signal, replaces the
hardcoded version literal with the real binary version, and fixes
the /clear-blindness bug.

---

## v1.0.697-alpha — 2026-05-24

**ADR-036 Phase A W2: claude-code adapter consumes statusLine for
in-process field overrides; `claudeModelContextWindow` relegated to
fallback-tier.** Hub-only wedge. Pairs with W1's wire emission to
turn statusLine snapshots into authoritative values on `session.init`
and `usage` events. No mobile changes yet — pure correctness uplift
on the hub side.

### Added

- `hub/internal/drivers/local_log_tail/claude_code/adapter.go` —
  Adapter gains a `latestStatusLine` cache (RWMutex-guarded) +
  `OnStatusLine(ctx, payload)` method (implements the W1
  `StatusLineSink` interface). Two accessors —
  `statusLineVersion()` and `statusLineContextWindow()` — return the
  cached fields with the JSON-decoding numeric-shape tolerance
  (float64 / int / json.Number).
- `hub/internal/hostrunner/launch_m4_locallogtail.go` —
  `gw.StatusLineSink = adapter` (sister to `gw.HookSink = driver`).
  Wires the per-spawn UDS gateway's statusLine seam to the
  claude-code adapter so OnStatusLine fires synchronously after the
  gateway posts the AgentEvent.

### Changed

- `maybeEmitSessionInit` (adapter.go) — `session.init.version` now
  prefers the statusLine-sourced binary version (e.g. "2.1.150")
  over the hardcoded "claude-code" literal. Falls back to the
  literal during the cold-open race before the first statusLine
  frame.
- `runLoop` (adapter.go) — `usage.context_window` is overridden
  in-place with the statusLine-sourced authoritative value when
  available. The mapper still emits its
  `claudeModelContextWindow`-derived best-guess as a floor; the
  override is purely additive (no change to events the heuristic
  already gets right; corrections for what it doesn't).
- `claudeModelContextWindow` (mapper.go) — RELEGATED to fallback
  per ADR-036 D6. Function header gains an explicit note pointing
  to the authoritative source. Function body unchanged — the
  three-tier resolution (env → legacy override → prefix family) is
  the floor when statusLine isn't available (older claude, cold-open
  race, operator removed the statusLine install).

### Tests

- `hub/internal/drivers/local_log_tail/claude_code/adapter_statusline_test.go`
  (5 new cases) — nil OnStatusLine is a no-op; helpers return zero
  before first frame; context_window decode handles all three JSON
  numeric shapes; integration tests show session.init uses
  statusLine version after a frame, usage event uses statusLine
  context_window after a frame, AND usage falls back to the
  heuristic when no statusLine has fired (locks the relegation —
  not removal — contract).

### Plan status

[ADR-036 plan](plans/claude-code-statusline-as-telemetry.md) Phase A
W2 ✓ shipped. W3 (session_id rotation handler — fixes /clear
blindness in M4 by re-pointing the tailer at the new
`transcript_path`) is the last Phase A wedge.

---

## v1.0.696-alpha — 2026-05-24

**ADR-036 Phase A W1: host-runner `status-fire` shim + UDS gateway
status_line tool + per-spawn dedupe + settings.local.json install.**
First wedge of the claude-code statusLine telemetry rollout. The hub
side of the wire now has everything it needs to receive periodic
statusLine snapshots from M4 LocalLogTail claude-code spawns; mapper
+ adapter consumers land in W2/W3 next.

### Added

- `hub/internal/statusfire/run.go` (+201 lines) — new package backing
  the `host-runner status-fire` subcommand. Reads claude-code's
  statusLine JSON from stdin, posts it as a JSON-RPC `tools/call
  status_line` to the per-spawn UDS gateway, prints a single line to
  stdout for claude to render. Best-effort by design — transport
  failures degrade quietly (exit 0 + stderr note) because the ~10s
  cadence makes loud failures intolerable in the TUI. Carries a
  `--wrap` flag for operator-set statusLine passthrough.
- `hub/cmd/host-runner/main.go` — `status-fire` subcommand dispatch
  (mirrors the `hook-fire` pattern; same multicall shape).
- `hub/internal/hostrunner/mcp_gateway.go` — new `status_line` tool
  in the catalog + `dispatchStatusLine` handler. 1s identical-payload
  dedupe (SHA-keyed, per-spawn) catches the host-verified turn-end
  doubles (~0.3s apart, identical bytes). On non-deduped fires,
  forwards as `AgentEvent{kind:"status_line", producer:"agent"}` via
  the existing hub client.
- `hub/internal/hostrunner/mcp_gateway.go` — `StatusLineSink`
  interface (forward-compat hook for W3's session_id rotation
  handler). Sink is invoked SYNCHRONOUSLY after the hub post; nil
  sink falls back to default "post and move on" behaviour.
- `hub/internal/hostrunner/hooks_install.go` — new
  `installClaudeStatusLine(workdir, hostRunnerExe, udsSocket)`.
  Atomic-rename merge into `.claude/settings.local.json` alongside
  the hooks block. Wrap-and-passthrough preserves operator-set
  statusLine — operator's `command` is recorded under
  `_termipod_wrapped_command` and re-emitted via the shim's `--wrap`
  flag, so operator config stays visible while telemetry is additive.
- `hub/internal/hostrunner/launch_m4_locallogtail.go` — call
  `installClaudeStatusLine` right after `installClaudeHooks`. Failure
  is non-fatal: the chip strip degrades to the pre-ADR-036
  JSONL-derived baseline rather than blocking the spawn.

### Tests

- `hub/internal/statusfire/run_test.go` (2 cases) — round-trip via
  fake UDS gateway; dial-error surface when socket gone.
- `hub/internal/hostrunner/hooks_install_test.go` (4 new cases) —
  cold install (no prior block, no --wrap), wraps operator-set
  statusLine (records `_termipod_wrapped_command`, emits `--wrap`),
  re-install preserves wrap across UDS-socket rotation, coexists
  with `installClaudeHooks` (neither clobbers the other; reinstall
  cycle stable).
- `hub/internal/hostrunner/mcp_gateway_test.go` (2 new cases) —
  catalog + dedupe (1st identical fire forwards; 2nd within 1s
  drops with `deduped:true` flag; differing payload always forwards);
  StatusLineSink invoked with the decoded payload.

### Plan status

[ADR-036 plan](plans/claude-code-statusline-as-telemetry.md) Phase A
W1 ✓ shipped. W2 (mapper consumes `status_line`; relegate the
context-window heuristic) and W3 (session_id rotation handler) next
on the hub-only side; W4-W6 (mobile chips) follow once Phase A is
complete.

---

## v1.0.695-alpha — 2026-05-24

**ADR-030 Phase 3 W19: steward-side propose inbox.** Final wedge of
Phase 3. AppBar icon-button on every session screen self-gates to
visibility-when-relevant; tap opens a list view of propose rows
addressed to the project-steward tier, scoped to the steward's
project, rendered via the same per-kind cards (W15-W18) the
principal's Me-page uses — primary variant (Approve/Reject) because
the steward IS the addressee.

**Status: ADR-030 ALL PHASES COMPLETE** (Phase 1 + Phase 2 + Phase 3,
v1.0.674-695, 22 wedges across 24 commits).

### Added

- `lib/screens/sessions/widgets/steward_propose_inbox.dart` — two
  widgets + one public predicate:
  - `StewardProposeInboxPill` — AppBar icon button (inbox icon +
    amber count badge) with self-gating: hidden unless
    `agentKind.startsWith('steward.')` AND `projectId.isNotEmpty`
    AND ≥1 matching propose row. Safe to drop in every
    session AppBar.
  - `StewardProposeInboxScreen` — list view pushed by the pill.
    Each row renders via `ProposeCardRouter(myTier:
    'project-steward')` so the per-kind cards show their PRIMARY
    variant for the addressee. Empty-state explains the surface.
  - `stewardProposeInboxRows(attention, projectId)` — 4-clause
    predicate (`kind=propose` AND `assigned_tier=project-steward`
    AND `status=open` AND `project_id` matches). Exposed publicly
    so tests can verify it without widgets.
- `test/screens/sessions/steward_propose_inbox_test.dart` — 8 cases:
  5 predicate tests (empty list / canonical 4-clause filter /
  multi-match order preservation / empty projectId zero matches /
  legacy row without project_id never matches) + 3 widget gating
  tests (hidden when agentKind not a steward / hidden when
  projectId empty / hidden when no matching rows).

### Changed

- `lib/screens/sessions/sessions_screen.dart` —
  `SessionChatScreen` build path computes `agentProjectId` from
  the agent row and threads it + `_agentKind()` into the new pill
  at the start of the AppBar `actions:` list. Self-gating means
  worker / general-steward / team-only sessions stay unchanged.

### Forensics

Mobile binary shifts (1 new widget file + 1 new test + sessions
screen wiring). Hub binary unchanged. pubspec 1.0.694 → 1.0.695.

### ADR-030 final status

22 wedges shipped across 24 commits (v1.0.674-695):
- Phase 1 (11 hub wedges): migration 0045, policy.Kinds, propose
  registry, mcpPropose, 5 apply functions (deliverable / phase /
  task / agent / template), permission_prompt re-addressing,
  override path, fan-back envelope, loop-sweep escalation audit
- Phase 2 (3 wedges): re-propose rule in 9 stewards, 10 lifecycle
  scenarios S33-S42, seed-demo annotation
- Phase 3 (8 wedges): hub fields exposure (W19.6 hub), mobile
  filter + predicate (W19.5), 5 per-kind cards (W15-W18 covers
  5 kinds), policy viewer (W21), override sheet (W20), stalled
  digest (W19.6-mobile), steward inbox (W19)

Open follow-ups: W20-resolved (override on resolved rows in
Me-page), deferred propose kinds (criterion.set_state,
agent.terminate, agent.archive, permission_policy.change, etc.
per plan §5), tier-identity check on override (currently soft
string match against `@principal`).

---

## v1.0.694-alpha — 2026-05-24

**ADR-030 Phase 3 W19.6-mobile: top-of-Me stalled-decisions digest
card.** The D-7 Option 2′ signal walk's mobile surface — surfaces
the count of propose rows that escalated to the principal's tier
but haven't been decided. Hidden when there are zero stalled rows
(no visual clutter); rendered above the existing chip-filter bar
with a tap-to-filter affordance.

### Added

- `lib/screens/me/widgets/stalled_decisions_digest.dart` —
  amber-bordered card with `Icons.schedule`, header, count badge,
  and a subtitle that splits "N stalled at stewards" vs "N stalled
  with you" (rows where `escalation_state == 'escalated_principal'`).
  Hidden when total stalled count = 0. Tap toggles
  `stalledFilterProvider` (NotifierProvider<bool>); when ON, the
  Me-page item list filters further to `escalation_state != 'none'`
  rows AND-combined with the active chip-filter. Header copy flips
  to "Showing stalled decisions" + active-state border highlight
  when ON.
- Helpers in the same file: `hasStalledDecisions(items)`,
  `stalledDecisionsCount(items)`,
  `stalledOverDayDecisionsCount(items)` — the last counts only
  `escalated_principal` rows so the digest distinguishes "with
  stewards still" from "with you now". Tests + the upcoming W19
  steward inbox will reuse these.
- `test/screens/me/stalled_decisions_digest_test.dart` — 9 cases:
  5 pure-function counter tests + 4 widget tests (render-when-
  count=0 / render-when-count>0 / split-subtitle / tap-toggles-
  filter-and-flips-header).

### Changed

- `lib/screens/me/me_screen.dart` — digest sliver inserted above
  `_SectionLabel`. Filter narrowing applied in the items pipeline:
  `items.where(filter.matches).where(stalledFilter)`. New imports
  for the digest helpers + `isStalledPropose` predicate.

### Forensics

Mobile binary shifts (1 new widget + 1 new test + 3 me_screen
edits). Hub binary unchanged. pubspec 1.0.693 → 1.0.694.

---

## v1.0.693-alpha — 2026-05-24

**ADR-030 Phase 3 W20: override confirmation sheet.** Modal bottom
sheet that replaces v1.0.688's inline AlertDialog placeholder.
Surfaces the change_kind + change_spec preview so the principal can
see what they're overriding before they type the required reason.

**Scope narrowed** per the plan rewrite — the original W20 said
"Resolved rows gain an … overflow menu with Override decision", but
the current Me-page only renders OPEN rows (listAttention filters
status='open'). Supporting override on resolved rows requires a
separate resolved-rows toggle/filter — a feature, not a small UI
affordance. Shipped the visible MVP improvement (the sheet itself);
override on resolved rows deferred to **W20-resolved**. The override
path itself works fine via direct REST today; the gap is only the
Me-page surface for resolved rows.

### Added

- `lib/screens/me/widgets/override_sheet.dart` — modal bottom sheet
  with drag handle, gavel-icon header, explanation paragraph naming
  the addressee + ADR-030 W9 Rollback semantic, context block
  (change_kind + change_spec + target_ref, compact 1-line previews),
  autofocus required-reason TextField, inline error on empty submit
  (no snack — sheet stays open), Submit button with inline
  CircularProgressIndicator during the decide round-trip. Returns
  `bool` (true on success, false on cancel).
- `test/screens/me/override_sheet_test.dart` — 4 widget cases:
  shows change_kind/addressee/reason field/Override button; Cancel
  returns false + closes sheet; empty reason → inline error + stays
  open; change_spec preview includes from_state + to_state.

### Changed

- `lib/screens/me/widgets/propose_card_actions.dart` —
  `StalledProposeActions` API changed: takes the full `attention`
  Map<String, dynamic> instead of just `id` so the sheet can render
  the change_kind + change_spec context without a second fetch.
  All 5 propose cards (W15-W18) updated to pass `attention:` instead
  of `id:`. Existing widget tests pass unchanged because they assert
  on rendered text, not the API shape.

### Deferred (W20-resolved)

- Override affordance on RESOLVED rows in Me-page — currently the
  Me-page only fetches `status='open'` rows; supporting an Override
  affordance on resolved rows requires a resolved-rows toggle/filter
  + the listAttention(status='resolved') call path. The override
  REST endpoint itself works today (see hub-side W9 path); the gap
  is only the surface.

### Forensics

Mobile binary shifts (1 new sheet + 5 caller updates + 1 new test
+ propose_card_actions API change). Hub binary unchanged.
pubspec 1.0.692 → 1.0.693.

---

## v1.0.692-alpha — 2026-05-24

**ADR-030 Phase 3 W21: read-only governed-action policy viewer** on
the team-settings surface. **Re-homed out of mobile Settings**
per the principal's clarification — mobile Settings is for mobile
preferences (theme, voice, etc); team/hub-scoped settings belong on
the team surface reachable via the team switcher. The viewer renders
the `kinds:` block of `<dataRoot>/team/policy.yaml` as a four-column
table (kind / default tier / commits / override allowed).

### Added

- `hub/internal/server/handlers_policy.go::handleGetPolicyKinds` —
  new `GET /v1/teams/{t}/policy/kinds` endpoint returns the parsed
  `kinds:` block from team/policy.yaml as JSON so the Flutter binary
  doesn't need a YAML parser. Empty file or legacy file without the
  `kinds:` block → `{"kinds": {}}`; mobile renders the empty-state
  explaining where to edit. Malformed YAML on disk → 500 surfaces
  the parse error to the operator. Read-only; canonical edit path
  stays `PUT /policy`.
- `hub/internal/server/handlers_policy_kinds_test.go` — 4 test cases
  covering missing-file, full-policy parses every field, legacy
  policy without kinds block, malformed yaml returns 500.
- `KindPolicy` + `QuorumPolicy` structs in `policy.go` gained
  `json:` tags alongside the existing `yaml:` tags so JSON
  serialization round-trips per ADR-030 spec.
- `lib/services/hub/hub_client.dart::getPolicyKinds` — calls the new
  endpoint, returns `Map<String, dynamic>` keyed by kind name.
- `lib/screens/team/governed_actions_policy_screen.dart` (new) —
  read-only viewer reached via Team switcher → Team settings →
  Governance tab → "Governed action policy" tile. Four-column
  table (kind / default tier / commits / override allowed), sorted
  by kind name. Empty-state pointing the operator at the Policies
  sibling tab for editing. Reload button in the AppBar.
- `lib/screens/team/team_screen.dart` — new ListTile under the
  Governance tab (the existing `_SettingsView`): "Governed action
  policy". Icon `Icons.gavel_outlined`. Sibling to Budgets / Auth
  / Councils / Steward.

### Changed

- Plan §4.3 W21 entry rewritten with the re-homing note + shipped
  reality + verify anchors. The re-homing note codifies the
  general principle for Phase 3 surfaces: policies in
  `<dataRoot>/team/` belong on team settings; policies in
  `~/.shared_preferences` belong on mobile Settings. The user's
  clarification is captured for future contributors.

### Forensics

Hub binary shifts (new endpoint + KindPolicy/QuorumPolicy JSON tags).
Mobile binary shifts (1 new screen + 1 new hub_client method + 1
new team_screen tile). pubspec 1.0.691 → 1.0.692. Hub test suite
green (126.811s server pkg, full suite); 4 new tests pass.

---

## v1.0.691-alpha — 2026-05-24

**ADR-030 Phase 3 W18: per-kind propose cards for `agent.spawn` +
`template.install`.** All 5 MVP propose kinds now have per-kind cards
in the Me-page router; unknown change_kinds fall through to the legacy
`InlineApprovalActions` (which already handles approval_request and
template_proposal via the pre-ADR-030 flows).

**Plan-literal reinterpretation.** Plan W18 named the wedge
"worker_tool_call.escalate" — that's not a Phase 1 propose kind.
The worker-tool-escalation flow is realised as `permission_prompt`
after W10's parent-steward re-addressing; its card is the existing
`InlineApprovalActions` fallback. Shipped the structural intent —
per-kind cards for the two ALIAS kinds (agent.spawn +
template.install — the legacy approval_request / template_proposal
flows that W8 re-routed through propose). See
[[feedback_plan_narrative_loose_talk]] for the pattern.

### Added

- `lib/screens/me/widgets/propose_card_agent_spawn.dart` — compact
  body: child_handle (bold mono) + engine kind chip (deep purple),
  reason, host (when pinned), project (when bound). Punts full
  spawn_spec_yaml to the Details affordance. Stalled variant uses
  "View spawn detail" label.
- `lib/screens/me/widgets/propose_card_template_install.dart` —
  compact body: `<category>/<name>` path (bold mono with file
  icon), rationale, proposed_by handle, blob sha256 12-char
  prefix. Punts full body to the Details affordance (the legacy
  v1.0.602 template-proposal preview block already renders the
  full YAML there). Stalled variant uses "View template body"
  label.
- `test/screens/me/propose_card_alias_test.dart` — 7 widget cases
  across both cards: primary variants with full body rendering;
  stalled variants with Override + tailored View label; edge
  cases (missing handle → "(no handle)"; missing category/name
  → "(unknown)"; missing rationale → block omitted).

### Changed

- `lib/screens/me/widgets/propose_card_router.dart` — agent.spawn +
  template.install registered. Closing comment updated to note all
  5 MVP propose kinds are covered.

### Forensics

Mobile binary shifts (2 new cards + 1 new test + router
registration). Hub binary unchanged. pubspec 1.0.690 → 1.0.691.

---

## v1.0.690-alpha — 2026-05-24

**ADR-030 Phase 3 W17: per-kind propose card for `task.set_status`** +
fix for v1.0.689's `library_directive_not_first` lint error.

### Added

- `lib/screens/me/widgets/propose_card_task.dart` — body: `→ status`
  (no from-side chip; task.set_status's change_spec has no
  `from_status` field — the hub Apply compares the row's current
  status at runtime), result_summary as a wrapped quote-block when
  present (recommended for `done`, allowed-but-pointless for
  `cancelled`), task + project ids stacked. Status chip uses the
  blueGrey/slate family per the W16 visuals colour-family map.
- `test/screens/me/propose_card_task_test.dart` — 7 widget cases:
  primary variant + chip + summary block + ids + absent-summary
  hidden + stalled variant + addressee pill.

### Changed

- `lib/screens/me/widgets/propose_card_router.dart` — task.set_status
  registered alongside deliverable.set_state + phase.advance.

### Fixed

- v1.0.689's `propose_card_visuals.dart` placed the `library;`
  directive AFTER the imports (it was tucked inside the file's
  docstring, but Dart's parser counts the import block first).
  Flutter analyze flagged it as `library_directive_not_first`
  (error level — fatal under the project's `flutter analyze
  --no-fatal-infos` config), failing CI on the v1.0.689 push.
  Fixed by moving `library;` to position 19 (immediately after
  the docstring, before the first import).

### Forensics

Mobile binary shifts (1 new card + 1 new test + router registration
+ visuals lint fix). Hub binary unchanged. pubspec 1.0.689 → 1.0.690.

---

## v1.0.689-alpha — 2026-05-24

**ADR-030 Phase 3 W16: per-kind propose card for `phase.advance`** +
shared visuals refactor + fix for v1.0.688's lint warning.

### Added

- `lib/screens/me/widgets/propose_card_phase.dart` — primary +
  stalled variants. Body: `from_phase → to_phase` transition with
  project id + summary. Handles the optional `from_phase` (renders
  `→ to_phase` without a from-side chip when the optimistic-
  concurrency check is omitted — this is the per-kind quirk
  unique to phase.advance among the W15-W17 state-shaped cards).
- `lib/screens/me/widgets/propose_card_visuals.dart` — shared
  visual + parsing primitives extracted once W16 made the
  duplication concrete. Exports `decodeJsonObject` (defensive
  JSON-or-Map decoder), `StalledPill`, `TransitionChip` +
  `TransitionFrame` + `TransitionChipFamily` enum
  (state=green / phase=indigo / status=slate). Different colour
  families per kind so propose-rows are at-a-glance distinguishable.
- `test/screens/me/propose_card_phase_test.dart` — 7 widget cases
  including the from-phase-omitted forced-advance scenario.

### Changed

- `lib/screens/me/widgets/propose_card_deliverable.dart` refactored
  to consume the shared visuals (-130 LOC of local primitives).
  Behaviour unchanged; test suite passes unchanged.
- `lib/screens/me/widgets/propose_card_router.dart` — phase.advance
  registered alongside deliverable.set_state.

### Fixed

- v1.0.688's `propose_card_deliverable.dart` carried an unused
  `hub_provider.dart` import (left over from the original draft
  before actions were extracted into `propose_card_actions.dart`).
  Flutter analyze flagged it as `unused_import` (warning level),
  failing CI on the v1.0.688 push. The visuals refactor removed
  it along with the local primitives; CI green again at v1.0.689.

### Forensics

Mobile binary shifts (1 new card + 1 new visuals file + 1 new test
+ deliverable card refactor + router registration). Hub binary
unchanged. pubspec 1.0.688 → 1.0.689. Verified on CI per repo
convention.

---

## v1.0.688-alpha — 2026-05-24

**ADR-030 Phase 3 W15: per-kind propose card for `deliverable.set_state`.**
First of the four per-kind cards (W15-W18); also ships the shared
action-row helpers (`PrimaryProposeActions` / `StalledProposeActions`)
+ the `ProposeCardRouter` dispatch widget that the remaining three
cards will register against. me_screen.dart's new approvals branch
picks the per-kind card via the router; unrecognised change_kinds
fall back to the legacy Approve/Reject pair so partially-rolled-out
new kinds stay usable.

### Added

- `lib/screens/me/widgets/propose_card_deliverable.dart` — primary +
  stalled variants. Body block: from_state → to_state transition
  chips (to_state in green/emphasis), summary text, deliverable id
  monospace. Primary actions: Approve / Reject. Stalled actions:
  Override (opens an inline reason-prompt dialog → decide with
  `override=true`) + View deliverable.
- `lib/screens/me/widgets/propose_card_actions.dart` —
  `PrimaryProposeActions` (Approve/Reject buttons that POST decide
  via hubProvider) + `StalledProposeActions` (Override that prompts
  for a required reason, then POSTs decide with `decision='override',
  override=true` — W20 will replace this inline dialog with the
  D-8 confirmation sheet).
- `lib/screens/me/widgets/propose_card_router.dart` — single dispatch
  widget that picks the per-kind card by `change_kind`. Currently
  only `deliverable.set_state` is registered; W16-W18 register here
  as they ship. Unrecognised change_kinds (or kind != 'propose')
  fall back to `InlineApprovalActions`.
- `lib/screens/me/me_screen.dart` — new `else if (item.kind ==
  'propose' && item.attention != null)` branch routes through the
  router BEFORE the legacy `InlineApprovalActions` fallback. Tier
  hardcoded to `'principal'` for MVP.
- `lib/services/hub/hub_client.dart::decideAttention` — gained
  `override: bool` named param. Forwarded to the POST body as
  `{override: true}` only when set; existing Approve/Reject call
  sites unaffected.
- `lib/providers/hub_provider.dart::decide` — same `override: bool`
  pass-through.
- `test/screens/me/propose_card_deliverable_test.dart` — 10 widget
  test cases covering both variants + edge cases (addressed-and-
  stalled → primary; legacy row → primary fallback; change_spec
  parses from raw JSON string).

### Changed

- ADR-030 plan stamp bumped to v1.0.688-alpha. W15 entry rewritten
  with shipped reality + verify anchors. Notes that `dry_run`
  preview rendering is deferred — the W4 mcpPropose dry_run path
  returns the preview synchronously to the proposing agent without
  creating an attention row, so there's no row for the card to
  decorate today.

### Forensics

Hub binary unchanged (this is mobile-only work + a hub_client
field addition that compiles into the mobile binary). Mobile
binary shifts (4 new widgets files + me_screen.dart + provider
+ client + test). pubspec 1.0.687 → 1.0.688. Verified on CI
(Flutter not installed locally per repo convention).

---

## v1.0.687-alpha — 2026-05-24

**ADR-030 Phase 3 W19.6 hub + W19.5 mobile.** Wire-contract foundation
for the per-kind propose cards (W15-W18). Hub `attentionOut` gains the
5 ADR-030 columns from migration 0045 (`change_kind`, `assigned_tier`,
`change_spec`, `target_ref`, `executed`) plus `escalation_state` from
migration 0042 so the mobile per-kind cards (forthcoming W15-W18) and
the steward-side inbox (W19) can render variants in a single fetch.
Mobile gains the `propose` → `_Filter.approvals` mapping + the
`isAddresseeOfPropose` / `isStalledPropose` / `stalledPillLabel`
predicates that the per-kind cards will consume to pick between
primary (Approve/Reject) and stalled (Override/View source) variants.

### Added

- `hub/internal/server/handlers_attention.go::attentionOut` — 6 new
  fields with omitempty (`ChangeKind`, `AssignedTier`, `ChangeSpec`,
  `TargetRef`, `Executed`, `EscalationState`). Both
  `handleListAttention` and `handleGetAttention` SELECT-widened to
  fetch them; pre-ADR-030 rows elide via COALESCE + omitempty so
  no API consumer sees a behavior change. The hub side accepts an
  `include_escalated` query parameter (parsed but unused in MVP — a
  forward-compat hook for when `?tier=<t>` narrowing arrives;
  mobile W19.5 passes it unconditionally so the contract locks in
  before any tier-narrow widening).
- `hub/internal/server/handlers_attention_adr030_fields_test.go` —
  6 test cases covering: propose-shaped row exposes all 5 ADR-030
  fields on list endpoint; same on get endpoint; legacy row omits
  via omitempty; escalated row exposes `escalation_state` AND
  preserves `assigned_tier` (the D-7 Option 2′ "decision stays"
  contract); `include_escalated` query param parses with any value
  including bogus; `executed` field populates after the W8
  dispatcher mirrors apply result onto the row.
- `lib/services/hub/hub_client.dart::listAttention` +
  `listAttentionCached` — new `includeEscalated: bool` named
  parameter forwarding to the wire's `include_escalated` query
  param. Shared `_attentionQuery(status, includeEscalated)` helper
  so the cached + uncached call sites stay consistent.
- `lib/providers/hub_provider.dart` — both Me-page list call sites
  (`_resolveCached` primary fetch + `_reloadAttention` post-decide
  refresh) pass `includeEscalated: true` unconditionally.
- `lib/screens/me/me_screen.dart::_filterForAttention` — added
  `case 'propose': return _Filter.approvals;` so generic propose
  rows land in the Requests bucket (where Phase 3 per-kind cards
  W15-W18 will render). The IA fit lands without a chip add or
  bucket rename — `propose` is a Request-by-kind; stalled is a
  *state*, decorated by the per-card variant.
- `lib/screens/me/widgets/propose_addressee.dart` (new) — three
  top-level utility functions: `isAddresseeOfPropose(attention,
  myTier)` (primary vs stalled selector); `isStalledPropose(
  attention)` (`true` when `escalation_state != 'none'`);
  `stalledPillLabel(attention)` (returns `'Stuck'` for the pill
  noun). Top-level functions (not class statics) so the
  forthcoming W15-W18 cards + W19 steward inbox import one
  predicate.
- `test/screens/me/propose_addressee_test.dart` — 11 unit cases
  covering both predicates' happy paths and the empty-tier /
  cross-tier / empty-viewer-tier / missing-state edge cases.

### Changed

- ADR-030 plan stamp bumped to v1.0.687-alpha. Plan §4.3 W19.5 + W19.6
  entries rewritten with shipped reality + verify anchors. **Plan
  literal reinterpreted on W19.6 hub side** — the plan's
  `WHERE assigned_tier = caller_tier` baseline doesn't exist on the
  current handler (the endpoint returns all rows), so shipping the
  literal widening would have changed visibility for every existing
  API caller. Shipped the forward-compat hook
  (parse-but-unused `include_escalated`) instead; see
  [[feedback_plan_narrative_loose_talk]] (mem) for the pattern.
- W19.6 mobile half (top-of-Me stalled-decisions digest card)
  split out to a follow-up wedge **W19.6-mobile** so this commit
  stays focused on the wire-contract foundation.

### Forensics

Hub test suite green (123.475s server pkg, full suite). Mobile
test suite verified on CI (cannot run flutter locally per repo
convention). pubspec 1.0.686-alpha → 1.0.687-alpha.

---

## v1.0.686-alpha — 2026-05-24

**ADR-030 Phase 2 COMPLETE.** W12 (re-propose rule in 9 bundled
steward templates), W13 (10 lifecycle scenarios S33-S42),
W14 (seed-demo annotation) shipped together. Hub binary changes
because steward prompts are embedded into the binary via
`//go:embed all:templates`; no Go-code-behavioural change. Phase 3
(mobile per-kind cards + override affordance) is the remaining
ADR-030 work — still not started but now unblocked end-to-end on
the hub side AND in the prompt corpus stewards spawn against.

### Added

- 9 steward templates gain `### Governed actions — use the `propose`
  verb (ADR-030)` subsection under their Authority (or Concierge mode
  for `steward.claude-m4.v1.md`) block. Canonical three-paragraph
  block: (1) governed-actions gate naming the 5 MVP kinds + the
  "do not mutate directly" prohibition; (2) `dry_run: true` self-
  correction usage; (3) re-propose discipline ("do not re-propose
  to higher tier; re-examine the rejection reason in the fan-back
  envelope"). Three domain tailorings: `steward.infra.v1.md` (route
  deploy/rollback through propose even with shell access);
  `steward.antigravity.v1.md` (route through propose regardless of
  agy's local file tools); `steward.research.v1.md` (call out
  phase 3 review-cycle outcomes + `plan.advance` as in-scope verbs).
- 10 lifecycle scenarios in `docs/how-to/test-steward-lifecycle.md`
  under a new H1 banner `# ADR-030 governed actions + `propose`
  verb (Scenarios 33-42)`:
  - **S33** `deliverable.set_state` propose → approve → audit row
    with `via=propose` discriminator.
  - **S34** propose → reject → fan-back to worker via ADR-032
    envelope; worker re-examines (does not auto re-propose).
  - **S35** `phase.advance` propose → approve → activity feed
    + variation for stale `from_phase` (optimistic concurrency).
  - **S36** `task.set_status.done` propose → approve + W7's
    narrowed-set rejection test (`to_status="in_progress"` errors).
  - **S37** worker tool permission_prompt → W10 re-addressing
    to parent steward → approve; variation when parent steward
    is dead (falls back to principal).
  - **S38** principal override of S33's resolved row → W9 Rollback
    runs → `attention.override` audit + second fan-back; variation
    for `agent.spawn` override (emits `agent.spawn.rollback_todo`
    instead of terminating, per plan §5).
  - **S39** alias compat: legacy `approval_request + spawnIn`
    routes through W8 dispatcher with `via=alias_legacy` lineage
    stamp (forensics can tell legacy vs new-shape callers apart).
  - **S40** `dry_run: true` returns preview synchronously
    without inserting an attention row; no `propose.raised` audit;
    variation for no-op preview (`from==to` returns
    `no_op: true`).
  - **S41** D-7 Option 2′ signal walk: stalled propose past
    `inactivity_deadline` → sweep emits W11.5
    `attention.escalation_advanced` audit → push to principal →
    override; verifies push fires once per transition (not per
    sweep tick) by the sweep being idempotent on `escalation_state`.
  - **S42** late-but-valid decision: original steward decides
    between sweep tick and principal opening Me. Verifies normal
    path runs (`via=propose`, NOT `via=override`); `assigned_tier`
    stays unchanged (the D-7 "decision stays, signal walks"
    contract); no override audit fires.
- 13-line NOTE block in `hub/internal/server/seed_demo_lifecycle.go`
  immediately before the `INSERT INTO attention_items` call,
  explaining why seeded lifecycle attentions are session_id-less
  ON PURPOSE (so the demo doesn't accidentally flip a real
  deliverable / phase / task as the principal explores the queue).
  Per the principal's instruction, the seed itself is unchanged.

### Changed

- `docs/plans/governed-actions-mvp-rollout.md` — status block bumped
  to "Phase 1 + Phase 2 COMPLETE". W12/W13/W14 rewritten with
  shipped reality + per-template tailoring notes + verify anchors
  for the canonical block, the S33-S42 banner, the seed file, and
  the W11 missing-session guard.
- `docs/decisions/030-governed-actions-and-propose-verb.md` —
  stamp bumped to v1.0.686-alpha.
- `docs/doc-spec.md` — stamp bumped to v1.0.686 (transitively,
  because contract-stamped docs that reference it must stay in
  sync with their referent under the doc-freshness mechanism).

### Forensics

Single Phase-2 commit (no per-wedge bisect needed because the
work is prose-only with no test-bisect signal). Phase 1 conventions
honoured: pubspec bumped to 1.0.686-alpha+10686 (the hub binary
bytes shift due to embedded templates), full hub test suite green
unchanged, no new test files (W13 is a how-to doc, not an
executable suite).

---

## v1.0.685-alpha — 2026-05-24

**ADR-030 Phase 1 COMPLETE.** All 11 hub-side wedges (W1-W11.5)
shipped end-to-end with full test coverage across v1.0.674-685.
Phase 2 (steward prompts + lifecycle scenarios) and Phase 3 (mobile
per-kind cards + override affordance) are not yet started but
unblocked.

W11.5 is the final wedge — the loop-closure signal that lets the
sweep escalate stalled propose rows the same way it already
escalates tasks. Adds a propose-specific
`attention.escalation_advanced` audit row alongside the existing
generic `loop.stall_escalated`, so the Activity feed renderer can
show "stalled propose: <change_kind>" with enough context to
navigate without a follow-up fetch.

### Added

- `hub/internal/server/loop_sweep.go::emitProposeEscalationAudit`
  — new helper called from `escalateStall` when the escalating
  entity is an `attention_items` row with a non-empty
  `change_kind`. Meta carries the W11.5-spec shape:
  `{attention_id, change_kind, from_state, to_state,
  original_assigned_tier, project_id, change_spec_preview}`.
  `change_spec_preview` = first 200 bytes of change_spec_json
  with `…` suffix when truncated (single-pass at emit time,
  no per-render JSON re-parsing).
- `truncateChangeSpecPreview` helper + 4-case table test.
- `hub/internal/server/loop_sweep_propose_audit_test.go` — 5
  cases: one tick emits exactly one audit (alongside the legacy
  loop.stall_escalated); audit meta carries spec-shape; two
  ticks at the same state emit ONE transition (dedup
  regression); legacy non-propose attention rows do NOT emit
  the propose audit; truncate helper.

### Changed

- `hub/internal/server/loop_entity.go::questionAttentionKinds`
  — added `"propose"` to the loop-bearing attention-kind set.
  Without this entry the sweep would never pick up propose
  rows as loop-entities and W11.5's audit could never fire.
  Comment block documents the rationale (propose is loop-bearing
  by construction).
- `pubspec.yaml` 1.0.684 → 1.0.685-alpha.
- `docs/plans/governed-actions-mvp-rollout.md` — status flipped
  from "Proposed" to "Phase 1 COMPLETE". W11.5 entry rewritten
  with the helper shape + questionAttentionKinds extension +
  5-test coverage. Two new verify symbol anchors
  (`emitProposeEscalationAudit`, `truncateChangeSpecPreview`).
- `docs/decisions/030-governed-actions-and-propose-verb.md` +
  `docs/doc-spec.md` — stamps bumped to v1.0.685 per their
  `Freshness: contract` declaration. 26 anchors total now,
  all green.

### Phase 1 retrospective

11 wedges, 12 alpha versions (v1.0.674-685, one wedge per
release except W11.5 which lands the same day). Final code
totals approximate (excluding tests):

| Wedge | Subject | LOC |
|---|---|---|
| W1 | migration 0045 + columns | ~80 |
| W2 | Policy.Kinds + KindFor | ~150 |
| W3 | propose-kind registry + lint script | ~370 |
| W4 | propose MCP verb + scope check | ~390 |
| W5 | apply deliverable.set_state + rollback | ~270 |
| W6 | apply phase.advance + rollback | ~275 |
| W7 | apply task.set_status + rollback | ~280 |
| W8 | alias apply (agent.spawn + template.install) + decide-handler dispatcher refactor | ~360 |
| W9 | principal override + 6-guard handler | ~280 |
| W10 | permission_prompt re-addressing | ~70 |
| W11 | dispatchAttentionReply allowlist + ADR-032 envelope | ~140 |
| W11.5 | loop-closure escalation audit | ~110 |
| **Total** | | **~2,775** |

Test count: ~85 new cases across 12 new test files. Full hub
test suite runtime ~123s, unchanged green throughout.

Architectural surfaces shipped:
- ADR-030 propose verb + per-kind apply/rollback registry
- ADR-032 envelope composition on attention fan-back
- ADR-033 native_tools.go unified registry consumed by W4
- ADR-034 loop-closure signal extended to propose rows

### Notes

- **propose entered the loop-entity set.** The
  `questionAttentionKinds` extension is the load-bearing change
  beyond W11.5's stated wording — propose rows now participate
  in the loop-closure runtime (stall detection, escalation,
  timeout). Justified in the comment + plan W11.5 rewrite.
- **Phase 2/3 unblocked.** Phase 2 ships steward-template prompts
  teaching `propose` adoption + adds test-steward-lifecycle
  scenarios. Phase 3 ships mobile per-kind propose cards
  (deliverable / phase / task / worker_tool) + override
  affordance + Me-page stalled-decisions digest.

---

## v1.0.684-alpha — 2026-05-24

ADR-030 Phase 1 W11 — propose joins the fan-back allowlist and the
ADR-032 envelope rides nested under the payload. With this wedge,
the propose loop closes end-to-end on the agent side: a worker
calls propose, ends its turn, and the next user turn it sees
carries `{decision, change_kind, executed, envelope:{cause, ...}}`
so the agent knows what landed without a second hub round-trip.
Override decisions fan back too (W9 inter-op) so requesters learn
when the state reverted.

### Added

- `attentionReplyExtras { ChangeKind, Executed }` — new param on
  `dispatchAttentionReply` for propose-specific fields. Backward-
  compat: existing call sites pass `{}` and see no payload change.
- `propose` added to the fan-back allowlist in `handleDecideAttention`
  (the same list approval_request / select / help_request /
  permission_prompt / elicit / project_steward_request are on).
- ADR-032 envelope composition under `payload.envelope`. Nested
  rather than flattened because the payload's top-level `kind`
  already holds the attention kind and would collide with
  envelope.kind.
- `dispatchAttentionReply` SELECT widened to fetch `actor_handle`
  + `cause` for the envelope.
- Override path calls `dispatchAttentionReply` after the rollback,
  so requesters see the second fan-back with the rollback
  executed payload.
- `attentionReplyEnvelopeText` + `stringOrDefault` helpers.
- `hub/internal/server/handlers_attention_fanback_test.go` — 6
  cases: approve fan-back shape (envelope + executed + thread/
  from/to); reject omits executed; dry_run produces zero events;
  cause round-trips from row to envelope; override fan-back
  carries rollback payload; legacy approval_request still fans
  back unchanged.

### Changed

- `pubspec.yaml` 1.0.683 → 1.0.684-alpha.
- `docs/decisions/030-governed-actions-and-propose-verb.md` +
  `docs/plans/governed-actions-mvp-rollout.md` — stamps bumped to
  v1.0.684. Plan W11 rewritten to record the nested-envelope
  decision, the `KindReport` choice for envelope.kind, the
  override-also-fan-backs inter-op, and 6-test coverage.
  New verify symbol anchor on `dispatchAttentionReply` → 24
  anchors total now, all green.

### Notes

- **envelope.kind = `KindReport`, not `"attention_reply"`.** The
  plan W11 narrative referenced `"attention_reply"`, but that's
  the `agent_events.kind` value (the event-level kind), not an
  ADR-032 envelope kind. ADR-032 D-2 defines a closed
  four-value enum {directive, question, report, notification};
  a propose-decision closes the loop the propose opened, so
  `report` is the structurally correct mapping. The
  `agent_event` row itself still uses
  `kind="input.attention_reply"`.
- **Envelope is nested.** input_envelope.go's convention is to
  flatten envelope fields at the payload top level, but
  attention-reply's existing top-level `kind` field holds the
  attention kind ("propose", "approval_request", …). Flattening
  would clobber it. Nested envelope under `payload["envelope"]`
  preserves both.
- **All 5 propose kinds + 2 alias kinds now close the loop
  end-to-end.** A worker calls propose → row inserts with
  session_id → /decide approve runs the apply → fan-back fires
  with executed → requester's next turn carries the result.
  Override decisions get the same treatment via W9's
  `handleAttentionOverride` calling dispatchAttentionReply with
  the rollback payload.

---

## v1.0.683-alpha — 2026-05-24

ADR-030 Phase 1 W10 — re-address `permission_prompt` rows raised by
steward-parented workers. When a worker has a same-project steward
parent, the row lands `assigned_tier='project-steward'` +
`current_assignees_json=[<parent_id>]` so the parent steward sees
it in their inbox first. Otherwise the row stays team-wide-addressed
(existing behaviour preserved for orphan workers, non-steward
parents, and binding-drift cases).

### Added

- `hub/internal/server/mcp_more.go::permissionPromptAddressee` —
  new helper. Single SQL JOIN with the strict three-clause
  predicate per ADR-030 pre-W1 decision #3:
  ```
  worker.parent_agent_id IS NOT NULL
  AND parent.kind LIKE 'steward.%'
  AND parent.project_id IS NOT NULL AND = worker.project_id
  ```
  Two `IS NOT NULL` guards defend against SQL's
  `NULL = NULL → NULL` semantics. Best-effort: DB errors log
  a warn + return "" so transient issues degrade to safe
  (team-wide addressing).
- `mcpPermissionPrompt` calls the helper before the row INSERT
  and stamps `assigned_tier` + `current_assignees_json`
  accordingly. The INSERT statement was widened to include
  `assigned_tier` (the ADR-030 W1 column previously written
  only by the propose path).
- `hub/internal/server/mcp_permission_prompt_addressing_test.go`
  — 6 cases: same-project steward parent addresses row;
  cross-project steward parent (binding drift) stays
  team-wide; non-steward parent stays team-wide; orphan
  worker stays team-wide; NULL project_ids on both sides
  stay team-wide (NULL=NULL guard); direct helper test
  against ghost worker returns "".

### Changed

- `pubspec.yaml` 1.0.682 → 1.0.683-alpha.
- `docs/decisions/030-governed-actions-and-propose-verb.md` +
  `docs/plans/governed-actions-mvp-rollout.md` — stamps bumped to
  v1.0.683. Plan W10 rewritten to document the helper shape, the
  5-conjunct SQL JOIN, and the 6-test coverage breakdown. New
  verify symbol anchor on `permissionPromptAddressee` → 23
  anchors total now, all green.

### Notes

- **`dispatchAttentionReply` unchanged.** Fan-back still
  addresses by `session_id`; the new addressing only affects
  which inbox surfaces the row first. Existing
  `TestDecide_PermissionPromptFansOutAttentionReply` passes
  unchanged.
- **Binding-drift guard is the load-bearing clause.** Without
  the `p.project_id = w.project_id` check, a v1.0.605-class
  bug where the parent-id pointer survives but the project
  binding has drifted would route the row to a steward that
  no longer owns the worker. Test 2
  (`CrossProjectStewardParent_StaysTeamWide`) is the
  regression for that scenario.
- **Principal-override of permission_prompt deferred.** W9
  ships override against propose-kind rows;
  permission_prompt isn't a propose kind. Widening
  `handleAttentionOverride` to handle it would require
  per-engine-driver coordination (codex parked-RPC, etc.).
  Tracked as a follow-up.

---

## v1.0.682-alpha — 2026-05-24

ADR-030 Phase 1 W9 — principal override. The most complex wedge in
Phase 1: per-kind Rollback on the registry + a new override branch
on the decide handler. Principal can now overturn a steward-approved
governed action, with per-kind reverse semantics ranging from "swap
from/to and re-apply" (state kinds) to "delete the installed file"
(template.install) to a manual-cleanup TODO audit (agent.spawn,
since terminate is post-MVP).

### Added

- `ProposeKind.Rollback` field on the registry shape. Optional;
  kinds without one refuse override (422).
- Per-kind rollbacks for all 5 W5-W8 kinds:
  - `deliverable.set_state` → re-calls Apply with from_state as
    the new state. All transition-direction logic (stamp
    clearing on ratified→draft, etc.) runs unchanged.
  - `phase.advance` → swaps from_phase ↔ to_phase, re-calls
    Apply. Optimistic-concurrency check still fires.
  - `task.set_status` → bypasses the propose-permitted check
    (rollback target may be in_progress / blocked / todo);
    writes UPDATE directly + clears completed_at for
    non-terminal restorations.
  - `template.install` → deletes the installed file + emits
    `template.uninstall` audit. Does NOT restore a prior
    version (apply path doesn't capture one).
  - `agent.spawn` → emits `agent.spawn.rollback_todo` audit
    pointing the principal at the agent_id with manual-cleanup
    hint. Per plan: terminate-via-propose is post-MVP.
- `hub/internal/server/handlers_attention.go::handleAttentionOverride`
  — new branch reached when `status != "open"` AND
  `in.Override == true`. 6 guard checks before dispatching to
  Rollback through the registry; emits `attention.override`
  audit row with the rollback's executed payload inline in
  meta so consumers don't need to join.
- `attentionDecideIn.Override` field on the wire.
- `decision` validation extended: now accepts `"override"`
  alongside `"approve"` / `"reject"`, only when paired with
  `override: true`.
- `hub/internal/server/handlers_attention_override_test.go` —
  8 cases: task.set_status revert with audit chain;
  template.install file deletion; override_allowed=false → 400;
  no override flag preserves 409; non-principal caller → 403;
  double-override → 409; kind without Rollback → 422;
  still-open row + override-decision is rejected by the
  validation gate (open-row override doesn't fire).

### Changed

- `pubspec.yaml` 1.0.681 → 1.0.682-alpha.
- `docs/decisions/030-governed-actions-and-propose-verb.md` +
  `docs/plans/governed-actions-mvp-rollout.md` — stamps bumped to
  v1.0.682. Plan W9 rewritten to record the 6 guard order, the
  decision-validation gate extension, and per-kind rollback
  semantics shipped. Two new verify symbol anchors
  (`Rollback`, `handleAttentionOverride`) → 22 anchors total,
  all green.

### Notes

- **Principal-tier check is MVP-soft.** Today `in.By ==
  "@principal"` is the gate. A token-identity-based tier check
  is a follow-up wedge; the mobile app already sends
  by="@principal" for the principal session, so this is
  honest for the current threat model.
- **Original-apply prerequisite.** Override requires
  `executed_json` to be non-empty — if the prior decision was
  reject (no apply ran), there's nothing to roll back.
- **Audit emphasis trade-off.** The override audit's meta
  inlines the rollback's executed payload (the W1 schema
  designed `executed_json` for this). Consumer queries can
  read the override row standalone without joining to the
  attention row.
- **Registry still at 5 kinds.** No new kind registrations
  in W9 — the Rollback field is per-kind metadata on the
  existing entries.

---

## v1.0.681-alpha — 2026-05-24

ADR-030 Phase 1 W8 — alias apply functions + decide-handler dispatcher
refactor. The pre-ADR-030 `approval_request + spawnIn` and
`template_proposal` paths now route through the same propose-kind
registry the new W4 verb uses; the audit-meta `via` tag distinguishes
the two dispatch shapes. With this wedge, every load-bearing
state change converges on `LookupProposeKind(...).Apply(...)`.

### Added

- `hub/internal/server/apply_agent_spawn.go` — wraps `DoSpawn`.
  change_spec IS the spawnIn JSON shape (same shape the legacy
  pending_payload carried) so the two dispatch paths share the
  same unmarshal. target_ref is cosmetic for this kind.
- `hub/internal/server/apply_template_install.go` — wraps
  `installProposedTemplate`. DryRun stat's the blob so the
  preview can show body size + presence; missing-blob is a soft
  signal (not an error).
- `ProposeApplyContext.Via` field + `ViaOrDefault()` — default
  "" → "propose"; the legacy-alias dispatch sets "alias_legacy"
  so audit-meta consumers can distinguish dispatch shapes without
  parsing the kind name.
- `hub/internal/server/handlers_propose_dispatch_test.go` — 5
  end-to-end cases: propose+task.set_status, propose+agent.spawn,
  legacy approval_request+spawnIn (via=alias_legacy + propose_id),
  legacy template_proposal (via=alias_legacy), reject-skips-apply
  regression.
- `apply_agent_spawn_test.go` (6) + `apply_template_install_test.go`
  (7) — unit-level Validate/DryRun/Apply coverage including
  Apply-via-alias-legacy via-tag assertions.

### Changed

- `hub/internal/server/handlers_attention.go::handleDecideAttention`
  — the SELECT now reads 4 ADR-030 W1 columns alongside the legacy
  set. The two prior `if` blocks (one for approval_request +
  spawnIn, one for template_proposal) collapse into a single
  dispatcher arm with three input shapes that all converge on the
  registry. Best-effort UPDATE populates `executed_json` from the
  apply return value so post-mortem queries on the attention row
  show what landed.
- `hub/internal/server/apply_deliverable_set_state.go` +
  `apply_phase_advance.go` + `apply_task_set_status.go` — use
  `ac.ViaOrDefault()` instead of the hard-coded "propose" literal.
- `pubspec.yaml` 1.0.680 → 1.0.681-alpha.
- `docs/decisions/030-governed-actions-and-propose-verb.md` +
  `docs/plans/governed-actions-mvp-rollout.md` — stamps bumped to
  v1.0.681. Plan W8 rewritten to document the dispatcher refactor +
  via-tag discriminator + 16-test coverage breakdown. Two new
  verify symbol anchors (`applyAgentSpawn`, `applyTemplateInstall`)
  → 20 anchors total, all green.

### Notes

- **No backward-compat regression.** Existing legacy-path tests
  pass unchanged through the refactor; the audit-meta `via`
  discriminator is purely additive. Old MCP callers see the same
  `approval_request` / `template_proposal` wire shape; consumers
  that already read `meta.via` continue to see "propose" on
  ADR-030 calls and now also "alias_legacy" on pre-ADR-030
  calls.
- **Registry now reports 5 kinds**: deliverable.set_state,
  phase.advance, task.set_status, agent.spawn, template.install.
  The lint's bidirectional R⇄P check would now meaningfully gate
  drift the moment an operator commits a policy.yaml fixture.
- **`executed_json` column populated for the first time.** The
  W1 schema added the column; W4 set it to NULL on insert; W8
  is the first writer. Consumers reading attention rows post-
  decide now see the apply result inline without re-querying
  audit_events.

---

## v1.0.680-alpha — 2026-05-24

ADR-030 Phase 1 W7 — third per-kind apply function: `task.set_status`.
Workers and stewards can now propose closing a task (done or
cancelled). The MVP triangle of governed-action kinds —
deliverable.set_state, phase.advance, task.set_status — is complete
on the apply side; W8 wires the alias dispatchers (agent.spawn,
template.install) and the decide-time auto-dispatch.

### Added

- `hub/internal/server/apply_task_set_status.go` — registers
  `task.set_status` via `init()`. Mirrors W5/W6 shape (Validate /
  DryRun / Apply).
- `hub/internal/server/apply_task_set_status_test.go` — 8 cases
  (Validate has 9 sub-cases).

### Changed

- `pubspec.yaml` 1.0.679 → 1.0.680-alpha.
- `docs/decisions/030-governed-actions-and-propose-verb.md` +
  `docs/plans/governed-actions-mvp-rollout.md` — stamps bumped to
  v1.0.680. Plan W7 rewritten to record the narrowed status set
  (done|cancelled only) + Apply behaviour + assigner-notification
  carry-over from the legacy path. New verify symbol anchor on
  `applyTaskSetStatus`. 18 anchors total now, all green.

### Notes

- **Narrowed status set.** Per ADR-029 D-3, propose only permits
  `done` and `cancelled`. `in_progress` and `blocked` are
  auto-derived by `deriveTaskStatusFromAgent` (spawn-lifecycle
  watchers); proposing them would race the auto-derive and
  confuse the audit timeline. The `todo` initial state is set at
  task-create time and never re-entered through propose. Validate
  rejection message names the forbidden status + the allowed set
  + the ADR-029 D-3 reason so the agent re-proposes correctly.
- **notifyTaskAssigner carries over.** The legacy PATCH path fires
  `notifyTaskAssigner` on terminal-status flip so the steward who
  delegated hears about it inline. Apply does the same — same
  best-effort semantics (silently degrades on missing assigner,
  sleeping session, DB error).
- **Registry now holds all 3 MVP kinds.** Lint reports
  `registry=3` after this commit. With a policy.yaml fixture, the
  bidirectional R⇄P check would now light up against missing
  declarations.

---

## v1.0.679-alpha — 2026-05-24

ADR-030 Phase 1 W6 — second per-kind apply function: `phase.advance`.
Workers and stewards can now propose a project-phase transition with
optimistic-concurrency safety (caller stakes `from_phase`; Apply
rejects on mismatch).

### Added

- `hub/internal/server/apply_phase_advance.go` — registers
  `phase.advance` via `init()`. Mirrors the W5 shape (Validate /
  DryRun / Apply); rejects on stale `from_phase` so racing
  approvals don't silently apply against the wrong starting state.
  No-op (from == to) short-circuits without row touch or audit
  emission, same as W5.
- `loadProjectPhaseRowAnyTeam` helper — cross-team variant of
  `loadProjectPhaseRow` used by DryRun (which doesn't have a team
  to scope on). Apply itself ALWAYS uses the scoped loader since
  `ac.Team` is the authoritative scope; cross-team lookups would
  fail closed there.
- `hub/internal/server/apply_phase_advance_test.go` — 9 cases:
  registered-at-init; Validate (5 sub-cases); DryRun preview;
  DryRun flags from_phase drift; Apply happy-path (history append
  + audit lineage); stale-from_phase rejection + unchanged-state
  invariant; no-op short-circuit; empty from_phase skips the
  optimistic check; wrong-team not-found.

### Changed

- `pubspec.yaml` 1.0.678 → 1.0.679-alpha.
- `docs/decisions/030-governed-actions-and-propose-verb.md` +
  `docs/plans/governed-actions-mvp-rollout.md` — stamps bumped to
  v1.0.679. Plan W6 rewritten to record the two ship-time design
  calls (audit action stays `project.phase_advanced` not
  `phase_set`; criteria gating not enforced at Apply since the
  approver IS the gate). New verify symbol anchor on
  `applyPhaseAdvance`.
- `docs/doc-spec.md` — stamp bumped v1.0.673 → v1.0.679. The
  freshness mechanism flagged it as `stale-contract` on this PR;
  no content drift, just acknowledging the contract.
- 17 anchors total now, all green.

### Notes

- Audit-action choice: `project.phase_advanced`, not `phase_set`.
  The legacy `phase_set` action is for NULL → first-phase
  hydration; propose never reaches that path (initial hydration
  happens at project-create).
- Acceptance-criteria gating is intentionally NOT enforced at
  Apply time. The legacy REST endpoint 409s on pending criteria;
  here, the principal IS the gate — if they approve, they're
  explicitly overriding criteria. The propose `reason` field
  should make that clear.

---

## v1.0.678-alpha — 2026-05-24

ADR-030 Phase 1 W5 — the first per-kind apply function:
`deliverable.set_state`. Workers and stewards can now propose every
direction of the ratification triangle (draft / in-review / ratified)
and the apply function is invocable through the registry; W8 wires
the decide-time auto-dispatch.

### Added

- `hub/internal/server/apply_deliverable_set_state.go` — registers
  `deliverable.set_state` via `init()` with Validate / DryRun /
  Apply. Three transition branches mirror the legacy REST
  endpoints (handleRatifyDeliverable / handleUnratifyDeliverable /
  handlePatchDeliverable); no-op is short-circuited with
  `executed.no_op = true`.
- `hub/internal/server/apply_deliverable_set_state_test.go` —
  8 cases: init-registered; Validate happy + 4 reject paths;
  DryRun preview shape + read-only; Apply in-review→ratified
  (stamps + audit meta lineage); Apply ratified→draft (clears
  stamps); Apply draft→in-review (no stamps); Apply no-op (no row
  change + no audit); end-to-end propose-then-manual-Apply with
  request_id round-trip into the audit meta.

### Changed

- `hub/internal/server/propose_kinds.go` — `ProposeKind.Apply`
  signature extended to receive `ProposeApplyContext { AttentionID,
  Team, AssignedTier, DeciderHandle }`. Lets per-kind audit rows
  carry the propose lineage (`via="propose"`, `by_tier`, `propose_id`)
  without re-querying. Validate + DryRun signatures unchanged. Cost
  zero — no consumers yet outside W5.
- `hub/internal/server/propose_kinds.go` — added
  `snapshotProposeKindsForTest` / `restoreProposeKindsForTest` so
  tests can mutate the registry without dropping init()-time
  registrations for sibling tests.
- `hub/internal/server/handlers_propose_test.go` +
  `hub/internal/server/lint_governed_actions_test.go` — switched
  from `resetProposeKindsForTest` to the snapshot/restore pattern.
- `scripts/lint-governed-actions.sh` — static-grep now strips Go
  comments before scanning, so doc-comment example registrations
  (e.g. `// RegisterProposeKind(ProposeKind{...})`) don't pollute
  the registry count. Caught the moment the first real init()
  registration landed (lint reported `registry=2` when only 1
  call existed).
- `pubspec.yaml` 1.0.677 → 1.0.678-alpha.
- `docs/decisions/030-governed-actions-and-propose-verb.md` +
  `docs/plans/governed-actions-mvp-rollout.md` — stamps bumped to
  v1.0.678 per `Freshness: contract`. Plan W5 rewritten to record
  the audit-action reconsideration (we keep the legacy three
  per-direction actions + stamp lineage on meta, rather than
  introducing a unified `deliverable.state_changed`), the Apply
  signature change, and the lint hardening. New verify symbol
  anchor on `applyDeliverableSetState`. 16 anchors total.

### Notes

- Audit-action choice: kept the legacy three actions
  (`deliverable.{ratified, unratified, updated}`) and stamped
  `via="propose"` on the meta instead of introducing a new
  `deliverable.state_changed` action. Activity-feed renderers
  stay unchanged; the meta is the discriminator.

---

## v1.0.677-alpha — 2026-05-24

ADR-030 Phase 1 W4 — the generic `propose` MCP verb. Workers and
stewards can now raise a governed-action attention row carrying
`change_kind`, `target_ref`, `change_spec`, `assigned_tier`, and a
preview-capable `dry_run` branch. The decide path still resolves
rows through the existing approve/reject handler; W5-W7 register
the per-kind apply functions, W8 wires the decide-time dispatch.

### Added

- `hub/internal/server/handlers_propose.go` — `mcpPropose` plus
  `checkProposeScope`, `resolveAssigneesForTier`,
  `extractTargetProjectID`, `isStewardKind`, `isValidGovTier`,
  `validGovTiers`. Pipeline: kind-registry lookup → per-kind
  Validate hook → cross-project scope check (pre-W1 decision #4) →
  addressee_tier resolution (caller hint > policy default >
  permissive principal) → current_assignees_json via
  `findRunningProjectSteward` for project-steward tier (symbolic
  role-handle fall-through when no live agent matches) → dry_run
  branch returns preview without insert → row INSERT carrying every
  ADR-030 column + legacy `pending_payload_json` mirror → audit
  row `propose.raised`.
- `propose` entry in `buildNativeTools()` — full JSON schema (kind /
  target_ref / change_spec required; reason / addressee_tier /
  dry_run optional). `WorkerEligible: true`, `Tier: TierSignificant`.
- `propose` entry in `tiers.go::toolTiers` as `TierSignificant`,
  matching `templates_propose`.
- `propose` entry in `nativeToolMeta` overlay — ADR-031 D-1 SeeAlso
  list `[request_approval, tasks_update, templates_propose]`.
- `hub/internal/server/handlers_propose_test.go` — 11 cases:
  happy-path row shape; unknown kind echoes registered set;
  dry_run returns preview without insert; worker cross-project
  out_of_scope; steward cross-project allowed; no-project-id in
  target_ref skips scope check; addressee_tier hint overrides
  policy default; policy default chosen when no caller hint;
  invalid tier rejected; Validate hook rejects malformed spec;
  live project steward's handle appears in assignees when tier is
  project-steward.

### Changed

- `pubspec.yaml` 1.0.676 → 1.0.677-alpha.
- `docs/decisions/030-governed-actions-and-propose-verb.md` +
  `docs/plans/governed-actions-mvp-rollout.md` — stamps bumped to
  v1.0.677 per `Freshness: contract`. Plan W4 rewritten to record
  shipped behaviour (handler step-order, scope-check semantics,
  assignee fall-through) + new `verify symbol` anchor on
  `mcpPropose`. 15 anchors total now, all green.

### Notes

- No `propose` kinds are registered yet — `propose` calls fail with
  `unknown kind` until W5/W6/W7 land. The `propose` tool surface is
  exposed in `tools/list` so operators can preview the schema.
- `nativeToolMeta` overlay invariant
  (`TestNativeToolMeta_EveryToolCovered`) caught the missing meta
  row on first full-suite run; added before the commit.

---

## v1.0.676-alpha — 2026-05-24

ADR-030 Phase 1 W3 — add the propose-kind registry skeleton and the
governed-actions linter that keeps it in lockstep with the policy
file. Still no runtime consumer (W4 ships the `propose` MCP verb);
the registry is callable but empty.

### Added

- `hub/internal/server/propose_kinds.go` — `ProposeKind` type
  (Kind + Validate + DryRun + Apply), `proposeKinds` global map
  under `sync.RWMutex`, `RegisterProposeKind` (panics on empty
  Kind), `LookupProposeKind`, `ListProposeKinds` (sorted),
  `resetProposeKindsForTest`. The `RegisterProposeKind` doc-comment
  fixes the static-grep contract the linter relies on: only literal
  `Kind: "<name>"` registrations are discoverable.
- `scripts/lint-governed-actions.sh` — three checks: (1) kind-shape
  enforces snake_case-with-dots; (2) bidirectional consistency
  between registered kinds (static-grepped from
  `hub/internal/server/*.go` for `RegisterProposeKind(ProposeKind{`
  blocks) and the policy.yaml `kinds:` block (auto-discovered by
  repo glob, overridable via `--policy <path>`); (3)
  escalate-on-timeout sanity per the ADR-030 D-7 Option 2′
  amendment. FAIL on shape or registry⇄policy mismatch; WARN on
  no-policy-found when registry non-empty (silenceable via
  `--no-warn-empty`); WARN on the escalate-on-timeout-with-principal
  case (signal has nowhere to walk).
- `hub/internal/server/lint_governed_actions_test.go` — 8 script
  cases (empty / registered-no-policy / policy-no-handler /
  bidirectional-match / bad-kind-shape / escalate-on-timeout WARN /
  empty-registry-with-non-empty-policy / no-policy WARN) plus 2
  registry cases (List sorted + LookupProposeKind, panic on
  empty-Kind). Tests shell out to the bash script so the bash
  contract stays authoritative.

### Changed

- `.github/workflows/ci.yml` — new "Lint governed actions" step
  after "Lint doc anchors".
- `pubspec.yaml` 1.0.675 → 1.0.676-alpha.
- `docs/decisions/030-governed-actions-and-propose-verb.md` and
  `docs/plans/governed-actions-mvp-rollout.md` — stamps bumped to
  v1.0.676 per `Freshness: contract`. Plan W3 rewritten to record
  what shipped (registry split-out, test counts, CI wiring); gains
  `verify symbol` anchor on `ListProposeKinds` and `verify file`
  anchor on the lint script.

---

## v1.0.675-alpha — 2026-05-24

ADR-030 Phase 1 W2 — extend `Policy` with the `kinds:` block.
Registry-side data only: the propose handler (W4) is the runtime
consumer; nothing else calls `KindFor` yet, so there is no behaviour
change in v1.0.675 for any existing path. The `tiers / approvers /
quorum / escalation` keys remain valid in the same file unchanged.

### Added

- `hub/internal/server/policy.go` — `Policy.Kinds map[string]KindPolicy`
  field; `KindPolicy` + `QuorumPolicy` types; `KindFor(kind) (KindPolicy, bool)`
  accessor with a permissive default (route to principal, M=1, override
  allowed, escalation off) and a WARN log when the kind isn't
  configured.
- `GovTierWorker / GovTierProjectSteward / GovTierGeneralSteward /
  GovTierPrincipal` constants — the ADR-030 governance ladder, mirrored
  in migration 0045's CHECK constraint on `attention_items.assigned_tier`.
- `parsePolicy([]byte) (*Policy, error)` — typed-error parse path
  surfaced as a free function so future `hub init --check` (post-MVP)
  can refuse to start on a structurally bad file. The runtime
  `policyStore.reload` still keeps last-known-good on malformed YAML.
- `newPolicyStoreWithLogger(dataRoot, *slog.Logger)` — wired in
  `server.go` so the `KindFor` WARN lands on the daemon's structured
  log (the parameterless `newPolicyStore` still exists as a
  default-logger shim for any future direct caller).
- `hub/internal/server/policy_kinds_test.go` — 7 tests pinning the
  contract: missing block / unknown kind / configured kind / parse
  error / last-known-good degradation / legacy paths untouched /
  end-to-end through `Server.policy`.

### Changed

- `pubspec.yaml` 1.0.674 → 1.0.675-alpha.
- `docs/decisions/030-governed-actions-and-propose-verb.md` and
  `docs/plans/governed-actions-mvp-rollout.md` — stamps bumped to
  v1.0.675 per their `Freshness: contract` declaration. Plan W2
  gains a `verify symbol` anchor on `KindFor` so any future rename
  fails CI before the doc drifts.

---

## v1.0.674-alpha — 2026-05-24

ADR-030 Phase 1 W1 — schema migration that opens the door for the
generic `propose` MCP verb. Pure-additive: five nullable columns and
two partial indexes on `attention_items`. No behaviour change yet;
W4 (the `propose` handler) and W5/W6/W7 (the three apply functions)
populate the new columns in subsequent wedges.

### Added

- `hub/migrations/0045_attention_items_governed_actions.up.sql` —
  adds `change_kind`, `assigned_tier` (CHECK-constrained to the four
  governance tiers), `change_spec_json`, `target_ref_json`, and
  `executed_json` to `attention_items`. Two partial indexes on
  `change_kind` and `(assigned_tier, status)` mirror the
  `idx_artifacts_run` / `idx_artifacts_sha` precedent (no entries
  added until a propose is exercised).
- `hub/internal/server/attention_governed_actions_migration_test.go` —
  PRAGMA-based regression on column presence + index shape. Mirrors
  the `agents_archive_respawn_test.go` pattern; catches column drift
  on any future table rebuild.

### Changed

- `docs/decisions/030-governed-actions-and-propose-verb.md` and
  `docs/plans/governed-actions-mvp-rollout.md` — stamp bumped to
  v1.0.674 per their `Freshness: contract` declaration.
- `docs/plans/governed-actions-mvp-rollout.md` W1 — verify anchor
  for the 0045 slot flipped from `no-file` to `file` now that the
  migration exists.
- `docs/discussions/doc-freshness-maintenance.md` — illustrative
  `no-file` example re-pointed at the now-unused 0099 slot (the
  0045 slot it previously cited was filled by this wedge).

### Disjoint from migration 0042 (ADR-034 loop-entity columns)

No literal collision. Two conceptual overlaps the propose handler
must respect when it lands in W4: (1) `cause` (lineage) vs
`target_ref_json` (mutation target) — for `task.set_status` the two
often hold the same `task_id`; both must be populated. (2)
`assigned_tier` is immutable across ticks (ADR-030 D-3);
`escalation_state` (ADR-034 D-4) is the walker.

---

## v1.0.673-alpha — 2026-05-24

ADR-027 W11 fix-up wedge #16 — first on-device test of v1.0.672's M4
claude-code resume surfaced two cosmetic-but-significant defects.

User report: "i just tested the resume, session resumed, the mobile
seems rendered a duplicated agent's last session's text plus there is
'No response requested' response from agent when i enter a new input
in the resumed session."

**Root cause — defect 1 (duplicated transcript).** Inspecting the
resumed JSONL on the dev box
(`~/.claude/projects/-home-ubuntu-hub-work-m4-test/9e3d2110-….jsonl`)
revealed that **claude-code APPENDS to the original `<uuid>.jsonl` on
resume rather than minting a new file**. The pre-existing lines (the
prior session's full transcript) and the resumed agent's new lines
share one file. The M4 adapter's default `TailMode =
StartFromBeginning` then re-emits ALL existing bytes under the new
agent_id at attach. Both the prior agent and the resumed agent are
stamped with the same session_id, so mobile's session-view (which
merges by session_id) shows every assistant text and thought twice.

(Side note: this also corrects a caveat shipped in v1.0.672's changelog
entry — the captured `engineSessionID` is NOT overwritten on the next
resume because the JSONL filename UUID is stable across resumes.
Resume-of-resume is safe by construction.)

**Root cause — defect 2 ("No response requested." stub reply).** When
claude-code starts under `--resume`, it auto-injects an
`isMeta: true` user message with body "Continue from where you left
off." and the model replies with the literal "No response requested."
(claude's `CVH` constant, verified by string sweep of v2.1.144).
Mobile rendered this as the agent's reply to the user's first
post-resume input.

**Fixed.**

- **launch_m4_locallogtail.go**: new `cmdContainsResumeFlag(cmd)`
  helper detects `--resume <id>` / `--resume=<id>` in the rendered
  spawn cmd (the shape `spliceClaudeResume` produces). When true, the
  M4 launch path sets `adapter.TailMode = claudecode.StartFromEnd` so
  the tailer seeks past the pre-existing transcript before live tail
  begins. Logged at INFO so operators can see the decision in
  hostrunner.log.
- **mapper.go**: new `assistantTextNoise` set — currently just
  `"No response requested."`, matched EXACTLY on `mapAssistantBlock`'s
  `case "text"`. Drop is surgical: any reply that merely quotes the
  string (e.g. a user-facing summary that includes it) still flows.
- **Note on the resume init race**: defect 2's auto-injected stub
  typically lands BEFORE the resumed adapter attaches (claude's
  `--resume` init takes a moment), so `StartFromEnd` alone usually
  skips it. The mapper-side noise filter is belt-and-suspenders for
  the race-condition window when claude is unusually fast.

**Test coverage.**

- `TestMapLine_AssistantTextNoise_NoResponseRequestedDropped` —
  asserts both the drop AND the negative case (quoted-constant still
  flows) so a future widening of the set doesn't accidentally swallow
  legitimate replies.
- `TestCmdContainsResumeFlag` — 7-case sweep including the spliced
  shape, equals form, mid-flag position, fresh spawn, empty cmd,
  substring-trap, and the `cd <workdir> && claude --resume …` form
  the launch path actually produces.

Root cause class. **Producer protocol assumption changed at a
boundary** — the v1.0.672 resume fix assumed each resume opens a new
JSONL (so all events at byte 0 of any JSONL the adapter sees are
fresh content). Reality is claude-code reuses the file. Same family
as the v1.0.666 `replay:true` cross-driver assumption: a producer
contract that holds for one driver path (fresh spawn) doesn't hold
for an adjacent one (resume). Detection: when adding any
mode-conditional adapter behaviour, enumerate the modes and verify
each against a real on-disk artefact.

---

## v1.0.672-alpha — 2026-05-24

ADR-027 W11 fix-up wedge #15 — M4 claude-code resume cold-started a
fresh session every time. User reported: "could M4 mode resume
previous session? i just test resume and it seems it just start a new
session."

**Root cause.** End-to-end chain was broken at the producer end:

1. `handleResumeSession` (`hub/internal/server/handlers_sessions.go:586-596`)
   reads `sessions.engine_session_id` and, for `claude-code`, calls
   `spliceClaudeResume` to thread `--resume <uuid>` into the respawn
   `backend.cmd`.
2. `spliceClaudeResume` injects the flag after the `claude` bin token.
3. The new claude process picks up the flag and reattaches to the
   prior JSONL.

Step 1 needs `engine_session_id` populated. The hub populates it via
`captureEngineSessionID` (`handlers_sessions.go:711-730`), which
filters for `kind == session.init && producer == agent` and reads
`payload.session_id`. M1/M2 emit this naturally from their engine's
`init` frame. **M4 synthesises session.init from the first usage
event** (v1.0.667) — and that synthetic payload carried
`{engine, model, cwd, version}` with **no `session_id` field**. So
`captureEngineSessionID` returned early at the empty-string guard,
the column stayed NULL, the splice was a no-op, and every resume
opened a fresh `<new-uuid>.jsonl`.

**Fixed.**

- `Adapter.engineSessionID` captured in `resolveAndRun` once
  `WaitForSessionSince` picks the live JSONL — the UUID is the
  basename of the file (sans `.jsonl`). Verified by sampling a fresh
  JSONL: each line carries a `sessionId` field that matches the
  basename, and that same UUID is what claude-code's own `--resume`
  flag takes (`hub/internal/server/resume_splice.go:65-100`).
- `maybeEmitSessionInit` now includes `session_id: <uuid>` on the
  synthetic payload when the field is non-empty.
- Adapter Info log now reports `engine_session_id` alongside the
  JSONL path so operators can spot a resolution gap.

**Test coverage.** Existing
`TestAdapter_SynthesisesSessionInitFromFirstUsage` extended to:
- use a UUID-shaped JSONL basename (matches real on-disk shape)
- assert `payload["session_id"]` equals the basename UUID

The hub-side resume-splice path is already covered by
`resume_splice_test.go` (no change needed — it tests against an
already-populated `engine_session_id`, which is now actually getting
populated end-to-end for M4).

**Known caveat.** Each `--resume` opens a NEW `<new-uuid>.jsonl`
file with a new UUID (claude-code's design: the prior session is
referenced internally, not appended to). The adapter's first usage
event on the resumed agent will overwrite `engine_session_id` with
the new UUID, which is the right behaviour for the next resume in
the chain — but resume-of-resume hasn't been smoke-tested on device.

Root cause class. **Producer-side payload incompleteness silently
disables a consumer-side feature.** Same class as v1.0.666's
`replay:true` drop and v1.0.652's persona-prompt-not-written
[[feedback_cross_driver_replay_tag]] — a feature the codebase
appears to support fails end-to-end because one driver omits a
field that other drivers happen to provide. Detection: when adding
a new driver, audit every consumer that reads any field on the
events it emits, not just the rendering pipeline.

---

## v1.0.671-alpha — 2026-05-24

ADR-027 W11 fix-up wedge #14 — drift-resistance for the context-
window lookup. v1.0.670 used exact-name matching against the 5
models claude-code's `gm()` knew about at extraction time. The
moment Anthropic ships `claude-opus-4-8` or `claude-sonnet-5-0`,
that table goes stale and the chip suppresses for the new model
until someone pushes a hub patch.

**Fixed.**

- `mapper.go::claudeModelContextWindow` rewritten as a prefix
  heuristic plus per-name legacy overrides:
  - **Env override** (`CLAUDE_CODE_MAX_CONTEXT_TOKENS`) wins first —
    operator's always-correct lever for any tier/model combo the
    default heuristic gets wrong.
  - **Legacy overrides**: `claude-opus-4-0`, `claude-opus-4-1`,
    `claude-opus-4-5` stay 200K despite the family default. These
    are the three opus-4 minors that shipped before Anthropic flipped
    the family to 1M at opus-4-6.
  - **200K families**: `claude-haiku-*` + `claude-3-*` (3.x used
    the reversed naming form `claude-3-opus-…` so this rule catches
    every 3.x variant cleanly).
  - **1M families**: `claude-opus-*` + `claude-sonnet-*`. The prefix
    only ever matches v4+ models — v3 was reversed — so the family
    rule is safe to apply broadly. Future generations
    (`opus-5-0`, `opus-6-2`, `sonnet-5-0`, dated variants like
    `claude-sonnet-7-0-20280515`) auto-pick up 1M without a hub
    patch.
  - 0 (chip suppresses) for unrecognised identifiers.

- New helper `stripModelDateSuffix(model)` removes `-YYYYMMDD` or
  `@YYYYMMDD` tails (8 ASCII digits) so dated variants
  (`claude-opus-4-1-20250805` etc.) hit the right bucket.

**Test coverage.** Sweep expanded to 17 cases covering: gm() set,
legacy overrides, future generations (opus-5/6, sonnet-5/7), dated
variants in both buckets, haiku, 3.x, unknown-family suppression.
Plus a dedicated `TestStripModelDateSuffix` for the suffix helper
(7 cases including pathological 6-digit / alphanumeric / empty
inputs that must NOT strip).

**Limitations carried forward from v1.0.670.** Non-Max users on
1M-capable models still see the chip over-count; workaround stays
the `CLAUDE_CODE_MAX_CONTEXT_TOKENS` env var. A full fix would read
`~/.claude.json::oauthAccount.organizationRateLimitTier` per spawn
— deferred.

Root cause class. Stale tables. Avoid exact-name allowlists for any
data Anthropic versions on a quarterly cadence; lean on family
prefixes when the naming convention is structured enough to make
the heuristic honest.

---

## v1.0.670-alpha — 2026-05-24

ADR-027 W11 fix-up wedge #13 — context-window chip showed `<used>/200K`
on a `claude-opus-4-7` spawn that actually has a 1M-token window
(Max-tier OAuth account). v1.0.667 hardcoded 200K for every
`claude-*` model; that under-counted by 5× for the Pro+/Max-tier
1M-capable families and the chip's `%` then implied way less
headroom than really existed.

**Investigation source** (recorded so future updates know how to
re-derive the mapping when Anthropic ships new models): claude-code
binary string sweep on the dev box, 2026-05-24:

- `function JG(model)` — claude's own context-window resolver.
  Honours `CLAUDE_CODE_MAX_CONTEXT_TOKENS` env var first, then a
  cascade of model + auth + beta-header checks, falling through to
  `n56 = 200000`.
- `function gm(model)` — returns true for 1M-capable models:
  `claude-opus-4-7, claude-opus-4-6, claude-sonnet-4-6,
  claude-sonnet-4-5, claude-sonnet-4-0`. False for haiku +
  claude-3-* + older opus.
- `function G7H(model)` — adds claude-opus-4-7 specifically when
  auth provider is firstParty+wA() / anthropicAws / mantle.
- `~/.claude.json::oauthAccount.organizationRateLimitTier` =
  `default_claude_max_5x` on the dev box confirmed the user
  qualifies for 1M.

**Fixed.**

- `mapper.go::claudeModelContextWindow(model)` now returns:
  - the env override if `CLAUDE_CODE_MAX_CONTEXT_TOKENS` is set
    (same variable name claude-code honours; host-runner and
    claude agree on the cap)
  - 1,000,000 for the gm() set (exact-name match — a future
    suffix lands in the 200K fallback until added explicitly)
  - 200,000 for `claude-opus-4-0/4-1/4-5`, `claude-haiku-*`,
    `claude-3-*`
  - 0 for unknown identifiers (mobile suppresses the chip)

**Test coverage.**

- `TestMapLine_UsageCarriesContextWindowFromModel` swept to cover
  9 models across the two buckets.
- `TestMapLine_UsageRespectsEnvOverride` locks the env-var path.
- `TestMapLine_UsageInvalidEnvFallsThroughToModelDefault` —
  guards against a typo silencing the chip.

**Known limitation.** For non-Max users running 1M-capable models,
the chip will now over-count (show 1M cap when the account is
actually capped at 200K). The fix would be to read
`~/.claude.json::oauthAccount.organizationRateLimitTier` per spawn
and gate the 1M decision on it; deferred — couples the hub to
claude-code's config schema, and the operator workaround (export
`CLAUDE_CODE_MAX_CONTEXT_TOKENS=200000`) is one line. Over-count
is operationally better than the pre-v1.0.670 under-count, which
also failed silently when the operator hit the real cap claude saw.

---

## v1.0.669-alpha — 2026-05-24

CI rescue. v1.0.668 mobile change introduced two
`unnecessary_non_null_assertion` warnings in `agent_feed.dart`
(`latestContextWindow! > 0` and `t.contextWindow = latestContextWindow!`
after the `!= null` guard — Dart's flow analysis promotes the
local to non-null, making the `!` redundant). `flutter analyze`
treats warnings as fatal, so CI rejected the build. Hoisted the
field into a local with `final cw = latestContextWindow;` and
referenced `cw` for both reads — no functional change. Also fixed
a stale `${prefix}(empty reply)` info-level brace warning while
touching the file. No version-bumpable behaviour change beyond
v1.0.668; everything in that wedge stands.

---

## v1.0.668-alpha — 2026-05-24

ADR-027 W11 fix-up wedge #12. Two M4 polish gaps surfaced after the
v1.0.667 APK install — context-utilisation chip now renders fine,
cancel-button-race + token-flow pill remain.

**Fixed.**

- *Cancel button stuck after MCP-tool turns.* `hookStop` posted
  `turn.result` SYNCHRONOUSLY the moment claude invoked the Stop
  hook — before the JSONL tailer had a chance to read + post the
  preceding assistant `text` frame. Wire seq order ended up
  `turn.result(N) → text(N+1) → usage(N+2)`, and mobile's
  `_isAgentBusy` walks tail-first: usage skip (v1.0.667), then
  text → return busy. turn.result at seq N was never reached.

  Fix: emit `turn.result` from the JSONL's OWN
  `system{subtype:turn_duration}` frame — the LAST frame claude
  writes for a turn, after assistant text + stop_hook_summary.
  That guarantees turn.result has the HIGHEST seq of the turn,
  so the walker hits it first and flips to idle. Dropped the
  turn.result emission from `hookStop`; FSM transition kept.

- *Token-flow pill stayed blank.* `_TelemetryStrip` gates the
  pill on `modelTotals.isNotEmpty`, and the populating source is
  `turn.result.by_model` (codex/claude stream-json shape). M4's
  `turn.result` carries no `by_model`, so the pill stayed
  suppressed even though every assistant message had full usage.

  Fix: after the events loop, synthesise a `_ModelTokens` entry
  from per-message usage when `modelTotals` is empty. SET semantics
  on every field (NOT add) so the pre-v1.0.662 sum-across-tool-use-
  iterations bug doesn't reappear. Bucket key = the per-message
  `model` (e.g. `claude-opus-4-7`) or `claude-code` if unknown.

**Test coverage.**

- `TestMapLine_TurnDurationSystemEmitsTurnResult` — locks the new
  mapper emission shape (kind/producer/reason/status/duration_ms/
  message_count).
- `TestOnHook_StopOnlyTransitionsFSM` — replaces
  `TestOnHook_StopEmitsTurnResultForBusyWalker`; asserts hookStop
  no longer posts ANY event, only flips FSM state.

Mobile changes covered by CI flutter analyze.

**Note on the change of emission timing.** Pre-v1.0.668, turn.result
landed within milliseconds of claude finishing a turn (hook is
synchronous on engine side). Post-v1.0.668, it lands within one
tailer poll-tick (≤250ms default) after claude writes the
`system{turn_duration}` frame. Net: ~250ms added latency on the
cancel-button flip, in exchange for correct ordering against
text/usage. Acceptable — the alternative was the cancel button
never flipping at all.

---

## v1.0.667-alpha — 2026-05-24

ADR-027 W11 fix-up wedge #11 — three residual M4 polish gaps surfaced
after v1.0.666 (the text-rendering fix) on-host smoke.

**Fixed.**

- *Cancel button stuck on after end-of-turn.* Mobile's `_isAgentBusy`
  walks events tail-first and treats any "agent-produced" kind as
  proof a turn is still in motion. The M4 wire order at end-of-turn
  is `turn.result → text → usage` (turn.result is posted from the
  Stop hook handler, text+usage from the JSONL tail), so the LATEST
  event is `usage`. Walker fell through to the default "busy = true"
  branch. Added `usage` (and `rate_limit`, similar shape) to the
  skip list — both are pure telemetry kinds that don't move the
  turn-progress signal.

- *Context-utilisation chip stayed blank.* The mapper's per-message
  usage event carried `input_tokens`/`cache_read`/`cache_create` but
  no `context_window`, so mobile couldn't compute the percentage
  (chip suppresses itself when capacity is zero). Now the mapper
  derives the capacity from the model name via a small lookup
  (`claude-opus-*` / `claude-sonnet-*` / `claude-haiku-*` /
  `claude-3-*` → 200000; everything else → omit so the chip
  stays suppressed rather than render a wrong %). Mobile's
  per-message usage branch picks up the new field.

- *Session.init header chip blank.* M4's on-disk JSONL has no
  equivalent of M2 stream-json's `init` frame, so mobile's AppBar
  chip (engine + model + cwd row) stayed empty. The adapter now
  synthesises a `session.init` event from the FIRST usage frame it
  sees (the one that carries the model name). Idempotent per
  Adapter lifetime — subsequent usage events don't re-emit.

**Test coverage.**

- `TestMapLine_UsageCarriesContextWindowFromModel` — sweeps all 4
  known model prefixes, asserts 200K each.
- `TestMapLine_UsageOmitsContextWindowForUnknownModel` — better blank
  than wrong.
- `TestAdapter_SynthesisesSessionInitFromFirstUsage` — exactly one
  init landed; payload carries engine/model/cwd; lands BEFORE the
  first usage event so mobile's build pass picks it up in the same
  pass.

Mobile changes covered by CI `flutter analyze`.

---

## v1.0.666-alpha — 2026-05-24

ADR-027 W11 fix-up wedge #10 — the load-bearing one. Closes the
"mobile shows nothing for M4" investigation that opened at v1.0.662
and survived four diagnostic wedges.

**Fixed.**

- *Agent text + thought never reached mobile.* The M4 adapter's
  `runLoop` stamped `replay: true` on every event when
  `TailMode == StartFromBeginning` — which is the default for fresh
  spawns. Mobile's `agent_feed.dart:749` unconditionally drops
  `kind=text` and `kind=thought` events that carry `replay: true`
  (the M1 ACP `session/load` dedup path). Every assistant reply,
  every Thinking… frame from M4 was silently nuked on the client.
  Cancel button got stuck on because — wait, that was actually
  unrelated; turn.result IS rendered fine, but it doesn't flip the
  busy state when the only events landing are lifecycle +
  input.text + turn.result + usage (no text frames to pair them
  with). Once text frames arrive, the chat reads correctly.

  Removed the per-event `replay: true` stamping in
  `adapter.go::runLoop`. M4 doesn't need the tag at all:
  - SSE delivery is seq-gated (`since=<maxSeq>`) — events mobile
    already cached aren't redelivered.
  - ID dedup in `agent_feed.dart` (`_ids.add(id)`) catches anything
    else, since hub event IDs are globally unique and stable across
    cold-open + live tail.

  The mobile-side `replay: true` filter stays as-is — it remains
  useful for the M1 ACP `session/load` path it was designed for.

**Diagnosis trail.** Five wedges to find one bug. v1.0.661 dropped
the noise frames that were masking the symptom. v1.0.662 added per-
message usage. v1.0.663 dropped duplicate user_input + state_changed
+ dedupped hooks (those WERE real bugs in their own right, all
caught after the noise dropped at v1.0.661). v1.0.664 raised
post-failure log to Warn. v1.0.665 added HOSTRUNNER_LOG_LEVEL. Each
wedge eliminated one hypothesis; v1.0.665's debug-level posting log
proved every event was making it to the hub. A direct curl against
the remote hub confirmed all 5 events lived in `agent_events`
correctly with the right session_id. That left "mobile filter" as
the only remaining suspect — and the replay filter, designed for a
totally different driver, was the culprit.

**Test coverage.** Updated
`TestAdapter_Start_ReplaysExistingThenLive` to assert "replay tag
MUST be absent" — the inverse of the prior assertion that locked
the buggy behaviour in.

Root cause class. Cross-driver feature reuse. The M1 ACP driver had
a real need for a replay tag (session/load notifications re-emit
historical events with new IDs); the adapter framework propagated
the convention to M4, but M4's events come from a strictly-monotonic
JSONL tail where the existing seq + ID guarantees make the tag
redundant — and given mobile's drop-text-on-replay rule, harmful.
Convention copied without checking the consumer.

---

## v1.0.665-alpha — 2026-05-24

ADR-027 W11 fix-up wedge #9 — diagnostic-only. After v1.0.664
on-host smoke confirmed the tailer is running (the new "first JSONL
line received" INFO line landed) and NO "post failed" Warn appeared
(= posts are succeeding), the bug narrows to hub-side or mobile-side.

**Added.**

- `HOSTRUNNER_LOG_LEVEL` env var on the `host-runner run` command.
  Accepts `debug` / `info` / `warn` / `error` (case-insensitive,
  default Info, unknown → Info so a typo never silences the host).
  At `debug` the adapter logs one line per successful POST
  (`claude-code adapter: posted agent_id=… kind=…`) so an on-host
  smoke can confirm whether every assistant text + usage + turn.result
  reached the wire — closing the diagnostic loop opened at v1.0.664.

How to use:

```bash
HOSTRUNNER_LOG_LEVEL=debug /tmp/host-runner run --hub … 2>&1 | tee /tmp/hr.log
# then in another shell:
grep "claude-code adapter:" /tmp/hr.log
```

No behaviour change.

---

## v1.0.664-alpha — 2026-05-24

ADR-027 W11 fix-up wedge #8 — diagnostic-only. On-host smoke of
v1.0.663 reproduced the "mobile shows nothing" symptom: agent text
not on mobile, cancel button stuck on (= no turn.result received),
session.init / token chip / context chip all blank. JSONL has the
assistant text + Stop hook attachment correctly; running the actual
mapper against the file locally confirms it emits `kind=text` +
`kind=usage` per the v1.0.661/662 design. Conclusion: the events
ARE being generated, but somewhere between mapper output and the
hub's `agent_events` row, they vanish without surfacing an error on
the host-runner terminal.

Cause is not yet known — the smoking gun for picking between "POST
failing silently", "POST succeeding but rejected by hub", or "POST
succeeding but mobile filters by session_id which is NULL" requires
a stderr trace from the next run. Diagnostic logging was buried at
Debug (the default level), so the on-host smoke had no signal at
all. This wedge raises it to Warn for the failure case and adds a
one-shot INFO breadcrumb for the success case so the next on-host
smoke can tell us which leg is broken.

**Changed.**

- `adapter.go` `runLoop`: post-failure log raised from Debug to Warn,
  with a clear "post failed" message and the agent_id / kind / err.
- `adapter.go` `runLoop`: one-time INFO log when the first JSONL line
  arrives — confirms the tailer is producing data when the mobile
  transcript stays empty.
- `hooks.go` `post`: hook-emit failure log raised from Debug to
  Warn. Same reasoning as runLoop — hookStop's turn.result is the
  signal mobile's busy walker watches, and a silent drop means the
  cancel button never flips off.

No behaviour change. If v1.0.664 fixes nothing, the stderr signal
will tell us where to dig next.

---

## v1.0.663-alpha — 2026-05-24

ADR-027 W11 fix-up wedge #7 — three on-device noise sources caught
on the v1.0.662 dev-box smoke. Same root-cause class as v1.0.661 +
v1.0.662: the M4 pipeline emits frames mobile doesn't have a clean
renderer for, so each one lands as a raw JSON dump or a duplicate.

**Fixed.**

- *Every hook event fired TWICE.* claude-code rewrites
  `<workdir>/.claude/settings.local.json` when the operator accepts
  the per-server MCP enable dialog (it adds `enabledMcpjsonServers`)
  AND silently strips top-level keys it doesn't recognise — including
  our `_termipod_managed: true` marker. The next spawn's
  `appendTermipodMatcher` saw the marker-less entry, decided it was
  operator-authored, preserved it, and appended a SECOND entry
  pointing at the new (live) UDS socket. Every hook then fired
  twice, the first hitting a dead socket. Fix: backup identifier
  matches by `<hookFireExe> hook-fire ` command-string prefix even
  without the marker. `isManagedByCommandShape` lives in
  hooks_install.go.

- *FSM emitted `system{subtype:state_changed,from,to,reason}` on
  every transition.* Same shape as the three hook-emitted system
  frames we dropped at v1.0.661 — mobile had no renderer, every
  PreToolUse/Stop/Notification dumped a raw JSON blob in the
  transcript. The FSM still tracks state internally (drives
  hookStop's `turn.result` emission indirectly); only the
  per-transition poster call is gone. `state.go` `Transition` now
  logs at debug level for operator forensics instead.

- *user_input duplicated as the directive envelope.* The hub stores
  what the user typed as an `input.text` event the moment mobile
  POSTs to `/v1/agents/<id>/input` (handlers_sessions.go) — that
  record is the canonical, mobile-rendered source. The M4 mapper's
  `mapUserString` was also emitting `kind=user_input` from the
  JSONL replay, carrying the FULL hub-injected envelope (`[directive
  from the principal]\n<body>\n\nReply in this chat…`). Mobile
  rendered both as user-side messages — typed "hi" and a verbose
  envelope showed as a "duplicate" row. Fix: drop the user_input
  emission. `mapUserArray` (tool_result branch) is unaffected.

**Test coverage.** Updated four mapper/integration tests to assert
the v1.0.663 drops (`TestMapLine_UserStringIsDropped` +
`TestMapLine_MultiLineSessionOrdering` sequence,
`TestAdapter_Start_ReplaysExistingThenLive` and siblings use
assistant text for sentinels). Rewrote three FSM tests to assert
"transition updates state, posts nothing"
(`TestFSM_TransitionUpdatesStateWithoutPosting`,
`TestFSM_SequenceOfTransitionsLeavesStateAtTerminal`). Added two
hooks_install tests (`TestInstallClaudeHooks_DedupsWhenManagedMarkerStripped`
+ `TestInstallClaudeHooks_PreservesUnrelatedManagedlessEntries`)
locking the backup-identifier behaviour against operator hooks.

Root cause class. Every M4 cleanup wedge since v1.0.661 has been the
same pattern — "the producer emits more than the renderer can
consume; mobile's fallback is a JSON dump or a stray card."
Following candidate emissions to audit next: SubagentStop
`system{subagent_complete}`, SessionEnd `system{session_end}`, and
the `kind=attachment` fan-out for unknown attachment types (already
filtered for the 5 known telemetry shapes at v1.0.661 — but new
shapes will appear).

---

## v1.0.662-alpha — 2026-05-24

Three on-device UX issues caught after the v1.0.661 deploy. Touches
hub (M4 mapper) + mobile (sessions, admin) — all three independent,
shipped together because each is small and they all stem from "the
data plumbed through the layers is wrong-shaped for what the surface
shows."

**Fixed.**

- *Context-window chip showed >1M tokens on uncompacted sessions.*
  The chip's `used` number was sourced from
  `turn.result.by_model.<model>.input + cache_read + cache_create`
  — claude-code's modelUsage block reports SUMS across every API
  call inside a turn, so a turn with many tool-use iterations
  (Read/Bash/Write each doing their own API call) reported many
  multiples of the actual current context. The right input is one
  API call's prompt size = what claude's own `/context` slash
  command shows. The driver_stdio M2 path already emitted per-message
  `usage` events but mobile only consumed cumulative-marked ones
  (codex shape); M4 LocalLogTail's mapper didn't emit usage at all.
  Now mapper.go's `mapAssistant` emits a `kind=usage` event for
  every claude `message.usage` block; mobile's telemetry strip
  prefers per-message-snapshot over the per-turn fallback. Chip now
  matches `/context` exactly.

- *Sessions multi-select had only Archive + Delete + global
  Select-all.* Long-press → multi-select mode now also exposes:
  - **Stop** action (bottom bar) — terminates the agent serving each
    selected ACTIVE session via `terminateAgent`. Dedups by
    `agent_id` so a steward with current + archived sessions
    selected only fires the call once. Skips
    closed/archived/deleted rows with a SnackBar explanation.
  - **Per-category filter chips** (strip under the AppBar) — one
    chip per non-empty category (General / Project / Domain /
    Detached) with row count. Tapping narrows visible AND
    Select-all/Invert/bulk-action scope to the picked
    categories. Empty filter = no narrowing.
  - **Invert selection** action (AppBar) — appears once at least
    one row is selected; useful for "select-all then drop a few"
    workflows.

- *Admin command rows looked unbalanced* (label not centered,
  trailing `slide ▸` mono text too small to register as
  interactive). `ConfirmActionTile` redesign: label
  `fontSize: 13 → 14, weight 600 → 700, centered with
  textAlign + Stack-based layout` so it stays centered even when
  the trailing affordance changes; leading icon enlarged 18 → 20
  and left-aligned; trailing "slide ▸" replaced by a pill chip
  with a chevron icon (visible at a glance as "drag me"); height
  `48 → 52, border-radius 8 → 10`; border highlights when armed;
  background fill darkens slightly when armed so the operator gets
  two redundant signals (chip dim + fill darken) that the commit
  gesture is engaged.

**Added.**

- `usageFromMessage(model, usage)` helper in
  `mapper.go` — folds a claude `message.usage` block into a
  `kind=usage` MappedEvent. Drops all-zero usage blocks and missing
  usage blocks (chip stays at its prior snapshot rather than going
  to zero).
- `sessionsProvider.bulkStop(agentIds)` — sequential
  `terminateAgent` sweep with per-id failure collection, mirroring
  `bulkArchive` / `bulkDelete`.
- `_CategoryFilterStrip` + `_invertSelection` + `_bulkStop` on the
  Sessions screen.
- `_SlideChip` widget extracted from `ConfirmActionTile` for the
  redesigned trailing affordance.

**Test coverage.** Three new mapper tests (usage emitted with
correct shape, usage absent when block missing, usage absent when
all-zero); existing mapper tests unchanged. Mobile-side changes
covered by CI's `flutter analyze` (no flutter SDK locally).

Root cause class. Issue 1 is the same "the producer emits more than
the renderer expects" shape from v1.0.661 — driver_stdio emits
per-message usage that mobile silently ignored unless flagged
cumulative; the chip then fell through to a per-turn sum that
double-counts on multi-iteration turns. Caught only by on-device
inspection; the test surface for context-chip accuracy was missing.

---

## v1.0.661-alpha — 2026-05-24

ADR-027 W11 fix-up wedge #5 — claude-code M4 cleanup pass, caught on
on-host smoke of v1.0.660. Fixes four distinct symptoms surfaced by
the first end-to-end on-device test of the M4 LocalLogTail spawn
since the async-Start refactor:

**Fixed.**

- *Stale-JSONL bleed-through.* Adapter's session resolver used
  `ResolveLatest` (newest `.jsonl` by mtime, no lower bound). When a
  workdir had a prior interactive `claude` session, its transcript —
  including `/exit` slash-command + the `<local-command-caveat>`
  wrapper claude injects for command lines — replayed into the new
  agent's feed on attach. New `WaitForSessionSince(minMtime)` +
  `ResolveLatestSince(minMtime)`; `NewAdapter` records a "now"
  cutoff and passes it through, mirroring agy's brain-dir-since-
  launch resolver shipped at v1.0.645.
- *Three raw-JSON `system{subtype:…}` frames on mobile.* `hookSessionStart`
  posted `system{session_start,source,model}`, `hookStop` posted
  `system{turn_complete,final_message,permission_mode}`, and the
  `idle_prompt` branch of `hookNotification` posted
  `system{awaiting_input}`. Mobile has no renderer for these subtypes
  so each turn left a JSON blob in the transcript. All three are
  redundant: lifecycle:started already covers session start, `turn.result`
  (which `hookStop` still posts) is the canonical end-of-turn signal
  mobile's busy walker listens on, and the FSM transition alone is
  enough for idle_prompt. The three `a.post(…)` calls in `hooks.go`
  are gone; existing tests rewritten to assert "no longer emitted".
- *Attachment fan-out leaking telemetry + registry deltas.* `mapAttachment`
  forwarded every `attachment` JSONL line as a `kind=attachment` event
  — including `hook_success`/`hook_error` records of our own hooks
  firing (one per Stop/SessionStart/etc, every turn) and claude's
  internal `deferred_tools_delta`/`agent_listing_delta`/`skill_listing`
  registry sync frames. New `attachmentDropTypes` set filters those
  five inner types before fan-out; legitimate file attachments still
  flow through. `ai-title` added to the top-level drop list (was
  falling through to the `unknown_type` drift handler).
- *Two confirm dialogs on cold start.* Pre-trust covered the workdir
  trust dialog (v1.0.657) but not the per-server MCP enable consent
  or the bypass-permissions confirm. `hooks_install.go` now also
  writes `enabledMcpjsonServers:["termipod","termipod-host"]` into
  `settings.local.json` (merging with any operator-set list, lifting
  any prior denial); `steward.claude-m4.v1.yaml` adds
  `--allow-dangerously-skip-permissions` (the operator-opt-in
  meta-flag) ahead of `--dangerously-skip-permissions` so claude's
  `BypassPermissionsModeDialog` short-circuits. Source confirmation:
  `claude --help` v2.1.144 + `allowDangerouslySkipPermissionsPassed`
  string sweep against the binary on the dev box, 2026-05-24.

**Added.**

- `pathresolver.ResolveLatestSince(minMtime)` +
  `WaitForSessionSince(ctx, dir, pollEvery, minMtime)` — public
  alongside the existing zero-cutoff variants so non-adapter callers
  keep working unchanged.
- `Adapter.SessionCutoff` field; `NewAdapter` seeds it to
  `time.Now().Add(-100ms)` (filesystem-mtime slack).
- `hooks_install.preEnabledMcpServers` + `mergeEnabledMcpServers` +
  `removeManagedFromDisabled` helpers with idempotency + preserve-
  operator + lift-prior-deny tests.

**Test coverage.** Five new tests in `claude_code` (attachment drop
sweep, real-attachment passthrough, stale-JSONL cutoff, zero-cutoff
parity, ai-title drop); four new tests in `hostrunner` (MCP fresh
grant, no-duplicates on repeat, preserves-operator, lifts-prior-deny);
three hook tests rewritten in-place to assert the v1.0.661 drops.

Root cause class. Three of the four bugs are the same shape — a
pipeline emitting *more* than the surface above it can render, with
mobile silently JSON-dumping the residue. Hooks emitting signals that
duplicate cleaner JSONL/turn.result channels; mapper forwarding every
attachment regardless of whether mobile has a card for it; pre-trust
covering one dialog out of three. Filter at the producer, not at the
renderer, and only emit what the next layer agreed to consume.

---

## v1.0.660-alpha — 2026-05-23

ADR-027 W11 fix-up wedge #4 — caught on on-host smoke of v1.0.659:

```
ERROR msg="M4 LocalLogTail launch failed; marking agent failed (no PaneDriver fallback)"
  handle=claude-m4-steward
  err="locallogtail M4: driver start: local_log_tail adapter start:
       claude-code adapter: wait for session jsonl in ...
       waiting for claude-code session in ...:
       context deadline exceeded"
```

### Two failures in one error

1. **30s synchronous deadline.** The claude-code adapter's `Start`
   blocked on `WaitForSession` (the polling lookup for claude's
   on-disk session JSONL) with a 30-second default. claude doesn't
   write a JSONL until it has cleared the welcome screen (any
   first-run dialogs: trust, model picker, hook warnings) AND
   received its first message — easily past 30s on a cold start.
2. **Sync failure escalated to hard fail.** With `Start` returning
   an error, `launchM4LocalLogTail` returned an error, the W7
   runner posted `lifecycle:failed` + `PatchAgent(status:failed)`
   (the no-PaneDriver-fallback path that v1.0.657 introduced), and
   the agent appeared dead in the mobile UI — even though the tmux
   pane was perfectly healthy and claude was actively initializing.

Both symptoms are the same defect class agy fixed at v1.0.643 in
its own adapter — synchronous start with a tight deadline doesn't
match the human-paced interactive engine reality. v1.0.660 ports
agy's resolve-and-run shape to the claude-code adapter.

### Fix shape

`hub/internal/drivers/local_log_tail/claude_code/adapter.go`:

- `Adapter.Start` now returns `nil` immediately after kicking off a
  `resolveAndRun` goroutine. `started=true` + `cancel`/`fsm`
  initialisation happen synchronously inside the lock so a second
  `Start` is a no-op (idempotency preserved).
- `resolveAndRun` owns the lifecycle: HOME resolve → mkdir
  projectDir → `WaitForSession(waitCtx, ...)` → `Tailer.Start` →
  `runLoop`. `defer a.wg.Done()` at the top accounts for the
  goroutine the `Stop` path waits on.
- Default `SessionWaitTimeout` bumped from **30s → 30 min**, mirroring
  agy. The wait now budgets the human-paced welcome-screen path, not
  just a JSONL-flush race.
- New `noteFailure(ctx, phase, err)` posts a `system{text: "...tail
  unavailable... pane is still live — type to interact"}` event on
  any goroutine-side failure. The agent stays in `running` status;
  HandleInput keeps working (it only needs PaneID, set by the W7
  launcher before Start). Same noteFailure shape as
  `antigravity/adapter.go`.

`runLoop` no longer touches the WaitGroup — `resolveAndRun` is its
caller and owns the `wg.Done`. Pre-async, `runLoop` was its own
goroutine; post-async it's a synchronous call inside
`resolveAndRun`, so a stray `defer wg.Done()` here would double-Done
and panic.

### Tests

- `TestAdapter_Start_AsyncWaitsForSessionFile` (renamed from
  `TestAdapter_Start_WaitsForSessionFile`) — asserts Start returns
  promptly (<50ms) AND the delayed session file is still picked up
  by the goroutine.
- `TestAdapter_Start_TimesOutPostsSoftFailureEvent` (renamed from
  `TestAdapter_Start_TimesOutWhenSessionNeverAppears`) — asserts a
  short SessionWaitTimeout produces a `system{text: "tail
  unavailable"}` event, not a Start-time error.
- `TestAdapter_StartIsIdempotent` (renamed from
  `TestAdapter_StartIsIdempotent_OnFailure`) — Start always returns
  nil now; the idempotency check is that the second call doesn't
  spawn a second goroutine (covered by Stop's WaitGroup accounting).

### Net effect for the user

Before v1.0.660 (the smoke report):
- Launch path: `driver.Start` blocks 30s → times out → agent
  marked failed → tmux pane orphaned, claude still running.

After v1.0.660:
- Launch path: `driver.Start` returns immediately → agent marked
  running → mobile can send text via send-keys → claude finishes
  its welcome screen and starts a session → JSONL appears →
  resolver picks it up → events stream live. If claude truly
  never starts a session within 30 min, a `system` notice surfaces
  the half-broken state without killing the agent.

### Tag

- Tag: `v1.0.660-alpha`

## v1.0.659-alpha — 2026-05-23

ADR-027 W11 fix-up wedge #3 — **claude-code M4 boot regression: hooks
have been silently broken since v1.0.592**. On-host smoke of v1.0.658
caught it on first start: claude-code refuses to launch with
`Expected string, but received undefined. Hooks use a matcher + hooks
array. ...`

### Root cause

`hub/internal/hostrunner/hooks_install.go` emitted hook entries of
shape:

```json
{"matcher": "*",
 "_termipod_managed": true,
 "hooks": [{"type": "mcp_tool",
            "tool": "mcp__termipod-host__hook_pre_tool_use",
            "timeout": 30}]}
```

The `type: "mcp_tool"` form was an ADR-027 W6 speculative design —
the idea was claude-code would route hook events through MCP tools
on the `termipod-host` server. **claude-code's actual hook schema
only supports `type: "command"` with a `command: <string>` field.**
The `tool` field is unknown; `command` is missing; the validator
fails at file load and the agent never gets past the welcome screen.

Why it never surfaced for so long: claude-code is mostly run in M2
stream-json mode (no hook installation). M4 LocalLogTail spawns —
introduced v1.0.592 — were rarely exercised end-to-end until the
agy fix-up arc forced parallel smoke on claude-code M4 at v1.0.657.

### Fix: rebuild W6 with a `type: "command"` shim

New host-runner subcommand `hook-fire` (in
`hub/internal/hookfire/`) — a one-shot stdio bridge that wraps the
claude-code hook contract over the existing UDS MCP gateway:

```
claude-code → spawns `host-runner hook-fire --socket <uds> --event <Event>`
            → writes hook payload (single JSON object) to stdin
            → reads response JSON from stdout
hook-fire   → parses stdin as JSON object
            → wraps as JSON-RPC `tools/call` with name=`hook_<event>`
            → dials the per-spawn UDS, half-closes write side
            → reads response line, extracts `result.content[0].text`
            → writes to stdout, exits 0
```

Failure semantics tuned for blocking hooks: a transport blip yields
`{}` on stdout (claude defaults to "allow") + non-zero exit + stderr
warning — same outcome as an uninstalled hook. The previous design's
parking semantics (PreToolUse / PreCompact / AskUserQuestion) still
flow through the gateway's `dispatchHookTool` → `HookSink.OnHook` →
adapter; the only wire change is the front-end format.

`hooks_install.go` rewrites `appendTermipodMatcher` to emit:

```json
{"matcher": "",
 "_termipod_managed": true,
 "hooks": [{"type": "command",
            "command": "host-runner hook-fire --socket '/path/to.sock' --event PreToolUse",
            "timeout": 30}]}
```

The matcher is the empty string (claude-code canonical "match all"
form). `_termipod_managed` is retained as the strip-then-append key —
which lets v1.0.659 spawns self-heal stale workdirs that still hold
the pre-v1.0.659 invalid `mcp_tool` entries (new test
`TestInstallClaudeHooks_SelfHealsStaleMcpToolEntries` locks this).

`hostRunnerExe` + `udsPath` are now plumbed through `installClaudeHooks`
from `launch_m4_locallogtail.go`; the latter already had both values
in scope (the runner exe is `hostRunnerExePath()`, the UDS is
`socketPath(ChildID)`).

### Cancel-button restoration

The v1.0.657 `hookStop` → `turn.result` emission has been dead code
since v1.0.592 (hookStop never fired because no valid hook ever
registered). With hooks fixed, that emission now lights up: end of
turn → Stop hook fires → adapter posts `turn.result{reason:end_of_turn}`
→ mobile `_isAgentBusy()` flips to idle → cancel-on-send overlay
drops. v1.0.657's symptom #4 fix is now actually load-bearing.

### Test additions

- `hub/internal/hookfire/run_test.go` — 6 tests:
  - `TestTransport_RoundTrip` — stdin → tools/call → UDS → response
    JSON unwrap, with an in-process fake gateway.
  - `TestTransport_GatewayErrorSurfaces` — JSON-RPC `error` frame
    propagates as a Go error.
  - `TestTransport_DialFailure` — missing socket → error, no crash.
  - `TestEventToToolName_Complete` — locks the 9-event coverage
    against drift between `hookfire/run.go` and
    `hooks_install.go:claudeHookEvents`.
  - `TestRun_RejectsMissingSocket` + `TestRun_RejectsUnknownEvent` —
    CLI contract (exit 2 on usage errors).

- `hub/internal/hostrunner/hooks_install_test.go` — rewritten:
  - `TestInstallClaudeHooks_NewFile_ValidSchema` — every emitted
    matcher block obeys claude-code's documented hook schema
    (matcher:string, hooks[] with type="command" + command:<string>).
  - `TestInstallClaudeHooks_SelfHealsStaleMcpToolEntries` — a stale
    workdir with the pre-v1.0.659 `type: "mcp_tool"` entries gets
    cleanly upgraded on next spawn.
  - The existing five behaviour tests are updated to the new shape
    (`extractCommands` replaces `extractToolNames`).

### Tag

- Tag: `v1.0.659-alpha`

## v1.0.658-alpha — 2026-05-23

ADR-027 W11 fix-up wedge #2 — the deferred symptom #5 from v1.0.657's
five-bug parallel: **multi-line text input slicing**.

### Root cause

`claude_code/sendkeys.go:inputText` had two paths:

- single-line short body (≤512 chars, no `\n`/`\r`) → `send-keys -l
  <body>` + `send-keys Enter`. **Correct.**
- multi-line OR long body → `strings.Split(body, "\n")` → for each
  line: `send-keys -l <line>` + `send-keys Enter`. **Wrong.**

Each `send-keys Enter` submits the current input buffer to claude's
TUI. A 5-line message therefore landed as 5 SEPARATE user turns — only
the first line's "/" + slash command (or whatever started the body)
received any meaningful reply, the rest streamed in as bare-text
prompts the agent then tried to address one at a time. ADR-032 envelope
bodies (4-line `[<kind> from <sender>]\n<text>\n\n<reply instruction>`)
were among the worst affected: the agent saw a header alone, then the
text alone, then the reply instruction alone, never a coherent
directive.

### Fix shape — same as agy v1.0.652

`hub/internal/drivers/local_log_tail/claude_code/sendkeys.go`:

```go
if len(body) <= 512 && !strings.ContainsAny(body, "\n\r") {
    // cheap path unchanged
} else {
    bufName := "ccinput_" + strings.TrimPrefix(a.PaneID, "%")
    runner.Run(ctx, "tmux", "set-buffer", "-b", bufName, body)
    runner.Run(ctx, "tmux", "paste-buffer", "-b", bufName, "-d", "-r", "-t", a.PaneID)
    runner.Run(ctx, "tmux", "send-keys", "-t", a.PaneID, "Enter")
}
```

`-r` is the load-bearing flag: it suppresses tmux's default LF→CR
translation, so internal LF bytes in the buffer stay as LF on the
wire. claude's input field accepts them as in-field newlines (the same
`\<Enter>` newline-without-submit affordance it offers interactively),
and only our explicit final `send-keys Enter` triggers submission.
Buffer name keys off pane id so two concurrent multi-line inputs to
different agents don't collide.

Failure path: if `paste-buffer` errors, best-effort `delete-buffer` so
a stale buffer doesn't survive into the next call.

### Test additions

- `TestHandleInput_TextMultilineUsesAtomicPasteBuffer` — the contract
  for the new path (set-buffer + paste-buffer -d -r + Enter, three
  calls total, no per-line Enter).
- `TestHandleInput_TextLongSingleLineUsesPasteBuffer` — bodies >512
  chars still take the paste-buffer path even with no newlines.
- `TestHandleInput_TextCRLFUsesPasteBuffer` — `\r\n` line endings also
  fall through to paste-buffer (the cheap-path guard tests for BOTH
  `\n` AND `\r` via strings.ContainsAny).
- `TestHandleInput_TextMultilineCleansBufferOnPasteFailure` — locks
  the failure-path delete-buffer cleanup.

Replaces the pre-v1.0.658 `TestHandleInput_TextMultilineUsesPerLineSendKeys`
which had calcified the wrong behaviour into the test suite.

### Tag

- Tag: `v1.0.658-alpha`

## v1.0.657-alpha — 2026-05-23

ADR-027 W11 fix-up wedge #1 (mirror of the agy v1.0.643–.652 arc) —
claude-code M4 LocalLogTail launch had the **same five-bug pattern**
agy hit in [v1.0.643–.652](#v10643-alpha--2026-05-23) but stayed
hidden because the path has rarely been exercised end-to-end since
v1.0.592 (most claude-code spawns run M2 stream-json). On-host smoke
on star surfaced all of them in one go.

### The five symptoms (verbatim from the smoke report)

1. host-runner spawns M4 cc in its OWN cwd, not the yaml-configured
   workdir.
2. `WARN msg="M4 LocalLogTail launch failed; falling back to PaneDriver"`
   on every spawn.
3. claude-code shows its "Do you trust this folder?" welcome-screen
   dialog at start (mobile has no affordance to drive that picker).
4. Session status stays `busy` / cancel button never clears after a
   turn ends.
5. (audit-side) silent gap: spawn never materialises `CLAUDE.md`
   persona, so stewards launch persona-less.

### Root causes — five-way table

| # | Symptom | File:line | Class | Mirror of |
|---|---|---|---|---|
| 1 | wrong cwd | `launch_m4_locallogtail.go:LaunchCmd` had no `cd <workdir> &&` prefix; M1/M2/agy-M4 all do | shell-frame ordering | v1.0.643 |
| 2 | PaneDriver fall-through warn | `runner.go:633` kept the silent fallback alive | error-handling cascade | v1.0.643 |
| 3 | trust dialog | no pre-trust of `~/.claude.json` `projects.<workdir>.hasTrustDialogAccepted` | external-tool consent persistence | v1.0.644 (agy equivalent) |
| 4 | busy state never drops | Stop hook emitted `system{turn_complete}` only; mobile `_isAgentBusy()` explicitly skips `system` kinds | UI contract mismatch | v1.0.647 |
| 5 | persona missing | `launch_m4_locallogtail.go` never called `writeContextFiles(workdir, spec.ContextFiles)` | M4 forgot the M1/M2 ritual | v1.0.652 |

### Fix shape

`hub/internal/hostrunner/launch_m4_locallogtail.go`:

- after `MkdirAll(workdir)`, call `writeContextFiles(workdir,
  spec.ContextFiles)` so `CLAUDE.md` lands at the workdir root.
  Identical shape to M1, M2, and agy-M4.
- after `installClaudeHooks`, best-effort call
  `preTrustWorkspaceClaudeCode(workdir)` which writes/updates
  `~/.claude.json` → `projects.<workdir>.hasTrustDialogAccepted: true`
  + `hasCompletedProjectOnboarding: true`. Idempotent (re-spawn is a
  no-op when both flags already set, mtime preserved); preserves
  every other top-level key + every other project entry.
- before `LaunchCmd`, prepend `cd <shellEscape(workdir)> && ` to the
  resolved `spec.Backend.Cmd`. `TmuxLauncher.LaunchCmd` does NOT cd,
  and claude-code's pathresolver keys its session JSONL by
  encoded-cwd — without the prefix the launch hung at
  `WaitForSession` and runner.go reported "M4 LocalLogTail launch
  failed", which then fed back into symptom #2.

`hub/internal/hostrunner/runner.go`:

- drop the PaneDriver fall-through in the claude-code M4 arm. On
  `launchM4LocalLogTail` error: emit a `lifecycle:failed` event
  + `PatchAgent(status=failed)` + return. Mobile then offers respawn.
  Identical to the antigravity arm (same code shape, same comment).

`hub/internal/drivers/local_log_tail/claude_code/hooks.go`:

- `hookStop` now emits **both** the existing
  `system{subtype:turn_complete}` (kept for telemetry consumers that
  already key off it) AND a `turn.result{reason:end_of_turn,
  status:success}` event (with the same `final_message` +
  `permission_mode`). Mobile `agent_feed.dart:_isAgentBusy()`
  explicitly skips `system` frames; `turn.result` is the contract.

### Test additions

- `TestLaunchM4LocalLogTail_PrefixesCmdWithCdWorkdir` — locks the
  cmd-prefix invariant (recommended-against silent regression).
- `TestLaunchM4LocalLogTail_WritesContextFiles` — locks
  CLAUDE.md materialisation.
- `TestPreTrustWorkspaceClaudeCode_FreshFile` +
  `_PreservesOtherKeys` + `_AlreadyTrusted_NoMutation` — three-way
  cover of the trust function (boundary cases + idempotency).
- `TestOnHook_StopEmitsTurnResultForBusyWalker` — locks the
  turn.result emission alongside the pre-existing `turn_complete`
  test which still passes.

### Symptom #5 in the strict sense ("paste-buffer -r")

The `-r` flag specifically does NOT apply to claude-code: its sendkeys
path uses `tmux send-keys -l` (literal mode) per line, no paste-buffer
involved. **However** the same family has a different bug:
`claude_code/sendkeys.go:84-99` splits multi-line bodies on `\n` and
sends `send-keys Enter` between every line — each Enter submits a
**separate turn** to claude's TUI. Real multi-line input arrives as N
mini-turns instead of one. Flagged for a follow-up wedge; not part of
v1.0.657.

### Tag

- Tag: `v1.0.657-alpha`
- Built: hub-server + host-runner

## v1.0.656-alpha — 2026-05-23

ADR-035 W11 fix-up wedge #12 — agy's MCP `tools/call` STILL failed
after v1.0.654's dot-name filter. Different error, same root family
(strict-client rejection on protocol non-compliance). Caught from
agytest's stderr log: agy sends `notifications/roots/list_changed`
to every connected MCP server, but our `/mcp/<token>` endpoint
returned a JSON-RPC error response for it.

### Root cause: JSON-RPC 2.0 §4.1 violation

JSON-RPC 2.0 §4.1: *"The Server MUST NOT reply to a Notification."*
A notification is a request without an `id` field. Our hub's MCP
handler had a default-case error path that wrote an error frame for
every unknown method — including notifications. The flow:

1. agy spawns hub-mcp-bridge subprocess, sends `initialize` (works)
2. agy sends `notifications/initialized` (works — explicitly handled)
3. agy sends `tools/list` (works after v1.0.654 dot-name filter)
4. **agy sends `notifications/roots/list_changed`** — this is the
   trigger. The hub falls through to the default `method not found`
   error and writes a JSON-RPC error frame back through the bridge
   to agy's stdin.
5. agy receives an unsolicited error frame (no request was
   outstanding) → MCP client treats this as a protocol violation
   → closes the stdio transport.
6. Subsequent `tools/call` from the LLM hits a closed transport →
   `connection closed: calling "tools/call": client is closing:
   invalid request`.

The same hub also failed any other notification method we didn't
explicitly enumerate (`notifications/cancelled`,
`notifications/progress`, etc.) — `notifications/initialized` only
worked because it had its own case branch.

### The smoking gun

Two pieces of evidence aligned:

1. **agytest's stderr log** captured the exact frame agy sends:
   ```
   IN {"jsonrpc":"2.0","method":"notifications/roots/list_changed","params":{}}
   ```
   No `id`, periodic, sent to every MCP server. agytest (permissive
   Python parser) silently ignores it. Our hub didn't.

2. **Manual probe against the deployed bridge** confirmed:
   ```
   $ echo '{"jsonrpc":"2.0","method":"notifications/roots/list_changed"}' | hub-mcp-bridge
   {"jsonrpc":"2.0","error":{"code":-32601,"message":"method not found: ..."}}
   ```
   An error frame on stdout where the spec requires silence.

### Fix

Insert a notification gate BEFORE the per-method switch in
`server/mcp.go`:

```go
isNotification := len(req.ID) == 0 || string(req.ID) == "null"
if isNotification {
    w.WriteHeader(http.StatusNoContent)
    return
}
```

The standalone daemon path (`hubmcpserver/run.go`) already handles
this correctly — only the in-process `/mcp/<token>` route was buggy.

Lock test: `TestMCP_NotificationsGetNoResponse` exercises 5
notifications (including the host-verified
`notifications/roots/list_changed` and an unknown method that must
also produce no body) and asserts each returns 204 with empty body.

### Why agy's diagnosis missed this

agy's three theories from the prior session were all dead ends:

1. *"hub-mcp-bridge writes non-JSON to stdout"* — wrong, manual
   probe showed clean stdout.
2. *"PATH discrepancy / agy can't find the binary"* — wrong, agy
   logs showed the bridge started fine.
3. *"sandbox restricts the bridge's network access"* — wrong,
   manual `tools/call` from a wrapped bridge worked end-to-end.

The actual cause was a wire-level frame agy sent AFTER initialize
that hadn't been considered. Finding it required reading the
agytest server's stderr log (where agy ALSO sent
`notifications/roots/list_changed`) and comparing what agytest's
permissive parser ignored vs what our strict-handler responded
to. Verify-don't-guess discipline + cross-server log comparison
was the load-bearing technique.

### MCP debug arc, summarized

Four wedges, each a distinct layer of the strict-client wall:

| Tag | Bug | What broke |
|---|---|---|
| v1.0.649 | hub hard-coded protocolVersion 2024-11-05; agy sends 2025-11-25 | Negotiation |
| v1.0.653 | workdir .mcp.json pinned stale token | Auth |
| v1.0.654 | catalog held dot-named aliases (spec violation) | Catalog wire shape |
| v1.0.656 (here) | hub replied to JSON-RPC notifications | Protocol-frame discipline |

Each surfaced only after the previous was fixed — strict clients
fail at the first wall they hit, hiding everything behind it.

### Deploy

Builds clean, all tests pass:

```
ok  github.com/termipod/hub/internal/server 107.7s
```

```bash
sudo cp /tmp/hub-server  /usr/local/bin/hub-server
cp     /tmp/host-runner  ~/.local/bin/host-runner
sudo systemctl restart termipod-hub.service
# restart your tmux host-runner
```

Then spawn an antigravity steward, ask "list termipod projects"
— agy should invoke `projects_list` via the bridge and return the
team's projects without "client is closing" errors.

---

## v1.0.655-alpha — 2026-05-23

Three independent fixes from the post-v1.0.654 review — one UX bug
(double-spinner on session mutations) and two antigravity polish
wins (turn-count chip + model name extraction).

### Fix 1: double-spinner on archive / stop / resume

Symptom: tapping Archive, Stop, or Resume on a session flashed a
centered fullscreen spinner TWICE in quick succession before settling.

Root cause: `sessions_provider.dart`'s `build()` used a bare
`ref.watch(hubProvider)`, which subscribes the provider to every
state transition of the hub. `hubProvider.refreshAll()` emits TWO
transitions internally (`HubState.loading=true` then `loading=false`)
between sequential `await`s — and every mutation flow funnels
through `_refreshSessionsAndHub` → `refreshAll`. Each transition
triggered an auto-rebuild of sessionsProvider, briefly putting state
back into `AsyncLoading`, which the screen renders as the centered
spinner. Two transitions → two spinners.

Fix: narrow the watch to a `.select` projection that captures only
the active hub's identity (baseUrl + teamId tuple). refreshAll
doesn't touch config, so the projection returns the same string,
Riverpod skips the rebuild, sessionsProvider stays in AsyncData
throughout the refresh. Login/logout/hub-switch still rebuild (they
DO change config). ~10 LOC in `lib/providers/sessions_provider.dart`.

### Fix 2: antigravity telemetry strip surfaces turn count

`_TelemetryStrip` gated the cost tile on `totalCostUsd > 0`. agy
keeps Gemini `usageMetadata` (token counts, no cost) in memory only
and never persists it, so the gate was false for every agy session.
The strip's other tiles (token totals, rate-limit, context-window)
also all depend on engine-emitted data agy doesn't ship, so the
whole strip then hid via `tiles.isEmpty → SizedBox.shrink()`.

Fix: split the cost-tile guard. When `totalCostUsd > 0` show the
cost+turns tile (claude-code shape, unchanged). When cost is unknown
but `turnCount > 0`, render a "N turns" tile alone with an autorenew
icon — same affordance for agy and for codex (codex's
`turn/completed` notification also doesn't carry cost). ~15 LOC in
`lib/widgets/agent_feed.dart`.

### Fix 3: antigravity model name in AppBar chip

The AppBar `SessionInitChip` reads `payload['model']`, but the
antigravity adapter's `session.init` emits only `{session_id}`. agy
keeps the active model in the `<USER_SETTINGS_CHANGE>` block of
step-0 USER_INPUT in the transcript — that's the ONLY on-disk signal
of which model is answering (just like usage, agy doesn't write it
anywhere else).

Fix in three small pieces:

1. **mapper.go**: USER_INPUT was previously dropped wholesale. Now
   when step 0 contains `<USER_SETTINGS_CHANGE>` with the
   "Model Selection from … to <X>" sentence, parse `<X>` and emit a
   synthetic `session.init` with `{model: <X>}`. Returns nothing for
   USER_INPUT steps without the block (resume / follow-up turns), so
   no event spam. New helper `extractAntigravityModel` + corpus test
   updated.

2. **adapter.go**: stamps the convID onto any mapper-emitted
   session.init missing a `session_id`. Keeps the mapper pure and
   gives mobile a fully-decorated payload (engine + model + sid).

3. **agent_feed.dart**: `_latestSessionInitPayload` now MERGES across
   all session.init events (later fields overwrite, earlier-only
   fields persist). Most engines emit session.init exactly once
   (claude, codex, gemini-cli) so this is a no-op for them; only
   antigravity emits twice (one at conv-id resolution, one at step
   0). Also extends the onSessionInit firing gate to compare
   `sid|model` so the partial later emit refires the parent callback.
   Same merge applied to the backfill path (`_maybeBackfillSessionInit`)
   so cold-loads see the same shape.

After deploy, an antigravity session's AppBar chip will read
"antigravity · Gemini 3.5 Flash (Medium) · 1 turn" (or whatever
model the user selected in agy's TUI) instead of "antigravity ·
(blank) · —".

### Verification + deploy

Builds clean, tests pass:

```
ok  github.com/termipod/hub/internal/drivers/local_log_tail/antigravity 0.896s
ok  github.com/termipod/hub/internal/server 107.717s
ok  github.com/termipod/hub/internal/hostrunner 6.665s
[+ all other packages]
```

```bash
sudo cp /tmp/hub-server  /usr/local/bin/hub-server
cp     /tmp/host-runner  ~/.local/bin/host-runner
sudo systemctl restart termipod-hub.service
# restart your tmux host-runner
# flutter rebuild required for the mobile fixes; CI tag pushes the APK
```

### What's still NOT surfaced for agy

These remain genuinely unavailable because agy never writes them:

- per-model token totals (input/output/cache_read)
- per-call cost
- rate-limit / quota status

The only way to surface those would be to MITM agy's outgoing
HTTPS to Google's Code Assist API, which is not practical. Cleanly
out of scope until agy upstream starts logging.

---

## v1.0.654-alpha — 2026-05-23

ADR-035 W11 fix-up wedge #11 — agy's MCP `tools/call` STILL failed
after v1.0.653 (different error this time, surfaced as
`server name termipod failed to load: failed to get tools: calling
"tools/list": invalid request`). Reproduced manually with the live
token: hub-mcp-bridge → hub's `/mcp/<token>` returned a 167-entry
tools list, but 71 of those entries had dot-separated names
(`documents.list`, `projects.get`, `plans.steps.create`, …).

### Root cause: MCP-spec-noncompliant tool names

MCP spec requires tool names to match `[A-Za-z0-9_-]+` — dots are not
allowed. Our catalog kept 71 dot-named DEPRECATED aliases for
backwards-compat with agents still using the legacy spellings
(ADR-031 / ADR-033 introduced the snake_case canonical names and
left the dot-named ones as aliases on the dispatcher).

Most MCP clients (claude-code, codex, kimi-code) accept the
non-compliant names silently. **agy 1.0.1 validates strictly and
rejects the WHOLE `tools/list` payload with `invalid request` when
any entry violates the regex** — so agy sees zero tools, marks the
server as `failed to load`, and every `call_mcp_tool` errors out.
`agytest` (a user-owned test server) kept working because its only
tool is `ping`.

### Fix

`tools/list` now filters MCP-spec-noncompliant names off the wire
in both handler paths:

- `server/mcp.go` `mcpToolListDefs` (the in-process `/mcp/<token>`
  surface the bridge talks to)
- `hubmcpserver/run.go` `tools/list` (the standalone daemon path)

The dispatcher still accepts both spellings on `tools/call`, so
legacy callers don't break — only the catalog is filtered.

Verified by manual probe: every one of the 71 dropped dot-named
tools has a snake_case sibling already in the wire output (e.g.
`documents.list` → `documents_list`, `plans.steps.create` →
`plan_steps_create`), so zero functionality is lost.

Lock tests:
- `TestMCP_ToolListDefs_FiltersDotNamedAliases` asserts the 8
  named samples are absent + their snake_case siblings present.
- `TestIsMCPCompliantToolName` exercises the regex matcher.
- `TestMCP_ToolListDefs_ServesShort` updated to expect
  `len(defs) < len(full)` (was `==`).
- `TestMCPAuthority_RoundTrip` updated: tools/list now checks
  snake_case names + asserts dot-named are filtered; tools/call
  still passes the dot-named alias to prove the dispatcher
  resolves both.
- `TestToolsList_RoundTrip` (hubmcpserver) updated count expectation.

### On the multi-wedge MCP debug arc

This is the third MCP-related fix in three wedges, each one a
different layer:

- **v1.0.649**: hub `initialize` hard-coded `protocolVersion:
  2024-11-05` → agy 1.0.1 sends 2025-11-25 → fatal protocol error
  on connect.
- **v1.0.653**: workdir `.mcp.json` pinned a stale per-spawn MCP
  token because the launch path only wrote the global config →
  401 invalid token on every `tools/call`.
- **v1.0.654 (here)**: catalog held dot-named aliases → agy
  rejected the whole `tools/list` batch as `invalid request`.

Each one surfaced only after the previous one was fixed — strict
clients fail at the first wall they hit, hiding everything behind
it. The sequence is the kind of error-cascade-discipline lesson
worth distilling: when a permissive validator obscures multiple
nested errors, fix-and-retry surfaces them one at a time.

### Verification + deploy

Builds clean, tests pass. After deploying v1.0.654:

```bash
sudo cp /tmp/hub-server  /usr/local/bin/hub-server
cp     /tmp/host-runner  ~/.local/bin/host-runner
sudo systemctl restart termipod-hub.service
# restart your tmux host-runner
```

Then spawn antigravity and ask "list the projects you can see".
Expected: agy calls `projects_list` natively via MCP, gets the
team's projects, replies with a short list.

---

## v1.0.653-alpha — 2026-05-23

ADR-035 W11 fix-up wedge #10 — agy's MCP `tools/call` was failing on
the post-v1.0.652 smoke with `connection closed: calling "tools/call":
client is closing: invalid request`. User had agy investigate; agy
correctly identified the bridge process was unhappy but mis-attributed
the cause to env-var propagation. Actual root cause traced from the
live `~/.gemini/config/mcp_config.json` + `<workdir>/.mcp.json` pair +
agy's tool_call args.

### Root cause: stale-token persistence

agy 1.0.1 reads MCP server configs from TWO files and merges them,
with WORKDIR winning on same-server-name conflicts:

- GLOBAL — `~/.gemini/config/mcp_config.json`
- WORKDIR — `<cwd>/.mcp.json`

agy auto-syncs the workdir copy from global on FIRST read of a new
workspace, then never re-syncs. v1.0.640..v1.0.652 wrote the global
config (with the fresh per-spawn MCP token) but never wrote the
workdir copy. So a workdir `.mcp.json` from a prior session pinned
the OLD token forever; every tools/call from the new spawn went out
through `hub-mcp-bridge` carrying the dead token, the hub returned
`401 invalid mcp token`, and agy classified the response as `invalid
request → client is closing`.

Observable mismatch from the smoke session:

| File | Token | Notes |
|---|---|---|
| `~/.gemini/config/mcp_config.json` (global) | `firjPlDM…` (fresh, valid) | Written by v1.0.652 launch path. |
| `<workdir>/.mcp.json` (workdir copy) | **`FIrJO6…` (stale, hub returns 401)** | Modtime 12:35 — from the OLD pre-v1.0.652 session. v1.0.652 didn't touch it; agy reads workdir over global → tools/call fails. |

A manual shell test of `hub-mcp-bridge` with the CORRECT token (which
agy ran during its investigation) succeeded — proving the bridge,
hub, and protocol are all healthy. Only the token was wrong.

`agytest` (a user-owned MCP server unrelated to termipod) kept
working throughout because it lives only in the global config — no
workdir entry to go stale.

### Fix

`launch_m4_antigravity.go` now writes the workdir `.mcp.json` in
addition to the global `mcp_config.json` at every spawn, mirroring
M2's pattern for claude-code. `writeMCPConfig` is idempotent and
overwrites any prior copy, so the token stays current across respawns.

Both writes are best-effort: a failure degrades to "no hub MCP" but
the agent still launches. The smoke caught this as a one-way drift
that compounded silently across sessions; the test
`TestWriteMCPConfig_OverwritesStaleToken` locks it.

### Verification

Builds clean, tests pass. After deploying v1.0.653 binaries, the
operator should:

```bash
sudo cp /tmp/hub-server  /usr/local/bin/hub-server
cp     /tmp/host-runner  ~/.local/bin/host-runner
sudo systemctl restart termipod-hub.service
# restart tmux host-runner

# One-time: purge the stale workdir .mcp.json so the next spawn
# writes a clean fresh-token copy (the v1.0.653 writer is idempotent,
# but eyeballing a known-clean state is easier than debugging mid-
# spawn). Optional — the new writer will overwrite it anyway.
rm -f /home/ubuntu/hub-work/antigravity/.mcp.json
```

Then spawn an antigravity steward and ask it to call any termipod
MCP tool (e.g. "list the projects you can see"). Expected: agy
invokes `projects_list` natively via call_mcp_tool and gets back the
team's project list.

### Notes on agy's diagnosis

agy concluded the env-var block in `.mcp.json`'s `"env"` field
wasn't being propagated to the spawned bridge process. That's
WRONG — env-var propagation works fine; the workdir `.mcp.json`
content was correct shape, just with the wrong token value. Agy was
right that the bridge was unhappy and the connection was marked
permanently closed; it just localised one level too high in the
stack. Verify-don't-guess discipline meant we re-checked the actual
config files before applying agy's proposed workaround
(wrapper-script with baked env vars), and the file comparison
surfaced the real culprit.

---

## v1.0.652-alpha — 2026-05-23

ADR-035 W11 fix-up wedge #9 — three independent root-cause fixes from
the post-v1.0.651 on-host smoke. User reported "say hi, then agy did a
lot of unexpected work, almost hit quota limit"; traced from the live
agy session's transcript + agent_events + audit_events. No task was
ever assigned to agy — every one of the 357 steps it ran came from its
OWN self-direction after receiving an empty envelope. Three lined-up
bugs, each load-bearing.

### Fix 1: paste-buffer was splitting the envelope into N submissions

Symptom: agy's transcript shows USER_INPUT step 0 = just
`[directive from the principal]` (header only — no body, no reply
instruction), then 357 steps of self-invented work, then USER_INPUT
step 357 = `hi\nReply in this chat...` (body + footer, no header) five
minutes later. The 4-line ADR-032 envelope arrived as TWO separate
user submissions to agy, split by 5 minutes.

Root cause: tmux's `paste-buffer` defaults to translating LF (`\n`) to
CR (`\r`) on the way to the pane. CR is "Enter pressed" to a TUI, so
each line of a multi-line paste was being submitted as a separate
user input. The first one (`[directive from the principal]` alone)
fired immediately; agy read it as an empty directive, improvised work
for 5 minutes; the remaining lines drained from the kernel TTY buffer
after agy returned to the prompt and landed as the second submission.

Fix: pass `-r` to `paste-buffer` so LF stays as LF (agy's input field
treats LF as a newline character in a multi-line edit, not as submit).
Only the explicit final `send-keys Enter` triggers submission.
`hub/internal/drivers/local_log_tail/antigravity/sendkeys.go`. Lock
test in `adapter_test.go`'s `TestAdapter_TextInput_MultiLineUsesPasteBuffer`
now asserts the `-r` flag is present.

### Fix 2: agy was spawning with NO persona prompt

Symptom: even with the v1.0.649 prompt's "Hard constraints" section,
agy autonomously crawled the workdir, ran `list_dir`, `grep_search`,
`view_file`, `run_command` on its own scratch files, dispatched MCP
tool tests, edited docs autonomously. The persona's guidance never
applied.

Root cause: TWO gaps in lockstep, either of which was sufficient to
break delivery.

(a) `contextFileNameForKind` (server/template.go) had no case for
    `antigravity` — kind=antigravity fell through to the default
    `CLAUDE.md`. But agy reads `GEMINI.md` and `AGENTS.md` (host-
    verified — both strings present in the agy 1.0.1 binary), not
    `CLAUDE.md`. So the hub inlined the rendered prompt under the
    wrong filename for agy to ever open.

(b) `launch_m4_antigravity.go` didn't call `writeContextFiles` at
    all. M1 (launch_m1.go) and M2 (launch_m2.go) launch paths
    materialize `spec.ContextFiles` into the workdir; the M4
    LocalLogTail path did not. So even if (a) had been correct,
    the file would not have landed on disk.

Fix: add `case "antigravity": return "AGENTS.md"` to
`contextFileNameForKind`, AND call `writeContextFiles(workdir,
spec.ContextFiles)` from `launch_m4_antigravity.go` right after
`mkdir -p workdir`. The error path is fatal (matches M1/M2): an agy
session without its persona is a bare-agy session, which is exactly
the "behaves nothing like a steward" outcome the smoke caught.

The same gap likely exists in `launch_m4_locallogtail.go` for
claude-code M4 stewards; deferred as a follow-up (the W11 smoke only
caught the agy case, and claude-code M4 paths may have an alternate
delivery channel via `~/.claude/CLAUDE.md` or settings).

### Fix 3: prompt hardened against "discover what to do" reflex

Added two explicit hard constraints to
`steward.antigravity.v1.md`:

- **Do not crawl your workdir to "discover what to do".** The workdir
  may contain artifacts from prior runs that are NOT instructions —
  scratch space, not a to-do list. Ask the principal, don't guess
  from leftover files.
- **Do not list_dir / grep_search / view_file / run_command in your
  workdir or the source repo as a response to a greeting.** A bare
  "hi" / "what's up" / "status" deserves a one-line acknowledgement
  and a clarifying question — nothing else.
- **Do not advance project phase or status.** That's a
  decision-quality act; the principal owns it.

The second-smoke incident on the seeded research-lit-review demo
project (agy authored a doc section, resolved redlines, and
autonomously advanced phase from 1→2 on a casual "hi") motivates the
phase-advance constraint.

### Verification + redeploy

Builds clean, tests pass. After deploying v1.0.652 binaries to the
box, the operator should also **purge the polluted workdir** —
`sudo rm /home/ubuntu/hub-work/antigravity/*.txt` — so a fresh smoke
starts from a clean state. The .txt files are root-owned SQL-query
dumps from an earlier agy autonomous-investigation cycle; they were
load-bearing context for the work cascade.

### Out-of-scope follow-ups noted

- `launch_m4_locallogtail.go` (claude-code M4) likely has the same
  ContextFiles-not-written gap; untested.
- "client is closing: invalid request" still surfaces on agy's MCP
  `tools/call` after a successful `initialize` — protocol-version
  negotiation works, but something else in the call shape upsets agy.
  Defer until reproducible.
- Workdir-pollution detection at spawn (warn if pre-existing files in
  workdir not owned by the spawn's user) — declined for now; the
  prompt-level fix tells agy to ignore them, and adding a launch-time
  check risks false positives on legitimate resumed-workdir cases.

---

## v1.0.651-alpha — 2026-05-23

**No code change.** Version bump only, to re-trigger the Android release
workflow after v1.0.650-alpha hit the same transient `Bad credentials`
flake from the GitHub API that v1.0.649-alpha did during the "Create
Release" step — the APK and all eight server tarballs built and signed
fine; only the upload to the GitHub release page failed. The PAT
available in this session can't re-run individual jobs via API, and the
Android workflow only fires on tag push, so a fresh version is the
cleanest re-trigger.

Identical artefacts to v1.0.650-alpha. Use this tag instead of v1.0.650
if you need the APK from a GitHub release page (v1.0.650 has the iOS
.ipa attached but is missing the APK + tarballs because of the upload
flake).

---

## v1.0.650-alpha — 2026-05-23

ADR-035 W11 fix-up wedge #8 — three findings from the same post-v1.0.649
review: an `attention_reply` routing gap in the antigravity adapter, an
overview_widget warning storm in the hub logs, and one polish win from
the agy local-log review.

### Fixed

- **antigravity (sendkeys):** new `attention_reply` case in
  `HandleInput`. The hub fans out the principal's `/decide` on a
  request_approval / select / help_request attention as
  `input.attention_reply` to the owning agent; pre-fix the adapter
  rejected it with "unsupported input kind" (visible as a host-runner
  WARN `input dispatch failed ... err="antigravity adapter: unsupported
  input kind \"attention_reply\""`), the input router posted a failure
  `system` event, and the agent never saw the decision. Now: an inlined
  `formatAttentionReplyText` (mirror of the hostrunner-package version
  in `driver_stdio.go`; duplication noted, future
  `internal/drivers/attentionreply` refactor flagged) renders the
  structured payload into a humanised line ("Approved.", "Picked: foo",
  "[reply to approval_request 01k…] Approved. Reason: …") and feeds it
  through `inputText` so it reaches agy as a normal text turn. Two lock
  tests (`RoutesAsText` against the exact W11 payload shape,
  `EmptyPayloadErrors`).

- **hub server — overview_widget warning storm**
  (`hub/internal/server/init.go`):
  pre-fix the template walker logged `unknown overview_widget` on every
  walk, and the walker re-ran on every list-projects / list-templates /
  project-detail call. Result: same warning per stale (template,widget)
  pair fired multiple times per minute, drowning the rest of the log.
  New `warnOverviewWidgetOnce(template, widget)` helper de-duplicates
  by `(template, widget)` so the first stale value still surfaces but
  subsequent walks stay quiet. Resets on process restart (intentional —
  a deploy that fixes the validator should re-confirm). Two lock tests
  (`DedupesByPair`, `DistinctKeysEachWarn`). The user's `<dataRoot>/team/
  templates/projects/ablation-sweep.yaml` and `benchmark-comparison.yaml`
  reference `sweep_compare` (retired in v1.0.506) — the warning still
  surfaces once on startup to remind the operator.

- **antigravity mapper — surface agy's humanised intent strings**
  (`hub/internal/drivers/local_log_tail/antigravity/mapper.go`):
  every agy `PLANNER_RESPONSE.tool_calls[].args` carries `toolAction`
  ("Querying matching attentions from database") and `toolSummary`
  ("Grep search") strings that describe the model's intent in plain
  English. Pre-fix mobile saw only the raw tool name + arg blob
  (`grep_search({"Query":"foo","SearchPath":"/x"})`); now the mapper
  lifts both strings to top-level payload fields (`tool_action`,
  `tool_summary`) so a mobile tool_call card can render them as a
  subtitle. Additive on engines that don't emit them. Two lock tests
  (`SurfacesAgyActionStrings`, `NoActionStringsAbsent`).

### Verified

- All 20 hub packages green; `go vet ./...` clean.
- 6 new lock tests across the changes.

### Out-of-scope follow-ups noted from the log review

- **agy emits two tool names for the same operation**: native
  `Bash` (Title-Case, in-engine) vs MCP `run_command` (snake_case, our
  bridge). Same shell, two cards. Worth a normalisation map in a future
  wedge.
- **`list_dir` vs `list_directory` name mismatch**: the tool_call
  emits `list_dir` (from `tc.Name`), the tool_result emits
  `list_directory` (from `strings.ToLower(ln.Type)`). Mobile pairs by
  `tool_use_id` so rendering is fine, but the name disagreement is
  noisy in audit reads. Map agy's short forms to canonical Type names
  in a future wedge.
- **`replace_file_content` / `write_to_file`**: agy's native file-write
  tools execute under `--dangerously-skip-permissions`. The v1.0.649
  prompt constraints now forbid writes outside workdir, but a future
  wedge could surface these calls with a distinct mobile card
  affordance (red border, "review writes" link) so the principal
  spot-checks them.

---

## v1.0.649-alpha — 2026-05-23

ADR-035 W11 fix-up wedge #7 — four findings from one smoke pass,
all bundled. The W11 transcript and audit log were the source of
every fix: agy's own diagnostics, not guesswork.

### Fixed

- **MCP — protocol version negotiation** (`hub/internal/server/mcp.go`,
  `hub/internal/hubmcpserver/run.go`,
  `hub/internal/hostrunner/mcp_gateway.go`):
  the three MCP `initialize` handlers hard-coded
  `protocolVersion: 2024-11-05` in the response, ignoring whatever the
  client requested. agy 1.0.1 sends `protocolVersion: 2025-11-25` and
  treats a downgrade in the ack as a fatal protocol error — the
  exact "connection closed: calling \"tools/call\": client is closing:
  invalid request" we saw on every termipod MCP call in the W11
  smoke (seq 6/8 in the new agent's event stream). New
  `negotiate(MCP)ProtocolVersion` helper in each handler: echo back
  the requested version when it's in the supported set
  (`2024-11-05`, `2025-03-26`, `2025-06-18`, `2025-11-25`), fall back
  to default otherwise. Empty input → default. Three lock tests
  (`EchoesKnown` / `UnknownFallsBack` / `EmptyFallsBack`). This is
  the high-leverage fix: with MCP working, agy uses the proper
  termipod tool surface (`documents_list`, `projects_list`, etc.)
  instead of falling back to raw bash + curl + git, which is what
  gave it the autonomy to crawl the repo and edit content.

- **antigravity mapper — `is_error` propagation**
  (`hub/internal/drivers/local_log_tail/antigravity/mapper.go`):
  the default-case `tool_result` emitter hard-coded `is_error: false`
  regardless of agy's `status` field. agy sets `status=ERROR` on tool
  failures (MCP errors, permission-denied, etc.), but our mapper was
  hiding those from mobile — the tool_result card renders errors in
  red and folds them into the parent tool_call card; without
  `is_error=true` the failure looked like success. Now propagates:
  `status=ERROR → is_error=true`. Two lock tests
  (`ErrorStatusPropagatesIsError` / `DoneStatusKeepsIsErrorFalse`).

- **steward.antigravity.v1 prompt — over-reach guard**
  (`hub/templates/prompts/steward.antigravity.v1.md`):
  the W11 smoke had agy autonomously author a document section,
  resolve 4 attention items (including a `revision_requested` and a
  `project_steward_request` meant for the principal), and ratify a
  deliverable — all from a casual "hi". Two prompt additions:
  (a) "Default is ask, then act — not act, then report" in the style
  guidance, telling the steward to reply briefly to ambiguous
  directives instead of starting an investigation;
  (b) a new "Hard constraints — what NOT to do without explicit
  direction" section explicitly forbidding writes outside the
  workdir, mutations on project content, auto-resolution of
  principal-only attention kinds, and "completing the lifecycle" of
  seeded demo projects.

- **input envelope — softer reply instruction**
  (`hub/internal/hostrunner/input_envelope.go`):
  the per-message trailer "Reply in this chat when you have a result."
  framed every directive as needing a substantive result, which
  combined with the prompt encouraged investigation on greetings.
  Reworded to "Reply in this chat. Match the response to the ask —
  a brief acknowledgement is fine when the directive isn't a task."
  Still satisfies the existing envelope-render tests (they check for
  the "Reply in this chat" substring).

### Q2 — what is the idle detector for? (verified)

NOT related to ADR-034 directive loop closure. Two distinct
mechanisms at different layers:
- **Idle detector** (`hub/internal/hostrunner/idle.go`) — pane scrape
  + regex over the bottom 5 lines, raises `attention_items{kind:"idle"}`
  when a tmux pane hasn't changed for the threshold AND the tail
  matches a "waiting for confirmation" pattern (y/N, `password:`,
  bare prompt chars). Originally for legacy PaneDriver agents whose
  CLI might halt on an interactive prompt. v1.0.648 already gated it
  off for engines registered in `agentfamilies` (claude-code, codex,
  gemini-cli, kimi-code, antigravity) which report busy/idle via
  events.
- **Loop closure** (`hub/internal/server/loop_hooks.go`, ADR-034) —
  hub-side runtime that watches directive → terminal-event matching
  per loop entity; raises `attention_items{kind:"idle_at_loop_close"}`
  (and runs `loop-hooks.yaml` actions) when a directive sits without
  a terminal event past its deadline. v1.0.647's antigravity
  `turn.result` emission feeds this surface.

### Verified

- All 20 hub packages green; `go vet ./...` clean.
- 8 new lock tests across the changes.

### Out of scope (user to action)

- The seed-demo lifecycle project agy touched (`research-lit-review-demo`
  — section authored, redlines resolved, deliverable ratified) is now
  in a non-pristine state. To restore: `hub-server seed-demo --shape
  lifecycle -reset`.

---

## v1.0.648-alpha — 2026-05-23

ADR-035 W11 fix-up wedge #6 — two on-host smoke findings cleared up
together. (1) The host-runner's idle detector
(`hub/internal/hostrunner/idle.go`) was a regex-based fallback for
engines without structured state, but its prompt regex
(`^[?>$#%]\s*$` among others) false-positives on modern agentic TUIs
whose `>` input prompt is ALWAYS visible. The W11 smoke saw "agent
idle at prompt: >" attention items land every 30 min for antigravity
even though agy was behaving normally. (2) Mobile rendered every
non-`select`/non-`help_request`/non-`project_steward_request`
attention item with an Approve/Reject pair — fine for actual decisions
the principal owes the agent, wrong for informational kinds like
`idle` that are state notices the principal just wants to clear.

### Fixed

- **host-runner (idle detector):** `tickIdle` now skips agents whose
  `Kind` matches a registered agent family (claude-code, codex,
  gemini-cli, kimi-code, antigravity). Those drivers emit explicit
  busy/idle signals through lifecycle / turn.result / completion
  events — the regex scrape isn't needed and false-positives on their
  always-on chat prompt. The detector remains for legacy/unknown
  agents PaneDriver runs without structured state. New
  `hasStructuredDriver(kind)` helper in `idle.go` (look-up against
  `agentfamilies.ByName`); three new lock tests
  (`KnownEnginesSkipped`, `UnknownKindStillScanned`,
  `EmptyKindSkipped`).
- **mobile (Me / attention detail):** `_isInformational(kind)` switch
  in `InlineApprovalActions` (`lib/screens/me/inline_actions.dart`).
  For `kind: idle` (and future system-notice kinds) the card / detail
  page now renders a single "Dismiss" button instead of Approve /
  Reject. Dismiss still routes through `/decide` with
  `decision='approve'` so the audit trail records a clean
  resolution.
- **on-host cleanup:** the two stale `agent idle at prompt` attention
  items the v1.0.647-and-earlier detector raised on the smoke box
  were resolved directly so the Me page is clean after redeploy.
- **revert:** agy edited
  `docs/decisions/035-antigravity-engine-m4-locallogtail.md` and
  `docs/plans/antigravity-engine-rollout.md` on its own during the
  W11 smoke, flipping the ADR to Accepted and the plan to Completed
  — factually wrong (W11 hasn't passed end-to-end). Reverted to
  HEAD. Why it happened: the ADR-032 envelope renderer wraps "hi"
  as `[directive from the principal] / hi / Reply in this chat...`
  → the steward prompt says "Act decisively on ratified ones" →
  `--dangerously-skip-permissions` + cwd in a git-tracked repo. The
  combination gives the agent a lot of latitude. Separate follow-up
  (likely a prompt tightening + a `read-only` mode flag for early
  smoke runs).

### Open follow-up (not landed)

- **MCP bridge `client is closing: invalid request`:** every
  `termipod/<tool>` call from agy returned that error during the
  W11 smoke (seq 6/8 in the new agent's event stream). agy then
  fell back to direct filesystem access (bash, find, grep, git) —
  which is also what gave it the autonomy to edit docs. This is the
  most impactful follow-up: with MCP working agy would have used
  `documents_list` / `projects_list` instead of crawling the repo.
  Separate from this wedge.

### Verified

- All 20 hub packages green; `go vet ./...` clean.
- lint-docs + lint-templates clean.

---

## v1.0.647-alpha — 2026-05-23

ADR-035 W11 fix-up wedge #5: a structural busy-state bug exposed by
the v1.0.646 smoke. Mobile's `_isAgentBusy()`
(`lib/widgets/agent_feed.dart:1298`) scans the latest non-`system`
event and decides idle on `turn.result` / `completion` /
`session.init` / `lifecycle.exited|stopped`; everything else (text,
tool_call, tool_result) is "still working." The antigravity mapper
never emitted any of the terminal kinds, so once any agent event
landed the cancel button stayed on forever — even when agy was
genuinely idle waiting for the next user message. The user couldn't
send a follow-up because the composer's send button is replaced by
the cancel button when busy (`agent_compose.dart:759-774`), which is
the correct gate — but the busy signal was wrong.

The fix is mapper-side. agy's transcript marks turn end with a
`PLANNER_RESPONSE` step that has `status=DONE`, empty `tool_calls`,
and non-empty `content` (the model's final text answer; the next
step is `USER_INPUT`). The mapper now emits `text` followed by
`turn.result {reason:"end_of_turn"}` for that shape. Streaming
intermediate `status=RUNNING` placeholders DO NOT emit `turn.result`
— that's reserved for the DONE finalisation, so the marker lands
once per real turn.

### Fixed

- **antigravity (mapper):** synthetic `turn.result` event on
  end-of-turn `PLANNER_RESPONSE` so mobile's busy-state ladder has
  the terminal marker it expects. The text and turn.result pair
  carries the same `agy_step_index` / `agy_status` keys for
  downstream coalescing, and the latter has
  `payload.reason = "end_of_turn"` for forensic clarity
  (`hub/internal/drivers/local_log_tail/antigravity/mapper.go`).

### Verified

- `TestMapStep_PlannerFinalText_EmitsTurnResult` locks the exact
  shape (text + turn.result) for the W11 transcript's step 147.
- `TestMapStep_PlannerStreaming_NoTurnResult` confirms RUNNING
  intermediates DO NOT emit the marker.
- `TestMapStep_Corpus` updated to expect the new pair on the
  trailing PLANNER_RESPONSE with content.
- All 20 hub packages green, 0 fails; `go vet ./...` clean.

### Notes

Send-keys gating itself was already correct: when `isAgentBusy=true`,
the send button is replaced by the cancel icon, so the UI can't
submit text through the normal path. The bug was purely the busy
SIGNAL — fixed it, gating now works as designed. No host-runner
defense-in-depth check needed (mobile UI is the sole gate; if a
future bug bypasses it the worst case is a stray `tmux send-keys` to
agy, which agy can ignore or queue — not a corruption risk).

---

## v1.0.646-alpha — 2026-05-23

ADR-035 W11 fix-up wedge #4 — two on-host bugs surfaced after v1.0.645
shipped, both rooted in the same misdiagnosis: I claimed agy stops
writing `last_conversations.json` for project workdirs. That was wrong.
agy writes it **lazily, on graceful exit**, so the cache was stale
during my mid-conversation probes but landed correctly later. The
v1.0.645 resolver kept the cache lookup as a secondary signal — which
on the next smoke proved fatal: a fresh spawn that fired *after* the
prior agy exit flushed the cache mis-resolved to the OLD conversation
id, posted `session.init` with it, captured engine_session_id on the
new session row, and the reader opened the existing 293 KB transcript
and re-emitted every step (its `emitted` map is in-memory, fresh per
process). Mobile saw the entire prior conversation replayed onto a
brand-new session with the cancel button stuck on "busy".

Plus a third bug — caught in the same pane capture — where the ADR-032
envelope renderer's three-line text turn was being sent line-by-line
via `send-keys -l + Enter`, which agy's TUI counted as separate user
submissions. The first line (`[directive from the principal]`) hit
during agy's startup race and got swallowed; "hi" became step 0 of the
transcript but never appeared in the pane prompt's echo.

### Fixed

- **antigravity (pathresolver):** dropped the `last_conversations.json`
  fallback in `WaitForConversation`. Brain-dir-since-launch is now the
  ONLY signal — a stale cache entry from a prior agy exit can no
  longer mis-resolve a fresh spawn. The function still accepts
  `workdir` (used in the error message) but no longer reads it; the
  signature is preserved for callers. The legacy ConversationIDForWorkdir
  helper remains as a public read-only inspector for diagnostics but
  is not on the resolution path. Regression-locked by
  `TestWaitForConversation_StaleCacheMustNotResolve` (the exact W11
  scenario: stale cache + pre-launch brain dir + must time out instead
  of mis-resolving).
- **antigravity (Reader):** new `SkipExisting` field. On resume paths
  (the adapter's `wasResume = a.ConversationID != ""` — set when the
  launch glue read `--conversation <id>` off backend.cmd via the
  engine_session_id resume cursor), the reader drains the
  currently-present transcript into its `emitted` map without sending
  anything downstream, then incrementally polls for genuinely new
  steps. Fresh spawns keep the default behaviour (emit every step
  including the first USER_INPUT). Locked by
  `TestReader_SkipExisting_DoesNotReplayHistory` +
  `TestReader_DefaultEmitsExisting`.
- **antigravity (sendkeys):** multi-line text input now goes via
  `tmux set-buffer + paste-buffer + Enter` — ONE atomic submission to
  the TUI instead of the prior line-by-line `-l + Enter` loop that
  fragmented the ADR-032 envelope's three-line text turn into separate
  user messages. Single-line short bodies keep the cheap `send-keys
  -l` fast path. Buffer name derived from PaneID so concurrent inputs
  to different agents can't clobber each other. Locked by
  `TestAdapter_TextInput_MultiLineUsesPasteBuffer` (exact W11 envelope
  body) + `TestAdapter_TextInput_SingleLineUsesSendKeys`.

### Verification

- All 20 hub packages green, 0 fails; `go vet ./...` clean.
- Five new lock tests added; existing
  `TestAdapter_StartResolvesAndPosts` updated to mimic the real flow
  (brain dir + transcript appear AFTER Start, in response to the user's
  first message).
- Stale entry in the on-host `last_conversations.json` cleared so the
  next on-host spawn won't hit pre-fix state.

### Notes

ADR-035 still Proposed. The misdiagnosis in v1.0.645 is on me; the
cache lookup added complexity without reliability gain, and the W11
smoke exposed it the next day. The pre-trust-workspace from v1.0.644
remains correct (different mechanism). Concurrent-sub-second-spawn
disambiguation via transcript-first-step content still deferred — the
brain-dir mtime threshold is sufficient for typical use.

---

## v1.0.645-alpha — 2026-05-23

ADR-035 W11 fix-up wedge #3 — third smoke pass uncovered the **mobile
feed shows nothing even when agy is responding**. Root cause: agy no
longer writes `~/.gemini/antigravity-cli/cache/last_conversations.json`
for workdirs that have been promoted to "projects" (which happens the
first time the user accepts the trust dialog — agy creates a project
shadow at `~/.gemini/config/projects/<uuid>.json` and a workspace-local
symlink at `<workdir>/.antigravitycli/<uuid>.json`, then switches off
the cache mechanism for that path). Host-verified: the on-host cache
was last modified 2026-05-22 (yesterday's probes), while the live
conversation `5536eee5-…` was actively writing its transcript with no
corresponding cache entry. Our resolver was polling a file that would
never update for project workdirs → 30-min timeout → silent feed.

### Fixed

- **antigravity (pathresolver):** `WaitForConversation` gains a `since
  time.Time` parameter and a new primary signal — the newest
  `brain/<convId>/` directory whose mtime is strictly after `since`.
  agy creates that dir at the instant it mints a conversation in
  response to the first user message, regardless of which persistence
  mechanism (cache vs. project shadow) it's using on that workdir.
  Reliable for both project and casual workdirs; pre-launch siblings
  on the same host are filtered out by the time threshold. The legacy
  cache lookup remains as the back-compat secondary signal — whichever
  fires first wins on each poll tick. `since` defaults to `time.Now()`
  captured at the top of `resolveAndRun` so it always covers the
  current spawn (`pathresolver.go`, `adapter.go`).
- New `newestBrainSince(homeDir, since) (string, bool)` helper —
  pure-function variant of the prior `NewestBrainFallback` knob, now
  the load-bearing primary path rather than an after-timeout last
  resort.

### Verification

- Three new lock tests in `pathresolver_test.go`:
  `NewBrainDirSinceWinsWithStaleCache` (the W11 smoke scenario:
  yesterday's cache + a pre-launch sibling dir + our spawn lands
  → only our conv id is returned); `LegacyCacheStillWorks` (casual
  workdir without a project shadow); `IgnoresOlderDirs` (the time
  threshold actually filters).
- All 20 hub packages green; `go vet ./...` clean.

### Notes

ADR-035 still Proposed; this is W11 fix-up #3. The cache-write
asymmetry is upstream agy behaviour, not a regression on our side —
documenting it on the ADR (resolver gets a second signal, can't rely
on the cache alone) is the next paperwork wedge once smoke is green
end-to-end. Per-host sub-second concurrent spawn disambiguation (using
transcript-first-step content to cross-check) is deferred — the time
threshold is sufficient for single-user smoke and the ~99% case.

---

## v1.0.644-alpha — 2026-05-23

ADR-035 W11 fix-up wedge #2: suppress agy's "trust this folder?" arrow-
nav dialog at spawn time so a fresh workdir doesn't sit blocked on a
menu the mobile UI has no way to drive yet (no custom keyboard / action
bar / joystick wired up on the principal side). Host-verified that agy
persists trust in `~/.gemini/antigravity-cli/settings.json` under a
`trustedWorkspaces` array of absolute paths — the launch path now
idempotently appends the resolved workdir to that list before exec'ing
`agy`. Menu-driving UX (Q1 from the smoke debrief — action keys panel /
CapturePane menu detector) deliberately deferred until more menus
appear during smoke; ADR-035 §W9 stays deferred.

### Added

- **antigravity (launch):** `preTrustWorkspaceAntigravity(workdir)` in
  `hub/internal/hostrunner/launch_m4_antigravity.go` reads
  `~/.gemini/antigravity-cli/settings.json`, deduplicates against the
  cleaned absolute workdir, appends if missing, writes back atomically.
  Preserves any unrelated keys (`enableTelemetry`, `statusLine`, future
  agy additions). Fresh box (no settings file) creates one with just
  the `trustedWorkspaces` entry. Best-effort: a malformed or unreadable
  settings.json logs and continues — the user gets the dialog once
  rather than blocking the spawn. Three locking tests in
  `launch_m4_antigravity_test.go` (idempotent re-spawn, fresh box, path
  dedup with trailing slash).

### Notes

ADR-035 stays Proposed; this is W11 fix-up #2. Re-test after redeploy:
spawn antigravity in a never-trusted workdir → expect no trust dialog
→ first message reaches agy → conversation mints → transcript tails →
mobile feed populates. If a different menu surfaces (e.g. a tool-call
permission prompt), capture its layout for the W9 detector corpus
before designing the menu-driving UX.

---

## v1.0.643-alpha — 2026-05-23

First on-host smoke of the antigravity engine (ADR-035 W11) caught two
bugs in one cascade: the adapter blocked on a conversationId that agy
mints only **after** the user's first message — but the user can't send
that message from mobile until the agent leaves `pending`, which only
happens once the driver returns. Adapter timed out at 60s; runner then
silently fell back to PaneDriver, which spawned a SECOND tmux pane in
the host-runner's cwd (no `cd <workdir>` prefix in the fallback path),
scraped raw TUI bytes into the mobile feed, and left the session bound
to the wrong driver — "busy" cancel button, input gated. Both fixed.

### Fixed

- **antigravity (M4 adapter):** `Adapter.Start` is now async — it
  validates, sets up the cancel context, kicks off the
  resolver+reader pipeline in a goroutine, and returns immediately.
  The agent flips `pending → running` without waiting for agy's first
  model turn; mobile input flows via `tmux send-keys` (which only
  needs PaneID, not ConversationID); the user's first message drives
  agy to mint the conversation; the resolver picks it up and the
  transcript tail spins up. Default `SessionWaitTimeout` bumped from
  60s to 30 min — with async resolution this just governs the
  background goroutine's give-up window, and 60s was failing every
  interactive smoke that wasn't already typing. Async-pipeline
  failures post a `system` notice ("antigravity transcript tail
  unavailable") instead of vanishing silently
  (`hub/internal/drivers/local_log_tail/antigravity/adapter.go`).
- **antigravity (runner):** dropped the silent PaneDriver fall-through
  on launch failure. PaneDriver scrapes raw bytes — meaningless for
  agy's TUI — and the fall-through path was reaching the generic M4
  branch that re-launches the raw `backend.cmd` (no `cd <workdir>`
  prefix), spawning a SECOND pane in the host-runner's cwd. On
  `launchM4Antigravity` error the runner now emits `lifecycle.failed`
  with the underlying error and patches the agent to `failed` so
  mobile can offer respawn (`hub/internal/hostrunner/runner.go`).

### Notes

ADR-035 stays Proposed; this is the W11 fix-up pass — ADR flips
Accepted only after a clean end-to-end on-host smoke against
v1.0.643+. The MCP test rig `/tmp/agymcp/server.py` is still wired
into the user's global `~/.gemini/config/mcp_config.json`.

---

## v1.0.642-alpha — 2026-05-23

Two on-device UX papercuts caught while smoke-testing v1.0.641 against
the local hub: pull-to-refresh on the Sessions page flashed a fullscreen
spinner over the existing list (looked like a second refresh on top of
the pull-down one), and the Settings → Data "Clear offline cache" row
was wiping every hub profile's snapshot partition, not just the active
one. Both fixed; mobile-only commit.

### Changed

- **mobile (Sessions):** `SessionsNotifier.refresh()` no longer resets
  the provider to `AsyncLoading` before guarding the next build —
  the previous data stays rendered until the new build resolves and
  swaps in atomically. The `RefreshIndicator`'s native pull-down
  spinner remains the sole visual indicator. Mutations that funnel
  through `_refreshSessionsAndHub` (archive / resume / fork / delete)
  inherit the same silent-swap behaviour
  ([`72a7e83`](https://github.com/physercoe/termipod/commit/72a7e83)).
- **mobile (Settings → Data):** the single "Clear offline cache" row
  splits in two — "Clear cache (this hub)" runs `wipeHub(key)` against
  the active profile's partition only, "Clear cache (all hubs)" keeps
  the prior `wipeAll()` behaviour and now names the scope in the row
  label and confirm dialog. `wipeHub` returns the row count so the
  per-hub variant reports the same "Cleared N entries" SnackBar. Blob
  cache (sha-keyed, shared across hubs) stays untouched by the per-hub
  variant; the all-hubs variant still wipes it. EN + ZH strings updated
  ([`72a7e83`](https://github.com/physercoe/termipod/commit/72a7e83)).

### Notes

No hub-side changes, no schema migration, no ADR motion. v1.0.641's
on-host smoke for the antigravity engine (plan §W11) is still the
outstanding gate before ADR-035 flips Accepted.

---

## v1.0.641-alpha — 2026-05-22

Antigravity (`agy`) lands as the **fifth engine** via M4 LocalLogTail
(ADR-035) — Google retires Gemini CLI 2026-06-18. agy 1.0.1 has no ACP
(M1) and no `--output-format` (M2), so M4 is the only mode. Phase 0 + 1
+ 2 of the rollout shipped; the on-host smoke (W11) gates the ADR's
flip to Accepted.

### Added

- **Antigravity adapter** (`internal/drivers/local_log_tail/antigravity`):
  a conversationId pathresolver (agy's workspace→id cache), a
  **watch-and-diff snapshot reader** (agy rewrites its transcript in
  place keyed by `step_index`, RUNNING→DONE — not an append log, so the
  shared tail-from-offset reader doesn't apply), a mapper locked against
  a real host-captured transcript, send-keys input, and a CapturePane
  mechanism. Composes the existing `LocalLogTailDriver`.
- **Launch path + kind-gate** (`launch_m4_antigravity.go`, `runner.go`):
  agy spawns use the adapter, falling back to PaneDriver on any failure.
  Resume via `agy --conversation <id>` (`spliceAntigravityResume`); the
  adapter posts `session.init` to persist the cursor.
- **`steward.antigravity.v1`** template (M4-only, auto-approve via
  `--dangerously-skip-permissions`, "use termipod dispatch not agy's
  native subagents").
- **Mobile**: antigravity is first-class in the spawn UI — a dedicated
  engine chip in the steward sheet + the worker kind hint.

### Changed

- **Family-level mode floor**: an explicit M1/M2 request for an M4-only
  engine now returns 422 + Hint even on an unprobed host (the host-caps
  fallback is permissive and would otherwise coerce the mode and hang at
  launch).
- agy's MCP config is merged into the **global**
  `~/.gemini/config/mcp_config.json` (a per-spawn HOME would break agy's
  HOME-rooted OAuth); attribution rides the `_meta.conversation_id` hook.

### Deprecated

- **`gemini-cli`** — sunset 2026-06-18 for consumer tiers (enterprise
  licences keep it). Marked deprecated in `agent_families.yaml`; not
  removed. New Google-engine work targets `antigravity`.

## v1.0.640-alpha — 2026-05-20

Second build fix on top of v1.0.639 — the `KeepAliveLink` import
landed, but the `alreadyLive` guard introduced a `notifier.state`
access that Riverpod 3.x flags as `@protected` /
`@visibleForTesting`, and `flutter analyze` promotes the warning to
fatal.

### Fixed

- **Read SSH state via provider, not `notifier.state`.**
  `_connectAndSetup` now reads `ref.read(sshProvider(connId))` for
  the `isConnected` check and `ref.read(sshProvider(connId).notifier)`
  for the `client` accessor — same data, public API surface only. No
  behavioural change vs v1.0.638/v1.0.639.

---

## v1.0.639-alpha — 2026-05-20

Build fix only — v1.0.638-alpha failed CI on a Riverpod 3.x import
that compiled locally but not in the analyzer's strict export check.

### Fixed

- **`KeepAliveLink` import.** In flutter_riverpod 3.x the class was
  moved out of the top-level `flutter_riverpod.dart` export and into
  the `misc.dart` opt-in surface (alongside other low-level types
  like `ProviderBase` and `ProviderListenable`). `ssh_provider.dart`
  now explicitly imports `package:flutter_riverpod/misc.dart` for the
  one symbol it needs. No behavioural change vs v1.0.638. **Note:**
  this tag also fails CI on a separate `notifier.state` warning that
  only became reachable once the import compiled; see v1.0.640.

---

## v1.0.638-alpha — 2026-05-20

> **Note:** This tag failed CI (`KeepAliveLink` import gap). Use
> v1.0.639-alpha, which carries the same feature work plus the
> import fix. The entry below is kept for historical accuracy.

Personal-SSH UX: keep the connection alive while the user navigates
away from the terminal, surface it on the Hosts row, and land directly
on the last viewed session/window/pane on reopen. Mobile-only —
hub binary unchanged in behaviour (the buildinfo version is bumped in
lockstep with the app per the per-release convention).

### Added

- **Live SSH across navigation.** `SshNotifier` grabs a `KeepAliveLink`
  after a successful connect; releasing happens only on explicit
  `disconnect()` (the "Disconnect" item in the terminal overflow menu)
  or app teardown. Popping TerminalScreen no longer drops the socket
  — re-entering reuses the live client and skips the dial + auth
  round-trip. `terminal_screen.dart` adds an `alreadyLive` guard in
  `_connectAndSetup` so the second visit only rebuilds the local
  backend.
- **Live-dot indicator on the Hosts row.** New `_LiveDot` widget
  (`hosts_screen.dart`) — 8px green circle with a soft glow, rendered
  before `_ScopeBadge` on personal-Connection rows when the
  connection is in `activeSshConnectionIdsProvider`. Tooltip:
  *"Connected · disconnect from terminal menu."* Manual disconnect
  remains in the terminal overflow menu (unchanged).
- **`activeSshConnectionIdsProvider`.** New `NotifierProvider<Set<String>>`
  alongside `sshProvider`. `SshNotifier` adds itself on connect and
  removes on disconnect; the Hosts row watches the set rather than
  instantiating every connection's notifier eagerly.
- **Persisted tmux session/window/pane restore on reopen.**
  `_setupTmuxBackend` now consults `activeSessionsProvider` as a
  third source after deep-link and constructor-arg paths. Picks the
  session with the most recent `lastAccessedAt` (when its name is
  still present server-side), then applies `lastWindowIndex` +
  `lastPaneId` with the same `orElse: () => session.windows.first` /
  `() => window.panes.first` fallback the constructor-args branch
  already used. Stale targets degrade silently to "first available."
  Also registers the resolved session via `addOrUpdateSession` so a
  freshly-created session has a row for subsequent `updateLastPane`
  writes to land in.

### Changed

- **Foreground service refcounted by `connectionId`.**
  `SshForegroundTaskService.startService` / `stopService` now require
  a `connectionId` and maintain a `Set<String> _activeConnectionIds`.
  The underlying `FlutterForegroundTask` only stops when the set
  drains — needed because keep-alive lets multiple sockets coexist;
  a global stop-on-any-disconnect would yank the service out from
  under the others. Notification text updates to the most-recent
  connection when a second one comes up.

### Notes

- iOS background time is still bounded by the OS — the foreground
  service is Android-only (`!Platform.isAndroid` short-circuits in
  `startService`). Existing exponential-backoff reconnect handles
  wake-up.
- No new MCP affordance for the steward. Keep-alive only extends the
  lifetime of a user-initiated personal-host connection; a
  `mobile.send_text` / `mobile.tmux_send` intent is still post-
  prototype per `discussions/agent-driven-mobile-ui.md`.

### Tests

- `test/providers/active_ssh_connections_test.dart` (new) — locks
  `activeSshConnectionIdsProvider`'s add / remove / idempotence
  semantics so neither the Hosts row nor any future consumer ends up
  with a stale or duplicated entry.

---

## v1.0.637-alpha — 2026-05-20

Three coordinated boundary fixes uncovered by on-device debugging of
v1.0.636: a spawn validator gap, a workers-can't-finish-tasks gap,
and a handle-naming gap. Same defect class
([validate-at-every-boundary](discussions/validate-at-every-boundary.md))
— names and contracts that drifted between schema, handler, prose,
and storage. All three closed inside one release.

### Added

- **MCP dispatcher schema validator.** New
  `hub/internal/hubmcpserver/schema_validate.go` (~225 LOC, no new
  deps): pure-Go validator covering `type` / `required` / `properties`
  / `enum` / `minimum` / `items` / `minItems`. Wired into both
  dispatchers (`handleToolsCall` for the standalone hub-mcp-server
  stdio path and `dispatchTool` for the in-process `/mcp/{token}`
  HTTP path agents actually hit). Violations come back as
  `isError=true` content (matching how handler errors already
  surface) so an LLM can self-correct on the next turn. Every
  `required[]` array in the 72-tool catalog is now a real
  enforcement boundary instead of documentation. See
  [`docs/reference/glossary.md`](reference/glossary.md) → handle for
  the matched-storage convention.
- **`agents.spawn` host gate.** REST and MCP boundaries both reject
  spawns with empty `host_id`, unknown `host_id`, or offline
  `host_id` with HTTP 422 + a structured `Hint{see_tool:
  "hosts_list"}`. Workers were silently sitting in `pending` forever
  when a caller forgot the field; the schema validator catches the
  missing case at the MCP layer and `checkSpawnHostReachable` covers
  the rest at the REST layer.
- **Migration 0044 — handle normalization.** Strips a single leading
  `@` from `agents.handle` and `a2a_cards.handle` for every existing
  row. New rows ship bare via `normalizeAgentHandle` in `DoSpawn`
  and `handleCreateAgent`. The `@` is a display sigil now — Slack /
  GitHub style — never part of the stored name. (ADR-030 W1's
  pencilled-in migration slot moved from 0044 to 0045 to accommodate.)

### Changed

- **`agents.spawn` schema and description.** `host_id` is now in
  the `required[]` array. The long description was rewritten —
  "Requires `child_handle`, `kind`, `spawn_spec_yaml`, AND `host_id`;
  call `hosts.list` first to discover the available host_ids — even
  on a single-host team, the caller must explicitly name the host
  so a misconfigured fleet fails loudly instead of leaving the agent
  stuck in `pending` with no claimer." The short-form catalog entry
  matches.
- **`tasks_complete` is now `WorkerEligible: true`.** The
  pre-fix `false` contradicted the close-out protocol footer the
  hub bakes into every worker's CLAUDE.md (`renderTaskInstructions`)
  AND every bundled worker template's `default_capabilities` list.
  Workers were forced into `request_help` to finish their own
  assigned tasks. `roles.yaml` worker.allow gets a matching
  `tasks.complete` entry for the legacy fallback path.
- **Role-denial message rewritten.** The pre-fix wording —
  `call request_help(target='@<parent_handle>', question=...)` —
  was wrong on three counts: `request_help` has no `target` (it
  routes to the principal, not the parent), the literal `@<…>`
  template encouraged LLMs to compose double-`@` handles, and
  `target=` is not the param name (a2a.invoke uses `handle=`). New
  text: `call a2a_invoke(handle=<parent.handle>, text=...) to ask
  your parent steward to act or widen the grant; or
  request_help(question=...) to ask the principal directly.`
- **Bundled steward + worker templates aligned to bare-handle.**
  Six prompt templates updated: `steward.research.v1.md` and
  `steward.general.v1.md` lost the `@` from every `child_handle=`
  argument (6 sites total); `briefing/critic/coder/ml-worker/
  paper-writer/lit-reviewer .v1.md` have their `a2a_invoke(target=
  "@…", body=…)` calls rewritten to the correct schema —
  `a2a_invoke(handle="…", text=…)` — and `lit-reviewer.v1.md`'s
  one `request_help(target=…)` call split into the two correct
  shapes (a2a to parent, request_help to principal).
- **A2A handle lookup is now `@@`-tolerant.** `a2a.invoke` strips
  any number of leading `@`s before card lookup, so in-flight
  workers whose persona files still bake in the pre-fix
  `@@steward.xxx` form land on the right card instead of "no A2A
  agent found". Defense in depth — templates SHOULD always pass
  bare.

### Fixed

- **The `@@steward.xxx` "no A2A agent found" failure mode.**
  Root cause: storage convention diverged from the documented
  convention. Templates passed `child_handle="@coder"` literally;
  `DoSpawn` stored `@coder`; worker prompts rendered
  `@{{parent.handle}}` against that and produced `@@coder`; the
  a2a card directory keys on exact match and didn't find it. Fixed
  end-to-end: migration normalizes existing rows, storage strips
  on insert, prompts pass bare, lookup is lenient, glossary
  documents the rule.
- **Worker stuck after `tasks_complete` denial.** The role gate
  flipped to `WorkerEligible: true` (above).
- **Empty `host_id` spawn → agent stuck in `pending` forever.**
  Schema validator + REST host gate (above).
- **`docs/reference/glossary.md`** — handle entry gained a
  paragraph spelling out "stored bare — no `@` prefix" + the
  strip + lenient-lookup machinery, plus the migration 0044
  pointer.
- **`docs/decisions/030-governed-actions-and-propose-verb.md`** and
  **`docs/plans/governed-actions-mvp-rollout.md`** — both updated
  to reflect ADR-030 W1's migration slot moving from 0044 → 0045
  (the 0044 slot was taken by today's handle-normalization
  migration).

Tag `v1.0.637-alpha`.

## v1.0.636-alpha — 2026-05-19

The mobile Admin pane — [ADR-028](decisions/028-host-control-via-tunnel-and-cli.md)
Phase 5, the last phase. The hub owner can now drive the fleet
(shutdown / update / restart / token-rotate / db-vacuum) from the
phone, not just the CLI. ADR-028 is now fully shipped.

### Added

- **Mobile Admin pane (ADR-028 W23).** New `lib/screens/admin/`
  surface, reached from a new **Admin** AppBar action on the Hub
  detail screen — beside the existing "Hub config" button, *not* a
  sixth bottom-nav tab. Three bands: fleet-wide actions, a per-host
  card list (live / version / ping), and a recent-admin-actions audit
  strip. Owner-scope: a member token sees an "owner token required"
  message — the screen surfaces the hub's 403, it does not pre-probe.
- **`ConfirmActionTile` gesture widget (W24).** Every destructive
  admin action requires a deliberate **long-press + slide** — a
  progress fill grows under the tile during the slide; release early
  and nothing fires. A plain tap only shows the gesture hint. Guards
  the fat-finger fleet shutdown.
- **Audit-log query screen (W25).** `admin_audit_screen.dart` —
  filterable cross-team view of `audit_events` by action prefix,
  target kind, time window, and actor handle.
- **Phase 5 admin REST endpoints (W22).** Per-host control routes
  `POST /v1/admin/hosts/{host}/{shutdown,restart,update}`,
  `POST /v1/admin/db/vacuum` (VACUUMs the live database), and
  `GET /v1/admin/audit` (owner-scope cross-team audit query with
  left-anchored action-prefix match). All owner-gated; member tokens
  get 403.
- **Scenario 29** in `test-steward-lifecycle.md` — the mobile-admin
  happy path, the plain-tap guard, offline-host disable, the audit
  trail, and the member-token 403 negative check.

### Changed

- The fleet shutdown/restart and update orchestrators were refactored
  around shared `stopOneHost` / `updateOneHost` helpers, so a per-host
  action produces the identical session-stop + `audit_events` trail as
  the fleet-wide path.

---

## v1.0.635-alpha — 2026-05-19

Fleet ops, the inspect-and-maintain half — [ADR-028](decisions/028-host-control-via-tunnel-and-cli.md)
Phase 4. Nine independent ops subcommands so an operator can diagnose,
inspect, and maintain the fleet without SSHing to a host.

### Added

- **`doctor` preflight (ADR-028 W13 / W21).** `hub-server doctor`
  checks the data root, DB, disk space, and listen address;
  `host-runner doctor` checks HOME, hub reachability, the host token,
  engines on PATH, and the scratch dir. Both print green/red per check
  with a remediation hint, honour `--json`, and exit 1 on any red.
  Commit `e85755d`.
- **`hub-server version [--remote]` (W14).** Prints the release tag +
  git revision; `--remote` fans the new read-side `host.ping` verb
  across the fleet and flags any host whose version differs from the
  hub. Commit `89cc4f6`.
- **`hub-server hosts ls` / `hosts ping` (W15).** Lists the registered
  fleet with heartbeat liveness + runner build info (`--ping` for each
  host's live version); `hosts ping <id>` round-trips the verb.
  Backed by `host.ping` and the owner-gated `GET /v1/admin/hosts` +
  `POST /v1/admin/hosts/{id}/ping`. Commit `89cc4f6`.
- **`hub-server db vacuum` / `db migrate` (W18 / W19).** Offline
  sqlite maintenance — `vacuum` rebuilds the file and reports reclaimed
  space; `migrate` applies pending schema migrations as an explicit
  preflight and reports the version. Commit `db5375f`.
- **`hub-server agents ls` / `agents kill` (W17).** Lists live agents
  fleet-wide and terminates one (`kill <id>`) or all (`kill --all`)
  via the owner-gated `GET /v1/admin/agents` +
  `POST /v1/admin/agents/{id}/kill`. The kill path shares
  `applyAgentTerminationEffects` with the mobile Stop, so the audit
  trail is identical. Commit `04d68d3`.
- **`hub-server logs tail` (W16).** Tails this hub's local journald
  unit — `--lines` / `--follow` / `--unit`. Local-only by design: no
  per-host fan-out. Commit `94ad964`.
- **`hub-server tokens rotate` (W20).** Issues a new host token,
  broadcasts it to every live host via the new `host.token_rotate`
  verb, and revokes the old host tokens — but only once every live
  host has acked, so an un-acked host keeps working (`--force-revoke`
  for recovery). `POST /v1/admin/tokens/rotate`, owner-scope.
  Commit `d14af3b`.

### Changed

- **host-runner persists a rotated bearer token.** The
  `host.token_rotate` verb writes the new token to the host-runner
  state dir (`host-runner.json`); the runner prefers it over the
  `--token` flag on startup, so a rotation survives a restart. The
  in-memory `Client` bearer is swapped under a lock so a rotation also
  takes effect live, without a restart. Commit `d14af3b`.

### Notes

- ADR-028 Phase 4 is complete; Phase 5 (the mobile Admin pane)
  remains. Deferred follow-ups — `serve --no-migrate` and cross-host
  log streaming over the tunnel — are tracked in
  `plans/hub-host-control-cli.md` §7.

---

## v1.0.634-alpha — 2026-05-19

Fleet ops — [ADR-028](decisions/028-host-control-via-tunnel-and-cli.md)
Phases 2 and 3: `self-update`, `update-all`, and `restart-all`. An
operator now moves the whole fleet a version, or bounces it, without
SSHing to each host. (The reconciliation that found Phase 1 had
already shipped in v1.0.611 also flipped ADR-028 → Accepted.)

### Added

- **`self-update` (ADR-028 Phase 2).** New `hub/internal/selfupdate`
  package resolves a GitHub release (explicit `--version` or
  `--channel stable|alpha`), downloads the per-binary tarball, verifies
  it against the release `SHA256SUMS`, and atomically replaces the
  running binary — verify-before-extract, so a bad download never
  lands. `host-runner self-update` and `hub-server self-update` are
  thin wrappers; both exit 75 on success so systemd respawns the new
  binary, exit 1 on failure with the old binary left untouched.
  Commits `b68636e`, `40a2f60`.
- **`host.update` verb + `update-all` orchestrator.** The
  `host.update` control verb runs self-update inside the host-runner
  daemon. `POST /v1/admin/fleet/update` (owner-scope) and the
  `hub-server update-all` CLI fan the verb across every live host,
  then bounce the hub last; flags `--target hosts|hub|both`,
  `--dry-run`, `--upstream-repo`. A host error skips the hub bounce.
  Commits `21aa328`, `c0a3cef`.
- **`restart-all` + `host.restart` verb (ADR-028 Phase 3).**
  `POST /v1/admin/fleet/restart` and the `hub-server restart-all` CLI
  bounce every host-runner — each exits 75, systemd respawns it with
  the *same* binary (clear bad state, no upgrade). Commit `9d4f919`.

### Changed

- **`release.yml` ships per-binary tarballs.** The release pipeline
  now produces eight tarballs per tag — `termipod-{hub-server,
  host-runner}-<tag>-<os>-<arch>.tar.gz`, each a single bare binary —
  plus one `SHA256SUMS` over all eight, so `self-update` fetches only
  the binary it needs. Commit `5b44c31`.

## v1.0.633-alpha — 2026-05-19

Loop-closure configurability — two [ADR-034](decisions/034-orchestration-loop-closure.md)
§7 amendments that make the runtime's enforcement knobs editable
rather than hardcoded.

### Added

- **Per-project loop-closure deadline override.** Migration `0043`
  adds `loop_inactivity_minutes` + `loop_absolute_cap_minutes` to
  `projects` (nullable — `NULL` keeps the hub default). `loopBudgets`
  resolves them, and the sweep applies them everywhere it sets a
  deadline — lazy-stamp, the escalation push, and the per-task
  progress bump (now resolved per task, so a multi-project agent's
  tasks each take their own project's budget). Settable from the
  mobile project-edit sheet and over the `projects.update` REST / MCP
  path. Commit `0e9cae5`.
- **Loop-hooks config disk overlay.** The lifecycle-hook config
  (`PreAgentIdle` / `PostDirectiveOutcome`) was `//go:embed`'d —
  rebuild-only. It now has a disk overlay: `Server.New()` seeds
  `<dataRoot>/loop-hooks.yaml` from the bundled default (never
  overwriting an operator edit) and loads it; SIGHUP hot-reloads it;
  the live config sits in an `atomic.Value` so the sweep never races
  the reload. Commit `357e056`.

## v1.0.632-alpha — 2026-05-19

The **orchestration contract** — [ADR-032](decisions/032-message-routing-envelope.md)
(the message envelope) + [ADR-034](decisions/034-orchestration-loop-closure.md)
(the loop-closure runtime), shipped together as the 10-wedge
[message-routing rollout](plans/message-routing-rollout.md). Replaces
the v1.0.626 / v1.0.630 A2A band-aids with a typed message contract and
a runtime that guarantees a directive's loop reaches an observable
terminal state. Both ADRs stay `Proposed` until on-device verification.

### Added

- **The message envelope (ADR-032).** Every message crossing an agent
  boundary — principal→agent, agent→agent (A2A), system→agent — is a
  structured `{from,to,kind,text,cause,thread}` envelope, composed
  entirely by hub-server and marshaled as the `input.text` payload
  itself. `kind` is a closed four-value enum (`directive` / `question`
  / `report` / `notification`); `composeMessage` is the single
  authoring point. Commits `eb12a09`, `91ef11d`.
- **Driver-side envelope render.** `input_router.go` renders the
  envelope into an unambiguous engine-facing turn — sender, kind, and a
  derived `reply_via` instruction — and drops a self-echo; the drivers
  stay envelope-agnostic. Commit `91ef11d`.
- **The message-admission pipeline.** `validateEnvelope →
  routing-legality → context` runs at the compose boundary, fail-safe,
  `deny > allow` — an agent-declared bad envelope is rejected with an
  ADR-031 hint, a hub-composed one fails fast as a programming error.
  Commit `91ef11d`.
- **The loop-entity data model.** Migration `0042` adds per-hop
  deadline columns, `escalation_state`, and an additive
  `terminal_reason` (`completed` / `failed` / `killed` / `timed_out` /
  `superseded`) to `tasks` + `attention_items`. The loop-entity is a
  role over the two existing tables — no new table; the human-facing
  `status` set is unchanged. Commit `91ef11d`.
- **The loop-closure reconcile sweep.** A periodic hub-server sweep
  reconciles every open loop-entity against its per-hop deadlines: an
  inactivity breach escalates the stall one level up the chain
  (idempotent — never re-fires past the principal), an absolute-cap
  breach terminates the entity `timed_out`. Commit `73a1197`.
- **Orchestration lifecycle hooks.** `PreAgentIdle` re-wakes an agent
  that goes idle while it still owns open loop-entities;
  `PostDirectiveOutcome` flags a bare-relay directive close. Configured
  by bundled YAML. Commit `0dac034`.
- **The directive trace.** `GET /v1/teams/{team}/directives/{task}/trace`
  reconstructs a directive's timeline by walking the parent/cause chain
  — a query, no new event stream; stall escalations carry a `[STALL]`
  marker. Commit `0dac034`.
- **Per-persona orchestration prose.** All 10 main persona prompts
  gained "How messages are addressed" + "Closing the loop" sections.
  Commit `908ba89`.
- **Mobile envelope rendering.** The transcript shows an A2A message's
  sender + kind; closed tasks render `terminal_reason` as additive
  detail alongside the unchanged status. Commit `2a498df`.

### Changed

- **A2A relay provenance travels as structure, not prose.**
  `tunnel_a2a.go`'s `decorateA2ABodyWithSender` became
  `stampA2AEnvelopeMeta` — sender / kind / cause are stamped into the
  A2A body's `message.metadata.termipod` bag, and the recipient
  host-runner composes the envelope. Commit `eb12a09`.
- **`a2a_invoke` gained `kind` + `cause` parameters** so an agent
  declares a message's illocutionary force and lineage explicitly.
  Commit `eb12a09`.

### Removed

- **The v1.0.630 `[A2A from @sender]` text-prefix decoration** — the
  envelope carries provenance as structure now. Commit `eb12a09`.

## v1.0.631-alpha — 2026-05-18

ADR-031 (agent tool ergonomics) — rollout-plan phases 1 + 2, the MVP.
[ADR-031](decisions/031-agent-tool-ergonomics.md) flipped to Accepted.

### Added

- **Two-tier tool catalog (ADR-031 W2.a + W2.b).** `tools/list` now
  serves each tool's one-line `short`; the long body is fetched
  per-tool via `tools_get`. Measured: the long descriptions dropped
  ~50 KB out of the always-loaded catalog. `tools_get` also returns
  the D-1 structured payload — `see_also` discovery pointers and the
  fail-closed `concurrency_safe` / `side_effecting` operational pair,
  populated for all 92 tools. Commits `51f0b8f`, `df09631`.
- **Per-persona `## Tools at a glance` index (W4).** All 14 bundled
  persona prompts gained an intent → tool map — 10 main personas get
  a full table, the 4 per-engine stewards a one-line `tools_get`
  pointer. Commit `08b6972`.
- **Structured recovery hints on 4xx errors (W3).** New `Hint`
  envelope + `writeErrHint`: a `documents_get` / `get_project_doc`
  404 names the sibling tool, a role-gate denial names `request_help`,
  an `agents_spawn` 422 points at `tools_get`. Commit `ce6d58f`.

### Fixed

- **Dangling tool references in bundled persona prompts (W6.a).**
  Prompts cited names that resolve to no tool — an agent calling them
  got `unknown tool`. Clean-renamed across `templates/prompts` +
  `templates/agents`: `documents.read` → `documents_get`,
  `agents.archive` → `agents_terminate`, `runs.register` →
  `runs_create`, `attention.create(kind=…)` → `request_help` /
  `request_select` / `request_approval`. Commit `bc0dd9e`.

## v1.0.630-alpha — 2026-05-18

### Added

- **`documents.get` MCP tool.** The catalog had `documents.list`
  (no body) and `documents.create`, but no way to fetch a single
  doc's full body. Steward in the field tried `get_project_doc`
  (filesystem, 404), `documents_get` (wrong delimiter, no tool),
  `search` (separate bug) — couldn't read back a memo by ULID.
  HTTP endpoint already existed (`handleGetDocument`); only MCP
  catalog entry missing — same defect class as v1.0.591
  (request_project_steward). Commit `095fe6d`.
- **A2A sender attribution in relay body.** Hub-side
  `tunnel_a2a.go handleRelay` now decorates the JSON-RPC
  envelope's text parts with `[A2A from @<sender>]` prefix +
  `To reply: a2a.invoke(handle=..., text=...)` hint when caller
  forwarded bearer. Receiver knows the source + reply mechanism.
  Replaces v1.0.626's body unification (which left no A2A
  attribution). Best-effort: non-message/send methods + unauthed
  peer relays pass through unchanged.

### Changed

- **`get_project_doc` description** rewritten to disambiguate
  filesystem files vs document-table rows by id. Names the
  alternative (`documents.get`) for the wrong-tool case.

## v1.0.629-alpha — 2026-05-18

### Added

- **Mobile archive agent gets Feed tab.** `ArchivedAgentDetailScreen`
  previously showed only Summary + Spawn spec + Journal. Operators
  investigating a failed worker had to bounce out to Me → Sessions
  to read the transcript. Body restructured into
  `DefaultTabController` with Feed (default) / Summary / Journal
  tabs, mirroring the live agent sheet pattern. Reuses
  `AgentFeed(agentId)` widget; `agent_events` rows preserved on
  archive. Commit `7dc0491`.

## v1.0.628-alpha — 2026-05-18

### Fixed

- **Preserve worker's blocked verdict on manual stop.**
  `deriveTaskStatusFromAgent` only protected `cancelled` from
  overwrite. Manual stop on a blocked worker flipped task to
  `cancelled` (no result_summary) or `done` (if any summary),
  erasing the worker's verdict + posting a misleading wake to the
  steward. Skip list extended to `{cancelled, blocked}` — operator
  cleanup is cleanup; worker verdict is task outcome. Commit
  `f308391`.

## v1.0.627-alpha — 2026-05-17

### Added

- **Worker prompts gain "When you're blocked" reflex.** 6 worker
  prompts (briefing/coder/critic/lit-reviewer/ml-worker/paper-writer)
  get identical section: on non-recoverable tool failure,
  `tasks.update(blocked)` + `a2a.invoke(parent_steward)` + stop.
  Explicit that "printing 'blocked' in chat does NOT notify anyone."
- **Steward prompts gain "Validate before delegating" + "Reacting
  to worker outcomes" sections.** 4 main steward prompts (.v1,
  .general, .research, .infra) get capability table (project /
  plan / template / schedule mutations are steward-only — workers
  hit 403) + per-outcome reaction (done → read artifact / blocked
  → handle or reassign / cancelled → reason then proceed).
- Engine-variant stewards (codex/gemini/kimi/claude-m4) NOT
  edited — follow-up if device testing shows the gap matters.

Commit `0ed0f6c`.

## v1.0.626-alpha — 2026-05-17

### Fixed

- **Steward wake never reached engine for task.notify.** v1.0.611
  W5 emitted `input.task_completed` kind to wake the steward after
  worker terminal task transition. InputRouter allowlist accepted
  it — but **no driver had a case for `task_completed`** (every
  driver's switch fell through to `default: unsupported input
  kind`). Card on feed appeared; steward's compose box stayed
  idle. Broken since v1.0.611. Same single-boundary validation
  failure as v1.0.619.
- Corrective fix: unify on `input.text` with canonical `body`
  payload field (every driver's text branch handles). Renamed
  `taskCompletedInputBody` → `taskOutcomeInputBody`; body verb now
  matches `toStatus` (done/blocked/cancelled — pre-bundle always
  said "completed"). Added `TestInputRouter_DispatchesSystemInputText`
  to lock the host-runner dispatch boundary the W5 wedge missed.

Commit `5277f4d`.

## v1.0.625-alpha — 2026-05-17

### Fixed

- **`{{project_id}}` unbound in steward.research.v1.md.** v1.0.622
  prompt rewrite added 5 references in `spawn_spec_yaml` examples;
  the var was never in `buildSpawnVars` map → expanded to ""
  → steward's persona contained literal `project_id: ` (empty).
  Bound from `in.ProjectID`.
- **`{{parent.handle}}` (dotted) unbound in 4 worker prompts.**
  coder/critic/lit-reviewer/paper-writer .v1.md used `@{{parent.handle}}`;
  bound key was `parent_handle` (underscore) only → dotted form
  expanded to "@". Workers never learned their steward's handle.
  Bound both forms.

### Added

- **Layer-4 startup audit `auditBundledTemplateVarRefs`** — scans
  every bundled `templates/agents/*.yaml` + `templates/prompts/*`
  at hub start; refuses to boot if any `{{var}}` reference isn't
  in the canonical allowlist (`boundSpawnVarNames` +
  `boundSpawnVarNamesConditional`). Worker prompts whitelisted via
  `promptAlwaysParented` for parent-context vars.

Commit `ca68d99`.

## v1.0.624-alpha — 2026-05-17

### Fixed

- **`buildSpawnVars` sibling-boundary fix.** A project steward
  called `agents.spawn` with `spawn_spec_yaml: "template: agents.coder"`.
  Worker got "400 API Error … passed --print" as model name.
  `renderSpawnSpec` correctly merged the template ref; the sibling
  `buildSpawnVars` re-read `backendVarsFromSpec(in.SpawnSpec, …)`
  from the **un-merged** input. `{{model}}` and `{{permission_flag}}`
  expanded to "" → cmd became `claude --model --print …` → claude
  CLI consumed `--print` as the model value. Pushed merge into
  `buildSpawnVars` so var extraction sees the same backend block
  `renderSpawnSpec` does.

Commit `91cef92`.

## v1.0.623-alpha — 2026-05-17

### Fixed

- **5 HIGH-severity free-form field validators.** Closes the
  remaining gaps from the v1.0.620 audit — `projects.create`
  `config_yaml` (template requires non-empty `phases:`),
  `documents.create.content_inline` (non-empty after trim),
  `channels.post_event.parts` (non-empty + per-kind required
  payload), `artifacts.create` / `runs.attach_artifact`
  `lineage_json` (must be a JSON object), `projects`
  `policy_overrides_json` (JSON object only). Each rejects
  malformed payloads with HTTP 422 + structured error instead of
  writing them silently and stalling downstream consumers.

### Changed

- **MCP description rewrites** for the validated verbs — shape +
  minimal example + failure mode, per the v1.0.621 hygiene rule.

Commit `544716a`.

## v1.0.622-alpha — 2026-05-17

### Changed

- **Steward prompt spawn examples use the canonical `agents.<name>`
  form.** `steward.research.v1.md`'s 5 spawn examples used a
  `<load …>` placeholder an LLM could short-circuit into
  `template: lit-reviewer.v1` shorthand — which post-v1.0.621
  returns 422. Rewritten to the explicit
  `template: agents.lit-reviewer` form. Prompt-only.

Commit `5617132`.

## v1.0.621-alpha — 2026-05-17

### Changed

- **Agent-template naming formalised.** New reference
  `docs/reference/agent-template-naming.md`: file
  `<basename>.v<N>.yaml` declares internal
  `template: agents.<basename>`; the `agents.` prefix is a
  load-bearing category namespace for string-only contexts.
  `template_audit.go` enforces filename↔internal-id match at hub
  start. The v1.0.620 dual-form lookup band-aid is removed —
  `agents.<basename>` is now the only accepted reference.
- **MCP description hygiene rule.** `agents.spawn` +
  `plans.steps.create` descriptions stripped of version markers
  and `docs/discussions/*` references — the agent only sees
  current behavior and cannot fetch repo files.

Commit `9745e46`.

## v1.0.620-alpha — 2026-05-17

### Fixed

- **Spawn robustness — 10-wedge bundle closing the coder.v1
  incident.** A steward sent `spawn_spec_yaml: "template:
  coder.v1"`; the hub passed it through unchecked, the hostrunner
  fell to the interactive-bash placeholder, the PaneDriver pumped
  the task prompt into bash, and the agent entered an unbounded
  respawn loop. Every layer was permissive; only 1 of 10
  fail-fasted. Fix validates at every boundary: `renderSpawnSpec`
  template merge, `launchOne` respawn-loop dedup, hub `DoSpawn`
  fail-fast on empty `backend.cmd` (HTTP 422), InputRouter
  system-producer allowlist, plus validators on 7 HIGH-severity
  free-form fields. See `discussions/validate-at-every-boundary.md`.

Commit `f420bc9`.

## v1.0.619-alpha — 2026-05-17

### Fixed

- **Abandoned tasks auto-derive to `cancelled`, not `done`.**
  ADR-029 D-3 refined: a `terminated` worker with an empty
  `result_summary` was abandoned, not finished — flipping it to
  `done` was a lie. New rule: terminated + summary → `done`;
  terminated + no summary → `cancelled`; crashed/failed →
  `blocked`. Audit meta carries `abandoned: true`.

### Changed

- **Project-scoped agent history.** The Agents-tab "Archived"
  button is now a project-scoped history view (icon → `history`):
  broadens the filter from archived-only to
  terminated/crashed/failed-or-archived for that project,
  most-recent first, so a freshly terminated worker is visible
  without a separate Archive step.

Commit `cc9a1bd`.

## v1.0.618-alpha — 2026-05-17

### Changed

- **Host-runner logs agent terminate symmetric with spawn.**
  Added INFO lines `stopping agent driver` / `agent driver
  stopped` / `agent terminated` so operators see kill evidence in
  journalctl, matching the existing `agent pane created` spawn
  line. Log volume only; no behavior change.

Commit `9c80843`.

## v1.0.617-alpha — 2026-05-17

### Fixed

- **`permission_mode` default on MCP spawn.** `agents.spawn`'s MCP
  schema lacked `permission_mode`, so MCP-spawned workers got an
  empty `{{permission_flag}}` — claude in `--print` mode then
  denied Write/Edit/Bash and the worker stalled with no attention
  item. `backendVarsFromSpec` now rewrites empty mode → `skip`;
  the schema adds an explicit `permission_mode: {enum: skip,
  prompt}`.

### Added

- **Library reset menu.** New `POST .../templates/reset` and
  `.../agent-families/reset` endpoints + a Library AppBar overflow
  menu let operators pick up fixed bundled templates after a hub
  upgrade (boot-time write is no-overwrite). User-only files
  preserved; custom agent-families deleted (dialog flags this).

Commit `eb78e6a`.

## v1.0.616-alpha — 2026-05-17

### Changed

- **Task detail screen collapsed from seven sections to three.**
  Status×5 + priority×4 chip grids → two compact
  `PopupMenuButton` pickers in a `_StateRow`; `_SourceSection` +
  `_TaskAttributionBlock` + `_LinkedWorkSection` folded into one
  `_AttributionCard`. Fixes the ad_hoc mislabel — spawn-created
  tasks with an assigner now read "assigned by @…" instead of
  "Created manually".

Commit `221c658`.

## v1.0.615-alpha — 2026-05-17

### Fixed

- **Per-engine context file (CLAUDE/AGENTS/GEMINI.md).**
  `resolveContextFiles` hardcoded the rendered prompt into
  `CLAUDE.md` regardless of backend — but only claude-code reads
  CLAUDE.md; codex + kimi-code read AGENTS.md, gemini-cli reads
  GEMINI.md. Every codex/kimi/gemini spawn had shipped a CLAUDE.md
  the engine never opened, so those stewards ran without their
  persona + task body. New `contextFileNameForKind` lookup picks
  the right filename for both the emit and operator-override
  paths. Expect codex/kimi/gemini stewards to suddenly read their
  full prompt.

Commit `f73bd32`.

## v1.0.614-alpha — 2026-05-17

### Fixed

- **Task close-out protocol footer.** A coder.v1 worker received a
  task body saying "do not modify files / create documents /
  spawn agents", followed it literally, and never called
  `tasks.complete` — the task sat `in_progress` forever.
  `renderTaskInstructions` now appends a system-rendered footer
  with the literal `tasks.complete(...)` /
  `tasks.update(status='blocked')` calls and the worker's own IDs
  baked in; footer prose states close-out verbs are orchestration
  protocol, exempt from task-body `TOOLS:` / `BOUNDARIES:`
  restrictions. `ml-worker.v1` gains the close-out capability;
  steward prompts get a BOUNDARIES warning + template-selection
  guidance.

Commit `6c9ce67`.

## v1.0.613-alpha — 2026-05-17

### Fixed

- **A2A notification flipped to the sender side (`a2a.sent`).**
  W2.11 originally pushed `a2a.received` into the receiver's
  session — duplicate content, since the host-runner already
  delivers the body as `input.text producer='a2a'`. Now the
  *sender's* session gets a `kind='a2a.sent' producer='system'`
  event (`→ A2A to @<receiver>: <preview>`) so the sender has an
  in-chat trace of what it dispatched. Receiver unchanged.

Commit `db928d1`.

## v1.0.612-alpha — 2026-05-17

### Fixed

- **`project_steward_request` resolution fans back to the general
  steward.** The `/decide` attention-reply allowlist excluded
  `project_steward_request`, so approving a general steward's
  delegation resolved the attention but inserted no
  `input.attention_reply` event — the general steward parked
  forever. Approve now delivers the spawned project-steward agent
  id (for A2A); reject delivers `decision=reject` + reason.

Commit `5d237e0`.

## v1.0.611-alpha — 2026-05-16

### Added

- **ADR-028 Phase 1 — `hub-server shutdown-all`.** Promotes the
  A2A tunnel into a host RPC bus: `TunnelEnvelope` gains a `kind`
  discriminator (`a2a` | `host.<verb>`); `RunTunnel` routes
  `host.*` through a `HostVerbHandler`; unknown verbs return a
  typed `unknown_verb` envelope. New `host.shutdown` verb logs the
  reason, runs a cleanup pass, and exits 0 (systemd does not
  respawn). `POST /v1/admin/fleet/shutdown` (owner-scope) stops
  every active session on every live host via the new
  `stopSessionInternal` helper — shared with the mobile Stop
  path — then fires the verb; `cmd/hub-server/shutdown-all` is a
  thin client over it. Hub-server stays up. Commit `83170b0`.
- **ADR-029 Phase 1.5 — task delivery + notification edges
  (D-8).** Worker delivery: task body inlined into the CLAUDE.md
  `## Task` section + a `producer='user'` event posted after
  spawn so the worker's first turn fires automatically. Worker
  close-out: `tasks.complete` MCP verb bundling `status='done'` +
  `completed_at` + `result_summary`. Assigner notification:
  `task.notify` system event into the assigner's session on every
  terminal flip; generalised to `run.notify` (run terminal
  transitions) and `a2a.sent` (outbound peer message).
- **ADR-029 Phase 2 — mobile task surfaces.** `_TaskTile` renders
  the triad (assignee chip + status pip, assigner attribution,
  relative time; `cancelled` strikethrough + muted).
  `TaskDetailScreen` gains an attribution block, a linked-work
  section, and a per-action audit timeline. `handleListTasks` /
  `handleGetTask` denormalize assignee/assigner via LEFT JOIN.
  Pull-to-refresh works in the empty state.

Commit `5c26710`.

## v1.0.610-alpha — 2026-05-16

### Added

- **Tasks as first-class primitive (ADR-029 Phase 1).** Closes the
  empty Tasks-tab bug when the project steward spawned a worker.
  - `agents.spawn` (REST + MCP) accepts `task_id` to link to an
    existing task **or** an inline `task: {title, body_md,
    priority, parent_task_id, milestone_id}` object to materialize
    one in the same transaction. Mutual exclusion is a 400; spawn
    against a `done` / `cancelled` task is a 409 with a hint to flip
    to `in_progress` first; `blocked` stays valid (a fresh spawn is
    the canonical unblock path).
  - Inline-create stamps `assignee_id = new agent`,
    `created_by_id = parent_agent_id` (NULL when the caller is
    principal-direct), `status = 'in_progress'`, `started_at = now`.
    `agent_spawns.task_id` carries the linkage.
  - Flip-on-spawn for the linkage path: `todo` / `blocked` /
    unset → `in_progress`, `started_at` stamped if not already.
    `in_progress` is left intact so the most-recent-spawn rule
    still drives lifecycle.
  - `deriveTaskStatusFromAgent` auto-derive on agent terminal
    transition: `terminated` → `done` + `completed_at`,
    `crashed` / `failed` → `blocked`. Most-recent-spawn drives.
    `cancelled` is the sticky terminal override — auto-derive
    never enters or leaves it.
- **`tasks.delete` MCP wrapper + REST handler.** `tier=Routine`,
  steward-only via `roles.yaml` (workers can `tasks.update` but
  not delete). Distinct from `tasks.update status='cancelled'`:
  delete drops the row, cancelled keeps it for the audit trail.
- **`cancelled` task status** added to the documented vocabulary
  (no migration; status column has no CHECK constraint).
  `handlePatchTask` stamps `completed_at` on `done` | `cancelled`.
- **Task audit at six sites** (ADR-029 D-4) with a `source` axis:
  create (`ad_hoc` / `plan` / `spawn`), status-flip
  (`principal` / `steward` / `worker` / `plan_step` / `spawn`),
  non-status update (`changed_fields`), delete, plan-step
  materialise, plan-step sync auto-flip, and the W3 auto-derive.
- **0041 migration** — `agent_spawns.task_id` (FK to `tasks` with
  `ON DELETE SET NULL` + partial index) and `tasks.started_at` /
  `completed_at` / `result_summary`.

### Changed

- **`NoteKind.todo` → `NoteKind.reminder`** (mobile). Resolves the
  on-device collision with the hub-side `tasks.status='todo'`.
  sqflite schema bumped v1 → v2 with `onUpgrade` rebuild-the-table
  + rewrite `todo` → `reminder`. Note editor ChoiceChip label
  "Todo" → "Reminder". `_kindFromString` accepts both for
  defence-in-depth.
- **Glossary** — extended `### task` with the full ADR-029 vocabulary
  (`todo` / `in_progress` / `blocked` / `done` / `cancelled`), the
  auto-derive vs manual-override semantics, and the linkage triad
  pointer; new `### note` entry (device-local, never synced); new
  `### todo` disambiguation pointer (hub-side `task.status='todo'`
  vs the retired `NoteKind.todo`).

### Deferred / pending

- ADR-029 Phase 2 (mobile triad rendering — assignee chip + assigner
  attribution + relative timestamp on the Tasks-tab tile; task
  detail screen surfaces linked spawn/session/audit; LEFT JOIN
  denormalisation in `handleListTasks`; pull-to-refresh) is
  documented in `plans/tasks-first-class-rollout.md` §3 but not
  started.

### Why

- The bug surfaced 2026-05-16: when the project steward spawned a
  worker "for a task," the Tasks tab stayed empty. Schema had
  `assignee_id` + `created_by_id` but no edge from `agent_spawns`,
  no auto-derive on agent lifecycle, no audit at the task surface,
  and an on-device "todo" name collision with the hub primitive.
  Phase 1 closes the four gaps end-to-end on the hub.

---

## v1.0.609-alpha — 2026-05-16

### Fixed

- **Cross-scope session guard** — three-layer fix to the bug where
  asking the general steward to spawn a project worker created a
  phantom "proj steward session" stamped with the general steward's
  history.
  - Mobile `open_steward_session.dart`: when `scopeKind='project'`
    and no live project-bound steward exists, route to
    `showSpawnProjectStewardSheet` instead of creating a cross-scope
    session against the general steward.
  - Hub guard `handlers_sessions.go::handleOpenSession`: reject 400
    when `scope_kind='project'` and `agent.project_id` doesn't
    match `scope_id`. Closes the hole for any REST / MCP caller.
  - Hub `lookupSessionForAgent`: when an agent has multiple live
    sessions, prefer one matching the agent's intrinsic scope.
    Defense-in-depth.
- **Offline host chip on the Hosts screen** — `_ScopeBadge` rows in
  the Hub group now reflect `hub_host.status`: `offline` → red,
  `pending` → warning, `online` → intrinsic green/magenta.
  Personal-only rows stay cyan. Previously all rows showed
  green regardless of status.

### Added

- **Test coverage for the cross-scope guard.**
  `TestSessions_RejectCrossScopeProjectSession` walks four paths
  (NULL `project_id`, mismatched `project_id`, missing `scope_id`,
  happy case). Two existing fork tests were updated — they were
  silently exercising the bug being fixed.

### Why

- The phantom-session report broke the steward → project-steward
  delegation handshake described in ADR-025. The fix layers the
  guard at the mobile, hub, and lookup paths so the same root cause
  can't re-surface via a different entry point.

---

## v1.0.608-alpha — 2026-05-16

### Added

- **Hub-side `a2a.message_sent` audit row.** `/a2a/relay/{host}/{agent}`
  was pure pass-through — engine JSONLs were the only record of who
  messaged whom over A2A. Now writes one row to `audit_events` per
  successful 2xx forward: `action=a2a.message_sent`, target =
  receiving agent, summary truncates the first text part to 200
  chars, meta carries `host_id`, `recv_agent_id`, `recv_agent_handle`,
  `body_bytes`, plus `from_agent_id` / `from_agent_handle` when
  sender attribution is available. Surfaces on the existing audit
  feed (mobile Activity tab, `GET /v1/teams/{team}/audit`).
- **Optional sender attribution on the unauthed relay path.**
  `hubmcpserver/client.go::doAbsolute()` now forwards the caller's
  bearer when the absolute URL points back at our hub baseURL.
  External A2A peers (no bearer) still relay fine — the relay
  remains unauthed per A2A v0.3 spec; the bearer is informational
  attribution only. New `auth.ResolveBearer()` helper does the
  lookup-without-enforce dance for unauthed endpoints.

### Why

- Lifecycle test Scenario 7 used to instruct the operator to inspect
  `audit_events` for an `a2a.message_sent` row that **didn't exist**.
  That mismatch was the audit gap — closed.

---

## v1.0.607-alpha — 2026-05-16

### Fixed

- **Project stewards now visible on the Sessions screen.** Filter at
  `sessions_screen.dart:493` used `isStewardHandle()`, which only
  matches `steward` or `*-steward`. Project stewards spawn with
  handle `@steward.<pid8>` (`handlers_project_steward.go:46`) — those
  fell through. Now also accepts agents whose `kind == 'steward.v1'`
  or `kind.startsWith('steward.')` so general + domain + project
  stewards all surface.

### Added

- **Sessions screen groups stewards by category** with collapsible
  headers: **General steward** / **Project stewards** /
  **Domain stewards** / **Detached sessions**. Each header
  (chevron + label + count) toggles its section. Detached defaults
  to collapsed (history/diagnostic). Collapse state lives on the
  screen so swapping tabs preserves it.

---

## v1.0.606-alpha — 2026-05-16

### Added

- **`a2a.cards.list` MCP tool.** Wraps `GET /v1/teams/{team}/a2a/cards`
  so agents can discover which handles are callable over A2A.
  Optional `handle=` arg scopes to one entry. Worker role manifest
  already allows `*.list`, so no role change needed.
- **`agents.list` gains a `live: true` shortcut** (maps to
  `status IN (running, idle, paused)`) and an `include_terminated`
  flag. The default behavior changes to **hide terminated /
  failed / crashed rows** unless `include_terminated=true` is
  passed. `status=X` still takes precedence. Mobile aligned:
  Budget + Archived screens pass `include_terminated: true` to
  preserve their current behavior; project Agents tab and steward
  overlay inherit the cleaner default.

### Why

- Long-running teams accumulated terminated rows and workers calling
  `agents.list` saw mostly noise. The new defaults + `live` shortcut
  cover the canonical "who could I plausibly talk to right now"
  query without enumerating each state.

---

## v1.0.605-alpha — 2026-05-16

### Fixed

- **Auto-inject `parent_agent_id` on the MCP `agents.spawn` path.**
  The tool's schema lists `parent_agent_id` as optional and no
  template prompts the steward to pass it. Result:
  `agent_spawns.parent_agent_id` landed NULL even though the W9
  gate immediately above already proved who the caller is.
  Downstream broke in two places — `a2a.invoke` denied with -32601
  "a2a target not permitted: workers may only invoke parent steward"
  (`authorizeA2ATarget` resolves caller's parent via this column),
  and `get_parent_thread` returned empty. Fix: in the MCP
  dispatcher, after the W9 gate passes, inject
  `parent_agent_id = callerAgentID` when the caller didn't supply
  one. Caller-supplied wins; principal-token MCP calls
  (`agentID == ""`) skip — those are the bypass path.

### Why

- W9 gate proved who the caller is but didn't enrich downstream
  state — a class of bug similar to v1.0.591's dispatcher × catalog
  symmetry gap (handler present, advertisement missing). Lesson
  added to memory: when a gate proves identity, propagate that
  identity to every consumer that re-derives it.

---

## v1.0.604-alpha — 2026-05-16

### Fixed

- **Mobile new-template starter has `driving_mode` + `fallback_modes`.**
  The agent starter body shipped in v1.0.600 omitted these required
  fields. Any user creating a new agent template from the Library
  → New template sheet got a body the launcher's spawn-mode
  resolver couldn't satisfy; the steward then flagged it at spawn
  time. Aligned to the canonical `coder.v1.yaml` shape:
  `driving_mode: M2`, `fallback_modes: [M4]`. Also dropped
  `default_workdir` so the launcher's per-project auto-derive
  (`~/hub-work/<pid8>/<handle>`, v1.0.595) still applies to
  user-authored templates.
- **`templates.{cat}.get` MCP tool defaults to merged response.**
  Returned the on-disk overlay verbatim before, so a stale
  pre-v1.0.520ish disk copy of e.g. `coder.v1.yaml` would have
  the steward see holes (missing `driving_mode`) when fetching
  via MCP — even though the embedded built-in was complete. Now
  defaults to `merge=1`; new `raw=true` argument restores the
  prior behavior for editor-overwrite paths.

### Audit

- Bundled templates (14 agents, 15 prompts, 1 plan, 3 project
  shapes) audited end-to-end against the current schema. All
  complete; the upstream gaps are the two fixed above.

---

## v1.0.603-alpha — 2026-05-16

### Fixed

- **Pull-to-refresh on the project Agents tab.** Was the only
  sibling tab without a `RefreshIndicator` — Overview / Activity /
  Tasks already had one. Wrap both the empty-state and populated
  branches; empty state uses a `ListView` with
  `AlwaysScrollableScrollPhysics` so the gesture is reachable even
  when no agents exist (common case right after spawning a project
  steward and waiting for the row to land).

---

## v1.0.602-alpha — 2026-05-16

### Added

- **`template_proposal` preview block on `ApprovalDetailScreen`.**
  Closes the "approve sight-unseen" gap — agents could submit
  arbitrary template body via `templates.propose` and the principal
  had no preview before approving. New `_TemplateProposalPreview`
  fetches the proposed body via `downloadBlob(blob_sha256)`,
  fetches the currently-installed template via `getTemplate()`,
  and renders: category/name header with a status chip
  (**NEW** / **revise** / **no change**), `proposed_by` handle,
  rationale, and the proposed YAML in a mono code view.
- **`project_steward_request` inline action on Me-page card.**
  Promoted to the `_Filter.approvals` filter so the
  "Spawn project steward" + "Reject" buttons render directly on
  the card instead of requiring a Details drill-in. Details
  remains available for full context.

---

## v1.0.601-alpha — 2026-05-16

### Fixed

- **Me FAB → general steward chat (not spawn sheet).**
  `openStewardSession()` built `liveStewards` via
  `isStewardHandle()`, which deliberately excludes `@steward` (the
  team singleton). With only the general steward live, 0 stewards
  matched → fell through to the spawn sheet. Fix: also accept
  handles matching `isGeneralStewardHandle()` so the Me FAB's
  "talk to any live steward" intent works for general-only setups.
- **Approve `project_steward_request` actually does something.**
  Mobile had **zero references** to this attention kind. The
  generic Approve button just recorded an audit. New branch in
  `InlineApprovalActions` for `kind == 'project_steward_request'`:
  renders **"Spawn project steward"** that opens
  `showSpawnProjectStewardSheet` prefilled with `project_id` +
  `suggested_host_id` from `pendingPayload`. On successful spawn,
  resolves the attention with `body=<agent_id>` so the audit row
  links to the new agent.

---

## v1.0.600-alpha — 2026-05-16

### Added

- **Project list pull-to-refresh invalidates `insightsProvider`.**
  Phase/progress on each row reads from a separate `FutureProvider`
  family that `refreshAll()` didn't touch. Tapping into Insights
  warmed it; pull-to-refresh on the list didn't. Fix is a
  `ref.invalidate(insightsProvider)` next to the `refreshAll()` call.
- **`ProjectCreateSheet` gets an `isTemplate` constructor param.**
  When true, the title flips to "New project template" and the
  payload sets `is_template: true` so the project lands as a
  reusable template (DB row with `is_template=1`).
- **Library New-template sheet: `plans` + `projects` categories +
  Clone-from-existing.** Adds `plans` to the filesystem chip list
  (writes a YAML file like other categories) and a `projects`
  chip that routes to `ProjectCreateSheet(isTemplate: true)` since
  project templates are DB rows, not files. Each filesystem
  category gains a "Clone from existing" affordance — opens a
  picker, fetches the picked template's body via `getTemplate()`,
  and seeds the editor with a suggested `<source>-copy` name to
  avoid shadowing built-ins.
- **Library Templates section ordering pinned to canonical list:**
  agents → prompts → plans → projects → policies. Unknown server
  categories sink to the bottom in alpha order.

### Changed

- **Renamed "Steward template" → "Project template"** across
  `project_create_sheet`, `project_detail_screen`,
  `project_edit_sheet`. Both fields always pointed at project
  rows with `is_template=1` (per blueprint §6.1) — the old label
  read as "agent-of-kind-steward" and confused users. Added
  helper text under each field clarifying the timing distinction
  (`template_id` = ongoing recipe; `on_create_template_id` = fires
  once at create).
- **On-create template moved into Advanced expander** on
  `ProjectCreateSheet` so the primary flow is one picker.

---

## v1.0.599-alpha — 2026-05-16

### Added

- **Library tab gains collapsible category sections + cross-tab
  search.** The Library AppBar gets a search icon that swaps the
  title for a `TextField`; the query filters both Templates (by
  name + category) and Engines (by family + bin + supports). Each
  Templates category now renders as a `_CategoryGroup` with a
  tappable header (chevron + name + tile count); user collapse
  state persists in-memory across tab swaps. Search overrides
  collapse — every section with a match is forced open while a
  query is active. Default state matches prior behavior
  (everything expanded), so users who don't touch the new
  affordance see no behavioral change. Engines tab gets the same
  filter — flat list, no groups.

---

## v1.0.598-alpha — 2026-05-16

### Added

- **`projects.create` MCP tool now advertises `is_template` +
  template-shape fields.** The tool's description distinguishes
  the two authoring paths (concrete project vs reusable project
  template) and the InputSchema gains `is_template`,
  `parameters_json`, `on_create_template_id`, `steward_agent_id`,
  `policy_overrides_json` with role descriptions. The REST handler
  always accepted these — only the MCP advertisement was missing,
  so stewards weren't including them. Stewards can now author
  project templates without server-side changes.
- **Steward prompts disambiguate plan-template vs project-template.**
  `steward.general.v1.md` bootstrap splits the old step 6 into
  two: "plan template" (YAML file via `templates.plan.create`) and
  "project template" (`projects.create({is_template:true,
  on_create_template_id:<plan-name>})`). `steward.v1.md` authority
  section gains the same disambiguation for runtime template
  revision.
- **`applicable_to.template_ids:` schema field on agent / prompt /
  plan templates.** Hub-side: `handlers_templates.go` parses the
  field during list and surfaces it as `applicable_template_ids`
  on each row (single-roundtrip filtering). Mobile-side: new
  `lib/services/template_filter.dart` + filter applied in
  `spawn_agent_sheet._loadTemplate` and `plan_create_sheet`.
  Templates without the field stay team-shared (the back-compat
  default — every bundled template remains visible until a
  steward explicitly scopes new ones).

---

## v1.0.597-alpha — 2026-05-16

### Fixed

- **Spawn-steward sheet engine row reflects YAML `driving_mode` +
  `backend.model`.** The chip used to read `backend.kind` only and
  stamp a hardcoded per-engine description, so a template with
  `driving_mode: M4` still claimed `stream-json · MCP gate` (the
  M2 claude-code default) and a model swap never surfaced. Now
  parses both fields on every template-picker change. Mode-aware
  transport hint for claude-code (M1=ACP stdio, M2=stream-json,
  M4=JSONL tail). `kimi-code` added to the engine table (was
  previously falling through to the "custom template" default
  with a useless `kind=kimi-code` blurb).

---

## v1.0.596-alpha — 2026-05-16

### Added

- **`templates.{agent,prompt,plan}.scaffold` MCP tools.** Server
  returns a clean skeleton with all schema-mandated fields
  populated and persona-specific bits stripped. Args:
  `{kind: worker|steward, engine: claude-code|codex|gemini-cli|kimi-code}`
  for agents, `{kind: worker|steward}` for prompts, `{phases: N}`
  for plans. Engine arg swaps the cmd line + permission gating
  per the bundled `steward.<engine>.v1.yaml` conventions. Read-
  only — no side effects.
- **Steward prompts gain a "never improvise YAML" rule with two
  named discovery paths.** `steward.general.v1.md` bootstrap
  step 3 (new) and `steward.v1.md` authority section both
  explicitly teach: call `templates.<cat>.scaffold` for a clean
  skeleton, OR `templates.<cat>.list + .get` on the closest
  bundled template. The bundled templates are the schema reference.

### Changed

- **`templates.{cat}.create` description enriched** with a one-
  paragraph scaffold-then-modify hint pointing at both discovery
  paths. The MCP tool description is read at every call so this
  is the highest-leverage spot for the schema-discovery problem
  that caused stewards to author non-functional templates.

---

## v1.0.595-alpha — 2026-05-15

### Fixed

- **Project-scoped stewards (`steward.v1`, `steward.codex.v1`,
  `steward.gemini.v1`, `steward.kimi.v1`) lose their hardcoded
  `default_workdir: ~/hub-work`.** They now auto-derive
  `~/hub-work/<pid8>/<handle>` from the spawn — same fallback
  the launcher already applied for worker templates. Two project
  stewards on the same host used to silently collide on the same
  `.mcp.json` / `.claude/settings.local.json` / `CLAUDE.md` per-
  spawn config; this was the root of the "phantom kimi steward"
  user-report on `research-method-demo`.
- **`launch_m4_locallogtail.go` gains the same auto-derive
  fallback** so the LocalLogTailDriver isn't a regression. New
  regression test
  `TestLaunchM4LocalLogTail_AutoDerivesWorkdirFromProjectAndHandle`
  verifies `.mcp.json` + `settings.local.json` materialize at
  the derived path when the template omits `default_workdir`.

### Unchanged (by design)

- `briefing.v1`, `ml-worker.v1` keep their explicit
  `~/hub-work` — their YAML comments document the
  "team-shared scratch" intent.
- Persona stewards (`general`, `infra`, `research`, `m4-test`)
  keep their sub-paths (`~/hub-work/<persona>`) — already
  disambiguates per persona.

---

## v1.0.594-alpha — 2026-05-15

### Added

- **Project Agents detail sheet header now matches the team
  Session-chat surface.** Backfilled the latest `session.init`
  for the agent via a one-shot
  `listAgentEvents(tail: true, limit: 200)` scan on sheet open
  (same pattern AgentFeed uses internally). When found, the
  header renders a `SessionInitChip` underneath the title row:
  engine kind + model + permission mode + tools count + mcp
  count. Tap opens the same details sheet the team session uses.
  Best-effort: silent miss for agents that never emitted
  `session.init` (e.g. exec-per-turn engines mid-warm-up).
- **"View agent config" overflow item.** Reuses the existing
  `showAgentConfigSheet` (persona kind, derived role, driving
  mode, parent, host, status, raw `spawn_spec_yaml`).

### Changed

- **Pause / Terminate / Respawn collapsed into a
  `PopupMenuButton`.** The full-width action `Wrap` is gone.
  New `_ActionsMenu` widget carries the same actions plus the
  config entry. State-aware: Pause/Resume only when live + has
  pane; Respawn only when spec is available; Terminate vs
  Delete depending on whether the agent is dead. Mirrors the
  `SessionChatScreen` overflow shape so both surfaces feel like
  one product.

---

## v1.0.593-alpha — 2026-05-15

### Added

- **Type-prefixed short ids on sessions list, project Agents
  tab, and audit detail Target row.** Hub primary keys are
  26-char Crockford-base32 ULIDs — visually indistinguishable
  across entity types. New `lib/services/id_format.dart` with
  `formatId(kind, id)` returns
  `'<kind>-<head8>…<tail4>'`, e.g.
  `'prj-01KRNVJT…N4M0'`. `idKindFor()` maps target_kind /
  scope_kind strings to display tokens (`prj`, `sess`, `agt`,
  `att`, `audit`, `evt`, `run`, `plan`, `doc`, `art`, `ch`,
  `host`, `task`). `copyIdToClipboard` is the long-press
  affordance. Full id stays selectable in the audit detail
  panel for `grep`-able server logs. ULIDs remain verbatim in
  storage — no schema migration.

---

## v1.0.592-alpha — 2026-05-15

### Added

- **ADR-027 LocalLogTailDriver shipped.** Claude-code M4 swap
  from raw-PTY/xterm-VT to JSONL tail + per-spawn host-runner
  UDS gateway hosting 9 hook MCP tools (`mcp__termipod-host__hook_*`).
  Mobile renders M4 claude-code agents as typed cards
  (text/thinking/tool_call/tool_result/approval) instead of TUI
  text dump. Plan-approval card + compaction card surface as
  attention items. Egress proxy preserves hub-URL masking. 20
  wedges across 12 new Go files; see ADR-027 + plan in
  `docs/decisions/` / `docs/plans/`.

### Fixed (between v1.0.592 and v1.0.593)

- **`request_project_steward` registered in `tools/list`.**
  ADR-025 W4 handler shipped but the catalog entry was never
  added, so claude-code reported "No such tool available"
  when the general steward followed its prompt. Now visible.
- **`--a2a-addr` defaults to `127.0.0.1:0`** (loopback auto-
  pick). The hub-side relay rewrites the public card URL
  regardless, so loopback bind works behind NAT. Prior
  empty-string default silently disabled the entire A2A
  subsystem on stock installs — every `a2a.invoke` returned
  "no A2A agent found". New explicit opt-out:
  `--a2a-addr=disabled`.
- **`project.update` audit meta captures new scalar values**
  (goal, steward_agent_id, on_create_template_id, budget_cents).
  Activity timeline finally answers "what was it set to?"
  instead of only "which column changed?".
- **Tier-table misclassifications fixed.** Explicit rows for
  `agents.terminate` (Significant), `templates.{agent,prompt,
  plan}.{create,update,delete}` (Significant) +
  `.list/.get` (Trivial), `hosts/agents.list/get` (Trivial),
  `mobile.navigate` (Trivial), `request_project_steward`
  (Routine). Eight tools were silently defaulting to Routine.
- **Two new safety tests.** `TestEveryCatalogEntryHasTier`
  rewritten to read `toolTiers` directly (the old check was
  toothless because `tierFor()` always returns non-empty).
  `TestEveryDispatcherCaseAdvertised` asserts every dispatcher
  case appears in `tools/list` with a documented alias allowlist.

---

## v1.0.544-alpha — 2026-05-12

### Added

- **Router covers every Overview tile + phase summary + personal SSH
  connect.** The v1.0.543 round missed several of the tile slugs the
  steward uses for navigation; the user reported "hero, outputs,
  assets" as gaps. Added project sub-routes for:
  - `outputs` (alias of `artifacts`) → ArtifactsScreen
  - `assets` → AssetsScreen (extracted public from the private
    `_AssetsHostScreen` previously buried in `shortcut_tile_strip.dart`)
  - `experiments` (alias of `runs`)
  - `schedules` → SchedulesScreen
  - `deliverables` → DeliverablesScreen
  - `acceptance-criteria` (and `acceptance_criteria`, `criteria`,
    `acceptancecriteria` aliases) → AcceptanceCriteriaScreen
  - `discussion` / `channels` → ProjectChannelsListScreen
  - `phases/<phase>` / `phase/<phase>` → PhaseSummaryScreen
    (the per-phase hero; resolves projectName + isCurrent from the
    cached project record with refresh-retry).
- **`termipod://host/<idOrName>` handles personal hosts too.**
  Steward agents see "two kinds of hosts": team-registered hub hosts
  (open detail sheet) and personal SSH bookmarks
  (`Connection` rows — "open" means connect to terminal). The router
  now mirrors `hosts_screen._HostRow._handleTap`: pass 1 is a
  Connection match → push TerminalScreen; pass 2 is a hub host
  match → openHostDetail sheet. Refresh-retry on hub miss as before.
  Connection lookup is case-insensitive against id / name / host.
- **`termipod://connect/<idOrName>` for explicit terminal-only.**
  Skips the hub-host fallback; returns unknown if no Connection
  matches. Useful when the steward wants to force "connect" semantics
  regardless of whether a hub-host with the same label exists.

### Changed

- `widgets/shortcut_tile_strip.dart` now imports the public
  `AssetsScreen` instead of constructing its private host wrapper.
  No user-visible behavior change; just deduplicates the wrapper so
  the router can reach it.

---

## v1.0.543-alpha — 2026-05-12

### Added

- **URI router covers all project sub-entities + host lookup by
  name.** Steward navigation needed more shapes than just `documents`:
  - Project sub-routes: `/tasks/<tid>` → TaskDetail,
    `/agents/<aid>` → Agent sheet, `/plans/<plid>` → PlanViewer,
    `/plans` → PlansScreen, `/runs/<rid>` → RunDetail, `/runs` →
    RunsScreen, `/artifacts` → ArtifactsScreen.
  - Project tab anchors: `/overview` `/activity` `/agents` `/tasks`
    `/files` push ProjectDetail with the right pill highlighted.
    `ProjectDetailScreen` accepts an `initialTab` param; the
    `PageController` initial page + `_index` derive from it.
  - Top-level forms: `termipod://run/<rid>` and
    `termipod://host/<idOrName>` (the host form tolerates either
    a ULID or a case-insensitive `name`/`hostname` match, with
    refresh-retry — steward agents tend to know hostnames not ids).
- **Steward can now `termipod://host/<hostname>` to open the host
  detail sheet.** Previously only `termipod://hosts` (tab switch)
  was wired.

---

## v1.0.542-alpha — 2026-05-12

### Added

- **URI router: `termipod://project/<pid>/documents/<docId>` and
  `termipod://document/<docId>` push DocumentDetailScreen.**
  The steward emitted the nested form after creating a doc and we
  silently fell through to ProjectDetail (taking only `segments[0]`
  as the project id and ignoring trailing segments). Sub-routes now
  branch off `case 'project'`:
  - `/documents/<docId>` → push the document directly. The detail
    screen fetches by id, so the project doesn't need to be in
    cache.
  - `/documents` (no id) → push the project-scoped documents list.
  Top-level `termipod://document/<id>` also added for the steward to
  emit when the project context is implicit.

---

## v1.0.541-alpha — 2026-05-12

### Fixed

- **IME regression — deleted text returns on Android Gboard.** v1.0.539
  wrapped the overlay chat input's `TextField` in a `ListenableBuilder`
  watching `_ctrl` so the inline mic suffix could toggle on emptiness
  changes. That fires on every keystroke and rebuilds the `TextField`
  per character — same shape as the v1.0.466 SSE-driven rebuild bug
  the v1.0.472 isolation work fixed. Per-character rebuild bounces
  `EditableText.didUpdateWidget`, which can re-emit `setEditingState`
  to the IME and the IME rebounds with its cached predictive word —
  deleted characters reappear. Replaced the wrap with a
  `ValueNotifier<bool>` updated via a `_ctrl` listener; the notifier's
  `==` check dedupes per-keystroke notifications so only the suffix
  icon (a small `ValueListenableBuilder`) rebuilds on actual
  emptiness flips. The `TextField` itself is stable.
- **Overlay chat panel scrolls to the latest message on open.** The
  `_MessagesRegion` `ListView` used to render at the top of the
  cached message history; users had to manually scroll to find what
  the steward just said. Tracks `_lastSeenLength`; whenever the
  message count changes (panel-open initial build or new SSE message)
  schedules a post-frame `jumpTo(maxScrollExtent)`.

---

## v1.0.540-alpha — 2026-05-12

### Fixed

- **`termipod://project/<id>` failed for steward-created projects.**
  When the steward MCP `create_project` tool returned and the steward
  immediately emitted a `mobile.intent` to navigate to the new
  project, the mobile client's hub snapshot hadn't yet observed the
  create — the router's local-cache lookup missed and surfaced "could
  not navigate." The router now accepts an optional `refreshHub`
  callback; on a project/agent cache miss it refreshes the hub once
  and retries the lookup before failing. Live SSE caller wires the
  callback through; tap-to-refire on past intent pills does too.
  `navigateToUri` is now `Future<NavigateResult>`; the call sites
  in `steward_overlay_controller._dispatchIntentLive` and
  `_IntentPill._refire` use `unawaited` so the SSE handler and tap
  callback stay non-blocking.

### Changed

- **Mode A recording HUD redesigned for prominence.** Tester
  feedback was that the v1.0.536 HUD (280-px pill, small red dot,
  13-pt text) read as a passive tooltip, not "you are LIVE on the
  mic." New shape (340 × 175):
  - Red header bar with `RECORDING` label, pulsing dot, and an
    outward-rippling ring.
  - 32-pt monospace mm:ss timer flanked by three staggered animated
    "audio level" bars (not real RMS amplitude — RMS strip is still
    deferred polish — but the staggered motion sells "live" without
    a per-frame amplitude pipeline).
  - 15-pt transcript area (3 lines).
  - Footer split: "Release to send" on the left, "Drag away to
    cancel" on the right so the cancel affordance is unmissable.
  Positioning helper in `steward_overlay.dart` widened to match.

---

## v1.0.539-alpha — 2026-05-12

### Changed

- **Mode B voice input polish — overlay chat input rearranged.** The
  voice button is no longer the same affordance as Send; tester
  feedback was that the old mic/send icon swap on the right of the
  composer read as a single, ambiguous button. New layout:
  `[voice toggle] [field] [attach…] [send]`. Voice is the leftmost
  surface (where the file-attach button used to live); attach buttons
  moved next to Send. Tapping the voice toggle replaces the text field
  with a large "Hold to speak" gesture surface that mirrors the Mode A
  puck (long-press dictates, release commits per
  `voiceSettings.autoSendPuckTranscripts` — auto-send when on, drop
  into the field for review when off). Tapping the toggle again
  switches back to keyboard mode.
- **Inline streaming mic inside the text field.** When the field is
  empty AND the user's first input from empty wasn't keyboard, a
  small mic icon appears as the field's suffix. Tap once to start
  streaming dictation (icon turns red); transcripts stream directly
  into the field as partials arrive. Tap again to stop and commit.
  Hidden once the user types manually into an empty field — a
  one-way signal that this user prefers typing, so the prompt stops
  inviting voice.

---

## v1.0.538-alpha — 2026-05-12

### Fixed

- **iOS release build broke at v1.0.537-alpha.** Tagging surfaced a
  transitive-dep incompatibility: `record_linux 0.7.2` (resolved
  alongside `record: ^5.2.0`) is missing a `hasPermission` named
  argument that the newer
  `record_method_channel_platform_interface` declares — the iOS Xcode
  build pulls in all Flutter platform plugins for compile checks, so
  the Linux variant fails the build even though the runtime target
  is iOS. Bumped the `record` constraint to `^6.0.0` so pub resolves
  aligned transitive deps. The 6.x API surface is backward-
  compatible for the calls we use (`AudioRecorder` /
  `hasPermission` / `startStream(RecordConfig)` / `stop` / `cancel`
  / `dispose`); no Dart code changes needed.

### Notes

- The `flutter test` workflow does not exercise Pod-level iOS
  compilation, so the issue was latent across v1.0.531 → v1.0.537's
  green CI runs. The release-workflow surface is the only signal for
  iOS-side native-dep incompatibilities — they only manifest at tag
  time.

---

## v1.0.537-alpha — 2026-05-12

### Changed

- **Voice input W5 — docs + status block updates.** Closes the Path C
  wedge (no code change in this release; doc-only). Adds **Scenario
  11 — voice input Path C** to
  [`how-to/test-agent-driven-prototype.md`](how-to/test-agent-driven-prototype.md)
  covering the end-to-end walkthrough: enabling the master toggle,
  pasting the DashScope key, testing Mode B (panel mic button), Mode
  A (puck long-press auto-send), Mode A's auto-send-off review-
  fallback v1 stub, and what to capture on failure. Flips the plan
  at
  [`plans/voice-input-path-c-alibaba.md`](plans/voice-input-path-c-alibaba.md)
  from "Proposed" to "Shipped 2026-05-12 (v1.0.531 → v1.0.536)". Flips
  the discussion at
  [`discussions/voice-input-cloud-vs-offline.md`](discussions/voice-input-cloud-vs-offline.md)
  to "Path C shipped". Updates the "Voice via system IME only"
  limitation in the prototype how-to to reflect the new in-app
  dictation as the alternative.

---

## v1.0.536-alpha — 2026-05-12

### Added

- **Voice input W3b — Mode A puck long-press + recording HUD.** Hands-
  free voice path. Long-press the steward overlay puck on any screen
  (panel collapsed) to start recording. The puck flips its avatar to
  a mic icon with a red ring; a floating
  [`VoiceRecordingHud`](../lib/widgets/steward_overlay/voice_recording_hud.dart)
  anchors above or below the puck (whichever has more screen room)
  showing a red pulse + mm:ss timer + the live streaming partial
  transcript + a "drag away to cancel" hint. Release → the session's
  `completed` event fires; if Settings → "Auto-send puck transcripts"
  is on (default), the transcript is auto-sent via
  `StewardOverlayController.sendUserText` and a confirmation SnackBar
  appears with the first ~60 chars of what was sent. The panel does
  NOT auto-open in the auto-send case — the user can tap the puck
  later to see the response. Drag finger >80 dp from the puck origin
  → cancel (no send, no toast). 60-second cap auto-stops; permission
  / mic / WS errors surface as SnackBars.
- **Auto-send off → review fallback (v1 stub).** When the toggle is
  off, the panel opens and a SnackBar shows the transcript verbatim
  — first-class pre-fill into the chat input is a v1.0.537 follow-up
  (needs an injection signal from overlay state into the chat input
  controller).
- **Soundwave strip deferred.** The plan's third HUD strip
  (CustomPainter rendering RMS bars per ~100 ms PCM chunk) is
  v1.0.537+ polish; v1.0.536 ships the strict-minimum useful signal
  (pulse + timer + transcript line) so the wedge fits a single CI
  cycle.

---

## v1.0.535-alpha — 2026-05-12

### Added

- **Voice input W3a — Mode B panel mic button.** First tester-
  verifiable surface for the Path C pipeline. When voice is enabled +
  has API key AND the chat input is empty, the send icon swaps for a
  mic icon. Long-press to record: PCM16 streams over the WS to
  DashScope; partial transcripts arrive every ~600 ms and replace the
  in-progress sentence in the input field; finals accumulate with
  trailing spaces. Release → the recorder closes, the cloud_stt
  client sends `finish-task`, the final transcript stays in the
  input field, and the icon flips back to send for the user to
  review + tap. Drag the finger >60 dp away from the mic → cancel,
  restore whatever text was in the input before recording started.
  Errors (mic permission, WS failure, server-side `task-failed`)
  surface as SnackBars. Session lifecycle hooks ride on the
  v1.0.534 orchestrator. Wired through `_ChatInputSlot` as a
  `voiceStarter` closure so the API key is read lazily at long-press
  time and never enters widget state
  (`lib/widgets/steward_overlay/steward_overlay_chat.dart`). No
  Mode A (puck long-press + HUD) yet — that lands in v1.0.536+.

---

## v1.0.534-alpha — 2026-05-12

### Added

- **Voice input W3/W4 — settings screen + session orchestrator.**
  Settings → "Voice input" tile (in the Behavior section) opens the
  new `VoiceSettingsScreen`
  (`lib/screens/settings/voice_settings_screen.dart`): master enable
  toggle, auto-send-puck toggle, DashScope API key entry (obscured
  TextField → `flutter_secure_storage`), region picker (Beijing /
  Singapore / US), model picker (Fun-ASR realtime / Paraformer
  realtime v2). The API key tile shows "Stored securely • tap to
  replace" when set with a trash-icon clear action; "Not set" with
  warning tint when empty.
- **`VoiceRecordingSession` orchestrator**
  (`lib/services/voice/voice_recording_session.dart`) combines the
  W1 `RecordingController` and the W2 `CloudStt` client into a
  single session abstraction. Owns the partial→final accumulation
  policy: each partial replaces the in-progress sentence, each
  final appends with a single trailing space, next partial begins
  a new sentence. Emits tagged `VoiceSessionEvent`s
  (`transcriptUpdated` / `completed` / `cancelled` /
  `maxDurationReached` / `error`) that UI surfaces subscribe to.
  60-second max-duration timer auto-stops the session; cancel
  discards the partial, stop commits via the ASR's finish-task
  flow. 9 unit-test cases cover language-hint forwarding, partial
  + final accumulation, stop vs cancel divergence, completed
  event, permission-denied propagation, and the max-duration
  timer firing stop. No mic UI surface yet — Mode B mic button +
  Mode A puck land in v1.0.535+.

---

## v1.0.533-alpha — 2026-05-12

### Added

- **Voice input W3/W4 prep — config layer.** Third release in the Path
  C series. Adds the `VoiceSettings` immutable model
  (`lib/services/voice/voice_settings.dart`) carrying `enabled`,
  `autoSendPuckTranscripts`, `region`, `model`, `languageHints`, and a
  derived `hasApiKey` flag — the API key itself stays in
  flutter_secure_storage and is read on-demand so the secret never
  enters the observable Riverpod state graph. Adds
  `voiceSettingsProvider`
  (`lib/providers/voice_settings_provider.dart`) wired to
  shared_preferences + flutter_secure_storage using the `await _ready`
  pattern (`feedback_prefs_load_race`) so the very first user toggle
  doesn't race the async on-disk load. Region + model keys are
  hand-mapped strings (not enum indices) so reordering the enum later
  doesn't shift saved values. 14 unit-test cases cover defaults,
  copyWith, equality, and the round-trip + fallback behavior of the
  region/model JSON helpers. No UI surface yet — the settings screen
  + mic affordance follow in v1.0.534.

---

## v1.0.532-alpha — 2026-05-12

### Added

- **Voice input W2 — DashScope WebSocket ASR client.** Second wedge
  of the Path C plan. Adds `web_socket_channel: ^2.4.0` and
  `lib/services/voice/cloud_stt.dart` (`CloudStt` interface +
  `AlibabaWebSocketStt` concrete impl). State machine:
  `connecting → running → finishing → closed`. Opens the WebSocket
  to `wss://dashscope.aliyuncs.com/api-ws/v1/inference` (Beijing
  default; Singapore + US endpoints selectable), sends the
  `run-task` JSON for `fun-asr-realtime` (or
  `paraformer-realtime-v2`), pumps PCM chunks as binary frames once
  `task-started` lands, parses `result-generated` events into
  `TranscriptUpdate(text, isPartial, isFinal)`, and sends
  `finish-task` when the audio stream closes. `task-failed` events
  surface as `DashScopeAsrException`. The WebSocket channel + task
  ID generator are both injectable so the tests
  (`test/services/voice/cloud_stt_test.dart`, 7 cases) run with a
  `_FakeWebSocketChannel` — happy path, task-failed, server close,
  early audio cancellation, model id forwarding, regional endpoint
  selection. No UI surface yet; W3 wires the recording + WS stack
  into the mic affordances.

---

## v1.0.531-alpha — 2026-05-12

### Added

- **Voice input W1 — audio recording infrastructure.** First wedge of
  the Path C plan (`docs/plans/voice-input-path-c-alibaba.md`). Adds
  `record: ^5.2.0`, the Android `RECORD_AUDIO` permission, and the
  iOS `NSMicrophoneUsageDescription` string. Introduces
  `lib/services/voice/recording_controller.dart` — a thin wrapper
  that opens a PCM16 16 kHz mono stream from the platform mic via
  `startStream()`, with `stop()` / `cancel()` / `dispose()`
  lifecycle. The plugin's concrete `AudioRecorder` sits behind a
  `RecorderBackend` interface so the unit tests
  (`test/services/voice/recording_controller_test.dart`, 8 cases)
  run without platform channels. Permission denial,
  already-recording, and platform-error states surface as
  `VoiceRecordingException` with explicit `kind`s for the UI layer
  to dispatch on. No UI surface yet; W2 wires this stream into the
  DashScope WebSocket client and W3 adds the mic gestures.

---

## v1.0.530-alpha — 2026-05-12

### Fixed

- **PDF gray-screen on v1.0.529** — even removing the
  `pagePaintCallbacks` line, the bare `PdfTextSearcher`
  construction + listener wiring in the screen state grays the
  viewport on pdfrx 2.2.24. Stripped back to v1.0.527's state:
  TOC drawer, internal/external link tap, page badge, deferred
  onViewerReady all stay; find-in-PDF (search UI + searcher) is
  fully removed. **Find-in-PDF is now declared incompatible with
  the pdfrx 2.2.24 pin** until either pdfrx upstream stabilises
  text search in a future 2.2.x patch or we move past the
  native-assets regression in 2.3.x.

### Added

- **Tappable page badge → "Go to page" dialog.** Tap the floating
  `12 / 47` pill at the bottom of the PDF viewer to open a "Go to
  page" dialog with a numeric `TextFormField` (validated against
  `1 – pageCount`) plus Cancel/Go buttons. Pre-fills the current
  page, supports keyboard "Go" action. On confirm, calls
  `PdfViewerController.goToPage(pageNumber:)` to jump. Replaces
  the scroll-only navigation path with a direct way to seek
  long PDFs (`lib/widgets/artifact_viewers/pdf_viewer.dart`).

---

## v1.0.529-alpha — 2026-05-12

### Fixed

- **PDF gray-screen regression in v1.0.528** — adding
  `pagePaintCallbacks: [searcher.pageTextMatchPaintCallback]` to
  `PdfViewerParams` grays the viewport on pdfrx 2.2.24. Removed
  that single line — the `PdfTextSearcher` itself stays wired up
  so the AppBar search UI still functions: type a query, see
  "3/12" match count, tap prev/next to jump pages via the
  searcher's `goToNextMatch` / `goToPrevMatch`. **The only thing
  missing is in-viewport highlighting** of matches — they don't
  paint over the page text. Likely a signature mismatch between
  the 2.2.x `PdfViewerPagePaintCallback` typedef and the closure
  pdfrx 2.3.x ships for the searcher's highlight rendering.
  Acceptable degradation; find-in-PDF is still usable via the
  match-count navigation
  (`lib/widgets/artifact_viewers/pdf_viewer.dart`).

---

## v1.0.528-alpha — 2026-05-12

### Added

- **Find in PDF** — last v1.0.518 feature re-added; full PDF parity
  recovered. Tap the `search` icon in the AppBar to swap title for a
  TextField + match-count `3/12` + prev/next arrows + close. Matches
  highlight in the viewport via `PdfTextSearcher.
  pageTextMatchPaintCallback` (the last v1.0.518 suspect; clears
  the bisect). Case-insensitive by default. The searcher's listener
  callback's `setState` is also wrapped in `addPostFrameCallback`
  defensively — matches the v1.0.526 defer pattern we now know is
  the cure for "setState during pdfrx build pass."
- **Recovery sequence complete.** Every v1.0.518 feature now ships
  on top of pdfrx 2.2.24 without a gray-screen regression. Six
  layered releases bisected the regression: linkHandlerParams +
  controller + onPageChanged + onViewerReady + loadOutline + TOC +
  pagePaintCallbacks. The pattern that fixes it all is
  `WidgetsBinding.instance.addPostFrameCallback` wrapping any
  `setState` in a pdfrx callback that can fire during the build
  pass (`lib/widgets/artifact_viewers/pdf_viewer.dart`).

---

## v1.0.527-alpha — 2026-05-12

### Added

- **PDF outline / bookmarks / TOC drawer is back** — re-added on top
  of the v1.0.526 deferred-setState pattern. AppBar grows an
  `Outline` icon (visible only when the PDF carries a non-empty
  outline tree) that opens an end-drawer with the nested chapter
  list. Tap a node → `PdfViewerController.goToDest`. Same UX as
  v1.0.518's TOC drawer but with the `loadOutline` + outline-loaded
  setState calls now inside `WidgetsBinding.instance
  .addPostFrameCallback` so they don't fire during pdfrx's build
  pass. Synthetic PDFs without an outline hide the icon entirely.
- **Refactor:** `ArtifactPdfViewerScreen` is now `StatefulWidget`
  again (was `StatelessWidget` since v1.0.521's strip-back) — it
  owns the `PdfViewerController` and the outline state. The leaf
  `ArtifactPdfViewer` accepts `controller:` + `onOutlineLoaded:`
  as optional constructor params; if neither is supplied it falls
  back to the self-contained v1.0.524 mode (no TOC, internal
  controller) (`lib/widgets/artifact_viewers/pdf_viewer.dart`).

---

## v1.0.526-alpha — 2026-05-12

### Added

- **Total page count on PDF badge** — the floating page pill now
  reads `12 / 47` instead of just `12` once pdfrx reports the
  document's total page count via `onViewerReady`. Hidden until
  the count resolves; falls back to current-page-only display if
  the callback never fires.
- **Hypothesis-test for v1.0.518's gray screen.** The
  `onViewerReady` callback's setState is wrapped in
  `WidgetsBinding.instance.addPostFrameCallback` so the state
  update bounces to the next frame instead of firing during
  pdfrx's build pass. This is the strong hypothesis: v1.0.518's
  onViewerReady called `widget.onOutlineLoaded` synchronously,
  which `setState`'d the parent screen during build → Flutter's
  "setState during build" assertion → ErrorWidget (gray). If
  rendering survives v1.0.526, the defer pattern is correct and
  the same wrapping unlocks the TOC drawer
  (`loadOutline`) in a later step
  (`lib/widgets/artifact_viewers/pdf_viewer.dart`).

---

## v1.0.525-alpha — 2026-05-12

### Added

- **Current-page badge on PDF viewer** — floating semi-transparent
  black pill at the bottom centre showing the current page number
  (e.g. `12`). Hidden until the first `onPageChanged` event fires
  so single-page PDFs and the initial-render frame stay
  uncluttered. No total page count yet — that needs
  `onViewerReady`, the next bisect step. Bisect significance: if
  rendering still works, the `onPageChanged` callback is innocent
  and v1.0.518's gray-screen break is either `pagePaintCallbacks`
  or `onViewerReady` (`lib/widgets/artifact_viewers/pdf_viewer.dart`).

---

## v1.0.524-alpha — 2026-05-12

### Added

- **Tappable internal page-refs inside PDFs** — v1.0.523 confirmed
  external URLs work without grayscreening, so this step adds a
  local `PdfViewerController` and routes internal page-dests via
  `_controller.goToDest(link.dest)`. The controller lives inside
  the leaf viewer's state — not threaded from the screen — so it
  cannot interact with any other surface (no TOC drawer, no
  text-search, no overlay builder). This is the second bisect
  point on the v1.0.518 recovery list: if rendering still works,
  passing `controller:` to `PdfViewer.data` is innocent and the
  v1.0.518 break was from `pagePaintCallbacks` or `onViewerReady`
  instead (`lib/widgets/artifact_viewers/pdf_viewer.dart`).

---

## v1.0.523-alpha — 2026-05-12

### Added

- **Tappable external URLs inside PDFs** — first careful re-add
  from the v1.0.518/.519 gray-screen recovery list. URLs embedded
  in PDFs (citation links in academic papers, etc.) now launch in
  the system browser when tapped. Implementation deliberately
  minimal — only the `PdfViewerParams.linkHandlerParams` field
  added, callback handles `link.url` via `launchUrl(externalApplication)`.
  No `PdfViewerController` is passed to `PdfViewer.data` (which
  was the suspected runtime trigger for v1.0.518's gray-screen),
  so internal page-refs (`link.dest`) are silently ignored — those
  need a controller and will land in a later step once we re-add
  the controller safely. Single-line API change against the
  v1.0.521 baseline; should not regress rendering
  (`lib/widgets/artifact_viewers/pdf_viewer.dart`).

---

## v1.0.522-alpha — 2026-05-12

### Changed

- **Structured-deliverable component rows show name + artifact kind**
  — tester report: "delivery row only shows types like document /
  artifact / run, no name or artifact-kind". The
  `_ComponentCard` in `StructuredDeliverableViewer` previously
  displayed only the kind label as the primary line and the raw
  ref-id below. Refactored from `ConsumerWidget` to
  `ConsumerStatefulWidget` so it can eagerly fetch the referenced
  entity (`getArtifact` / `getDocument` / `getRun`) on mount and
  surface real info:
  - **Primary line**: resolved entity name (e.g. *"Lifecycle demo
    PDF"*, *"Method doc"*) with the `required` pill on the right.
  - **Secondary line**: `kind · sub-kind` — for artifacts that
    becomes *"artifact · pdf"* / *"artifact · image"* / *"artifact
    · tabular"*; documents stay *"document · <refId-prefix>"* so
    the id is still locatable. Loading state shows "Loading…" as
    the primary until the fetch resolves; fetch failures fall back
    to the refId (`lib/screens/deliverables/structured_deliverable_viewer.dart`).

---

## v1.0.521-alpha — 2026-05-12

### Reverted

- **Backed out v1.0.518's TOC drawer + find-in-PDF** after v1.0.520
  (pure revert of v1.0.519) also full-screen-grayed. Root cause:
  v1.0.518 introduced `controller: widget.controller` and
  `pagePaintCallbacks: [searcher.pageTextMatchPaintCallback]` and
  `onViewerReady: (document, controller) async {...}` on
  `PdfViewer.data` / `PdfViewerParams`. The Dart type system
  accepted all three on pdfrx 2.2.24, but at runtime one (or
  more) of them throws — pdfrx 2.2.24's `PdfViewer.data`
  constructor likely doesn't accept `controller:` in 2.2.x the way
  the 2.3.x master docs describe; Flutter then renders its default
  gray `ErrorWidget` over the viewport. Restored
  `lib/widgets/artifact_viewers/pdf_viewer.dart` to v1.0.517's
  state (commit `b20e2a2`): plain `PdfViewer.data(bytes, sourceName:
  ..., params: const PdfViewerParams(backgroundColor: Colors.white))`,
  no controller, no searcher, no overlay. PDFs render again.
  - `blobs_section.dart` extension-fallback dispatch (also from
    v1.0.517) is preserved.
  - Tags **v1.0.518, v1.0.519, v1.0.520 are all retired in
    spirit** — do not re-tag those numbers; their content
    regressed. Future TOC/search/links/etc. re-attempts will
    land as v1.0.522+, layered ONE feature per release with
    on-device verification before the next layer.

---

## v1.0.520-alpha — 2026-05-12

### Reverted

- **Backed out v1.0.519's PDF feature bundle** (tappable links + page
  badge + scroll thumb + double-tap zoom). Tester reported full-
  screen gray viewport on both seed-demo and uploaded PDFs after
  installing v1.0.519, even though CI + release builds went green
  — the regression is a runtime issue in pdfrx 2.2.24's interaction
  with `viewerOverlayBuilder` / `linkHandlerParams` API surface
  (those docs were drafted against the master / 2.3.x branch; the
  2.2.24 signatures may diverge in ways the compiler didn't catch).
  Pure revert of commit `520741a`; ships v1.0.518's state (TOC
  drawer + find-in-PDF working) under a fresh v1.0.520 tag.

  v1.0.519 number is retired — do not re-tag. Future re-attempts at
  the bundle (Issue: layer features one at a time, verify per-build
  on device before stacking) will land as v1.0.521+.

---

## v1.0.518-alpha — 2026-05-12

### Added

- **PDF outline / bookmarks / table-of-contents drawer** — tap the
  `menu_book` icon in the PDF viewer AppBar (only visible when the
  PDF carries an outline; most synthetic PDFs don't, real papers
  and books do) to slide in an end drawer listing the outline tree.
  Tap an entry → viewer jumps to that destination via
  `PdfViewerController.goToDest`. Renders nested levels with
  indentation; outline-less PDFs hide the icon entirely so it
  doesn't tease an empty drawer.
- **Find in PDF (text search)** — tap the `search` icon in the
  AppBar to enter search mode. Type a query, press search/return,
  and matches are highlighted in the viewport via pdfrx's
  `PdfTextSearcher.pageTextMatchPaintCallback`. AppBar shows the
  current match index (e.g. `3/12`) plus up/down arrows to step
  through matches. Close button restores the default AppBar and
  clears highlights. Case-insensitive by default.

Both features use pdfrx 2.2.24's high-level APIs (`PdfDocument.
loadOutline`, `PdfTextSearcher`) — no native code (`lib/widgets/
artifact_viewers/pdf_viewer.dart`).

---

## v1.0.517-alpha — 2026-05-12

### Fixed

- **PDF diagnostic strip showed "TIMEOUT" while content rendered
  fine** — pdfrx 2.2.24's `onDocumentLoadFinished` callback doesn't
  fire reliably on the success path even when pdfium renders the
  document; the 10 s watchdog from v1.0.515 then flipped the strip
  to red. Misleading for testers. The strip + watchdog served their
  purpose in v1.0.514–515 (identifying the 2.3.x native-assets
  regression); removed now that pdfium renders. If a future
  regression calls for it, resurrect from git history at commit
  `6dc5614`. (`lib/widgets/artifact_viewers/pdf_viewer.dart`)
- **Uploaded PDFs / images / text fell through to the share sheet
  instead of opening the viewer** — `_preview` checked the mime
  string only. `file_picker` sometimes returns a null extension
  and the upload pipeline then stores `application/octet-stream`;
  on tap, the PDF mime check failed and the row dropped to the
  share/save flow. Added a **filename-extension fallback** for the
  PDF / image / text dispatch paths — `foo.pdf` now opens in the
  PDF viewer even when the server's mime says `octet-stream`
  (`lib/screens/projects/blobs_section.dart`).

---

## v1.0.516-alpha — 2026-05-12

### Fixed

- **Canvas-app viewer flashed a white page on first open** — only on
  the very first time a canvas-app artifact was opened in a session;
  second open rendered instantly. Root cause: cold platform-view
  initialisation on Android. `WebViewWidget` mounted, native WebView
  process spun up with its default white background, then HTML
  rendered on top a few hundred ms later. Two-part fix:
  - `WebViewController.setBackgroundColor(0x00000000)` makes the
    WebView itself transparent so the OS default never paints.
  - `NavigationDelegate.onPageFinished` flips a `_pageReady` flag
    that fades out an opaque `DesignColors.canvasDark` overlay
    stacked above the WebView. The user sees the dark overlay
    during the cold-init window, then the canvas content fades in
    when the page is actually ready. 120 ms `AnimatedOpacity`
    crossfade for a smooth transition. Subsequent opens are
    same-frame since the platform-view process is already warm
    (`lib/widgets/artifact_viewers/canvas_viewer.dart`).

---

## v1.0.515-alpha — 2026-05-12

### Fixed

- **PDF viewer hangs at "pdfium: loading…" forever** — v1.0.514's
  diagnostic strip revealed the actual failure mode: bytes load
  fine and the viewer mounts, but pdfium's `onDocumentLoadFinished`
  callback never fires. The native pdfium library never wakes up.
  Root cause: pdfrx 2.3.0 migrated `pdfium_flutter` to Dart's
  native-assets link hooks for the native binary, which requires
  Flutter build-time experiment plumbing we don't have wired in
  CI. The 2.3.x line ships with a silent native-lib-not-loaded
  failure on our Android release builds.
  - **Pinned pdfrx to `2.2.24`** (the last 2.2.x release, using
    the prior platform-channel mechanism that "just works" out
    of the box). Decisive backout of the regression.
  - Removed the speculative `pdfrxFlutterInitialize()` call from
    `main.dart` — pdfrx docs say it's a no-op for widget use, and
    the canonical viewer example doesn't have it.
  - Reverted `useProgressiveLoading: false` to the default `true`
    (canonical example uses the default; v1.0.514's override
    didn't help).
  - Added a **10 s watchdog** in the diagnostic strip — if
    `onDocumentLoadFinished` doesn't fire by then, the strip
    flips to "pdfium: TIMEOUT (native lib likely not loaded)" so
    we never spend another release cycle on "loading…" with no
    signal (`pubspec.yaml`, `lib/main.dart`,
    `lib/widgets/artifact_viewers/pdf_viewer.dart`).

---

## v1.0.514-alpha — 2026-05-12

### Changed

- **PDF viewer now surfaces pdfium-side load state on screen** — four
  prior releases (.508/.510/.511/.512/.513) chased "white page" with
  speculative fixes (PDF `/Encoding`, page geometry, white viewport,
  `pdfrxFlutterInitialize()` call) and none moved the needle because
  we had no visibility into WHICH stage was failing. v1.0.514 wires
  `PdfViewerParams.onDocumentLoadFinished` + `onDocumentChanged`
  through to a diagnostic strip below the viewer that reports:
  bytes downloaded (KiB), pdfium load result (ok/failed), error
  string (if failed), and page count. Next tester screenshot tells
  us whether the breakdown is in the byte fetch, the pdfium open, or
  the rendering pipeline — no more guess-and-ship. Also disabled
  progressive loading (`useProgressiveLoading: false`) since our
  blobs are sub-25 MiB and the progressive path has been a known
  source of blank-page bugs upstream (pdfrx#617, merged 2026-05-08)
  (`lib/widgets/artifact_viewers/pdf_viewer.dart`).

---

## v1.0.513-alpha — 2026-05-12

### Fixed

- **Project list showed 100% progress on paper-demo despite an open
  AC** — `phases_done` counted every entry in `phase_history`,
  including the synthetic `from="" → first_phase` creation marker.
  For a paper-demo project with `pastPhases=[idea, lit-review,
  method, experiment]`, `buildPhaseHistory` emits 5 transitions:
  one creation + 4 real changes. `phasesDone = 5` over `phasesTotal
  = 5` rolled `progress` to 1.0 even though the paper isn't ratified.
  Tightened the consumer to count only transitions where `from !=
  ""` — i.e. real phase changes, not the creation marker. paper-demo
  now reads `4/5 + 0/2 AC ratio` → `progress = 0.8` (80%)
  (`hub/internal/server/handlers_insights.go`).

### Added

- **`N/M` phase counter on the project-list phase pill** — added
  `phase_index` + `phases_total` to `/v1/insights` `by_project[]`
  rows. The mobile `_PhasePill` now renders `Method 3/5` (compact,
  muted suffix) when both > 0, matching the dense `PhaseBadge` on
  the project detail header so the same at-a-glance progress info
  is reachable from the list without drilling in
  (`hub/internal/server/handlers_insights.go`,
  `lib/screens/projects/projects_screen.dart`).

## v1.0.512-alpha — 2026-05-12

Three follow-ups on v1.0.511 — one of them the real root cause for
the "PDF preview never works" thread.

### Fixed

- **PDF viewer: pdfrx was never initialized** — root cause for the
  v1.0.510/.511 "white/gray page on every PDF" reports (seed + real
  uploads alike). pdfrx 2.3.x docs require
  `pdfrxFlutterInitialize()` once at app startup before any
  `PdfViewer` widget builds; without it pdfium silently fails and
  pages render blank. Added the call in `main()` immediately after
  `WidgetsFlutterBinding.ensureInitialized()`
  (`lib/main.dart`). The /Encoding/WinAnsiEncoding + white-paint
  defenses from v1.0.510/.511 stay in place but are no longer
  load-bearing.
- **Lit-review demo project had no tabular citation artifact** —
  the v1.0.509 wiring for the References tile expects a tabular
  schema=citation artifact, but the citation seed was only wired
  into the *method-demo* project's deliverables. Testers opening
  the *lit-review-demo* project hit the document-only fall-back,
  which felt like "References = duplicated Documents." Added the
  same `seedCitationArtifact` call inside the lit-review-demo
  deliverables block so the tile lands on the tabular viewer in
  this project too
  (`hub/internal/server/seed_demo_lifecycle.go`).
- **Image viewer ran to the phone's bottom edge with no meta info**
  — added a footer strip showing the filename + intrinsic
  dimensions (`W×H`) + byte size, resolved via `ui.instantiateImageCodec`
  after the bytes load. Strip lives in the fullscreen viewer's
  `Scaffold` body (`Column[Expanded(viewer), MetaStrip]`); inline
  uses of `ArtifactImageViewer` (without `onMeta`) stay footer-less.
  Also gives the image breathing room above the phone bottom bar
  (`lib/widgets/artifact_viewers/image_viewer.dart`).

## v1.0.511-alpha — 2026-05-12

Six fixes — five from the visual/UX walkthrough of v1.0.509, plus a
"may not reproduce" snippet-loss race that was diagnosed between
v1.0.509 and the walkthrough.

### Fixed

- **Snippets saved from the compose box sometimes disappear after
  leaving + reentering the terminal page** — a "may not reproduce
  every time" tester report. Root cause: `SnippetsNotifier.build()`
  fire-and-forgets `_loadSnippets()` to hydrate state from
  SharedPreferences; if the user reached "save as snippet" before
  that load resolved, `addSnippet` set state to `[newSnippet]` and
  awaited `_save()`. While `_save` was suspended on
  `SharedPreferences.getInstance()`, `_loadSnippets` resumed first
  (it had awaited that same future earlier), read the still-empty
  on-disk list, and set state back to `[]`. `_save` then resumed and
  serialized that empty list back to prefs — the new snippet was lost
  from both memory and disk. Fixed by gating every public mutator on
  a `_ready` future that completes when `_loadSnippets` finishes
  (`lib/providers/snippet_provider.dart`). Same pattern is suspected
  in 8 other prefs-backed providers — audit deferred (see
  memory `feedback_prefs_load_race.md`).

- **Image viewer rendered uploaded photos at intrinsic size and ran
  off-screen** — `InteractiveViewer` feeds tight constraints to its
  child, but the `Center` wrapper re-loosened them, so
  `Image.memory(fit: BoxFit.contain)` never engaged and a 4000×3000
  photo rendered at 4000×3000. Replaced `Center` with
  `SizedBox.expand` so the image fits on first paint; pinch-zoom and
  pan still work via `InteractiveViewer`
  (`lib/widgets/artifact_viewers/image_viewer.dart`).
- **PDF preview still rendered "empty/gray" for both the seed PDF
  *and* uploaded PDFs** — the prior /Encoding fix wasn't enough.
  Wrapped `PdfViewer.data` in a white `ColoredBox` (so a transparent
  page background can't blend into the Scaffold's gray and read as
  "empty") + passed an explicit `PdfViewerParams(backgroundColor:
  Colors.white)`. If pdfrx is in fact rendering but to a transparent
  page, this exposes the text; if it's failing earlier, the white
  background still beats the gray scaffold for telling testers it's
  broken vs invisible. The next gray-report will isolate which side
  is at fault (`lib/widgets/artifact_viewers/pdf_viewer.dart`).
- **Code-bundle viewer had a "fixed-width" band on folded phones** —
  HighlightView's atom-one theme background only painted the
  intrinsic content width; if the longest line was narrower than the
  viewport, the right edge showed Scaffold color, which read as
  "code block has a fixed width." Wrapped the inner horizontal
  scroll in `LayoutBuilder + ConstrainedBox(minWidth: viewport)` so
  the theme background paints the full width while pan still works
  when lines exceed it
  (`lib/widgets/artifact_viewers/code_bundle_viewer.dart`).
- **Canvas (eval curve) seed was too thin and rendered in a small
  top-left region on folded phones** — replaced the fixed
  `<svg width="320" height="200">` single-curve seed with a richer
  train+val dual-line chart: SVG `viewBox` + `width:100%` for
  responsive scaling, gridlines, axis labels, legend with toggle
  chips, hover crosshair, click-to-pin readouts, and a summary
  table. Flattened the styling to a single background after the
  tester saw the body/card/chartwrap three-tone layering as the
  same "viewport-in-viewport" vibe as the B4 code-block report —
  chart is now edge-to-edge, divider lines do the chrome work
  (`hub/internal/server/seed_demo_lifecycle.go`).
- **Paper-demo seed had no open AC** — the `paper-draft-ratified`
  gate was seeded with `state: "waived"`, which represents a
  deliberate director-deferral, not "waiting on the deliverable."
  The paper-draft deliverable is `in-review`, so the gate is
  genuinely PENDING. Flipped the state to `pending` so the
  paper-demo's AC list now shows one open required criterion. Added
  a non-required `supplementary-materials-packaged` text criterion
  with `state: "waived"` alongside so the bundle still exercises
  the waived-state UI (the test that asserts ≥1 waived across the
  5 projects stays green)
  (`hub/internal/server/seed_demo_lifecycle.go`).
- **References tile in lit-review felt indistinguishable from
  Documents tile** — the prior tap path always went through an
  intermediate "kind=tabular schema=citation" artifact-list screen,
  which on a single-citation project rendered a one-row list with
  the filename and no visible table. The screen pattern looked like
  Documents to testers. Now the tap-References handler peeks at the
  matching artifacts first: exactly one citation artifact → push the
  tabular viewer directly so the table renders on first paint; >1 →
  the list-by-kind screen for disambiguation; lit-review deliverable
  but no citations → the structured deliverable viewer; else
  fall-back to DocumentsScreen with the same snackbar
  (`lib/widgets/shortcut_tile_strip.dart`).

## v1.0.509-alpha — 2026-05-12

Follow-up bug batch from the v1.0.508 tester report. Four small fixes
on the artifact-viewer + project-header surface.

### Fixed

- **Artifact viewers — tap row goes straight to fullscreen, not via
  metadata sheet** — testers reported "canvas / code-bundle viewport
  still not full screen" because the prior tap path opened a 55%-high
  bottom sheet, then required tapping "Open canvas" / "Open code" to
  push the actual viewer. The two-step felt like the viewer itself was
  constrained. Now: tap → fullscreen viewer route directly when the
  kind has one (`pdf`, `tabular`, `image`, `code-bundle`, `audio`,
  `video`, `canvas-app`, `metric-chart`); long-press still opens the
  metadata sheet for uri/sha/lineage. Same dispatch is reused by
  `showArtifactDetailSheet()` so the StructuredDeliverableViewer's
  component-card tap follows the same fast path
  (`lib/screens/projects/artifacts_screen.dart`).
- **Seed PDF still rendered empty/gray** — v1.0.508 fixed the page
  size (300×80 → US-Letter) but pdfium still produced no glyphs in
  some builds because the Helvetica font object had no `/Encoding`
  entry. The standard-14 fonts are nominally implicit-encoding but
  not every pdfium build picks that up. Added explicit
  `/Encoding/WinAnsiEncoding` to the font dict and `/ProcSet[/PDF/Text]`
  to the page resource dict, repositioned the text so it lands near
  the top of the visible fit-to-width window
  (`hub/internal/server/seed_demo_lifecycle.go`).
- **Assets tile — uploaded PDFs / markdown not previewable** — the
  blob tile's `onTap` always launched the system share/save flow, so
  testers couldn't verify the PDF / markdown viewer with their own
  uploads (and `_guessMime` returned `application/octet-stream` for
  `.pdf` regardless). Now `onTap` dispatches by mime:
  - `application/pdf` → `ArtifactPdfViewerScreen`
  - `image/*` → `ArtifactImageViewerScreen`
  - `text/markdown` / `text/plain` / `application/json` /
    `application/yaml` → new `BlobTextViewerScreen` (flutter_markdown
    for `text/markdown`, monospace `SelectableText` otherwise)
  - Anything else → falls back to the download/share flow
  
  Added `pdf → application/pdf` to `_guessMime` so the upload itself
  records the right mime
  (`lib/screens/projects/blobs_section.dart`).
- **Project detail header — dropped Project/Workspace kind chip; phase
  badge regained N/M counter** — testers wanted more horizontal space
  for the phase badge in the AppBar title row, and missed the
  at-a-glance progress info that dense mode had stripped. Removed
  `ProjectKindChip` from the title row (kind is still surfaced by the
  side-tab and template chip elsewhere); added the position counter
  back into dense `PhaseBadge` (compact form: `Method 3/5` instead of
  `Method · 3/5 ›` — chevron stays hidden in dense mode since the
  whole pill is the tap target)
  (`lib/screens/projects/project_detail_screen.dart`,
  `lib/widgets/phase_badge.dart`).

## v1.0.508-alpha — 2026-05-12

Tester-reported bug batch from the v1.0.507 seed-demo walkthrough.
Six small, independent fixes — no design changes.

### Fixed

- **Asset upload (project Assets tile)** — first upload on a fresh
  device threw "Cannot remove from an unmodifiable list". `BlobCache.list()`
  returned `const []` on cold start; `add()` then called `removeWhere`
  on it. Replaced all three `const []` returns with growable
  `<BlobRecord>[]` so the dedup-then-prepend flow works on the empty
  case (`lib/services/hub/blob_cache.dart`).
- **Tasks tab priority filter** — selecting "Any priority" was
  unreachable after picking a concrete value. Flutter's
  `PopupMenuButton<T?>` conflates a `null` selection with cancellation
  (`onSelected` never fires for a `value: null` item). Switched the
  menu to `PopupMenuButton<String>` with `'any'` + wire-string values
  and translated back to `TaskPriority?` in the handler
  (`lib/screens/projects/project_detail_screen.dart`).
- **Overview hero: cannot re-pick the phase template default** —
  symptom: switching to any non-default hero worked, but picking
  `experiment_dash` (which IS the research.v1 experiment-phase
  template default) silently kept the previous override. Two stacked
  causes:
  1. (mobile) `ShortcutTileStrip`'s non-empty-tiles branch dropped
     the `onProjectChanged` callback, so the PATCH response never
     reached the parent `_project` snapshot — plumbed it through
     both branches (`lib/widgets/shortcut_tile_strip.dart`).
  2. (hub) The "clear the override" path posts
     `overview_widget_overrides: null`, but `projectPatch`'s typed
     `*json.RawMessage` field can't tell "absent" from "explicit
     null" — Go's JSON decoder nullifies the pointer on either, so
     the handler's `if != nil` check skipped the UPDATE and the
     previous override stuck in the row. Switched
     `PhaseTileOverrides` + `OverviewWidgetOverrides` to non-pointer
     `json.RawMessage` (presence detected via `len > 0`) and added a
     `clearableRawJSON` helper that maps both `null` and `{}` onto
     SQL NULL so the column truly resets
     (`hub/internal/server/handlers_projects.go`).
- **No pull-to-refresh on project Overview** — added `RefreshIndicator`
  around the Overview `ListView` plus a new `HubClient.getProject(id)`
  helper that fetches the single-project endpoint (resolved
  `overview_widget`, `phase_tiles_template`, …) and pushes the result
  through `onProjectChanged`. Closes the "have to nav back and in to
  see the customized result" gap
  (`lib/screens/projects/project_detail_screen.dart`,
  `lib/services/hub/hub_client.dart`).
- **Seed PDF rendered as a tiny gray strip** — the synthetic lifecycle
  seed PDF used a 300×80-pt MediaBox; pdfrx scale-to-fit on a phone
  rendered the page so small that testers read it as "totally empty /
  gray". Bumped MediaBox to US-Letter (612×792), upsized the title
  to 32pt, and added two subtitle lines so the demo PDF is legible at
  default zoom (`hub/internal/server/seed_demo_lifecycle.go`).
- **Code-bundle + canvas viewers landed as sub-screens** —
  `_ArtifactViewerLauncher` pushed its fullscreen viewer route via
  `Navigator.of(context)`; when the artifact detail sheet was opened
  from inside a nested-Navigator route (e.g.
  `StructuredDeliverableViewer`), the new screen rendered as a
  constrained sub-rectangle. Switched
  `showArtifactDetailSheet`'s `showModalBottomSheet` to
  `useRootNavigator: true` so launcher pushes land on the top-level
  Navigator. Also wrapped canvas `WebViewWidget` in `SizedBox.expand`
  so the platform view pins to the parent's full constraints rather
  than collapsing to intrinsic size on some Android builds
  (`lib/screens/projects/artifacts_screen.dart`,
  `lib/widgets/artifact_viewers/canvas_viewer.dart`).
- **Phase badge stole a whole row under the project name** — moved
  the badge into the AppBar title row alongside the project-kind chip
  via a new `PhaseBadge(dense: true)` mode (shrunk padding + font;
  drops the `N/M` position counter and trailing chevron). The
  body-Column version still renders with the original geometry for any
  caller that wants the full-width pill
  (`lib/widgets/phase_badge.dart`,
  `lib/screens/projects/project_detail_screen.dart`).

---

## v1.0.507-alpha — 2026-05-11

W4 + W5 of [`multi-run-experiment-phase`](plans/multi-run-experiment-phase.md):
retire the legacy ablation seed shape + supporting templates, then
catch the load-bearing docs up. The `seed-demo --shape lifecycle`
path (v1.0.505) is now the only supported seed flow; the single-
project ablation-sweep demo and its two project templates
(`ablation-sweep`, `benchmark-comparison`) are gone.

### Removed

- **`hub/cmd/hub-server` `--shape ablation` branch** — flag is now
  `lifecycle`-only. Unknown values report a clear error citing the
  v1.0.507 retirement.
- **`hub/internal/server/seed_demo.go`** — slimmed to `insertDemoBlob`
  only (renamed to `seed_demo_blob.go`). All ablation-specific seed
  code (`SeedDemo`, `ResetDemo`, `SeedDemoResult`,
  `drawCheckpointPNG`, etc.) deleted.
- **`hub/internal/server/seed_demo_test.go`** — exercised the deleted
  ablation seed; removed.
- **`hub/templates/projects/ablation-sweep.yaml`,
  `benchmark-comparison.yaml`** — both retired. `research.v1` covers
  the same shape natively (one experiment-results deliverable with
  N runs + aggregate metric-chart).
- **`hub/templates/prompts/steward.v1.md`** — "Decomposition recipe:
  ablation sweep" + "Decomposition recipe: benchmark-comparison"
  sections deleted; the lifecycle template's recipes are in the
  research.v1 prompt.

### Added

- **`hub/internal/server/seed_demo_run_curves.go`** — carries
  `synthRunCurves`, `synthLossCurve`, `demoCurve`, `roundTo` out of
  the retired `seed_demo.go`. The lifecycle seed still uses them
  for per-run `run_metrics` synthesis.
- **`hub/internal/server/seed_demo_blob.go`** — single-helper file
  retaining `insertDemoBlob`. Header comment explains the historic
  shrink.
- **Hub regression test** `TestInit_RetiredTemplatesAreGone` —
  fails loudly if either `ablation-sweep` or `benchmark-comparison`
  templates re-appear in the init seed.

### Changed

- **`hub/cmd/mock-trainer` default `--project`** — was
  `ablation-sweep-demo`, now `mock-trainer-demo`. The tool is
  unchanged otherwise.
- **`hub/templates/projects/research.v1.yaml`** — the experiment-
  phase `run` component's `ref` renamed from `ablation-sweep-run`
  to `sweep-run`. Cosmetic, but the YAML no longer references the
  retired template name.
- **Docs updated to v1.0.507 reality:**
  - `docs/decisions/024-project-detail-chassis.md` — D2 hero list
    shrinks to 8; "Amended 2026-05-11" block records the
    `sweep_compare` retirement rationale.
  - `docs/reference/project-detail-chassis.md` — hero registry
    table + archetype list updated; `experiment_dash` row notes it
    now covers single-run + N-run sweeps.
  - `docs/spine/blueprint.md` P4.1 — names `research.v1` as the
    canonical demo template; calls out the ablation/benchmark
    retirement.
  - `docs/how-to/release-testing.md` §0.1 — replaces the
    `ablation-sweep-demo` seed example with the lifecycle pointer;
    mock-trainer example updated.
  - `docs/how-to/local-dev-environment.md` §3.4 + §6 — same
    substitution.
  - `docs/plans/demo-script.md` status line — flagged for an
    ablation-grid sweep through; arc unchanged.

### Migration note

Existing demo data seeded under the legacy path keeps working — the
hub doesn't auto-migrate; users with an ablation-sweep-demo project
in their dev DB can leave it (read-only via mobile) or
`hub-server seed-demo --shape lifecycle --reset` to swap.

---

## v1.0.506-alpha — 2026-05-11

W3 of [`multi-run-experiment-phase`](plans/multi-run-experiment-phase.md):
drop `sweep_compare`. The 3-series metric-chart `experiment_dash`
embeds (v1.0.503+) subsumes the cross-run scatter use case, so the
extra hero was redundant.

### Removed

- `lib/screens/projects/overview_widgets/sweep_compare.dart` —
  hero file deleted.
- `lib/widgets/sweep_scatter.dart` — sole caller was sweep_compare;
  deleted with it.
- `sweep_compare` slug from `kKnownOverviewWidgets`,
  `kOverviewWidgetSpecs`, mobile dispatch, and the hub-side
  `validOverviewWidgets` enum. Templates that still declare
  `overview_widget: sweep_compare` (ablation-sweep,
  benchmark-comparison) degrade to `task_milestone_list` until W4
  retires those templates entirely.

### Added

- Mobile + hub regression tests for the retired slug, mirroring the
  v1.0.501 `portfolio_header` guard. Both layers fail loudly if
  `sweep_compare` returns to the closed set.

---

## v1.0.505-alpha — 2026-05-11

W1+W2 of [`multi-run-experiment-phase`](plans/multi-run-experiment-phase.md):
the research lifecycle's experiment phase now embodies the multi-run
shape it always implied. One `experiment-results` deliverable, three
runs (n_embd ∈ {128, 256, 384} × lion), three per-run metric-charts,
three per-run checkpoints, plus one aggregate metric-chart that
overlays all series. The mobile `experiment_dash` embed picks the
aggregate via its newest-first picker and renders the 3-series
comparison inline. Wedges W3 (drop `sweep_compare`), W4 (retire
ablation shape + templates), and W5 (ADR-024 amendment + docs)
follow.

### Changed

- **Template `research.v1` experiment phase** — `experiment-results`
  components grow to: `document, eval-aggregated, best-checkpoint,
  eval-per-run, ablation-sweep-run`. The criterion comment notes
  threshold applies to max-across-runs from the aggregate chart.
- **Seed `seed-demo --shape lifecycle`** — both demo sites (mid-
  lifecycle draft, late-lifecycle ratified) now loop over
  `defaultSweepConfigs` (3 entries) producing per-run runs +
  per-run charts + per-run checkpoints; one aggregate chart is
  seeded last with a strictly-later `created_at` (via `timeAfter`)
  so the mobile newest-first picker lands on it.
- **`seedMetricChartArtifact` signature** — now accepts an explicit
  body + `createdAt`, so callers can vary per-run shapes and
  assign a strictly-later timestamp to the aggregate.

### Added

- `sweepRunConfig`, `defaultSweepConfigs`, `sweepRunLabel`,
  `generateSweepPoints`, `demoPerRunMetricChartBody`,
  `demoAggregateMetricChartBody`, `timeAfter` helpers in
  `seed_demo_lifecycle.go`.

---

## v1.0.504-alpha — 2026-05-11

Three more in-hero typed embeds, completing the wave-3 first pass:
every research-phase hero now surfaces its load-bearing content
inline instead of pointing the operator to dig for it.

### Added

- **`paper_acceptance` PDF embed** — `PaperAcceptanceHero` fetches the
  newest `kind=pdf` artifact for the project and renders page 1 in a
  220 px constrained `PdfViewer.data` (gestures locked via
  `IgnorePointer`). Tap → `ArtifactPdfViewerScreen`. Silent when no
  PDF exists yet.
- **`deliverable_focus` next-section embed** — `DeliverableFocusHero`
  walks the loaded overview's `deliverables[0].components` for a
  `document` ref, fetches the document, parses its sections, and
  surfaces the first non-ratified section as a card: title + 3-line
  preview + ratified/total count. Tap → `SectionDetailScreen` so the
  director can read + ratify without first opening the structured
  viewer.
- **`idea_conversation` scope criterion embed** — `IdeaConversationHero`
  fetches `listProjectCriteria(phase: 'idea')`, picks the first pending
  criterion (falls back to most-recent for context), and renders it
  inline with the same Mark met / Mark failed / Waive sheet the full
  deliverable viewer uses.

### Changed

- `_PhaseHero.extras` (added in v1.0.503) replaced with
  `extrasBuilder: Widget Function(BuildContext, Map<String,dynamic>? overview)?`.
  Builders receive the already-loaded overview map so deliverable-aware
  embeds skip a second fetch; the metric-chart embed continues to
  ignore the param.

---

## v1.0.503-alpha — 2026-05-11

First in-hero typed-artifact embed: the `experiment_dash` hero now
shows the project's metric-chart inline below its deliverables list,
tap-through to the fullscreen viewer. Removes the "open Outputs,
find the chart, tap it" round-trip that the v1.0.502 review surfaced
as the most visible chassis-vs-content gap on the demo's eval-results
moment.

### Added

- **`MetricChartInline` + public `MetricChartPainter`**
  (`lib/widgets/artifact_viewers/metric_chart_viewer.dart`) — pure-paint
  widget that draws axes + polylines + legend from an already-parsed
  `MetricChartBody`. Accepts `expand: true` for fullscreen use or a
  `collapsedHeight` for inline use. The fullscreen viewer now composes
  this widget instead of inlining the painter.
- **`_PhaseHero.extras` slot**
  (`lib/screens/projects/overview_widgets/research_phase_heroes.dart`)
  — optional widget rendered below the deliverable list, owned by
  the hero variant. Keeps the loading/scaffold logic centralised
  while letting individual phase heroes hang typed previews.
- **`ExperimentDashHero` metric-chart embed** — fetches the newest
  `kind=metric-chart` artifact for the project via
  `listArtifactsCached`, downloads the blob, parses through the shared
  `parseMetricChart`, and renders `MetricChartInline` inside a tap-
  enabled card. Tap → `ArtifactMetricChartViewerScreen`. Silent when
  no chart exists yet, so phases that haven't produced one show
  nothing rather than an empty card.

### Changed

- `parseMetricChart` is now a fully public function (was
  `@visibleForTesting`); heroes embedding charts inline use the same
  parser as the fullscreen viewer.
- `_MetricChartPainter` renamed to `MetricChartPainter`. No
  behavioural change; required for reuse from the inline widget.

### Tests

- `test/widgets/metric_chart_viewer_test.dart`: two new tests cover
  `MetricChartInline` — title visibility toggling, painter presence,
  legend rendering.

---

## v1.0.502-alpha — 2026-05-11

`metric-chart` artifacts now render a graph. Closes the
biggest functional gap exposed by the v1.0.501 seed review — the
kind existed in the closed-set registry and the seed attached it,
but the body was a mock URI with no real bytes and there was no
mobile viewer, so testers saw the chip and tapped nothing.

### Added

- **MetricChartViewer** (`lib/widgets/artifact_viewers/metric_chart_viewer.dart`)
  — `ArtifactMetricChartViewer` + `ArtifactMetricChartViewerScreen`
  download an AFM-V1-style JSON blob via
  `HubClient.downloadBlobCached`, parse it, and draw a native line
  chart with axes + grid + per-series legend via `CustomPaint`. No
  new dependencies; stdlib `dart:math` only. Wire shape (locked
  v1):
  ```
  {
    "version": 1,
    "title": "Eval accuracy",
    "x_label": "Step",
    "y_label": "Accuracy",
    "series": [
      {"name": "eval_accuracy", "color": "#ff00aa?",
       "points": [[0, 0.50], [100, 0.62], ...]}
    ]
  }
  ```
  Multi-series + optional hex color per series; brand palette cycles
  by index when color is omitted. Parser is tolerant of malformed
  points (dropped) and missing labels.
- **`_ArtifactViewerLauncher.metricChart` branch** — `Open chart`
  button on `metric-chart` rows; routes to the new screen. Filter
  pill in `artifacts_screen.dart` already included the kind.
- **Hub seed: real JSON bytes for the metric-chart artifact.** New
  `seedMetricChartArtifact` replaces the prior `seedArtifact(...,
  "metric-chart", ...)` shortcut at both demo project sites. Body
  is `demoMetricChartBody()` — 11-point accuracy curve from
  step=0 (acc=0.50) to step=1000 (acc=0.88). Real bytes via
  `insertDemoBlob`; same `blob:sha256/…` URI shape as the other
  wave-2 typed artifacts so the viewer round-trips through the
  standard blob endpoint.
- **Lifecycle seed coverage now 8 of 11** closed-set kinds:
  + metric-chart (joins code-bundle, canvas-app, tabular,
  external-blob, pdf, image; intentional gaps: audio + video
  upload-only, diagram post-MVP, prose-document via documents).

### Fixed

- **`overview_widgets_registry_test.dart` no longer asserts
  `portfolio_header` is in `kKnownOverviewWidgets`.** The slug
  retirement in v1.0.501 left the test stale; CI flagged it on
  push. Test now asserts the inverse (regression guard against the
  slug sneaking back).

---

## v1.0.501-alpha — 2026-05-11

First-pass hero consolidation + lifecycle seed reaches PDF + image
viewer coverage. Sets up Wave 3 hero redesign without changing the
research demo's surface layout.

### Removed

- **`portfolio_header` hero slug** — retired both sides. It was a
  no-op pointer rendering an explanatory paragraph in front of the
  chassis-A header that already sat directly above it. ADR-024 D2
  noted the boundary confusion; v1.0.501 drops the slug from both
  mobile `kKnownOverviewWidgets` and hub `validOverviewWidgets`.
  Templates that named it as `default_overview_widget` (only
  `research.v1.yaml` in tree) now fall through to the chassis
  default (`task_milestone_list`); per-phase heroes are unaffected.
  `PortfolioHeaderHero` class deleted from
  `research_phase_heroes.dart`. `sweep_compare` stays for now —
  `benchmark-comparison.yaml` and `seed-demo --shape ablation`
  depend on it; consolidation is a follow-up wedge.

### Added

- **Lifecycle seed: PDF + PNG artifacts on the experiment-results
  deliverable.** New helpers `seedPdfArtifact` (builds a small valid
  PDF at runtime via `fmt.Sprintf` so xref offsets stay accurate)
  and `seedImageArtifact` (encodes a 128×64 magenta/cyan PNG via
  `image/png`) attach to both demo project variants. Testers can
  now exercise the wave 2 W2 (pdfrx) and W4 (image) viewers via
  `seed-demo --shape lifecycle` instead of uploading their own
  bytes. Audio + video stay manual-upload by design; diagram is
  post-MVP (no viewer exists yet).
  - Closed-set artifact-kind coverage in the lifecycle seed:
    `code-bundle`, `canvas-app`, `tabular`, `metric-chart`,
    `external-blob`, `pdf`, `image` — **7 of 11**. Remaining four
    (`audio`, `video`, `diagram`, `prose-document` as artifact) are
    not seeded; `prose-document` is intentional (the demo uses the
    typed `documents` table instead).

### Docs

- `docs/reference/template-yaml-schema.md` and
  `docs/reference/research-template-spec.md` examples switched
  `default_overview_widget: portfolio_header` →
  `default_overview_widget: task_milestone_list`.
- `docs/reference/project-detail-chassis.md` §2 footnote updated
  to record the v1.0.501 removal.

---

## v1.0.500-alpha — 2026-05-11

Project detail chrome cleanup. Compact phase indicator + collapsed
AppBar actions reclaim ~24px of vertical space and make narrow-phone
titles legible. ADR-024 D4 updated to reflect the swap.

### Changed

- **Phase indicator → compact badge.** Inline `PhaseRibbon` (56px,
  scrollable) replaced by `PhaseBadge` (~32px pill, e.g.
  `Method · 3/5 ›`) in
  `lib/screens/projects/project_detail_screen.dart`. Tap opens a
  bottom sheet that hosts the existing `PhaseRibbon` so per-phase
  navigation stays one extra tap away.
  `PhaseRibbon` retained verbatim (own tests + sheet caller); new
  widget at `lib/widgets/phase_badge.dart`. Pattern reference:
  Linear / Jira / Notion status badge.
- **AppBar actions consolidated.** Edit pencil and View-template-YAML
  IconButtons folded into the existing `more_vert` overflow which
  previously held only `New sub-project`. Title row (project name +
  kind chip) now has headroom on narrow phones; the overflow menu
  finally lives up to its tooltip.

### Internal

- ADR-024 D4 status, ASCII diagram, consequences, and reversibility
  row updated to reflect `PhaseBadge` (was `PhaseRibbon`). Follow-up
  wedges section marked wave 2 (artifact-type-registry W1–W7) and
  canvas-viewer plan as ✅ shipped.
- `docs/reference/project-detail-chassis.md` layout diagram +
  file table updated to list `phase_badge.dart` alongside
  `phase_ribbon.dart`.
- `docs/spine/information-architecture.md` §6.2 phase-indicator
  bullet updated.

---

## v1.0.499-alpha — 2026-05-11

Four small UX corrections + a save-refresh wiring fix. All four
were direct feedback from device testing.

### Changed

- **Overlay backfill targets 5 user turns, not 50 events.** Tool-
  heavy steward turns fan out to 30+ events apiece, so the original
  50-event budget surfaced 1–2 visible turns on chats with frequent
  tool calls. `_backfillEventCeiling` raised to 500, new
  `_backfillTurnTarget = 5` walks the newest-first event list and
  cuts at the (target+1)th user input. `_overlayMessageCap` raised
  to 15 to hold the initial backfill view without immediate
  eviction. Mobile-only change — no hub work.
  (`lib/widgets/steward_overlay/steward_overlay_controller.dart`)
- **Phaseless projects can now customize tiles + hero.** Manually-
  created projects (no template) had a Customize affordance that
  bailed silently because the open-handler checked
  `phase.isEmpty`. Empty string is now a valid phase key for both
  `phase_tile_overrides_json` and `overview_widget_overrides_json`;
  hub `resolveOverviewWidget` consults `overrides[""]` when the
  project has no phase. `_showHeroPicker` always-true now (manual
  projects resolve to the default hero, so there's always
  something to swap). Mobile + hub.
- **Hosts page grouped into HUB + Personal.** Single "Hosts" list
  split into two sections: HUB section now nests the hub-registered
  hosts as 32px-indented children under the existing HubTile;
  Personal section holds local-only bookmarks. Each section has its
  own collapsible header + inline empty-state hint; full
  `_EmptyState` only fires when both are empty.
  (`lib/screens/hosts/hosts_screen.dart`)
- **Tasks tab — one filter row, group by status.** Two filter rows
  (status pills + priority pills) collapsed into one
  (`_TaskFilterBar`): status pills on the left, priority as a
  compact `Icon(filter_list)` popup tinted by the active selection
  on the right. When no status filter is active, the list groups by
  status with section headers (`todo` → `in_progress` → `blocked` →
  `done`) — Linear / Asana mobile pattern. Per-row
  `_StatusDot` and trailing status text removed (status is implied
  by the section header or active pill); `TaskPriorityDot` is the
  sole color cue per row. Dead-code: `_StatusDot` class +
  `_TaskFilterPill.leadingDot` field deleted.
  (`lib/screens/projects/project_detail_screen.dart`)

### Fixed

- **Customize sheet → Save now refreshes the project detail.** The
  PATCH succeeded but `PhaseTileEditorSheet._save` popped without a
  body, so `_ProjectDetailScreenState._project` stayed stale and
  the strip rebuilt with the same props (from the user's POV: the
  sheet closed and nothing changed). Plumbed a single
  `ValueChanged<Map<String, dynamic>>? onProjectChanged` callback:
  `PhaseTileEditorSheet._save` / `_reset` pop the updated body →
  `_CustomizeTilesRow._open` fires `onProjectChanged` and triggers
  `hubProvider.refreshAll()` → `ShortcutTileStrip` →
  `_OverviewView` → `_ProjectDetailScreenState` updates `_project`
  with `setState`. Mirrors the pattern the project Edit sheet
  already used.

---

## v1.0.498-alpha — 2026-05-11

Canvas-viewer plan W2+W3+W4: sandboxed WebView for `canvas-app`
artifacts. The 11th and final closed-set kind from
artifact-type-registry W1 now has a viewer. Trust-the-agent
read-only interaction model (clicks/plays stay WebView-local; no
agent bridge). AFM-V1 (shipped W1 the same day) is the body schema;
canvas is its second user after code-bundle.

### Added

- **Canvas viewer** (`lib/widgets/artifact_viewers/canvas_viewer.dart`)
  — `ArtifactCanvasViewer` + `ArtifactCanvasViewerScreen`. Downloads
  the AFM-V1 blob, parses via the shared
  `parseArtifactFileManifest`, then merges sub-files into a single
  self-contained HTML document by rewriting `<script src>`,
  `<link rel="stylesheet" href>`, and `<img src>` against the
  manifest (Q13 resolution rules: strip `./`, exact match, reject
  `..`/leading-`/`/scheme). Loaded via
  `WebViewController.loadHtmlString(html, baseUrl: 'about:blank')`.
- **Navigation delegate** (W4) — `decideCanvasNavigation` permits
  `about:blank`, `data:` URIs, and HTTPS against a fixed CDN
  allowlist (`cdn.jsdelivr.net`, `unpkg.com`,
  `cdnjs.cloudflare.com`, `esm.sh`); everything else returns
  `NavigationDecision.prevent`. No CSP injection (Q8 locked).
- **Launcher + filter pill** (`artifacts_screen.dart`) — `Open
  canvas` button on `canvas-app` rows; `canvas-app` joins the
  closed-set filter pill row.
- **Demo seed** (`hub/internal/server/seed_demo_lifecycle.go`) —
  `demoCanvasBundle()` returns a 3-file AFM-V1 (index.html +
  chart.js + style.css) rendering an interactive SVG line chart over
  the synthetic eval data. `seedCanvasArtifact()` materialises it on
  the ratified experiment-results deliverable in both demo projects;
  blob dedups across them.
- **Refresh affordance** — fullscreen route's AppBar carries a
  `Reload canvas` icon button that remounts the viewer (lazy
  re-download); auto-detection via `listArtifactsCached` deferred
  per Q5.

### Changed

- **`webview_flutter`** added to `pubspec.yaml` (`^4.10.0`). ~2 MB
  APK cost on Android — re-examine under
  artifact-type-registry Q10 if APK split lands.

---

## v1.0.497-alpha — 2026-05-11

Wave 2 W7.2 of artifact-type-registry: true multimodal attach for PDF /
audio / video. Closes the artifact-type-registry plan (W1-W7 all
shipped). PDF is cross-engine (Claude `document`, Codex `file_data`,
Gemini ACP `resource`); audio/video are Gemini-only.

### Added

- **Hub validator** (`handlers_agent_input.go`) — per-modality MIME
  allowlists + size caps mirroring image:
  PDF ≤32 MB / ≤1 per turn (`application/pdf` only);
  audio ≤20 MB / ≤1 per turn (mp3/m4a/wav/webm/ogg/aac/flac);
  video ≤20 MB / ≤1 per turn (mp4/webm/quicktime). New
  `attachmentInput` shape carries `{mime_type, data, filename}`;
  `validateAttachments` generalises image validation across the
  three modalities. Payload persists to `payload_json["pdfs"]` etc.
- **Family registry** (`agent_families.yaml` + `families.go`) —
  `PromptPDF` / `PromptAudio` / `PromptVideo` flag maps mirror
  `PromptImage`. Claude + Codex declare `prompt_pdf` on M1+M2;
  Gemini declares all four (`prompt_image` / `pdf` / `audio` /
  `video`) on M1 only.
- **Driver wire mapping** (`driver_stdio.go` / `driver_appserver.go`
  / `driver_acp.go` / `driver_exec_resume.go`) — each driver lowers
  the canonical attachment list into its engine's content-block
  shape:
  - Claude `document` block (`type: document, source: {type:
    base64, media_type, data}, title: filename`).
  - Codex `input_file` block (`type: input_file, file_data:
    data:application/pdf;base64,..., filename`).
  - Gemini ACP `resource` block (`type: resource, resource: {uri:
    data:<mime>;base64,..., mimeType}`) for PDF and video; `audio`
    block (`type: audio, mimeType, data`) for audio.
  - gemini exec-per-turn (M2) strips all four modalities and emits
    a `kind=system` warning matching the existing image strip path.
- **Mobile composer** (`agent_compose.dart` +
  `steward_overlay/steward_overlay_chat.dart`) — new `_pickMultimodal`
  flow with kind picker when the family supports >1 modality.
  `_canAttachPdfs` / `_canAttachAudio` / `_canAttachVideo` flags
  resolved alongside `_canAttachImages` from the family registry.
  Pending attachments render as `InputChip`s above the input
  toolbar; per-modality caps mirror the hub-side caps so the
  composer clamps before send.
- **`HubClient.postAgentInput`** — new optional `pdfs` / `audios` /
  `videos` named parameters carry the attachment maps to the hub.
- **`composer_multimodal_attach.dart`** — shared
  `pickMultimodalFile(MultimodalKind)` helper plus
  `MultimodalAttachment`, `MultimodalAttachError`, MIME allowlists,
  extension allowlists. Pulled into a parallel module to
  `composer_image_attach.dart` since the modalities are
  pass-through (no compression) and the picker filters by
  extension.

### Test

- **`hub/internal/server/handlers_agent_input_test.go`** —
  `TestPostAgentInput_PdfHappyPath`,
  `TestPostAgentInput_AudioVideoHappyPath`,
  `TestPostAgentInput_MultimodalValidation` covering MIME, empty
  data, and count caps per modality.
- **`test/widgets/multimodal_attach_test.dart`** —
  `mimeForExtension` per-kind disambiguation (esp. `.webm` audio
  vs video); MultimodalKindX extension wiring.
- **`test/widgets/agent_compose_image_gate_test.dart`** — extended
  with `resolveCanAttach{Pdfs,Audio,Video}` cases for the three
  new flags.
- Driver-side wire shapes verified by the existing hostrunner
  tests (image strip warning updated to "no inline multimodal
  support" matches the new copy).

### References

- Plan: `docs/plans/artifact-type-registry.md` (wave 2 W7.2 —
  status now Done overall).

---

## v1.0.496-alpha — 2026-05-11

Wave 2 W7.1 of artifact-type-registry: inline-as-text file picker for
composers. The small half of W7 — no hub/driver work, works on every
engine because the file bytes splice into the prompt body as a fenced
code block rather than riding on the wire as a separate content block.

### Added

- **`lib/widgets/text_attach/composer_text_attach.dart`** —
  shared helper module. `pickAndInlineTextFile()` opens the system
  picker via `file_picker`, enforces the 256 KiB cap, decodes as
  UTF-8 (`allowMalformed: false`), and returns a `TextAttachment`
  whose `markdown` field is a fenced code block ready to splice
  into the composer text. `fenceLanguageForExtension` maps a file
  extension to a markdown fence tag (`py` → `python`, `tsx` →
  `tsx`, `md` → `markdown`, etc.); unknown extensions return an
  empty tag (still a valid fence, just uncoloured downstream).
  `buildFencedBlock` escalates fence length when the input contains
  triple-backtick runs so the closing fence is always longer than
  any internal run — CommonMark behaviour. `kTextAttachExtensions`
  is the conservative allowlist (~45 entries) used by the picker
  to reject obvious binaries before the UTF-8 step.
- **`agent_compose.dart`** and
  **`steward_overlay/steward_overlay_chat.dart`** — paperclip
  affordance now sits next to the image-attach button. Always
  visible (engine-agnostic); tapping picks a file, surfaces a
  banner on cap/format errors, and splices the fenced markdown at
  the cursor (existing selection is replaced; cursor lands at the
  end of the inserted block). Wave 2 W4's image attach stays gated
  on `prompt_image[mode]`; the two affordances coexist.

### Test

- **`test/widgets/text_attach_test.dart`** — `fenceLanguageForExtension`
  covers common code/text/unknown cases. `buildFencedBlock` covers
  default fence length, untagged fence for plain text, escalation
  when the input has triple-backticks (and 4-backtick runs), and
  trailing-whitespace trimming.

### References

- Plan: `docs/plans/artifact-type-registry.md` (wave 2 W7.1).

---

## v1.0.495-alpha — 2026-05-11

Wave 2 W6 of artifact-type-registry: audio + video viewers. Closes the
multimodal-IO slot on the closed artifact-kind set.

### Added

- **`lib/widgets/artifact_viewers/audio_viewer.dart`** —
  `ArtifactAudioViewer` (Riverpod consumer; `just_audio` under the
  hood) + `ArtifactAudioViewerScreen` fullscreen route. Resolves
  `blob:sha256/<sha>` via `HubClient.downloadBlobCached`, stages
  bytes into the app's temp dir via `path_provider`, then hands the
  file path to `AudioPlayer.setFilePath`. just_audio cannot ingest
  raw bytes — the temp-file dance is the supported path. UI is a
  large play/pause toggle, a scrub slider, and `m:ss` /
  `h:mm:ss` position+duration labels via the exported
  `formatAudioDuration` helper. Temp file is best-effort deleted
  on dispose.
- **`lib/widgets/artifact_viewers/video_viewer.dart`** —
  `ArtifactVideoViewer` + `ArtifactVideoViewerScreen`. Same temp-
  file staging pattern (`video_player` also wants a path, not
  bytes). Renders the video at its native aspect ratio with a
  `VideoProgressIndicator` scrubber and a centered play/pause
  overlay that subscribes to the controller's `ValueListenable`.
  Screen uses a black backdrop so the player isn't fighting the
  app's surface colour.
- **`_ArtifactViewerLauncher`** in `artifacts_screen.dart` gains
  `audio` ("Play audio") and `video` ("Play video") branches; the
  filter pill bar gains `audio` + `video` entries so users can
  scope by modality.

### Changed

- **`pubspec.yaml`** — adds `just_audio: ^0.10.4` and
  `video_player: ^2.10.0`. Both packages bring native platform
  channels (~1–2 MB APK each); acceptable cost for closing the
  multimodal slot. APK-split discussion stays under Q10.

### Test

- **`test/widgets/audio_viewer_test.dart`** — `formatAudioDuration`
  covers `m:ss` and `h:mm:ss` ranges; widget tests assert the
  unsupported-uri error path + screen title rendering.
- **`test/widgets/video_viewer_test.dart`** — mirror tests for the
  video viewer.

### References

- Plan: `docs/plans/artifact-type-registry.md` (wave 2 W6).

---

## v1.0.494-alpha — 2026-05-11

Wave 2 W5 of artifact-type-registry: read-only code-bundle viewer.

### Added

- **`lib/widgets/artifact_viewers/code_bundle_viewer.dart`** —
  `ArtifactCodeBundleViewer` (Riverpod consumer; resolves
  `blob:sha256/<sha>` via `HubClient.downloadBlobCached`) +
  `ArtifactCodeBundleViewerScreen` (fullscreen route). Parses three
  JSON manifest shapes: `{files: [{path, content}, …]}`, flat
  list-of-objects, and the single-file `{path, content}` degenerate
  form — picked because agents and human-typed bundles emit both.
  Syntax highlighting runs through `flutter_highlight` (the same
  dep the transcript fenced-code blocks already use); language is
  resolved from the file extension via the in-file
  `languageForPath` map. Unknown extensions fall back to
  `plaintext` (still themed/padded, just uncoloured). File picker
  is a horizontally-scrollable chip bar above the highlighted
  content; tapping a chip swaps the viewer to that file.
- **`_ArtifactViewerLauncher`** gains a `code-bundle` branch, so
  any `code-bundle`-kind artifact in the detail sheet surfaces an
  "Open code" outlined button matching the pdf/tabular/image
  launchers.
- **Hub seed** — `demoRunBundle()` returns a 3-file python
  scaffold (`train.py` + `config.py` + `README.md`) so the wave-2
  demo arc has a real round-trip target. `seedCodeBundleArtifact`
  follows the citation-artifact pattern: real bytes written
  through `insertDemoBlob` when `dataRoot` is set, mock URI
  otherwise. Attached to the ratified experiment-results
  deliverable in both the experiment-phase and paper-phase demo
  projects.

### Test

- **`test/widgets/code_bundle_viewer_test.dart`** — `parseCodeBundle`
  round-trips all three shapes, drops malformed entries, returns
  empty on unsupported shapes. `languageForPath` covers common
  extensions + fallback. Widget tests assert the unsupported-uri
  error path + screen title rendering, mirroring the pdf/tabular
  test patterns.

### References

- Plan: `docs/plans/artifact-type-registry.md` (wave 2 W5).

---

## v1.0.493-alpha — 2026-05-11

W4 follow-on: image artifact view-on-tap + CI fix.

### Added

- **`lib/widgets/artifact_viewers/image_viewer.dart`** —
  `ArtifactImageViewer` (Riverpod consumer; resolves
  `blob:sha256/<sha>` via `HubClient.downloadBlobCached`) +
  `ArtifactImageViewerScreen` (fullscreen route wraps the viewer
  in an `InteractiveViewer` so users can pinch-zoom and pan).
- **`_ArtifactViewerLauncher`** gains an `image` branch, so any
  `image`-kind artifact in the detail sheet now surfaces an
  "Open image" outlined button matching the pdf/tabular pattern.
- **Cross-engine multimodal input follow-up** documented in
  `docs/plans/artifact-type-registry.md` W4 section — Claude/
  Gemini/Codex CLI accept more than images (PDF cross-engine;
  audio/video on Gemini). Tracking the wedge as a separate plan
  rather than expanding this one.

### Fixed

- Test import for `resolveCanAttachImages` updated to
  `composer_image_attach.dart` — the v1.0.492 refactor moved the
  function out of `agent_compose.dart` and the gate test still
  imported from the old location, breaking analyze.

---

## v1.0.492-alpha — 2026-05-11

Wave 2 W4 — image attach on the steward overlay composer.
Multimodal landing for the floating chat surface. ADR-021's existing
hub validator + per-driver wire-mapping already handle the bytes;
this wedge wires the affordance into the smaller composer.

### Added

- **`lib/widgets/image_attach/composer_image_attach.dart`** —
  shared helpers extracted from `agent_compose.dart`:
  - `pickAndCompressImage()` returning a `ComposerImageAttachment`
    (mime + base64-encoded data)
  - `ComposerImageThumbnailStrip` widget (horizontal × strip)
  - `resolveCanAttachImages()` capability gate (visible-for-tests)
  - `kMaxImagesPerTurn` / `kMaxImageBytes` / etc. constants
- **Paperclip + thumbnails on the steward overlay chat**
  (`steward_overlay_chat.dart`):
  - `_ChatInputState` now owns `_pendingImages` + `_attaching` +
    `_attachError` alongside the existing IME-stable controller.
  - `_ChatInputSlot` becomes a `ConsumerWidget` and watches
    `agentId` via `.select` so the slot only rebuilds the once
    when the overlay binds an agent (SSE traffic doesn't reach it).
  - Parent state's `_resolveCapabilityIfNeeded(agentId)` joins the
    family registry to set `_canAttachImages`; the flag flows down
    to `_ChatInput`.
- **`sendUserMessage(text, {images})`** on
  `StewardOverlayController` — new entry point that lifts the text
  body to nullable and forwards `images` through to
  `HubClient.postAgentInput`. `sendUserText(text)` retained as a
  back-compat shim so snippet chips don't break.

### Changed

- `agent_compose.dart` refactored to import the shared helpers
  instead of carrying its own copies of the pick+compress, mime
  map, and thumbnail strip. Net diff: ~120 LOC moved, behaviour
  unchanged.

### Notes

- No hub or engine-driver work was needed — ADR-021 W4.1/W4.2-W4.5
  already shipped the validator + per-driver mapping. Plan W4 had
  some scope overlap with that prior wedge; the artifact-creation
  pathway it proposed was redundant given the existing inline
  base64 pipeline works.
- Capability gate still respects `prompt_image[mode]`: gemini M2
  (exec-per-turn) keeps the affordance hidden because the W4.5
  strip-and-warn fallback isn't an invitation to send.

---

## v1.0.491-alpha — 2026-05-11

Wave 2 W3 — Tabular viewer + References tile reclassification.
Second user-visible viewer on the wave 2 closed-set chassis,
landed alongside the seed change that puts a structured References
component on every ratified lit-review deliverable.

### Added

- **`lib/widgets/artifact_viewers/tabular_viewer.dart`** —
  `ArtifactTabularViewer` (Riverpod consumer) +
  `ArtifactTabularViewerScreen`. Resolves `blob:sha256/<sha>` URIs
  through `HubClient.downloadBlobCached`, parses JSON (top-level
  list-of-objects OR `{rows: [...]}`), renders a `DataTable` with
  empty / error / unsupported-scheme states. Schema discovery via
  MIME's `schema=` param (Q6 option (a)) — known schemas (today:
  `citation`) pick a canonical column order, unknown schemas derive
  from the union of keys in the first 8 rows.
- **`lib/screens/artifacts/artifacts_by_kind_screen.dart`** —
  project-scoped artifact list filtered by closed-set kind +
  optional schema. Used by the References tile; reusable for other
  kind-targeted views as wave 2 progresses.
- **Citation seed** — `seed_demo_lifecycle.go` gains
  `demoCitations()` (8 deterministic rows) +
  `seedCitationArtifact()` that writes the bytes through
  `insertDemoBlob` (when `dataRoot` is set) and emits a real
  `blob:sha256/<sha>` URI with MIME
  `application/json; schema=citation`. Every ratified lit-review
  deliverable gains a 2nd component (`{kind: artifact, refID:
  citationArt.id, ord: 1}`).
- **`test/widgets/tabular_viewer_test.dart`** — unsupported-uri
  error path + screen-scaffold smoke test.

### Changed

- **`SeedLifecycleDemo(ctx, db, dataRoot)`** — signature gains
  `dataRoot string`. Empty string preserves the old mock-URI
  behaviour for tests that don't care about renderable citations;
  the `seed-demo --shape lifecycle` CLI passes the real data root
  so citations resolve through the hub blob endpoint.
- **`_openReferences` (shortcut_tile_strip.dart)** — now tries
  `listArtifactsCached(kind=tabular)` first and routes to
  `ArtifactsByKindScreen(kind=tabular, schema=citation,
  title=References)` when a citation-shaped row exists. Falls back
  to the existing StructuredDeliverableViewer / DocumentsScreen
  ladder when nothing matches.
- **Artifact detail launcher** — `_ArtifactViewerLauncher` in
  `artifacts_screen.dart` extracted into a `switch (spec.kind)`;
  pdf and tabular kinds get distinct launcher buttons; remaining
  MVP kinds wait for W4–W6.

### Notes

- Schema discovery deliberately stops at MIME params today (Q6
  option (a)). Escalate to option (c) (a `artifact_schema_id`
  column) only if domain-specific viewers proliferate.
- Inline-edit on table cells (Q7) remains out of scope — the
  viewer is read-only.

---

## v1.0.490-alpha — 2026-05-11

Wave 2 W2 — PDF viewer for `pdf`-kind artifacts. First user-visible
viewer on the wave 2 closed-set chassis.

### Added

- **`pdfrx ^2.3.3`** dep — PDFium-backed Flutter PDF lib (Q5 in plan).
  Built-in pinch zoom, text search/selection, outline, password
  support. ~2 MB APK cost.
- **`lib/widgets/artifact_viewers/pdf_viewer.dart`** — `ArtifactPdfViewer`
  (Riverpod consumer; resolves `blob:sha256/<sha>` URIs via
  `HubClient.downloadBlobCached`, then renders via `PdfViewer.data`)
  + `ArtifactPdfViewerScreen` (fullscreen route — keeps the pinch-zoom
  gesture from fighting the artifact detail sheet's vertical drag).
- **`_ArtifactViewerLauncher`** in `artifacts_screen.dart` — dispatches
  on `artifactKindSpecFor(row['kind']).kind`; for `ArtifactKind.pdf`
  surfaces an "Open PDF" outlined button below the title. Other kinds
  render no launcher today (W3+ extends the dispatcher).

### Notes

- Non-`blob:sha256/` URI schemes (seed mock data, external HTTPS,
  raw filesystem paths) show an explicit "unsupported uri scheme"
  card rather than crashing. The hub blob endpoint is the only
  load-bearing path today.
- Down-stack: `HubClient.downloadBlobCached` already handles auth +
  on-disk content-addressed caching; the viewer is a thin wrapper
  over existing infrastructure.

---

## v1.0.489-alpha — 2026-05-11

Wave 2 W1 — artifact-type-registry closed-set chassis. Lifts
`artifacts.kind` from a free-form string into the 11-entry MVP
vocabulary defined in
`docs/plans/artifact-type-registry.md`. No new viewers yet; this
wedge is the chassis the W2–W6 viewers will dispatch on.

### Added

- **`hub/internal/server/artifact_kinds.go`** — closed-set registry.
  `validArtifactKinds` (11 entries: `prose-document`, `code-bundle`,
  `tabular`, `image`, `audio`, `video`, `pdf`, `diagram`,
  `canvas-app`, `external-blob`, `metric-chart`) is the wire
  vocabulary; `backfillLegacyArtifactKind` maps the pre-W1
  free-form values (`checkpoint`/`dataset`/`other`/`eval_curve`/
  `log`/`report`/`figure`/`sample`) onto the new set so MCP clients
  still in flight survive a tester cycle.
- **`hub/migrations/0039_artifacts_kind_check.{up,down}.sql`** —
  documentation + backfill `UPDATE` pass. No CHECK constraint
  (Q3 resolved in plan against DB-level enforcement so new kinds
  don't require a forward migration each time); the down migration
  is a documented no-op because the remap is lossy.
- **`lib/models/artifact_kinds.dart`** — Dart enum mirroring the hub
  registry, plus `ArtifactKindSpec` (label / icon / mime hint /
  colour role) and `artifactKindSpecFor(slug)` with legacy-alias
  remapping and `externalBlob` fallback so the UI always has
  something to render.
- **`test/models/artifact_kinds_test.dart`** — round-trip + alias
  + fallback test coverage.
- **`hub/internal/server/handlers_artifacts_test.go`** —
  `TestCreateArtifact_ClosedKindSet` covers every MVP kind (201),
  a bogus kind (400), and every legacy alias round-tripping to the
  remapped MVP kind.

### Changed

- `handleCreateArtifact` now rejects unknown kinds with 400 unless
  they live in `validArtifactKinds` or the legacy alias map; legacy
  values are silently remapped + the artifact stores the new slug.
- `seed_demo_lifecycle.go` emits `external-blob` (was `checkpoint`)
  and `metric-chart` (was `eval_curve`) so demo data ships under
  the closed set.
- `ArtifactKindChip` (artifacts_screen.dart) now dispatches through
  `artifactKindSpecFor`, so legacy cached rows render with the
  remapped label/colour and new kinds (pdf, tabular, image, code…)
  pick up sensible defaults instead of the muted `?` fallback.
- Filter pills on the Artifacts screen swap to the closed MVP set
  (`prose-document`, `tabular`, `image`, `pdf`, `metric-chart`,
  `code-bundle`, `external-blob`) — what new agents will emit.

### Deprecated

- The free-form `checkpoint`/`eval_curve`/`log`/`dataset`/`report`/
  `figure`/`sample`/`other` kind strings are accepted only as
  legacy aliases. Migrate emitters to the MVP set; the alias bridge
  will be removed in a later wedge once the next tester cycle
  confirms no live emitter relies on it.

---

## v1.0.488-alpha — 2026-05-11

Projects list filter / sort AppBar affordance. Common-case default
(active + recent) plus quick "needs me" toggle and name / created
alternates.

### Added

- **AppBar filter icon** (`Icons.filter_list`) on the Projects screen,
  between the team-overview Insights icon and Refresh. Tap opens a
  modal bottom sheet with three sections:
  - **Status**: SegmentedButton `Active` (default — hides archived) /
    `All` / `Archived`
  - **Needs me**: switch — show only projects with open attention
    or open AC
  - **Sort**: SegmentedButton `Recent` (default — uses insights
    `last_activity` with `created_at` fallback) / `Name A-Z` / `Created`
- **Active-filter indicator**: small primary-color dot on the icon
  when the filter is non-default, so a power-user setup is
  immediately visible at a glance.
- **Persisted preference**: SharedPreferences key
  `projects_list_filter_v1` survives app restarts. Reset link in the
  sheet clears to defaults.
- **Filter-aware empty state**: the projects-list empty message now
  differentiates "no projects yet" from "no projects match the
  current filter" so a filtered user doesn't think the list vanished.

### Changed

- `_ProjectsTab.build` applies the filter before partitioning into
  goals / workspaces, so the sub-project flatten and the kind split
  both honor the user's pick.

---

## v1.0.487-alpha — 2026-05-11

UI polish pass on project surfaces before wave 2. No new schema, no new
endpoints — re-shape what the existing `/v1/insights?team_id=X`
payload renders into.

### Changed

- **Project list rows: drop redundant kind chip.** The `[PROJECT]` /
  `[WORKSPACE]` leading chip on each row was redundant — the section
  header above each list (`PROJECTS` / `WORKSPACES`) already declares
  it. `ProjectKindChip` retained for project-detail use; only the
  list-row leading is gone.
- **Project list rows: 3-line card for goal projects.** Sources
  current phase, progress, open-AC count from
  `/v1/insights?team_id=X`'s `by_project[]` (no extra round-trip;
  same data the Insights icon already pulls).
  - Line 1: name · status dot · attention badge
  - Line 2: phase pill · "N open AC" chip (or "no open AC")
  - Line 3: progress bar + percentage
  - Parent-with-children rows append "N sub-projects" below the bar.
  - Workspaces, lifecycle-disabled projects, and goal projects that
    haven't been seen by Insights yet fall back to the existing
    two-line tile (no kind chip).
- **Team Insights page redesigned.** Pivoted from per-project card
  list (now redundant with the inline list rows) to a team-level
  aggregate dashboard:
  - **Summary tiles**: Active / Open AC / Open attention / Live <24h
  - **Phase distribution**: horizontal bar chart by current_phase
  - **Activity recency buckets**: <24h / <7d / >7d / idle
  - **Most recent · top 5**: tap → project detail
  - **Top agents · by event volume**: leaderboard from existing
    `by_agent[]` (sorted by `tokens_in`, fallback to event/tool counts)

---

## v1.0.486-alpha — 2026-05-11

Chassis follow-up wave 1 — D10 hero override mechanism + deliverables +
acceptance-criteria tiles. Per ADR-024 follow-up ordering:
[`docs/decisions/024-project-detail-chassis.md`](decisions/024-project-detail-chassis.md)
§Follow-up wedges (ordered).

### Added

- **Hero overrideability per-phase per-project (D10).** New migration
  `0038_project_overview_widget_overrides` adds
  `projects.overview_widget_overrides_json` (mirrors the v1.0.484
  tile-override column). `Server.resolveOverviewWidget` consults the
  override map first, then per-phase template YAML, then template
  default, then chassis default. Wire payload gains
  `overview_widget_overrides` (raw user map) and
  `overview_widget_template` (per-phase template-side map for the
  picker's Reset affordance). PATCH `projects` accepts
  `overview_widget_overrides`. Closes the ADR-023 inconsistency where
  the steward could swap tiles but not heroes.
- **Hero picker in `PhaseTileEditorSheet`.** ChoiceChip Wrap above
  the tile-composition section. Picks from the closed
  `kKnownOverviewWidgets` set with `overviewWidgetSpecFor` labels.
  Save bundles the tile + hero override into one PATCH; Reset
  clears both per-phase overrides.
- **`deliverables` tile slug** + `DeliverablesScreen`. Project-scoped
  flat list grouped by phase; tap → existing
  `StructuredDeliverableViewer`. Unblocks lifecycle-walkthrough W7-W8
  by making deliverables reachable without going through a phase
  chip.
- **`acceptance_criteria` tile slug** + `AcceptanceCriteriaScreen`.
  Project-scoped flat list grouped by phase with state filter
  (all/pending/met/failed/waived); tap → parent deliverable viewer.

### Changed

- **Closed `TileSlug` enum now 11 slugs** (was 9 at ADR-024 lock).
  Added `deliverables` + `acceptanceCriteria`. Wire format accepts
  `deliverables`, `acceptance_criteria`, `acceptance-criteria`,
  `criteria` as aliases for the AC slug.
- **`resolveOverviewWidget` signature** now takes an `overrides
  map[string]string` parameter. Empty/nil falls through to the
  prior template-side resolution. All three call sites
  (`handleListProjects`, `handleGetProject`, `handleCreateProject`)
  updated.

### Documentation

- ADR-024 D10 + Follow-up Wedges sections updated: wave 1 marked
  shipped, 11-slug locked set. Status block bumped to v1.0.486.
- `reference/project-detail-chassis.md` resolution chain rewritten
  (override → template phase → template default → chassis default),
  §8 per-project-vs-per-template matrix updated, §9 ordering table
  gains Status column.

---

## v1.0.485-alpha — 2026-05-11

Project overview attention redesign — W1+W2+W3. Plan:
[`docs/plans/project-overview-attention-redesign.md`](plans/project-overview-attention-redesign.md).

### Changed

- **Discussion AppBar icon dropped from project detail.** Was a
  redundant fourth navigation surface alongside the 5 tab pills, the
  AppBar Insights icon (deferred), and the in-Overview tile strip.
  Discussion remains reachable via the `TileSlug.discussion` tile,
  added to the current phase composition through the v1.0.484
  per-project `PhaseTileEditorSheet`. The Activity tab continues to
  cover the "what's been said?" use case for event-level feed.
  `lib/screens/projects/project_detail_screen.dart`.
- **Outer metadata rows + Archive action now collapsed by default**
  behind a "Details" `ExpansionTile` at the bottom of the Overview
  tab. The PortfolioHeader (goal, status, budget, task progress) and
  the InsightsPanel above the divider stay inline; only the
  rarely-accessed Name/Kind/Status/Goal/Steward template/On-create
  template/ID/Docs root/Created list and the destructive Archive
  CTA fold under the expander. F-pattern preserved: eye lands on
  banner → header → hero → tiles → metrics, then "Details" if needed.
  `lib/screens/projects/project_detail_screen.dart`.

### Added

- **Cross-project Insights surface — `/v1/insights?team_id=X` now
  returns `by_project[]`.** One row per goal-kind, non-archived
  project in the team: `{project_id, name, current_phase, status,
  progress, open_attention, open_criteria, last_activity}`. Sort:
  `last_activity` desc. Server-side hard cap 100 rows. Workspaces
  (`kind='standing'`) and archived projects filtered out per Q3 of
  the plan. `progress` follows the weighted formula
  `(phases_done + current_phase_AC_ratio) / phases_total` (Q2 (c)),
  smooth-monotonic across phase advances. Field is omitted from
  non-team scopes.
  `hub/internal/server/handlers_insights.go`,
  `hub/internal/server/handlers_insights_scope_test.go`.
- **Team overview AppBar icon on Projects list** → new
  `TeamOverviewInsightsScreen`. Renders one card per project with
  name, phase chip, status pill, progress bar (% derived from the
  weighted formula above), attention badge, open-criteria badge,
  and relative-time last-activity. Tap → opens project detail
  (looks up the full project map off `hubProvider.projects`).
  `lib/screens/projects/projects_screen.dart`,
  `lib/screens/insights/team_overview_insights_screen.dart`.

### Background

The project detail Overview tab had accumulated six vertical
regions (attention banner / PortfolioHeader / phase hero / tile
strip / InsightsPanel / metadata+Archive) plus AppBar icons for
Discussion + Template-YAML plus 5 tab pills plus chassis
PhaseRibbon — twelve interaction zones competing for above-fold
attention. Applied the three attention principles from the prior
design discussion (Orient → Focus → Explore): drop one redundant
navigation surface (W1), demote rarely-accessed metadata to a
collapsible footer (W2), and promote the missing cross-project
surface to its proper home on the Projects list AppBar (W3).
Risks register stays explicitly post-MVP — the closed `TileSlug`
enum keeps `risks` but no template surfaces it and no
implementation work lands here.

---

## v1.0.484-alpha — 2026-05-11

Lifecycle-walkthrough follow-ups batch (W1–W6). Plan:
[`docs/plans/lifecycle-walkthrough-followups.md`](plans/lifecycle-walkthrough-followups.md).

### Fixed

- **Plans / Schedules tiles now scope to the current project.** Tapping
  Plans from `research-method-demo` was dumping the team-wide list (5
  plans, one per seeded project) because `shortcut_tile_strip.dart`
  pushed `const PlansScreen()` with no project context. Both screens
  now accept a `projectId` constructor arg; the tile entry passes it.
  Filter sheets still let the user broaden to team-wide.
  `lib/screens/projects/plans_screen.dart`,
  `lib/screens/projects/schedules_screen.dart`,
  `lib/widgets/shortcut_tile_strip.dart`.

### Changed

- **Seed-demo `--shape lifecycle` plan_steps now use schema-valid kinds**
  (`agent_spawn` / `llm_call` / `shell` / `human_decision`) instead of
  the placeholder `agent_driven` that mirrored the phase ribbon. Each
  project now seeds realistic per-phase work — research-method-demo,
  for instance, has step kinds spanning `human_decision` (scope
  ratification), `agent_spawn` (lit-reviewer + critic), `llm_call`
  (draft method), and ends with a pending human_decision for
  ratification. Test coverage in `seed_demo_lifecycle_test.go` asserts
  every seeded kind is in `planStepKinds`. Phase progression itself
  still lives on `projects.phase` + `phase_history`, where it belongs.
- **Seed-demo `--shape lifecycle` now seeds project-scoped tasks too.**
  Each of the five demo projects gets 2–5 kanban tasks in mixed
  states (`todo` / `in_progress` / `done`), some with subtasks via
  `parent_task_id`. The Tasks tab on project detail is no longer
  empty during walkthrough QA.

### Added

- **`docs/reference/glossary.md` §10b — Project lifecycle entities.**
  Canonical entries + relationship arrows for project / phase / plan /
  plan-step / task / document / deliverable / acceptance criterion.
  Resolves the plan-vs-phase confusion the v1.0.482 walkthrough QA
  surfaced.
- **`steward-lifecycle-walkthrough.md` Scenario 0 — project conjuration.**
  New head-of-arc scenario where the steward creates the project from
  template via `projects.create` + `mobile.navigate`. Mirrors §11 of
  the agent-driven-mobile-ui discussion doc. Companion how-to also
  updated.
- **Configurable per-phase tile composition.** New column
  `projects.phase_tile_overrides_json` (migration 0037) holds a
  `{phase: [slug...]}` map. The hub also serves the template's YAML
  default at `phase_tiles_template` on the project payload. Mobile's
  `resolveTilesForPhase` resolves project override → template YAML →
  hardcoded safety-net → chassis default. No APK rebuild needed to
  change which tiles surface on which phase; the closed `TileSlug`
  vocabulary stays APK-bound, only the *composition* is data.
  `lib/widgets/shortcut_tile_strip.dart`,
  `hub/internal/server/handlers_projects.go`,
  `hub/internal/server/template_hydration.go`,
  `hub/migrations/0037_*.sql`.
- **On-device tile editor.** Trailing "Customize shortcuts for this
  phase" row on the tile strip opens a modal sheet — checkbox + drag
  reorder over the full `TileSlug` vocabulary; saves via PATCH
  `phase_tile_overrides`. A "Reset" button clears the per-project
  override and falls back to the template default. Both the steward
  (`projects.update` MCP tool) and the user (this sheet) write to the
  same `phase_tile_overrides_json` field.
  `lib/widgets/shortcut_tile_strip.dart` (PhaseTileEditorSheet).
- **Research template `phase_specs[idea].tiles = [Documents]`.** Idea
  phase is conversation-first by spec, but the steward routinely
  creates idea memos there; the Documents tile gives the director a
  path to find them. Replaces v1.0.483's hardcoded-in-Dart workaround
  with a template-driven override.

---

## v1.0.483-alpha — 2026-05-11

### Fixed

- **General steward sessions no longer bucket under "Detached".**
  `isStewardHandle()` deliberately excludes `@steward` so spawn /
  collision-check sites treat the team concierge as separate; the
  Sessions screen reused that predicate when building `liveStewardIds`,
  which caused any session whose `current_agent_id` pointed at the
  general steward to fall through to the orphan branch. The Sessions
  screen now widens its check to `isStewardHandle(h) ||
  isGeneralStewardHandle(h)` — the predicate's other call sites are
  unchanged. `lib/screens/sessions/sessions_screen.dart`.
- **Documents tile now shows on the idea phase Overview.** The research
  template marked idea as "conversation-first" with `tiles: []`, but
  scenario 3 of the lifecycle walkthrough creates idea memos via
  `documents.create` — the document landed in the DB and the director
  had no UI path to find it. Added `TileSlug.documents` to the idea
  phase in both the spec (`docs/reference/research-template-spec.md`
  §3) and the renderer (`lib/widgets/shortcut_tile_strip.dart`).
- **Steward overlay no longer spams "stream errored: connection closed".**
  After a turn ends, the SSE goes idle and mobile carriers / reverse
  proxies typically reap the TCP socket within ~60–90s. The overlay
  controller used to post a system note on every reconnect cycle; now
  it (a) suppresses notes for known idle-drop signatures (matching the
  heuristic `agent_events_provider` / `agent_feed` already use) and
  (b) defers real-error notes by 3s so a fast reconnect heals
  invisibly. Server-side ping cadence also dropped from 15s → 5s
  on both agent-events and channel-events streams to give NATs /
  proxies more frequent activity to count.
- **Snippets manage page now has an Add action + the 3 starter chips
  are editable.** The page wraps `SnippetsScreen` (a Vault embedded
  body widget without its own Add button), so pushing it as a route
  from the overlay's Edit chip surfaced a read-only-looking list. The
  3 chip-strip defaults were also in-memory constants that never
  entered the snippet store. Both are fixed: the manage page AppBar
  carries an Add action that opens `SnippetEditDialog` pre-filled
  with `category=steward`, and the 3 starter chips moved into
  `SnippetPresets` as a `steward` profile — they now render in the
  manage page with the existing preset-tile machinery (tap to edit,
  swipe to delete, restore-chip to revert overrides).

### Changed

- **Server SSE ping cadence: 15s → 5s** (`handlers_agent_events.go`,
  `handlers_stream.go`). Shorter cadence keeps mobile carrier NATs /
  reverse proxies from reaping quiet streams between turns.

---

## v1.0.482-alpha — 2026-05-10

### Changed

- **Edit chip on the overlay no longer auto-collapses the panel.**
  v1.0.481 made the Edit chip dismiss the panel before pushing the
  snippets manager. The principal flagged it as inconsistent with
  ADR-023 D1 (persistent overlay across all routes); `mobile.navigate`-
  driven pushes don't auto-collapse, so a chip-driven push shouldn't
  either. The panel is also draggable / resizable / opacity-tunable —
  user can move it themselves if it covers the destination. Reverted
  the auto-close + the `onCloseRequested` plumbing through
  StewardOverlayChips. Note kept on `_ManageChip` documenting that
  `_openFullSession` (header "Open in new" button) IS the intended
  exception: it opens the steward's full session transcript = same
  conversation as the panel, leaving both open is redundant.

  v1.0.481's Scaffold wrapper for `SnippetsScreen` (the actual fix
  for yellow underlines / can't-scroll / incomplete chrome) stays
  in place.

---

## v1.0.481-alpha — 2026-05-10

### Fixed

- **Snippets manager renders correctly when opened from the
  overlay's Edit chip.** v1.0.479 added a trailing "Edit" chip on
  the steward-overlay chip strip that pushed `SnippetsScreen` as
  a `MaterialPageRoute`. `SnippetsScreen` was authored as an
  *embedded* widget for the Vault page (`vault_screen.dart`):
  it returns a bare `Column`, no Material ancestor, no scroll
  view, no AppBar / system-inset padding. Pushed directly as a
  route, the user saw yellow "missing Material" double-underlines
  on every Text, no ability to scroll past the visible viewport,
  and an incomplete page chrome. Compounded by the overlay panel
  staying expanded on top, since the chip's onTap didn't dismiss
  the panel — the destination route rendered behind the panel.
  Fix layered:
  - New `_SnippetsManagePage` wrapper inside
    `steward_overlay_chips.dart` provides the missing Scaffold +
    AppBar + `SafeArea` + `SingleChildScrollView` so the
    embedded `SnippetsScreen` body has a real route chrome to
    sit in.
  - `StewardOverlayChips` accepts an optional
    `onCloseRequested` callback; the chat surface plumbs its
    own `onCloseRequested` (= `_ExpandedPanel.onClose`)
    through, and the Edit chip calls it before pushing so the
    panel collapses first.
  No new MCP tools, no new screens — purely a hosting fix.

---

## v1.0.480-alpha — 2026-05-10

Follow-up to v1.0.479. The QA report on issue 4 was sharper than
v1.0.479 read it: "there is keyboard for input but not my input
method." A keyboard DID attach — it just wasn't the user's CJK
IME. v1.0.479's puck-hide + keyboard-shift work address tap
hit-testing and IME-covered-panel layout, but neither addresses
the IME-mode bug.

### Fixed

- **CJK / non-Latin IMEs now engage on the overlay chat input.**
  The TextField was setting `autocorrect: false` and
  `enableSuggestions: false` (added in v1.0.471 as belt-and-
  suspenders for the deleted-text-returning bug; v1.0.472 fixed
  that bug architecturally via rebuild-scope isolation, so the
  flags were no longer load-bearing). Android maps them to
  `TYPE_TEXT_FLAG_NO_AUTO_CORRECT` / `TYPE_TEXT_FLAG_NO_SUGGESTIONS`;
  CJK IMEs (Sogou, Gboard-CN, Baidu, Mozc, native Japanese /
  Korean stacks) treat no-suggestions as a hard signal to fall
  back to Latin-only mode because the suggestion strip IS their
  candidate display. The user's selected IME would attach but
  refuse to engage its composition pipeline. Both flags are now
  removed; the v1.0.472 isolation continues to keep the input
  subtree out of the SSE rebuild scope.

---

## v1.0.479-alpha — 2026-05-10

Four-issue QA fix on top of v1.0.478. Each was an independently
visible regression once the user exercised the overlay end-to-end.

### Fixed

- **`mobile.intent` events stamped with `session_id`.** v1.0.474's
  W1 backfill added a session-filtered SSE subscription (`?session=`)
  to scope the overlay to the steward's current session. The hub's
  `handleStreamAgentEvents` filter drops any event whose
  `session_id` doesn't match. `handleMobileIntent` published the
  event with `agent_id` + `team_id` but never `session_id`, so the
  filter dropped every navigation intent → the past-tense pill
  never rendered, the URI never dispatched, the user only saw the
  steward's text reply ("done — opened your projects") with no
  side effect. Fix: `lookupSessionForAgent(stewardID)` and stamp
  the result on the bus envelope, matching the pattern
  `handlePostAgentEvent` already uses for text frames. New test
  `TestMobileIntent_StampsSessionID` locks the contract.
- **SSE auto-reconnect on stream close/error.** The controller used
  to attach the SSE subscription once at bootstrap and append a
  system message on `onError` / `onDone`, leaving the panel dead
  until the user manually reopened it. New behaviour:
  exponential backoff (1s → 16s capped) reconnect with the last
  observed seq as the resume cursor, single user-visible system
  note per reconnect cycle, full reset of the backoff once a
  fresh frame arrives. Resolves the QA report "stream errored
  saying connection closed while the turn is ended."
- **"Open in new" header button now opens the full session.** The
  panel's BuildContext sits OUTSIDE the inner Navigator (overlay
  is mounted via `MaterialApp.builder`, which wraps the Navigator
  widget). `Navigator.of(context)` from there couldn't resolve
  the inner Navigator. Switched to the shared
  `overlayNavigatorKeyProvider` — same pattern the live
  `_dispatchIntentLive` path already uses — which IS the
  `MaterialApp.navigatorKey` override.
- **Snippet "Edit" entry-point added to the chip strip.** Trailing
  pencil chip pushes `SnippetsScreen` via the same overlay
  navigator key. Without this, users could not see / add / edit
  steward-tagged snippets — the chip strip only ever showed the
  three built-in defaults.
- **System IME now appears when tapping the chat input.** Two
  fixes layered:
  - **Puck hidden while panel expanded.** The puck (56×56)
    floated at a Stack position that overlapped the bottom-right
    chat surface (chips + input + send). Stack paints later
    children on top → puck ate taps on the input region → tap
    collapsed the panel via the puck's `onTap` instead of
    focusing the TextField → IME never attached. Hide the puck
    when expanded so the panel owns its own hit-testing surface.
  - **Keyboard-aware panel shift.** When `MediaQuery.viewInsets.
    bottom > 0` and the panel bottom would go behind the
    keyboard, shift the rect up by the overlap (+12px breathing
    room). Non-persistent — snaps back to the saved rect when
    IME closes. The overlay isn't inside a Scaffold so
    `resizeToAvoidBottomInset` doesn't apply; this is the manual
    equivalent.

### Test

- New `TestMobileIntent_StampsSessionID` asserts the bus envelope
  carries `session_id` so the SSE filter passes the event.
  All five existing mobile-intent tests still pass.

---

## v1.0.478-alpha — 2026-05-10

CI fix on top of v1.0.477 — the v1.0.477 tag's Android build
failed `flutter analyze` because the overlay migration used
`ref.listenManual`, which doesn't exist on Riverpod 3.x's `Ref`
(only on `WidgetRef`). The Notifier-side equivalent doesn't
compose cleanly with the async-resolved-key pattern this
overlay needs without a non-trivial restructure (split-provider
shape: separate FutureProvider for `(agentId, sessionId)`
resolution + family-keyed Notifier for the events listener).

This release ships v1.0.477's WORKING parts (the
`agentEventsProvider` infrastructure file) and reverts the
broken overlay migration. v1.0.476's overlay controller code
is restored verbatim.

### Reverted from v1.0.477
- `StewardOverlayController` migration to the shared provider.
  Overlay continues to own its own SSE subscription + backfill
  + reconnect logic for now.

### Retained from v1.0.477 (still good)
- `lib/providers/agent_events_provider.dart` — the
  `NotifierProvider.autoDispose.family<AgentEventsKey>` shared
  data layer. Sits in the codebase ready for consumers; today
  has no callers but P2 (AgentFeed migration, post-MVP) and
  future surfaces will plug in.

### Notes
- Tag v1.0.477-alpha exists in the repo but its release build
  did not produce an APK. v1.0.478 is the canonical successor.
- The overlay's pre-existing capabilities (no cache-only first
  paint; no reconnect-with-backoff) remain pre-existing —
  they'd have come along for free via the migration if the
  Riverpod 3.x lifecycle had cooperated. Migrating the overlay
  cleanly needs a split-provider refactor that is out of scope
  for the CI fix; tracked as a follow-up under the same plan
  doc.
- The provider file itself adds to `flutter analyze`'s noise as
  defines-but-no-callers, but Dart doesn't error on unused
  public symbols.

---

## v1.0.477-alpha — 2026-05-10 (broken — see v1.0.478)

Build of v1.0.477's tag failed `flutter analyze`; no APK
produced. See v1.0.478 above for the corrected release shape
and the retained / reverted breakdown.

---

## v1.0.476-alpha — 2026-05-10

Compact-mode rework of the steward overlay (Option A from the
"compact vs duplicate" architectural review). The overlay was
rendering essentially the same content as the Sessions screen —
just with chat-bubble styling instead of full-fidelity cards.
This reframes it as the recent-directive-context surface: shorter
window, action-aware rendering, and a clear pivot to the full
session for everything else.

### Changed
- **Rolling message cap dropped from 100 → 20** (`_overlayMessageCap`).
  The Sessions screen owns the full transcript; the overlay's job
  is the last ~10 turns of recent directive context, not a parallel
  log.
- **`mobile.intent` events now render on cold-open replay** as
  past-tense pills ("Steward → Insights · 14:32"). Reverses the
  v1.0.474 B5 decision — those are the most informative directive
  signal and skipping them on replay was the wrong call. The pill
  shape uses `OverlayIntentAction{verb, target, uri}` which is
  action-aware (defaults to navigation `→` for v1; future create /
  edit / write actions get the right verb without a model change).
  Tap a pill to re-fire the URI.
- **Long steward replies truncate at 240 chars** with a "open full
  session for the rest" italic suffix. Keeps the overlay's
  directive purpose obvious — it's not a transcript.
- **Live-vs-replay split for `mobile.intent`** clarified in the
  controller. `_eventToMessage` produces the chat bubble for both
  live and replay paths (single source of truth for shape);
  `_dispatchIntentLive` runs ONLY on live SSE — handles the actual
  navigation + snackbar without re-appending a message.

### Added
- **"Open full session" icon** in the panel header. Pushes
  `SessionChatScreen` for the steward's current session, then
  collapses the overlay so the user can scroll the full transcript
  unobstructed. Disabled (greyed) until backfill resolves agentId
  + sessionId.
- **Pending-attention badge** in the panel header. Counts attention
  items where `agent_id == steward_agent_id` and status is `open`
  / `pending`. Tap jumps to the Me tab + collapses the overlay.
  Hidden when 0. Sourced directly from `hubProvider.attention`,
  not duplicated.

### Notes
- The full transcript / attention-detail / approval-decide flows
  remain on their dedicated screens. The overlay only links into
  them — no data duplication.
- The agent_events SSE subscription is still owned independently
  by the overlay controller (Option B from the review — sharing
  the data source with `agent_feed.dart` — is a cleanup wedge for
  later, not bundled here).

---

## v1.0.475-alpha — 2026-05-10

W2 + W3 of the overlay-history-and-snippets plan. Closes the
plan's three-workband bundle.

### Added
- **Quick-action chip strip above the chat input** (W3). User
  snippets with `category == 'steward'` (B1) render first in
  insertion order, followed by three built-in defaults so the
  row is non-empty on cold install: "Show insights", "What's
  blocked?", "Open my projects". Tap fires the snippet body
  through the same `sendUserText` path the input uses; the bubble
  appears via the SSE round-trip (W2 path). Defaults are visually
  muted so users can tell which they can replace by editing
  their snippets.
  (`lib/widgets/steward_overlay/steward_overlay_chips.dart`,
  `lib/widgets/steward_overlay/steward_overlay_chat.dart`)

### Changed
- **User input renders as user bubbles via SSE round-trip** (W2 —
  Option A). The hub already publishes user input as `kind ==
  'input.text'` with `producer == 'user'` on the same agent bus
  the steward output flows through; we now demux those frames in
  `_handleEvent` and `_hydrateFromEvents` (cold-open backfill)
  via a single `_eventToMessage` folder. Live and replay paths
  produce identical bubble shapes — no dedup, no risk of
  divergence between cold-open render and live typing.
- **Local pre-echo dropped from `sendUserText`.** The user's bubble
  no longer appears synchronously on tap-send; instead it arrives
  ~100-300 ms later when the SSE echo lands. Trade-off documented
  in the controller (Option A vs Option B in the plan). The send
  button's existing spinner state covers the latency window. If
  QA flags the lag, swap to id-based dedup (~30 LOC).

### Notes
- The chip strip is a sibling of the input + messages region —
  watches `snippetsProvider` only, so SSE events don't trigger
  rebuilds on the chip subtree.
- Built-in default snippet ids are prefixed `_overlay_default_*`
  to prevent collision with user snippets.
- W4 polish (visual / accessibility / haptics) from the plan is
  optional and not bundled here.

---

## v1.0.474-alpha — 2026-05-10

W1 of the overlay-history-and-snippets plan.

### Added
- **Overlay chat backfills the last 50 events on cold open.** Per
  the plan's W1 + B1–B6 decisions: pull through
  `listAgentEventsCached` (mirrors `agent_feed.dart`'s pattern),
  filter both the backfill AND the live `streamAgentEvents` to
  the resolved session id (B3), reverse from seq DESC tail order
  to ASC for chat display, render `kind=='text'` frames as
  steward bubbles. The `agentId` field stays null — the panel's
  spinner stays up — until backfill completes (B6), so users
  don't see an empty chat flash before content appears.
  (`lib/widgets/steward_overlay/steward_overlay_controller.dart`)

### Changed
- **`mobile.intent` events skipped on backfill replay** (B5).
  Live navigation events still render the snackbar + system note
  + dispatch the route, but historical ones are dropped — they're
  transient logs and re-rendering them as if the steward is
  navigating *now* would be confusing.
- **`streamAgentEvents` now subscribes with `sinceSeq` cursor
  derived from the backfill** so the hub doesn't replay frames
  the panel already shows.

### Notes
- Backfill failure is non-fatal: if both network and cache miss,
  the panel proceeds with empty messages + a system note ("Could
  not load history: …") so the user can still chat live.
- Cache stale fallback also surfaces as a system note ("Showing
  cached history (offline)").
- W2 (user-input rendering) and W3 (snippet chips) are still
  upcoming in the same wedge.

---

## v1.0.473-alpha — 2026-05-10

### Fixed
- **System IME (Gboard) now attaches to the steward overlay text
  field.** v1.0.471 added `autofillHints: const []` as one of three
  belt-and-suspenders flags meant to harden the input against
  predictive-restore. The empty list (rather than `null`, the
  default) signals Android's `AutofillManager` that this field is
  *managed by autofill but has no hints* — which on some
  Android+Gboard combinations causes the IME to refuse to attach.
  Visible bug in v1.0.472: tapping the input did nothing; no
  keyboard appeared. Audit shows none of the other deterministic-
  typing inputs in the codebase (`compose_bar` direct mode,
  `hub_bootstrap`, `templates`) set `autofillHints`; we shouldn't
  either. Dropped the line and added a do-not-restore comment.
  `autocorrect: false` and `enableSuggestions: false` stay — they
  match the rest of the codebase and don't suppress IME attach.
  (`lib/widgets/steward_overlay/steward_overlay_chat.dart`)

The rebuild-scope fix from v1.0.472 (split `_MessagesRegion` /
`_ChatInputSlot` siblings) remains the primary correctness
mechanism; the IME flags now serve only as cosmetic alignment with
the rest of the app.

---

## v1.0.472-alpha — 2026-05-10

The architectural fix the v1.0.471 IME workaround was masking.

### Changed
- **Steward overlay chat: rebuild scope tightened to the messages
  region only.** Before, `_StewardOverlayChatState` was a
  ConsumerStatefulWidget whose `build()` watched
  `stewardOverlayControllerProvider` directly, so every SSE event
  (text chunks, tool calls, system frames) rebuilt the entire
  Column — including the `_ChatInput` subtree. Even with stable
  controller + focus node, the rebuild traversal triggered
  EditableText's `_updateRemoteEditingValueIfNeeded` IME poke,
  which GBoard interpreted as a composition reset and rebounded
  by re-pushing its cached predictive word. That was the real
  root cause of "deleted text returns when retyping."
  Restructured into three sibling pieces:
  - `_StewardOverlayChatState.build()` returns a `const Column` —
    no `ref.watch`, never rebuilds on SSE events.
  - `_MessagesRegion` (new ConsumerStatefulWidget) is the *only*
    widget that watches the provider; loading / error / empty /
    list branches all live here.
  - `_ChatInputSlot` (new const StatelessWidget) is a pure sibling
    of `_MessagesRegion` — its subtree is structurally untouched
    by SSE traffic, so the IME never gets poked outside of actual
    user input.
  Net effect: IME state is now genuinely orthogonal to network
  state, which is how it should have been from the start. The
  `autocorrect: false` / `enableSuggestions: false` /
  `autofillHints: const []` flags from v1.0.471 are kept as
  belt-and-suspenders — they are no longer load-bearing for the
  bug fix, but they keep the overlay's typing feel consistent
  with the rest of the app's deterministic-input pattern
  (compose_bar direct mode, hub_bootstrap, templates).
  (`lib/widgets/steward_overlay/steward_overlay_chat.dart`)

---

## v1.0.471-alpha — 2026-05-10

Steward overlay text-input bug, finally root-caused. Plus two
pieces of design work that the principal asked be documented
rather than implemented inline.

### Fixed
- **Old / history input no longer reappears in the steward overlay
  text field as the user retypes.** v1.0.467 extracted the input
  to its own non-Consumer `_ChatInput` State which fixed the
  cursor-jump-to-end symptom, but it never disabled the IME's own
  predictive-restore path — so deleted characters were still
  re-pushed by GBoard after each IME detach / re-attach (which
  happens on every SSE event because the chat parent watches a
  high-frequency Riverpod provider). Mirrors what the rest of the
  codebase already does for inputs that need deterministic typing
  (`compose_bar`, `hub_bootstrap`, `templates`):
  - `autocorrect: false`
  - `enableSuggestions: false`
  - `autofillHints: const []`
  Also moved the `FocusNode` to be owned by the input's State so
  it isn't re-minted on each parent rebuild — a stable focus node
  reduces the IME attach/detach churn that was triggering the
  predictive-restore in the first place.
  (`lib/widgets/steward_overlay/steward_overlay_chat.dart`)

### Added — design work, not implementation
- `docs/discussions/agent-driven-mobile-ui.md §13` —
  floating-surface capacity model. Locks the recommendation that
  multi-conversation goes through **one shell + multi-conversation
  list inside** (Pattern B) rather than N independent pucks.
  Reasons: SSE bandwidth, drag/resize state, attention budget,
  and clean URI-router fit. Pattern A (N-pucks) is rejected;
  Pattern C (edge-dock) is a deferred cosmetic. Adds Q15 to the
  ADR-023 question set.
- `docs/plans/overlay-history-and-snippets.md` (new) — wedge plan
  bundling three QA gaps the principal flagged together: (W1)
  cold-start panel is empty until new SSE events arrive, (W2)
  user's own prior prompts never render even when steward output
  does, (W3) no quick-action chip strip above the input. ~250 LOC
  mobile-only, no hub change. Mirrors `agent_feed.dart`'s
  `listAgentEvents` + `streamAgentEvents(sinceSeq=...)` pattern
  for backfill. Hub already publishes user input as `kind =
  input.text` with `producer = user` (verified at
  `handlers_agent_input.go:378-407`); the wedge teaches the
  overlay controller to render that.

---

## v1.0.470-alpha — 2026-05-10

Two QA fixes after v1.0.469.

### Fixed
- **No more yellow double underlines under text in the steward
  overlay panel.** The expanded panel was a bare `Container`,
  with no `Material` ancestor — Flutter's classic "missing Material"
  debug hint draws yellow underlines under every Text in that
  scope. The overlay is mounted via `MaterialApp.builder`, which
  sits OUTSIDE the Navigator's Material/Scaffold scope, so the
  fix has to be local: wrap the panel column in
  `Material(type: MaterialType.transparency)` so descendants
  inherit a DefaultTextStyle without changing any pixel of the
  panel's appearance.
  (`lib/widgets/steward_overlay/steward_overlay.dart`)
- **Steward setup sheet no longer auto-pops.** The W4 first-run
  experience used to auto-trigger `showSpawnStewardSheet` from
  `_maybeShowBootstrap` whenever the Projects screen saw a
  configured team with online hosts and no steward. v1.0.468 added
  a `staleSince != null` guard but the sheet still popped once the
  cache hydrated as empty. Principal: stop auto-popping entirely.
  Removed both auto-trigger paths (initState postFrame +
  `ref.listen` on `hubProvider`); spawning the steward is now a
  fully manual gesture (the spawn sheet is still reachable from
  every previous manual entry point — `Replace steward`,
  Sessions screen, etc.).
  (`lib/screens/projects/projects_screen.dart`)

### Removed
- `_maybeShowBootstrap`, `_bootstrapAttempted` flag, and the
  `ref.listen<AsyncValue<HubState>>` block in
  `_ProjectsScreenState`. The `bootstrapDismissedKey` helper
  itself stays — it's still used by `confirmAndRecreateSteward`
  to clear the Skip flag on explicit recreate.

---

## v1.0.469-alpha — 2026-05-10

Steward overlay is now **non-modal** — a deliberate UX shift in
response to v1.0.468 QA. Principal report: tapping the Me bottom-
nav tab or scrolling the underlying page collapsed the panel
instead of doing what they wanted. Root cause was the full-screen
`Positioned.fill` barrier with `onTap: _collapse` and a translucent
black scrim — that's the *modal sheet* pattern, wrong for a
floating chat that's meant to coexist with whatever surface the
user is reading.

### Changed

- **Panel coexists with underlying page.** Removed the tap-to-
  close barrier and the scrim. Tap → bottom nav switches tabs.
  Scroll → page scrolls. The panel just floats on top with its
  border + drop shadow as the elevation cue.
- **Dismissal is explicit only.** Tap the X button in the panel
  header, or tap the puck — both still work. There's no longer
  any "tap outside to close" affordance because there's no
  outside-to-close concept in non-modal floating chat (Slack /
  Discord / iOS PiP all behave the same way).

This unblocks the original use case from the discussion doc §2:
"the steward is *above* the app, sharing the user's context, so
the user-to-system information loop runs at superfluid efficiency"
— with the modal barrier the loop was anything but.

---

## v1.0.468-alpha — 2026-05-10

Two QA items from v1.0.467 testing:

### Fixed

- **Spurious steward-bootstrap sheet on app open.** The Projects
  tab's `_maybeShowBootstrap` listener fires on every hub-state
  transition. On cold start the first transition is the
  cache-hydration event with `staleSince != null` — and the cached
  agents list may not reflect the *current* live steward (the user
  could have spawned one after the last cache write). Bootstrap
  spuriously fired before the network refresh landed, popping a
  "set up your steward" sheet over an already-running steward. The
  listener now skips while `staleSince != null` and re-evaluates on
  the next event after refreshAll succeeds (and clears stale).

### Added

- **Panel opacity slider** in Settings → Experimental → Steward
  overlay. Configurable 50–100% (default 85%); applies to the panel
  background only so chat text and inputs stay fully readable while
  the underlying page peeks through. Wrapping in `Opacity()` was
  rejected — it would fade messages too, wrong for a chat surface.
  New persisted key:
  `settings_steward_overlay_panel_opacity` (double 0.5..1.0).

---

## v1.0.467-alpha — 2026-05-10

Steward overlay input-box fixes from v1.0.466 QA. Two symptoms,
same root cause:

1. Deleting some text and retyping caused the deleted text to
   come back automatically.
2. Tapping a new cursor location and then typing made the cursor
   jump to the end.

Both are signatures of the `TextEditingController` being reset by
parent rebuilds. The chat widget watches
`stewardOverlayControllerProvider` (which emits on every SSE
event), and the controller previously lived in that watching
State — every steward event reached down through the TextField
subtree, occasionally re-applying a stale value.

### Fixed

- **TextField subtree is now isolated.** The input box moved out
  of `_StewardOverlayChatState` (which watches Riverpod) into its
  own `_ChatInput` `StatefulWidget` with its own
  `TextEditingController`. The chat parent's rebuilds no longer
  cascade into the input — only the input's own `setState` calls
  rebuild it.
- **Stable widget key** (`ValueKey('steward-overlay-chat-input')`)
  on `_ChatInput` so its State survives even if the parent's tree
  shape ever changes.
- **Send-clear timing.** Previously the controller was cleared
  AFTER the network round-trip, which wiped any text the user
  typed during the send. Now the controller clears immediately,
  and on send-failure the original text is restored only if the
  user hasn't started typing something new.
- **Multiline keyboard hints.** Added `keyboardType:
  TextInputType.multiline` and `textInputAction:
  TextInputAction.newline` so the IME treats the field as a
  multi-line composer instead of single-line text.

### Removed

- `_Composer` StatelessWidget (folded into `_ChatInput`).

---

## v1.0.466-alpha — 2026-05-10

Steward overlay layout customisation. Principal QA: the prototype's
fixed-size, bottom-anchored panel covers content the user is trying
to read while talking to the steward. The whole point of the
overlay is "see related info while directing the steward" — a
non-movable panel breaks that.

### Added

- **Drag the panel.** Panel header is now a drag handle (drag
  indicator icon + grab cursor on web). Drag from anywhere on the
  header bar except the close button to reposition the panel.
- **Resize the panel.** Bottom-right corner resize grip with a
  diagonal arrow icon. Drag to resize width + height; clamped to
  260×200 minimum and the viewport size.
- **Layout persists across app restarts.** Puck position + panel
  rect now survive via `shared_preferences`; previously the
  position reset to the bottom-right corner every cold start. New
  keys: `settings_steward_overlay_{puck_x,puck_y,panel_left,
  panel_top,panel_width,panel_height}` (all double, all null until
  first user customisation).
- **Settings → Experimental → Steward overlay toggle** (default
  on). Disable to hide the puck entirely; the controller no longer
  starts when disabled, freeing the SSE subscription too.

### Changed

- `_StewardOverlayHost` (in `main.dart`) now gates overlay mount on
  the new `stewardOverlayEnabled` setting in addition to hub config
  presence.

---

## v1.0.465-alpha — 2026-05-10

Agent-driven mobile UI prototype — first round of QA fixes from the
v1.0.464 test run. The principal reported that asking the steward
"take me to insights" produced no visible response or navigation in
the overlay — only a tool_call card in the session transcript.
Two root causes plus diagnostic improvements so the next test
surfaces what's happening directly in the panel.

### Fixed

- **Overlay chat now renders steward text replies.** `_extractText`
  in `steward_overlay_controller.dart` was reading `evt['body']` —
  but the hub's agent-events bus envelope publishes the assistant
  payload under `evt['payload']` (claude-sdk text frames carry
  `{"text": "...", "message_id": "..."}`). The body field never
  existed; every text reply was being silently dropped.
- **`mobile.intent` failure modes are now visible.** Every silent
  `return` in `_dispatchIntent` (empty URI, unparseable URI,
  navigator-not-ready) now appends a system message to the chat
  panel so the user can tell *which* path dropped the intent
  instead of seeing nothing.
- **SSE stream death is visible.** `onError` and `onDone` on the
  steward stream subscription now append a system message so a
  silent disconnect doesn't look like the steward simply not
  responding.

### Added

- `kDebugMode` console print of every incoming SSE frame
  (`[steward-overlay] evt kind=… keys=…`) so logcat reveals which
  events the overlay sees during diagnosis.

---

## v1.0.464-alpha — 2026-05-10

Agent-driven mobile UI prototype — first-stage spike. The steward
can now navigate the user's app to any v1 destination via a
persistent floating overlay; user talks (text + system-IME voice),
steward listens + responds + navigates. Read-only verbs only —
edits, approvals, ratifications still require manual taps. Validates
the architectural calls in
[`discussions/agent-driven-mobile-ui.md`](discussions/agent-driven-mobile-ui.md):
URI-shaped public API, shared-state model, the multiplex-screen
metaphor.

### Added

- **Persistent floating overlay** (`lib/widgets/steward_overlay/`).
  Draggable steward puck mounted at the app root via
  `MaterialApp.builder`; expands to half-height chat panel on tap.
  Survives Navigator pushes/pops + tab switches. Hides itself when
  the hub isn't configured.
- **`mobile.navigate(uri)` MCP tool** — new `mobile.*` tool family.
  Steward emits `termipod://...` URIs; hub publishes
  `mobile.intent` events on the general steward's bus channel;
  mobile dispatches via `navigateToUri`.
- **`POST /v1/teams/{team}/mobile/intent`** endpoint — validates
  URI scheme (`termipod://` / `muxpod://`), publishes the SSE
  event, records an `audit_events` row so steward-driven
  navigations are reviewable. 5 hub tests cover publish + audit
  + bad scheme + no-steward + missing-uri paths.
- **URI router** (`lib/services/deep_link/uri_router.dart`) —
  single dispatcher for both legacy `DeepLinkService` cold/warm
  links and the new steward intents. v1 grammar: top-level tabs
  (projects/activity/me/hosts/settings), project detail, session
  chat, agent detail, insights with scope qualifier.
- **Steward chat embedded in overlay**
  (`steward_overlay_chat.dart` + `steward_overlay_controller.dart`).
  Lazily ensures the general steward, subscribes to its SSE
  stream, demultiplexes text frames vs `mobile.intent` events.
  System-row + snackbar feedback ("Steward → \<label>") on every
  navigation; visible even when the chat panel is collapsed
  (puck-only).
- **`steward.general.v1` template** updated — exposes
  `mobile.navigate` in `default_capabilities`; bundled prompt
  gains a "Driving the mobile app" section listing the URI
  grammar + when to invoke navigate.
- **How-to test doc** —
  [`docs/how-to/test-agent-driven-prototype.md`](how-to/test-agent-driven-prototype.md).
  10 numbered scenarios with expected behaviour + failure-mode
  signatures so QA can report issues precisely.

### Changed

- `MyApp` accepts the navigator key as a constructor param; main
  exposes it via `overlayNavigatorKeyProvider` so the overlay
  controller can dispatch routes from the SSE listener
  (independent of widget-tree context).

### Documents

- New how-to: [test-agent-driven-prototype.md](how-to/test-agent-driven-prototype.md).
- The discussion doc at
  [`discussions/agent-driven-mobile-ui.md`](discussions/agent-driven-mobile-ui.md)
  is referenced by the prototype but stays Open — ADR-023 will
  resolve once the prototype findings come in.

### Lessons

- The package-level navigator key + provider override pattern
  cleanly hands a stable key to both `MaterialApp` and any
  controller that needs to push routes from outside the widget
  tree. Beats per-widget GlobalKeys juggled through callbacks.

---

## v1.0.463-alpha — 2026-05-09

Steward Insights wedge — surfaces an aggregate view of every live
steward (general + domain) and adds a time-range picker to the
fullscreen Insights view. Closes the "where do I see steward usage"
question raised after v1.0.462: the Persistent Steward Card jumps
straight into chat, so per-agent insights for stewards previously
required navigating to the Sessions screen, finding the right row,
and opening Agent Detail — none of which scaled past one steward.

### Added

- **Sessions AppBar → Insights icon.** Pushes a fullscreen
  `InsightsScreen` scoped to `team_stewards`. Aggregates every agent
  whose handle matches the steward predicate (`steward`, `*-steward`,
  or `@steward`) on the active team. One destination, all stewards.
- **`/v1/insights?team_id=X&kind=steward` qualifier.** Narrows the
  team-scoped aggregator to steward-handle agents. Response echoes
  `scope.kind = "team_stewards"` so the body is self-describing.
  `kind` is silently ignored on non-team scopes (hub-side
  `insights_scope.go`).
- **`by_agent` breakdown dimension on `/v1/insights`.** New top-level
  array with one row per agent in the scope (excluding agent scope,
  where the breakdown is degenerate). Each row carries
  `agent_id` / `handle` / `engine` / `status` / token totals /
  `turns` / `errors`. Sorted by `tokens_in` desc.
- **`InsightsByAgentSection` widget.** Renders the new dimension
  inside any non-agent-scope `InsightsScreen`. Steward rows get a
  small concierge badge; tap a row → drills into that single agent's
  fullscreen Insights view.
- **Time-range picker on `InsightsScreen`.** ChoiceChip row at the
  top — 24h / 7d / 30d. Selecting a chip re-keys the
  `insightsProvider` family (since/until are now part of
  `InsightsScope`'s identity), so each window has its own snapshot
  cache row that persists across screen revisits.

### Changed

- `InsightsScope` is now a value object that includes `since` /
  `until` plus the existing kind+id pair. Equality + hashCode were
  updated so the family provider's cache key includes the time
  window. `withWindow()` returns a copy with new bounds.
- `InsightsScreen` is now a `ConsumerStatefulWidget` (was
  `ConsumerWidget`). Holds the selected range + the "frozen now"
  timestamp so the family-provider cache key stays stable across
  rebuilds inside one chip view.

### Documents

- ADR-022 §D3 amended to call out `team_stewards` as a sub-qualifier
  on team scope (not a sixth top-level scope kind). Phase 2 plan
  status unchanged — this wedge ships post-MVP-completion.
- `api-overview.md` §3.13 updated to document `kind=steward` and
  the `by_agent` dimension.

### Lessons

- The package-level `hubInsightsCache` is shared across tests in one
  `go test` invocation — adjacent tests that hit the same
  `(scope_kind, scope_id, since, until)` key can see each other's
  bodies on systems with sub-nanosecond clock collisions. Added
  `resetInsightsCache()` test helper; new tests call it at start.

---

## v1.0.444 → v1.0.462-alpha — 2026-05-09

Observability work — ADR-022 + insights phases 1 + 2. Closed the
"how much have I spent / is the hub OK / where in the lifecycle is
this project" gap that the v1.0.440 device test surfaced.

### Added
- **`/v1/hub/stats` endpoint** (v1.0.444, ADR-022 D2). Hub-self
  observability: machine block (OS / CPU / RAM / kernel), DB block
  (per-table rows + bytes via `dbstat` virtual table when available,
  schema_version, WAL size), live block (active agents, open
  sessions, SSE subscribers). 30s row-count cache. Mobile renders a
  Hub group at the top of the Hosts tab + a fullscreen Hub Detail
  screen.
- **`/v1/insights` endpoint** with project scope (v1.0.449, ADR-022
  D3). Tier-1 dimensions: spend (tokens in/out, cache read/create),
  latency (p50/p95 of `turn.result.duration_ms`, linear
  interpolation), errors (failed turns, open attention),
  concurrency (active agents, open sessions, turns/min). Token
  rollups via `by_engine` + `by_model`. 30s response cache.
  Migration `0036_agent_events_project_id` adds `project_id` column
  + composite `(project_id, ts)` index + AFTER INSERT trigger that
  stamps from `sessions(scope_kind='project')` so the seven
  existing INSERT call sites stay untouched.
- **A2A relay throughput** in `/v1/hub/stats` (v1.0.456). 30s × 1s
  rolling window for aggregate + per-destination bytes/sec; `Begin`
  / `Record` / `Dropped` instrumentation on `handleRelay`. Mobile
  Hub Detail gains an A2A RELAY section.
- **`/v1/insights` multi-scope** — project / team / agent / engine /
  host (v1.0.457). Each scope has its own per-table SQL fragment in
  `insights_scope.go`. user_id parked: ADR-005's principal/director
  model has no users table at MVP. Mobile gains a typed
  `InsightsScope` value object; `getInsights` takes named-arg scope
  params and throws synchronously on >1.
- **Fullscreen `InsightsScreen`** (v1.0.458, ADR-022 D7). Activity
  tab AppBar gains an Insights icon that opens the screen with
  project scope (when project filter is set) or team scope.
- **Me tab Stats card** (v1.0.459). Today's tokens + Δ% vs prior 7d
  average via two-window read; tap → fullscreen team-scoped
  Insights.
- **Agent Detail Insights tab** + **Host Detail Insights button**
  (v1.0.460). Agent Detail's existing 3-tab controller grew to 4
  (embedded panel for agent-scoped tiles); Host Detail gained an
  Insights button that pops the sheet and pushes the fullscreen
  view scoped to host.
- **Tier-2 drilldowns** on `InsightsScreen` (v1.0.461 + v1.0.462):
  - Engine + model breakdown — share bars, tokens/turn ratio,
    sorted by tokens descending.
  - Multi-host distribution — per-host agent count + capability
    fingerprint; hides on degenerate scopes / single-host.
  - Tool-call efficiency — `tools` block (tool_calls excluding
    streaming `tool_call_update`, tools/turn, approval rate from
    `EXISTS json_each(decisions_json) → approve` walk). Mobile
    color-codes the rate (green ≥85%, warning ≥50%, error
    otherwise).
  - Lifecycle flow (project scope only) — `lifecycle` block with
    phase timeline (trailing phase runs to `now()`), ratification
    rate, criterion pass-rate, stuck count from
    `acceptance_criteria.state='failed'`. Mobile renders a
    timeline with a current-phase dot + rate bars + inline warning
    when stuck > 0.

### Changed
- **`agent_events` schema** — `project_id TEXT` column added by
  migration `0036`; existing rows backfilled from
  `sessions(scope_kind='project')`. New events stamped via AFTER
  INSERT trigger so the seven existing INSERT call sites need no
  edits.

### Deprecated / Deferred
- **W5e unit economics** ($/session, $/deliverable, $/attention)
  needs a pricing table (token×$ per model). ADR-022 marks pricing
  post-MVP; current token-based metrics are the MVP proxy.
- **W5f snippet usage telemetry** needs new instrumentation — the
  action bar fires the `snippet` action without emitting an event.
- **W6 p95 alert + materialized rollup** — fires on production load
  that doesn't exist yet; the trigger-deferred design *is* the
  design. Reopen when first real deployment crosses the p95 > 1s
  threshold.

### Documents
- **ADR-022** observability surfaces — locked 7 design decisions
  (Activity ≠ Insights, hub stats is purpose-built, scope-
  parameterized insights, agent_events.project_id column,
  rollups post-MVP, cache-first per ADR-006, six entry points + one
  fullscreen view).
- `plans/insights-phase-1.md` flipped to **Done**.
- `plans/insights-phase-2.md` flipped to **Done — MVP scope**;
  W5e/W5f/W6 marked deferred post-MVP with rationale.

### Lessons (architectural)
- Scope filter `SessionsClause` must prefix columns with `s.` so
  the same fragment slots into a JOIN with `attention_items`
  (which also has `scope_kind`/`scope_id`) without ambiguity.
- Time-bucket rate windows: `>=` cutoff vs `>` matters — `>` clips
  to 29s and biases the rate ~3% low.
- `SUM(CASE …)` returns NULL on zero rows; always wrap in
  `COALESCE` when scanning into a fixed Go type.
- `*Type` + `omitempty` on optional response fields — keeps the
  contract explicit ("the field IS sometimes absent") rather than
  emitting zeroed structs that look like real data.
- Dispatcher loops calling methods that block on the remote peer
  must run those calls in goroutines — synchronous dispatch
  deadlocks when the next event is what would unblock the previous
  one. (See v1.0.454 `InputRouter` fix; `feedback_input_router_dispatch_async.md`.)
- For `attention_items` decision walking, `EXISTS (SELECT 1 FROM
  json_each(...) WHERE json_extract(value, '$.decision') =
  'approve')` is the readable SQLite idiom — much cleaner than
  LIKE-substring or `$[#-1]` indexing.

---

## v1.0.443-alpha — 2026-05-09

### Changed
- **`tool_call_update` and non-`end_turn` `turn.result` now visible by
  default in the transcript** (correcting the v1.0.442 verbose-only
  approach). Reasoning: v1.0.442 hid both kinds behind the debug
  toggle, but the user reported they expected these wire frames in
  the normal log so they could trace approval-flow state. New rules:
  - `tool_call_update` shows standalone only when its parent
    `tool_call` is hidden by a gate (`request_approval`,
    `request_select`, `request_help`, `request_decision`,
    `permission_prompt`) — that's the case where the standalone card
    is the only place to see the wire result. For non-gated tools
    the update keeps folding into the parent card to avoid
    duplicating the latest status pill.
  - `turn.result` shows when `stop_reason != end_turn`. Cancelled /
    error / max-token / refused turns become inline cards (e.g. the
    cancelled in-flight prompt that gets replaced by an
    attention_reply). Clean `end_turn` boundaries stay silent so
    every reply doesn't add a "turn ended" card.
- **`input.attention_reply` card now leads with the rendered prompt
  text the agent received** (e.g. `[reply to approval_request
  01KR5CT6] Approved.`), not just the structured decision fields.
  Mobile ports `formatAttentionReplyText` (Go: `driver_stdio.go`) as
  `renderAttentionReplyText` (Dart) so the transcript matches
  exactly what the engine sees on the wire. Cross-language contract
  pinned by `attention_reply_render_test.dart` (Dart) +
  `TestFormatAttentionReplyText` (Go) — same input table, same
  expected outputs.

---

## v1.0.442-alpha — 2026-05-09

### Fixed
- **`input.attention_reply` rendered as raw JSON in the transcript.**
  The widget had handlers for `input.text` / `input.cancel` /
  `input.approval` but fell through to `_jsonPretty` on the
  attention-reply kind, so the principal's decision card read like a
  config dump. New `_inputAttentionReplyBody` shows decision / kind /
  option_id / reply / reason / request_id as a clean key-value
  block.

### Changed
- **`tool_call_update` and `turn.result` demoted from unconditional
  hide → verbose-gated.** They still drive folding (parent tool_call
  card status pill) and the telemetry strip, but the wire frames
  themselves are now revealed by the top-right debug chip alongside
  the existing `lifecycle / raw / system` reveals. This makes the
  request_approval gate's tool_call_update inspectable (it carries
  the attention_id + severity payload the inline approval card was
  built from) and surfaces the orphan `stopReason=cancelled` frame
  that the driver's attention_reply path produces by design. Verbose
  chip tooltip updated to "wire frames" so the surface is
  discoverable. Also added compact renderers for both kinds
  (`_toolCallUpdateBody`, `_turnResultBody`).

---

## v1.0.441-alpha — 2026-05-09

### Fixed
- **Cancel-button overlay stuck on after gemini ACP approval flow
  ended.** The ACP driver's `attention_reply` Input writes a fresh
  `session/prompt` with the rendered approval text — the only way to
  push a turn-based decision back to gemini-cli. That new prompt
  cancels the in-flight prompt that originally raised the attention
  (gemini replies `stopReason=cancelled` on the old id), and the new
  prompt then runs to `end_turn` normally. The bug: the
  `attention_reply` branch in `driver_acp.go` discarded the new
  prompt's response and never called `postTurnResult`, so mobile
  never saw the live `end_turn`. The orphan cancel + the streaming
  `partial:true` chunks left the busy walker glued to "turn in
  progress" and the cancel-button overlay stayed on long after the
  agent's final reply. Driver now mirrors the `text` branch:
  `postTurnResult(res)` after a successful attention-reply prompt.
  Test extended to assert the `turn.result(end_turn)` event is
  posted.

### Added
- **Batch ops on the Sessions list.** Long-press a session tile (or
  use the AppBar "Select…" menu) to enter multi-select mode. The
  AppBar swaps to a count badge + Select-all / Cancel actions; tiles
  render checkboxes; a bottom action bar exposes Archive (gated to
  archive-eligible rows) and Delete (gated to all-archived rows; hub
  refuses non-archived deletes). `SessionsNotifier.bulkArchive` /
  `bulkDelete` run sequentially with a single refresh at the end, and
  return per-id failures so the SnackBar can summarise instead of
  bursting one toast per failure.

### Changed
- **Mode/Model picker moved from inline strip → AppBar icon.** The
  ADR-021 W2.5 chip strip used to render above every transcript,
  costing a row of vertical real estate even on engines that never
  re-advertise mode/model after handshake. The picker now hangs off
  the SessionChatScreen AppBar as a single `tune` icon — tap opens
  one bottom-sheet showing both Mode and Model sections (whichever
  the agent advertised). Tooltip surfaces the current values so the
  current state is still glanceable without opening the sheet.
  `AgentFeed` exposes the picker payload via a new
  `onModeModelChanged` callback; the inline `_ModeModelStrip` is
  retired.

---

## v1.0.440-alpha — 2026-05-09

### Fixed
- **Image-attach button always hidden on mobile (ADR-021 Phase 4
  reachability bug).** AgentCompose's `_resolveImageAttachAffordance`
  read `agent['driving_mode']` from the `getAgent` response, but the
  hub serialises that field as `mode` (see `agentOut.Mode` in
  `hub/internal/server/handlers_agents.go`). Field mismatch made
  `drivingMode` always null → `resolveCanAttachImages` defaulted to
  `M4` → all families have `prompt_image['M4'] == false` → button
  hidden for every agent regardless of capability. Compose now reads
  `agent['mode'] ?? agent['driving_mode']` so the gate works on
  current hubs and stays forward-compat if a future payload reverts
  the field name.
- **Mode/Model picker chips never showed for ACP agents on cold
  start.** gemini-cli (and the ACP spec) returns the available
  mode/model lists *and* the current ids in the `session/new` /
  `session/load` response — NOT as `current_mode_update` /
  `current_model_update` notifications. The driver cached the lists
  locally for `set_mode` / `set_model` validation but never surfaced
  them to mobile, so `modeModelStateFromEvents` had nothing to walk
  and `_ModeModelStrip` rendered empty. Driver now emits a synthetic
  `kind=system, producer=system` event after handshake with the
  top-level `currentModeId / availableModes / currentModelId /
  availableModels` shape (matches the runtime notification path so
  the mobile reducer joins both transparently).
- **Stream-dropped banner appeared on idle network drops.** dart:io
  surfaces `HttpException: Connection closed before full body
  received` and similar on Android-doze / carrier-NAT-timeout /
  proxy-idle reaps — the SSE reconnect logic recovers transparently,
  but the banner pushed noise to a user with nothing to act on. The
  reconnect path's banner gate now suppresses the well-known idle
  signatures (`connection closed`, `connection reset`,
  `connection abort`, `connection terminated`, `before full body
  received`, `stream closed`); genuine connectivity loss
  (network-unreachable / DNS) still surfaces the banner.

---

## v1.0.439-alpha — 2026-05-09

### Fixed
- **ACP driver missing `attention_reply` Input handler.** When the
  principal approved a `request_approval` MCP attention on mobile,
  the hub's `/decide` resolved the DB row and posted
  `input.attention_reply` correctly, but the ACPDriver's `Input`
  switch had no case for `attention_reply` — the InputRouter call
  fell through to the default arm and returned `unsupported input
  kind "attention_reply"`. The agent's wake-up turn never reached
  gemini-cli, so the principal saw their decision card in the feed
  but the agent stayed idle waiting for them. Driver now mirrors
  the stdio + exec-resume pattern: render the structured payload
  via `formatAttentionReplyText` and dispatch as a fresh
  `session/prompt`. ACP needs none of the parked-JSON-RPC branch
  the codex appserver carries — `permission_prompt` on this driver
  goes through the dedicated `Input("approval")` path that responds
  on the original `session/request_permission` RPC.

### Added
- **Per-card fold/collapse toggle on every transcript card.**
  AgentEventCard gains a chevron in the header (next to the copy
  affordance) that collapses the card to a single-line preview;
  the whole header row is also a tap target so thumbs don't have
  to aim. Default is expanded for every kind so the existing
  transcript shape is unchanged on first render. Previously only
  `tool_call` and `tool_result` had built-in collapse behaviour;
  thoughts, approval-request cards, plans, diffs, system rows
  etc. now share the same affordance. Preview text reuses
  `_copyTextFor`'s output so what you see when collapsed is what
  you'd get on copy.

---

## v1.0.438-alpha — 2026-05-09

### Fixed
- **Duplicate transcript bubbles after gemini-cli M1 resume.** A
  resumed session showed the previous turn's `agent_thought_chunk`
  and `agent_message_chunk` rendered a second time in the feed,
  duplicating cached content. Root cause: the M1 driver's
  `replayActive` window closed the moment the `session/load` response
  arrived, but gemini-cli@0.41.2 emits the final burst of historical
  `session/update` notifications AFTER the response (last turn's
  trailing chunks land ~50µs to ~100ms after the load reply, on the
  same connection). Those trailing frames went out without
  `replay: true`, so mobile's W1.3 dedupe couldn't recognize them as
  already-cached and rendered them as live. Fix keeps the replay
  window open until the operator's first `Input()` (text / cancel /
  approval / attach / set_mode / set_model) — autonomous agent
  emissions in the gap between `session/load` and a user action are
  by definition either historical replay (deduped on content key) or
  capability-state (`available_commands_update`,
  `current_mode_update`, `current_model_update` — already routed
  through the no-replay-tag system path). Updates
  `TestACPDriver_TagsReplayEvents` to cover the trailing-history and
  post-Input cases.

---

## v1.0.437-alpha — 2026-05-09

### Fixed
- **ACP approval card rendered as already-cancelled on resume.** A
  `session/request_permission` arriving on a freshly-resumed gemini-cli
  M1 session showed `decided: cancel` before the user tapped anything.
  Root cause: the driver surfaced the agent's raw JSON-RPC `id` as the
  externally-visible `request_id` in the `approval_request` agent_event.
  Each spawn of the agent (including resume after pause) restarts
  gemini's outbound id counter from a low number, so id=`0` from the
  current spawn collided with id=`0` from a previous spawn that had
  been cancelled. Mobile's `resolvedApprovals` map is keyed by
  `request_id` and persists across spawns via the agent_events history;
  the colliding id made the new card render as already-decided. Fix
  namespaces the externally-visible `request_id` with a per-spawn
  nonce + monotonic counter (`<UnixNano>-<n>`), keeping the agent's
  raw JSON-RPC `id` only in the internal `pendingPerm` map so we still
  respond on the correct RPC. Codex and claude were unaffected
  (codex uses hub-generated `attention.ID`, claude uses
  Anthropic-generated `tool_use_id`s — both globally unique by
  construction). Regression test
  `TestACPDriver_PermissionRequestIDUniquePerSpawn` asserts two
  consecutive spawns each emitting id=`0` produce distinct request_ids.

---

## v1.0.435-alpha — 2026-05-08

### Added
- **Mobile image-attach UI (ADR-021 W4.6).** Closes Phase 4 of the
  ACP capability surface plan. AgentCompose gains an attach button
  (paperclip / image icon) gated on `family.prompt_image[mode]`:
  - claude-code on M1/M2 → engaged
  - codex on M1/M2 → engaged
  - gemini-cli on M1 (--acp) → engaged
  - gemini-cli on M2 (exec-per-turn) → hidden (the driver-side
    W4.5 strip-and-warn is a fallback for forwarded payloads, not
    an invitation to send them)
  Tap → image_picker → ImageConverter (1024px max edge / 70% JPEG)
  → base64 → enqueued on the composer. Up to 3 thumbnails render
  in a horizontal strip above the text field with × removal taps.
  Pre-flight cap matches the hub W4.1 validator (5 MiB decoded).
  On send, the queued images ride alongside the text body in
  `postAgentInput(images: …)`. Body becomes optional when at least
  one image is queued — image-only turns are first-class. Family
  registry endpoint now serves `prompt_image` and
  `runtime_mode_switch` so the mobile gate has the same view the
  hub does. Top-level `resolveCanAttachImages` helper exposed via
  `@visibleForTesting` for the gate-decision unit test.

---

## v1.0.434-alpha — 2026-05-08

### Added
- **gemini-exec image strip + warn (ADR-021 W4.5).** ExecResumeDriver
  now intercepts `payload["images"]` on text input. gemini's
  exec-per-turn argv (`gemini -p "<text>"`) has no inline-image
  affordance, so the driver:
  - emits a `kind=system` event with engine=`gemini-exec`,
    reason="…no inline image support — switch to gemini --acp (M1)
    for multimodal turns…", dropped=count, so the principal sees
    why the attachment didn't reach the model and what to do
    about it,
  - lets the text portion proceed normally as `gemini -p <body>`.
  Image-only inputs (no body) emit the warning and then return the
  existing missing-body error so the principal isn't left thinking
  silence = success.

---

## v1.0.433-alpha — 2026-05-08

### Added
- **ACP image content blocks (ADR-021 W4.4).** ACPDriver's `text`
  Input branch now lowers `payload["images"]` entries to ACP shape
  `{type:"image", mimeType, data}` and leads them in the
  `session/prompt.params.prompt` array; the text block (if any)
  trails. promptCapabilities.image is now lifted from the agent's
  `initialize` response into a tri-state cache: absent → permitted
  (forward-compat with agents that omit the field), explicit
  `false` → strip + emit a `kind=system` warning event, explicit
  `true` → forward as-is. When images are stripped and there's no
  body left, the call returns a typed error so the operator
  notices instead of dispatching an empty turn. Image-only inputs
  (no body) are accepted when capability allows.

---

## v1.0.432-alpha — 2026-05-08

### Added
- **Codex image content blocks (ADR-021 W4.3).** AppServerDriver's
  `startTurn` now takes an `images []imageInput` arg and lowers
  each entry to OpenAI responses-API shape
  `{type:"input_image", image_url:"data:<mime>;base64,<b64>"}`.
  Image blocks lead the `turn/start.params.input` array; the
  `{type:"text"}` block (if any) trails so the model sees the
  imagery before the question. Image-only inputs (no body)
  produce a single image block. attention_reply path passes
  `nil` images — replies remain text-only by design.

---

## v1.0.431-alpha — 2026-05-08

### Added
- **Claude image content blocks (ADR-021 W4.2).** StdioDriver's
  `buildStreamJSONInputFrame` text branch now produces a content
  array. Image inputs from `payload["images"]` lower to Anthropic's
  stream-json shape `{type:"image", source:{type:"base64",
  media_type, data}}` and lead the array; the text block (if any)
  comes last so the model reads the question after seeing the
  imagery. Image-only inputs (no body) are accepted. Hub-side
  validation (W4.1) already enforced mime/size/count caps so the
  driver trusts the payload shape. Shared
  `extractImageInputs(payload)` helper extracted to
  `image_inputs.go` so W4.3 (codex) and W4.4 (ACP) reuse the same
  type-assertion ladder.

---

## v1.0.430-alpha — 2026-05-08

### Added
- **Hub input contract for `images: []` (ADR-021 W4.1).** Opens Phase 4
  of the ACP capability surface plan. `POST /agents/{id}/input` accepts
  an optional `images: [{mime_type, data}]` array alongside `body`.
  Validation: mime allowlist (`image/png` / `image/jpeg` / `image/webp`
  / `image/gif`), well-formed base64, ≤5 MiB decoded per image, ≤3
  images per request. Caps are the lower bound across our engines so
  any accepted payload is acceptable to every content-array driver.
  Plumbed onto `payload_json["images"]` verbatim — per-driver shape
  mapping (Anthropic image_source / OpenAI input_image / ACP
  prompt-array) lands in W4.2–W4.4. Drivers that don't know about
  images ignore the field, so text-only turns remain backward-
  compatible. UI surface (composer attach branch) lands in W4.6.
  HubClient gains an `images` named parameter on `postAgentInput`;
  consumers don't yet send it.

---

## v1.0.424-alpha — 2026-05-08

### Added
- **Mobile mode + model picker UI (ADR-021 W2.5).** Closes Phase 2 of
  the ACP capability surface plan. AgentFeed now renders a small
  ActionChip strip above the message list when the active agent has
  advertised mode and/or model state via system notifications
  (`currentModeId` / `availableModes` / `currentModelId` /
  `availableModels`). Tap → bottom-sheet picker → `postAgentInput`
  with `set_mode` / `set_model`. The wire payload is engine-neutral;
  the hub's `runtime_mode_switch` table (W2.1) routes per-driver:
  gemini M1 RPC → instant; claude/codex respawn → ~3-5s with the
  transcript intact via the engine_session_id resume cursor; gemini
  exec-per-turn → applies on the next prompt.

### Changed
- `HubClient.postAgentInput` gains `modeId` / `modelId` named
  parameters mirroring the new hub input contract.

## v1.0.423-alpha — 2026-05-08

### Added
- **NextTurnMode / NextTurnModel for gemini-exec (ADR-021 W2.4).**
  Lights up the `per_turn_argv` route declared by W2.1. ExecResumeDriver
  gains `Input("set_mode")` / `Input("set_model")` cases that stash the
  override on `nextTurnMode` / `nextTurnModel`; the next `runTurn`
  consumes the slot and splices `--approval-mode <id>` / `--model <id>`
  into argv. One-shot semantics by design (sticky behavior is a
  follow-up wedge): an absent override falls through to the rendered
  cmd's existing flags. When the mode override fires, the legacy
  `--yolo` flag is suppressed for that turn so `--approval-mode` wins.

## v1.0.422-alpha — 2026-05-08

### Added
- **Respawn-with-mutated-spec for claude/codex (ADR-021 W2.3).** Lights
  up the `respawn` route declared by W2.1. New helper
  `respawnWithSpecMutation` reads the active session's
  `spawn_spec_yaml`, surgically swaps the per-engine flag (claude:
  `--model` / `--permission-mode`; codex: `--model` / `--approval-policy`)
  via a yaml.v3 Node-API mutator that preserves all other fields
  byte-for-byte, splices the engine_session_id resume cursor (ADR-014
  for claude, W1.2 for ACP), enqueues a host-runner terminate, and
  calls `DoSpawn` with the existing `SessionID` so the prior agent is
  swapped inside one tx. Transcript continuity rides on the session
  row; the picker selection lands as a fresh `--model` argv on the
  new pane.
- New `mutateBackendCmdFlag(specYAML, flag, newValue)` returns
  `errFlagNotInCmd` when the rendered cmd doesn't carry the target
  flag — surfaced as 422 by the input handler so mobile shows
  "this template doesn't expose <flag>" rather than a silent no-op.

### Changed
- `POST /agents/{id}/input` `set_mode`/`set_model` on a respawn-route
  family no longer returns 501; happy path now responds 202 and lands
  a real respawn. Failure modes map to typed 422s
  (`errUnknownFamilyField`, `errFlagNotInCmd`).

## v1.0.421-alpha — 2026-05-08

### Added
- **ACP `session/set_mode` + `session/set_model` driver dispatch
  (ADR-021 W2.2).** ACPDriver caches the agent's `availableModes` /
  `availableModels` id sets at session/new (and session/load) time
  and exposes two new Input kinds — `set_mode { mode_id }` and
  `set_model { model_id }`. Each validates the requested id against
  the cached set before dispatching the matching ACP RPC, so a typo
  fails locally without burning a round trip. An agent that didn't
  advertise modes/models at handshake gets a typed
  "did not advertise modes/models" error rather than a silent no-op.
  W2.1's hub routing already emits these as input.set_mode /
  input.set_model events for gemini-cli M1 (route=rpc); the driver
  picks them up via the existing InputRouter polling loop.

## v1.0.420-alpha — 2026-05-08

### Added
- **`runtime_mode_switch` family declaration + hub routing (ADR-021
  W2.1).** Opens Phase 2 of the ACP capability surface plan. Each
  `agent_families.yaml` entry declares one of `rpc | respawn |
  per_turn_argv | unsupported` per driving_mode (M1/M2/M4) — keyed by
  mode rather than per-family because gemini-cli supports both M1
  (rpc) and M2 exec-per-turn (per_turn_argv) and a single string
  couldn't disambiguate. `POST /agents/{id}/input` accepts new kinds
  `set_mode` (with `mode_id`) and `set_model` (with `model_id`); the
  handler resolves `(family, driving_mode)` against the
  runtime_mode_switch table and dispatches: rpc/per_turn_argv → emit
  input event for driver pickup (handlers ship in W2.2/W2.4);
  respawn → call `respawnWithSpecMutation` helper (stub returns 501
  until W2.3 lands the real string-edit + pause/spawn orchestration);
  unsupported → 422. Mobile sends one shape; only the wire path
  varies per engine.
- **Family declarations:** `claude-code` = respawn (M1 + M2);
  `gemini-cli` = rpc (M1) / per_turn_argv (M2); `codex` = respawn
  (M1 + M2). M4 is unsupported across the board (tmux pane scrape
  has no model concept).

### Changed
- `agentfamilies.Family` gains a `runtime_mode_switch map[string]string`
  field; mirrored on the wire shape `AgentFamilyFromHub` so probe
  sweeps see the same declaration the hub-server consults.

## v1.0.413-alpha — 2026-05-08

### Added
- **ACP `authenticate` after `initialize` (ADR-021 W1.4).** Closes
  Phase 1 of the ACP capability surface plan. ACPDriver now lifts
  `authMethods` from the initialize response and, when non-empty,
  dispatches `authenticate(methodId=...)` before `session/new` /
  `session/load`. Selection precedence: explicit
  `SpawnSpec.AuthMethod` (steward template) → family default
  (`agent_families.yaml`'s `default_auth_method`) → first
  non-interactive method in the agent's advertised list. Empty
  `authMethods` is treated as pre-authenticated and skipped.
- **`gemini-cli` family default = `oauth-personal`.** Targets the
  single-user-developer case (`gemini auth` once on the host caches
  tokens at `~/.gemini/oauth_creds.json`; the daemon reuses them
  without opening a browser). Service-account / shared-host
  deployments override via `auth_method: gemini-api-key` in the
  steward template.
- **`attention_request` agent_event for auth failures.** When
  authenticate returns rpc-error, only-interactive methods are
  available with no preference, an explicit `auth_method` doesn't
  match the agent's advertised list, or the call hits
  `AuthTimeout` (default 30s), the driver emits a typed
  `attention_request` event with `kind: auth_required`,
  the configured method, the available method options, and a
  remediation hint, then fails Start. Surface for principal-level
  resolution (run `gemini auth`, set `GEMINI_API_KEY`, or override
  the steward template) without silent infinite hangs.

### Tests
- 6 new ACPDriver tests cover: skip when no methods, explicit
  preference wins, first-non-interactive fallback, attention on
  interactive-only, attention on rpc failure, attention on
  preference-not-in-advertised-list typo.
- 2 new launch_m1 tests pin the resolution precedence (spec
  override beats family default; family default applies when spec
  is empty).

---

## v1.0.412-alpha — 2026-05-08

### Added
- **Mobile dedupe for `replay:true` events (ADR-021 W1.3).** First
  APK-touching wedge of the ACP capability surface plan. The
  AgentFeed renderer now filters incoming SSE events flagged
  `replay: true` by `agentEventReplayKey`, dropping any whose
  content-stable key matches an event already in the cached
  transcript. Without this, a session/load resume re-renders every
  prior turn under the new agent's stream, doubling the visible
  transcript. Keys are content-based (text body, tool_call_id,
  request_id) because hub-side ids and seqs differ between the
  dead agent's original event and the resumed agent's replay.
- **`agentEventReplayKey` + `agentEventIsReplay` helpers** — exported
  via `@visibleForTesting` so the dedupe contract has a unit-test
  pin (`test/widgets/agent_feed_replay_dedupe_test.dart`). Keying
  by kind: text/thought → length-prefixed body; tool_call →
  tool_call_id; tool_call_update → tool_call_id + status;
  approval_request → request_id. Other kinds (raw, lifecycle,
  system, plan, diff) pass through replay unchanged — better
  to duplicate than to drop on a fragile match.

---

## v1.0.411-alpha — 2026-05-08

### Added
- **ACP `session/load` on respawn (ADR-021 W1.2).** When the hub
  resumes a gemini-cli session that has a captured engine cursor,
  it now injects `resume_session_id: <id>` into the rendered
  `spawn_spec_yaml`. `SpawnSpec.ResumeSessionID` plumbs the value
  through `launch_m1.go` to `ACPDriver.ResumeSessionID`. On
  handshake, the driver caches `agentCapabilities.loadSession`
  from the `initialize` response; when both the cursor is set AND
  the agent advertises load support, it calls `session/load`
  instead of `session/new`. On load failure (stale cursor, agent
  doesn't actually implement the method), the driver logs a
  warning and falls back to `session/new` so the operator still
  gets a session — fresh, but usable.
- **Replay event tagging.** Session/update notifications streamed
  by the agent during `session/load` (the historical-turn replay)
  are tagged `replay: true` in their event payloads via the new
  `tagIfReplay` helper. Live notifications after Start completes
  are unaffected. Mobile-side dedupe (W1.3) consumes this flag.
- **`spliceACPResume` helper.** Sibling to `spliceClaudeResume` —
  yaml.v3-Node-based top-level field injection so the cursor
  flows through the same template-derived YAML pipeline as
  claude's `--resume` cmd splice. Defensive: empty cursor →
  no-op, idempotent, replaces a stale prior id.

### Tests
- 4 new ACPDriver tests cover load-when-capable, fallback when
  loadSession unsupported, fallback on rpc-error, and replay
  tagging round-trip.
- 4 new `spliceACPResume` shape tests + 1 end-to-end resume test
  (`TestSessions_ResumeThreadsACPCursor`) pin the gemini-cli
  resume path mirror of the claude resume pin.

---

## v1.0.410-alpha — 2026-05-08

### Added
- **ACP `session.init` event for engine-side cursor capture
  (ADR-021 W1.1).** `ACPDriver.Start()` now emits a dedicated
  `session.init` agent event with `producer=agent` after the ACP
  `session/new` handshake completes. The hub's engine-neutral
  `captureEngineSessionID` (gate: `kind=session.init &&
  producer=agent`) lifts the gemini sessionId into
  `sessions.engine_session_id` — same column claude already uses
  per ADR-014. No migration; column existed since 0033. This is
  the prerequisite for W1.2 (`session/load` on respawn): without
  the cursor in the database, there is nothing to splice on
  resume. Tests cover the driver-side emission and the hub-side
  capture for `kind=gemini-cli` agents.

---

## v1.0.349-alpha+1 — 2026-04-30 (docs/tooling, no app rebuild)

### Added
- **Glossary** ([`docs/reference/glossary.md`](reference/glossary.md))
  — canonical defs for every project-specific term that has more
  than one possible meaning. ~50 entries across 11 domains
  (Sessions, Agents, Engines, Hosts, Events, Attention, UI,
  Protocols, Storage, Process). Each entry has a one-line def, an
  optional *Distinguish from:* line, and a link to its canonical
  concept doc. §12 indexes the "easy to confuse with" pairs for
  fast disambiguation. Trigger: 200K LOC of accumulated drift +
  the 2026-04-30 claude-code resume bug, which surfaced because
  *session* meant two different things in two adjacent layers and
  nothing pinned the boundary.
- **doc-spec §7 — term-consistency contract.** Codifies the rules:
  first-use linking to glossary, no new term without an entry in
  the same commit, qualifier required when ambiguous. CI lint
  enforces #1 and #2; #3 is review discipline.
- **CI lint** (`scripts/lint-glossary.sh`). Four checks: glossary
  structure (no orphan headings), §12 index integrity, spelling-
  variant drift detection across all docs (with code-context
  filtering so `hub/internal/hostrunner` package paths don't
  false-flag), and a warning-level new-term gate. Wired into
  `.github/workflows/ci.yml` alongside the existing
  `lint-docs.sh`.
- **PR template** gains a "Term consistency" section pointing at
  the glossary contract and the local lint command.
- **Tester / end-user UI guide**
  ([`docs/how-to/report-an-issue.md`](how-to/report-an-issue.md))
  — bug-report template + annotated ASCII layouts of every major
  screen + UI vocabulary (AppBar, BottomNav, BottomSheet, Card,
  Chip, ListTile, FAB, TabBar, …) + verb glossary (tap vs
  long-press vs swipe) + common confusion points (Resume vs Fork,
  agent vs engine, status chip colours). Parallel artifact to the
  engineering glossary, audience: testers and normal users.

### Changed
- **doc-spec.md** restructured: §7 is the new term-consistency
  contract; §8 (was §7) is the contract for new docs; §9 (was §8)
  lists CI lints; §10/§11 (open questions / references)
  renumbered.
- **Two real prose drift fixes** caught by the new lint:
  `host runner` → `host-runner` in
  `discussions/transcript-ux-comparison.md` and
  `plans/agent-state-and-identity.md`.
- **`discussions/transcript-source-of-truth.md`** status block
  forwarded to ADR-014 (the operation-log framing this discussion
  rests on); broken auto-memory cross-link replaced with a memory
  reference (not a doc link).
- **`docs/README.md`** index gains pointers to glossary +
  report-an-issue.

---

## v1.0.349-alpha — 2026-04-30

### Fixed
- **Claude-code resume actually resumes** ([ADR-014](decisions/014-claude-code-resume-cursor.md)).
  Pre-v1.0.349, tapping Resume on a paused claude-code session
  spawned a fresh engine session every time — same hub transcript
  window, brand-new claude conversation cursor. The CLI flag exists
  (`claude --resume <session_id>`); the hub just never threaded it.
  Surfaced from device-test feedback on v1.0.348-alpha.

  Three pieces, one wedge:
  - **Migration `0033`** adds `sessions.engine_session_id TEXT`.
    Engine-neutral column — claude calls it `session_id`, gemini
    calls it `session_id`, codex calls it `threadId`; all three
    can land their cursors here as their capture paths get wired.
  - **Capture path** (`captureEngineSessionID` in
    `handlers_sessions.go`). The `POST /agents/{id}/events`
    handler watches for `kind=session.init && producer=agent`
    frames, lifts `payload.session_id` from claude's stream-json
    `system/init` (already extracted by `StdioDriver.legacyTranslate`
    at `driver_stdio.go:295`), and `UPDATE`s the live session row.
    Best-effort — capture failure can't fail the event insert; the
    worst case is a cold-start resume, the pre-ADR-014 baseline.
    `kind=text` events that happen to carry session_id are
    explicitly ignored, as are `producer=user` echoes.
  - **Splice path** (`spliceClaudeResume` in `resume_splice.go`).
    `handleResumeSession` reads `engine_session_id` alongside
    `spawn_spec_yaml`. When the dead agent's `kind=claude-code`
    and a cursor exists, the helper walks the spec's yaml.v3 node
    tree to `backend.cmd`, strips any prior `--resume <other>`
    pair, and splices `--resume <id>` directly after the `claude`
    binary token. The handler passes the rewritten spec to
    `DoSpawn` but never `UPDATE`s `sessions.spawn_spec_yaml`, so
    successive resumes always splice from a clean cmd.

  Codex (`AppServerDriver.ResumeThreadID`) and gemini
  (`ExecResumeDriver.SetResumeSessionID`) already have the
  driver-side resume plumbing; both are still waiting on hub-side
  capture paths to feed them. Tracked as ADR-014 OQ-1 / OQ-2.

  11 resume-cursor tests: 7 splice unit tests (basic shape,
  idempotence, prior-id replacement, non-claude passthrough, empty
  inputs, malformed yaml, missing key, absolute path bin) + 3
  capture + 2 end-to-end resume tests proving
  `agent_spawns.spawn_spec_yaml` carries `--resume <id>` after a
  warm resume and stays clean after a cold one + 1 fork guard
  (`TestSessions_ForkDoesNotInheritEngineSessionID`) pinning the
  fork-is-cold-start invariant so a future "helpfully" inheriting
  change fails loudly at CI rather than mid-conversation.

### Added (continued)
- **Hub transcript is the operation log** ([ADR-014](decisions/014-claude-code-resume-cursor.md) OQ-4 input-side).
  The three engines all ship interactive commands that mutate
  engine-side context without emitting any frame back: claude's
  `/compact` `/clear` `/rewind`, gemini's `/compress` `/clear`. The
  engine's view of the conversation silently diverges from the
  hub's `agent_events` log — same `engine_session_id`, smaller or
  differently-shaped context. Without observability the operator
  scrolls back through what *looks* like a continuous transcript
  and gets surprising agent answers grounded in a context that no
  longer matches what they're reading.

  v1.0.349 ships the input-side observable. The hub's input route
  watches `kind=text` bodies for a leading per-engine slash command
  and, on match, emits a follow-up typed `agent_event` row with
  `producer=system` and `kind ∈ {context.compacted, context.cleared,
  context.rewound}`. Mobile renders these as inline operation chips
  so the transcript reads "[user] /compact → [system] context
  compacted" — same hub session, same `engine_session_id`, but the
  marker pins where the engine view diverged.

  Per-engine vocabulary in
  `hub/internal/server/context_mutation.go`:
  - claude-code: `/compact`, `/clear`, `/rewind`
  - gemini-cli: `/compress`, `/clear`
  - codex: TBD — slash vocabulary not yet audited; emission is a
    no-op until ADR-014 OQ-4b lands

  Engine-*emitted* mutations (e.g. claude's auto-compact when the
  context window fills) still aren't observable — those need the
  engine's stream to surface the event, which is option α deferred
  in `discussions/fork-and-engine-context-mutations.md`.

  10 new tests: 5 detector unit tests (per-engine vocab, leading-
  slash discipline, case sensitivity, unknown-engine no-op) + 5
  end-to-end input-route tests proving the marker lands at
  `seq=N+1` after the input.text row, that plain text emits no
  marker, that non-text input kinds (answer, etc.) skip the
  detector even when their body looks slash-y, and that codex
  agents stay silent until their vocabulary is audited.

### Changed
- **ADR-014 expanded** with the fork-is-cold-start section, the
  hub-vs-engine session boundary (cursor inheritance forbidden),
  and four open questions for follow-up wedges:
  OQ-1 codex `threadId` capture, OQ-2 gemini cross-restart cursor
  feeder, OQ-3 reconcile-driven respawn, **OQ-4 engine-side
  context mutations** (claude `/compact` `/clear` `/rewind`,
  gemini `/compress` — the hub today doesn't observe these and
  the engine's view of the conversation drifts from the hub's
  `agent_events` log without any marker frame), and OQ-5 fork
  productisation. Cross-linked to a new
  [`discussions/fork-and-engine-context-mutations.md`](discussions/fork-and-engine-context-mutations.md)
  that maps the design space across both axes (fork carryover +
  mutation observability) for the next wedge to start from.
- **`docs/decisions/README.md`** index gains rows for ADR-013 and
  ADR-014 — the prior wedge's index update was missed in v1.0.348.

---

## v1.0.348-alpha — 2026-04-29

### Added
- **Gemini integration via exec-per-turn-with-resume** ([ADR-013](decisions/013-gemini-exec-per-turn.md)).
  Third engine alongside claude-code (M2 stream-json) and codex
  (M2 app-server JSON-RPC). gemini-cli has no `app-server`
  equivalent, but headless mode now emits a stable `session_id`
  (PR [#14504](https://github.com/google-gemini/gemini-cli/pull/14504),
  Dec 2025) and accepts `--resume <UUID>` for cross-process session
  continuity. Wedge shipped as slices 1-6, all in this release:
  - **Slice 1:** ADR-013 written; ADR-011 D6 + ADR-012 D6 cross-link
    the per-engine `permission_prompt` matrix.
  - **Slice 2:** gemini-cli frame profile in `agent_families.yaml`
    — top-level `type`-keyed dispatch (init/message/tool_use/
    tool_result/error/result) into the same typed agent_event
    vocabulary claude/codex emit. M2 added to supports. No
    evaluator extension needed (unlike codex's dotted-path
    matchesAll).
  - **Slice 3:** `driver_exec_resume.go` is the spawn-per-turn
    driver. Captures `session_id` from the first `init` event,
    threads `--resume <UUID>` through every subsequent argv;
    `SetResumeSessionID` seeds the cursor on host-runner restart.
    `launch_m2` short-circuits family=gemini-cli before the
    long-running spawn machinery — exec-per-turn doesn't anchor a
    pane (PaneID=""), the bin is resolved via `exec.LookPath`, and
    a `CommandBuilder` injection seam keeps tests off real exec.
  - **Slice 4:** `permission_prompt` is unsupported on gemini
    (ADR-013 D4 — gemini has no in-stream approval gate). Driver
    rejects `attention_reply` with `kind=permission_prompt` as a
    defense-in-depth check. Reference + discussion docs grew the
    per-engine matrix (Claude sync, Codex turn-based, Gemini
    unsupported). Stewards self-route through `request_approval`.
  - **Slice 5:** per-family MCP config materializer adds
    `<workdir>/.gemini/settings.json` (JSON, stdio command+env shape
    matching claude's `.mcp.json` — gemini-cli's `mcpServers`
    schema accepts it identically). 0o600 inside .gemini/ 0o700.
    No CODEX_HOME-style env trick needed; gemini reads project-
    scoped settings.json automatically.
  - **Slice 6:** `agents.steward.gemini.v1` template + prompt ship
    in the embedded fs. Spawn cmd is bin-only (`gemini`) — the
    driver appends `-p <text> --output-format stream-json
    --resume <UUID> --yolo` per turn, ADR-013 D7. Prompt grows a
    "Decisions that need approval" section since gemini has no
    engine-side gate.

  15 new tests cover every wire-format contract: 7 driver tests
  (first-turn argv, second-turn --resume threading, rehydration,
  Stop interrupting in-flight Wait, permission_prompt rejection,
  nil CommandBuilder), 4 MCP-config tests (wire shape, escapes,
  perms, dispatcher branch isolation), 3 frame-profile tests
  (corpus, payload fields, embedded), 1 embedded-template test.
  Slice 7 (cross-vendor `request_help` smoke against live codex +
  live gemini binaries) remains unfunded and gated on a test host
  with both binaries installed — same gate as ADR-012 slice 7.

### Changed
- **Roadmap "Now" gains the gemini wedge** as Done; verifying on
  device next. The "Next" entry "Gemini exec-per-turn driver"
  collapses into the cross-vendor smoke (slice 7 × 2) — codex and
  gemini share the integration-smoke gate.

---

## v1.0.347-alpha — 2026-04-29

### Added
- **Codex integration via app-server JSON-RPC** ([ADR-012](decisions/012-codex-app-server-integration.md)).
  Codex CLI joins claude-code as a first-class engine; the hub
  drives `codex app-server --listen stdio://` over a long-lived
  JSON-RPC pipe rather than `codex exec --json` per turn. Wedge
  shipped as slices 1-6:
  - **Slice 2 (v1.0.343):** frame profile in `agent_families.yaml`
    translates app-server's thread/turn/item lifecycle plus
    telemetry into the same typed agent_event vocabulary
    claude uses. `matchesAll` grew dotted-path support
    (`params.item.type: agentMessage`) for one-method-many-types
    dispatch.
  - **Slice 3 (v1.0.344):** `driver_appserver.go` is the JSON-RPC
    client + thread manager. Handshake is initialize → initialized
    notification → thread/start (or thread/resume <id>); Input(text)
    maps to turn/start; the Driver interface is the launch_m2
    return type so codex and claude both fit.
  - **Slice 4 (v1.0.345):** approval bridge. Codex's
    `item/commandExecution/requestApproval` and siblings POST an
    `attention_items` row (kind=permission_prompt) and park the
    JSON-RPC request id locally; `dispatchAttentionReply` fires for
    permission_prompt too, and the driver's `Input("attention_reply")`
    looks up the parked id and writes the per-method JSON-RPC
    response on /decide resolution. Vendor-neutral equivalent of
    Claude's permission_prompt without the canUseTool sync limit.
  - **Slice 5 (v1.0.346):** per-family MCP config materializer.
    Claude keeps `.mcp.json`; codex writes `.codex/config.toml`
    (TOML, hand-formatted, no library dep). Token at 0o600.
  - **Slice 6 (v1.0.347):** `agents.steward.codex.v1` template +
    prompt ship in the embedded fs. Spawn cmd
    `CODEX_HOME=.codex codex app-server --listen stdio://` bypasses
    codex's trusted-projects gate.
- **Decision history on Me page.** Clock icon opens recent resolved
  attentions; tap into one to see the per-decision audit trail
  (timestamp, decider, verdict, reason/body/option) on the detail
  screen.

### Changed
- **Permission_prompt is now per-engine, not per-architecture.**
  Sync on Claude (canUseTool contract); turn-based on Codex
  (app-server deferrable JSON-RPC). ADR-011 D6's
  bridge-mediated-stdio post-MVP wedge is now Claude-only by
  construction (ADR-012 D7).
- **Me filter chip "Approvals" → "Requests"** since the bucket
  spans approval_request, select, help_request, template_proposal —
  none of which are pure approve/deny.

### Fixed
- **Resume preserves transcript.** Stopping an active session and
  resuming it minted a new agent and the chat opened empty — the
  list/SSE endpoints AND'd `agent_id = ?` even when `session=<id>`
  was provided. Now session=<id> scopes by session_id (with team
  auth), orders by ts, and the mobile feed dedupes by event id +
  paginates with a new `before_ts` cursor since per-agent seq is
  unusable as a cross-agent total order.
- **Stream-dropped banner on idle close cycles.** SSE onDone with
  no error is an idle artifact (proxy keepalive, mobile carrier),
  not a real drop. Banner now fires only on onError.
- **Rate-limit countdown rendering "1540333567h"** when Anthropic
  shipped resetsAt as a microsecond-precision integer. Unit
  heuristic now handles seconds / ms / µs / ns plus a 7-day
  sanity bound so any future unit confusion drops the tile.

## v1.0.338-alpha — 2026-04-29

### Changed
- request_approval / request_select / request_help converted from
  long-poll to turn-based delivery. The MCP call now returns
  immediately with `{id, status: "awaiting_response"}`; the agent
  ends its turn per the updated tool description. The principal's
  reply lands as a fresh user turn (`input.attention_reply` agent
  event, `producer="user"`) when /decide resolves the attention.
  Removes the 10-minute timeout, the connection-pinned wait, and
  the failure mode where a reply 12 minutes after the question was
  silently dropped. Persistence moves from the open HTTP connection
  to the conversation history — a 3-day-later reply still wakes
  the agent. permission_prompt is unchanged: it stays sync because
  Claude's canUseTool protocol has no "deferred" branch (vendor
  contract limitation, not a design choice).
- handleDecideAttention fans out the resolution to the originating
  agent via a new `dispatchAttentionReply` helper. Target lookup is
  attention.session_id → sessions.current_agent_id; if the session
  was resumed since the request was raised, the new agent (which
  inherits the conversation context) receives the reply. Best-
  effort: a fan-out hiccup doesn't roll back the /decide.
- StdioDriver gains a new input kind `attention_reply` that produces
  a user-text turn (NOT a tool_result, since the original tool call
  has already returned). Format per attention kind:
    approval → "Approved" / "Rejected. Reason: <reason>"
    select   → "Selected: <option>"
    help     → "<body>" verbatim or "Dismissed without reply"
  Short correlation prefix `[reply to <kind> <id-prefix>]` so the
  agent can match replies to multiple in-flight requests.
- `agent_input` HTTP handler accepts the new `attention_reply` kind
  for completeness (so an operator can wake an agent from CLI in a
  pinch); server-side fan-out from /decide is the primary producer.

### Removed
- `requestSelectTimeout` and `requestHelpTimeout` constants (10
  minutes each). No replacement — turn-based delivery has no time
  bound.
- The long-poll branches and timeout-handling code in mcpRequestSelect
  and mcpRequestHelp.

### Tests
- TestRequestHelp_ReturnsAwaitingResponseImmediately: pins the
  synchronous return contract (1s upper bound, fail-fast on a long-
  poll regression).
- TestDecide_HelpRequestFansOutAttentionReply: end-to-end — agent
  asks → user decides → input.attention_reply event posted to the
  agent with the principal's body verbatim.
- TestMCP_RequestSelect_TurnBasedRoundTrip: replaces the prior
  `_StoresOptionsAndLongPolls` test; covers the new return shape +
  decide behavior.
- TestStdioDriver_InputFrames: 3 new subtests for attention_reply
  formatting (help_request approve, select approve, approval_request
  reject).

### Docs
- docs/reference/attention-kinds.md §5 rewritten as
  "Resolution semantics — turn-based delivery" with a worked round-
  trip diagram, per-kind /decide payloads, per-kind user-turn text
  format, and a "Why turn-based, not long-poll" rationale section.
  permission_prompt called out as the principled exception.

## v1.0.337-alpha — 2026-04-29

### Added
- "Open project" button on the approval-detail Origin section, next to
  "Open in chat". Visible when the attention has a project pointer
  (project_id column or scope_kind='project' + scope_id). Routes to
  ProjectDetailScreen using the cached project row from hub state.
- Scroll-to-event-id on session chat: SessionChatScreen + AgentFeed
  gain an `initialSeq` parameter. After the cold-open backfill, the
  feed scrolls to and briefly highlights (2px primary-tinted border,
  ~1.2s) the event whose seq matches. Used by approval-detail's
  "Open in chat" button so the principal lands at the agent's turn
  that raised the request, not at the generic tail.
  Implementation: GlobalKey on the matched AgentEventCard +
  Scrollable.ensureVisible — works with non-uniform row heights
  without a positioned-list dependency. Falls back to tail scroll
  when the seq isn't in the loaded page (older than 200 newest).
  Auto tail-follow disables on a successful jump so subsequent SSE
  events don't yank the user back to the bottom mid-read.
- Host info on host detail: OS, arch, kernel, CPU count, total
  memory, hostname now render as named rows on the host detail
  sheet (Hosts tab → tap host). Sourced from a new
  `capabilities.host` field on the host-runner capabilities sweep.
  Host-runner probes once at startup (ProbeHostInfo) and re-attaches
  the cached pointer to every push so a hub mobile session always
  sees the static facts even if the runner restarted in the middle.
  Linux reads /proc/meminfo MemTotal; Darwin reads `sysctl hw.memsize`;
  kernel via `uname -r` on both. Memory rendered in GiB
  (10 GiB → "10 GiB", 0.5 GiB → "512 MiB"). Replaces the previous
  raw-JSON dump that wasn't readable in practice.
- Capabilities row on host detail rewritten as "Engines" with
  installed family + version joined by `·` (e.g.
  "claude-code 1.0.27 · codex 0.5.1"). Missing engines hidden so
  the sheet doesn't list every supported engine just to say "no".
- Tests: TestProbeHostInfo_PopulatesStaticFields pins OS/arch/CPU
  population and asserts memory is non-zero on Linux/Darwin where
  the probe path is reachable.

### Changed
- HostInfo struct embedded in Capabilities is JSON-optional
  (`omitempty`) for back-compat — old runners (pre-v1.0.337) emit no
  host field and the renderer hides those rows rather than showing
  unknowns.

## v1.0.336-alpha — 2026-04-29

### Added
- Approval detail screen now renders origin context: agent + session
  pointers ("Open in chat" jumps directly to the originating session's
  transcript), the last 10 transcript turns leading up to the request
  (filtered by session_id, capped by attention.created_at), and
  inline action controls that mirror the Me-page card. Resolving from
  the detail screen pops back to the Me page since the row drops off
  the open list.
- Server: request_approval / request_select / request_help all stamp
  attention_items.session_id at insert time via new
  Server.lookupAgentSession helper. Empty for system-originated
  attentions (budget, spawn approval) and pre-v1.0.336 rows; the
  detail screen degrades gracefully to a metadata-only view.
- New endpoint: GET /v1/teams/{team}/attention/{id}/context returns
  {session_id, agent_id, agent_handle, events: [...]} with newest-
  first transcript turns. Two tests pin the contract — full round
  trip from request_help and the no-session-pointer fallback.
- attentionOut now carries session_id; the list endpoint exposes it
  to mobile so the Me-page card can pre-decide whether the detail
  screen will have anything to render.

### Changed
- Inline action widgets (InlineApprovalActions, InlineHelpRequestActions)
  extracted from me_screen.dart to lib/screens/me/inline_actions.dart
  so the approval detail screen can reuse them without a circular
  import. Both gain an optional onResolved callback so the detail
  screen can pop after a successful decide; the Me-page card leaves
  it null and lets the row drop out of the open list on its own.
- approval_detail_screen.dart rewritten as a ConsumerStatefulWidget
  that fetches context on mount; the apologetic "actions will land
  here in a follow-up" footer is gone — actions are inline.

## v1.0.335-alpha — 2026-04-29

### Added
- New `help_request` attention kind — the third interaction shape,
  complementing `approval_request` (binary) and `select` (n-ary).
  Used when the agent needs free-text input from the principal:
  clarification, direction, opinion, or hand-back ("I'm stuck, take
  over"). MCP tool `request_help` parallels `request_approval` and
  `request_select`; payload carries `question`, optional `context`
  (agent's framing), and `mode` (`clarify` | `handoff`). The decide
  endpoint now accepts a `body` field; an approve on a help_request
  without a body is rejected (400) since the principal's reply *is*
  the answer. Long-poll surfaces the body to the agent verbatim,
  same shape as `request_select`'s option_id flow.
- `docs/reference/attention-kinds.md` — canonical authoring guide
  for picking between the three kinds. Decision tree by
  answer-space cardinality, anti-pattern table with what to use
  instead, worked examples for clarify and handoff modes. The MCP
  tool docstring on `request_help` carries the short form;
  contributors and AI agent maintainers consult this doc for the
  long form. Linked from `hub-agents.md`.
- Mobile `_HelpRequestActions` widget on the Me page renders a
  free-text composer (Send / Skip) when a help_request attention
  appears in the approvals list. Mode chip ("clarify" / "hand-back")
  surfaces the agent's framing; agent's `context` shows above the
  composer. The approval-detail screen footer copy is now
  kind-aware so it doesn't mislead help_request users with
  "Approve / Deny" instructions.

### Changed
- `request_select` is now explicitly tracked in `tiers.go` as
  `TierRoutine` (was relying on the `request_decision` alias entry).

## v1.0.334-alpha — 2026-04-29

### Fixed
- Steward auth tokens now revoke when the agent terminates. Each
  spawn mints a `kind='agent'` row in `auth_tokens` (the bearer the
  agent uses for `/mcp/{token}`); previously no path revoked it, so
  every spawn → terminate cycle left a still-valid token row, and
  pause/resume compounded it (one resume = one fresh token + one
  orphaned-but-live token). New `auth.RevokeAgentTokens(ctx, exec,
  agentID, now)` helper accepts either `*sql.DB` or `*sql.Tx`; called
  from `handlePatchAgent` when status flips to terminated/failed/
  crashed (covers UI terminate, host-runner ack, and the
  `shutdown_self` MCP path which lands here via host-runner) and
  from `handleSpawn`'s session-swap branch in the same tx so a
  rolled-back swap also rolls back the revoke. Idempotent on the
  `revoked_at IS NULL` clause.
- Mobile Auth screen (`tokens_screen.dart`) hides agent-kind rows.
  They're machine-issued + machine-revoked; surfacing them invited
  the operator to revoke a live agent's bearer (which would just
  look like a crash). The "New token" dialog also drops the `agent`
  kind chip — there's no human-issuance flow for agent tokens.

## v1.0.333-alpha — 2026-04-29

### Added
- ADR-010 Phase 1.6: `frame_translator` flag wired end-to-end. New
  `Family.FrameTranslator` field in `agent_families.yaml` selects
  the per-engine translator: `""` / `"legacy"` (default; today's
  hardcoded `legacyTranslate`), `"profile"` (data-driven
  `ApplyProfile` authoritative, legacy not invoked), `"both"`
  (profile authoritative + legacy in shadow with divergence logged
  via slog). Schema sidecar carries the enum so editor LSPs catch
  typos.
- Driver dispatch refactor: `StdioDriver.translate()` is now a
  3-way switch on `FrameTranslator`; the existing translator body
  moved verbatim into `legacyTranslate` and is reachable from both
  the default path and the "both" shadow run. `launch_m2.go`
  populates `FrameTranslator` + `FrameProfile` from the family
  registry at driver construction.
- `profile_diff.go`: extracted `DiffEvents` + `ParityIgnoreFields`
  + `capturingPoster` from the parity test into shared production
  code so the runtime "both"-mode divergence logging and the test
  parity diff use the same machinery and respect the same known-gap
  list. Misconfig (FrameTranslator set, FrameProfile nil) falls
  through to legacy with a warning rather than silently dropping
  events.
- 5 mode-dispatch tests: legacy default, profile-only, both with
  parity-clean frame (no warning), both with synthetic mismatched
  profile (warning fires with diff details), profile-mode misconfig
  fallback.

### Status
- ADR-010 Phase 1 is complete. The data-driven translator is
  shipped, parity-tested, flag-controllable, and dark by default.
  Phase 2 (canary → flip default → delete legacy) starts when the
  operator flips claude-code's `frame_translator: both` in their
  hub deploy and runs for a release window without divergence
  warnings.

## v1.0.332-alpha — 2026-04-29

### Added
- ADR-010 Phase 1.5: parity-test harness + seed corpus.
  `profile_parity_test.go` runs every frame in
  `testdata/profiles/claude-code/corpus.jsonl` through both
  translators (the legacy hardcoded `translate()` and the new
  data-driven `ApplyProfile`) and diffs the resulting agent_events
  by `(kind, producer, payload)`. Diff output is rule-level and
  agent-readable: which frame, which event index, which payload
  field, and what the legacy/profile values were. 13-frame seed
  corpus exercises every translate() branch (system.init / 3
  rate_limit shapes / task subtypes / assistant text+tool / user
  tool_result / result / error / unknown raw fallback).
- Grammar extension: `payload_expr: <expr>` for whole-payload
  passthrough. Used when the legacy translator emits the raw frame
  as payload (system fallback, error, deprecated completion alias)
  — three rules in the claude-code profile now use it. Mutually
  exclusive with `payload`; documented in
  `docs/reference/frame-profiles.md` §4 and the JSON Schema sidecar.
- `HUB_STREAM_DEBUG_DIR` env var: when set, the StdioDriver tees
  every raw stream-json line to `<dir>/<agent_id>.jsonl`. Operators
  use this to grow the corpus from real claude-code traffic — run
  the agent, copy interesting frames into the testdata directory,
  re-run the parity test.

### Changed
- Two known-gap fields documented as deliberate parity skips
  rather than profile bugs:
    - `by_model` — legacy normalizeTurnResult renames inner
      camelCase keys (inputTokens → input, etc.); v1 grammar has
      no map-iter construct.
    - `overage_disabled` — legacy derives a bool from
      `reason != nil`; v1 grammar has no bool-from-nullable
      predicate. Mobile reads `reason` directly.
  Adding to `parityIgnoreFields` is a deliberate policy decision;
  reviewers should read the comment before extending.

### Status
- ADR-010 Phase 1 is feature-complete (1.1 schema, 1.2 evaluator,
  1.3 translator, 1.4 profile + agent-readability artifacts, 1.5
  parity harness). Phase 1.6 (frame_translator flag) and Phase 2
  (canary → flip default) remain. Profile-driven translation is
  still dark — the legacy translator owns production traffic until
  the flag wires up.

## v1.0.331-alpha — 2026-04-29

### Removed
- `aider` retired from supported engines. Project decision: only
  cover dominant-vendor products (Anthropic claude-code, OpenAI
  codex, Google gemini-cli). Aider is a small open-source project
  that doesn't justify the per-engine maintenance cost. Touched:
  `agent_families.yaml` (entry deleted), `modes/resolver.go`
  (AgentKind comment), `lib/screens/team/agent_families_screen.dart`
  (defaults list), `families_test.go` /
  `spawn_mode_test.go` / `resolver_test.go` (test inputs swapped to
  `codex` where the test exercised cross-engine resolver behavior),
  `driver_stdio.go` comment, plus docs (discussion, plan, reference,
  hub-agents.md, steward-ux-fixes.md). ADR-010 §Context kept its
  decision-time mention of aider per ADR-immutability convention.

## v1.0.330-alpha — 2026-04-29

### Added (still dark — profile authored but legacy translator owns traffic)
- `hub/internal/agentfamilies/agent_families.yaml`: canonical
  claude-code `frame_profile` block. ~10 rules covering session.init
  (with camelCase/snake_case coalesce), all three rate_limit_event
  shape variants (flat / system-subtype / nested rate_limit_info),
  the system fallback, assistant multi-emit (content blocks +
  when_present-gated usage), user.tool_result filter, result →
  turn.result + completion (deprecated alias), and error. Each rule
  carries an inline `# ` comment naming the SDK release it was
  authored for so AI maintainers extending later have the
  upstream-shape lineage.
- `docs/reference/frame-profiles.md`: the agent-facing authoring
  reference. Grammar in BNF, dispatch semantics, scope rules, three
  worked input→output examples (rate_limit shape collapse, assistant
  multi-emit, system subtype hierarchy), common pitfalls calling out
  divergences from JSONata-style expectations. ~250 lines.
- `hub/internal/agentfamilies/agent_families.schema.json`: JSON
  Schema sidecar so editor LSPs (and AI editors) get autocomplete +
  inline validation while authoring overlays. yaml-language-server
  comment in the YAML wires it up automatically.
- `FrameProfile.Description` field — agent-facing prose header that
  states dispatch semantics + scope conventions inline so a fresh
  maintainer reading rule 17 sees the model without grep'ing the
  implementation.
- 7 smoke tests against the embedded profile covering every rule
  surface; full corpus diff test arrives in Phase 1.5.

### Changed
- `docs/plans/frame-profiles-migration.md` Phase 1.4 expanded with
  the five agent-native deliverables (description / reference /
  schema / inline comments / validator). New project memory entry
  `feedback_agent_native_design.md` captures "agent-native is a
  design principle" as a durable lesson — applies beyond frame
  profiles to any future declarative surface (action bar profiles,
  templates, attention-item options).

### Known parity gap
- `result.modelUsage` inner-key renaming (camelCase → snake_case in
  the `by_model` payload). The v1 grammar has no map-iter construct;
  by_model passes through verbatim. Tracked for grammar extension in
  Phase 1.5 once the parity diff surfaces the real shape.

## v1.0.329-alpha — 2026-04-29

### Added (dark code — not yet wired into live driver)
- `hub/internal/agentfamilies`: extended `Family` struct with optional
  `FrameProfile` (ADR-010 schema). New types `FrameProfile`, `Rule`,
  `Emit`. YAML round-trip test locks the wire shape so a rename
  surfaces immediately. Embedded families ship without profiles in
  v1; `FrameProfile == nil` is the steady state until Phase 1.4
  authors the claude-code profile.
- `hub/internal/hostrunner/profile_eval`: new package implementing
  the hand-rolled expression subset (D2 of ADR-010). Grammar:
  `$.path`, `$.path[N]`, `$$.outer.path`, `"literal"`, and
  `a || b || "default"` coalesce. ~150 LoC, zero third-party deps,
  full test coverage of nil propagation / outer scope / array
  indexing / malformed input.
- `hub/internal/hostrunner/profile_translate.go`: `ApplyProfile`
  evaluates a profile against a frame and returns the emitted events.
  Most-specific-match-wins dispatch: an init frame fires only the
  `{type: system, subtype: init}` rule, not the generic `{type:
  system}` fallback. Rules tied for specificity all fire (assistant's
  per-block + usage rules co-fire). When-present gates on a
  non-nil expression; gated rules suppress emit but don't trigger
  the raw fallback. No-match → `kind=raw` verbatim (D5).

This wedge is the load-bearing infrastructure for plan
`docs/plans/frame-profiles-migration.md` Phase 1. Phases 1.4–1.6
(claude-code profile + parity corpus + flag wiring) remain.

## v1.0.328-alpha — 2026-04-29

### Added
- `lib/widgets/agent_feed.dart`: inline answer card for the
  `AskUserQuestion` tool. claude-code emits a tool_call whose input
  carries `questions[].options[]`; the card renders the question +
  options as buttons and ships the picked label back as a
  `tool_result` so the agent can continue. Previously the prompt
  silently timed out, leaving a stale "looks like the question
  prompt was canceled" reply in the transcript.
- `hub/internal/server/handlers_agent_input.go` + `driver_stdio.go`:
  new `answer` input kind. Carved off `approval` because the agent
  expects a clean reply string, not a "decision: note" tuple — the
  driver wraps `body` in a `tool_result` keyed by `request_id` and
  ships it on stdin.

### Fixed
- `hub/internal/hostrunner/driver_stdio.go`:
  `translateRateLimit` now peeks into `rate_limit_info` (and
  `rateLimitInfo`) before reading status/window/resets-at fields.
  Recent claude-code SDK builds nest the actual rate-limit values
  under that sub-object; with the flat lookup the mobile telemetry
  strip stayed empty (window/status/resets-at all nil) every time
  the agent shouted about quota. Three shapes are now handled in
  one path: top-level fields (legacy), `system.subtype=rate_limit_event`
  (mid-versions), and the nested `rate_limit_info` (current).
  Regression test: `TestStdioDriver_RateLimitEventNestedInfo`.
- `lib/widgets/agent_feed.dart`: SSE re-subscribe no longer pops
  "Stream dropped" the moment a *clean* close happens. A clean close
  (`onDone`) after the agent finished a turn is normal — proxy idle
  timeout, mobile-network keepalive cycle, app suspend — and the
  reconnect either gets immediate replay or sits idle waiting on the
  next event. Banner now fires only on real `onError`, or after
  three consecutive empty close cycles, so a finished transcript
  doesn't surface a phantom error.

## v1.0.327-alpha — 2026-04-29

### Fixed
- `hub/migrations/0032_sessions_heal_orphan_active.up.sql`: one-shot
  migration that flips orphan-active sessions to `paused`. Bad data
  accumulated when an agent died via a code path that didn't auto-
  pause its sessions (the auto-pause was added in v1.0.326 but only
  fires through PATCH /agents/{id} status=terminated). Without this
  heal, the device-walkthrough showed sessions in the Detached group
  with a green "active" pill even though the agent was long gone.
  Regression test: `TestSessions_HealOrphanActive`.
- `lib/screens/sessions/sessions_screen.dart`: the Detached sessions
  group now treats every member as Previous and renders any
  `status=active|open` row as `paused` for display. Same rationale as
  the migration — the engine these rows pointed to is gone, so a
  green pill misleads the user. The bucket also auto-expands now
  (instead of starting collapsed) since Previous is the only content
  there. The chat AppBar's Stop action drops out when the attached
  agent isn't live in `hubProvider.agents`, mirroring the list-row
  defensive override.
- `lib/providers/sessions_provider.dart`: `resume()` and `fork()` now
  also call `hubProvider.refreshAll()` so a freshly-spawned steward
  shows up in the cached agents list immediately. Without this, the
  resumed/forked session got bucketed into the Detached group on the
  next render — its `current_agent_id` pointed at an agent the cache
  hadn't seen yet — until the user pulled-to-refresh.

### Changed
- `lib/screens/sessions/sessions_screen.dart`: per-row session menu
  now exposes a status-appropriate terminal action — Stop (active),
  Archive (paused). Previously the only way to kill a session was
  via the chat AppBar's Stop, which forced the user to enter the
  conversation first; archiving a paused session had no surface at
  all. Existing rename / fork-from-archive / delete entries are
  unchanged.
- `lib/screens/sessions/sessions_screen.dart`: Detached group is now
  default-expanded; previously the user had to tap "previous (N)"
  to see what was inside, which was confusing because for that
  group the previous list IS the entire group.

## v1.0.326-alpha — 2026-04-28

### Fixed
- `hub/internal/hostrunner/egress_proxy.go`: rewrite `req.Host` to
  upstream's host in the reverse-proxy Director. Without this, the
  agent's local `127.0.0.1:41825` Host header was forwarded upstream;
  Cloudflare-fronted hubs returned 403 because that hostname isn't a
  known CF zone. Regression test added.
- `hub/internal/hostrunner/driver_stdio.go`: also dispatch
  `type=system,subtype=rate_limit_event` to the rate-limit
  translator. Recent claude-code SDK versions wrap the signal under a
  `system` envelope; without the subtype branch the event was
  passed through as kind=`system` and the mobile telemetry strip
  never saw a `rate_limit` kind. Both shapes now feed the same
  helper.
- `lib/screens/projects/projects_screen.dart`: drop the
  Project/Workspace bottom-sheet picker that fronted the create FAB.
  The kind toggle inside `ProjectCreateSheet` already covers the
  same choice via a SegmentedButton, so the pre-pick was a redundant
  extra tap.
- `lib/widgets/agent_feed.dart` `_systemBody`: render claude-code's
  `task_started` / `task_updated` / `task_notification` system
  subtypes as one-liners (e.g. `Task updated · is_backgrounded=true`)
  instead of dumping the full envelope JSON.
- `hub/internal/server/handlers_agents.go`: extend the auto-pause
  rule to `terminated`. Previously only `crashed` and `failed`
  flipped the matching active session to `paused`, so a user who
  tapped Stop session ended up with a dead agent but a session that
  still claimed to be active — the chat AppBar kept offering Stop
  and the sessions list kept the row in the active bucket. Per
  ADR-009 D6 / the documented Stop-session contract. Existing
  test renamed/extended to cover all three terminal statuses.

### Changed
- `lib/widgets/agent_feed.dart`: jump-to-tail pill is now always
  visible while the user is scrolled away from the bottom (not just
  when new events arrive) and surfaces the current scroll position
  as a percentage. Tool-call cards gained a fold chevron in the name
  row that collapses the body to just the name + status pill, so
  noisy multi-step calls don't dominate the transcript.
- `hub/internal/server/handlers_sessions.go` `handleForkSession`:
  fork no longer auto-attaches to the team's live steward. A
  running steward agent is bound to its own active session via a
  single stream-json connection; pointing a second active session
  at it would race events between the two and silently strand the
  older conversation mid-turn. Fork now always lands the new
  session as `paused` with `current_agent_id` NULL by default, and
  the app drives a spawn (or replace-into-session) into it. An
  explicit `agent_id` parameter is still honoured for callers
  that genuinely have a session-less steward, but the server
  rejects (409) if that agent already owns an active session.
  Tests reworked: `TestSessions_ForkAlwaysUnattachedByDefault`
  asserts the no-auto-attach contract, and
  `TestSessions_ForkRejectsBusyAgent` covers the explicit-but-busy
  guard.
- `lib/screens/sessions/sessions_screen.dart` `_forkSession`:
  always opens the spawn-steward sheet bound to the new session id
  on a successful fork response with empty agent_id (now the
  default path), then navigates into the chat once the spawn
  lands. Replaces the prior misleading "no live steward to attach
  the fork to" error and the silent dual-attach race.
- `lib/screens/sessions/sessions_screen.dart`: the synthetic
  "(no live steward)" group on the Sessions page is renamed to
  "Detached sessions" with a sub-line explaining why the bucket
  exists ("Original steward gone — open to read, fork to continue
  with a fresh one").
- `lib/services/hub/open_steward_session.dart`: when a scope is
  passed but no scope-matching session exists for the live
  steward, open one in that scope instead of silently falling back
  to the steward's general/team session. Fixes the "tap project
  steward chip → land in team/general" routing surprise.
- `lib/screens/team/spawn_steward_sheet.dart`: cap sheet height at
  85% of the screen and wrap the content in a SingleChildScrollView
  so the Cancel/Start row stays reachable on short phones.
- `lib/screens/me/me_screen.dart`: replace the "My work" project
  strip with an "Active sessions" strip — sessions are what the
  principal is actively in the middle of, while the Projects tab
  already covers full project navigation. Each tile shows session
  title + scope (General / Project: <name> / Approving) + steward
  name; tap pushes `SessionChatScreen`. Strip is hidden when no
  active sessions exist. New `meActiveSessionsSection` arb key
  (en + zh); legacy `meMyWorkSection` key removed since nothing
  else referenced it.
- `lib/screens/team/spawn_steward_sheet.dart` + rename dialog in
  `sessions_screen.dart`: relabel the field as **Name** and accept
  the bare domain (`research`, `infra-east`); the app appends the
  `-steward` suffix internally via `normalizeStewardHandle` before
  submitting. The user no longer has to know about the suffix
  convention. Helper text now spells out the uniqueness scope —
  unique among **live stewards on this team**; stopping a steward
  frees the name for reuse. Stale description text dropped its
  `#hub-meta` reference and the "one agent" framing now that
  multi-steward is shipped.

## v1.0.316-alpha — 2026-04-28

### Added
- `scripts/lint-docs.sh` — enforces doc-spec status block,
  resolved-discussion forward links, cross-reference resolution, and
  stale-doc warning (Layer 1 of the anti-drift design).
- `.github/workflows/codeql.yml` — security/quality scanning on push
  and weekly cron.
- `.github/dependabot.yml` — weekly dep-update PRs for Flutter pub +
  Go modules + GitHub Actions.
- `.github/pull_request_template.md` — PR checklist mirroring
  doc-spec §7.
- `docs/changelog.md` (this file) — Keep-a-Changelog format.

### Changed
- `doc-spec.md` §7: documents the three CI rules and DISCUSSION
  resolution accepting both ADR and plan links.

## v1.0.315-alpha — 2026-04-28

### Changed
- `spine/sessions.md`: 14 "Tentative:" markers walked individually,
  marked Resolved (with version where known) or Open. Reading note
  added.
- `spine/blueprint.md` §9: per-bullet status indicators (✅/🟡) +
  ADR cross-links.
- `spine/information-architecture.md` §11: 7 wedges marked ✅ shipped
  with version range; final paragraph rewritten as archaeology.

## v1.0.314-alpha — 2026-04-28

### Changed
- `reference/coding-conventions.md`: rewritten first-principles —
  links to upstream (Effective Dart, `analysis_options.yaml`) instead
  of duplicating; project-specific deltas only; each rule justified
  by the bug it prevents.

### Fixed
- Memory body drift: `user_physercoe.md` (fork name + retired dev
  machine), `project_research_demo_focus.md` (P4 status),
  `project_steward_workband.md` (sequence completed).

## v1.0.313-alpha — 2026-04-28

### Added
- Status blocks on every remaining doc (21 files). Every doc in
  `docs/` now declares Type / Status / Audience / Last-verified at
  the top.
- `reference/ui-guidelines.md` rewritten for Flutter (was
  pre-rebrand React Native).

### Changed
- H1s renamed to match filenames where they had drifted
  (`Wedge memo: Transcript / approvals / quick-actions UX` →
  `Transcript / approvals / quick-actions UX — competitive scan`,
  etc.).

## v1.0.312-alpha — 2026-04-28

### Added
- `reference/coding-conventions.md` rewritten for Flutter/Dart + Go
  (was pre-rebrand React Native).

### Changed
- 4 spine docs gain formal status blocks.
- 3 resolved discussions linked to their ADRs.

## v1.0.311-alpha — 2026-04-27

### Added
- 8 retroactive ADRs in `docs/decisions/` covering shipped decisions:
  Candidate-A lock, MCP consolidation, A2A relay, single-steward MVP,
  owner-authority model, cache-first cold start, MCP-vs-A2A protocol
  roles, orchestrator-worker slice.
- `decisions/README.md` indexes them.

## v1.0.310-alpha — 2026-04-27

### Changed
- 26 doc files reorganized into 7-primitive layout: spine/,
  reference/, how-to/, decisions/, plans/, discussions/, tutorials/,
  archive/.
- Renames per naming spec: `ia-redesign.md` →
  `information-architecture.md`, `agent-harness.md` →
  `agent-lifecycle.md`, `steward-sessions.md` → `sessions.md`,
  `vocab-audit.md` → `vocabulary.md`, `hub-host-setup.md` →
  `install-host-runner.md`, `hub-mobile-test.md` →
  `install-hub-server.md`, `release-test-plan.md` →
  `release-testing.md`, `mock-demo-walkthrough.md` →
  `run-the-demo.md`, `monolith-refactor-plan.md` →
  `monolith-refactor.md`, `wedges/` → `plans/`.
- `spine/sessions.md` promoted out of DRAFT.

## v1.0.309-alpha — 2026-04-27

### Added
- `docs/README.md` — navigation index.
- `docs/roadmap.md` — vision + phases + Now/Next/Later.
- `docs/doc-spec.md` — contract every doc honors (7 primitives,
  status block spec, naming spec, lifecycle rules).

## v1.0.308-alpha — 2026-04-27

### Changed
- Steward composer: cancel button surfaces whenever agent is busy
  (regardless of field content). Tooltip varies by content.

## v1.0.307-alpha — 2026-04-27

### Changed
- Steward composer: cancel only on text+busy (predictive-input flow).
  `isAgentBusy` plumbed from `AgentFeed` via event-stream scan.

## v1.0.306-alpha — 2026-04-27

### Changed
- Steward composer: collapsed cancel onto send slot via text-empty
  heuristic; bolt long-press = save-as-snippet (mirrors action-bar
  pattern).

## v1.0.305-alpha — 2026-04-27

### Added
- Read-through caches for `getAgent`, `getRun`, `getPlan` +
  `listPlanSteps`, `getReview`, `listAgentFamilies` — every detail
  screen serves last-known data from cache.

## v1.0.304-alpha — 2026-04-27

### Added
- Cache-first cold start: `_loadConfig` reads SQLite snapshots
  synchronously into `HubState`; UI lights up before network refresh
  resolves. Pairs with v1.0.303's `refreshAll` schedule. (ADR-006)

## v1.0.303-alpha — 2026-04-27

### Fixed
- Empty Projects/Me/Hosts/Agents on cold start: `HubNotifier.build()`
  now schedules `Future.microtask(refreshAll)` whenever
  `_loadConfig()` returns a configured state.

## v1.0.302-alpha — 2026-04-27

### Changed
- Documentation pass: agent-protocol-roles.md, hub-agents.md,
  research-demo-gaps.md, steward-ux-fixes.md updated to reflect
  v1.0.298 MCP consolidation + W-UI completion.

## v1.0.301-alpha — 2026-04-27

### Fixed
- Drop unused `_statusColor` (CI lint, was unreferenced after v1.0.299
  refactor).

## v1.0.300-alpha — 2026-04-27

### Changed
- Steward composer matched to action-bar composer: fontSize 14,
  maxHeight 120 (unbounded lines), inline clear button, save-as-snippet
  button.

## v1.0.299-alpha — 2026-04-27

### Added
- Steward chat polish: syntax-highlighted code blocks via
  `flutter_highlight`, color-coded diff view with line gutter,
  per-tool icons on `tool_call` cards.

## v1.0.298-alpha — 2026-04-27

### Changed
- Single MCP service: `mcp_authority.go` reuses the hubmcpserver
  catalog in-process via chi-router transport. One `hub-mcp-bridge`
  symlink, one `.mcp.json` entry. (ADR-002)

## v1.0.297-alpha — 2026-04-27

### Changed
- *(Superseded by v1.0.298.)* Wired `hub-mcp-server` into spawn
  `.mcp.json` via host-runner multicall pattern.

## v1.0.296-alpha — 2026-04-27

### Added
- SOTA orchestrator-worker slice: `agents.fanout`, `agents.gather`,
  `reports.post` MCP tools + steward template recipe + worker_report
  v1 schema. (ADR-008)
- Mobile: per-host agents view.

## v1.0.295-alpha — 2026-04-26

### Changed
- Renamed `request_decision` → `request_select` MCP tool with
  back-compat alias. Start-session path for orphaned stewards.

## v1.0.294-alpha — 2026-04-26

### Changed
- Hide MCP gate `tool_call` cards in transcript; remove standalone
  Close-session action (close = terminate).

## v1.0.293-alpha — 2026-04-26

### Added
- Cache sessions list + channel events for offline.

## v1.0.292-alpha — 2026-04-26

### Fixed
- Cache `recentAuditProvider` for offline activity feed.

## v1.0.291-alpha — 2026-04-26

### Added
- Multi-steward wedges 2+3: hosts sort + agent rename.

## v1.0.290-alpha — 2026-04-26

### Added
- Multi-steward wedge 1: handle-suffix convention (`*-steward`),
  auto-open-session on spawn, domain steward templates
  (`steward.research`, `steward.infra`).

## v1.0.286-alpha — 2026-04-26

### Added
- Egress proxy in host-runner: in-process reverse proxy masks the
  hub URL from spawned agents (`.mcp.json` carries
  `127.0.0.1:41825/`, not the public hub).

## v1.0.285-alpha — 2026-04-26

### Added
- Tail-first paginated transcripts.
- Hub backup/restore via `hub-server backup` / `hub-server restore`.

## v1.0.281-alpha — 2026-04-26

### Changed
- Replace-steward keeps the session: engine swap continues the
  conversation. Sessions are durable across respawn.

## v1.0.280-alpha — 2026-04-26

### Added
- Soft-delete sessions + UI; documented agent-identity binding.

---

## Earlier history

Major work units shipped before v1.0.280, summarized:

- **v1.0.200–203** — Artifacts primitive (§6.6 end-to-end). Outputs
  is the 4th axis (Files/Outputs/Documents/Assets).
- **v1.0.208** — Offline snapshot cache: HubSnapshotCache +
  read-through + mutation invalidation + Settings clear (5 wedges).
- **v1.0.175–182** — IA redesign: 7 wedges (nav skeleton, host
  unification, Me tab, Projects tab, Activity tab, Team switcher,
  Steward surface).
- **v1.0.166–167** — Activity feed foundation: audit_events as the
  activity log; mutations call recordAudit; MCP `get_audit` exposes it.
- **v1.0.157** — A2A relay + tunnel for NAT'd GPU hosts.
- **v1.0.151–156** — MCP tool surface expansion to close P4.4 audit:
  `schedules.*`, `tasks.*`, `channels.create`, `projects.update`,
  `hosts.update_ssh_hint`.
- **v1.0.141–148** — Trackio metric digest (storage + poller +
  mobile sparkline).
- **v1.0.49** — Audit log: `audit_events` table + REST + mobile screen.
- **v1.0.27** — Rebrand from MuxPod to termipod.
- **v1.0.18** — File manager (Settings > Browse Files).
- **v1.0.17** — Compose drafts (Save as Snippet → drafts category).
- **v1.0.2** — Data Export/Import via DataPortService.

For any version not listed above, `git log v1.0.X-alpha` and
`git show v1.0.X-alpha` (tag annotation) are authoritative.

---

## Conventions

- **One section per tagged release**, newest first.
- **Categories** (Keep a Changelog): Added · Changed · Fixed ·
  Deprecated · Removed · Security. Omit unused categories.
- **Cross-references**: link to ADRs (`ADR-NNN` or
  `decisions/NNN-name.md`) when a change implements a decision.
- **Patch-level entries**: bug-fix-cadence releases roll up; the
  changelog records substantive changes, not every tag.
- **Append at top**: new entries go above `## v1.0.316-alpha`.
- **Don't rewrite history**: changelog is append-only (modulo typo
  fixes). Past entries are the historical record.
