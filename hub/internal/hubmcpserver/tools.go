// tools.go — MCP tool catalog + dispatch table.
//
// Each tool is a thin adapter over a hub REST endpoint (see blueprint §5.2
// "The relay principle": MCP never bypasses the hub's authority — it is a
// protocol translation in front of the existing REST surface). We expose
// what P1.5 requires and nothing more; speculative tools are rejected at
// tools/call time rather than silently no-op'd so the client can see the
// schema boundary.
package hubmcpserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
)

// randID returns a short random hex id. Used for A2A message ids when the
// caller doesn't provide one — the A2A spec requires messageId to be unique
// per send, so we generate rather than leaving it to the MCP client.
func randID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// toolDef captures everything the MCP protocol surfaces for one tool:
// a name, a human description, a JSON-Schema for its input, plus the
// Go function that actually runs it.
type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	// call executes the tool. Returning (result, nil) produces a normal
	// MCP tool result frame; returning a non-nil error becomes an MCP
	// protocol error (which clients typically render as "tool failed").
	call func(c *hubClient, args map[string]any) (any, error) `json:"-"`
}

// schema is a tiny helper that emits a JSON object literal as a RawMessage;
// using plain json.RawMessage keeps this file grep-friendly without dragging
// in a schema-builder dependency.
func schema(s string) json.RawMessage { return json.RawMessage(s) }

// buildTools returns the fixed dispatch table. A fresh slice per call keeps
// the call-closures hermetic — callers always see a client injected via `c`
// rather than a captured package-level global.
func buildTools() []toolDef {
	tools := []toolDef{
		{
			Name:        "projects.list",
			Description: "List projects in the configured team. Optional `kind` filters to 'goal' or 'standing'.",
			InputSchema: schema(`{"type":"object","properties":{"kind":{"type":"string","enum":["goal","standing"]}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				q := url.Values{}
				if k, ok := args["kind"].(string); ok && k != "" {
					q.Set("kind", k)
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/projects"), q, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name: "projects.create",
			Description: "Create a new project OR a reusable project template. Requires `name` and `kind` ('goal' = finite end-state / 'standing' = ongoing). " +
				"Two distinct authoring paths: " +
				"(1) CONCRETE project — omit `is_template`. Optionally pass `template_id` to instantiate from an existing project template (copies its `parameters_json` shape + binds `on_create_template_id` for auto-attached plans). " +
				"(2) PROJECT TEMPLATE — pass `is_template: true`. The project becomes a row other concrete projects can name via their `template_id` field. Carry the per-domain shape in `parameters_json` (intent params), `goal` (intent statement template), `config_yaml` (phase declarations + acceptance criteria scaffolds), and `on_create_template_id` (auto-bound plan template). " +
				"Project templates are distinct from plan templates (`templates.plan.create`) — plans are YAML scaffolds stored on disk that attach to a project's plans table; project templates are full project rows that other projects clone from. Use `templates.plan.scaffold` to author the plan; `projects.create({is_template: true, on_create_template_id: <plan-template-id>})` to bundle the project template that auto-attaches it.",
			InputSchema: schema(`{"type":"object","required":["name","kind"],"properties":{"name":{"type":"string"},"kind":{"type":"string","enum":["goal","standing"]},"goal":{"type":"string","description":"Intent statement (for concrete) or intent template (for is_template=true)."},"docs_root":{"type":"string"},"config_yaml":{"type":"string","description":"Phase declarations + acceptance criteria scaffolds. Inspected by the project chassis for is_template=true rows."},"parent_project_id":{"type":"string","description":"Sub-project parent (max depth 2)."},"template_id":{"type":"string","description":"Source project template for instantiation. Mutually exclusive with is_template=true in practice."},"is_template":{"type":"boolean","description":"true = this row is a reusable project template; other projects name it via template_id. Default false."},"parameters_json":{"type":"object","description":"For is_template=true: the parameter shape projects-of-this-kind accept. For concrete: the bound values."},"on_create_template_id":{"type":"string","description":"Plan template id auto-attached when a concrete project is instantiated from this template."},"budget_cents":{"type":"integer"},"steward_agent_id":{"type":"string","description":"Bind a specific steward agent to this project (validated)."},"policy_overrides_json":{"type":"object"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				if _, ok := args["name"].(string); !ok {
					return nil, fmt.Errorf("name is required")
				}
				var out json.RawMessage
				if err := c.do("POST", c.teamPath("/projects"), nil, args, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "projects.get",
			Description: "Fetch one project by id.",
			InputSchema: schema(`{"type":"object","required":["project"],"properties":{"project":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				id, _ := args["project"].(string)
				if id == "" {
					return nil, fmt.Errorf("project is required")
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/projects/"+url.PathEscape(id)), nil, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "plans.list",
			Description: "List plans. Optional `project` filters to one project.",
			InputSchema: schema(`{"type":"object","properties":{"project":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				q := url.Values{}
				if p, ok := args["project"].(string); ok && p != "" {
					q.Set("project", p)
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/plans"), q, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "plans.create",
			Description: "Create a plan scaffold. Requires `project` and `title`.",
			InputSchema: schema(`{"type":"object","required":["project","title"],"properties":{"project":{"type":"string"},"title":{"type":"string"},"description":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				var out json.RawMessage
				if err := c.do("POST", c.teamPath("/plans"), nil, args, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "plans.get",
			Description: "Fetch one plan by id.",
			InputSchema: schema(`{"type":"object","required":["plan"],"properties":{"plan":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				id, _ := args["plan"].(string)
				if id == "" {
					return nil, fmt.Errorf("plan is required")
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/plans/"+url.PathEscape(id)), nil, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "plans.steps.create",
			Description: "Append a step to a plan. Requires `plan`, `phase_idx`, `step_idx`, `kind` (one of agent_spawn|llm_call|shell|mcp_call|human_decision). Optional `spec_json` object holds kind-specific params.",
			InputSchema: schema(`{"type":"object","required":["plan","phase_idx","step_idx","kind"],"properties":{"plan":{"type":"string"},"phase_idx":{"type":"integer","minimum":0},"step_idx":{"type":"integer","minimum":0},"kind":{"type":"string","enum":["agent_spawn","llm_call","shell","mcp_call","human_decision"]},"spec_json":{"type":"object"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				plan, _ := args["plan"].(string)
				if plan == "" {
					return nil, fmt.Errorf("plan is required")
				}
				body := map[string]any{}
				for _, k := range []string{"phase_idx", "step_idx", "kind", "spec_json"} {
					if v, ok := args[k]; ok {
						body[k] = v
					}
				}
				var out json.RawMessage
				if err := c.do("POST", c.teamPath("/plans/"+url.PathEscape(plan)+"/steps"), nil, body, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "plans.steps.list",
			Description: "List all steps for a plan, ordered by phase_idx, step_idx.",
			InputSchema: schema(`{"type":"object","required":["plan"],"properties":{"plan":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				plan, _ := args["plan"].(string)
				if plan == "" {
					return nil, fmt.Errorf("plan is required")
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/plans/"+url.PathEscape(plan)+"/steps"), nil, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "plans.steps.update",
			Description: "Patch one step of a plan. Requires `plan` and `step`. Any of status, started_at, completed_at, input_refs_json, output_refs_json, agent_id may be supplied.",
			InputSchema: schema(`{"type":"object","required":["plan","step"],"properties":{"plan":{"type":"string"},"step":{"type":"string"},"status":{"type":"string"},"started_at":{"type":"string"},"completed_at":{"type":"string"},"input_refs_json":{"type":"array"},"output_refs_json":{"type":"array"},"agent_id":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				plan, _ := args["plan"].(string)
				step, _ := args["step"].(string)
				if plan == "" || step == "" {
					return nil, fmt.Errorf("plan and step are required")
				}
				body := map[string]any{}
				for k, v := range args {
					if k == "plan" || k == "step" {
						continue
					}
					body[k] = v
				}
				if len(body) == 0 {
					return nil, fmt.Errorf("at least one field to update is required")
				}
				// PATCH returns 204; decode nothing.
				if err := c.do("PATCH", c.teamPath("/plans/"+url.PathEscape(plan)+"/steps/"+url.PathEscape(step)), nil, body, nil); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "plan": plan, "step": step}, nil
			},
		},
		{
			Name:        "runs.list",
			Description: "List runs in the team. Optional `project` filter (runs can cross projects via parent_run_id).",
			InputSchema: schema(`{"type":"object","properties":{"project":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				q := url.Values{}
				if p, ok := args["project"].(string); ok && p != "" {
					q.Set("project", p)
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/runs"), q, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "runs.get",
			Description: "Fetch one run by id.",
			InputSchema: schema(`{"type":"object","required":["run"],"properties":{"run":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				id, _ := args["run"].(string)
				if id == "" {
					return nil, fmt.Errorf("run is required")
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/runs/"+url.PathEscape(id)), nil, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "documents.list",
			Description: "List documents. Optional `project` filters by project.",
			InputSchema: schema(`{"type":"object","properties":{"project":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				q := url.Values{}
				if p, ok := args["project"].(string); ok && p != "" {
					q.Set("project", p)
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/documents"), q, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "documents.create",
			Description: "Create a new document. Body is passed through; see hub docs for field list (typically: project, kind, title, body).",
			InputSchema: schema(`{"type":"object","properties":{"project":{"type":"string"},"kind":{"type":"string"},"title":{"type":"string"},"body":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				var out json.RawMessage
				if err := c.do("POST", c.teamPath("/documents"), nil, args, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "reviews.list",
			Description: "List reviews. Optional `project` filter.",
			InputSchema: schema(`{"type":"object","properties":{"project":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				q := url.Values{}
				if p, ok := args["project"].(string); ok && p != "" {
					q.Set("project", p)
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/reviews"), q, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "reviews.create",
			Description: "Create a new review request. Body is passed through; typical fields include project, document_id, reviewer, question.",
			InputSchema: schema(`{"type":"object","properties":{"project":{"type":"string"},"document_id":{"type":"string"},"reviewer":{"type":"string"},"question":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				var out json.RawMessage
				if err := c.do("POST", c.teamPath("/reviews"), nil, args, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "policy.read",
			Description: "Read the team policy document. STUB in P1.5: returns an empty-rules placeholder while the real policy engine is designed; the underlying /policy endpoint is still proxied so callers can observe the raw hub response for now.",
			InputSchema: schema(`{"type":"object"}`),
			call: func(c *hubClient, _ map[string]any) (any, error) {
				// Best-effort: return whatever the hub serves, but wrap it so
				// a client can tell this is the stubbed version. If the hub
				// call errors we still return the placeholder, because the
				// P1.5 contract promises a non-failing read.
				var raw json.RawMessage
				_ = c.do("GET", c.teamPath("/policy"), nil, nil, &raw)
				return map[string]any{
					"stub":  true,
					"rules": []any{},
					"raw":   raw,
				}, nil
			},
		},
		{
			Name:        "runs.create",
			Description: "Create a new run row. Requires `project_id`. Optional: agent_id, config_json (object), seed, parent_run_id, trackio_host_id, trackio_run_uri.",
			InputSchema: schema(`{"type":"object","required":["project_id"],"properties":{"project_id":{"type":"string"},"agent_id":{"type":"string"},"config_json":{"type":"object"},"seed":{"type":"integer"},"parent_run_id":{"type":"string"},"trackio_host_id":{"type":"string"},"trackio_run_uri":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				if p, _ := args["project_id"].(string); p == "" {
					return nil, fmt.Errorf("project_id is required")
				}
				var out json.RawMessage
				if err := c.do("POST", c.teamPath("/runs"), nil, args, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "runs.attach_artifact",
			Description: "Attach a content-addressed artifact (checkpoint, eval_curve, log, dataset, report, figure, sample) to a run. Requires `run`, `project_id`, `kind`, `name`, `uri`. Optional: sha256, size, mime, producer_agent_id, lineage_json (object).",
			InputSchema: schema(`{"type":"object","required":["run","project_id","kind","name","uri"],"properties":{"run":{"type":"string"},"project_id":{"type":"string"},"kind":{"type":"string"},"name":{"type":"string"},"uri":{"type":"string"},"sha256":{"type":"string"},"size":{"type":"integer"},"mime":{"type":"string"},"producer_agent_id":{"type":"string"},"lineage_json":{"type":"object"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				runID, _ := args["run"].(string)
				if runID == "" {
					return nil, fmt.Errorf("run is required")
				}
				// Forward to the generic artifact create endpoint with run_id set.
				body := make(map[string]any, len(args))
				for k, v := range args {
					if k == "run" {
						continue
					}
					body[k] = v
				}
				body["run_id"] = runID
				var out json.RawMessage
				if err := c.do("POST", c.teamPath("/artifacts"), nil, body, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "artifacts.list",
			Description: "List artifacts in the team. Optional filters: `project`, `run`, `kind`. Newest first.",
			InputSchema: schema(`{"type":"object","properties":{"project":{"type":"string"},"run":{"type":"string"},"kind":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				q := url.Values{}
				for _, k := range []string{"project", "run", "kind"} {
					if v, _ := args[k].(string); v != "" {
						q.Set(k, v)
					}
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/artifacts"), q, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "artifacts.get",
			Description: "Fetch one artifact by id.",
			InputSchema: schema(`{"type":"object","required":["artifact"],"properties":{"artifact":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				id, _ := args["artifact"].(string)
				if id == "" {
					return nil, fmt.Errorf("artifact is required")
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/artifacts/"+url.PathEscape(id)), nil, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "artifacts.create",
			Description: "Create a standalone artifact (not tied to a run). For run outputs prefer `runs.attach_artifact`. Requires `project_id`, `kind`, `name`, `uri`. Optional: sha256, size, mime, producer_agent_id, lineage_json (object).",
			InputSchema: schema(`{"type":"object","required":["project_id","kind","name","uri"],"properties":{"project_id":{"type":"string"},"kind":{"type":"string"},"name":{"type":"string"},"uri":{"type":"string"},"sha256":{"type":"string"},"size":{"type":"integer"},"mime":{"type":"string"},"producer_agent_id":{"type":"string"},"lineage_json":{"type":"object"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				for _, k := range []string{"project_id", "kind", "name", "uri"} {
					if v, _ := args[k].(string); v == "" {
						return nil, fmt.Errorf("%s is required", k)
					}
				}
				var out json.RawMessage
				if err := c.do("POST", c.teamPath("/artifacts"), nil, args, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "agents.spawn",
			Description: "Spawn a child agent. Requires `child_handle`, `kind`, `spawn_spec_yaml`. Optional: host_id, parent_agent_id, worktree_path, budget_cents, mode, project_id (binds the agent to a project per ADR-025; the YAML `project_id:` is the canonical site, this body field is a fallback). May return 202 + attention_id if policy gates the spawn on approval. Project-bound spawns require the caller to be that project's steward (ADR-025 W9); the general steward must delegate via `request_project_steward`.",
			InputSchema: schema(`{"type":"object","required":["child_handle","kind","spawn_spec_yaml"],"properties":{"child_handle":{"type":"string"},"kind":{"type":"string"},"spawn_spec_yaml":{"type":"string"},"host_id":{"type":"string"},"parent_agent_id":{"type":"string"},"worktree_path":{"type":"string"},"budget_cents":{"type":"integer"},"mode":{"type":"string"},"project_id":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				for _, k := range []string{"child_handle", "kind", "spawn_spec_yaml"} {
					if v, _ := args[k].(string); v == "" {
						return nil, fmt.Errorf("%s is required", k)
					}
				}
				var out json.RawMessage
				if err := c.do("POST", c.teamPath("/agents/spawn"), nil, args, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "channels.post_event",
			Description: "Post an event to a channel (the hub's chat/message surface). Requires `channel` and `type`. Optional `project` — when omitted the event targets a team-scope channel. Body-pass-through fields: parts (array), from_id, to_ids, task_id, correlation_id, metadata (object).",
			InputSchema: schema(`{"type":"object","required":["channel","type"],"properties":{"channel":{"type":"string"},"project":{"type":"string"},"type":{"type":"string"},"parts":{"type":"array"},"from_id":{"type":"string"},"to_ids":{"type":"array","items":{"type":"string"}},"task_id":{"type":"string"},"correlation_id":{"type":"string"},"metadata":{"type":"object"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				channel, _ := args["channel"].(string)
				if channel == "" {
					return nil, fmt.Errorf("channel is required")
				}
				if t, _ := args["type"].(string); t == "" {
					return nil, fmt.Errorf("type is required")
				}
				var path string
				if p, _ := args["project"].(string); p != "" {
					path = c.teamPath("/projects/" + url.PathEscape(p) + "/channels/" + url.PathEscape(channel) + "/events")
				} else {
					path = c.teamPath("/channels/" + url.PathEscape(channel) + "/events")
				}
				// Strip routing-only fields from the body so the hub sees a clean event payload.
				body := make(map[string]any, len(args))
				for k, v := range args {
					if k == "channel" || k == "project" {
						continue
					}
					body[k] = v
				}
				var out json.RawMessage
				if err := c.do("POST", path, nil, body, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "a2a.invoke",
			Description: "Send an A2A message to another agent by handle. Looks up the agent card in the team directory, then POSTs a JSON-RPC `message/send` envelope to the card's relay URL. Returns the JSON-RPC response envelope (typically a Task).",
			InputSchema: schema(`{"type":"object","required":["handle","text"],"properties":{"handle":{"type":"string","description":"target agent handle (e.g. \"worker.ml\")"},"text":{"type":"string","description":"message body as plain text"},"task_id":{"type":"string","description":"optional existing task id to continue"},"message_id":{"type":"string","description":"optional message id; auto-generated when omitted"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				handle, _ := args["handle"].(string)
				text, _ := args["text"].(string)
				if handle == "" || text == "" {
					return nil, fmt.Errorf("handle and text are required")
				}
				q := url.Values{}
				q.Set("handle", handle)
				var cards []struct {
					HostID  string          `json:"host_id"`
					AgentID string          `json:"agent_id"`
					Handle  string          `json:"handle"`
					Card    json.RawMessage `json:"card"`
				}
				if err := c.do("GET", c.teamPath("/a2a/cards"), q, nil, &cards); err != nil {
					return nil, fmt.Errorf("lookup card: %w", err)
				}
				if len(cards) == 0 {
					return nil, fmt.Errorf("no A2A agent found for handle %q", handle)
				}
				var card map[string]any
				if err := json.Unmarshal(cards[0].Card, &card); err != nil {
					return nil, fmt.Errorf("malformed card: %w", err)
				}
				relayURL, _ := card["url"].(string)
				if relayURL == "" {
					return nil, fmt.Errorf("card missing url field")
				}
				msgID, _ := args["message_id"].(string)
				if msgID == "" {
					msgID = "mcp-" + randID()
				}
				msg := map[string]any{
					"messageId": msgID,
					"role":      "user",
					"parts":     []map[string]any{{"kind": "text", "text": text}},
				}
				params := map[string]any{"message": msg}
				if tid, ok := args["task_id"].(string); ok && tid != "" {
					params["taskId"] = tid
				}
				env := map[string]any{
					"jsonrpc": "2.0",
					"id":      msgID,
					"method":  "message/send",
					"params":  params,
				}
				var resp json.RawMessage
				if err := c.doAbsolute("POST", relayURL, env, &resp); err != nil {
					return nil, fmt.Errorf("a2a relay: %w", err)
				}
				return resp, nil
			},
		},
		{
			Name:        "projects.update",
			Description: "Patch a project's mutable fields. Requires `project`. Any of `goal`, `parameters_json` (object), `budget_cents`, `policy_overrides_json` (object), `steward_agent_id`, `on_create_template_id` may be supplied. Create-time fields (kind, template_id, parent_project_id) are immutable by design.",
			InputSchema: schema(`{"type":"object","required":["project"],"properties":{"project":{"type":"string"},"goal":{"type":"string"},"parameters_json":{"type":"object"},"budget_cents":{"type":"integer"},"policy_overrides_json":{"type":"object"},"steward_agent_id":{"type":"string"},"on_create_template_id":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				id, _ := args["project"].(string)
				if id == "" {
					return nil, fmt.Errorf("project is required")
				}
				body := map[string]any{}
				for k, v := range args {
					if k == "project" {
						continue
					}
					body[k] = v
				}
				if len(body) == 0 {
					return nil, fmt.Errorf("at least one field to update is required")
				}
				// Handler returns 200 + the patched project row on success, or
				// calls the same read path when body is empty — either way we
				// decode into RawMessage and forward.
				var out json.RawMessage
				if err := c.do("PATCH", c.teamPath("/projects/"+url.PathEscape(id)), nil, body, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "hosts.list",
			Description: "List host-runners registered with the team. Each row carries `id`, `name`, `status` (online/stale/offline), `capabilities` (engines/modes the host can run), `last_seen_at`, and `ssh_hint_json`. Use this to resolve a hostname → host_id for `agents.spawn` (which requires the id, not the name).",
			InputSchema: schema(`{"type":"object","properties":{}}`),
			call: func(c *hubClient, _ map[string]any) (any, error) {
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/hosts"), nil, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "hosts.get",
			Description: "Fetch one host-runner by id. Returns the same shape as `hosts.list` rows. Use after `hosts.list` to confirm a host's capabilities before spawning.",
			InputSchema: schema(`{"type":"object","required":["host"],"properties":{"host":{"type":"string","description":"host_id"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				h, _ := args["host"].(string)
				if h == "" {
					return nil, fmt.Errorf("host is required")
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/hosts/"+url.PathEscape(h)), nil, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "agents.list",
			Description: "List agents in the team. Optional `host_id` filters to agents on one host; optional `status` filters to one engine state (running/idle/paused/terminated/failed/crashed); optional `project_id` filters to agents bound to one project (per ADR-025 — the steward + its workers). By default archived rows are hidden — pass `include_archived: true` to include them. Each row carries `id`, `handle`, `kind`, `status`, `pause_state`, `host_id`, `parent_agent_id`, `project_id`, `created_at`, `last_event_at`. Use this to check what's already running before spawning a duplicate.",
			InputSchema: schema(`{"type":"object","properties":{"host_id":{"type":"string"},"status":{"type":"string"},"project_id":{"type":"string"},"include_archived":{"type":"boolean"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				q := url.Values{}
				if h, ok := args["host_id"].(string); ok && h != "" {
					q.Set("host_id", h)
				}
				if st, ok := args["status"].(string); ok && st != "" {
					q.Set("status", st)
				}
				if pid, ok := args["project_id"].(string); ok && pid != "" {
					q.Set("project_id", pid)
				}
				if inc, ok := args["include_archived"].(bool); ok && inc {
					q.Set("include_archived", "1")
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/agents"), q, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "agents.get",
			Description: "Fetch one agent by id. Returns full detail including `spawn_spec_yaml` and `spawn_authority_json` when known (agents spawned via `agents.spawn` have these; agents minted by other paths may not). Use before `agents.terminate` to confirm the right target.",
			InputSchema: schema(`{"type":"object","required":["agent"],"properties":{"agent":{"type":"string","description":"agent_id"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				a, _ := args["agent"].(string)
				if a == "" {
					return nil, fmt.Errorf("agent is required")
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/agents/"+url.PathEscape(a)), nil, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "agents.terminate",
			Description: "Mark an agent as terminated. The host-runner reconciles by killing the underlying process on its next loop. Sets status=`terminated` and stamps `terminated_at`. Destructive — only stewards may call this. To also drop the row from the live list afterwards, archive via the REST surface (no MCP wrapper today; rare cleanup operation).",
			InputSchema: schema(`{"type":"object","required":["agent"],"properties":{"agent":{"type":"string","description":"agent_id"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				a, _ := args["agent"].(string)
				if a == "" {
					return nil, fmt.Errorf("agent is required")
				}
				body := map[string]any{"status": "terminated"}
				var out json.RawMessage
				if err := c.do("PATCH", c.teamPath("/agents/"+url.PathEscape(a)), nil, body, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "hosts.update_ssh_hint",
			Description: "Patch a host's non-secret ssh_hint_json. Requires `host` and `ssh_hint` (object). The hub rejects payloads containing password/private_key/passphrase/secret/token keys per the data-ownership law (§4) — use only non-secret hints (username, port, jump, proxy_command, identity_file path).",
			InputSchema: schema(`{"type":"object","required":["host","ssh_hint"],"properties":{"host":{"type":"string"},"ssh_hint":{"type":"object","description":"JSON object of non-secret SSH hints; stringified before sending to the hub"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				host, _ := args["host"].(string)
				hint, _ := args["ssh_hint"].(map[string]any)
				if host == "" {
					return nil, fmt.Errorf("host is required")
				}
				if hint == nil {
					return nil, fmt.Errorf("ssh_hint object is required")
				}
				hintBytes, err := json.Marshal(hint)
				if err != nil {
					return nil, fmt.Errorf("marshal ssh_hint: %w", err)
				}
				// REST handler expects {"ssh_hint_json": "<stringified object>"}.
				body := map[string]any{"ssh_hint_json": string(hintBytes)}
				if err := c.do("PATCH", c.teamPath("/hosts/"+url.PathEscape(host)+"/ssh_hint"), nil, body, nil); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "host": host}, nil
			},
		},
		{
			Name:        "project_channels.create",
			Description: "Create a channel scoped to one project. Requires `project_id` and `name`. Project channels carry project-local traffic; use team_channels.create for cross-project rooms like #hub-meta.",
			InputSchema: schema(`{"type":"object","required":["project_id","name"],"properties":{"project_id":{"type":"string"},"name":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				p, _ := args["project_id"].(string)
				name, _ := args["name"].(string)
				if p == "" || name == "" {
					return nil, fmt.Errorf("project_id and name are required")
				}
				var out json.RawMessage
				if err := c.do("POST", c.teamPath("/projects/"+url.PathEscape(p)+"/channels"), nil, map[string]any{"name": name}, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "team_channels.create",
			Description: "Create a team-scope channel (project_id=NULL, scope_kind='team'). Requires `name`. Use for cross-project rooms like #hub-meta.",
			InputSchema: schema(`{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				name, _ := args["name"].(string)
				if name == "" {
					return nil, fmt.Errorf("name is required")
				}
				var out json.RawMessage
				if err := c.do("POST", c.teamPath("/channels"), nil, map[string]any{"name": name}, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "tasks.list",
			Description: "List tasks for a project (ad-hoc and plan-materialized alike). Requires `project_id`. Optional `status` and `priority` filters. Default sort is priority DESC (urgent→low) then updated_at DESC; pass `sort=updated` for reverse-chronological. Each row includes `priority`, `plan_step_id`, and `source` (`ad_hoc` | `plan`).",
			InputSchema: schema(`{"type":"object","required":["project_id"],"properties":{"project_id":{"type":"string"},"status":{"type":"string"},"priority":{"type":"string","enum":["low","med","high","urgent"]},"sort":{"type":"string","enum":["priority","updated"]}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				p, _ := args["project_id"].(string)
				if p == "" {
					return nil, fmt.Errorf("project_id is required")
				}
				q := url.Values{}
				if st, ok := args["status"].(string); ok && st != "" {
					q.Set("status", st)
				}
				if pr, ok := args["priority"].(string); ok && pr != "" {
					q.Set("priority", pr)
				}
				if sortMode, ok := args["sort"].(string); ok && sortMode != "" {
					q.Set("sort", sortMode)
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/projects/"+url.PathEscape(p)+"/tasks"), q, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "tasks.create",
			Description: "Create a task under a project. Requires `project_id` and `title`. Optional `body_md`, `status` (default 'todo'), `priority` (low|med|high|urgent, default 'med'), `assignee_id`, `parent_task_id`, `milestone_id`, `created_by_id`. Note: tasks.list now sorts by priority DESC then updated_at DESC by default.",
			InputSchema: schema(`{"type":"object","required":["project_id","title"],"properties":{"project_id":{"type":"string"},"title":{"type":"string"},"body_md":{"type":"string"},"status":{"type":"string"},"priority":{"type":"string","enum":["low","med","high","urgent"]},"assignee_id":{"type":"string"},"parent_task_id":{"type":"string"},"milestone_id":{"type":"string"},"created_by_id":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				p, _ := args["project_id"].(string)
				if p == "" {
					return nil, fmt.Errorf("project_id is required")
				}
				if t, _ := args["title"].(string); t == "" {
					return nil, fmt.Errorf("title is required")
				}
				body := make(map[string]any, len(args))
				for k, v := range args {
					if k == "project_id" {
						continue
					}
					body[k] = v
				}
				var out json.RawMessage
				if err := c.do("POST", c.teamPath("/projects/"+url.PathEscape(p)+"/tasks"), nil, body, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "tasks.get",
			Description: "Get one task by id. Requires `project_id` and `task`. Response includes `priority` (low|med|high|urgent), `plan_step_id` (empty for ad-hoc tasks) and `source` (`ad_hoc` | `plan`) so callers can tell plan-materialized tasks from user-created ones.",
			InputSchema: schema(`{"type":"object","required":["project_id","task"],"properties":{"project_id":{"type":"string"},"task":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				p, _ := args["project_id"].(string)
				id, _ := args["task"].(string)
				if p == "" || id == "" {
					return nil, fmt.Errorf("project_id and task are required")
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/projects/"+url.PathEscape(p)+"/tasks/"+url.PathEscape(id)), nil, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "tasks.update",
			Description: "Patch a task. Requires `project_id` and `task`. Any of `title`, `body_md`, `status`, `priority` (low|med|high|urgent), `assignee_id` may be supplied.",
			InputSchema: schema(`{"type":"object","required":["project_id","task"],"properties":{"project_id":{"type":"string"},"task":{"type":"string"},"title":{"type":"string"},"body_md":{"type":"string"},"status":{"type":"string"},"priority":{"type":"string","enum":["low","med","high","urgent"]},"assignee_id":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				p, _ := args["project_id"].(string)
				id, _ := args["task"].(string)
				if p == "" || id == "" {
					return nil, fmt.Errorf("project_id and task are required")
				}
				body := map[string]any{}
				for k, v := range args {
					if k == "project_id" || k == "task" {
						continue
					}
					body[k] = v
				}
				if len(body) == 0 {
					return nil, fmt.Errorf("at least one field to update is required")
				}
				if err := c.do("PATCH", c.teamPath("/projects/"+url.PathEscape(p)+"/tasks/"+url.PathEscape(id)), nil, body, nil); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "project_id": p, "task": id}, nil
			},
		},
		{
			Name:        "schedules.list",
			Description: "List schedules for the team. Optional `project` filters to one project.",
			InputSchema: schema(`{"type":"object","properties":{"project":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				q := url.Values{}
				if p, ok := args["project"].(string); ok && p != "" {
					q.Set("project", p)
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/schedules"), q, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "schedules.create",
			Description: "Create a schedule that fires a plan from a template. Requires `project_id`, `template_id`, `trigger_kind` (cron|manual|on_create). `cron_expr` is required when trigger_kind='cron'. Optional `parameters_json` (object) and `enabled` (default true).",
			InputSchema: schema(`{"type":"object","required":["project_id","template_id","trigger_kind"],"properties":{"project_id":{"type":"string"},"template_id":{"type":"string"},"trigger_kind":{"type":"string","enum":["cron","manual","on_create"]},"cron_expr":{"type":"string"},"parameters_json":{"type":"object"},"enabled":{"type":"boolean"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				for _, k := range []string{"project_id", "template_id", "trigger_kind"} {
					if v, _ := args[k].(string); v == "" {
						return nil, fmt.Errorf("%s is required", k)
					}
				}
				var out json.RawMessage
				if err := c.do("POST", c.teamPath("/schedules"), nil, args, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "schedules.update",
			Description: "Patch a schedule. Requires `schedule`. Any of `enabled`, `cron_expr`, `parameters_json` may be supplied.",
			InputSchema: schema(`{"type":"object","required":["schedule"],"properties":{"schedule":{"type":"string"},"enabled":{"type":"boolean"},"cron_expr":{"type":"string"},"parameters_json":{"type":"object"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				id, _ := args["schedule"].(string)
				if id == "" {
					return nil, fmt.Errorf("schedule is required")
				}
				body := map[string]any{}
				for k, v := range args {
					if k == "schedule" {
						continue
					}
					body[k] = v
				}
				if len(body) == 0 {
					return nil, fmt.Errorf("at least one field to update is required")
				}
				if err := c.do("PATCH", c.teamPath("/schedules/"+url.PathEscape(id)), nil, body, nil); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "schedule": id}, nil
			},
		},
		{
			Name:        "schedules.delete",
			Description: "Delete a schedule. Requires `schedule`.",
			InputSchema: schema(`{"type":"object","required":["schedule"],"properties":{"schedule":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				id, _ := args["schedule"].(string)
				if id == "" {
					return nil, fmt.Errorf("schedule is required")
				}
				if err := c.do("DELETE", c.teamPath("/schedules/"+url.PathEscape(id)), nil, nil, nil); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "schedule": id}, nil
			},
		},
		{
			Name:        "schedules.run",
			Description: "Manually fire a schedule — equivalent to a cron tick but user-initiated. Works for any trigger_kind. Returns the newly created plan_id. Requires `schedule`.",
			InputSchema: schema(`{"type":"object","required":["schedule"],"properties":{"schedule":{"type":"string"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				id, _ := args["schedule"].(string)
				if id == "" {
					return nil, fmt.Errorf("schedule is required")
				}
				var out json.RawMessage
				if err := c.do("POST", c.teamPath("/schedules/"+url.PathEscape(id)+"/run"), nil, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name: "mobile.navigate",
			Description: "Navigate the user's mobile app to a `termipod://` URI " +
				"(read-only — no edits, no taps that mutate state). The URI " +
				"addresses an in-app destination such as " +
				"`termipod://project/<id>`, " +
				"`termipod://project/<id>/documents/<docId>/sections/<sectionId>`, " +
				"`termipod://project/<id>/deliverables/<delId>/criteria/<critId>`, " +
				"`termipod://activity?filter=stuck`, " +
				"`termipod://attention/<id>`, " +
				"`termipod://agent/<id>/transcript`, " +
				"`termipod://session/<id>`, or " +
				"`termipod://insights?scope=team_stewards`. " +
				"Use this when the user asks to view or open something — the app " +
				"will animate to the destination and surface a brief banner showing " +
				"the navigation. The mobile floating overlay must be open (the user " +
				"is interacting with you) for the intent to land.",
			InputSchema: schema(`{"type":"object","required":["uri"],"properties":{"uri":{"type":"string","description":"termipod:// URI naming the in-app destination"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				uri, ok := args["uri"].(string)
				if !ok || uri == "" {
					return nil, fmt.Errorf("uri is required")
				}
				body := map[string]any{"uri": uri}
				var out json.RawMessage
				if err := c.do("POST", c.teamPath("/mobile/intent"), nil, body, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			Name:        "audit.read",
			Description: "List audit events for the team. Supports optional `limit` and `since` query params.",
			InputSchema: schema(`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":1000},"since":{"type":"string","description":"RFC3339 timestamp"}}}`),
			call: func(c *hubClient, args map[string]any) (any, error) {
				q := url.Values{}
				if v, ok := args["limit"]; ok {
					switch n := v.(type) {
					case float64:
						q.Set("limit", fmt.Sprintf("%d", int(n)))
					case int:
						q.Set("limit", fmt.Sprintf("%d", n))
					case string:
						q.Set("limit", n)
					}
				}
				if s, ok := args["since"].(string); ok && s != "" {
					q.Set("since", s)
				}
				var out json.RawMessage
				if err := c.do("GET", c.teamPath("/audit"), q, nil, &out); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
	}
	// W2 (ADR-016): template-authoring MCP tools per category
	// (agents/prompts/plans) — five ops each (create, update,
	// delete, list, get). Defined in tools_templates.go.
	tools = append(tools, templateToolDefs()...)
	return tools
}

// findTool does a linear scan of buildTools(). The table is ~14 entries
// so a map would only add noise; linear scan keeps "the one source of
// truth" literal.
func findTool(tools []toolDef, name string) (toolDef, bool) {
	for _, t := range tools {
		if t.Name == name {
			return t, true
		}
	}
	return toolDef{}, false
}
