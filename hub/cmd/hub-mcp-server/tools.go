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
	"encoding/json"
	"fmt"
	"net/url"
)

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
