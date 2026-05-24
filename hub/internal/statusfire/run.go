// Package statusfire is the stdio shim that claude-code invokes for the
// settings.local.json statusLine entry (ADR-036 W1). It converts the
// statusLine JSON payload into a JSON-RPC `tools/call status_line`
// against the per-spawn host-runner UDS MCP gateway, then prints a
// single line to stdout for claude to render in its status row.
//
// Protocol on the wire — claude-code's statusLine contract:
//
//  1. claude spawns `host-runner status-fire --socket <uds>` on every
//     status-line refresh (cadence is calm — host-verified ~10s avg
//     with occasional 0.3s turn-end doubles; max ~50s idle).
//  2. claude writes the structured statusLine JSON to the shim's stdin
//     and closes stdin.
//  3. The shim writes ONE line to stdout (claude renders it verbatim)
//     and exits 0.
//
// The shim is deliberately silent on failure paths — a status-line
// shim that crashes loud would make claude's TUI display the stderr
// (or worse, render an error in the status row every 10s). Transport
// failures (UDS gone, socket dial timeout, gateway error) degrade to
// a quiet `termipod` line on stdout with the error noted on stderr.
//
// Wrap-and-passthrough — if the operator already has a statusLine
// command registered, the host-runner installer records it under
// `_termipod_wrapped_command` in the marker block (see hooks_install.go
// W7); this shim, if invoked with `--wrap <cmd>`, will invoke that
// command AFTER posting to the gateway, piping the same stdin through,
// and use its stdout as the rendered status text. Operator config
// stays visible; telemetry is additive.
//
// Why a separate subcommand from `hook-fire`: that one is event-typed
// (PreToolUse, PostToolUse, ...) and the dispatch maps event→tool. The
// statusLine has no event — every fire goes to the same `status_line`
// tool on the gateway. Different protocol shape (no `--event` flag),
// different contract (claude renders stdout verbatim, not as JSON),
// different latency budget (10s cadence, must not block the TUI).
package statusfire

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"time"
)

// Tool is the gateway tool name this shim invokes. Single source of
// truth across statusfire (subprocess) and mcp_gateway.go (host-runner
// side).
const Tool = "status_line"

// DefaultStatusText is what the shim prints to stdout when there's no
// operator-wrapped command to defer to. claude renders it verbatim in
// the status row. Kept terse — operators who want a richer line wire
// their own command via --wrap.
const DefaultStatusText = "termipod"

// Run executes the shim with `args` (typically os.Args[2:] from the
// host-runner multicall). Returns a process exit code.
//
// Exit semantics:
//   - 0 → success (status line written to stdout).
//   - 0 → transport failure (UDS dial / write / read errored). Stderr
//     gets a one-line warning; stdout still gets a status line so
//     claude has something to render. Status-line UI must not crash
//     because the gateway is briefly unreachable.
//   - 2 → usage error (missing --socket flag).
//
// We deliberately exit 0 on best-effort transport failure — a 10s
// cadence of non-zero exits would leak as a constant red indicator in
// claude's TUI on every status refresh. The error gets logged once on
// stderr (claude routes that to its own log file).
func Run(args []string) int {
	fs := flag.NewFlagSet("status-fire", flag.ContinueOnError)
	socket := fs.String("socket", os.Getenv("STATUS_FIRE_SOCKET"),
		"UDS path of the per-spawn host-runner MCP gateway")
	wrap := fs.String("wrap", "",
		"optional operator command to invoke after posting (D1 wrap-and-passthrough); "+
			"the shim feeds the same stdin into the wrapped cmd and uses its stdout")
	dialTimeout := fs.Duration("dial-timeout", 1*time.Second,
		"deadline for the initial UDS dial (status-line latency budget is tight)")
	callTimeout := fs.Duration("call-timeout", 3*time.Second,
		"deadline for the full request/response cycle on the UDS")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *socket == "" {
		fmt.Fprintln(os.Stderr, "status-fire: --socket (or STATUS_FIRE_SOCKET env) is required")
		return 2
	}

	// Read the statusLine JSON from stdin. claude-code closes stdin
	// after writing the payload, so io.ReadAll terminates.
	payloadBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "status-fire: read stdin: %v\n", err)
		writeStatus(*wrap, payloadBytes)
		return 0
	}

	// Decode as a generic JSON object. The gateway treats malformed
	// payloads as empty; we mirror that here rather than dropping the
	// fire on the floor (an unrecoverable parse error shouldn't make
	// the chip strip go dark).
	var argMap map[string]any
	if len(bytes.TrimSpace(payloadBytes)) > 0 {
		if err := json.Unmarshal(payloadBytes, &argMap); err != nil {
			fmt.Fprintf(os.Stderr, "status-fire: parse stdin: %v (posting as empty)\n", err)
			argMap = nil
		}
	}
	if argMap == nil {
		argMap = map[string]any{}
	}

	// Best-effort post; ignore transport errors per the exit-0-on-fail
	// rule above. The response body is discarded — status_line is a
	// fire-and-forget signal.
	if terr := transport(*socket, argMap, *dialTimeout, *callTimeout); terr != nil {
		fmt.Fprintf(os.Stderr, "status-fire: post: %v\n", terr)
	}

	writeStatus(*wrap, payloadBytes)
	return 0
}

// writeStatus emits the status row claude will render. If --wrap was
// passed, invoke the wrapped command with our stdin and use its stdout
// (first line); else print DefaultStatusText. The wrap branch is
// deliberately best-effort — a broken operator command degrades to the
// default line rather than blanking claude's status row.
func writeStatus(wrap string, payloadBytes []byte) {
	if wrap == "" {
		fmt.Fprintln(os.Stdout, DefaultStatusText)
		return
	}
	// Re-feed the original payload into the operator command's stdin.
	// We use /bin/sh -c so the operator's value can be a full command
	// line ("/usr/bin/jq -r '...'") — same shape claude-code itself
	// accepts in the `command` field.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", wrap)
	cmd.Stdin = bytes.NewReader(payloadBytes)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "status-fire: wrap %q: %v (falling back to default)\n", wrap, err)
		fmt.Fprintln(os.Stdout, DefaultStatusText)
		return
	}
	// Take the first non-empty line; trim trailing newline so we
	// don't double-emit. If the wrapped cmd printed nothing, fall back.
	s := bytes.TrimRight(out.Bytes(), "\n")
	if len(s) == 0 {
		fmt.Fprintln(os.Stdout, DefaultStatusText)
		return
	}
	// Multi-line wrapped output: pass through verbatim — claude-code
	// allows multi-line status rows. Don't post-process.
	os.Stdout.Write(s)
	os.Stdout.Write([]byte{'\n'})
}

// transport dials the gateway, sends the JSON-RPC `tools/call`
// status_line, and returns nil on success. The result body is
// discarded — the gateway acks the post and posts the AgentEvent
// itself; this shim's only job is to deliver the payload.
func transport(socket string, args map[string]any, dialTimeout, callTimeout time.Duration) error {
	dialCtx, cancelDial := context.WithTimeout(context.Background(), dialTimeout)
	defer cancelDial()

	var dialer net.Dialer
	conn, err := dialer.DialContext(dialCtx, "unix", socket)
	if err != nil {
		return fmt.Errorf("dial %s: %w", socket, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(callTimeout))

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      Tool,
			"arguments": args,
		},
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	if _, err := conn.Write(append(reqBytes, '\n')); err != nil {
		return fmt.Errorf("write request: %w", err)
	}
	// Half-close write side so the gateway flushes its response. Mirror
	// hookfire.transport (same UDS shape).
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}

	// Drain the response so the gateway sees a clean close. We don't
	// parse it — the gateway's mcpGWResultJSON success path is enough.
	respBytes, err := io.ReadAll(conn)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(bytes.TrimSpace(respBytes)) == 0 {
		return fmt.Errorf("empty response")
	}
	return nil
}
