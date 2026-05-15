// Package mcpudsbridge is the stdio ⇆ UDS shim for MCP clients that can
// only spawn a subprocess and exchange newline-delimited JSON-RPC on
// stdin/stdout (claude-code is the immediate caller via `.mcp.json`).
// Each spawned agent's `.mcp.json` runs `host-runner mcp-uds-stdio
// --socket /tmp/termipod-agent-<id>.sock` to bridge into the per-spawn
// host-runner UDS MCP gateway (hub/internal/hostrunner/mcp_gateway.go).
//
// Why a separate package from mcpbridge: mcpbridge speaks HTTP to the
// real hub; this one speaks UDS to the local gateway. Different
// transport, no shared connection state, no shared lifecycle — keeping
// them split avoids tangling unrelated request paths in one binary.
//
// One spawn → one shim process → one UDS connection. The gateway
// accepts as many connections as land on its listener, so multiple
// MCP clients in the same agent (rare) would each get their own shim
// instance; each instance owns its own dial.
package mcpudsbridge

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

// Run executes the shim with `args` (typically os.Args[2:] from the
// host-runner multicall). Returns a process exit code. The agent's
// `.mcp.json` invokes this with `--socket <path>` (or MCP_UDS_SOCKET
// env); we dial the UDS, then pump stdio ↔ socket until one side
// closes.
func Run(args []string) int {
	fs := flag.NewFlagSet("mcp-uds-stdio", flag.ExitOnError)
	socket := fs.String("socket", os.Getenv("MCP_UDS_SOCKET"),
		"UDS socket path of the per-spawn host-runner MCP gateway "+
			"(or MCP_UDS_SOCKET env)")
	dialTimeout := fs.Duration("dial-timeout", 5*time.Second,
		"timeout for the initial UDS dial; the gateway should already "+
			"be listening when host-runner spawns the agent")
	_ = fs.Parse(args)

	if *socket == "" {
		fmt.Fprintln(os.Stderr, "mcp-uds-stdio: --socket (or MCP_UDS_SOCKET env) is required")
		return 2
	}

	dialCtx, cancel := context.WithTimeout(context.Background(), *dialTimeout)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(dialCtx, "unix", *socket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcp-uds-stdio: dial %s: %v\n", *socket, err)
		return 1
	}
	defer conn.Close()
	return pump(os.Stdin, os.Stdout, conn)
}

// pump shuttles bytes between (stdin → conn) and (conn → stdout). MCP
// is already a byte stream (one JSON object per line, LF-delimited),
// so we don't re-frame — a pair of io.Copy calls is sufficient.
//
// Lifecycle:
//   - stdin → conn runs in a background goroutine. On stdin EOF we
//     half-close the UDS write side so the gateway sees a clean
//     end-of-input; the gateway then drains its replies and closes
//     its end.
//   - conn → stdout runs on pump's caller goroutine. It drives the
//     return: once the gateway closes (either responding to our
//     half-close, or unilaterally), io.Copy returns and pump returns.
//   - The stdin reader may still be blocked on a Read when we return
//     (the gateway closed first, before stdin EOF). That goroutine
//     leaks until process exit. This is intentional — Go has no
//     portable way to unblock a blocked file Read, and exiting is
//     fine for a per-spawn shim where the process is throwaway.
func pump(stdin io.Reader, stdout io.Writer, conn net.Conn) int {
	go func() {
		_, _ = io.Copy(conn, stdin)
		if uc, ok := conn.(*net.UnixConn); ok {
			_ = uc.CloseWrite()
		}
	}()

	if _, err := io.Copy(stdout, conn); err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "mcp-uds-stdio: uds->stdout: %v\n", err)
		return 1
	}
	return 0
}
