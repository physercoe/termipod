// hub-mcp-server — standalone binary that forwards into
// internal/hubmcpserver.Run. The implementation lives in the package
// so host-runner's multicall (cmd/host-runner/main.go) can route to
// the same code without forking a separate executable.
package main

import (
	"os"

	"github.com/termipod/hub/internal/hubmcpserver"
)

func main() {
	os.Exit(hubmcpserver.Run(os.Args[1:]))
}
