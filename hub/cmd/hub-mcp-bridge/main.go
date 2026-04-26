// hub-mcp-bridge — thin shim around internal/mcpbridge.
//
// host-runner is also built with multicall + a `mcp-bridge` subcommand,
// so deployments that install only host-runner (recommended) cover this
// role too. This standalone binary is preserved for back-compat with
// older builds and any direct go-build users; it shares its core with
// host-runner via the internal/mcpbridge package.
package main

import (
	"os"

	"github.com/termipod/hub/internal/mcpbridge"
)

func main() {
	os.Exit(mcpbridge.Run(os.Args[1:]))
}
