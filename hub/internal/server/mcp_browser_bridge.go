// mcp_browser_bridge.go — W3 hub-relayed agent browser bridge.
//
// The Electron desktop runs a "browser bridge": an MCP server in the
// main process exposing 12 browser_* tools that drive the desktop's
// embedded browser tabs (<webview> guests over CDP). Until W3 only
// agents on the SAME host could reach it (localhost relay). W3 lets an
// agent on any host drive a desktop's browser THROUGH the hub: the
// desktop registers a hosts row with capabilities.browser_bridge=true
// and long-polls the A2A reverse tunnel; browser_invoke here packages
// the call as a Kind="browser.invoke" tunnel envelope and parks on the
// response, exactly like the A2A relay (ADR-028 D-1).
//
// Safety posture: read tools route immediately. Action tools are gated
// by a hub-side approval card (attention_items kind='browser_action')
// the first time per (desktop, agent) pair; a principal approve with
// option_id="session" records an in-memory session grant so subsequent
// action calls from the same agent to the same desktop route without
// re-prompting. Grants are revocable via
// POST /v1/teams/{team}/hosts/{host}/browserbridge/revoke and die with
// the hub process (same posture as TunnelManager's queues).

package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/termipod/hub/internal/auth"
)

// browserTunnelKind discriminates browser-bridge envelopes on the A2A
// reverse tunnel. The desktop's poll loop routes by Kind and decodes
// Payload; an unknown Kind comes back as a transport error.
const browserTunnelKind = "browser.invoke"

// browserInvokeTimeout caps one hub→desktop round trip. Tighter than
// the approval wait; long enough for a navigate + CDP evaluation.
const browserInvokeTimeout = 60 * time.Second

// browserApprovalTimeout caps how long the MCP call stays parked on
// the approval card. Mirrors permission_prompt's 10-minute fail-closed
// window: on timeout the row is auto-resolved and the call denied.
const browserApprovalTimeout = 10 * time.Minute

// The 12 desktop browser tools, split by risk class. Must match the
// desktop's bridge exactly — the desktop enforces its own copy of this
// classification, so a skewed edit here only changes which calls the
// hub gates, never which the desktop permits.
var browserReadTools = []string{
	"browser_list_tabs",
	"browser_snapshot",
	"browser_screenshot",
	"browser_read_text",
}

var browserActionTools = []string{
	"browser_navigate",
	"browser_find_tab",
	"browser_click",
	"browser_type",
	"browser_send_keys",
	"browser_scroll",
	"browser_upload_file",
	"browser_eval",
}

// browserToolClass returns "read" or "action" for a known tool name,
// or "" for anything else.
func browserToolClass(tool string) string {
	for _, n := range browserReadTools {
		if n == tool {
			return "read"
		}
	}
	for _, n := range browserActionTools {
		if n == tool {
			return "action"
		}
	}
	return ""
}

// browserToolNames lists every known browser tool, reads first. Used
// in the tool schema enum and the invalid-params error message.
func browserToolNames() []string {
	out := make([]string, 0, len(browserReadTools)+len(browserActionTools))
	out = append(out, browserReadTools...)
	out = append(out, browserActionTools...)
	return out
}

// browserGrantStore is the session-grant cache for action-tool
// approvals (W3). Keyed team|hostID|agentID; presence means the
// principal approved with option_id="session" and this agent may run
// action tools against that desktop without further prompts. No
// expiry — a hub restart clears it (same posture as TunnelManager's
// in-memory state); revocation is explicit via the revoke endpoint.
type browserGrantStore struct {
	m sync.Map // "team|hostID|agentID" → struct{}{}
}

func browserGrantKey(team, hostID, agentID string) string {
	return team + "|" + hostID + "|" + agentID
}

func (g *browserGrantStore) has(team, hostID, agentID string) bool {
	_, ok := g.m.Load(browserGrantKey(team, hostID, agentID))
	return ok
}

func (g *browserGrantStore) grant(team, hostID, agentID string) {
	g.m.Store(browserGrantKey(team, hostID, agentID), struct{}{})
}

// revoke drops one (team, host, agent) grant; an empty agentID clears
// every grant for the (team, host) pair — the desktop's own "forget
// all sessions" path.
func (g *browserGrantStore) revoke(team, hostID, agentID string) {
	if agentID != "" {
		g.m.Delete(browserGrantKey(team, hostID, agentID))
		return
	}
	prefix := team + "|" + hostID + "|"
	g.m.Range(func(k, _ any) bool {
		if ks, ok := k.(string); ok && strings.HasPrefix(ks, prefix) {
			g.m.Delete(k)
		}
		return true
	})
}

// ---------------------------------------------------------------------
// browser_invoke — native MCP tool
// ---------------------------------------------------------------------

type browserInvokeArgs struct {
	HostID string          `json:"host_id"`
	Tool   string          `json:"tool"`
	Args   json.RawMessage `json:"args"`
}

func (s *Server) mcpBrowserInvoke(ctx context.Context, team, agentID string, raw json.RawMessage) (any, *jrpcError) {
	var a browserInvokeArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.HostID == "" || a.Tool == "" {
		return nil, &jrpcError{Code: -32602, Message: "host_id and tool required"}
	}
	class := browserToolClass(a.Tool)
	if class == "" {
		return nil, &jrpcError{Code: -32602, Message: fmt.Sprintf(
			"unknown browser tool %q — must be one of: %s",
			a.Tool, strings.Join(browserToolNames(), ", "))}
	}

	// The desktop must be registered in the caller's team, online (it
	// heartbeats + long-polls the tunnel only while up), and carrying
	// the browser_bridge capability. Each failure points the agent at
	// hosts_list so it can re-discover rather than retry blindly.
	var hostName, hostStatus, capsJSON string
	err := s.db.QueryRowContext(ctx, `
		SELECT name, status, COALESCE(capabilities_json, '')
		  FROM hosts WHERE team_id = ? AND id = ?`, team, a.HostID).
		Scan(&hostName, &hostStatus, &capsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return mcpResultError(fmt.Sprintf(
			"host %s not found in this team — call hosts_list and pick a host "+
				"whose capabilities show browser_bridge", a.HostID)), nil
	}
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	if hostStatus != "online" {
		return mcpResultError(fmt.Sprintf(
			"desktop %s (%s) is %s, not online — call hosts_list and pick an "+
				"online host whose capabilities show browser_bridge",
			hostName, a.HostID, hostStatus)), nil
	}
	if !browserBridgeCapable(capsJSON) {
		return mcpResultError(fmt.Sprintf(
			"host %s (%s) does not advertise the browser_bridge capability — "+
				"call hosts_list and pick a host whose capabilities show browser_bridge",
			hostName, a.HostID)), nil
	}

	// Best-effort handle for the approval summary + tunnel payload; a
	// caller without an agents row still works, just less legibly.
	agentHandle, _ := s.lookupHandleByID(ctx, team, agentID)

	// Action tools need a human decision unless the principal already
	// granted this (desktop, agent) session.
	if class == "action" && !s.browserGrants.has(team, a.HostID, agentID) {
		deny, jerr := s.requestBrowserApproval(ctx, team, agentID, agentHandle,
			a.HostID, hostName, a.Tool, a.Args)
		if jerr != nil {
			return nil, jerr
		}
		if deny != nil {
			return deny, nil
		}
	}
	return s.routeBrowserInvoke(ctx, agentID, agentHandle, a.HostID, a.Tool, a.Args)
}

// requestBrowserApproval raises the browser_action approval card and
// parks on its resolution. Returns (nil, nil) when the call may
// proceed (approve), (denyResult, nil) when the principal rejected or
// the wait timed out, or (nil, jrpcError) on an infrastructure fault.
// A session-option approve records the grant before returning.
func (s *Server) requestBrowserApproval(ctx context.Context, team, agentID, agentHandle, hostID, hostName, tool string, args json.RawMessage) (any, *jrpcError) {
	displayAgent := agentHandle
	if displayAgent == "" {
		displayAgent = agentID
	}
	summary := fmt.Sprintf("Agent %s wants to run %s on desktop %s", displayAgent, tool, hostName)
	payload, _ := json.Marshal(map[string]any{
		"host_id":   hostID,
		"host_name": hostName,
		"tool":      tool,
		"args":      redactBrowserArgs(args),
		"agent_id":  agentID,
	})

	id := NewID()
	now := NowUTC()
	// Same insert shape as mcpPermissionPrompt: team-scoped, severity
	// minor, team-wide-addressed, tier NULL (decide quorum = 1). The
	// waiter parks in-process below, so browser_action deliberately
	// stays OUT of attentionAwaitsAgentReply — no fan-back wanted.
	if _, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json, status, created_at,
			actor_kind, actor_handle, pending_payload_json
		) VALUES (?, NULL, 'team', ?, 'browser_action',
		          ?, 'minor', '[]', 'open', ?,
		          'agent', NULLIF(?, ''), ?)`,
		id, team, summary, now, agentHandle, string(payload),
	); err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	s.recordAudit(ctx, team, "browser_action.request", "attention", id,
		"browser action awaiting approval: "+tool+" on "+hostName,
		map[string]any{"tool": tool, "host_id": hostID, "agent_id": agentID})

	pctx, cancel := context.WithTimeout(ctx, browserApprovalTimeout)
	defer cancel()

	last, err := s.waitForAttentionResolution(pctx, id)
	if err != nil {
		// Timeout / context cancel — fail closed (deny), mirroring
		// mcpPermissionPrompt: auto-resolve the row so it doesn't
		// loiter in the inbox; the audit trail keeps the no-decision
		// outcome.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			_, _ = s.writeDB.ExecContext(context.Background(), `
				UPDATE attention_items
				   SET status = 'resolved', resolved_at = ?
				 WHERE id = ? AND status = 'open'`, NowUTC(), id)
			return mcpResultJSON(map[string]any{
				"behavior": "deny",
				"message":  "no decision within timeout — denied",
			}), nil
		}
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}

	decision, _ := last["decision"].(string)
	if decision == "approve" {
		// option_id="session" upgrades this approve to a session grant:
		// subsequent action calls from this agent to this desktop skip
		// the card until revoked or the hub restarts.
		if optionID, _ := last["option_id"].(string); optionID == "session" {
			s.browserGrants.grant(team, hostID, agentID)
		}
		return nil, nil
	}
	reason, _ := last["reason"].(string)
	if reason == "" {
		reason = "user denied"
	}
	return mcpResultJSON(map[string]any{
		"behavior": "deny",
		"message":  reason,
	}), nil
}

// routeBrowserInvoke packages one call as a browser.invoke tunnel
// envelope and parks on the desktop's response. The desktop answers
// Status 200 with BodyB64 = base64({"ok":true,"result":...} or
// {"ok":false,"error":"..."}); a non-200 Status is a transport-level
// failure and is treated as an error too. Failures come back as MCP
// error results (agent-visible, retryable), not protocol faults.
func (s *Server) routeBrowserInvoke(ctx context.Context, agentID, agentHandle, hostID, tool string, args json.RawMessage) (any, *jrpcError) {
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage(`{}`)
	}
	payload, _ := json.Marshal(map[string]any{
		"tool":         tool,
		"args":         json.RawMessage(args),
		"agent_id":     agentID,
		"agent_handle": agentHandle,
	})

	rctx, cancel := context.WithTimeout(ctx, browserInvokeTimeout)
	defer cancel()
	resp, err := s.tunnel.enqueueAndWait(rctx, hostID, &tunnelRequest{
		ReqID:   NewID(),
		Kind:    browserTunnelKind,
		Payload: payload,
	})
	if err != nil {
		return mcpResultError("desktop did not answer browser.invoke: " + err.Error()), nil
	}
	if resp.Status != 0 && resp.Status != http.StatusOK {
		return mcpResultError(fmt.Sprintf(
			"desktop browser bridge returned transport status %d", resp.Status)), nil
	}
	body, err := base64.StdEncoding.DecodeString(resp.BodyB64)
	if err != nil {
		return mcpResultError("desktop response body not base64: " + err.Error()), nil
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return mcpResultError("desktop response is not the {ok,result|error} envelope: " + err.Error()), nil
	}
	if !env.OK {
		if env.Error == "" {
			env.Error = "desktop reported failure without a message"
		}
		return mcpResultError(env.Error), nil
	}
	var result any
	if len(env.Result) > 0 {
		_ = json.Unmarshal(env.Result, &result)
	}
	return mcpResultJSON(result), nil
}

// browserBridgeCapable reads the truthiness of capabilities_json's
// browser_bridge key. The desktop writes a JSON true; tolerate the
// other JSON-truthy shapes (non-empty string, non-zero number) so a
// hand-rolled capabilities row can't silently strand the feature.
func browserBridgeCapable(capsJSON string) bool {
	var caps map[string]any
	if err := json.Unmarshal([]byte(capsJSON), &caps); err != nil {
		return false
	}
	switch v := caps["browser_bridge"].(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0
	}
	return true
}

// redactBrowserArgs returns the tool args as a map with the VALUES of
// the `text` and `keys` keys replaced by "[redacted N chars]" — typed
// content (passwords, message text, keystrokes) shouldn't sit in an
// approval card. Everything else (urls, selectors, the eval script)
// stays visible: the approver needs it to judge the action.
func redactBrowserArgs(raw json.RawMessage) map[string]any {
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	for _, k := range []string{"text", "keys"} {
		v, ok := out[k]
		if !ok {
			continue
		}
		n := 0
		switch t := v.(type) {
		case string:
			n = len(t)
		default:
			// Non-string shapes (e.g. keys as an array of chord
			// tokens) are measured by their JSON rendering.
			if b, err := json.Marshal(t); err == nil {
				n = len(b)
			}
		}
		out[k] = fmt.Sprintf("[redacted %d chars]", n)
	}
	return out
}

// ---------------------------------------------------------------------
// Revoke endpoint
// ---------------------------------------------------------------------

// handleBrowserBridgeRevoke clears session grants for this (team,
// host). Body {agent_id} drops that agent's grant; an empty agent_id
// clears every grant against the host. Returns 204 either way —
// revoking a grant that never existed is not an error.
func (s *Server) handleBrowserBridgeRevoke(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	host := chi.URLParam(r, "host")

	// Grants are principal decisions; a host-kind deputy token (the
	// desktop's own runner credential) must not erase them. Owner /
	// user / operator pass — the desktop calls this with a user token.
	if tok, ok := auth.FromContext(r.Context()); ok && tok != nil && tok.Kind == "host" {
		writeErr(w, http.StatusForbidden,
			"host tokens may not revoke browser bridge grants")
		return
	}

	var in struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed body: "+err.Error())
		return
	}
	s.browserGrants.revoke(team, host, in.AgentID)
	w.WriteHeader(http.StatusNoContent)
}
