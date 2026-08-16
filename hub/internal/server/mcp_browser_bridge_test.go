package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/termipod/hub/internal/auth"
)

// seedBrowserDesktop inserts an online host row carrying the
// browser_bridge capability — the shape the Electron desktop's
// registration writes (name desktop-<hostname>, capabilities_json
// {"browser_bridge":true}). Returns the host id.
func seedBrowserDesktop(t *testing.T, s *Server, team string) string {
	t.Helper()
	hostID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO hosts (id, team_id, name, status, capabilities_json, created_at)
		VALUES (?, ?, ?, 'online', '{"browser_bridge":true}', ?)`,
		hostID, team, "desktop-test-"+hostID[:6], NowUTC()); err != nil {
		t.Fatalf("seed browser desktop: %v", err)
	}
	return hostID
}

// fakeBrowserDesktop simulates the desktop's tunnel poll loop entirely
// in-process: it drains the host's queue via nextForHost and posts each
// reply through deliverResponse, recording every envelope it saw so the
// test can assert on the wire shape (Kind, Payload). No HTTP, no ports.
type fakeBrowserDesktop struct {
	mu   sync.Mutex
	reqs []*tunnelRequest
}

func (f *fakeBrowserDesktop) seen() []*tunnelRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*tunnelRequest, len(f.reqs))
	copy(out, f.reqs)
	return out
}

// startFakeBrowserDesktop runs the loop until test cleanup. respond
// maps each envelope to the desktop's (status, body) pair; the body is
// the raw JSON that lands in BodyB64.
func startFakeBrowserDesktop(t *testing.T, s *Server, hostID string,
	respond func(env *tunnelRequest) (int, string)) *fakeBrowserDesktop {
	t.Helper()
	f := &fakeBrowserDesktop{}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			req, err := s.tunnel.nextForHost(ctx, hostID, 200*time.Millisecond)
			if err != nil {
				return // ctx canceled by cleanup
			}
			if req == nil {
				continue
			}
			f.mu.Lock()
			f.reqs = append(f.reqs, req)
			f.mu.Unlock()
			status, body := respond(req)
			// A lost waiter (caller timed out) is fine in a fake.
			_ = s.tunnel.deliverResponse(&tunnelResponse{
				ReqID:   req.ReqID,
				Status:  status,
				BodyB64: base64.StdEncoding.EncodeToString([]byte(body)),
			})
		}
	}()
	t.Cleanup(func() { cancel(); wg.Wait() })
	return f
}

// okBrowserEnvelope is the desktop's success reply shape.
func okBrowserEnvelope(result string) (int, string) {
	return http.StatusOK, `{"ok":true,"result":` + result + `}`
}

// okBrowserToolResult wraps a *faithful* desktop reply. Every tool the
// bridge exposes returns an MCP tool result — `browserbridge.ts` runTool
// answers through textContent() or a literal `{content:[{type:"image",…}]}`,
// and dispatchHubInvoke forwards that object untouched as the envelope's
// `result`. Callers passing okBrowserEnvelope a bare JSON object are
// exercising the fallback wrapper for a shape the desktop does not send.
func okBrowserToolResult(blocks string) (int, string) {
	return okBrowserEnvelope(`{"content":[` + blocks + `]}`)
}

// awaitBrowserActionRow polls until the approval card lands (the
// tool call parks in a goroutine while the test plays the principal).
func awaitBrowserActionRow(t *testing.T, s *Server) string {
	t.Helper()
	for i := 0; i < 100; i++ {
		var id string
		if err := s.db.QueryRow(
			`SELECT id FROM attention_items WHERE kind = 'browser_action'
			 ORDER BY created_at DESC LIMIT 1`).Scan(&id); err == nil && id != "" {
			return id
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("browser_action attention row never appeared")
	return ""
}

// resolveAttention records a principal decision directly in
// decisions_json — the same shape the decide endpoint writes
// (handlers_attention.go appends {at, by, decision, reason, option_id?}).
func resolveAttention(t *testing.T, s *Server, id string, decision map[string]any) {
	t.Helper()
	decisions, _ := json.Marshal([]map[string]any{decision})
	if _, err := s.db.Exec(
		`UPDATE attention_items SET status='resolved', decisions_json = ? WHERE id = ?`,
		string(decisions), id); err != nil {
		t.Fatalf("resolve attention: %v", err)
	}
}

func browserActionRowCount(t *testing.T, s *Server) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM attention_items WHERE kind = 'browser_action'`).Scan(&n); err != nil {
		t.Fatalf("count browser_action rows: %v", err)
	}
	return n
}

// invokeResult runs mcpBrowserInvoke in a goroutine (action tools park
// on the approval card) and returns a channel for the outcome.
func invokeResult(s *Server, agentID string, args map[string]any) chan struct {
	out  any
	jerr *jrpcError
} {
	raw, _ := json.Marshal(args)
	done := make(chan struct {
		out  any
		jerr *jrpcError
	}, 1)
	go func() {
		o, e := s.mcpBrowserInvoke(context.Background(), defaultTeamID, agentID, raw)
		done <- struct {
			out  any
			jerr *jrpcError
		}{out: o, jerr: e}
	}()
	return done
}

func TestBrowserInvoke_UnknownTool(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	_, jerr := s.mcpBrowserInvoke(context.Background(), defaultTeamID, agentID,
		json.RawMessage(`{"host_id":"h1","tool":"browser_delete_everything"}`))
	if jerr == nil || jerr.Code != -32602 {
		t.Fatalf("want -32602, got %+v", jerr)
	}
	for _, want := range []string{"browser_delete_everything", "browser_click", "browser_snapshot"} {
		if !strings.Contains(jerr.Message, want) {
			t.Errorf("error message should mention %q; got %q", want, jerr.Message)
		}
	}

	// Missing required params are the same class of protocol fault.
	_, jerr = s.mcpBrowserInvoke(context.Background(), defaultTeamID, agentID,
		json.RawMessage(`{"tool":"browser_click"}`))
	if jerr == nil || jerr.Code != -32602 {
		t.Fatalf("missing host_id: want -32602, got %+v", jerr)
	}
}

func TestBrowserInvoke_HostMissing(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	out, jerr := s.mcpBrowserInvoke(context.Background(), defaultTeamID, agentID,
		json.RawMessage(`{"host_id":"no-such-host","tool":"browser_list_tabs"}`))
	if jerr != nil {
		t.Fatalf("want agent-visible error result, got protocol fault %+v", jerr)
	}
	if body := mcpResultTextBody(t, out); !strings.Contains(body, "hosts_list") {
		t.Errorf("error should hint at hosts_list; got %s", body)
	}
}

func TestBrowserInvoke_HostOffline(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")
	hostID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO hosts (id, team_id, name, status, capabilities_json, created_at)
		VALUES (?, ?, 'desktop-offline', 'offline', '{"browser_bridge":true}', ?)`,
		hostID, defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed offline host: %v", err)
	}

	args, _ := json.Marshal(map[string]any{"host_id": hostID, "tool": "browser_list_tabs"})
	out, jerr := s.mcpBrowserInvoke(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("want agent-visible error result, got protocol fault %+v", jerr)
	}
	body := mcpResultTextBody(t, out)
	if !strings.Contains(body, "offline") || !strings.Contains(body, "hosts_list") {
		t.Errorf("error should name the status and hint hosts_list; got %s", body)
	}
}

func TestBrowserInvoke_NoCapability(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")
	hostID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO hosts (id, team_id, name, status, capabilities_json, created_at)
		VALUES (?, ?, 'gpu-rig', 'online', '{"agents":{}}', ?)`,
		hostID, defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed cap-less host: %v", err)
	}

	args, _ := json.Marshal(map[string]any{"host_id": hostID, "tool": "browser_list_tabs"})
	out, jerr := s.mcpBrowserInvoke(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("want agent-visible error result, got protocol fault %+v", jerr)
	}
	if body := mcpResultTextBody(t, out); !strings.Contains(body, "browser_bridge") {
		t.Errorf("error should name the missing capability; got %s", body)
	}
}

func TestBrowserInvoke_ReadRoutesThroughTunnel(t *testing.T) {
	s, _ := newTestServer(t)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "researcher", "claude-code", "")
	hostID := seedBrowserDesktop(t, s, defaultTeamID)
	fake := startFakeBrowserDesktop(t, s, hostID, func(env *tunnelRequest) (int, string) {
		// What browser_list_tabs really answers: textContent() of the
		// serialized rows, i.e. a one-block MCP tool result.
		return okBrowserToolResult(`{"type":"text","text":"[{\"tabId\":1,\"url\":\"https://example.com\"}]"}`)
	})

	args, _ := json.Marshal(map[string]any{"host_id": hostID, "tool": "browser_list_tabs"})
	out, jerr := s.mcpBrowserInvoke(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("browser_invoke: %+v", jerr)
	}
	// The agent reads the desktop's own text block, not a JSON rendering
	// of the envelope around it (E4).
	body := mcpResultTextBody(t, out)
	var tabs []map[string]any
	if err := json.Unmarshal([]byte(body), &tabs); err != nil {
		t.Fatalf("result should carry the desktop's tab rows verbatim; got %q (%v)", body, err)
	}
	if len(tabs) != 1 || tabs[0]["url"] != "https://example.com" {
		t.Fatalf("result should carry the desktop's tabs; got %v", tabs)
	}

	// The wire contract: one browser.invoke envelope whose payload is
	// {tool, args, agent_id, agent_handle}.
	reqs := fake.seen()
	if len(reqs) != 1 {
		t.Fatalf("fake desktop saw %d requests; want 1", len(reqs))
	}
	if reqs[0].Kind != "browser.invoke" {
		t.Errorf("envelope Kind = %q; want browser.invoke", reqs[0].Kind)
	}
	var p map[string]any
	if err := json.Unmarshal(reqs[0].Payload, &p); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if p["tool"] != "browser_list_tabs" || p["agent_id"] != agentID || p["agent_handle"] != "researcher" {
		t.Errorf("payload mismatch: %v", p)
	}

	// Reads never raise an approval card.
	if n := browserActionRowCount(t, s); n != 0 {
		t.Errorf("read tool created %d browser_action rows; want 0", n)
	}
}

func TestBrowserInvoke_DesktopErrorEnvelope(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")
	hostID := seedBrowserDesktop(t, s, defaultTeamID)
	startFakeBrowserDesktop(t, s, hostID, func(env *tunnelRequest) (int, string) {
		return http.StatusOK, `{"ok":false,"error":"no tab with that id"}`
	})

	args, _ := json.Marshal(map[string]any{
		"host_id": hostID, "tool": "browser_read_text",
		"args": map[string]any{"tab_id": 42},
	})
	out, jerr := s.mcpBrowserInvoke(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("desktop failure must be an agent-visible result, not a fault: %+v", jerr)
	}
	m, ok := out.(map[string]any)
	if !ok || m["isError"] != true {
		t.Fatalf("want isError result; got %v", out)
	}
	if body := mcpResultTextBody(t, out); !strings.Contains(body, "no tab with that id") {
		t.Errorf("error should carry the desktop's message; got %s", body)
	}
}

func TestBrowserInvoke_TransportFailure(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")
	hostID := seedBrowserDesktop(t, s, defaultTeamID)
	startFakeBrowserDesktop(t, s, hostID, func(env *tunnelRequest) (int, string) {
		return http.StatusBadGateway, ""
	})

	args, _ := json.Marshal(map[string]any{"host_id": hostID, "tool": "browser_list_tabs"})
	out, jerr := s.mcpBrowserInvoke(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("transport failure must be an agent-visible result, not a fault: %+v", jerr)
	}
	m, ok := out.(map[string]any)
	if !ok || m["isError"] != true {
		t.Fatalf("want isError result; got %v", out)
	}
	if body := mcpResultTextBody(t, out); !strings.Contains(body, "502") {
		t.Errorf("error should carry the transport status; got %s", body)
	}
}

// The full approval happy path: first action call parks on a
// browser_action card (typed args redacted), an approve with
// option_id="session" routes it AND records a grant, so the second
// action call routes with no new card.
func TestBrowserInvoke_ActionApproveSessionGrant(t *testing.T) {
	s, _ := newTestServer(t)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "worker1", "claude-code", "")
	hostID := seedBrowserDesktop(t, s, defaultTeamID)
	fake := startFakeBrowserDesktop(t, s, hostID, func(env *tunnelRequest) (int, string) {
		return okBrowserEnvelope(`{"clicked":true}`)
	})

	done := invokeResult(s, agentID, map[string]any{
		"host_id": hostID, "tool": "browser_click",
		"args": map[string]any{"selector": "#login", "text": "hunter2-secret"},
	})

	attnID := awaitBrowserActionRow(t, s)

	// Card shape: kind, summary, actor, and the redacted pending_payload.
	var kind, summary, actorHandle, payload string
	if err := s.db.QueryRow(`
		SELECT kind, summary, COALESCE(actor_handle, ''), pending_payload_json
		  FROM attention_items WHERE id = ?`, attnID).
		Scan(&kind, &summary, &actorHandle, &payload); err != nil {
		t.Fatalf("read card: %v", err)
	}
	if kind != "browser_action" {
		t.Errorf("kind = %q; want browser_action", kind)
	}
	if !strings.Contains(summary, "browser_click") || !strings.Contains(summary, "worker1") {
		t.Errorf("summary should name agent + tool; got %q", summary)
	}
	if actorHandle != "worker1" {
		t.Errorf("actor_handle = %q; want worker1", actorHandle)
	}
	if strings.Contains(payload, "hunter2") {
		t.Errorf("typed text must never reach the card; payload = %s", payload)
	}
	var pp map[string]any
	if err := json.Unmarshal([]byte(payload), &pp); err != nil {
		t.Fatalf("pending_payload not JSON: %v", err)
	}
	ppArgs, _ := pp["args"].(map[string]any)
	if ppArgs["text"] != "[redacted 14 chars]" {
		t.Errorf("args.text = %v; want redacted placeholder", ppArgs["text"])
	}
	if ppArgs["selector"] != "#login" {
		t.Errorf("selector should stay visible for the approver; got %v", ppArgs["selector"])
	}
	if pp["host_id"] != hostID || pp["tool"] != "browser_click" || pp["agent_id"] != agentID {
		t.Errorf("pending_payload envelope mismatch: %v", pp)
	}

	// Principal approves for the whole session.
	resolveAttention(t, s, attnID, map[string]any{
		"at": NowUTC(), "by": "@principal", "decision": "approve", "option_id": "session",
	})

	r := <-done
	if r.jerr != nil {
		t.Fatalf("approved call: %+v", r.jerr)
	}
	if res := mcpResultMap(t, r.out); res["clicked"] != true {
		t.Errorf("approved call should return the desktop result; got %v", res)
	}

	// Second action call: the session grant routes it with no new card.
	out, jerr := s.mcpBrowserInvoke(context.Background(), defaultTeamID, agentID,
		mustJSON(t, map[string]any{
			"host_id": hostID, "tool": "browser_type",
			"args": map[string]any{"selector": "#q", "text": "more secrets"},
		}))
	if jerr != nil {
		t.Fatalf("granted call: %+v", jerr)
	}
	if res := mcpResultMap(t, out); res["clicked"] != true {
		t.Errorf("granted call should return the desktop result; got %v", res)
	}
	if n := browserActionRowCount(t, s); n != 1 {
		t.Errorf("grant bypass created a second card (rows=%d); want 1", n)
	}

	// Redaction is card-only: the tunnel payload carries args verbatim.
	reqs := fake.seen()
	if len(reqs) != 2 {
		t.Fatalf("fake desktop saw %d requests; want 2", len(reqs))
	}
	var p map[string]any
	_ = json.Unmarshal(reqs[1].Payload, &p)
	if pArgs, _ := p["args"].(map[string]any); pArgs["text"] != "more secrets" {
		t.Errorf("tunnel payload must carry args unredacted; got %v", pArgs["text"])
	}
}

func TestBrowserInvoke_ActionRejectDenies(t *testing.T) {
	s, _ := newTestServer(t)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "worker2", "claude-code", "")
	hostID := seedBrowserDesktop(t, s, defaultTeamID)
	fake := startFakeBrowserDesktop(t, s, hostID, func(env *tunnelRequest) (int, string) {
		return okBrowserEnvelope(`{"clicked":true}`)
	})

	done := invokeResult(s, agentID, map[string]any{
		"host_id": hostID, "tool": "browser_eval",
		"args": map[string]any{"script": "document.title"},
	})
	attnID := awaitBrowserActionRow(t, s)
	resolveAttention(t, s, attnID, map[string]any{
		"at": NowUTC(), "by": "@principal", "decision": "reject", "reason": "not on my browser",
	})

	r := <-done
	if r.jerr != nil {
		t.Fatalf("reject should be a deny result, not a fault: %+v", r.jerr)
	}
	body := mcpResultTextBody(t, r.out)
	if !strings.Contains(body, `"behavior": "deny"`) {
		t.Errorf("expected deny shape; got %s", body)
	}
	if !strings.Contains(body, "not on my browser") {
		t.Errorf("expected the principal's reason; got %s", body)
	}
	if n := len(fake.seen()); n != 0 {
		t.Errorf("rejected call must not route; fake desktop saw %d requests", n)
	}
	if s.bridgeGrants.has(browserGrantKind, defaultTeamID, hostID, agentID) {
		t.Error("reject must not record a session grant")
	}
}

// Revoke drops the session grant, so the next action call parks on a
// fresh approval card again. Also covers the clear-all variant and the
// host-token refusal.
func TestBrowserBridgeRevoke_ClearsGrant(t *testing.T) {
	s, token := newA2ATestServer(t)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "worker3", "claude-code", "")
	hostID := seedBrowserDesktop(t, s, defaultTeamID)
	startFakeBrowserDesktop(t, s, hostID, func(env *tunnelRequest) (int, string) {
		return okBrowserEnvelope(`{"clicked":true}`)
	})

	// Stand up grants for two agents against this host.
	s.bridgeGrants.grant(browserGrantKind, defaultTeamID, hostID, agentID)
	s.bridgeGrants.grant(browserGrantKind, defaultTeamID, hostID, "other-agent")

	// Host-kind deputy tokens may not revoke — grants are principal
	// decisions. Mint a host token scoped to the team to prove it.
	hostTok := auth.NewToken()
	if err := auth.InsertToken(context.Background(), s.db, "host",
		`{"team":"`+defaultTeamID+`"}`, hostTok, NewID(), NowUTC()); err != nil {
		t.Fatalf("insert host token: %v", err)
	}
	if status, body := doReq(t, s, hostTok, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/hosts/"+hostID+"/browserbridge/revoke",
		map[string]any{"agent_id": agentID}); status != http.StatusForbidden {
		t.Fatalf("host token: status=%d body=%s; want 403", status, body)
	}
	if !s.bridgeGrants.has(browserGrantKind, defaultTeamID, hostID, agentID) {
		t.Fatal("host-token call must not have revoked the grant")
	}

	// Targeted revoke: only the named agent's grant goes.
	if status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/hosts/"+hostID+"/browserbridge/revoke",
		map[string]any{"agent_id": agentID}); status != http.StatusNoContent {
		t.Fatalf("revoke: status=%d body=%s; want 204", status, body)
	}
	if s.bridgeGrants.has(browserGrantKind, defaultTeamID, hostID, agentID) {
		t.Error("grant survived revoke")
	}
	if !s.bridgeGrants.has(browserGrantKind, defaultTeamID, hostID, "other-agent") {
		t.Error("targeted revoke cleared another agent's grant")
	}

	// The next action call from the revoked agent parks again.
	done := invokeResult(s, agentID, map[string]any{
		"host_id": hostID, "tool": "browser_click",
		"args": map[string]any{"selector": "#buy"},
	})
	attnID := awaitBrowserActionRow(t, s)
	resolveAttention(t, s, attnID, map[string]any{
		"at": NowUTC(), "by": "@principal", "decision": "reject",
	})
	if r := <-done; r.jerr != nil {
		t.Fatalf("post-revoke call: %+v", r.jerr)
	}

	// Empty agent_id clears every grant for the host.
	if status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/hosts/"+hostID+"/browserbridge/revoke",
		map[string]any{}); status != http.StatusNoContent {
		t.Fatalf("clear-all revoke: status=%d body=%s; want 204", status, body)
	}
	if s.bridgeGrants.has(browserGrantKind, defaultTeamID, hostID, "other-agent") {
		t.Error("clear-all revoke left a grant behind")
	}
}

// ---------------------------------------------------------------------------
// Relay result passthrough (vision-parity E4, asymmetry #3)
//
// The desktop answers a relayed call with the same MCP tool result a local
// caller gets (`browserbridge.ts` dispatchHubInvoke → callTool), so the relay
// must forward it rather than render it into JSON. Before E4 every relayed
// result reached the agent double-wrapped, which for an image meant a base64
// PNG delivered as prose.
// ---------------------------------------------------------------------------

// The headline: an image survives the relay AS an image.
func TestBrowserInvoke_ImageResultStaysAnImageBlock(t *testing.T) {
	s, _ := newTestServer(t)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "researcher", "claude-code", "")
	hostID := seedBrowserDesktop(t, s, defaultTeamID)
	const png = "iVBORw0KGgoAAAANSUhEUg=="
	startFakeBrowserDesktop(t, s, hostID, func(env *tunnelRequest) (int, string) {
		return okBrowserToolResult(`{"type":"image","data":"` + png + `","mimeType":"image/png"}`)
	})

	args, _ := json.Marshal(map[string]any{"host_id": hostID, "tool": "browser_screenshot"})
	out, jerr := s.mcpBrowserInvoke(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("browser_invoke: %+v", jerr)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %T", out)
	}
	blocks, ok := m["content"].([]any)
	if !ok || len(blocks) != 1 {
		t.Fatalf("want one content block, got %+v", m["content"])
	}
	blk, _ := blocks[0].(map[string]any)
	if blk["type"] != "image" {
		t.Fatalf("content[0].type = %v; want image — a relayed screenshot must not "+
			"arrive as base64 inside a text block, which is unreadable to the model", blk["type"])
	}
	if blk["data"] != png || blk["mimeType"] != "image/png" {
		t.Errorf("image block lost its data/mimeType: %+v", blk)
	}
	// The failure this replaces: the PNG rendered into a text block.
	for _, b := range blocks {
		bb, _ := b.(map[string]any)
		if txt, _ := bb["text"].(string); strings.Contains(txt, png) {
			t.Error("the base64 payload appears inside a text block — the wrapper is still in the path")
		}
	}
}

// A text result stops being double-wrapped too. This is the same defect one
// content type over, and it is what makes the relayed reply identical to the
// local one rather than merely usable.
func TestBrowserInvoke_TextResultIsNotDoubleWrapped(t *testing.T) {
	s, _ := newTestServer(t)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "researcher", "claude-code", "")
	hostID := seedBrowserDesktop(t, s, defaultTeamID)
	startFakeBrowserDesktop(t, s, hostID, func(env *tunnelRequest) (int, string) {
		return okBrowserToolResult(`{"type":"text","text":"hello from the page"}`)
	})

	args, _ := json.Marshal(map[string]any{"host_id": hostID, "tool": "browser_list_tabs"})
	out, jerr := s.mcpBrowserInvoke(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("browser_invoke: %+v", jerr)
	}
	if body := mcpResultTextBody(t, out); body != "hello from the page" {
		t.Errorf("text block = %q; want the desktop's own text, not a JSON rendering of the result around it", body)
	}
}

// The negative arm: a reply that is NOT already a tool result keeps the JSON
// wrapper, so the passthrough can't be widened into "forward whatever the far
// end sent". Kept faithful about why the shape is possible at all: no bridge
// tool answers this way today, which is exactly why the guard has to be
// asserted rather than assumed.
func TestBrowserInvoke_NonToolResultKeepsTheJSONWrapper(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply string
	}{
		{"a bare object", `{"clicked":true}`},
		{"content is not an array", `{"content":"just a string"}`},
		{"a block is not an object", `{"content":["text"]}`},
		{"a block carries no type", `{"content":[{"text":"untyped"}]}`},
		{"an empty content array", `{"content":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestServer(t)
			agentID := seedAgentWithKind(t, s, defaultTeamID, "researcher", "claude-code", "")
			hostID := seedBrowserDesktop(t, s, defaultTeamID)
			startFakeBrowserDesktop(t, s, hostID, func(env *tunnelRequest) (int, string) {
				return okBrowserEnvelope(tc.reply)
			})

			args, _ := json.Marshal(map[string]any{"host_id": hostID, "tool": "browser_list_tabs"})
			out, jerr := s.mcpBrowserInvoke(context.Background(), defaultTeamID, agentID, args)
			if jerr != nil {
				t.Fatalf("browser_invoke: %+v", jerr)
			}
			// The wrapper renders the whole reply as JSON in one text block.
			var got any
			if err := json.Unmarshal([]byte(mcpResultTextBody(t, out)), &got); err != nil {
				t.Fatalf("wrapped body should be the reply as JSON: %v", err)
			}
			var want any
			if err := json.Unmarshal([]byte(tc.reply), &want); err != nil {
				t.Fatalf("bad fixture: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("wrapped body = %v; want the reply verbatim %v", got, want)
			}
		})
	}
}

// The shape predicate on its own, including the boundary this deliberately
// gets "wrong": `{"content":[]}` is a legal MCP result and is still wrapped,
// because nothing distinguishes it from an arbitrary object that happens to
// carry an empty list under that key. Wrapping costs a reader one layer of
// quoting; forwarding a non-result would hand the agent something malformed.
func TestMCPToolResultPassthrough(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
		want bool
	}{
		{"an image result", map[string]any{"content": []any{
			map[string]any{"type": "image", "data": "aGk=", "mimeType": "image/png"}}}, true},
		{"a text result", map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "hi"}}}, true},
		{"mixed blocks", map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "rendered"},
			map[string]any{"type": "image", "data": "aGk=", "mimeType": "image/svg+xml"}}}, true},
		{"isError rides along", map[string]any{"isError": true, "content": []any{
			map[string]any{"type": "text", "text": "boom"}}}, true},
		{"not a map", []any{1, 2}, false},
		{"nil", nil, false},
		{"no content key", map[string]any{"clicked": true}, false},
		{"content is a string", map[string]any{"content": "text"}, false},
		{"content is an object", map[string]any{"content": map[string]any{"type": "text"}}, false},
		{"a block is a string", map[string]any{"content": []any{"text"}}, false},
		{"a block has no type", map[string]any{"content": []any{map[string]any{"text": "x"}}}, false},
		{"a block's type is empty", map[string]any{"content": []any{map[string]any{"type": ""}}}, false},
		{"a block's type is not a string", map[string]any{"content": []any{map[string]any{"type": 7}}}, false},
		{"one bad block among good ones", map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "ok"}, map[string]any{"no": "type"}}}, false},
		{"an empty content array", map[string]any{"content": []any{}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := mcpToolResultPassthrough(tc.in)
			if ok != tc.want {
				t.Fatalf("mcpToolResultPassthrough(%#v) ok = %v; want %v", tc.in, ok, tc.want)
			}
			if ok && !reflect.DeepEqual(any(got), tc.in) {
				t.Errorf("passthrough must forward the result verbatim; got %#v", got)
			}
			if !ok && got != nil {
				t.Errorf("a rejected value must return a nil map; got %#v", got)
			}
		})
	}
}
