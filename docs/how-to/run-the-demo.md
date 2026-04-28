# Run the demo

> **Type:** how-to
> **Status:** Current (2026-04-28)
> **Audience:** operators, reviewers
> **Last verified vs code:** v1.0.312

**TL;DR.** Step-by-step for a reviewer who wants to see the
research-demo path (project → runs → sparklines → briefing → review
→ inbox) light up without running nanoGPT on a real GPU. Uses the
`seed-demo` + `mock-trainer` dress-rehearsal harness.

**Assumptions**
- Fresh Ubuntu 22.04 or 24.04 box with sudo access.
- A **hub URL** and a **bearer token** — either because you own the
  hub (see `install-hub-server.md` to stand one up) or because a hub
  operator minted a `kind=host` (and optionally `kind=user`) token
  for you.
- A TermiPod APK on your phone (≥ `v1.0.170-alpha`).
- The hub itself is already running somewhere. Standing up a hub is
  out of scope here — see `install-hub-server.md`.

There are two paths depending on what you have:

| You have…                              | Use path                |
|----------------------------------------|-------------------------|
| A hub you control + an owner token     | **A.** Hub-side shortcut |
| A fresh Ubuntu box + a hub URL + host token | **B.** Worker pipeline |

Path A shows the finished-project UI instantly (zero training, no
polling loop). Path B exercises the live pipeline — reader → poller →
digest → mobile sparkline — which is the dress rehearsal you actually
want before plugging in a real GPU worker.

---

## Path A — Hub-side shortcut (seed-demo)

Run this on the hub box (where `hub-server` is installed and the data
root lives). Takes about 10 seconds.

```bash
# as the hub's service user, if you installed under systemd (Track B
# in install-hub-server.md):
sudo -u termipod-hub /usr/local/bin/hub-server seed-demo \
  --data /var/lib/termipod-hub

# or, if you're running Track A with the data root in your $HOME:
hub-server seed-demo --data ~/hub-test
```

Expected output:

```
seed-demo: inserted demo state.
  project:    01H…
  runs:       6
  document:   01H…
  review:     01H… (pending)
  attention:  01H… (open decision)
```

Re-running is a no-op (prints "project already exists (id=…)
— nothing written"). To **refresh** after a seed-content upgrade
(new plot families, new run shapes) pass `-reset` — it wipes the
existing `ablation-sweep-demo` project (and its runs, metrics, docs,
reviews, attention) and re-inserts with current code. Other projects
on the hub are untouched:

```bash
hub-server seed-demo --data ~/hub-test -reset
# seed-demo: reset — deleted prior demo rows.
# seed-demo: reset + re-inserted demo state.
#   project:    01KPX…
```

On the phone: open TermiPod → **Me** tab. The seeded decision
("Approve nightly sweep budget…") shows up as an attention item,
stamped with a **StewardBadge** (authoritative since v1.0.183 —
`actor_kind='agent'` + `actor_handle='steward'` on the row). Tap
through → **Projects** tab → `ablation-sweep-demo` → tap each of the
6 runs. You should see ~10 metric tiles + 1 image panel + 1
"Distributions" panel per run, plus a "Sweep compare" scatter on the
project overview — covering the dominant wandb/tensorboard plot
archetypes (v1.0.184–v1.0.190):

- `loss/{train,val}` — multi-series overlay (val gap widens late)
- `smooth/{train_raw,train_ema}` — raw vs EMA-smoothed overlay
- `sys/{gpu_util,gpu_mem,cpu_util}` — three system metrics
- `weights_dist/p{5,25,50,75,95}` — percentile band over time
- `eval/{perplexity,bleu,accuracy}` — **sparse** eval (10 checkpoints
  per run, visibly fewer vertices than the dense curves)
- `grokking/success_rate` — phase-transition shape (flat → sharp ramp)
- `grads/layer{0..3}` — per-layer gradient-norm overlay
- `learning_rate`, `grad_norm`, `throughput/tokens_per_sec` — single
  scalars
- `samples/generations` (image panel, v1.0.185) — 3 PNG checkpoints
  per run at steps 0 / 500 / 999; scrub the slider to see the image
  evolve from noise to diagonal-wave structure as training progresses
- `attention/layer0_head0` (image panel, v1.0.190) — 32×32 causal
  attention heatmap at the same 3 checkpoints; the diagonal band
  tightens as the head specializes, Lion noticeably sharper than
  AdamW. Covers the wandb heatmap/contour archetype.
- `grads_hist/layer0`, `weights_hist/all` (Distributions panel,
  v1.0.188) — 4 histogram checkpoints per metric; scrub the slider
  to see gradients tighten and weights drift with training. Lion's
  gradient distribution is visibly narrower than Adam's by late steps.
- Project overview "Sweep compare" scatter (v1.0.187) — one dot per
  run, X/Y/color axes pick from `config_json` + `final_metrics`;
  defaults to (first config key) × loss/val × optimizer.

Bigger-embed + Lion converges lowest on `loss/*`; Lion also "groks"
~10% earlier.

**IA breadth (v1.0.191).** The sweep doesn't stand alone. seed-demo
also lands:

- **`lab-ops`** — a `kind=standing` parent project containing a
  `#lab-ops` channel (4 steward + trainer messages), 2 cron schedules
  (daily paper triage, weekly review), and a handbook memo. Shows
  that Project supports domains beyond ML training.
- **`ablation-sweep-demo`** — nested under `lab-ops`, carries a
  4-phase plan, a milestone, 3 tasks, and a 50000¢ budget badge. Runs
  attach to host `gpu-west-01` via agent `trainer-0`.
- **`reproduce-gpt2-small`** — a sibling `kind=goal` project
  instantiated from the `reproduce-paper` template
  (`template_id='reproduce-paper'`, `parameters_json` bound), 1 draft
  plan + 2 tasks + 0 runs. Proves the template-binding shape without
  running anything.
- **Activity tab** — 6 audit rows from the steward populate the
  Activity feed on first open.

Every row type in the ia-redesign entity-surface matrix is seeded at
least once so the generic IA is visible on a fresh install.

This covers the *review* surface. It does not exercise the host-runner
poller — those `run_metrics` rows were written directly by seed-demo.
For the live-pipeline rehearsal, continue with Path B below.

---

## Path B — Worker pipeline (mock-trainer + host-runner)

You are on a fresh Ubuntu box. You have a hub URL (e.g.
`https://hub.example.com`) and a **host token** (from your hub
operator — issued with `hub-server tokens issue -kind host …`; see
`install-hub-server.md` §3). You also want a **principal** (human) token
for running `curl` to create projects / runs.

### B.1 System prerequisites

```bash
sudo apt update
sudo apt install -y build-essential git tmux curl jq
# Install Go 1.23+ (Ubuntu's default is older — use the official
# tarball or a PPA):
curl -LO https://go.dev/dl/go1.23.4.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.23.4.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh
source /etc/profile.d/go.sh
go version                      # expect go1.23+
```

### B.2 Clone and build

```bash
git clone https://github.com/physercoe/termipod.git
cd termipod/hub
go build -o ~/hub-server   ./cmd/hub-server
go build -o ~/host-runner  ./cmd/host-runner
go build -o ~/mock-trainer ./cmd/mock-trainer
```

Three self-contained binaries. Only `hub-server` and `host-runner`
matter if you're a worker-side operator — `mock-trainer` is your
stand-in for a real training process.

### B.3 Save hub coords

```bash
export HUB=https://hub.example.com
export TEAM=default
export HOST_TOKEN=<paste-host-token>
export PRINCIPAL_TOKEN=<paste-principal-or-owner-token>
```

The two tokens have different roles:
- `HOST_TOKEN` (kind=host) — used by `host-runner` to register
  and heartbeat. Cannot create projects or runs.
- `PRINCIPAL_TOKEN` (kind=owner or kind=user with role=principal)
  — used by `curl` below to create the project + run that the
  host-runner will poll.

### B.4 Start the host-runner inside tmux

The host-runner daemon registers this machine with the hub, heartbeats,
and polls for spawns and metric files. We point it at a directory where
`mock-trainer` will write the trackio SQLite file.

```bash
mkdir -p ~/trackio
tmux new -d -s hub "~/host-runner run \
  --hub '$HUB' --team '$TEAM' --token '$HOST_TOKEN' \
  --tmux-session hub \
  --trackio-dir ~/trackio"
tmux attach -t hub              # Ctrl-b d detaches; keep the runner alive
```

Look for a log line like:

```
INFO registered with hub host_id=host-abc…
INFO heartbeat ok
```

On the phone, open the **Hosts** tab. Your host should appear within
10 s with a green "online" pill. Note the **host_id** — you'll need it
for the run you're about to create.

```bash
# From the worker box (or any terminal with PRINCIPAL_TOKEN):
HOST_ID=$(curl -fsS -H "Authorization: Bearer $PRINCIPAL_TOKEN" \
  "$HUB/v1/teams/$TEAM/hosts" | jq -r '.[0].id')
echo "host_id = $HOST_ID"
```

### B.5 Create a project + run on the hub

```bash
# Create a project.
PROJECT_ID=$(curl -fsS -H "Authorization: Bearer $PRINCIPAL_TOKEN" \
  -H 'content-type: application/json' \
  -X POST "$HUB/v1/teams/$TEAM/projects" \
  -d '{"name":"mock-live","goal":"Dress-rehearsal the trackio pipeline."}' \
  | jq -r .id)
echo "project_id = $PROJECT_ID"

# Reserve a run row whose trackio_run_uri points at the file
# mock-trainer is about to write. The URI scheme must match:
#   trackio://<project>/<run_name>
RUN_URI="trackio://mock-live/run-1"
RUN_ID=$(curl -fsS -H "Authorization: Bearer $PRINCIPAL_TOKEN" \
  -H 'content-type: application/json' \
  -X POST "$HUB/v1/teams/$TEAM/runs" \
  -d "$(jq -n --arg pid "$PROJECT_ID" --arg host "$HOST_ID" --arg uri "$RUN_URI" \
        '{project_id:$pid, trackio_host_id:$host, trackio_run_uri:$uri, seed:42}')" \
  | jq -r .id)
echo "run_id = $RUN_ID"
```

At this point the host-runner's poller sees a run tagged with its
own `host_id` and a `trackio://mock-live/run-1` URI. It will try to
open `~/trackio/mock-live.db` every 20 s — the file doesn't exist
yet, so the series is empty and nothing gets digested. That's fine —
next step creates the file.

### B.6 Fire the mock trainer

```bash
# Writes 1000 points to ~/trackio/mock-live.db. --interval-ms 500
# makes it "live" (~8 minutes wall time) so you can watch the
# sparkline fill in on the phone; pass 0 for an instant file.
~/mock-trainer --vendor trackio --dir ~/trackio \
  --project mock-live --run run-1 \
  --size 384 --optimizer lion --iters 1000 --interval-ms 500
```

On the phone: **Projects → mock-live → tap the run**. Within
20 s the sparkline populates from the first digest. Every 20 s
afterwards host-runner polls, downsamples, and PUTs the updated
digest; the sparkline extends rightward in near-real-time.

When mock-trainer exits, flip the run to `completed` so the UI stops
showing it as running:

```bash
curl -fsS -H "Authorization: Bearer $PRINCIPAL_TOKEN" \
  -H 'content-type: application/json' \
  -X POST "$HUB/v1/teams/$TEAM/runs/$RUN_ID/complete" \
  -d '{"status":"completed"}'
```

### B.7 (Optional) Run the whole ablation sweep

Chain `mock-trainer` six times to mirror the seed-demo shape (3 sizes
× 2 optimizers), each with its own run row:

```bash
for size in 128 256 384; do
  for opt in adamw lion; do
    RUN_URI="trackio://mock-live/size${size}-${opt}"
    RUN_ID=$(curl -fsS -H "Authorization: Bearer $PRINCIPAL_TOKEN" \
      -H 'content-type: application/json' \
      -X POST "$HUB/v1/teams/$TEAM/runs" \
      -d "$(jq -n --arg pid "$PROJECT_ID" --arg host "$HOST_ID" --arg uri "$RUN_URI" \
            '{project_id:$pid, trackio_host_id:$host, trackio_run_uri:$uri, seed:42}')" \
      | jq -r .id)
    ~/mock-trainer --vendor trackio --dir ~/trackio \
      --project mock-live --run "size${size}-${opt}" \
      --size $size --optimizer $opt --iters 1000 --interval-ms 0
    curl -fsS -H "Authorization: Bearer $PRINCIPAL_TOKEN" \
      -H 'content-type: application/json' \
      -X POST "$HUB/v1/teams/$TEAM/runs/$RUN_ID/complete" \
      -d '{"status":"completed"}' >/dev/null
  done
done
```

Wait ~60 s for the poller to catch up, then browse the project on
the phone — six runs, six sparklines, the 384-embed + Lion curve
visibly lowest.

### B.8 (Optional) Swap to wandb

Same flow, different writer. The host-runner needs `--wandb-dir` in
addition to (or instead of) `--trackio-dir`:

```bash
# Restart host-runner with both poller paths enabled:
tmux kill-session -t hub
mkdir -p ~/wandb
tmux new -d -s hub "~/host-runner run \
  --hub '$HUB' --team '$TEAM' --token '$HOST_TOKEN' \
  --tmux-session hub \
  --trackio-dir ~/trackio --wandb-dir ~/wandb"

# Write a wandb-format history file. The URI scheme switches to wandb://.
RUN_URI="wandb://mock-live/run-wandb-1"
RUN_ID=$(curl -fsS -H "Authorization: Bearer $PRINCIPAL_TOKEN" \
  -H 'content-type: application/json' \
  -X POST "$HUB/v1/teams/$TEAM/runs" \
  -d "$(jq -n --arg pid "$PROJECT_ID" --arg host "$HOST_ID" --arg uri "$RUN_URI" \
        '{project_id:$pid, trackio_host_id:$host, trackio_run_uri:$uri, seed:42}')" \
  | jq -r .id)
~/mock-trainer --vendor wandb --dir ~/wandb \
  --project mock-live --run run-wandb-1 \
  --size 256 --optimizer adamw --iters 500 --interval-ms 200
```

Both pollers run side-by-side; runs are routed to the correct reader
by the URI scheme.

---

## Troubleshooting

**Host doesn't appear on the Hosts tab.** Check that host-runner
logged `registered with hub` — if not, `--token` or `--hub` is
wrong, or the hub is unreachable from this box. Try
`curl -fsS -H "Authorization: Bearer $HOST_TOKEN" "$HUB/v1/_info"`
— a `{"server_version":…}` response means connectivity + auth are
fine.

**Run shows up but sparkline never populates.** Most common causes:
1. `trackio_run_uri` doesn't match the file path. The URI
   `trackio://<project>/<run>` resolves to
   `<trackio-dir>/<project>.db` with rows where `run_name=<run>`.
   Spell-check both sides.
2. `trackio_host_id` doesn't match the registered host. The poller
   only looks at runs tagged with its own host_id. Re-fetch with
   `curl "$HUB/v1/teams/$TEAM/hosts"` and confirm.
3. host-runner wasn't started with `--trackio-dir` (or
   `--wandb-dir`). Check the process args.

**Mobile app can't reach the hub.** The app rejects self-signed TLS
via `dart:io.HttpClient`. Use Let's Encrypt on the hub (Track B in
`install-hub-server.md`) or a plain `http://` LAN URL over a trusted
network.

**Host keeps showing "offline" after a few minutes.** Heartbeat
interval is 10 s; after ~60 s without one the hub clamps status to
offline. Confirm the host-runner process is still running
(`tmux attach -t hub`).

---

## Next steps

Once the mock pipeline works end-to-end:
- Swap `mock-trainer` for a real training process that writes to
  the same `~/trackio/` directory. No host-runner changes needed.
- Spawn a steward agent on a separate host (VPS) via
  `install-hub-server.md` and let it drive project creation + run
  reservation via MCP — you won't need the `curl` recipes above
  once the steward has a token.
- Keep `seed-demo` in your toolbox for offline demos where no
  network / GPU is available.
