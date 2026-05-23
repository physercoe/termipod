// Package hookfire is the stdio shim that claude-code invokes for each
// settings.local.json hook entry. It converts a single hook event into
// a JSON-RPC `tools/call` against the per-spawn host-runner UDS MCP
// gateway, and threads the gateway's response back to claude on stdout.
//
// Protocol on the wire — claude-code's hook contract:
//
//   1. claude spawns `host-runner hook-fire --socket <uds> --event <Event>`.
//   2. claude writes the hook payload (single JSON object) to the shim's
//      stdin and closes stdin.
//   3. The shim writes a response JSON object (possibly `{}`) to stdout
//      and exits 0 for success / non-zero on transport failure.
//
// For OBSERVATIONAL hooks (PostToolUse, Notification, Stop, Session*,
// UserPromptSubmit, SubagentStop) claude ignores stdout content; the
// process need only run to completion. For BLOCKING hooks (PreToolUse,
// PreCompact) the response object's `decision` / `message` fields gate
// the next action — we forward whatever the adapter returned.
//
// Why a separate subcommand from `mcp-uds-stdio`: that one is a generic
// stdio↔UDS pump for full JSON-RPC clients (claude-code's `.mcp.json`).
// Hooks aren't JSON-RPC on the wire — they're raw event payloads in
// and response JSON out — so the shim has to do the JSON-RPC wrapping
// itself.
//
// Background: ADR-027 W6 originally proposed `type: "mcp_tool"` hook
// entries that would let claude invoke MCP tools directly as hooks.
// claude-code's actual schema only accepts `type: "command"`, so every
// settings.local.json the M4 LocalLogTail path wrote since v1.0.592
// failed validation at agent startup. v1.0.659 routes around that with
// this shim — same UDS gateway, same per-event handler, just a thin
// command-typed bridge in front.
package hookfire

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// EventToToolName maps claude-code's PascalCase event names to the
// gateway's snake_case `hook_*` tool names. Single source of truth so
// hooks_install.go (host-runner side) and this shim (subprocess side)
// stay in sync. Order mirrors claudeHookEvents in hooks_install.go.
var EventToToolName = map[string]string{
	"PreToolUse":       "hook_pre_tool_use",
	"PostToolUse":      "hook_post_tool_use",
	"Notification":     "hook_notification",
	"PreCompact":       "hook_pre_compact",
	"Stop":             "hook_stop",
	"SubagentStop":     "hook_subagent_stop",
	"UserPromptSubmit": "hook_user_prompt",
	"SessionStart":     "hook_session_start",
	"SessionEnd":       "hook_session_end",
}

// Run executes the shim with `args` (typically os.Args[2:] from the
// host-runner multicall). Returns a process exit code.
//
// Exit semantics:
//   - 0 → success (response written to stdout).
//   - 1 → transport failure (UDS dial, write, or read errored). A
//     warning is on stderr. Stdout carries an empty `{}` so blocking
//     hooks default to "allow" rather than blocking on our outage.
//   - 2 → usage error (missing flag, malformed event).
//
// We deliberately prefer "0 + empty response on best-effort failure"
// for observational hooks: a transport blip should never kill claude's
// turn. For blocking hooks the empty response means claude proceeds
// with the default action — the same outcome you'd get if the hook
// hadn't been installed at all.
func Run(args []string) int {
	fs := flag.NewFlagSet("hook-fire", flag.ContinueOnError)
	socket := fs.String("socket", os.Getenv("HOOK_FIRE_SOCKET"),
		"UDS path of the per-spawn host-runner MCP gateway")
	event := fs.String("event", "",
		"claude-code event name (PreToolUse, PostToolUse, ...)")
	dialTimeout := fs.Duration("dial-timeout", 3*time.Second,
		"deadline for the initial UDS dial")
	callTimeout := fs.Duration("call-timeout", 60*time.Second,
		"deadline for the full request/response cycle on the UDS")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *socket == "" {
		fmt.Fprintln(os.Stderr, "hook-fire: --socket (or HOOK_FIRE_SOCKET env) is required")
		return 2
	}
	if *event == "" {
		fmt.Fprintln(os.Stderr, "hook-fire: --event is required")
		return 2
	}
	tool, ok := EventToToolName[*event]
	if !ok {
		fmt.Fprintf(os.Stderr, "hook-fire: unknown event %q; valid: %s\n",
			*event, knownEventsList())
		return 2
	}

	// Read the hook payload from stdin. claude-code closes stdin
	// after writing the event JSON, so io.ReadAll terminates.
	payloadBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hook-fire: read stdin: %v\n", err)
		fmt.Fprintln(os.Stdout, "{}")
		return 1
	}

	// Parse the payload as a JSON object. An empty stdin or a value
	// that isn't an object is tolerated as `{}` — the gateway side
	// already coerces nil to {}. We pass through whatever object
	// claude gave us untouched (don't strip / rename fields).
	var argMap map[string]any
	if len(bytes.TrimSpace(payloadBytes)) > 0 {
		if err := json.Unmarshal(payloadBytes, &argMap); err != nil {
			fmt.Fprintf(os.Stderr, "hook-fire: parse stdin: %v (treating as empty)\n", err)
			argMap = nil
		}
	}
	if argMap == nil {
		argMap = map[string]any{}
	}

	resp, terr := transport(*socket, tool, argMap, *dialTimeout, *callTimeout)
	if terr != nil {
		fmt.Fprintf(os.Stderr, "hook-fire: %v\n", terr)
		fmt.Fprintln(os.Stdout, "{}")
		return 1
	}

	// Write resp + newline so claude reads a complete JSON object.
	// The gateway's mcpGWResultJSON re-marshals with indent; we keep
	// it as-is — claude parses any well-formed JSON.
	if _, err := os.Stdout.Write(append(resp, '\n')); err != nil {
		// Stdout write error is unusual; report but exit 0 since the
		// request itself succeeded.
		fmt.Fprintf(os.Stderr, "hook-fire: write stdout: %v\n", err)
	}
	return 0
}

// transport dials the gateway, sends the JSON-RPC `tools/call`, and
// returns the unwrapped response JSON (the `result.content[0].text`
// field, which gateway's mcpGWResultJSON puts the OnHook map into).
//
// Errors are wrapped with context so the caller can log them as a
// single line on stderr. Network errors, JSON parse failures, and
// JSON-RPC `error` responses all surface here.
func transport(socket, tool string, args map[string]any, dialTimeout, callTimeout time.Duration) ([]byte, error) {
	dialCtx, cancelDial := context.WithTimeout(context.Background(), dialTimeout)
	defer cancelDial()

	var dialer net.Dialer
	conn, err := dialer.DialContext(dialCtx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", socket, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(callTimeout))

	// JSON-RPC request frame. id=1 is fine — we make one call then
	// close. Gateway's run-loop pairs requests by id but a single-
	// shot client doesn't need uniqueness.
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      tool,
			"arguments": args,
		},
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	if _, err := conn.Write(append(reqBytes, '\n')); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}
	// Half-close write side so the gateway sees end-of-input on its
	// read pump and flushes. Mirror mcpudsbridge.pump.
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}

	respBytes, err := io.ReadAll(conn)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(bytes.TrimSpace(respBytes)) == 0 {
		return nil, fmt.Errorf("empty response")
	}

	// The gateway may write multiple lines if it interleaves; for a
	// single tools/call we expect exactly one. Take the FIRST
	// non-empty line.
	var line []byte
	for _, l := range bytes.Split(respBytes, []byte("\n")) {
		if len(bytes.TrimSpace(l)) > 0 {
			line = l
			break
		}
	}
	if len(line) == 0 {
		return nil, fmt.Errorf("no non-empty response line")
	}

	var rpc struct {
		Error  *struct{ Message string } `json:"error,omitempty"`
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(line, &rpc); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if rpc.Error != nil {
		return nil, fmt.Errorf("gateway error: %s", rpc.Error.Message)
	}
	for _, c := range rpc.Result.Content {
		if c.Type == "text" {
			return []byte(c.Text), nil
		}
	}
	// Empty content array → empty map.
	return []byte("{}"), nil
}

func knownEventsList() string {
	ks := make([]string, 0, len(EventToToolName))
	for k := range EventToToolName {
		ks = append(ks, k)
	}
	// Stable order for the error message.
	for i := 0; i < len(ks); i++ {
		for j := i + 1; j < len(ks); j++ {
			if ks[j] < ks[i] {
				ks[i], ks[j] = ks[j], ks[i]
			}
		}
	}
	return strings.Join(ks, ", ")
}
