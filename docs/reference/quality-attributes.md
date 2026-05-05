# Quality attributes

> **Type:** reference
> **Status:** Current (2026-05-05)
> **Audience:** contributors, reviewers
> **Last verified vs code:** v1.0.351

**TL;DR.** Quantified targets for the seven arc42 §10 quality
scenarios — performance, security, scalability, reliability,
maintainability, portability, and (lightly) usability/a11y. Where a
target is not yet measured, the cell reads `TBD post-measurement` with
a rationale line. Numbers are budgets, not aspirations: a regression
past the budget is a release blocker.

This doc is the *quantitative* side; the *structural* side is in
[`cross-cutting.md`](cross-cutting.md). Together they fill arc42 §8 +
§10.

---

## 1. Performance

| Scenario | Budget | Mechanism / where measured |
|---|---|---|
| Mobile cold-start to first paint | ≤ 200 ms with cache hit | [ADR-006](../decisions/006-cache-first-cold-start.md); `HubSnapshotCache.get` then render |
| Mobile cold-start to first paint, no cache | ≤ 1.5 s on Wi-Fi | First `/v1/_info` + `/projects` round-trip, then render |
| Mobile list re-render (100 rows) | ≤ 50 ms | Sliver-based incremental rendering; no full rebuild |
| SSE reconnect after network blip | < 1 s | `Last-Event-ID` + 30 s server buffer |
| Hub p50 list endpoint | < 100 ms | Indexes per [`database-schema.md §4`](database-schema.md) |
| Hub p99 list endpoint | < 500 ms | Same |
| Hub p50 single-row GET | < 50 ms | PK lookup on indexed table |
| Hub p99 spawn → host-runner pickup | < 5 s | 3 s host-runner poll + ≤ 2 s app-server start |
| Heartbeat → hub `last_seen_at` update | ≤ 10 s | Heartbeat interval (constant) |
| Run-metric digest size | ≤ 100 points per metric per push | host-runner downsampler |
| Audit-event SSE fan-out | ≤ 200 ms from `recordAudit()` to phone | `eventbus` synchronous publish |

**Cold-start under load.** Cache-first means the user sees the last-
known-good world before the network responds. The "no cache" path is
only the very first launch; subsequent launches always hit cache.

**Pagination defaults.** 50 rows / page; max 200. Cursor-based.

---

## 2. Security

| Scenario | Posture |
|---|---|
| Bearer token at rest, hub | SHA-256 hashed; plaintext never persisted |
| Bearer token at rest, mobile | OS keychain (`flutter_secure_storage`) |
| SSH credentials | Mobile-only; never uploaded to hub; encrypted in keychain |
| Self-signed TLS | Rejected by mobile (`dart:io.HttpClient`); use Let's Encrypt |
| Network boundary, mobile↔hub | TLS 1.2+; nginx reverse proxy |
| Network boundary, hub↔host-runner | Tailnet or LAN per [ADR-018](../decisions/018-tailnet-deployment-assumption.md) |
| Hub stores SSH credentials | **Never** ([forbidden #15](../spine/forbidden-patterns.md)) |
| Agent sandboxing | bwrap/seatbelt/microVM is **post-MVP**; host-runner inherits user permissions today |
| Vulnerability disclosure | GitHub Security Advisory ([SECURITY.md](../../SECURITY.md)) |

**Threat model.** Personal-tool frame ([discussions/positioning.md
§1.5](../discussions/positioning.md)). The principal and the
operator are usually the same human; multi-tenant SaaS hardening is
out of scope.

**Explicit non-goals (security):**
- Client-side end-to-end encryption (the hub *is* the trusted layer)
- Per-agent network isolation (post-MVP per memory `project_post_mvp_sandbox`)
- TLS-pinning the mobile app to a single hub cert
- Operator-action auditing on the hub host (the operator is the principal)

---

## 3. Scalability

| Scenario | Target |
|---|---|
| Projects per team | ≤ 100 (UI tested) |
| Active agents per team | ≤ 100 concurrent |
| Hosts per team | ≤ 20 |
| Audit events per team | ≤ 10⁷ retained; older rows compactable |
| Run metric series per project | ≤ 50 metrics × 1000 points each (digested) |
| SSE concurrent streams per hub | TBD post-measurement (Go HTTP/2 default, in the thousands per CPU) |
| Hub disk per team-year | TBD post-measurement; expected ~1 GB (events dominate) |

**Single-tenant assumption.** termipod is single-team-per-hub. Multi-
team aggregation is post-MVP.

**Scaling plan (post-MVP).** Sharding is by hub instance, not by
table. Federation (multiple hubs exchanging A2A tasks) is named in
[`../decisions/003-a2a-relay-required.md`](../decisions/003-a2a-relay-required.md)
as out-of-MVP.

---

## 4. Reliability

| Scenario | Posture |
|---|---|
| Hub crash recovery | `event_log/*.jsonl` is append-only; `hub-server reconstruct-db` rebuilds SQLite on corruption |
| Host-runner crash recovery | No persistent local state; restart with `--host-id` resumes; in-flight agents reattach to tmux panes |
| Mobile crash recovery | No state lost; SSE reconnects with `Last-Event-ID` |
| SSE server-side buffer | ≥ 30 s for `Last-Event-ID` resume |
| Idempotency-Key TTL | 24 h |
| Heartbeat → offline transition | ~60 s without heartbeat → `status='offline'` |
| Hub outage degradation | host-runner buffers writes, serves cached reads; mobile read-mostly per [`cross-cutting.md §5`](cross-cutting.md) |
| Mutation queueing on mobile | None (out of MVP); banner instructs retry |

**Backups.** Operator-side: `<dataRoot>/` is a tarball away from a
full backup. `hub/deploy/litestream/` is a future spot for
continuous replication (currently empty).

---

## 5. Maintainability

| Concern | How it's enforced |
|---|---|
| Code style (Dart) | `flutter analyze --no-fatal-infos`; CI gate |
| Code style (Go) | `go vet ./...`; `gofmt`; CI gate |
| Doc spec | [`../doc-spec.md`](../doc-spec.md); status block + naming + cross-refs; `lint-docs.sh` CI gate |
| Glossary discipline | [`glossary.md`](glossary.md); `lint-glossary.sh` CI gate |
| Test surface | [`../how-to/run-tests.md`](../how-to/run-tests.md); `flutter test` + `go test` |
| Coverage on changed files | ~70% target (mobile) |
| Migration policy | Forward-only by default; `down` migrations exist for dev rollback |
| ADR discipline | [`../decisions/`](../decisions/); Accepted ADRs are immutable, superseded via link |

**Doc → code drift.** PR template's "Doc / spec updates" checklist
+ `Last verified vs code:` lines + the stale-doc warn from
`lint-docs.sh` make drift visible. Drift past 5 minor versions emits
a warning (non-failing); past 10 should be flagged in code review.

---

## 6. Portability

| Surface | Supported | Status |
|---|---|---|
| Mobile platform: Android | Yes | Primary target |
| Mobile platform: iOS | Yes | Secondary target; sideload via AltStore/Sideloadly |
| Mobile platform: Web | No | N/A in MVP; not on roadmap |
| Mobile platform: Desktop (macOS / Linux / Windows) | No | Out of scope |
| Hub OS: Linux (Ubuntu 22.04 / 24.04) | Yes | Primary; systemd unit ships in `hub/deploy/systemd/` |
| Hub OS: macOS | Dev only | No production unit |
| Hub OS: Windows | No | Untested |
| Host-runner OS: Linux | Yes | Primary |
| Host-runner OS: macOS | Yes | Tested |
| Host-runner OS: Windows | No | tmux not available |
| Engine: Claude Code | Yes | Primary; M2 stream-json default for Pro/Max |
| Engine: Codex CLI | Yes | M1 ACP via `codex app-server` per [ADR-012](../decisions/012-codex-app-server-integration.md) |
| Engine: Gemini CLI | Yes | M1 exec-per-turn-with-resume per [ADR-013](../decisions/013-gemini-exec-per-turn.md) |

**i18n.** Mobile supports en + zh; ja deferred. Doc-side translation is
post-MVP.

---

## 7. Usability + accessibility

| Scenario | Target |
|---|---|
| Tap target size | ≥ 44 × 44 dp |
| Color-only signals | Forbidden — every signal pairs color with icon or label |
| Dynamic type | Settings exposes a font-size scale; all text surfaces respect it |
| Dark mode | Supported, toggled in Settings |
| VoiceOver / TalkBack | Standard widget semantics; bespoke widgets need explicit `Semantics` wrappers (open work) |
| Localisation | en + zh; ja deferred |
| Keyboard / external input | Action bar profiles; custom keyboard for terminal use |

A formal a11y audit is **post-MVP**. The targets above are the
patterns currently followed across the codebase.

---

## 8. How budgets become release blockers

A regression past one of these budgets is a release blocker. Process:

1. **Detect.** Performance regressions surface in CI (when test
   coverage exists) or during release-testing
   ([`../how-to/release-testing.md`](../how-to/release-testing.md)).
   Security regressions surface in PR review.
2. **Triage.** A budget breach files an issue tagged `regression`.
3. **Decide.** Either the budget bumps (with rationale) or the PR
   that caused the regression rolls back. The default is rollback.
4. **Document.** Budget changes amend this doc + the PR description.

This doc is **versioned** like every other ref — as the system
evolves, the budgets evolve with measurement, and the
`Last verified vs code` line updates with each amendment.

---

## 9. Open follow-ups (post-measurement)

Captured here so they don't lose context after the demo:

1. **Cold-start measurement** under controlled network conditions
   (LTE / 3G / Wi-Fi / offline).
2. **SSE connection ceiling** per hub — empirical test with N
   simulated phones.
3. **Hub disk usage per team-year** — derive from one month of
   real-world team data.
4. **Mobile coverage measurement** end-to-end (currently approximate).
5. **Formal a11y audit** with VoiceOver / TalkBack passes per major
   surface.
6. **Performance regression CI** — synthetic benchmarks gating PRs
   (currently absent).

---

## 10. Cross-references

- [`cross-cutting.md`](cross-cutting.md) — structural side of
  cross-cutting concerns
- [`architecture-overview.md`](architecture-overview.md) — C4 view
- [`api-overview.md`](api-overview.md) — endpoint conventions
- [`database-schema.md`](database-schema.md) — data layout
- [`audit-events.md`](audit-events.md) — observability surface
- [`rate-limiting.md`](rate-limiting.md) — quota enforcement
- [`../how-to/release-testing.md`](../how-to/release-testing.md) —
  release-time validation
- [`../decisions/006-cache-first-cold-start.md`](../decisions/006-cache-first-cold-start.md)
  — cold-start posture
- [`../decisions/018-tailnet-deployment-assumption.md`](../decisions/018-tailnet-deployment-assumption.md)
  — network assumption
- [`../../SECURITY.md`](../../SECURITY.md) — vulnerability disclosure
- arc42 §10 (Quality Requirements) — https://arc42.org/overview
