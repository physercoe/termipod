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
const MCPServerName = "termipod"

// MCPHubServerName is the namespace for the second MCP server every
// spawned agent gets — the local stdio terminator
// (internal/hubmcpserver) that exposes the rich authority surface
// (projects, plans, runs, agents.spawn, a2a.invoke, …). Distinct from
// MCPServerName so claude-code's tool routing (mcp__<server>__<tool>)
// doesn't collide. Both servers are registered in writeMCPConfig.
const MCPHubServerName = "termipod-hub"
