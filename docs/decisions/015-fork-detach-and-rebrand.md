# 015. Fork detach + rebrand: mux-pod → termipod

> **Type:** decision
> **Status:** Accepted (2026-04-14)
> **Audience:** contributors
> **Last verified vs code:** v1.0.350-alpha

**TL;DR.** On 2026-04-14 we detached the GitHub fork from upstream `mux-pod`, rebranded the app to **termipod**, changed `applicationId` from `si.mox.mux_pod` to `com.remoteagent.termipod`, switched the deep-link scheme to `termipod://` (with `muxpod://` retained as legacy secondary), and bumped to `1.0.0-alpha`. The change is intentionally breaking: existing mux-pod installs cannot upgrade in place. We accept that cost because the rebrand marks a product pivot — from "remote-tmux client" to a multi-agent coordination platform — and continuing under the upstream identity would mislead users and constrain naming choices for hub-side features.

---

## Context

The codebase originated as `physercoe/mux-pod`, a fork of an upstream Flutter SSH/tmux client. Through 2026-04 the project absorbed three structural changes:

1. **Hub coordination plane** — a Go daemon orchestrating multiple CLI agents across hosts (planning began the week of 2026-04-19). The mobile app stops being the system of record; it becomes one surface among many onto a hub-managed multi-agent platform.
2. **Multi-engine support** — claude-code, codex, gemini-cli, plus a frame-profile data model for adding more (ADRs 010, 012, 013, 014). The "tmux client" framing no longer covers the product.
3. **Personal-tool positioning** — the user explicitly framed the product as a single-director research-assistant tool, not a multi-tenant SaaS (2026-04-19, "i want to build it as personal tool"). This narrows the audience but widens the scope per user.

Three operational questions forced the decision:

- **Identifier collision.** Continuing under `si.mox.mux_pod` would tie the hub product to an unrelated upstream namespace; partner discoverability (search "termipod" → see the right thing) requires new IDs.
- **Naming entropy.** The tmux-client story leaks into every screen ("Hosts", "Sessions", "Panes" — borrowed from tmux). We needed permission to rename freely (Inbox→Me, Hub→Projects, etc.) without dragging the upstream branding behind us.
- **Discoverability.** README, App Store metadata, GitHub repo topics, deep-link documentation all key off the name. Half-renaming is worse than not renaming.

Backwards-compatible migration was considered and rejected. Android prefs and Keystore namespaces are scoped by `applicationId`; an in-place upgrade would either silently lose the user's data or require a complex cross-namespace migration. v0.x mux-pod has a small user base; the rebrand window costs less than carrying mux-pod compatibility forward indefinitely.

---

## Decision

**D1. New identifiers (effective commit `e0ecf94`, 2026-04-14):**

| Surface | Old | New |
|---|---|---|
| Android `applicationId` | `si.mox.mux_pod` | `com.remoteagent.termipod` |
| iOS `PRODUCT_BUNDLE_IDENTIFIER` | `si.mox.mux-pod` | `com.remoteagent.termipod` |
| pubspec `name` | `mux_pod` (Dart package) | `termipod` |
| Launcher label | MuxPod | TermiPod |
| Deep-link scheme (primary) | `muxpod://` | `termipod://` |
| Deep-link scheme (legacy) | — | `muxpod://` retained as secondary intent-filter |
| Version line | `0.x.x` | `1.0.0-alpha` (and forward) |

**D2. Fork detached on GitHub.** The repository network link to the upstream mux-pod fork was severed via the web UI (the GitHub API does not expose this operation). Forks of `physercoe/termipod` from this point forward inherit no upstream-mux-pod relationship.

**D3. Deep-link dual-scheme transition.** The Android `intent-filter` and iOS `CFBundleURLSchemes` accept both `termipod://` and `muxpod://`. The primary scheme is `termipod://`; `muxpod://` remains parseable so any third-party links published before 2026-04-14 still launch the app. Documentation cites only `termipod://`. Removal of `muxpod://` is post-MVP and out of scope here.

**D4. No in-place upgrade path.** Because `applicationId` changed, Android sees the new build as a different app. Users with the old MuxPod installed must either uninstall it or run both side-by-side. The README and release notes call this out as the migration story; no automated import is offered (export/import via `DataPortService` lands as a separate feature, v1.0.2 — see memory `project_todo_data_export.md`).

**D5. Repo identity.** The git remote moves to `physercoe/termipod`. The local working directory at `/home/ubuntu/mux-pod` is *not* renamed — paths in tooling, scripts, and CLAUDE.md continue to say `mux-pod` for stability. The directory name is internal; the published identity is termipod.

---

## Consequences

**Becomes possible:**
- Free naming inside the app — Hosts, Projects, Me, Channels — without inheriting tmux's vocabulary or upstream's framing.
- Hub-side features (multi-agent coordination, A2A relay, frame profiles) ship under one coherent product name.
- Marketing surfaces (App Store, README, repo topics, demo video) all key off `termipod`, which is unambiguously about *this* product.

**Becomes harder:**
- Existing mux-pod users cannot upgrade in place. The migration path is uninstall + reinstall + (optional) export/import via `DataPortService` (v1.0.2+).
- Any third party linking to `muxpod://...` URLs continues to work but reads the wrong brand on the launch screen until users update their links.
- Internal tooling that hardcodes the old applicationId or scheme breaks. CI workflows, signing configs, deep-link tests all needed updates as part of the rebrand commit.

**Becomes forbidden:**
- New code referencing `si.mox.mux_pod`, `mux_pod` (Dart package), or `MuxPod` as the brand label. The `flutter_muxpod` history exists for archaeology; new code uses the termipod identifiers.
- Cherry-picking from upstream mux-pod after the detach. The fork relation is severed; any borrowed work has to be re-applied as a fresh patch with attribution in the commit message.

---

## Migration

This ADR documents a change that already shipped. No further migration steps. Two reminders for current/future contributors:

1. When updating bundle/applicationId/scheme references, update **all four**: Android manifest, Android Gradle, iOS Info.plist, deep-link router. A grep for `mux_pod` or `muxpod` in `lib/`, `android/`, `ios/` should return zero hits outside the legacy intent-filter and archived docs.
2. The CLAUDE.md project memory still references the legacy `mux-pod` directory for stability. Don't rename the working directory; that path is contracted with shell scripts, gh CLI configs, and CI runners.

---

## References

- Code: commit `e0ecf94` ("feat!: rebrand muxpod → termipod + bump to 1.0.0").
- Memory: `project_todo_rename_to_termipod.md` (operational checklist, DONE v1.0.27).
- Personal-tool positioning that motivated the rebrand: forthcoming expansion in `discussions/positioning.md` (T2-A in [doc-backfill plan](../plans/doc-backfill-load-bearing.md)).
- Audit context: [post-rebrand-doc-audit §3.1](../discussions/post-rebrand-doc-audit.md).
- Related: this ADR is also why the `decisions/` numbering had a 015→016 gap from 2026-04-30 until now — two unrelated proposals had been drafted for `015` in conversation but resolved elsewhere (one folded into ADR-014 OQ-4, one deferred post-MVP). Filling this slot closes the numbering gap.
