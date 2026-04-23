# Research-Demo Candidates

MVP design doc for blueprint §9 P4.3. Three candidate demos, all honoring
the fixed constraints: multi-host (GPU host + VPS, steward on VPS, worker on
GPU), natural-language goal in, steward writes the plan, AI-for-science loop
(code -> run -> curves -> report), review on phone.

Sources cited inline. This is a survey; no code changes.

---

## 1. Prior-art scan

| System | Loop (concrete) | Reusable? | Status | Shape fit | Hardware |
|---|---|---|---|---|---|
| **Sakana AI Scientist v1** | Template-bound: idea -> code edit -> run -> plot -> LaTeX -> auto-review. Per-domain template required. [repo](https://github.com/SakanaAI/AI-Scientist) | Apache-2.0 | Active 2025 | Full loop; template-tight | 1 GPU; cheap small |
| **Sakana AI Scientist v2** | Template-free **agentic tree search** w/ experiment-manager agent; branches code patches, prunes by val metric; writes LaTeX. One paper passed ICLR'25 ICBINB peer review. [arxiv](https://arxiv.org/abs/2504.08066) / [repo](https://github.com/SakanaAI/AI-Scientist-v2) | Apache-2.0 | Active; Nature pub | Full loop; costly | Hours 1x GPU + heavy API |
| **Weco AIDE** | Tree-search over code: each script a node, LLM patches spawn children, metric prunes. NL in, submission out. 4x medal rate of next-best on MLE-bench. [repo](https://github.com/WecoAI/aideml) / [report](https://www.weco.ai/blog/technical-report) | MIT | Active; Weco-backed | code->run->metric; no report | 1 GPU; 24h |
| **MLE-bench (OpenAI)** | Benchmark: 75 Kaggle comps, 24h / 36 CPU / 440 GB / 1x A10. Best: AIDE+o1-preview 16.9% medals. [repo](https://github.com/openai/mle-bench) / [arxiv](https://arxiv.org/abs/2410.07095) | Apache-2.0 | Active; ICLR'25 | Agent-pluggable | 1x A10 |
| **MLAgentBench** | 13 ML-research tasks, ReAct agent reads/writes files + runs code. Best Claude 3 Opus 37.5%. [repo](https://github.com/snap-stanford/MLAgentBench) / [arxiv](https://arxiv.org/abs/2310.03302) | MIT | Maintained | code->run->metric + loose report | Modest GPU |
| **Agent Laboratory** | 3 phases: Lit Review -> Experimentation -> Report Writing; specialized sub-agents use arxiv / HF / Python / LaTeX. AgentRxiv (3/25) shares agent preprints. [repo](https://github.com/SamuelSchmidgall/AgentLaboratory) / [arxiv](https://arxiv.org/abs/2501.04227) | MIT | Active | Strong fit: lit+expt+report | CPU-reasonable |
| **Stanford STORM / Co-STORM** | Topic -> multi-perspective Q&A + outline -> citation-backed article. No code exec. [repo](https://github.com/stanford-oval/storm) | MIT | Active | Write-up only | CPU + web |
| **PaperBench (OpenAI)** | Benchmark: replicate 20 ICML'24 papers; 8316 graded sub-tasks. Best Claude 3.5 Sonnet + open scaffold 21.0%. [arxiv](https://arxiv.org/abs/2504.01848) | Apache-2.0 harness | Active; ICML'25 | Full reproduction; costly | Multi-hour GPU/paper |
| **Claude Skills** | Filesystem capabilities: dir w/ `SKILL.md` frontmatter + scripts; auto-activated. [docs](https://platform.claude.com/docs/en/agents-and-tools/agent-skills/overview) / [repo](https://github.com/anthropics/skills) | MIT | GA late 2025 | Packaging format, not a loop | N/A |
| **OpenAI Deep Research** | "search+read+reason" while-loop -> cited long report. Clones: [langchain-ai/open_deep_research](https://github.com/langchain-ai/open_deep_research), Jina node-DeepResearch, qx-labs/agents-deep-research. | OAI closed; clones MIT | Active | Write-up only | CPU+search |
| **LangGraph Supervisor** | Hierarchical supervisor->worker Python lib (2/25). Canonical Researcher/Analyst/Writer/Supervisor templates. [repo](https://github.com/langchain-ai/langgraph-supervisor-py) | MIT | Active | Steward+fleet skeleton | Runtime-agnostic |
| **HKUDS AI-Researcher** | NeurIPS'25 "idea -> paper" pipeline. [repo](https://github.com/HKUDS/AI-Researcher). License unverified. | ? | Active 2025 | Full loop | Unverified |
| **Trackio** | wandb-compatible (`import trackio as wandb`) local-first tracker from HF; self-hostable HTTP. [repo](https://github.com/gradio-app/trackio) / [docs](https://huggingface.co/docs/trackio/index) | Apache-2.0 | Active 2025 | Metrics substrate (§6.5, P3.1) | CPU |
| **AG-UI** | Typed SSE events for agent UIs (TEXT_MESSAGE_CONTENT, TOOL_CALL_*, STATE_DELTA, HITL). [repo](https://github.com/ag-ui-protocol/ag-ui/) / [docs](https://docs.ag-ui.com/introduction) | MIT | Active; CopilotKit v1.50 native | Our mobile wire (§5.5) | N/A |
| **A2A** | Google/LF; agent-cards + task lifecycle over HTTPS/gRPC; v0.3 Jul'25. [repo](https://github.com/a2aproject/A2A) / [spec](https://a2a-protocol.org/latest/specification/) | Apache-2.0 | Active | Our cross-host wire (§5.4) | N/A |

OpenAI Swarm / Operator are closed and not reusable for self-hosted
fleets; referenced only as conceptual prior art.

---

## 2. Loop-shape extraction

Five primitives recur across the prior art:

1. **Decompose** — NL goal -> plan (AgentLab phases, LangGraph supervisor
   delegation, AIDE root-node, STORM outline).
2. **Generate code / artifact** — LLM emits a patch or full script
   (AIDE nodes, AI Scientist v2 tree-search branches, PaperBench).
3. **Execute in a sandbox** — run the script, capture stdout + metrics
   (all ML-engineering agents, always 1 GPU, 1 container).
4. **Log + evaluate** — metrics logged to a tracker (wandb / trackio);
   parent agent reads curves and scores the node. This is the critic /
   prune step in tree search.
5. **Write up** — synthesize a document (STORM, Sakana, AgentLab phase
   3, Deep Research). Often separated into draft -> review -> revise.

**Map to termipod:**

| Primitive | Termipod mechanism (shipped) |
|---|---|
| Decompose | steward agent (`agents/steward.v1.yaml`) + `plan.instantiate` MCP tool + `plan_steps.kind=agent_spawn` / `llm_call` / `human_decision`. |
| Generate code | `agent_spawn` plan-step spawning an M2 (Claude Code stdio) worker on the GPU host, with worktree + pane. |
| Execute | `shell` plan-step, or the worker runs `python train.py` in its ACP session; host-runner owns the pane (§3.2). |
| Log + eval | `runs` table + `runs.trackio_run_uri`; worker calls trackio; steward reads the URI. |
| Write up | `documents` table + `reviews`; briefing agent via scheduled plan (§6.10). |

**Gaps today** (i.e., not yet built; cross-referenced with
`docs/research-demo-gaps.md`):

- **G1** No seeded project templates (`is_template=1`) — gap is known and
  already tracked. Would need the four built-in templates + their plan
  outlines (size: medium).
- **G2** Steward prompt in `hub/templates/prompts/steward.v1.md` is
  generic; lacks the decompose recipe with concrete MCP tool calls
  (size: small).
- **G3** Briefing agent template (`briefing.v1.yaml` + prompt + schedule)
  not seeded (size: small-medium).
- **G4** Host-runner does not poll trackio HTTP for metrics; blueprint
  P3.1. Curves cannot flow back to the phone without this (size:
  medium).
- **G5** Cross-host A2A (P3.2–3.4) is the single largest unshipped block
  in the blueprint: A2A server on host-runner, hub A2A directory, hub
  reverse-tunnel relay, plus AG-UI surfacing of `a2a.invoke` /
  `a2a.response` events on both agents' streams (size: large).
- **G6** Steward → worker A2A delegation pattern (how the steward on VPS
  issues a "train this config" A2A task to the GPU-host worker, receives
  the run URI back). Not drafted in templates (size: medium).
- **G7** Worker agent template (`ml-worker.v1.yaml`) with the
  GPU-sandbox + trackio wiring. Does not exist (size: small-medium).
- **G8** Mobile "run detail" sparkline card reading from trackio URI
  (blueprint P2.3). Present as stub but not wired to trackio HTTP
  (size: small-medium).

All other primitives map.

---

## 3. Three concrete demo candidates

All three run on the **same two-host topology**:

- **VPS** (CPU only, public IP, hosts the hub): runs host-runner `vps`
  + the **steward** agent (M2 Claude Code stdio). Steward's job is
  decomposition, planning, briefing. No training, no heavy compute.
- **GPU host** (RTX 3090 or A100, behind NAT ok): runs host-runner
  `gpu` + the **worker** agent(s) (M2 Claude Code stdio) in worktrees
  under `~/hub-work/`. Trackio server runs here.
- **A2A wire**: `steward(vps) -> worker(gpu).train(config)` via
  host-runner A2A endpoints, hub-relayed through the reverse tunnel
  (§5.4). `a2a.invoke` / `a2a.response` events appear inline in the
  steward's AG-UI stream on the phone.

### Candidate A — "Scaling-law mini-sweep on nanoGPT"

**One-paragraph pitch.** "I told my phone 'compare two optimizers on
nanoGPT at three model sizes and tell me which scales better'. The
steward on my VPS wrote a 6-step plan I approved with one tap. My GPU
box ran six quick training runs. I got pushed a 3-paragraph briefing
with a loss-vs-steps plot per optimizer while I made coffee. Two taps
to approve. It read like a tiny paper section."

**User walkthrough:**
- **0s** Tap `+ New Project` -> pick **ablation-sweep** template ->
  type goal: *"AdamW vs Lion on nanoGPT Shakespeare, model sizes
  {128, 256, 384}, 1000 iters each, which scales better?"*. Tap Create.
- **30s** Steward card lights up on phone; AG-UI stream shows: "Drafting
  plan..." -> plan-viewer shows 6 phases: `fetch_repo`, `make_worktree`,
  `generate_configs`, `a2a.delegate(worker.train x6)`, `collect_metrics`,
  `brief`. Approve.
- **2min** First A2A invoke fires: `a2a.invoke` event on steward stream
  ("target: worker@gpu.train, config: adamw-128"). Runs tab shows 6
  runs registered, first one `running`, trackio URIs attached.
- **10min** All 6 runs complete. Sparkline cards stream live.
- **End (~12min)** Briefing doc posted; review card in Me tab;
  3 paragraphs + 1 plot; Approve -> done.

**Fleet topology:**
- steward(vps) -> `plan.instantiate` -> `plan.advance`.
- steward(vps) -> A2A -> worker(gpu) x6 (sequenced, since one GPU).
  Each task: `{repo: karpathy/nanoGPT, config: {...}, trackio_run_id}`.
- worker(gpu) runs `python train.py config/xxx.py`, logs to trackio,
  returns `{status, trackio_run_uri, final_val_loss}`.
- steward(vps) reads all URIs, writes document, requests review.

**Data + model.** Input: NL goal + char-Shakespeare dataset (bundled
with nanoGPT). Output: doc + 6 metric URIs. **Real** on GPU: the train
runs at n_embd=128, 4 layers, 1000 iters are ~60-90s each per literature
(nanoGPT speedrun corpus). **Mockable** for smoke: worker just sleeps +
writes synthetic loss curves to trackio (5-min path).

**Reusable skill borrow.**
- [karpathy/nanoGPT](https://github.com/karpathy/nanoGPT) (MIT) as the
  code the worker clones and edits. Tiny, fast, well-understood.
- [gradio-app/trackio](https://github.com/gradio-app/trackio) as the
  metric substrate; worker does `import trackio as wandb`.
- AIDE-inspired "config patch" style for config generation, but we do
  *not* import AIDE — steward just writes config files. (We could
  adopt AIDE later as a skill for the generate-patch step.)

**Infra needs.**
- VPS: Go hub, host-runner, Claude Code binary, Node (for ACP libs).
- GPU: host-runner, Claude Code, Python >= 3.10, torch+CUDA, trackio
  (`pip install trackio`), nanoGPT repo cloned once and seeded.
- Hub A2A directory + reverse-tunnel relay (G5).

**Risks & unknowns.**
- Sequential GPU queueing: if the user expects six in parallel they'll
  be disappointed; UI must show queue state clearly.
- Worker stability on first real train — OOM, driver issues. Mockable
  path protects the demo.
- Briefing-plot generation: need matplotlib in briefing agent env; or
  keep doc text-only + link to trackio dashboard.

### Candidate B — "Reproduce one figure from a paper"

**Pitch.** "I gave my phone an arXiv link and said 'reproduce figure 3'.
Overnight the steward pulled the repo, wrote a run config, kicked off
training on my GPU, and pushed a reproduction memo with my generated
curve next to the paper's curve. I approved it over breakfast."

**User walkthrough:**
- **0s** New Project -> **reproduce-paper** template -> goal: *"Repro
  figure 3 of arXiv:XXXX"* + paste arXiv URL. Approve the 8-phase plan.
- **30s** Steward clones the paper's referenced repo on the GPU host.
  Lit-review phase (AgentLab-style) summarizes the method into a memo.
- **2min** Human-gated checkpoint: "does this config look right?"
  posted as an approval. One tap.
- **10min** Worker training. Live loss curve streams.
- **End** Repro memo doc in Me tab: paper's reported number vs ours,
  delta, caveats. Review + approve.

**Fleet topology.**
- steward(vps) -> `shell` plan-step on gpu host (via host-runner RPC)
  to `git clone`.
- steward(vps) -> A2A -> worker(gpu).reproduce(fig_idx).
- worker(gpu) -> trackio; optional `a2a.invoke` back to steward for
  mid-run questions ("train set path ambiguous?"), surfacing the
  blueprint's bilateral-multi-turn A2A (§5.4).

**Data + model.** A small known-repro-able paper (e.g., a tiny
language-modeling or MNIST-adjacent result). Figure bytes on GPU,
final numbers only on hub. **Mockable:** canned "reproduction" that
re-plots paper's original CSV + injects small noise, 5 min.

**Reusable skill borrow.**
- [SakanaAI/AI-Scientist-v2](https://github.com/SakanaAI/AI-Scientist-v2)
  — fork the "experiment manager" prompt and its tree-search heuristic
  (Apache-2.0).
- [PaperBench harness](https://arxiv.org/abs/2504.01848) for rubric
  style.
- AgentLab's literature-review phase as a Claude Skill bundle.
- [stanford-oval/storm](https://github.com/stanford-oval/storm) for the
  memo's citation-backed write-up sub-phase.

**Infra needs.** Same as A, plus: a pre-selected small paper + repo
cached on the GPU host so we don't live-clone a 2 GB dataset in the
demo; arXiv / HF tool MCP for the lit-review phase.

**Risks.**
- Highest loop-length; most places to break on stage. Paper repo might
  require non-obvious deps.
- Figure-comparison requires a matching style; easy to look worse
  than it is.
- Claim accuracy: the demo must clearly label "not a full reproduction,
  one figure, one seed."

### Candidate C — "Overnight survey: optimizer literature + tiny empirical check"

**Pitch.** "Before bed I told my phone 'write me a 1-page primer on
second-order optimizers, and include a quick empirical check on
cifar-10 MLP, AdamW vs a Lion variant'. I woke up to a primer, a
sanity-check loss curve, and an author bio for the paper I most
needed to read. Approved in three taps."

**User walkthrough:**
- **0s** New Project -> **write-memo** template with *empirical-check*
  toggle -> goal: *"one-page primer on second-order optimizers with a
  tiny cifar-10 MLP check"*.
- **30s-2min** Steward decomposes into: lit-review (STORM-style) on
  VPS, tiny train on GPU, synthesize.
- **Overnight** Scheduled briefing collates into a single doc.
- **Morning** Review in Me tab. Approve.

**Fleet topology.**
- steward(vps) drives a STORM-style lit phase entirely on the VPS (pure
  CPU + web).
- steward(vps) -> A2A -> worker(gpu).train(tiny_mlp, 2 configs).
- briefing(vps) runs on cron at 06:00, reads both outputs, emits doc.

**Data + model.** cifar-10 torchvision MLP, ~500 steps, 2 configs. Real
on GPU but tiny enough that laptop-grade GPU suffices. Lit content is
web-retrieved with source URLs, tracked.

**Reusable skill borrow.**
- [stanford-oval/storm](https://github.com/stanford-oval/storm)
  (MIT) as a bundled **Claude Skill** for the lit-review phase;
  package `SKILL.md` that calls `knowledge-storm` via a shell step.
- torchvision for the dataset; trackio for logging.
- [langchain-ai/open_deep_research](https://github.com/langchain-ai/open_deep_research)
  (MIT) as the NL survey scaffold if STORM feels too heavyweight.

**Infra needs.** Same as A + web-search tool (MCP bridge to
Brave/DDG) + Python `pip install knowledge-storm`.

**Risks.**
- Web search adds a new MCP surface area.
- "Overnight" framing is not testable in a 20-min slot; the live demo
  compresses it to 10 min but loses the "wake up to a report" beat.
- STORM write is slow and LLM-bill-heavy; budget caps matter.

---

## 4. Recommendation

**Go with Candidate A ("scaling-law mini-sweep on nanoGPT").**

Rationale:

- **Smallest surface.** nanoGPT is ~300 lines; no dataset plumbing (char
  shakespeare bundles), no search tool, no LaTeX. Everything that can
  break is local to one repo we know.
- **Most faithful to the control plane.** It exercises every blueprint
  edge: mobile -> hub REST, hub -> host-runner (both), host-runner ->
  agent (M2 stdio, both steward and worker), A2A cross-host with six
  `a2a.invoke` events visibly streaming to the phone, MCP (plan
  tools, trackio URI attach), AG-UI (plan-viewer, run sparklines,
  review card), scheduled/manual plans, documents + reviews. No
  blueprint edge is skipped.
- **Most forgiving.** Six small runs degrade to five if one dies; the
  briefing still has material. A 5-min all-mocked smoke version has
  the exact same UI traces.
- **Best prior-art leverage.** nanoGPT + trackio are both thin, MIT,
  drop-in. No forking, no license homework.
- **Impressive without overclaiming.** An ablation sweep with a
  generated micro-paper is a real scientist-style output without
  needing the "reproduction" claim which is famously hard (PaperBench
  best-agent score: 21%).

Candidates B and C are later demos once A is the stable smoke.

**Smoke (5 min, all mocked):** worker is a stub that sleeps 10s per
"run" and writes a synthetic power-law loss curve into trackio. All
other wire is real: steward plan, A2A calls, AG-UI events, briefing
doc, review. Demonstrates the whole control plane without a GPU in
the room — bookable for developer loops and flaky-network demos.

**Full (~12-15 min, real):** GPU host actually runs six
nanoGPT-shakespeare trains with n_embd in {128, 256, 384} x
{AdamW, Lion}, 1000 iters each. trackio serves live metrics over
HTTP. Everything else identical.

---

## 5. Implementation gaps vs shipped code

Cross-checked against `hub/internal/server/`, `hub/internal/hostrunner/`,
`hub/migrations/`, `hub/templates/`, and `lib/`.

### Hub-side Go

- **Cross-host A2A** (P3.2-3.4): A2A directory table + handlers, reverse-
  tunnel relay. Not present (`internal/server/` has no a2a* files).
  **Large.**
- **AG-UI `a2a.invoke` / `a2a.response` event kinds**: surfacing of A2A
  call events on both agents' streams (§5.4 end). Current `agent_events`
  plumbing exists; need two new kinds + broker mapping. **Small.**
- **Seed built-in project templates** (G1) in `init.go`. **Small.**

### Host-runner Go

- **A2A server** binding agent-cards per live agent (P3.2). Not present
  (no a2a* files under `internal/hostrunner/`). **Medium-large.**
- **Trackio HTTP poller** (P3.1 / G4): periodic GET against configured
  endpoint per run; emit `run.attach_metric_uri` + cache last-N points
  for sparkline card. **Medium.**
- **Per-worker worktree + dataset-cache convention** for the
  nanoGPT demo (`~/hub-work/<project>/run-<id>/`). Partially there —
  `worktree.go` exists; needs demo-specific bootstrap. **Small.**

### Mobile Flutter

- **Sparkline card** wired to trackio URIs (P2.3 / G8). Stub exists;
  needs HTTP fetch + minimal plot. **Small-medium.**
- **A2A event rendering** on `AgentFeed` (invoke / response chips,
  task-id threading). **Small.**
- **Project-template picker** pointed at `listProjects(isTemplate=1)`
  (already called out in `research-demo-gaps.md`). **Small.**

### Templates / skills / prompts

- **`agents/ml-worker.v1.yaml` + `prompts/ml-worker.v1.md`** (G7): M2
  Claude Code, cwd under worktree, trackio + torch env, A2A server
  publishes `train(config)` capability. **Small-medium.**
- **`agents/briefing.v1.yaml` + prompt** (G3). **Small.**
- **`projects/ablation-sweep.template.yaml`** (the project-template row,
  not the YAML template file): parameters are model_sizes, optimizers,
  iters. **Small.**
- **Steward decompose-recipe prompt** (G2): concrete plan outline for
  ablation-sweep with `plan.instantiate` -> six `a2a.invoke` steps ->
  briefing. **Small.**
- Optional: package [STORM](https://github.com/stanford-oval/storm)
  and a paper-lit-review skill as Claude Skills under
  `hub/templates/skills/` for Candidate B/C later. **Medium.**

### External infra

- GPU host: Python 3.10+, torch+CUDA, `pip install trackio`, nanoGPT
  cloned once under a cached path, Claude Code binary, Node runtime.
  **Small** (docs-only; no code).
- VPS: hub already there; add `knowledge-storm` only if we ever ship
  Candidate C.
- Trackio server: `trackio server --port 7860` running under systemd
  on the GPU host, reachable from host-runner on localhost. **Small.**
- Model weights: none (char-shakespeare trains from scratch).
- Datasets: none beyond nanoGPT's bundled `shakespeare_char/prepare.py`.

**Total to ship Candidate A end-to-end:** roughly **1 large** (hub A2A
directory + relay) **+ 1 medium-large** (host-runner A2A server) **+ a
handful of smalls** (trackio poller, templates, mobile polish). The
biggest cost is blueprint P3.2-3.4, which the product owner has already
flagged as required because "multi-host is required." No blueprint
amendments needed.
