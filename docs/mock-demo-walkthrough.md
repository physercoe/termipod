# Mock Demo Walkthrough (no GPU)

Step-by-step for a reviewer who wants to see the research-demo path
(project → runs → sparklines → briefing → review → inbox) light up
without running nanoGPT on a real GPU.

**Assumptions**
- Fresh Ubuntu 22.04 or 24.04 box with sudo access.
- A **hub URL** and a **bearer token** — either because you own the
  hub (see `hub-mobile-test.md` to stand one up) or because a hub
  operator minted a `kind=host` (and optionally `kind=user`) token
  for you.
- A TermiPod APK on your phone (≥ `v1.0.170-alpha`).
- The hub itself is already running somewhere. Standing up a hub is
  out of scope here — see `hub-mobile-test.md`.

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
# in hub-mobile-test.md):
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
— nothing written").

On the phone: open TermiPod → **Inbox** tab. The seeded decision
("Approve nightly sweep budget…") shows up as an attention item. Tap
through → **Hub** tab → **Projects** → `ablation-sweep-demo` → tap
each of the 6 runs to see the synthetic loss sparkline (bigger embed
+ Lion converges lowest).

This covers the *review* surface. It does not exercise the host-runner
poller — those `run_metrics` rows were written directly by seed-demo.
For the live-pipeline rehearsal, continue with Path B below.

---

## Path B — Worker pipeline (mock-trainer + host-runner)

You are on a fresh Ubuntu box. You have a hub URL (e.g.
`https://hub.example.com`) and a **host token** (from your hub
operator — issued with `hub-server tokens issue -kind host …`; see
`hub-mobile-test.md` §3). You also want a **principal** (human) token
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

On the phone, open **Hub → Hosts** tab. Your host should appear within
10 s with a green "online" pill. Note the **host_id** — you'll need it
for the run you're about to create.

```bash
# From the worker box (or any terminal with PRINCIPAL_TOKEN):
HOST_ID=$(curl -fsS -H "Authorization: Bearer $PRINCIPAL_TOKEN" \
  "$HUB/v1/teams/$TEAM/hosts" | jq -r '.hosts[0].id')
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

On the phone: **Hub → Projects → mock-live → tap the run**. Within
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
`hub-mobile-test.md`) or a plain `http://` LAN URL over a trusted
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
  `hub-mobile-test.md` and let it drive project creation + run
  reservation via MCP — you won't need the `curl` recipes above
  once the steward has a token.
- Keep `seed-demo` in your toolbox for offline demos where no
  network / GPU is available.
