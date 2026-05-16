// tools_templates.go — MCP tool wrappers for the per-team template
// overlay (W2 of the lifecycle wedge plan, gated by ADR-016).
//
// Three categories — agent / prompt / plan — each gets five ops:
// create, update, delete, list, get. They're thin wrappers over the
// existing /v1/teams/{team}/templates/{category}/{name} REST surface;
// the heavy lifting (path validation, self-modification guard,
// overlay write) lives in handlers_templates.go.
//
// Tool names follow ADR-016 D2 literally (templates.agent.create,
// templates.prompt.update, …) so the operation-scope manifest in
// roles.yaml can name them precisely. The role gate at the MCP
// boundary in dispatchTool denies workers' write attempts; the
// self-modification guard in handlePutTemplate denies a steward
// editing its own kind's template.

package hubmcpserver

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// templateToolDefs returns the 15 template-authoring tools (3
// categories × 5 ops). Called from buildTools() and appended to the
// dispatch table.
func templateToolDefs() []toolDef {
	cats := []struct {
		toolPrefix string // "agent" | "prompt" | "plan"
		dirName    string // on-disk category: "agents" | "prompts" | "plans"
		ext        string // canonical file extension for help text
	}{
		{"agent", "agents", ".yaml"},
		{"prompt", "prompts", ".md"},
		{"plan", "plans", ".yaml"},
	}
	out := make([]toolDef, 0, len(cats)*6)
	for _, c := range cats {
		out = append(out,
			templateCreateTool(c.toolPrefix, c.dirName, c.ext),
			templateUpdateTool(c.toolPrefix, c.dirName, c.ext),
			templateDeleteTool(c.toolPrefix, c.dirName),
			templateListTool(c.toolPrefix, c.dirName),
			templateGetTool(c.toolPrefix, c.dirName),
			scaffoldToolFor(c.toolPrefix, c.dirName, c.ext),
		)
	}
	return out
}

// templateCreateTool — create a template via PUT. PUT is idempotent
// in the underlying handler, so create+update share semantics; we
// expose them as separate tool names per ADR-016 D2 so the manifest
// names them precisely. By convention, callers use create when the
// file is new and update when amending.
func templateCreateTool(toolPrefix, dirName, ext string) toolDef {
	return toolDef{
		Name:        "templates." + toolPrefix + ".create",
		Description: "Create a new " + toolPrefix + " template at <DataRoot>/team/templates/" + dirName + "/<name>. `name` should include the canonical extension (e.g. \"my-worker.v1" + ext + "\"). `content` is the raw file body. Returns 201 on first write, 200 on overwrite. SCHEMA: bundled templates ARE the schema reference — call `templates." + toolPrefix + ".scaffold` for an empty skeleton, or `templates." + toolPrefix + ".list` + `templates." + toolPrefix + ".get` on the closest existing template (e.g. `steward.v1.yaml` for a steward, `coder.v1.yaml` for a worker) and modify in place. Don't author from scratch — the YAML schema isn't in any prompt.",
		InputSchema: schema(`{"type":"object","required":["name","content"],"properties":{"name":{"type":"string","description":"file name including extension, e.g. \"my-worker.v1` + ext + `\""},"content":{"type":"string","description":"raw file body (YAML/Markdown/JSON)"}}}`),
		call: func(c *hubClient, args map[string]any) (any, error) {
			return templatePutCall(c, dirName, args)
		},
	}
}

func templateUpdateTool(toolPrefix, dirName, ext string) toolDef {
	return toolDef{
		Name:        "templates." + toolPrefix + ".update",
		Description: "Update an existing " + toolPrefix + " template. Same wire shape as templates." + toolPrefix + ".create — both PUT to the same path; the server distinguishes 200 (overwrite) from 201 (new). Fetch the current `content` via `templates." + toolPrefix + ".get` first; the body is a full overwrite, not a patch.",
		InputSchema: schema(`{"type":"object","required":["name","content"],"properties":{"name":{"type":"string","description":"file name including extension, e.g. \"my-worker.v1` + ext + `\""},"content":{"type":"string","description":"raw file body (YAML/Markdown/JSON)"}}}`),
		call: func(c *hubClient, args map[string]any) (any, error) {
			return templatePutCall(c, dirName, args)
		},
	}
}

func templateDeleteTool(toolPrefix, dirName string) toolDef {
	return toolDef{
		Name:        "templates." + toolPrefix + ".delete",
		Description: "Delete a " + toolPrefix + " template from the team overlay. Bundled defaults remain accessible via embedded FS — there is no \"restore\", just delete the overlay file. Returns 404 if no overlay file exists.",
		InputSchema: schema(`{"type":"object","required":["name"],"properties":{"name":{"type":"string","description":"file name including extension"}}}`),
		call: func(c *hubClient, args map[string]any) (any, error) {
			name, _ := args["name"].(string)
			if name == "" {
				return nil, fmt.Errorf("name is required")
			}
			var out json.RawMessage
			if err := c.do("DELETE", c.teamPath("/templates/"+dirName+"/"+url.PathEscape(name)), nil, nil, &out); err != nil {
				return nil, err
			}
			return out, nil
		},
	}
}

func templateListTool(toolPrefix, dirName string) toolDef {
	return toolDef{
		Name:        "templates." + toolPrefix + ".list",
		Description: "List " + toolPrefix + " templates in the team overlay. Returns array of {name, path, size, mod_time} entries.",
		InputSchema: schema(`{"type":"object","properties":{}}`),
		call: func(c *hubClient, args map[string]any) (any, error) {
			q := url.Values{}
			q.Set("category", dirName)
			var out json.RawMessage
			if err := c.do("GET", c.teamPath("/templates"), q, nil, &out); err != nil {
				return nil, err
			}
			return out, nil
		},
	}
}

func templateGetTool(toolPrefix, dirName string) toolDef {
	return toolDef{
		Name: "templates." + toolPrefix + ".get",
		// Defaulting raw=false (i.e. merge=1) means the steward always
		// sees the embedded built-in's required fields (driving_mode,
		// backend.cmd, …) even when a user's on-disk overlay was
		// authored before those fields were required. Opt out with
		// raw=true when you want the unmerged disk body (e.g. for an
		// editor that will overwrite verbatim).
		Description: "Fetch a " + toolPrefix + " template. Returns {category, name, content}. By default the on-disk overlay is merged with the embedded built-in so the returned body is always schema-complete (a stale overlay can't hide required fields like driving_mode). Pass raw=true to skip the merge.",
		InputSchema: schema(`{"type":"object","required":["name"],"properties":{"name":{"type":"string","description":"file name including extension"},"raw":{"type":"boolean","description":"skip the embedded-built-in merge and return the on-disk body verbatim","default":false}}}`),
		call: func(c *hubClient, args map[string]any) (any, error) {
			name, _ := args["name"].(string)
			if name == "" {
				return nil, fmt.Errorf("name is required")
			}
			raw, _ := args["raw"].(bool)
			path := c.teamPath("/templates/" + dirName + "/" + url.PathEscape(name))
			if !raw {
				path += "?merge=1"
			}
			body, err := c.doRaw("GET", path, nil)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"category": dirName,
				"name":     name,
				"content":  string(body),
			}, nil
		},
	}
}

// templatePutCall is the shared body for create + update — both PUT
// to the same path with the same payload shape. Differentiation is
// purely at the tool-name level (so the operation-scope manifest can
// gate them as expected, and so callers see two intent-tagged tools
// instead of one ambiguous PUT).
func templatePutCall(c *hubClient, dirName string, args map[string]any) (any, error) {
	name, _ := args["name"].(string)
	content, _ := args["content"].(string)
	if name == "" || content == "" {
		return nil, fmt.Errorf("name and content are required")
	}
	body, err := c.doRawPutBody("PUT", c.teamPath("/templates/"+dirName+"/"+url.PathEscape(name)), []byte(content))
	if err != nil {
		return nil, err
	}
	// PUT returns a small JSON object {category, name, size}; pass
	// it through as RawMessage so the MCP layer renders it directly.
	return json.RawMessage(body), nil
}
