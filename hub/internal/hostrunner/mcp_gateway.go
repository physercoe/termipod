// MCP gateway (blueprint §5.1, §5.2, §9 P1.6).
//
// Each agent spawn gets a local MCP endpoint served by host-runner over a
// Unix-domain socket. The gateway exposes two tool namespaces:
//
//   hub.*   — forwarded to the hub REST API using host-runner's single
//             persistent hub token. Host-runner stamps the agent's identity
//             via the X-Agent-Id header so the audit trail stays per-agent.
//   host.*  — local capabilities. This pass implements only host.ping as a
//             wiring proof; the full surface (pane/capture, shell/exec, …)
//             lands with the host-capability work.
//
// Transport: MCP stdio framing (one JSON-RPC object per line, LF delimited)
// carried over a per-agent UDS. That matches the wire shape already used by
// cmd/hub-mcp-bridge so adapters that know stdio MCP work unchanged.
//
// Scope notes for this pass:
//   - UDS only; TCP fallback is deferred.
//   - Three forwarded hub tools (agent_event_post, document_create,
//     review_create) — the highest-value outbound calls from an agent.
//   - StartGateway is exposed but not wired into the spawn path. P1.1 and
//     the parent coordinator will plumb HOST_MCP_URL into the child env.
//   - Socket path uses os.TempDir() for testability; production deploys
//     should switch to /run/termipod/ (see socketPath below).
package hostrunner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// socketPath returns the UDS path for a given agent. We keep this in
// os.TempDir() so tests (which can't always write to /run) still work;
// a production deployment should override via a host-runner config field
// once the launcher is wired up. Short enough to stay under the 108-byte
// sun_path limit for any reasonable agent id.
func socketPath(agentID string) string {
	return filepath.Join(os.TempDir(), "termipod-agent-"+agentID+".sock")
}

// McpGateway is the running gateway for a single agent. Callers obtain one
// from StartGateway and call Close (or cancel the parent ctx) to stop it.
type McpGateway struct {
	AgentID    string
	Endpoint   string // "unix:///tmp/termipod-agent-<id>.sock"
	sockPath   string // filesystem path (captured so Close can remove after listener shuts)
	listener   net.Listener
	hubClient  *Client
	hubURL     string // base URL for forwarded requests (http.Client)
	httpClient *http.Client

	// HookSink, when non-nil, is invoked by the 9 hook tool handlers
	// (ADR-027 W5b). The runner wires this for M4 LocalLogTail spawns
	// only; M1/M2/non-claude M4 spawns leave it nil and the hook tools
	// return -32601 method-not-found (defensive — settings.local.json
	// won't reference them for those spawns either).
	HookSink HookSink

	mu     sync.Mutex
	closed bool
	conns  map[net.Conn]struct{}
	wg     sync.WaitGroup
}

// HookSink is the gateway-side seam to the LocalLogTailDriver
// (ADR-027 W5b). Signature mirrors locallogtail.HookSink so a
// *locallogtail.Driver satisfies it by structural typing — no import
// cycle. The sink is responsible for posting any derived agent_event
// to the hub (via its own configured Poster); the return is solely
// the JSON-RPC body the gateway relays to claude-code.
type HookSink interface {
	OnHook(ctx context.Context, name string, payload map[string]any) (map[string]any, error)
}

// StartGateway starts a per-agent MCP gateway on a UDS. The returned cleanup
// func stops the accept loop and removes the socket; it is also triggered
// automatically when ctx is cancelled.
//
// hubClient may be nil in tests where no forwarding is exercised; the host.*
// namespace still works and hub.* tools return a "hub client not configured"
// error when called.
func StartGateway(ctx context.Context, agentID string, hubClient *Client) (*McpGateway, func(), error) {
	if agentID == "" {
		return nil, nil, errors.New("agentID required")
	}
	path := socketPath(agentID)
	// Best-effort cleanup of a stale socket left by a crashed predecessor.
	// net.Listen would otherwise fail with "address already in use".
	_ = os.Remove(path)

	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, nil, fmt.Errorf("listen %s: %w", path, err)
	}
	// 0600: only the host-runner user and its spawned agent (same uid) can
	// open the socket. Defence in depth — the token never leaves this
	// process, but we still don't want arbitrary local readers.
	_ = os.Chmod(path, 0o600)

	g := &McpGateway{
		AgentID:    agentID,
		Endpoint:   "unix://" + path,
		sockPath:   path,
		listener:   l,
		hubClient:  hubClient,
		httpClient: &http.Client{}, // no timeout: honour caller ctx instead
		conns:      make(map[net.Conn]struct{}),
	}
	if hubClient != nil {
		g.hubURL = strings.TrimRight(hubClient.BaseURL, "/")
	}

	g.wg.Add(1)
	go g.acceptLoop()

	// Stop on ctx cancel without requiring the caller to remember cleanup.
	stopCtx := context.AfterFunc(ctx, func() { _ = g.Close() })

	cleanup := func() {
		stopCtx()
		_ = g.Close()
	}
	return g, cleanup, nil
}

// Close stops the accept loop, closes the listener, and removes the socket.
// Idempotent so ctx-triggered and explicit cleanup paths can both fire.
func (g *McpGateway) Close() error {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil
	}
	g.closed = true
	g.mu.Unlock()

	err := g.listener.Close()
	// Listener.Close doesn't remove the socket file on Linux.
	if g.sockPath != "" {
		_ = os.Remove(g.sockPath)
	}
	// Unblock any serveConn goroutines parked on ReadBytes by closing the
	// active conns; Close() otherwise deadlocks waiting on clients that
	// never shut their end (e.g. tests that tear down via cleanup()).
	g.mu.Lock()
	for c := range g.conns {
		_ = c.Close()
	}
	g.mu.Unlock()
	g.wg.Wait()
	return err
}

func (g *McpGateway) acceptLoop() {
	defer g.wg.Done()
	for {
		conn, err := g.listener.Accept()
		if err != nil {
			// Either we were closed (expected) or the listener has failed;
			// either way, the only sensible move is to stop accepting.
			return
		}
		g.mu.Lock()
		if g.closed {
			g.mu.Unlock()
			_ = conn.Close()
			return
		}
		g.conns[conn] = struct{}{}
		g.mu.Unlock()
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			defer func() {
				g.mu.Lock()
				delete(g.conns, conn)
				g.mu.Unlock()
				_ = conn.Close()
			}()
			g.serveConn(conn)
		}()
	}
}

// serveConn reads newline-delimited JSON-RPC requests and writes responses
// on the same connection. We stay on a single goroutine per connection —
// MCP stdio clients are inherently sequential.
func (g *McpGateway) serveConn(conn net.Conn) {
	br := bufio.NewReader(conn)
	bw := bufio.NewWriter(conn)
	defer bw.Flush()

	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			resp := g.handleLine(bytes.TrimRight(line, "\r\n"))
			if resp != nil {
				if _, werr := bw.Write(resp); werr != nil {
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
			return
		}
	}
}

// --- JSON-RPC framing (mirrors hub/internal/server/mcp.go) ---

type gwReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type gwResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *gwRespError    `json:"error,omitempty"`
}

type gwRespError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const gwProtocolVersion = "2024-11-05"

func (g *McpGateway) handleLine(line []byte) []byte {
	if len(line) == 0 {
		return nil
	}
	var req gwReq
	if err := json.Unmarshal(line, &req); err != nil {
		return encodeResp(gwResp{
			JSONRPC: "2.0",
			Error:   &gwRespError{Code: -32700, Message: "parse error"},
		})
	}
	// Notifications (no id) get no response by JSON-RPC 2.0 rules.
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		if isNotification {
			return nil
		}
		return encodeResp(gwResp{
			JSONRPC: "2.0", ID: req.ID,
			Result: map[string]any{
				"protocolVersion": gwProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]any{
					"name":    "termipod-host-runner-gateway",
					"version": "0.1",
				},
			},
		})
	case "notifications/initialized":
		return nil
	case "tools/list":
		if isNotification {
			return nil
		}
		return encodeResp(gwResp{
			JSONRPC: "2.0", ID: req.ID,
			Result: map[string]any{"tools": gatewayToolDefs()},
		})
	case "tools/call":
		result, jerr := g.dispatchTool(req.Params)
		if isNotification {
			return nil
		}
		resp := gwResp{JSONRPC: "2.0", ID: req.ID}
		if jerr != nil {
			resp.Error = jerr
		} else {
			resp.Result = result
		}
		return encodeResp(resp)
	case "ping":
		if isNotification {
			return nil
		}
		return encodeResp(gwResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	default:
		if isNotification {
			return nil
		}
		return encodeResp(gwResp{
			JSONRPC: "2.0", ID: req.ID,
			Error: &gwRespError{Code: -32601, Message: "method not found: " + req.Method},
		})
	}
}

func encodeResp(r gwResp) []byte {
	b, _ := json.Marshal(r)
	return b
}

// --- Tool catalog ---

// claudeHookToolDefs returns the 9 ADR-027 hook tools (W5b). They live
// alongside the existing host.* and hub.* tools in gatewayToolDefs.
// Input schemas are intentionally loose (`type:object`, no required
// fields): claude-code's hook payloads are the source of truth and we
// don't want a schema mismatch to silently drop a hook call. The
// per-event payload shapes are documented in
// docs/reference/claude-code-hook-schema.md and parsed dynamically by
// the adapter.
func claudeHookToolDefs() []map[string]any {
	names := []string{
		"hook_pre_tool_use",
		"hook_post_tool_use",
		"hook_notification",
		"hook_pre_compact",
		"hook_stop",
		"hook_subagent_stop",
		"hook_user_prompt",
		"hook_session_start",
		"hook_session_end",
	}
	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]any{
			"name":        n,
			"description": "claude-code " + n + " hook (ADR-027 W5b); driven by the LocalLogTailDriver.",
			"inputSchema": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": true,
			},
		})
	}
	return out
}

// claudeHookToolNames returns the set of tool names installed by
// claudeHookToolDefs. dispatchTool consults this to route hook calls
// to the HookSink.
var claudeHookToolNames = map[string]struct{}{
	"hook_pre_tool_use":  {},
	"hook_post_tool_use": {},
	"hook_notification":  {},
	"hook_pre_compact":   {},
	"hook_stop":          {},
	"hook_subagent_stop": {},
	"hook_user_prompt":   {},
	"hook_session_start": {},
	"hook_session_end":   {},
}

func gatewayToolDefs() []map[string]any {
	defs := []map[string]any{
		{
			"name":        "host.ping",
			"description": "Liveness check for the host-runner MCP gateway.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			"name": "hub.agent_event_post",
			"description": "Forward an event to a hub channel. Caller supplies " +
				"project_id, channel_id, and an EventIn payload. Host-runner " +
				"stamps the agent's identity via X-Agent-Id.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_id": map[string]any{"type": "string"},
					"channel_id": map[string]any{"type": "string"},
					"type":       map[string]any{"type": "string"},
					"parts":      map[string]any{"type": "array"},
				},
				"required": []string{"project_id", "channel_id", "type"},
			},
		},
		{
			"name":        "hub.document_create",
			"description": "Create a document (memo/draft/report/review) on the hub.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_id":     map[string]any{"type": "string"},
					"kind":           map[string]any{"type": "string"},
					"title":          map[string]any{"type": "string"},
					"content_inline": map[string]any{"type": "string"},
					"artifact_id":    map[string]any{"type": "string"},
				},
				"required": []string{"project_id", "kind", "title"},
			},
		},
		{
			"name":        "hub.review_create",
			"description": "Request a review of a document or artifact.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_id":  map[string]any{"type": "string"},
					"target_kind": map[string]any{"type": "string"},
					"target_id":   map[string]any{"type": "string"},
					"comment":     map[string]any{"type": "string"},
				},
				"required": []string{"project_id", "target_kind", "target_id"},
			},
		},
	}
	defs = append(defs, claudeHookToolDefs()...)
	return defs
}

type gwToolCallIn struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (g *McpGateway) dispatchTool(params json.RawMessage) (any, *gwRespError) {
	var call gwToolCallIn
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, &gwRespError{Code: -32602, Message: "invalid params"}
	}
	switch call.Name {
	case "host.ping":
		return mcpGWResultJSON(map[string]any{
			"ok":       true,
			"agent_id": g.AgentID,
		}), nil

	case "hub.agent_event_post":
		return g.forwardEventPost(call.Arguments)
	case "hub.document_create":
		return g.forwardDocumentCreate(call.Arguments)
	case "hub.review_create":
		return g.forwardReviewCreate(call.Arguments)

	default:
		if _, isHook := claudeHookToolNames[call.Name]; isHook {
			return g.dispatchHookTool(call.Name, call.Arguments)
		}
		return nil, &gwRespError{Code: -32601, Message: "unknown tool: " + call.Name}
	}
}

// claudeHookEventByTool maps the gateway tool name to the claude-code
// hook event name the adapter expects. The two differ only in style
// (snake_case vs PascalCase) but the adapter's state machine keys on
// the event name, so we translate at the gateway boundary.
var claudeHookEventByTool = map[string]string{
	"hook_pre_tool_use":  "PreToolUse",
	"hook_post_tool_use": "PostToolUse",
	"hook_notification":  "Notification",
	"hook_pre_compact":   "PreCompact",
	"hook_stop":          "Stop",
	"hook_subagent_stop": "SubagentStop",
	"hook_user_prompt":   "UserPromptSubmit",
	"hook_session_start": "SessionStart",
	"hook_session_end":   "SessionEnd",
}

// dispatchHookTool routes a hook MCP call to the configured HookSink
// (ADR-027 W5b). If no sink is wired (M1/M2 spawn or runner failed to
// configure one) the call returns -32601; that's defensive — for a
// spawn that doesn't reference these tools in settings.local.json the
// call shouldn't happen in the first place.
//
// The sink is responsible for posting any derived agent_event to hub
// via its own Poster; the return is purely the JSON-RPC body the
// gateway relays. Parking (for PreCompact + AskUserQuestion) happens
// inside the sink — dispatchHookTool will block as long as the sink
// blocks, which is the desired contract for the `mcp_tool` hook type
// in claude-code's settings.local.json.
func (g *McpGateway) dispatchHookTool(name string, raw json.RawMessage) (any, *gwRespError) {
	if g.HookSink == nil {
		return nil, &gwRespError{Code: -32601,
			Message: "hook tool " + name + " not wired (no HookSink); " +
				"spawn is not configured for ADR-027 LocalLogTailDriver"}
	}
	event := claudeHookEventByTool[name]
	if event == "" {
		return nil, &gwRespError{Code: -32601, Message: "unknown hook tool: " + name}
	}
	var payload map[string]any
	if len(raw) > 0 {
		// claude-code passes the hook payload as the `arguments` object;
		// per Anthropic's contract every hook receives a JSON object,
		// possibly empty. We tolerate empty/missing by treating it as
		// an empty map rather than rejecting the call.
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, &gwRespError{Code: -32602,
				Message: "hook " + name + " payload parse: " + err.Error()}
		}
	}
	if payload == nil {
		payload = map[string]any{}
	}
	resp, err := g.HookSink.OnHook(context.Background(), event, payload)
	if err != nil {
		return nil, &gwRespError{Code: -32000,
			Message: "hook " + name + ": " + err.Error()}
	}
	if resp == nil {
		resp = map[string]any{}
	}
	return mcpGWResultJSON(resp), nil
}

// --- hub.* forwards ---

type eventPostArgs struct {
	ProjectID string          `json:"project_id"`
	ChannelID string          `json:"channel_id"`
	Type      string          `json:"type"`
	Parts     json.RawMessage `json:"parts,omitempty"`
}

func (g *McpGateway) forwardEventPost(raw json.RawMessage) (any, *gwRespError) {
	var a eventPostArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &gwRespError{Code: -32602, Message: "invalid args"}
	}
	if a.ProjectID == "" || a.ChannelID == "" || a.Type == "" {
		return nil, &gwRespError{Code: -32602, Message: "project_id, channel_id, type required"}
	}
	body := map[string]any{"type": a.Type}
	if len(a.Parts) > 0 {
		body["parts"] = json.RawMessage(a.Parts)
	}
	// from_id is deliberately NOT set from agent input — the hub derives
	// identity from X-Agent-Id that host-runner stamps below.
	path := fmt.Sprintf("/v1/teams/%s/projects/%s/channels/%s/events",
		g.teamID(), a.ProjectID, a.ChannelID)
	return g.forwardJSON(http.MethodPost, path, body)
}

type documentCreateArgs struct {
	ProjectID     string `json:"project_id"`
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	ContentInline string `json:"content_inline,omitempty"`
	ArtifactID    string `json:"artifact_id,omitempty"`
	PrevVersionID string `json:"prev_version_id,omitempty"`
}

func (g *McpGateway) forwardDocumentCreate(raw json.RawMessage) (any, *gwRespError) {
	var a documentCreateArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &gwRespError{Code: -32602, Message: "invalid args"}
	}
	if a.ProjectID == "" || a.Kind == "" || a.Title == "" {
		return nil, &gwRespError{Code: -32602, Message: "project_id, kind, title required"}
	}
	body := map[string]any{
		"project_id": a.ProjectID,
		"kind":       a.Kind,
		"title":      a.Title,
	}
	if a.ContentInline != "" {
		body["content_inline"] = a.ContentInline
	}
	if a.ArtifactID != "" {
		body["artifact_id"] = a.ArtifactID
	}
	if a.PrevVersionID != "" {
		body["prev_version_id"] = a.PrevVersionID
	}
	// author_agent_id is stamped server-side from X-Agent-Id; see the note
	// for parent when they wire the header-aware path in server.go.
	path := fmt.Sprintf("/v1/teams/%s/documents", g.teamID())
	return g.forwardJSON(http.MethodPost, path, body)
}

type reviewCreateArgs struct {
	ProjectID  string `json:"project_id"`
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id"`
	Comment    string `json:"comment,omitempty"`
}

func (g *McpGateway) forwardReviewCreate(raw json.RawMessage) (any, *gwRespError) {
	var a reviewCreateArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &gwRespError{Code: -32602, Message: "invalid args"}
	}
	if a.ProjectID == "" || a.TargetKind == "" || a.TargetID == "" {
		return nil, &gwRespError{Code: -32602, Message: "project_id, target_kind, target_id required"}
	}
	body := map[string]any{
		"project_id":  a.ProjectID,
		"target_kind": a.TargetKind,
		"target_id":   a.TargetID,
	}
	if a.Comment != "" {
		body["comment"] = a.Comment
	}
	path := fmt.Sprintf("/v1/teams/%s/reviews", g.teamID())
	return g.forwardJSON(http.MethodPost, path, body)
}

// forwardJSON is the single point where credential injection happens. The
// agent never sees the hub bearer token; we drop anything inbound and
// attach our own. X-Agent-Id carries the identity host-runner resolved
// from the socket's owning agent.
func (g *McpGateway) forwardJSON(method, path string, body any) (any, *gwRespError) {
	if g.hubClient == nil || g.hubURL == "" {
		return nil, &gwRespError{Code: -32000, Message: "hub client not configured"}
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, &gwRespError{Code: -32000, Message: "marshal: " + err.Error()}
	}
	req, err := http.NewRequest(method, g.hubURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, &gwRespError{Code: -32000, Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.hubClient.Token)
	req.Header.Set("X-Agent-Id", g.AgentID)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, &gwRespError{Code: -32000, Message: err.Error()}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, &gwRespError{
			Code:    -32000,
			Message: fmt.Sprintf("hub %d: %s", resp.StatusCode, bytes.TrimSpace(respBody)),
		}
	}
	// Passthrough: whatever JSON the hub returned, surface it as the tool
	// result so the agent can chain on e.g. the returned document id.
	var parsed any
	if len(bytes.TrimSpace(respBody)) > 0 {
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			// Non-JSON body — return as text.
			return mcpGWResultText(string(respBody)), nil
		}
	}
	return mcpGWResultJSON(parsed), nil
}

func (g *McpGateway) teamID() string {
	if g.hubClient == nil {
		return ""
	}
	return g.hubClient.Team
}

// --- result formatters (MCP convention: content=[{type:"text",text:...}]) ---

func mcpGWResultText(s string) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": s}},
	}
}

func mcpGWResultJSON(v any) map[string]any {
	b, _ := json.MarshalIndent(v, "", "  ")
	return mcpGWResultText(string(b))
}
