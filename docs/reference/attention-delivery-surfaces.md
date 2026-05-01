# Attention delivery surfaces

> **Type:** reference
> **Status:** Current (2026-05-01)
> **Audience:** contributors · prompt authors · operators
> **Last verified vs code:** v1.0.350-alpha

**TL;DR.** Attention items are produced by agents (`request_approval`, `request_select`, `request_help`, `permission_prompt`, `template_proposal`, `idle`) and consumed by the principal across multiple **delivery surfaces** — the Me tab queue, the in-app badge counts, system local notifications (Android + iOS), and (post-MVP) ntfy push for killed-app delivery. This file maps each attention kind × severity to the surfaces that fire, what payload they carry, and the timeout / silencing rules. Read [attention-kinds](attention-kinds.md) first for the *what each kind means*; this file is the *where each kind shows up*.

---

## Surfaces (today)

| Surface | Implementation | When fires | Behavior when user isn't looking |
|---|---|---|---|
| **Me tab attention queue** | `lib/screens/me/me_screen.dart` (`_AttentionSection`) | Always — every attention row appears here | Persists until resolved or dismissed |
| **App badge / tab dot** | Riverpod-derived from attention count | Always | Persists; cleared on read |
| **In-app local notification (foreground / backgrounded)** | `lib/services/notifications/local_notifications.dart` | Configured via Settings; default-on for `attention` channel | Fires while process is alive; dismissed on tap or resolve |
| **System notification on app close** | Same plugin; OS-managed once posted | Same conditions | Persists in tray until tap or dismiss |

**Out of scope today (post-MVP):**

| Surface | Status | Notes |
|---|---|---|
| Killed-state delivery via ntfy / FCM / APNs | Deferred | Planned in `plans/mvp-parity-gaps.md` Phase 1.5b. Local notifications work only while the app process is alive; for true killed-state push we need a server relay. |
| Email / Slack / iMessage bridging | Out of scope | Personal-tool frame ([positioning §1.5](../discussions/positioning.md)) — bridges are OpenClaw / Hermes territory. |
| Watch / wearable | Out of scope | Same. |

---

## Kind × surface matrix

The matrix below pins delivery for each attention kind. Severity tunes urgency *within* the surface (e.g. high importance vs default importance on Android local notification); it doesn't toggle the surface itself.

| Kind | Me queue | Badge | Local notification | Killed-state push (post-MVP) | Default severity |
|---|---|---|---|---|---|
| `approval_request` (request_approval) | ✓ | ✓ | ✓ (`attention` channel) | ✓ | `minor` (caller can override) |
| `select` (request_select) | ✓ | ✓ | ✓ (`attention` channel) | ✓ | `minor` |
| `help_request` (request_help, mode=clarify) | ✓ | ✓ | ✓ (`attention` channel) | ✓ | `minor` |
| `help_request` (mode=handoff) | ✓ | ✓ | ✓ (`attention`, importance=high) | ✓ | `major` |
| `permission_prompt` | ✓ | ✓ | ✓ (`attention` channel) — caller-blocking | ✗ (sync gate; long delay = call timeout) | n/a (engine-driven) |
| `template_proposal` | ✓ | ✓ | ✓ (`attention` channel) | ✓ | `minor` |
| `idle` (host-runner state signal) | ✗ — state, not request | ✗ | ✗ | ✗ | n/a |

Notes:

- `permission_prompt` is the only synchronous gate — a delayed answer eventually trips engine-side timeout and the call is denied/cancelled. Killed-state push is *not* useful here because the engine has already given up by the time the user sees the notification.
- `idle` is a state signal, not a request. It never surfaces as a notification; it shows in agent state UI (running / pending / paused / archived).

---

## Notification channels (Android)

The mobile app declares two notification channels (`AndroidNotificationChannel`):

| Channel id | Purpose | Default importance | Default user setting |
|---|---|---|---|
| `termipod_attention` | Attention items needing the principal's decision | High | **Enabled** |
| `termipod_hub_events` | Lower-salience signals (turn finished, session paused) | Default | **Disabled** |

The split lets the user enable urgent (attention) without enabling chatty (hub events). Both channels are created at app init; users can override the importance per-channel via system settings.

iOS uses analogous `DarwinNotificationDetails` — no channel concept at the OS level, but the same logical split via the in-app settings toggle.

---

## Severity → importance mapping

`severity` in the attention payload (set by the calling agent or defaulted by the hub) maps to OS-level importance:

| Severity | Android importance | iOS interruption | UI accent |
|---|---|---|---|
| `minor` | `Importance.defaultImportance` | none / standard | standard chip |
| `major` | `Importance.high` | active | accent chip |
| `critical` | `Importance.max` (sound + heads-up) | time-sensitive | red chip + persistent banner |

`critical` should be reserved for production-blocker / data-loss-risk situations. Most attention items should be `minor`; an escalation to `major` is the steward saying "don't miss this."

---

## Settings & user controls

User-facing toggles (`Settings → Notifications`):

| Toggle | Effect | Storage key |
|---|---|---|
| Enable notifications | Master kill switch; if off, no local or push notifications fire (Me queue + badge still work) | `settings_notifications_enabled` |
| Attention items | `termipod_attention` channel on/off | `settings_notify_attention` |
| Turn finished | `termipod_hub_events` channel — turn-finished events | `settings_notify_turn_finished` |
| Session paused | `termipod_hub_events` channel — session-paused events | `settings_notify_session_paused` |
| Tap routes to Me | Tap on attention notification opens Me; otherwise opens current screen | `settings_notify_route_to_me` |

When the master kill switch is off, the notification *channels* are still registered (the OS doesn't allow ad-hoc channel removal), but no `show()` calls fire.

---

## Tap routing

Notification tap opens a deep link via the registered `onDidReceiveNotificationResponse` handler:

| Notification source | Tap destination |
|---|---|
| Attention item (any kind) | Me tab → `_AttentionSection` scrolled to the item, or `approval_detail_screen.dart` for tappable subtypes |
| Turn finished | The session's chat screen |
| Session paused | The session's chat screen |
| Template proposal | `approval_detail_screen.dart` (template diff view) |

The handler is set in `main.dart` after the navigator is ready (the service can't dispatch routes without a ready navigator).

---

## Throttling and de-dup

Per-kind, per-agent rate limits prevent attention spam:

- **Per-agent floor:** 1 notification per 2 seconds. Bursts within 2s are coalesced into a single notification with "+ N more" pluralization.
- **Re-attention on resolution:** when an attention item is resolved (`/decide`), no notification fires for the *resolution* — the user just-pressed the button. The agent's follow-up turn (e.g., "thanks, proceeding") may fire separately under the turn-finished channel if enabled.
- **Killed-state deduping (post-MVP):** ntfy will use the attention's stable id as the message id so re-deliveries don't duplicate.

---

## Killed-state delivery — the post-MVP gap

Today, when the app process is killed (user swipes away on Android, iOS suspends after timeout), local notifications cannot fire because there is no process to call `show()`. This means:

- An attention item raised while the app is killed lands in the Me queue and sets the badge, but **no notification rings the device until the user opens the app**.
- Long-running stewards on a VPS can produce attention items the principal misses.

The closure is a server-side push relay. Two options on the table:

1. **ntfy.sh self-hosted.** Lightweight, self-hosted, FCM/APNs bridge built in. Pairs naturally with the personal-tool framing (no third-party SaaS). Tracked in `plans/mvp-parity-gaps.md` Phase 1.5b.
2. **FCM / APNs direct.** More plumbing (Google + Apple registration, certificate management) but native channel quality. Probably overkill for personal-tool MVP.

The decision tracker is in `plans/mvp-parity-gaps.md`. The above table marks killed-state push as ✗ today.

---

## How to add a new delivery rule

1. **Decide the surface(s).** Me queue is automatic. Local notification + badge needs an explicit fire-site.
2. **Pick or create a channel.** Don't create a new channel without a clear user-facing toggle — channel proliferation is a UX rot. If the new event fits an existing channel, reuse it.
3. **Wire the call site.** The hub event stream → mobile listener (`hub_provider.dart` and adjacent) → `LocalNotifications.instance.show…(channel, severity, …)`.
4. **Update this file's matrix.** Don't ship surface-mapping changes without a doc update.
5. **Test the killed-app behavior.** Until ntfy lands, surface mappings only matter for foreground/backgrounded delivery; document that explicitly.

---

## References

- [Reference: attention-kinds](attention-kinds.md) — what each kind means.
- [ADR-011](../decisions/011-turn-based-attention-delivery.md) — turn-based delivery model that backs the queue + badge.
- [Plan: mvp-parity-gaps](../plans/mvp-parity-gaps.md) — Phase 1.5b: killed-state push.
- [Discussion: attention-interaction-model](../discussions/attention-interaction-model.md) — design rationale.
- Code: `lib/services/notifications/local_notifications.dart`, `lib/screens/me/me_screen.dart` `_AttentionSection`, `lib/screens/approvals/approval_detail_screen.dart`, `hub/internal/server/handlers_attention.go`.
