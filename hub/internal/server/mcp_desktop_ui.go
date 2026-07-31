// mcp_desktop_ui.go — D5 hub-relayed desktop UI context.
//
// The sibling of mcp_browser_bridge.go for the SECOND traffic class the
// desktop exposes: not the embedded browser tabs, but the desktop's own
// UI as an agent-addressable entity (ADR-062). An agent anywhere in the
// team asks a desktop what its user is looking at (`ui_get_focus`), or
// — per-call approved, always — for a frame of it (`ui_screenshot`);
// `desktop_ui_invoke` packages the call as a Kind="desktop.invoke"
// tunnel envelope and parks on the response, exactly as browser_invoke
// does with "browser.invoke" (ADR-028 D-1).
//
// Everything structural is shared with the browser class — the grant
// store, the approval parking, the tunnel routing — because a third
// class should cost ~zero (plan §3.6). What is NOT shared is the
// consent shape, and that is the point of this file:
//
//   - the capability key is `desktop_ui`, registered only while the
//     desktop's UI-context-sharing toggle is on. Bridge toggle +
//     sharing toggle + Remote-driving opt-in, all three, before a
//     remote agent can reach any of this (plan §3.6);
//   - session grants are keyed BY KIND, so a browser-driving grant
//     cannot silence a desktop card (the §3.6 review amendment);
//   - `ui_screenshot` consults no grant at all. It raises a
//     `desktop_action` card on every single call, and the card says so
//     — screenshots are the one artifact with no standing consent
//     (plan §3.3, ADR-062 D-4).
//
// The hub relays and never stores: no focus snapshot, no capture, ever
// lands in a table (ADR-062 D-7). What crosses is audited by the
// desktop's own ring + mirror, exactly as the browser class is.

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// desktopTunnelKind discriminates desktop-UI envelopes on the A2A
// reverse tunnel. The desktop's poll loop routes by Kind and refuses a
// tool that does not belong to the kind it arrived under.
const desktopTunnelKind = "desktop.invoke"

// desktopGrantKind namespaces this class's session grants. Present for
// symmetry and for the class's future action tools; `ui_screenshot`
// itself never consults it (see desktopUIGrantable).
const desktopGrantKind = "desktop_ui"

// desktopUICapability is the hosts.capabilities key the desktop sets
// while UI context sharing is on.
const desktopUICapability = "desktop_ui"

// The desktop-UI tools, split by risk class. Must match the desktop's
// own classification (browserbridge.ts) — the desktop enforces its own
// copy, so a skewed edit here only changes which calls the hub gates,
// never which the desktop permits.
var desktopUIReadTools = []string{
	"ui_get_focus",
}

var desktopUIActionTools = []string{
	"ui_screenshot",
}

func desktopUIToolClass(tool string) string {
	for _, n := range desktopUIReadTools {
		if n == tool {
			return "read"
		}
	}
	for _, n := range desktopUIActionTools {
		if n == tool {
			return "action"
		}
	}
	return ""
}

func desktopUIToolNames() []string {
	out := make([]string, 0, len(desktopUIReadTools)+len(desktopUIActionTools))
	out = append(out, desktopUIReadTools...)
	out = append(out, desktopUIActionTools...)
	return out
}

// desktopUIGrantable reports whether an action tool of this class may
// EVER ride a session grant. `ui_screenshot` may not: a frame of
// everything the user can see is the most sensitive artifact the app
// can emit, so consent is per call, forever (plan §3.3). Encoded as a
// predicate rather than a comment because the grant path is shared
// with a class that does offer session grants.
func desktopUIGrantable(tool string) bool {
	return tool != "ui_screenshot"
}

// ---------------------------------------------------------------------
// desktop_ui_invoke — native MCP tool
// ---------------------------------------------------------------------

type desktopUIInvokeArgs struct {
	HostID string          `json:"host_id"`
	Tool   string          `json:"tool"`
	Args   json.RawMessage `json:"args"`
}

func (s *Server) mcpDesktopUIInvoke(ctx context.Context, team, agentID string, raw json.RawMessage) (any, *jrpcError) {
	var a desktopUIInvokeArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.HostID == "" || a.Tool == "" {
		return nil, &jrpcError{Code: -32602, Message: "host_id and tool required"}
	}
	class := desktopUIToolClass(a.Tool)
	if class == "" {
		return nil, &jrpcError{Code: -32602, Message: fmt.Sprintf(
			"unknown desktop UI tool %q — must be one of: %s",
			a.Tool, strings.Join(desktopUIToolNames(), ", "))}
	}

	// The desktop must be registered in the caller's team, online, and
	// advertising desktop_ui — which it does only while UI context
	// sharing is on, so "the toggle is off" reads to the agent as a
	// missing capability rather than a mysterious refusal.
	var hostName, hostStatus, capsJSON string
	err := s.db.QueryRowContext(ctx, `
		SELECT name, status, COALESCE(capabilities_json, '')
		  FROM hosts WHERE team_id = ? AND id = ?`, team, a.HostID).
		Scan(&hostName, &hostStatus, &capsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return mcpResultError(fmt.Sprintf(
			"host %s not found in this team — call hosts_list and pick a host "+
				"whose capabilities show desktop_ui", a.HostID)), nil
	}
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	if hostStatus != "online" {
		return mcpResultError(fmt.Sprintf(
			"desktop %s (%s) is %s, not online — call hosts_list and pick an "+
				"online host whose capabilities show desktop_ui",
			hostName, a.HostID, hostStatus)), nil
	}
	if !hostCapable(capsJSON, desktopUICapability) {
		return mcpResultError(fmt.Sprintf(
			"host %s (%s) does not advertise the desktop_ui capability — its user "+
				"has UI context sharing off (Settings → Assistant), or remote driving "+
				"is not enabled. Call hosts_list to see which desktops do",
			hostName, a.HostID)), nil
	}

	agentHandle, _ := s.lookupHandleByID(ctx, team, agentID)

	// Action tools need a human decision. A grantable one may ride a
	// prior session grant for THIS class; ui_screenshot never does.
	if class == "action" {
		grantKind := ""
		if desktopUIGrantable(a.Tool) {
			grantKind = desktopGrantKind
		}
		if grantKind == "" || !s.bridgeGrants.has(grantKind, team, a.HostID, agentID) {
			deny, jerr := s.requestBridgeApproval(ctx, bridgeApproval{
				Team:          team,
				AgentID:       agentID,
				AgentHandle:   agentHandle,
				HostID:        a.HostID,
				HostName:      hostName,
				Tool:          a.Tool,
				Args:          a.Args,
				AttentionKind: "desktop_action",
				GrantKind:     grantKind,
			})
			if jerr != nil {
				return nil, jerr
			}
			if deny != nil {
				return deny, nil
			}
		}
	}
	return s.routeTunnelInvoke(ctx, desktopTunnelKind, agentID, agentHandle, a.HostID, a.Tool, a.Args)
}

// hostCapable reads the truthiness of one capabilities_json key. The
// desktop writes a JSON true; the other JSON-truthy shapes are
// tolerated so a hand-rolled capabilities row can't silently strand a
// feature. (Generalized from browserBridgeCapable — same rule, two
// keys.)
func hostCapable(capsJSON, key string) bool {
	var caps map[string]any
	if err := json.Unmarshal([]byte(capsJSON), &caps); err != nil {
		return false
	}
	switch v := caps[key].(type) {
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
