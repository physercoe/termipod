# Tutorial 01 — Author a project template

> **Type:** tutorial
> **Status:** Current (2026-05-05)
> **Audience:** new contributors who want a working template
> **Last verified vs code:** v1.0.351

**Goal.** Write a custom YAML *project template* from scratch,
register it with your hub, instantiate it from the mobile app, and
watch the steward act on it. ~45 minutes.

**You'll learn:**
- What a project template *is* (vs a project, vs an agent template).
- The minimum YAML to instantiate.
- How parameters flow from instantiation to the steward's prompt.
- Where templates live on disk + how user edits beat the bundled defaults.

**Prerequisites:**
- You completed [Tutorial 00](00-getting-started.md), or you have a
  hub running with a steward online and the mobile app connected.
- You have shell access to the hub's `dataRoot` (in the tutorial
  defaults: `~/hub-tut`).

---

## What you'll build

A template called **`triage-paper`**: a 1-phase project that asks
the steward to read a paper and produce a short triage memo. Two
parameters: `paper_url` and `length`.

Conceptually it's a tiny version of the bundled `write-memo`
template ([`hub/templates/projects/write-memo.yaml`](../../hub/templates/projects/write-memo.yaml)).
You're going to author your own from blank.

---

## Step 1 — Find the templates directory

The hub copies bundled templates into the data root on first init.
User edits beat bundled defaults — once your file exists, `init` will
not overwrite it.

```bash
ls ~/hub-tut/default/templates/projects/
# ablation-sweep.yaml  benchmark-comparison.yaml  reproduce-paper.yaml  write-memo.yaml
```

Those four are the bundled set. Your custom template will land
alongside them.

---

## Step 2 — Author the YAML

Create `~/hub-tut/default/templates/projects/triage-paper.yaml`:

```yaml
name: triage-paper
kind: goal
goal: |-
  Read the paper at {paper_url}. Produce a {length}-paragraph triage
  memo with three sections: Claims, Methods, Critique. Cite specific
  sections of the paper for each claim. Request review when done.
parameters:
  paper_url: ""
  length: short
on_create_template_id: agents.steward
overview_widget: task_milestone_list
```

Field by field:

- **`name`** — must match the filename (sans `.yaml`). The hub keys
  off this for instantiation.
- **`kind`** — `goal` (bounded; closes when done) vs `standing`
  (ongoing container). Triage is bounded, so `goal`.
- **`goal`** — the prompt the steward decomposes from. Reference
  parameters by name in `{braces}`; the hub substitutes at
  instantiation.
- **`parameters`** — declared up front with default values. Empty
  string means required; non-empty defaults are pre-filled in the UI.
- **`on_create_template_id`** — agent template to spawn when the
  project is created. `agents.steward` is the bundled general
  steward; for tutorials use that one.
- **`overview_widget`** — UI hint for the project detail page.
  `task_milestone_list` is the default for memo-shaped work.

> **Why YAML instead of JSON.** Templates are read by humans (you,
> reviewers) more often than by machines. YAML's multi-line strings
> + comments make the goal prompt easier to author and review.

---

## Step 3 — Reload the hub

Templates are loaded at hub start (and exposed via the templates API
on every read). Restart the hub to pick up your new file:

```bash
# In the hub's terminal: Ctrl-C, then:
/tmp/hub-server serve -listen 0.0.0.0:8443 -data ~/hub-tut
```

Verify your template is visible to the API:

```bash
curl -fsS -H "Authorization: Bearer <owner-token>" \
  http://127.0.0.1:8443/v1/teams/default/templates | \
  python3 -c "import json,sys; d=json.load(sys.stdin);
  print('\n'.join(t['name'] for t in d['items'] if t['category']=='projects'))"
```

You should see `triage-paper` in the list.

---

## Step 4 — Instantiate from mobile

Open the app → bottom tab **Projects** → top-right **+** → "New from
template".

The picker shows the four bundled templates plus **`triage-paper`** —
your new one. Tap it.

The instantiation sheet renders the parameters you declared:

- **`paper_url`** — required; paste any URL (we'll use
  `https://arxiv.org/abs/1706.03762` — the "Attention Is All You
  Need" paper).
- **`length`** — pre-filled with `short`.

Tap **Create project**.

The hub:
1. inserts a `projects` row with `template_id='triage-paper'` and
   `parameters_json={paper_url:..., length:short}`.
2. emits `audit_events {action:'project.create'}`.
3. since `on_create_template_id='agents.steward'` is set, also
   triggers a steward spawn (or reuses the existing team steward).
4. the steward sees the new project and reads the parameter-
   substituted goal.

You land on the **Project detail** screen. Overview shows the goal
text with the substituted URL.

---

## Step 5 — Watch the steward act

Switch to the project's **Channel** tab (or via the Steward FAB on
Me). The steward should see the new project and post a message
acknowledging it. Depending on engine and prompt:

```
@steward: I see a new triage-paper project for
  https://arxiv.org/abs/1706.03762. I'll fetch the paper, draft the
  three-section triage, and request review. Working...
```

What happened under the hood:

```
mobile → POST /projects {template_id, parameters_json}
hub    → insert project row + emit audit
hub    → fire on_create steward (idempotent — reuses the existing one)
hub    → steward reads /projects/{id} via MCP
steward → drafts memo, posts to project channel, creates document,
          requests review
```

You can interrupt at any point. The steward's tool calls land in the
project's audit feed.

---

## Step 6 — Re-instantiate to confirm parameter substitution

Create a *second* `triage-paper` project — same template, different
URL. The steward picks it up and runs through the same shape, but
with the new substitution.

This is the load-bearing claim of templates: **one recipe, many
bound instances**. Each instance is a `projects` row with a different
`parameters_json`, and the goal prompt is materialised at create
time, not stored once and shared.

---

## What you just learned

```
~/hub-tut/default/templates/projects/triage-paper.yaml   ← author here
                            │
                            │ (read at hub start + per-API-read)
                            ▼
hub: GET /v1/teams/default/templates                     ← visible to mobile
                            │
                            │ (instantiation)
                            ▼
mobile → POST /projects {template_id, parameters_json}    ← bind values
                            │
                            ▼
hub: projects row, parameters_json materialised in goal   ← live project
                            │
                            ▼
steward (via on_create_template_id) reads + acts           ← work happens
```

Templates are how methodology travels: a lab's "reproduce a paper"
recipe, an ops team's "deploy + smoke-test" recipe. The bundled set
seeds `seed-demo` and the research demo; your custom set is yours.

---

## Cleanup

```bash
# Remove your template
rm ~/hub-tut/default/templates/projects/triage-paper.yaml

# Restart the hub to drop it from the API
# (existing projects keep working; they have parameters_json captured
#  at create time)
```

---

## What's next

- [Tutorial 02 — Build a worker agent](02-build-a-worker-agent.md) —
  custom worker that the steward spawns to do bounded work.
- [`../reference/template-yaml-schema.md`](../reference/template-yaml-schema.md)
  — full YAML schema for project + agent + step templates (when you
  outgrow this tutorial's minimal shape).
- [`hub/templates/projects/`](../../hub/templates/projects/) — the
  bundled set; read these as worked examples.

---

## Cross-references

- [`00-getting-started.md`](00-getting-started.md) — prerequisite
- [`../reference/template-yaml-schema.md`](../reference/template-yaml-schema.md)
  — full template schema
- [`../reference/data-model.md`](../reference/data-model.md) §1 —
  Projects primitive
- [`../reference/data-model.md`](../reference/data-model.md) §3 —
  Schedules primitive (when you graduate to scheduled instantiation)
- [`../spine/blueprint.md`](../spine/blueprint.md) — axioms templates
  follow from
