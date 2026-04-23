// tools.go — MCP tool catalog + dispatch table.
//
// Each tool is a thin adapter over a hub REST endpoint (see blueprint §5.2
// "The relay principle": MCP never bypasses the hub's authority — it is a
// protocol translation in front of the existing REST surface). We expose
// what P1.5 requires and nothing more; speculative tools are rejected at
// tools/call time rather than silently no-op'd so the client can see the
// schema boundary.
package main

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
	return []toolDef{
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
			Name:        "projects.create",
			Description: "Create a new project. Requires `name` and `kind` ('goal' or 'standing').",
			InputSchema: schema(`{"type":"object","required":["name","kind"],"properties":{"name":{"type":"string"},"kind":{"type":"string","enum":["goal","standing"]},"goal":{"type":"string"},"docs_root":{"type":"string"},"config_yaml":{"type":"string"},"parent_project_id":{"type":"string"},"template_id":{"type":"string"}}}`),
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
			Name:        "agents.spawn",
			Description: "Spawn a child agent. Requires `child_handle`, `kind`, `spawn_spec_yaml`. Optional: host_id, parent_agent_id, worktree_path, budget_cents, mode. May return 202 + attention_id if policy gates the spawn on approval.",
			InputSchema: schema(`{"type":"object","required":["child_handle","kind","spawn_spec_yaml"],"properties":{"child_handle":{"type":"string"},"kind":{"type":"string"},"spawn_spec_yaml":{"type":"string"},"host_id":{"type":"string"},"parent_agent_id":{"type":"string"},"worktree_path":{"type":"string"},"budget_cents":{"type":"integer"},"mode":{"type":"string"}}}`),
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
