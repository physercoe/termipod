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
//     (plan §3.3, ADR-062 D-4);
//   - `ui_highlight` raises NO card. It is non-actuating annotation —
//     it draws an attributed glow that expires and takes no action with
//     the user's authority — so ADR-062 D-5 puts its consent in the
//     sharing toggle plus the policy table's `highlight` bit, and its
//     safety in the desktop's rate limit and the audit trail;
//   - `author_read` / `author_render` / `author_apply` (ADR-064,
//     coworking lanes A and W2) join the class: the Author surface's
//     documents, read, drawn and written through the same tunnel.
//     `author_apply` is an ACTION and is carded per call here, because
//     the consent the desktop offers locally is per DOCUMENT and this
//     grant store's key has no document in it — see desktopUIGrantable.
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
// symmetry and for the class's future action tools; neither
// `ui_screenshot` nor `author_apply` consults it (see
// desktopUIGrantable).
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
	// ADR-064 lane A. A read of the user's own documents: it discloses
	// bytes rather than ids, so the desktop audits it on every leg, but
	// it changes nothing and takes no card.
	"author_read",
	// ADR-064, W2. A PICTURE of one document, drawn from that document.
	// It looks like `ui_screenshot` and is not: a screenshot is a frame
	// of the user's whole screen (hence D-3's surface table and D-4's
	// per-call card), while this renders a single Author document the
	// caller could already have read the source of under the same
	// toggle. So it is a read — same class as `author_read`, audited on
	// every leg, no card.
	"author_render",
}

// desktopUIAnnotateTools route immediately like a read, but WRITE
// something the user sees. `ui_highlight` (D6) is the whole class: an
// ephemeral, attributed, self-expiring marker over a surface the policy
// table allows. ADR-062 D-5 is explicit that it takes NO approval card —
// it is annotation, not action: nothing is focused, scrolled, clicked or
// typed, and the user's own click remains the only actuator. Its consent
// is the sharing toggle plus the `highlight` policy bit; its safety is
// the desktop's per-agent rate limit; and every call is audited like an
// action (ring + hub mirror), which is where "an agent drew on my
// screen" becomes visible.
//
// A separate list rather than a read: these are worth naming distinctly
// so nobody later reads "routes without a card" as "is a read".
var desktopUIAnnotateTools = []string{
	"ui_highlight",
}

var desktopUIActionTools = []string{
	"ui_screenshot",
	// ADR-064 lane A: a write into a document the user is authoring.
	"author_apply",
}

func desktopUIToolClass(tool string) string {
	for _, n := range desktopUIReadTools {
		if n == tool {
			return "read"
		}
	}
	for _, n := range desktopUIAnnotateTools {
		if n == tool {
			return "annotate"
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
	out := make([]string, 0, len(desktopUIReadTools)+len(desktopUIAnnotateTools)+len(desktopUIActionTools))
	out = append(out, desktopUIReadTools...)
	out = append(out, desktopUIAnnotateTools...)
	out = append(out, desktopUIActionTools...)
	return out
}

// desktopUIGrantable reports whether an action tool of this class may
// EVER ride a session grant. Both of today's action tools say no, for
// two different reasons — which is why this is a switch and not a
// negation:
//
//   - `ui_screenshot`: a frame of everything the user can see is the
//     most sensitive artifact the app can emit, so consent is per call,
//     forever (plan §3.3).
//   - `author_apply`: a grant here would be the WRONG SHAPE, not merely
//     too broad. This store keys grants by (kind, team, host, agent) —
//     there is no document in the key. The consent the desktop offers is
//     "allow this document for this session", so a grant minted from
//     that answer would silence the card for every OTHER document too:
//     the user would have said "edit my draft" and been taken to mean
//     "edit anything I open". Until the grant key carries a subject, a
//     relayed apply is carded per call, and the card's args name the
//     document. The desktop holds the per-document lease for its own
//     local agents, where it is the sole consent authority (ADR-064,
//     coworking A3).
//
// desktopGrantKind stays reserved: the grant path is shared with a class
// that DOES offer session grants, and a future grantable desktop action
// must land in its own namespace rather than borrowing the browser one
// (the §3.6 review amendment).
func desktopUIGrantable(tool string) bool {
	switch tool {
	case "ui_screenshot", "author_apply":
		return false
	default:
		return true
	}
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

	// Reads and annotations route immediately; only ACTION needs a human
	// decision. A grantable action may ride a prior session grant for THIS
	// class; ui_screenshot never does.
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
