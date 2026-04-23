# TermiPod — Release Test Plan

Evergreen manual test plan covering the mobile app + Termipod Hub
surfaces. Update in place when behavior changes; the version header at
the top of each section pins the last release the step was verified
against.

**Latest verified release: v1.0.172-alpha** (seed-demo + mock-trainer
dress-rehearsal harness on top of the v1.0.49 Observability reorg).

Pair this with:

- [`hub-mobile-test.md`](hub-mobile-test.md) — hub server install
- [`hub-host-setup.md`](hub-host-setup.md) — host-runner install
- [`hub-agents.md`](hub-agents.md) — agent spawn spec YAML
- [`mock-demo-walkthrough.md`](mock-demo-walkthrough.md) — no-GPU
  end-to-end walkthrough (fresh Ubuntu box, known hub URL)

---

## 0. Preconditions

1. Mobile APK / IPA from the GitHub Releases page — match the version
   you want to test (`termipod-vX.Y.Z-alpha-arm64-v8a.apk` on modern
   Android phones). See `hub-mobile-test.md` §1 for sideload details.
2. At least one SSH-reachable server with `tmux ≥ 3.2`. Not required
   for hub-only tests but required for §2.
3. For Hub tests: a hub-server instance + a registered host-runner +
   three tokens (owner, principal with `-handle`, host). Follow
   `hub-mobile-test.md` Track A (LAN) or Track B (VPS).
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
  Run Detail → Docs → Reviews → Inbox).

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

See `docs/research-demo-gaps.md` "Dress-rehearsal harness" for
the full pipeline recipe.

---

## 1. Smoke — bottom navigation & app boot

_Verified against v1.0.49-alpha._

The home screen shows five tabs, center-anchored on **Inbox**:

| Index | Tab      | Expected initial view |
|-------|----------|-----------------------|
| 0     | Servers  | Connection list (empty state or saved connections). |
| 1     | Vaults   | Keys + Snippets. |
| 2     | Inbox    | Default tab. SliverAppBar with title "Inbox" + search icon; filter chips below (All · Approvals · Agents · Messages · SSH). |
| 3     | Hub      | If unconfigured: "Configure Hub" CTA. If configured: four-tab layout (Projects · Agents · Hosts · Templates) with the Steward pill, Team, Refresh, and Hub-settings icons in the AppBar. |
| 4     | Settings | Scrollable settings list. |

**Steps**

1. Cold-launch the app. **Expected:** lands on Inbox (index 2). Tab
   bar underline sits under "Inbox".
2. Tap every other tab left-to-right. **Expected:** each view renders
   without jank; no crashes.
3. Kill the app and re-open. **Expected:** lands on Inbox again (not
   the last-selected tab — center tab is the default).

---

## 2. SSH / tmux round-trip

_Verified against v1.0.49-alpha. Unchanged from v1.0.2x._

1. **Servers tab → +** → fill Host / Port / Username / Auth method →
   save. **Expected:** row appears in the list.
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

_Verified against v1.0.49-alpha._

1. **Hub** tab → **Configure Hub**. (Or: Settings → Termipod Hub →
   Open Hub Dashboard.)
2. Fill:
   - **Base URL:** `https://hub.example.com` or `http://<lan-ip>:8443`
   - **Team ID:** `default`
   - **Bearer Token:** paste the owner or user token.
3. Tap **Probe URL**. **Expected:** green banner showing server
   version (e.g. `server_version: "0.4.x"`).
4. Tap **Save & Connect**. **Expected:** returned to Hub tab, now
   showing the 4-tab layout.
5. Kill + relaunch. **Expected:** configuration persists; no second
   bootstrap prompt.

---

## 4. Inbox — unified attention/feed/tasks

_Verified against v1.0.49-alpha._

The Inbox collapses attention items, recent channel events, and
in-progress tasks into one feed with filter chips.

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

### 4.2 Inbox behaviors

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

1. Tap the **search icon** in the Inbox AppBar. **Expected:** opens
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

## 5. Hub tab — 4-tab layout

_Verified against v1.0.49-alpha._

Tabs in the Hub app bar: **Projects · Agents · Hosts · Templates**.
AppBar actions: **Steward pill**, **Team** (people icon), **Refresh**,
**Hub settings** (gear).

### 5.1 Steward pill

1. Locate the Steward pill in the Hub AppBar (leftmost action).
   **Expected:** centered vertically with the neighbouring icons,
   readable contrast in both light and dark themes (chip bg uses
   `primaryContainer`, text/icon uses `onPrimaryContainer`). Tooltip
   on long-press reads "Open #hub-meta (steward)".
2. Tap it. **Expected:** navigates to the `#hub-meta` team channel.
   If the channel is missing, a SnackBar reports so.

### 5.2 Projects tab

1. Pull-to-refresh. **Expected:** project cards render with a
   created-at timestamp; FAB is bottom-right.
2. Tap the **+** FAB → enter a name → **Create**. **Expected:** new
   card appears at the top with the timestamp you just created.
3. Tap the new card. **Expected:** Linear-style project detail
   screen with a horizontal pill bar over six pages:
   **Activity · Tasks · Agents · Docs · Blobs · Info**.

### 5.3 Agents tab

1. Toggle **List / Tree** in the app bar. **Expected:** List is a
   flat table; Tree renders `agent_spawns` parent → child graph with
   indent (cycle-safe).
2. Long-press a preset chip (if any exist). **Expected:** delete
   confirmation.
3. Tap the **Spawn Agent** FAB. See §7 for the full spawn flow.

### 5.4 Hosts tab

1. **Expected:** rows for every host that has ever registered, with
   `last_seen_at` in the trailing column and `status: online` /
   `status: offline` in the subtitle. The server sweeps hosts to
   `offline` when `last_seen_at` falls more than 90 s behind; wait
   ~2 min after stopping a host-runner to see the flip.
2. Swipe a host (or tap it → **Delete host**). **Expected:** 409
   Conflict if any non-terminated agents still reference it;
   otherwise the row disappears and an audit row is written (see §6
   on Audit Log).

### 5.5 Templates tab

1. **Expected:** templates grouped by category under
   `<dataRoot>/default/templates/<category>/`. Tap one. **Expected:**
   raw YAML preview in a sheet.

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
| `agent.spawn`         | Hub → Agents → Spawn Agent FAB          | Spawn      |
| `agent.terminate`     | Tap agent → Terminate (or PATCH status) | Terminate  |
| `attention.decide`    | Inbox → Approve/Reject an attention row | Decide     |
| `schedule.create`     | Team → Settings → Schedules → **+**     | Schedule   |
| `schedule.delete`     | Team → Settings → Schedules → trash     | Schedule   |
| `host.delete`         | Hub → Hosts → tap row → Delete host     | Host       |

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

1. Hub → Agents → **Spawn Agent** FAB.
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
   awaiting approval.` No `agents` row yet; a new Inbox row appears
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

From Hub → Projects → tap a project → horizontal pill bar:
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
   non-blocking; Inbox shows a "Failed to refresh" strip, not a full
   crash screen.
3. **Token rotation.** Swap the saved token for an invalid one in
   Hub settings. **Expected:** all REST calls 401; the app surfaces
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
- **Stream memory cap.** Inbox / project channels keep ~200 events
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
