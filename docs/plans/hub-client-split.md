---
name: Hub client split
description: Executable wedge-by-wedge plan to split lib/services/hub/hub_client.dart (3,571 LOC, one HubClient God-class with 208 Future/Stream methods) into a transport core plus per-domain sub-clients. Transport-DI (a public HubTransport injected into each sub-client), not part/part-of. HubClient stays a thin facade — delegators + sub-client getters — so call sites never change. Realises R1 of docs/discussions/monolith-refactor.md.
---

# Hub client split — phased

> **Type:** plan
> **Status:** In progress — W1–W12 shipped (v1.0.736–747; …, DocumentsApi, DeliverablesApi); remaining: PlansApi, ProjectsApi, TasksApi, TemplatesApi + AgentFamiliesApi. NOTE: the real decomposition needs more sub-clients than the original 11-row map (Admin, Hosts, Tasks, Templates, AgentFamilies split out); the wedge table is being relabelled as each lands.
> **Audience:** contributors
> **Last verified vs code:** v1.0.747

**TL;DR.** `lib/services/hub/hub_client.dart` is 3,571 LOC — one
`HubClient` class with **208 `Future`/`Stream` methods** grouped by
domain (sessions, agents, projects, tasks, runs, documents, hosts,
attention, blobs, search, events). It nearly doubled (1,902 → 3,571)
since R1 was first sketched and never ran. This plan splits it **by
domain**, injecting a shared **`HubTransport`** into per-domain
sub-clients. `HubClient` is kept as a **thin facade** — short
delegators plus `sessions`/`agents`/… getters — so **no call site
changes** (the blast-radius killer). Value-neutral on behavior. One
green PR per wedge. Realises **R1** of
`docs/discussions/monolith-refactor.md`.

This plan is the Go-side analogue's sibling but Dart-only; the Go
handler/driver monoliths get their own plan later (see the discussion's
"Go-side companion" note).

---

## What the code actually looks like (verified at v1.0.735)

One library, one `HubConfig`, one `HubApiError`, and one **`HubClient`**
class. Two structural facts make this a clean split:

1. **Every method routes through a tiny private transport.** The class
   has ~140 LOC of plumbing — `_http`, `_uri`, `_open`, `_readJson`,
   `_get`/`_post`/`_patch`/`_put`/`_delete`, `_invalidate`,
   `_decodeListMaps`/`_decodeMap`, `_cacheHubKey` — and two optional
   caches (`snapshotCache`, `blobCache`) set by the provider *after*
   construction (`lib/providers/hub_provider.dart:226-230`). The other
   ~3,400 LOC are domain methods that do nothing but build a path,
   call a transport verb, and decode. SSE (`streamEvents`) and blobs
   (`uploadBlob`/`downloadBlob`) reach `_open`/`_readJson` directly for
   raw byte/stream handling.

2. **Call sites touch one handle.** The provider exposes
   `HubClient? get client => _client;`
   (`lib/providers/hub_provider.dart:300`). Screens call
   `provider.client.listSessions(...)` etc. Only **8 files** name
   `HubClient` at all; the rest hold a `client` reference. So as long
   as `HubClient` keeps the same public method surface, **nothing
   downstream changes** — the entire split is internal.

### The latent layering

```
Layer 0  HubTransport      cfg + _http + caches; get/post/patch/put/delete/open/readJson/invalidate/decode*
   ▲
Layer 1  *Api sub-clients  one per domain; each holds a HubTransport, owns its paths + decode
   ▲
Layer 2  HubClient facade  constructs the transport + sub-clients; delegators keep the legacy surface
```

Import-based, **not** `part`/`part of` (consistent with the agent_feed
split; keeps each sub-client independently testable and its imports
honest). `HubTransport`'s verbs are **public** so sub-clients in other
libraries can call them; only `_http` stays private inside it.

---

## Design decisions

- **D1 — Transport is a public class, injected.** `HubTransport(cfg)`
  owns `_http` + the two mutable caches and exposes public
  `get/post/patch/put/delete`, raw `open`/`readJson` (for SSE + blobs),
  `invalidate`, `decodeListMaps`/`decodeMap`, `uri`, `cacheHubKey`.
  Each sub-client takes a `HubTransport` in its constructor.

- **D2 — Facade keeps the surface; call sites never move.** `HubClient`
  constructs one `HubTransport` and one of each sub-client, exposes them
  as getters (`sessions`, `agents`, …), and keeps a one-line delegator
  for every existing public method (`listSessions(...) =>
  sessions.listSessions(...)`). The 208-method surface is preserved
  byte-for-byte at the call site. Migrating call sites to
  `client.sessions.list()` and deleting the delegators is an **optional
  later wave (W12+)**, explicitly out of scope here to keep each wedge
  zero-blast-radius.

- **D3 — Cache wiring stays where the provider expects it.** The
  provider sets `client.snapshotCache = …` / `client.blobCache = …`
  after construction. `HubClient` keeps those as forwarding setters
  onto `_t` (`set snapshotCache(v) => _t.snapshotCache = v`), so
  `hub_provider.dart` is **untouched**.

- **D4 — Behavior-neutral.** Pure rearrangement; no path, header,
  decode, or cache-invalidation semantics change. Diffs are
  cut-move-rewire.

- **D5 — Verification is CI-only.** No local Flutter SDK
  (`analyze`/`test` run only in CI). Each wedge is one PR; read the
  explicit `gh run view --json status,conclusion,jobs` conclusion, not
  a background watcher's exit code. Cross-library privacy traps from
  the agent_feed split apply (a moved method calling a now-private
  helper won't resolve) — grep each moved block for leading-`_`
  references it leaves behind before pushing.

---

## Domain map (banner → sub-client)

The in-file `// ---- … ----` banners already mark the domains. Sizes
are approximate at v1.0.735 and each wedge re-confirms its line range.

| Sub-client | Absorbs (banners) | ~LOC |
|---|---|---:|
| **HubTransport** | transport + cache plumbing (`HubClient` head) | 140 |
| **SystemApi** | info/probe · stats · insights · tokens · governance config | 280 |
| **HostsApi** | host lifecycle · host mutations · admin/ops fleet | 190 |
| **SessionsApi** | sessions | 460 |
| **AgentsApi** | collections(agents) · spawn · general steward · agent lifecycle · agent events | 640 |
| **ProjectsApi** | collections(projects/channels/principals) · project/task/channel writes · project docs | 620 |
| **RunsApi** | runs · schedules | 320 |
| **DocumentsApi** | documents+reviews · annotations · deliverables/components/overview · plans+plan_steps | 870 |
| **AttentionApi** | attention actions | 55 |
| **BlobsApi** | blobs | 77 |
| **SearchApi** | search | 74 |
| **EventsApi** | SSE event stream | 89 |

`getInfo`/`verifyAuth` are also used by the bootstrap screen via a
throwaway `HubClient` — they stay reachable on the facade (delegators),
so the probe path is unaffected.

---

## Wedge sequence

Smallest / most-isolated domains first to validate the transport-DI
seam cheaply before the big ones. One version bump per wedge.

| ID | Wedge | New file | Risk |
|---|---|---|---|
| ~~**W1**~~ ✅ | Extract `HubTransport`; HubClient holds `_t`, keeps private shims forwarding to it, forwards cache setters. Bodies untouched. **Shipped v1.0.736.** | `hub_transport.dart` | low (mechanical, internal) |
| ~~**W2**~~ ✅ | `SystemApi` (info/probe + stats + insights + tokens + governance). Also promoted `_listJson` → `HubTransport.listJson`. **Shipped v1.0.737.** | `system_api.dart` | low |
| ~~**W3**~~ ✅ | `BlobsApi` + `SearchApi` (raw `open`/`readJson` via transport). Dropped orphaned `dart:async`. **Shipped v1.0.738.** | `blobs_api.dart`, `search_api.dart` | low |
| ~~**W4**~~ ✅ | `EventsApi` (SSE; `_streamPath`/`_extractData` + all three stream methods incl. `streamAgentEvents` pulled from the agent-events section). **Shipped v1.0.739.** | `events_api.dart` | low-med (raw stream) |
| ~~**W5**~~ ✅ | `AttentionApi` (list+cached+actions, cherry-picked from 3 banner regions). **Shipped v1.0.740.** | `attention_api.dart` | low |
| ~~**W6**~~ ✅ | `AdminApi` — fleet admin/ops + policy.yaml editor (contiguous block, cleaved before Hosts). **Shipped v1.0.741.** | `admin_api.dart` | low |
| ~~**W7**~~ ✅ | `HostsApi` — host lists + lifecycle + mutations. **Shipped v1.0.742.** | `hosts_api.dart` | low |
| ~~**W8**~~ ✅ | `SessionsApi` (11 session methods). **Shipped v1.0.743.** | `sessions_api.dart` | med (size) |
| ~~**W10**~~ ✅ | `RunsApi` — schedules + runs (24 methods incl. 2 private translators); dropped orphaned `_put` shim. **Shipped v1.0.745.** | `runs_api.dart` | med |
| ~~**W9**~~ ✅ | `AgentsApi` — 22 methods (collections + spawn + steward + lifecycle + event queue). **Shipped v1.0.744.** | `agents_api.dart` | med (size) |
| **W10** | `ProjectsApi` (+ tasks, channels) | `projects_api.dart` | med (size) |
| ~~**W11**~~ ✅ | `DocumentsApi` — documents + annotations (13 methods + private cache helper); reviews/deliverables/plans split into later wedges. **Shipped v1.0.746.** | `documents_api.dart` | med |
| ~~**W12**~~ ✅ | `DeliverablesApi` — deliverables/criteria/overview/versions/artifacts/reviews (27 methods + 4 private helpers). **Shipped v1.0.747.** | `deliverables_api.dart` | med |
| W12+ | *(optional, deferred)* migrate call sites to `client.<domain>.x()`, drop delegators | — | high blast radius |

**Per-wedge recipe** (mirrors the agent_feed playbook):

1. Re-confirm the banner's line range (`grep -n '// ---- <domain>'`).
2. Create `<domain>_api.dart`: `class <Domain>Api { final HubTransport _t; <Domain>Api(this._t); … }`; move the bodies, rewriting `_get`→`_t.get`, `_invalidate`→`_t.invalidate`, `_decodeListMaps`→`_t.decodeListMaps`, etc.
3. In `HubClient`: add `late final <Domain>Api <domain> = <Domain>Api(_t);` and replace each moved method with a one-line delegator.
4. **Leak-scan the moved block** for leading-`_` refs that stayed behind in `HubClient` (use a reliable `grep -nE`, never a `for…done | sort` pipe — see the validate-negative-scan lesson) and for `@visibleForTesting`.
5. Audit `<domain>_api.dart` imports — drop any the moved bodies don't use; add only what they do.
6. `bash scripts/lint-docs.sh` gate (doc-freshness trips ~every 6 bumps), bump `pubspec.yaml`, changelog entry, commit, push.
7. Read the explicit `gh run view` conclusion for Analyze & Test.

---

## Outcome target

`hub_client.dart` 3,571 → **~450** (facade: `HubConfig`, `HubApiError`,
the sub-client getters, and 208 one-line delegators) + 11 cohesive
sub-client libraries + a 140-LOC `HubTransport`. If the optional W12+
call-site migration ever runs, the delegators dissolve and the facade
drops further — but that is a separate, higher-risk effort and not
promised here.

Honesty clause (per the agent_feed outcome): a facade of 208 trivial
delegators is still a few hundred LOC. That is acceptable — the value
is that every domain's *logic* now lives in a small, named, testable
unit, and the God-class is gone. Report the real final numbers in this
section when W11 lands; do not chase a smaller delegator count with
churn.
