# Release testing

> **Type:** how-to
> **Status:** Current (2026-04-28)
> **Audience:** operators
> **Last verified vs code:** v1.0.172 (per-section version markers below pin steps individually)

**TL;DR.** Evergreen manual test plan covering the mobile app +
Termipod Hub surfaces. Update in place when behavior changes; the
version header at the top of each section pins the last release the
step was verified against. Run the relevant subset before tagging a
release for device test.

Pair this with:

- [`install-hub-server.md`](install-hub-server.md) — hub server install
- [`install-host-runner.md`](install-host-runner.md) — host-runner install
- [`../reference/hub-agents.md`](../reference/hub-agents.md) — agent spawn spec YAML
- [`run-the-demo.md`](run-the-demo.md) — no-GPU
  end-to-end walkthrough (fresh Ubuntu box, known hub URL)

---

## 0. Preconditions

1. Mobile APK / IPA from the GitHub Releases page — match the version
   you want to test (`termipod-vX.Y.Z-alpha-arm64-v8a.apk` on modern
   Android phones). See `install-hub-server.md` §1 for sideload details.
2. At least one SSH-reachable server with `tmux ≥ 3.2`. Not required
   for hub-only tests but required for §2.
3. For Hub tests: a hub-server instance + a registered host-runner +
   three tokens (owner, principal with `-handle`, host). Follow
   `install-hub-server.md` Track A (LAN) or Track B (VPS).
4. A terminal on the hub box (or anywhere with the owner token) to
   seed test data via `curl`.

### 0.1 No-GPU dress-rehearsal harness

Two tools let you exercise the research-demo surface without
running nanoGPT on a real GPU. Use one or both before §3+ hub
walkthroughs.

- **`hub-server seed-demo`** — writes an `ablation-sweep-demo`
  project with 6 completed runs, a briefing memo, a pending
  review, and one open attention item. Idempotent. Good for
  reviewing the already-finished project UI (Project Detail →
  Run Detail → Docs → Reviews → Me).

  ```
  hub-server seed-demo --data <hub-data-root>
  ```

- **`hub/cmd/mock-trainer`** — writes a real trackio SQLite or
  wandb-offline JSONL file with a synthetic training curve. The
  host-runner's metrics readers consume the output unchanged, so
  polling + digest + mobile sparkline all light up. Pair with a
  hub `POST /v1/teams/{team}/runs` whose `trackio_run_uri`
  matches the printed URI, then point host-runner at the same
  `--dir`.

  ```
  mock-trainer --vendor trackio --dir /tmp/trackio \
    --project ablation-sweep-demo --run size384-lion \
    --size 384 --optimizer lion --iters 1000
  ```

See `../plans/research-demo-gaps.md` "Dress-rehearsal harness" for
the full pipeline recipe.

---

## 1. Smoke — bottom navigation & app boot

_Updated for the IA redesign (v1.0.175–v1.0.182). Replaces the old
Servers / Vaults / Inbox / Hub / Settings layout._

The home screen shows five tabs — **Projects · Activity · Me · Hosts ·
Settings** — center-anchored on **Me** (index 2), rendered as the big
outset button:

| Index | Tab       | Expected initial view |
|-------|-----------|-----------------------|
| 0     | Projects  | Project cards + Templates row. FAB creates a project. Project detail opens Overview · Tasks · Channels · Docs · Blobs · Agents. |
| 1     | Activity  | Team-wide audit/event feed. Steward filter chip in the app bar isolates rows where `actor_kind='agent'` AND `actor_handle='steward'`. |
| 2     | Me        | Default landing tab. Attention items + "My Work" strip + "Since you were last here" digest. StewardBadge lights up on steward-stamped rows (v1.0.183+). |
| 3     | Hosts     | Unified inventory: SSH connections ∪ hub-registered hosts, joined on `hostBindingsProvider`. |
| 4     | Settings  | Scrollable settings list. Team Settings, Templates, and hub profile management are reached via the TeamSwitcher pill (top-left of every tab) → popup menu, not from here. |

**Steps**

1. Cold-launch the app. **Expected:** lands on Me (index 2). Center
   button is raised/outset and highlighted.
2. Tap every other tab left-to-right. **Expected:** each view renders
   without jank; no crashes.
3. Kill the app and re-open. **Expected:** lands on Me again (not the
   last-selected tab — center tab is the default).
4. Tap the **TeamSwitcher pill** (top-left of any tab). **Expected:**
   popup menu opens with the saved hub profiles list (active marked
   with check), then "Add profile…" / "Manage profiles…" / "Templates
   & engines" / "Team settings". Tap **Team settings**. **Expected:**
   opens Team Settings with Councils · Steward · Schedules · Usage ·
   Members · Policies · Channels tiles.

---

## 2. SSH / tmux round-trip

_Verified against v1.0.49-alpha. The host creation flow now lives on
the unified **Hosts** tab (IA Wedge 2) — SSH-only entries show up
alongside hub-registered hosts._

1. **Hosts tab → +** → fill Host / Port / Username / Auth method →
   save. **Expected:** row appears in the list with scope "personal".
2. Tap the new connection → **Connect**. **Expected:** session list
   loads; if none, the "no tmux sessions" empty state shows a
   **Create session** button.
3. Tap a session → tap a window → tap a pane. **Expected:** terminal
   view opens with ANSI output; action-bar profile resolves
   (Claude Code / Codex / tmux / generic).
4. Send `Ctrl-b c` via the bolt menu → a new window appears in the
   list when you swipe back.
5. Deep link: open
   `termipod://connect?server=<id>&session=<n>&window=<n>&pane=<i>`
   from the browser. **Expected:** app launches and jumps straight
   to that pane.

---

## 3. Hub bootstrap

_Updated for the IA redesign. The standalone "Hub tab → Configure Hub"
CTA is gone — hub connectivity is assumed by the Projects / Activity /
Hosts tabs, which each show an empty-state bootstrap prompt if the hub
isn't configured yet._

1. Open **Settings → Termipod Hub → Open Hub Dashboard**. (Or tap the
   bootstrap prompt on any of Projects / Activity / Hosts.)
2. Fill:
   - **Base URL:** `https://hub.example.com` or `http://<lan-ip>:8443`
   - **Team ID:** `default`
   - **Bearer Token:** paste the owner or user token.
3. Tap **Probe URL**. **Expected:** green banner showing server
   version (e.g. `server_version: "0.4.x"`).
4. Tap **Save & Connect**. **Expected:** returned to Settings; the
   Projects / Activity / Hosts tabs now render their hub-backed lists.
5. Kill + relaunch. **Expected:** configuration persists; no second
   bootstrap prompt.

---

## 4. Me tab — unified attention/feed/tasks

_Renamed from "Inbox" by the IA redesign (v1.0.175); same underlying
feed, new framing as the director's personal desk._

The Me tab collapses attention items, recent channel events, and
in-progress tasks into one feed with filter chips, plus a "My Work"
strip and a "Since you were last here" digest card.

### 4.1 Seed test data

On the hub host:

```bash
HUB=https://hub.example.com
TOK=<owner-or-user-token>

# Attention item — this will appear under "Approvals" chip.
curl -fsS -H "Authorization: Bearer $TOK" -H 'content-type: application/json' \
  -X POST "$HUB/v1/teams/default/attention" \
  -d '{"scope_kind":"team","kind":"decision","summary":"Approve staging deploy?","severity":"major"}'

# Message to a team-scope channel — appears under "Messages".
CHID=$(curl -fsS -H "Authorization: Bearer $TOK" \
  "$HUB/v1/teams/default/channels" | jq -r '.[0].id')
curl -fsS -H "Authorization: Bearer $TOK" -H 'content-type: application/json' \
  -X POST "$HUB/v1/teams/default/channels/$CHID/events" \
  -d '{"type":"message","from_id":"@ops","parts":[{"kind":"text","text":"hello inbox"}]}'
```

### 4.2 Me tab behaviors

1. Pull-to-refresh. **Expected:** items reload; newest at top.
2. Tap chip **Approvals**. **Expected:** only the seeded decision item
   is shown.
3. Tap the attention row. **Expected:** bottom sheet with **Approve**
   / **Reject** buttons. Tap **Approve**. **Expected:** row
   disappears; SnackBar "Decision recorded".
4. Tap chip **Messages** → tap the "hello inbox" row. **Expected:**
   jumps into the team channel view, scrolled to the latest event.

### 4.3 Search

_Search screen talks to `GET /v1/search` (SQLite FTS5)._

1. Tap the **search icon** in the Me AppBar. **Expected:** opens
   a dedicated screen with an autofocused TextField.
2. Type `hello`. **Expected:** after ~350 ms debounce, results show
   the "hello inbox" event with its channel + timestamp.
3. Clear the field. **Expected:** results clear; no spinner stuck.
4. Type a query with no matches. **Expected:** empty state ("No
   results for '<q>'"), no error.
5. Type quickly so multiple requests race. **Expected:** only the
   final result set is rendered (request-sequence guard — older
   responses are dropped).

---

## 5. Projects / Hosts — the hub-backed surfaces

_Rewritten for the IA redesign. The old 4-tab "Hub" screen was
flattened into top-level **Projects** and **Hosts** tabs; Agents moved
into project detail; Templates live under Projects as a one-home row._

Top-level app bar (any hub-backed tab) actions: **TeamSwitcher pill**
(top-left — opens a popup menu with profiles, templates, and team
settings), **Refresh**, **search**.

### 5.1 TeamSwitcher pill (profiles / templates / team)

1. Locate the TeamSwitcher pill at the top-left of the app bar.
   **Expected:** readable contrast in both light/dark themes. Pill
   label shows the active hub profile's display name.
2. Tap it. **Expected:** popup menu opens with sections —
   **Profiles** (one row per saved profile, active marked with a
   check), **Add profile…**, **Manage profiles…**, **Templates &
   engines**, **Team settings**.
3. Tap a non-active profile in the list. **Expected:** active switches;
   dashboards re-hydrate from that profile's offline cache while a
   network refresh runs in the background. Pill label updates.
4. Re-open the menu and tap **Team settings**. **Expected:** opens
   Team Settings with tiles for **Councils · Steward · Schedules ·
   Usage · Members · Policies · Channels**. Tap **Steward** →
   Steward Config form with principal handle, tone, constraints
   (SharedPreferences-local today — server round-trip is an open
   follow-up).
5. Re-open the menu and tap **Add profile…**. **Expected:** Add hub
   profile screen with blank form. Cancel out — no new profile
   created.
6. Re-open the menu and tap **Manage profiles…**. **Expected:** list
   view of saved profiles with rename / edit / delete affordances on
   each row's overflow menu. Active profile marked with a filled
   check.

### 5.2 Projects tab

1. Pull-to-refresh. **Expected:** project cards render with a
   created-at timestamp; FAB is bottom-right. A **Templates** row
   underneath lists YAML agent templates grouped by category under
   `<dataRoot>/default/templates/<category>/` — tap one for a raw
   YAML preview.
2. Tap the **+** FAB → enter a name → **Create**. **Expected:** new
   card appears at the top with the timestamp you just created.
3. Tap the new card. **Expected:** Linear-style project detail screen
   with a horizontal pill bar over six pages:
   **Overview · Tasks · Channels · Docs · Blobs · Agents**.

### 5.3 Project detail → Agents

1. Toggle **List / Tree** in the sub-app-bar. **Expected:** List is a
   flat table; Tree renders `agent_spawns` parent → child graph with
   indent (cycle-safe).
2. Long-press a preset chip (if any exist). **Expected:** delete
   confirmation.
3. Tap the **Spawn Agent** FAB. See §7 for the full spawn flow.

### 5.4 Hosts tab

1. **Expected:** unified list: SSH connections (from the old Servers
   tab) ∪ hub-registered hosts, joined on `hostBindingsProvider`.
   Hub-registered rows show `last_seen_at` and `status: online` /
   `status: offline`; the server sweeps to `offline` when
   `last_seen_at` falls > 90 s behind (~2 min after a host-runner
   stops).
2. Swipe a hub-registered host (or tap it → **Delete host**).
   **Expected:** 409 Conflict if any non-terminated agents still
   reference it; otherwise the row disappears and an audit row is
   written (see §6 on Audit Log).

---

## 6. Team — Members · Policies · Channels · Settings

_Verified against v1.0.49-alpha._

Accessed via the **Team** (people) icon in the Hub AppBar. Four pill
sub-tabs.

### 6.1 Members

1. **Expected:** one row per `scope.handle` from `auth_tokens` (or
   `@principal` / `@role` fallbacks). Role chip (owner / user / …).
2. Tokens issued without `-handle` land as `@principal (unnamed)` —
   that's a server-side gap, not a client bug.

### 6.2 Policies

1. **Expected:** read-only YAML preview of the current
   `templates/policies/default.v1.yaml`.

### 6.3 Channels

1. **Expected:** team-scope channels (rows where `project_id` is
   NULL). `#hub-meta` is always present — that's the channel the
   Steward pill opens.
2. Tap a row → events stream live via SSE; excerpt parts render with
   a monospace line-number gutter.

### 6.4 Settings — Schedules, Usage, **Audit Log**

Three tiles. This is where Phase 2/3/audit-feature UIs live.

#### 6.4.1 Schedules

1. Tap **Schedules** tile.
2. Tap the **+** FAB → fill Name, Cron Expr (e.g. `*/5 * * * *`),
   Spawn Spec YAML (minimum: `backend:\n  cmd: bash -lc "echo hi"`),
   toggle Enabled. **Expected:** 201 Created; row appears with a
   green "Enabled" chip; an `agent.spawn` row lands in the audit log
   when the cron tick fires (actor is `system`).
3. Toggle **Enabled** off. **Expected:** chip goes grey; scheduler
   unregisters.
4. Tap the trash icon. **Expected:** row deletes; a `schedule.delete`
   row lands in the audit log.

#### 6.4.2 Usage

1. Tap **Usage** tile. **Expected:** aggregate card at the top
   (total spent / budget / %) + per-project and per-agent breakdown
   with mini progress bars. Values are summed client-side from
   `listAgents()`; rows without `budget_cents` show spent only.

#### 6.4.3 Audit Log  (NEW in v1.0.49)

1. Tap **Audit Log** tile.
2. **Expected:** list of sensitive actions newest-first, with rows
   shaped as `<icon> <summary> · @actor · action · short-time`.
3. Filter chips at the top: **All · Spawn · Terminate · Decide ·
   Schedule · Host**. Tap each and verify counts change.
4. Pull-to-refresh. **Expected:** reloads without duplicating rows.
5. Tap a row. **Expected:** bottom sheet with Action / Actor /
   Target / Time + the `meta` map as key/value pairs. Selectable
   text.

**End-to-end audit coverage check.** Trigger each write hook and
verify one row lands in the Audit Log under the right filter:

| Action                | How to trigger                          | Filter     |
|-----------------------|-----------------------------------------|------------|
| `agent.spawn`         | Projects → project → Agents → Spawn FAB | Spawn      |
| `agent.terminate`     | Tap agent → Terminate (or PATCH status) | Terminate  |
| `attention.decide`    | Me → Approve/Reject an attention row    | Decide     |
| `schedule.create`     | TeamSwitcher pill → Team settings → Schedules → **+**   | Schedule   |
| `schedule.delete`     | TeamSwitcher pill → Team settings → Schedules → trash   | Schedule   |
| `host.delete`         | Hosts → tap row → Delete host           | Host       |

Direct REST probe of the endpoint:

```bash
curl -fsS -H "Authorization: Bearer $TOK" \
  "$HUB/v1/teams/default/audit?limit=10" | jq '.[0:3]'
# Filter by action:
curl -fsS -H "Authorization: Bearer $TOK" \
  "$HUB/v1/teams/default/audit?action=agent.spawn&limit=20" | jq 'length'
```

**Actor attribution.** Rows triggered via the mobile app with a
principal token show `actor_handle=<handle>`, `actor_kind=user`.
Scheduler-initiated spawns show `actor_kind=system`, no handle.
MCP / REST callers carrying an agent token show `actor_kind=agent`.

---

## 7. Agent spawn — end-to-end

_Verified against v1.0.49-alpha._

### 7.1 Direct (no policy gate)

1. Projects → tap a project → **Agents** sub-tab → **Spawn Agent** FAB.
2. Handle: `smoke-1`. Kind: `claude-code`. Host: pick an online host.
   Spec YAML:

   ```yaml
   backend:
     cmd: bash -lc "echo hello; sleep 600"
   ```

3. Submit. **Expected:** SnackBar `Agent "smoke-1" spawned.` Row
   appears in Agents (List view) with status=`pending`, flips to
   `running` within ~3s once the host-runner picks it up.
4. Verify in Audit Log → **Spawn** filter: a new `agent.spawn` row
   named `spawn smoke-1 (claude-code)`.
5. Terminate: tap the agent → **Terminate**. **Expected:** status
   flips to `terminated`, pane closes on the host, a new row lands
   in the Audit Log under **Terminate**.

### 7.2 Policy-gated

1. Edit the team policy to set `spawn: significant` (requires an
   approver group). Restart `hub-server` (policy is loaded on boot).
2. Repeat §7.1 step 2. **Expected:** SnackBar `Spawn request sent —
   awaiting approval.` No `agents` row yet; a new Me-tab row appears
   under **Approvals**.
3. Open that attention row → **Approve**. **Expected:** real spawn
   fires, the agent row appears and flips to running. Audit Log
   shows **two** entries: `attention.decide` (with meta
   `decision=approve`) and `agent.spawn`.
4. Try the same with **Reject**. **Expected:** no agent row
   appears; audit shows `attention.decide` with `decision=reject`
   only.

---

## 8. Project detail — Docs, Blobs, Composer attachments

_Verified against v1.0.49-alpha (Phase 2)._

From Projects → tap a project → horizontal pill bar:
**Activity · Tasks · Agents · Docs · Blobs · Info**.

### 8.1 Activity + composer

1. Pick the **Activity** page. **Expected:** event stream with a
   composer at the bottom (TextField + attach icon + send button).
2. Type "hello project" → send. **Expected:** event appears at the
   top within a second (SSE).
3. Tap the **attach icon**, pick a small image. **Expected:** upload
   progress → a chip with filename renders in the composer; after
   send, the event bubble shows an `_AttachmentChip` that downloads
   on tap.

### 8.2 Tasks

1. **Tasks** page → FAB → fill title, optional body → **Create**.
   **Expected:** task appears under **Open** column. Swipe right to
   promote to **In progress**, swipe again to **Done**; swipe left
   to demote.
2. Tap a task. **Expected:** detail screen with subtasks and a
   parent chevron (if the task has a parent).

### 8.3 Docs (read-only Markdown)

1. Drop a Markdown file into
   `<dataRoot>/default/projects/<pid>/docs/notes.md` on the hub box.
2. **Docs** page → pull-to-refresh. **Expected:** `notes.md` appears
   in the list.
3. Tap it. **Expected:** viewer renders Markdown (headings, lists,
   code blocks, links). Text is selectable. No edit affordance —
   docs are read-only in v1.

### 8.4 Blobs

1. **Blobs** page → **+** FAB → pick a local file ≤25 MiB.
   **Expected:** upload succeeds, row shows `sha256-<first-8>` and
   size.
2. Re-upload the same file. **Expected:** dedup — the existing row
   reference-counts, no duplicate chip appears.
3. Tap a row. **Expected:** downloads into the app's temp dir and
   opens the system share sheet.
4. Try a file >25 MiB. **Expected:** server returns 413 Payload Too
   Large; SnackBar reports the limit.

---

## 9. Regression checks — must still work

Not feature areas but things easy to break:

1. **Light / dark theme parity.** Switch themes via Settings → verify
   Hub AppBar chips, Audit Log row icons, and attention severity
   chips are readable in both.
2. **Offline re-open.** Kill the app in airplane mode → reopen.
   **Expected:** UI renders the last known state; error banners are
   non-blocking; Me tab shows a "Failed to refresh" strip, not a full
   crash screen.
3. **Token rotation.** Swap the saved token for an invalid one via
   the TeamSwitcher pill → Manage profiles… → row overflow → Edit
   connection. **Expected:** all REST calls 401; the app surfaces
   this as a banner, not silently.
4. **Long-running SSE.** Leave a project channel open for 10 min on
   Wi-Fi with the screen on. **Expected:** no disconnect (nginx
   config in `hub/deploy/nginx/termipod-hub.conf` sets
   `proxy_read_timeout 3600s` for stream locations).
5. **Foldable / tablet.** Open the app on a tablet or unfolded
   device. **Expected:** two-pane layout where applicable; no
   clipped AppBars.

---

## 10. Known limitations (not failures)

Do **not** file these as test-plan failures — they're tracked
roadmap / caveats:

- **Host offline detection is ~90–120 s lagged** (sweeper tick is 30 s
  and threshold is 90 s past `last_seen_at`).
- **Agent status lags by one poll tick (~3 s)** after the backing CLI
  starts, exits cleanly, or falls back to the shell. The `crashed`
  state replaces the old "stale running" behaviour: if a pane
  disappears or the CLI exits, the host-runner reconcile loop will
  flip the row on its next tick.
- **Stream memory cap.** Me tab / project channels keep ~200 events
  in memory.
- **No push notifications.** The app updates while foreground only.
- **No token rotation UI.** Issue a new token via CLI and swap it in.
- **Audit log** covers the five write hooks listed in §6.4.3. Future
  extensions (policy edits, token issue/revoke, member role change)
  are follow-ups.

---

## 11. Reporting

For each failed step:

1. Capture the build version from **Settings → About** (or
   `version:` in `pubspec.yaml`).
2. Screenshot or record the UI state.
3. Grab the hub logs (`journalctl -u termipod-hub -n 200`) if the
   step touched the server.
4. File against `https://github.com/physercoe/termipod/issues` with
   a minimal repro. Tag with `release-test` and the test section
   number (e.g. `§6.4.3`).
