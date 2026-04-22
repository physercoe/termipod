// hub-mcp-server is a standalone MCP server (blueprint §5.1 "Agent ↔ Hub
// (authority caps)", §9 P1.5) that exposes hub authority capabilities —
// projects, plans, runs, documents, reviews, policy, audit — as MCP
// tools callable over stdio.
//
// Framing: newline-delimited JSON-RPC 2.0, the same convention as
// cmd/hub-mcp-bridge. We implement it by hand rather than pulling in an
// MCP SDK because P1.5 only needs `initialize`, `tools/list`, and
// `tools/call`; a dependency would be more code than the protocol
// translation it would save.
//
// Relation to hub-mcp-bridge: the bridge is a stdio⇆HTTP shim that
// forwards every JSON-RPC line to the hub's /mcp/{token} endpoint. This
// server is a *peer* to the bridge, not a replacement: it terminates MCP
// locally and dispatches to the hub REST API via a normal HTTP client.
// Host-runners (P1.6) will embed this server directly rather than
// shelling out to it.
//
// Configuration is all env-driven; see main() for the full set. Exit
// codes: 0 normal stdin EOF, 2 bad configuration. Anything else means a
// panic escaped and the OS already surfaced it.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
)

const (
	serverName    = "hub-mcp-server"
	serverVersion = "0.1.0"
	// Protocol version mirrors what the bridge negotiates; MCP clients
	// accept a broad range here and we don't rely on any newer feature.
	protocolVersion = "2024-11-05"
)

// jsonrpcReq is the shape we care about from the client. `Params` stays raw
// so each method can define its own schema without a pre-decode step that
// would reject unknown fields we'd rather pass through.
type jsonrpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// JSON-RPC error codes we actually produce. The spec reserves a wider
// range (see -32600 invalid request, etc.); we only define the ones used
// so `go vet`'s unused-const lint stays quiet.
const (
	errParse          = -32700
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
)

func main() {
	hubURL := os.Getenv("HUB_URL")
	token := os.Getenv("HUB_TOKEN")
	team := os.Getenv("HUB_TEAM")
	// Fail fast with a single clear message: MCP clients typically hide
	// stderr, so we also refuse to start rather than report cryptic
	// downstream HTTP failures on every tool call.
	missing := []string{}
	if hubURL == "" {
		missing = append(missing, "HUB_URL")
	}
	if token == "" {
		missing = append(missing, "HUB_TOKEN")
	}
	if team == "" {
		missing = append(missing, "HUB_TEAM")
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "%s: missing required env: %v\n", serverName, missing)
		os.Exit(2)
	}

	logger := log.New(os.Stderr, serverName+" ", log.LstdFlags|log.Lmsgprefix)
	client := newHubClient(hubURL, token, team)
	tools := buildTools()

	br := bufio.NewReader(os.Stdin)
	bw := bufio.NewWriter(os.Stdout)
	defer bw.Flush()

	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if resp, ok := handleLine(client, tools, line); ok && resp != nil {
				if _, werr := bw.Write(resp); werr != nil {
					logger.Printf("stdout write: %v", werr)
					return
				}
				if werr := bw.WriteByte('\n'); werr != nil {
					return
				}
				if ferr := bw.Flush(); ferr != nil {
					return
				}
			}
		}
		if err == io.EOF {
			return
		}
		if err != nil {
			logger.Printf("stdin read: %v", err)
			return
		}
	}
}

// handleLine parses one JSON-RPC line and returns the marshalled response
// bytes (without a trailing newline) plus a flag indicating whether a
// response should be written at all. The flag exists because JSON-RPC
// notifications — requests without an `id` — must not produce a reply.
func handleLine(c *hubClient, tools []toolDef, line []byte) ([]byte, bool) {
	// Strip trailing CR/LF before parsing so a blank line from a flaky
	// writer doesn't trip a parse error we'd then try to respond to
	// with a null id, which is itself malformed.
	trimmed := trimEOL(line)
	if len(trimmed) == 0 {
		return nil, false
	}
	var req jsonrpcReq
	if err := json.Unmarshal(trimmed, &req); err != nil {
		return marshalResp(jsonrpcResp{
			JSONRPC: "2.0",
			Error:   &jsonrpcError{Code: errParse, Message: "parse error", Data: err.Error()},
		}), true
	}
	// Notifications: no id, no reply. We still *process* them so a future
	// method with side effects works, but today all supported methods
	// demand a reply, so we just drop the notification.
	isNotification := len(req.ID) == 0

	result, rpcErr := dispatch(c, tools, req)
	if isNotification {
		return nil, false
	}
	resp := jsonrpcResp{JSONRPC: "2.0", ID: req.ID}
	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		resp.Result = result
	}
	return marshalResp(resp), true
}

// dispatch routes one parsed request to the matching handler. Keeping this
// separate from handleLine lets the test suite exercise routing without
// going through JSON framing.
func dispatch(c *hubClient, tools []toolDef, req jsonrpcReq) (any, *jsonrpcError) {
	switch req.Method {
	case "initialize":
		// MCP handshake. We return our tool list eagerly so clients that
		// skip tools/list (some do) still see the surface area.
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{
				"name":    serverName,
				"version": serverVersion,
			},
		}, nil

	case "initialized", "notifications/initialized":
		// Client acknowledgment of the handshake; nothing to do.
		return nil, nil

	case "tools/list":
		return map[string]any{"tools": tools}, nil

	case "tools/call":
		return handleToolsCall(c, tools, req.Params)

	case "ping":
		return map[string]any{}, nil

	default:
		return nil, &jsonrpcError{Code: errMethodNotFound, Message: "method not found: " + req.Method}
	}
}

// toolCallParams is the tools/call envelope. `Arguments` is optional per
// the MCP spec; omitting it means "call with no args".
type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// handleToolsCall decodes the envelope, dispatches to the matching tool,
// and wraps the return value in the MCP `content` shape. We serialize the
// adapter output as a JSON string inside a `text` content block because
// that is the most portable representation across MCP clients — some do
// not yet render structured `json` content blocks.
func handleToolsCall(c *hubClient, tools []toolDef, raw json.RawMessage) (any, *jsonrpcError) {
	var p toolCallParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &jsonrpcError{Code: errInvalidParams, Message: "invalid params", Data: err.Error()}
		}
	}
	if p.Name == "" {
		return nil, &jsonrpcError{Code: errInvalidParams, Message: "tool name required"}
	}
	tool, ok := findTool(tools, p.Name)
	if !ok {
		return nil, &jsonrpcError{Code: errMethodNotFound, Message: "unknown tool: " + p.Name}
	}
	out, err := tool.call(c, p.Arguments)
	if err != nil {
		// Per MCP convention, tool-level failures come back as an
		// isError=true content block rather than a JSON-RPC error, so
		// clients can still render the message in-line with the call.
		return map[string]any{
			"isError": true,
			"content": []any{
				map[string]any{"type": "text", "text": err.Error()},
			},
		}, nil
	}
	text, mErr := json.Marshal(out)
	if mErr != nil {
		return nil, &jsonrpcError{Code: errInternal, Message: "marshal tool result", Data: mErr.Error()}
	}
	return map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": string(text)},
		},
	}, nil
}

// marshalResp is a tiny wrapper that turns a jsonrpcResp into its wire form,
// swallowing errors into a best-effort parse-error frame. Marshal should
// not fail on our typed value but we prefer a reply to a crash.
func marshalResp(r jsonrpcResp) []byte {
	b, err := json.Marshal(r)
	if err != nil {
		fallback := `{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal marshal error"}}`
		return []byte(fallback)
	}
	return b
}

// trimEOL strips a single trailing \n or \r\n. We only trim trailing EOL,
// not leading whitespace, to avoid re-aligning a client's intentionally
// padded frame (unlikely but also not our concern).
func trimEOL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
