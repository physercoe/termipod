// Package hub is the root package for the Termipod Hub module.
// Its only job is to expose embedded resources (migrations, built-in
// templates) and a handful of cross-cutting protocol constants that
// both the server and the host-runner need to agree on, so internal/
// packages can consume them without crossing module-root directories
// with //go:embed.
package hub

import "embed"

//go:embed migrations/*.sql
var MigrationsFS embed.FS

//go:embed all:templates
var TemplatesFS embed.FS

// MCPServerName is the namespace under which the hub registers its MCP
// tools. Changing this is a wire-protocol-breaking change: every
// existing template that mentions `mcp__termipod__permission_prompt`
// would need updating, every host-runner's `.mcp.json` would need
// rewriting on next launch, and every steward agent's spawn command
// would need re-rendering. Templates use {{mcp_namespace}} so a future
// rename only touches this constant + a redeploy.
//
// Single namespace: as of v1.0.297 the hub's in-process MCP catalog
// also serves the rich-authority surface (projects, plans, runs, …)
// previously hosted by the standalone hub-mcp-server daemon, so one
// .mcp.json entry reaches everything (see mcp_authority.go).
const MCPServerName = "termipod"

// MCPServerNameHost is the namespace under which the per-spawn
// host-runner gateway registers ITS local MCP tools (currently the 9
// hook handlers from ADR-027 W5b). Only the claude-code M4
// LocalLogTailDriver path writes this server into `.mcp.json` —
// every other spawn keeps a single-entry `.mcp.json` pointing at
// MCPServerName. Changing this name is a wire change for ADR-027's
// `settings.local.json` hook entries (`mcp__termipod-host__hook_*`).
const MCPServerNameHost = "termipod-host"
