---
name: Agent-template naming spec
description: Names an agent template's filename pattern (`<basename>.v<N>.yaml`) and required internal `template:` field (`agents.<basename>`). Explains why the `agents.` prefix is load-bearing — string-only references inside `spawn_spec_yaml`, `on_create_template_id`, and predicates like `template.startsWith("agents.steward.")` need a self-describing namespace marker. Companion to `template-yaml-schema.md` (which covers project templates).
---

# Agent-template naming spec

> **Type:** reference
> **Status:** Current (2026-05-17)
> **Audience:** contributors · template authors · steward prompt authors
> **Last verified vs code:** v1.0.620-alpha

**TL;DR.** Agent templates live at `hub/templates/agents/`. Filename
pattern is `<basename>.v<N>.yaml`; the file's internal `template:`
field MUST equal `agents.<basename>`. The `agents.` prefix is the
category namespace — it lets string-only references identify a
template as an agent template without consulting the file path, which
matters because templates are referenced from `spawn_spec_yaml`,
project-template `on_create_template_id`, mobile icon-mapping
predicates, and steward-detection predicates like
`template.startsWith("agents.steward.")`. Both the hub startup audit
and `scripts/lint-templates.sh` enforce the rule; mismatched files
refuse hub start. This spec resolves the convention drift that the
v1.0.619 coder.v1 incident exposed.

---

## 1. The naming rule

```
hub/templates/agents/<basename>.v<N>.yaml
                  └─────┬─────┘ └┬┘
                        │        └── format version. Bump only when
                        │            the YAML schema is incompatible.
                        │            Most templates stay at v1.
                        │
                        └── basename. Lowercase, dot-separated for
                            multi-segment ids (e.g. steward.general,
                            steward.codex). Stable across format-
                            version bumps.
```

Inside the file:

```yaml
template: agents.<basename>      # required; MUST equal `agents.` + the
                                  # filename basename (without `.v<N>`)
version: <N>                      # the content version (not format)
backend:
  cmd: <command line>             # required; v1.0.620+ rejects empty
  kind: <engine-family>           # claude-code | codex | gemini-cli | …
  model: <model id>
  permission_modes: { … }
prompt: <prompt-file>.md          # sidecar in templates/prompts/
…
```

**Examples:**

| File | Internal `template:` |
|---|---|
| `coder.v1.yaml` | `agents.coder` |
| `briefing.v1.yaml` | `agents.briefing` |
| `lit-reviewer.v1.yaml` | `agents.lit-reviewer` |
| `steward.v1.yaml` | `agents.steward` |
| `steward.general.v1.yaml` | `agents.steward.general` |
| `steward.codex.v1.yaml` | `agents.steward.codex` |
| `steward.research.v1.yaml` | `agents.steward.research` |
| `ml-worker.v1.yaml` | `agents.ml-worker` |

---

## 2. Why the `agents.` prefix is load-bearing

Templates are referenced from contexts where the file path is not
available — only the string id survives:

1. **`spawn_spec_yaml`:** stewards write `template: agents.coder` to
   reference a template. Without the prefix, an unprefixed `coder`
   would be ambiguous with a project template named `coder`.
2. **`on_create_template_id` (in project templates):** the field
   names a steward kind to spawn on project creation, e.g.
   `on_create_template_id: agents.steward`. The receiver of this
   field has no way to tell agent from project from string alone
   without the prefix.
3. **Steward-detection predicates** in hub + mobile:
   - `template.startsWith("agents.steward.")` — used to distinguish
     domain stewards from workers
   - `lib/screens/team/template_icon.dart` case match `'agents.steward'`
     — picks the right icon without a runtime template lookup
   - `lib/services/steward_handle.dart` — documents the
     `agents.steward.<name>` convention as the steward signal
4. **Audit + log strings:** template ids appear in `audit_events.meta`,
   `agent_events.payload`, and prose log lines. The prefix lets a
   later reader (human or LLM) classify the row without joining back
   to the file system.

In short: the prefix encodes a category that string-only contexts
cannot recover from the directory layout. Removing it would force
every consumer to either consult the file system (impossible in many
contexts) or invent its own predicate (drift risk).

---

## 3. Relation to project templates

[`template-yaml-schema.md`](template-yaml-schema.md) covers project
templates, where the convention is unprefixed — file
`research.v1.yaml` has internal `template: research`. The difference
is intentional:

- **Project templates** are only ever referenced from inside
  contexts (database `projects.template_id`, mobile project picker)
  where the category is already known from surrounding scope. The
  unprefixed id is unambiguous.
- **Agent templates** are referenced from string-only contexts (see
  §2) where the category must be self-describing. The prefix carries
  that information.

The two conventions coexist because they solve different
disambiguation needs.

---

## 4. Enforcement

`hub/internal/server/template_audit.go` runs on hub start. It checks
every bundled `hub/templates/agents/*.yaml` against:

1. `template:` field is non-empty
2. `template:` matches `agents.<basename>` where `<basename>` is the
   filename without `.v<N>.yaml`
3. `backend.cmd` is non-empty

Any failure refuses hub start with a structured error naming the
offending file + the broken rule. CI runs the same audit via
`scripts/lint-templates.sh` so PRs that break the rule fail before
merge.

User-overlaid templates at `<dataRoot>/team/templates/agents/` are
NOT subject to the startup audit (operators are expected to verify
their own overrides; failing on a stale overlay could brick a
production hub). The lint script can be pointed at user overlays
manually if desired.

---

## 5. Authoring a new agent template — checklist

1. Pick the basename. Lowercase, hyphen-or-dot separated, no `.v<N>`
   suffix. (Examples above.)
2. Create `hub/templates/agents/<basename>.v1.yaml`.
3. Set the internal `template:` field to `agents.<basename>`.
4. Set `backend.cmd` to the full engine launch command, with
   `{{var}}` placeholders for `model`, `permission_flag`, etc.
5. Author the sidecar prompt at `hub/templates/prompts/<basename>.v1.md`
   referenced by the `prompt:` field.
6. Run `scripts/lint-templates.sh` locally; commit only when it
   passes.

---

## 6. Status

The `agents.` prefix convention pre-dates this spec (see commits
shipping `agents.steward.v1`, `agents.coder.v1`, etc. across the
0.x → 1.0.x range). The v1.0.621-alpha bundle formalises it
without renaming any existing template; this doc is the canonical
reference going forward.

---

## 7. References

- [`template-yaml-schema.md`](template-yaml-schema.md) — sibling spec for project templates.
- [`hub/internal/server/template_audit.go`](../../hub/internal/server/template_audit.go) — startup-time enforcement.
- [`scripts/lint-templates.sh`](../../scripts/lint-templates.sh) — CI enforcement.
- [`discussions/validate-at-every-boundary.md`](../discussions/validate-at-every-boundary.md) — the principle this enforcement instantiates.
