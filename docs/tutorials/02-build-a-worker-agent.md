# Tutorial 02 — Build a worker agent

> **Type:** tutorial
> **Status:** Current (2026-05-05)
> **Audience:** new contributors who want a working worker template
> **Last verified vs code:** v1.0.351

**Goal.** Write a custom *worker agent* template, have the steward
spawn it for a specific task, and observe the worker's transcript on
mobile. ~45 minutes.

**You'll learn:**
- What a worker agent *is* (vs a steward, per the manager/IC
  invariant).
- The minimum YAML to define one.
- How a steward decides when to spawn it.
- Where the steward ↔ worker handoff is — and why this split
  matters.

**Prerequisites:**
- You completed [Tutorial 00](00-getting-started.md) and
  [Tutorial 01](01-author-a-project-template.md), or have an
  equivalent setup: hub + steward + a project.

---

## What you'll build

A worker template called **`fact-checker`**: takes a memo as input,
extracts every claim, verifies each claim against a configured set
of references, and writes a notes document back. The steward spawns
it once per memo; the worker exits when it reports back.

This is a generic worker shape — bounded, single-task, exits when
done. You can use it as a starting point for any IC-class agent
(coder, plotter, summariser, …).

---

## Step 1 — Recall the manager / IC invariant

Before writing YAML: stewards plan and orchestrate; workers do the IC
work. From [`../spine/agent-lifecycle.md`](../spine/agent-lifecycle.md)
§4.9:

| Steward | Worker |
|---|---|
| Reads goals, decomposes plans | Reads its single bounded task |
| Spawns workers, requests reviews | Writes code / runs evals / drafts docs |
| Surfaces decisions to the director | Reports back on completion |
| Persistent or project-scoped | Task-scoped; archives at completion |

Workers do not author templates, do not spawn peers (other than the
single descendant they're explicitly granted), and do not arbitrate
approvals. That's the steward's job.

If your worker template starts to look like it's deciding things
beyond its task, redirect it to a steward.

---

## Step 2 — Author the agent YAML

Create `~/hub-tut/default/templates/agents/fact-checker.v1.yaml`:

```yaml
template: agents.fact-checker
version: 1

backend:
  kind: claude-code
  model: claude-opus-4-7
  default_workdir: ~/hub-work/fact-checker
  permission_modes:
    prompt: "--permission-prompt-tool mcp__{{mcp_namespace}}__permission_prompt"
  cmd: "claude --model {{model}} --print --output-format stream-json --input-format stream-json --verbose {{permission_flag}}"

default_role: worker.fact-checker
display_label: "Fact-checker"

default_capabilities:
  - documents.create
  - documents.read
  - documents.list
  - channels.post_event
  - attention.create        # request help when blocked
  - a2a.invoke              # report back to steward
  - journal_append
  - journal_read
  - get_event
  - search
  - spawn.descendants: 0    # no further spawning

prompt: fact-checker.v1.md

skills:
  - id: extract-claims
    name: extract-claims
    description: "Pull discrete factual claims from a memo"
    tags: [analysis, writing]
  - id: verify-claim
    name: verify-claim
    description: "Cross-check a claim against configured references"
    tags: [analysis, research]
  - id: write-notes
    name: write-notes
    description: "Compile a per-claim notes document"
    tags: [writing]
```

Field-by-field highlights:

- **`template: agents.fact-checker`** — the symbolic name. The
  steward references it via `template_id='agents.fact-checker'`.
- **`backend`** — the engine wrap. Claude Code via `stream-json`
  (M2). `{{model}}` and `{{permission_flag}}` are template variables
  the host-runner substitutes at spawn.
- **`default_role: worker.fact-checker`** — used by the
  `roles.yaml` manifest ([ADR-016](../decisions/016-subagent-scope-manifest.md))
  to gate which `hub://*` tools this worker can call.
- **`default_capabilities`** — the operation-scope manifest. List
  the MCP tools the worker is *allowed* to call. `spawn.descendants:
  0` is the load-bearing line that prevents this worker from
  spawning peers.

---

## Step 3 — Author the prompt

Workers receive a prompt template that gives them their identity,
the manager/IC invariant, and skill-specific guidance. Create
`~/hub-tut/default/templates/prompts/fact-checker.v1.md`:

```markdown
You are a **fact-checker** worker on the termipod platform.

## Role

You are an IC-class worker, not a steward. Your job is bounded:
extract claims from one memo, verify each against the references your
steward provided, and write a notes document back. You do not plan,
spawn, or arbitrate.

## Inputs

You will receive:
- `memo_document_id` — the memo to fact-check.
- `references` — a list of URIs / artifact paths the steward has
  pre-vetted as ground truth.

## Process

1. Read the memo via `documents.read(memo_document_id)`.
2. Extract every discrete factual claim. A claim is something that
   could be true or false.
3. For each claim, search the references for confirming or
   contradicting evidence.
4. Compile a notes document with this shape:
   ```
   ## Claim 1: <quote>
   - **Status**: confirmed | contradicted | unsupported
   - **Evidence**: <citation or "not found">
   - **Note**: <one line of context>
   ```
5. Save the notes via `documents.create(...)`.
6. Report back to your steward via `a2a.invoke` with the new
   document id.

## Constraints

- Do **not** add new claims of your own.
- Do **not** spawn peer workers.
- If a reference is unreachable or ambiguous, mark the claim as
  `unsupported` and note the reason.
- If you find more than 30 claims, summarise the top 30 by
  centrality and explicitly say so in your reply.
```

The prompt is loaded by the host-runner at spawn time and prepended
to the agent's session. Substitution placeholders (`{{model}}`,
parameter names) work the same way as in templates.

---

## Step 4 — Reload the hub

```bash
# In the hub's terminal: Ctrl-C, then:
/tmp/hub-server serve -listen 0.0.0.0:8443 -data ~/hub-tut
```

Verify the template loaded:

```bash
curl -fsS -H "Authorization: Bearer <owner-token>" \
  http://127.0.0.1:8443/v1/teams/default/templates | \
  python3 -c "import json,sys; d=json.load(sys.stdin);
  print('\n'.join(t['name'] for t in d['items'] if t['category']=='agents'))"
```

You should see `fact-checker.v1` (alongside the bundled templates).

---

## Step 5 — Have the steward spawn it

In a triage-paper project from [Tutorial 01](01-author-a-project-template.md),
ask the steward (via the Steward session) to fact-check the memo it
wrote:

```
You: now fact-check the memo you just wrote. Use these references:
- the original paper at <paper_url>
- whatever exists in the project's #refs channel
```

The steward should:
1. Read the memo it just produced.
2. Decide a fact-checker is appropriate (vs doing it itself —
   manager/IC invariant).
3. Invoke `agents.spawn` with `template_id='agents.fact-checker'`
   and pass the memo document id + references.
4. The spawn lands on host-runner, opens a new tmux pane, launches
   `claude` with the worker prompt.

On mobile: switch to the project's **Agents** pill. You should see a
new agent row labeled `Fact-checker` with `status='running'`. Tap it
to see the worker's live transcript.

---

## Step 6 — Watch the worker

The fact-checker reads the memo, extracts claims, verifies each
(with whatever references it can reach), writes a notes document
back, and posts an `a2a.response` to the steward.

Then it terminates. The agents row flips to `status='terminated'`,
then `'archived'` after the host-runner reaps.

> **What you should *not* see.** The fact-checker spawning peer
> workers. Authoring new templates. Editing the steward's memo.
> Approving its own work. If any of these happen, the prompt or
> capability list is too permissive — tighten and re-spawn.

---

## What you just built

```
project triage-paper
   │
   ├── steward (general / project-scoped) -- plans, decomposes
   │       │
   │       └── spawn agents.fact-checker -- one task, exits
   │              │
   │              ├── reads memo
   │              ├── extracts claims
   │              ├── verifies each
   │              └── writes notes doc + a2a response
```

The split is what makes this auditable. The director's review surface
is the steward's session; the worker's tool noise stays in its own
agent-events stream, off the steward's surface.

---

## Where to go next

- Add a `default_workdir` git worktree to make the worker's work
  reproducible.
- Add `runs.register` capability and have the worker register an
  experiment row (becomes useful when you graduate from text fact-
  checking to ML fact-checking).
- Build a domain steward template that bundles fact-checker, coder,
  and critic into a `research` workflow — at that point you're at
  the bundled `steward.research.v1.yaml` shape.

---

## Cleanup

```bash
# Remove your worker template + prompt
rm ~/hub-tut/default/templates/agents/fact-checker.v1.yaml
rm ~/hub-tut/default/templates/prompts/fact-checker.v1.md

# Existing spawned workers keep their journal in audit; new spawns
# of this template will fail until the file returns
```

---

## Cross-references

- [`00-getting-started.md`](00-getting-started.md) — prerequisite
- [`01-author-a-project-template.md`](01-author-a-project-template.md)
  — prerequisite
- [`../spine/agent-lifecycle.md`](../spine/agent-lifecycle.md) §4.9
  — manager/IC invariant in detail
- [`../reference/data-model.md`](../reference/data-model.md) §4 —
  Agents primitive
- [`../reference/template-yaml-schema.md`](../reference/template-yaml-schema.md)
  — full template schema
- [`../decisions/016-subagent-scope-manifest.md`](../decisions/016-subagent-scope-manifest.md)
  — operation-scope manifest + roles.yaml
- [`hub/templates/agents/coder.v1.yaml`](../../hub/templates/agents/coder.v1.yaml)
  — bundled worker as a worked example
