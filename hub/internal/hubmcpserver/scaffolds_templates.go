// scaffolds_templates.go — server-side skeletons for
// `templates.{agent,prompt,plan}.scaffold`. Returns a clean YAML or
// Markdown body the steward can fill in and PUT back via the
// matching `*.create` tool.
//
// Why a tool instead of "just read the bundled template": the bundled
// templates carry persona-specific fields (`coder.v1`'s `display_label`,
// the `skills` list, the `prompt:` reference) that an agent copying
// blindly would inherit inappropriately. Scaffolds are stripped of
// persona and replaced with `<placeholder>` markers so the agent
// fills in the parts that are theirs to author and leaves the
// schema-mandated parts as-is.
//
// Scaffold versioning: when the bundled-template schema changes, this
// file changes too. The scaffolds are the canonical "minimal valid"
// shape, kept in lockstep by review during template-schema evolution
// — see `docs/decisions/agent-template-schema.md` if/when one lands.
package hubmcpserver

import (
	"encoding/json"
	"fmt"
)

// ---------------------------------------------------------------------
// Tool definitions
// ---------------------------------------------------------------------

// scaffoldToolFor returns the `templates.<cat>.scaffold` tool for one
// category. Wired into templateToolDefs() in tools_templates.go.
func scaffoldToolFor(toolPrefix, dirName, ext string) toolDef {
	return toolDef{
		Name: "templates." + toolPrefix + ".scaffold",
		Description: "Return a clean " + toolPrefix + " template skeleton with " +
			"placeholder values + schema-mandated fields populated. The agent " +
			"customises in-place, then writes back via `templates." + toolPrefix +
			".create(name=<name>" + ext + ", content=<filled skeleton>)`. Args " +
			"select the variant when one category has multiple shapes (agent: " +
			"`kind=worker|steward`; plan: `phases=N`). The returned `content` " +
			"is the full file body; do not concatenate or partially fill — " +
			"the schema's required fields are all present in the skeleton.",
		InputSchema: scaffoldInputSchema(toolPrefix),
		call: func(c *hubClient, args map[string]any) (any, error) {
			_ = c // server-side data; no REST hop.
			content, err := scaffoldContent(toolPrefix, args)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"category":        dirName,
				"suggested_name":  scaffoldSuggestedName(toolPrefix, args, ext),
				"content":         content,
			}, nil
		},
	}
}

func scaffoldInputSchema(toolPrefix string) json.RawMessage {
	switch toolPrefix {
	case "agent":
		return schema(`{"type":"object","properties":{"kind":{"type":"string","enum":["worker","steward"],"description":"steward = elevated MCP surface (allow_all role); worker = bounded role from roles.yaml worker.allow"},"engine":{"type":"string","enum":["claude-code","codex","gemini-cli","kimi-code"],"description":"backend engine (default claude-code). Picks the cmd line + permission flags."}}}`)
	case "prompt":
		return schema(`{"type":"object","properties":{"kind":{"type":"string","enum":["worker","steward"],"description":"shapes the section headings + opening line"}}}`)
	case "plan":
		return schema(`{"type":"object","properties":{"phases":{"type":"integer","minimum":1,"maximum":10,"description":"number of phase blocks to scaffold (default 5)"}}}`)
	}
	return schema(`{"type":"object","properties":{}}`)
}

func scaffoldSuggestedName(toolPrefix string, args map[string]any, ext string) string {
	switch toolPrefix {
	case "agent":
		k, _ := args["kind"].(string)
		switch k {
		case "steward":
			return "steward.<your-domain>.v1" + ext
		default:
			return "<your-handle-base>.v1" + ext
		}
	case "prompt":
		k, _ := args["kind"].(string)
		switch k {
		case "steward":
			return "steward.<your-domain>.v1" + ext
		default:
			return "<your-handle-base>.v1" + ext
		}
	case "plan":
		return "<your-plan-id>.v1" + ext
	}
	return "<name>" + ext
}

// ---------------------------------------------------------------------
// Skeleton content
// ---------------------------------------------------------------------

func scaffoldContent(toolPrefix string, args map[string]any) (string, error) {
	switch toolPrefix {
	case "agent":
		k, _ := args["kind"].(string)
		engine, _ := args["engine"].(string)
		if engine == "" {
			engine = "claude-code"
		}
		if k == "steward" {
			return agentStewardScaffold(engine), nil
		}
		return agentWorkerScaffold(engine), nil
	case "prompt":
		k, _ := args["kind"].(string)
		if k == "steward" {
			return promptStewardScaffold, nil
		}
		return promptWorkerScaffold, nil
	case "plan":
		phases := 5
		if p, ok := args["phases"].(float64); ok && int(p) > 0 {
			phases = int(p)
		}
		return planScaffold(phases), nil
	}
	return "", fmt.Errorf("scaffold: unknown category %q", toolPrefix)
}

// engineCmd picks the cmd line + permission_modes block by engine. The
// bundled steward.<engine>.v1.yaml templates are the source of truth.
func engineCmd(engine string) string {
	switch engine {
	case "codex":
		return `  # Codex picks the model server-side via app-server protocol.
  # No permission_modes map: codex's per-tool gate is in-stream
  # (item/*/requestApproval), not flag-time.
  cmd: "codex app-server"`
	case "gemini-cli":
		return `  # Gemini's --acp flag puts it into Agent Client Protocol mode
  # (Zed spec, JSON-RPC over stdio); ACPDriver speaks the rest.
  cmd: "gemini --acp"`
	case "kimi-code":
		return `  # Kimi's acp subcommand mirrors gemini's protocol surface.
  # --yolo bypasses the engine-layer gate; the hub's permission_prompt
  # MCP tool still wraps tool calls (ADR-026). Switch to the explicit
  # gate by removing --yolo.
  cmd: "kimi --yolo acp"`
	default: // claude-code
		return `  permission_modes:
    skip: "--dangerously-skip-permissions"
    prompt: "--permission-prompt-tool mcp__{{mcp_namespace}}__permission_prompt"
  cmd: "claude --model {{model}} --print --output-format stream-json --input-format stream-json --verbose {{permission_flag}}"`
	}
}

func agentWorkerScaffold(engine string) string {
	model := `  model: claude-opus-4-7
`
	if engine != "claude-code" {
		model = "" // codex/gemini/kimi pick model out-of-band
	}
	return `# <one-line purpose>. Author with ` + "`templates.agent.create`" + `; the
# prompt body lives separately under
# ` + "`templates.prompt.create(name=\"<your-handle-base>.v1.md\")`" + `.

template: agents.<your-handle-base>
version: 1
extends: null

# Workers default to M2 (structured stdio) per ADR-025 W6 so the
# steward overlay can render their turns and gate tool use. M4 fallback
# covers hosts without the MCP gateway port.
driving_mode: M2
fallback_modes: [M4]

backend:
  kind: ` + engine + `
` + model + `  # default_workdir intentionally omitted — the launcher derives
  # ~/hub-work/<pid8>/<handle> from project_id + handle so per-project
  # workers stay isolated on shared hosts.
` + engineCmd(engine) + `

default_role: worker.<your-role>
display_label: "<Display label>"

# Workers have a bounded MCP surface — see roles.yaml worker.allow for
# the canonical pattern set. Add capabilities your agent actually uses;
# any worker.allow entry is fair game.
default_capabilities:
  - documents.create
  - documents.read
  - documents.list
  - channels.post_event
  - attention.create     # request_help / request_select / request_approval
  - a2a.invoke           # parent-steward target only (ADR-016 D4)
  - tasks.create
  - tasks.update
  - journal_append
  - journal_read
  - get_attention
  - get_event
  - search
  - decision.vote: minor
  - spawn.descendants: 0    # workers don't multiply

prompt: <your-handle-base>.v1.md

# A2A skills advertised on the agent card. One entry per skill the
# parent steward can invoke via a2a.invoke(handle=..., text=...).
skills:
  - id: <skill-id>
    name: <skill-name>
    description: "<what this skill does>"
    tags: [<tag1>, <tag2>]

default_channels:
  hub-meta: read
`
}

func agentStewardScaffold(engine string) string {
	model := `  model: claude-opus-4-7
`
	if engine != "claude-code" {
		model = ""
	}
	return `# Domain-steward template. One of these is materialised per project
# per ADR-025; the general steward delegates project work to the
# project's steward. Author with ` + "`templates.agent.create`" + `.

template: agents.steward.<your-domain>
version: 1
extends: null

driving_mode: M2
fallback_modes: [M4]

backend:
  kind: ` + engine + `
` + model + `  # default_workdir intentionally omitted — auto-derives
  # ~/hub-work/<pid8>/<handle> from the spawn so per-project stewards
  # stay isolated on shared hosts. Override only when you need a
  # stable shared scratch path for this domain (mirrors how
  # steward.general.v1 / steward.infra.v1 use ~/hub-work/<persona>).
` + engineCmd(engine) + `

default_role: team.coordinator
display_label: "<Domain> steward"

# Stewards get the elevated capability surface: spawn workers, propose
# templates, vote on significant decisions, create projects.
default_capabilities:
  - blob.read
  - blob.write
  - delegate
  - decision.vote: significant
  - spawn.descendants: 20
  - templates.read
  - templates.propose
  - tasks.create
  - tasks.assign_others
  - projects.create

prompt: steward.<your-domain>.v1.md

# A2A skills surfaced on the steward's card. The general steward
# discovers domain stewards by these skills.
skills:
  - id: <skill-id>
    name: <skill-name>
    description: "<what this skill does>"
    tags: [<tag>]

default_channels:
  hub-meta: full
`
}

const promptWorkerScaffold = `# <Worker display name>

You are ` + "`{{handle}}`" + `, a worker on project ` + "`{{project_id}}`" + `. Spawned by
the project steward to do <one-sentence-purpose>.

## What you do

<one paragraph describing the worker's primary responsibility, the
inputs it expects from the steward, and the outputs it produces>

## Tools you'll reach for

- ` + "`Bash` / `Edit` / `Read` / `Write`" + ` — your engine-native surface.
- ` + "`documents.create(kind=...)`" + ` — write your output as a deliverable.
- ` + "`a2a.invoke(handle=<parent-steward>, text=...)`" + ` — report back when
  done; ask for clarification when blocked.
- ` + "`request_help(question=..., mode=clarify)`" + ` — when the principal
  needs to weigh in.

See your spawn spec for the full MCP surface.

## Behaviour

- <safety guardrail 1: e.g. "install only from PyPI signed maintainers
  / official apt repos / official binary releases — no curl-pipe-bash">
- <safety guardrail 2>
- Stay inside your worktree. Don't wander up the filesystem.
- Spawn-descendants is 0 — you can't multiply yourself. If you need
  more parallelism, ` + "`request_help`" + ` and let the steward fan out.

## When you're done

` + "`reports.post(status=success|partial|failed, summary_md=..., output_artifacts=[<doc_id>])`" + `.
The steward's ` + "`agents.gather`" + ` long-poll wakes on this event.
`

const promptStewardScaffold = `# <Domain> Steward

You are ` + "`{{handle}}`" + `, the steward for project ` + "`{{project_id}}`" + `. Per
ADR-025 you are the project's accountability anchor — every project-
bound spawn flows through you, and you own the project's plan.

## What you do

<one paragraph describing the domain's specific workflow: what worker
templates you spawn, what phases the project moves through, what
deliverables land at each phase>

## Workers you spawn

- ` + "`<worker-handle>.v1`" + ` — <one-line purpose>
- ` + "`<another-worker>.v1`" + ` — <one-line purpose>

Spawn via ` + "`agents.spawn(kind=<worker>.v1, child_handle=@<handle>, project_id={{project_id}}, spawn_spec_yaml=<load template>)`" + `.

## Tools you have

- ` + "`agents.spawn`" + ` (project-bound only — the gate enforces D3).
- ` + "`a2a.invoke`" + ` to your workers; ` + "`agents.gather`" + ` to wait on a fan-out.
- ` + "`templates.{agent,prompt,plan}.{list,get,create,update}`" + ` if you
  need to author a new worker template mid-project.
- ` + "`documents.create`" + `, ` + "`reviews.create`" + `, ` + "`runs.register`" + ` for project
  artifacts.
- ` + "`request_approval`" + ` / ` + "`request_select`" + ` / ` + "`request_help`" + ` to escalate
  to the principal.

## Phase walk

1. <Phase 1: scope, deliverable, exit criterion>
2. <Phase 2: ...>
3. ...

## When in doubt

- Tempted to spawn for a sibling project → can't (ADR-025 D3). Send
  an A2A to that project's steward instead, or escalate via
  ` + "`request_help`" + `.
- Stuck on the principal's intent → ` + "`request_help`" + ` (mode=clarify).
- Need to author a template → ` + "`templates.<cat>.list`" + ` + ` + "`templates.<cat>.get`" + `
  on the closest existing one as a scaffold, then ` + "`templates.<cat>.create`" + `.
`

func planScaffold(phases int) string {
	out := `# Plan template. Authored with ` + "`templates.plan.create`" + `; instantiated
# via ` + "`plans.create(template_id=..., parameters_json={...})`" + `.

template: plans.<your-plan-id>
version: 1

# Phases are the principal's mental model — each one is a checkpoint
# they can review. Keep them goal-oriented (what success looks like)
# not task-oriented (which workers run). The steward decomposes phases
# into worker spawns at runtime.
phases:
`
	for i := 1; i <= phases; i++ {
		out += fmt.Sprintf(`  - id: phase-%d
    name: "<phase %d name>"
    goal: "<what success looks like>"
    deliverables:
      - kind: document
        name: "<deliverable-name>"
`, i, i)
	}
	return out
}
