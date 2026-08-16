// Tests for D5's hub-relayed desktop UI context
// (docs/plans/desktop-ui-context-and-pointing.md §3.6). Mirrors
// mcp_browser_bridge_test.go — the machinery is shared, so these tests
// cover what is DIFFERENT about the second traffic class: its own
// capability key, its own envelope kind, per-kind session grants, and
// the rule that a screenshot never rides a grant at all.

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// seedDesktopUIHost inserts an online host row advertising desktop_ui —
// the shape the desktop writes while UI context sharing is on.
func seedDesktopUIHost(t *testing.T, s *Server, team, caps string) string {
	t.Helper()
	hostID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO hosts (id, team_id, name, status, capabilities_json, created_at)
		VALUES (?, ?, ?, 'online', ?, ?)`,
		hostID, team, "desktop-ui-"+hostID, caps, NowUTC()); err != nil {
		t.Fatalf("seed desktop_ui host: %v", err)
	}
	return hostID
}

// awaitAttentionRow waits for a card of `kind` other than `notID`.
// The exclusion matters: created_at has second granularity, so "newest
// row" cannot distinguish two cards raised inside the same second —
// and the whole point of the per-call rule is that the SECOND capture
// raises its OWN card.
func awaitAttentionRow(t *testing.T, s *Server, kind, notID string) string {
	t.Helper()
	for i := 0; i < 100; i++ {
		var id string
		if err := s.db.QueryRow(
			`SELECT id FROM attention_items WHERE kind = ? AND id <> ? ORDER BY created_at DESC LIMIT 1`,
			kind, notID).Scan(&id); err == nil && id != "" {
			return id
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s attention row never appeared", kind)
	return ""
}

func attentionRowCount(t *testing.T, s *Server, kind string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM attention_items WHERE kind = ?`, kind).Scan(&n); err != nil {
		t.Fatalf("count %s rows: %v", kind, err)
	}
	return n
}

func desktopInvokeResult(s *Server, agentID string, args map[string]any) chan struct {
	out  any
	jerr *jrpcError
} {
	raw, _ := json.Marshal(args)
	done := make(chan struct {
		out  any
		jerr *jrpcError
	}, 1)
	go func() {
		o, e := s.mcpDesktopUIInvoke(context.Background(), defaultTeamID, agentID, raw)
		done <- struct {
			out  any
			jerr *jrpcError
		}{out: o, jerr: e}
	}()
	return done
}

// ── Validation + discovery ───────────────────────────────────────────

func TestDesktopUIInvoke_UnknownToolNamesTheRealOnes(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	_, jerr := s.mcpDesktopUIInvoke(context.Background(), defaultTeamID, agentID,
		json.RawMessage(`{"host_id":"h1","tool":"ui_read_the_vault"}`))
	if jerr == nil || jerr.Code != -32602 {
		t.Fatalf("want -32602, got %+v", jerr)
	}
	for _, want := range []string{"ui_read_the_vault", "ui_get_focus", "ui_screenshot"} {
		if !strings.Contains(jerr.Message, want) {
			t.Errorf("error should mention %q; got %q", want, jerr.Message)
		}
	}

	// A BROWSER tool is not a desktop-UI tool. The classes are separate
	// consent sentences, so crossing them is a protocol fault — not a
	// quietly-routed call that would skip this class's gate.
	_, jerr = s.mcpDesktopUIInvoke(context.Background(), defaultTeamID, agentID,
		json.RawMessage(`{"host_id":"h1","tool":"browser_click"}`))
	if jerr == nil || jerr.Code != -32602 {
		t.Fatalf("browser tool via desktop_ui_invoke: want -32602, got %+v", jerr)
	}
}

func TestBrowserInvoke_RejectsDesktopUITools(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	// The mirror of the cross-class check above: a desktop-UI tool routed
	// as browser.invoke would arrive without THIS class's card (the hub
	// gates by class), so it must die at the catalog — the desktop's own
	// kind re-check (tunnelclass.test.ts) is the second wall, not the
	// only one.
	for _, tool := range []string{"ui_screenshot", "ui_get_focus"} {
		_, jerr := s.mcpBrowserInvoke(context.Background(), defaultTeamID, agentID,
			json.RawMessage(`{"host_id":"h1","tool":"`+tool+`"}`))
		if jerr == nil || jerr.Code != -32602 {
			t.Fatalf("%s via browser_invoke: want -32602, got %+v", tool, jerr)
		}
	}
}

func TestDesktopUIInvoke_CapabilityIsTheSharingToggle(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	// A desktop with the bridge on but UI sharing OFF advertises
	// browser_bridge and not desktop_ui: the refusal must say so, since
	// that is a toggle the user can flip rather than a bug.
	hostID := seedDesktopUIHost(t, s, defaultTeamID, `{"browser_bridge":true}`)
	args, _ := json.Marshal(map[string]any{"host_id": hostID, "tool": "ui_get_focus"})
	out, jerr := s.mcpDesktopUIInvoke(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("want an agent-visible error result, got jrpc %+v", jerr)
	}
	text := mcpResultTextBody(t, out)
	if !strings.Contains(text, "desktop_ui") || !strings.Contains(text, "UI context sharing") {
		t.Errorf("refusal should name the capability AND the toggle; got %q", text)
	}

	// Offline desktops are refused before anything else.
	offline := seedDesktopUIHost(t, s, defaultTeamID, `{"desktop_ui":true}`)
	if _, err := s.db.Exec(`UPDATE hosts SET status='offline' WHERE id = ?`, offline); err != nil {
		t.Fatal(err)
	}
	args, _ = json.Marshal(map[string]any{"host_id": offline, "tool": "ui_get_focus"})
	out, _ = s.mcpDesktopUIInvoke(context.Background(), defaultTeamID, agentID, args)
	if !strings.Contains(mcpResultTextBody(t, out), "not online") {
		t.Errorf("offline desktop should be refused: %q", mcpResultTextBody(t, out))
	}
}

// ── Routing ──────────────────────────────────────────────────────────

func TestDesktopUIInvoke_ReadRoutesOnDesktopInvokeWithNoCard(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")
	hostID := seedDesktopUIHost(t, s, defaultTeamID, `{"browser_bridge":true,"desktop_ui":true}`)

	fake := startFakeBrowserDesktop(t, s, hostID, func(env *tunnelRequest) (int, string) {
		return okBrowserEnvelope(`{"content":[{"type":"text","text":"{\"surface\":\"read\"}"}]}`)
	})

	args, _ := json.Marshal(map[string]any{"host_id": hostID, "tool": "ui_get_focus"})
	out, jerr := s.mcpDesktopUIInvoke(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("read invoke: %+v", jerr)
	}
	if !strings.Contains(mcpResultTextBody(t, out), "surface") {
		t.Errorf("unexpected result: %q", mcpResultTextBody(t, out))
	}

	seen := fake.seen()
	if len(seen) != 1 {
		t.Fatalf("want one envelope, got %d", len(seen))
	}
	// The envelope kind is what the desktop routes on — and what lets it
	// refuse a ui_* tool that arrived as browser.invoke.
	if seen[0].Kind != desktopTunnelKind {
		t.Errorf("kind = %q, want %q", seen[0].Kind, desktopTunnelKind)
	}
	var payload map[string]any
	if err := json.Unmarshal(seen[0].Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["tool"] != "ui_get_focus" {
		t.Errorf("payload tool = %v", payload["tool"])
	}
	// Reads never raise a card.
	if n := attentionRowCount(t, s, "desktop_action"); n != 0 {
		t.Errorf("a read raised %d approval cards; want 0", n)
	}
}

// ── The screenshot rule: per call, always ────────────────────────────

func TestDesktopUIScreenshot_RaisesACardEveryTimeAndNeverGrants(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")
	hostID := seedDesktopUIHost(t, s, defaultTeamID, `{"desktop_ui":true}`)
	startFakeBrowserDesktop(t, s, hostID, func(env *tunnelRequest) (int, string) {
		return okBrowserEnvelope(`{"content":[{"type":"image","data":"aGk=","mimeType":"image/png"}]}`)
	})

	// First call: approve WITH the session option. The option is the
	// strongest consent a principal can express, and for a screenshot it
	// must still not create a standing grant (plan §3.3).
	done := desktopInvokeResult(s, agentID, map[string]any{"host_id": hostID, "tool": "ui_screenshot"})
	id := awaitAttentionRow(t, s, "desktop_action", "")
	resolveAttention(t, s, id, map[string]any{"decision": "approve", "option_id": "session", "by": "director"})
	if got := <-done; got.jerr != nil {
		t.Fatalf("first capture: %+v", got.jerr)
	}
	if s.bridgeGrants.has(desktopGrantKind, defaultTeamID, hostID, agentID) {
		t.Fatal("a screenshot approve must never record a session grant")
	}

	// Second call: a NEW card, because there is no standing consent.
	done = desktopInvokeResult(s, agentID, map[string]any{"host_id": hostID, "tool": "ui_screenshot"})
	id2 := awaitAttentionRow(t, s, "desktop_action", id)
	if id2 == id {
		t.Fatal("the second capture reused the first card — it must raise its own")
	}
	resolveAttention(t, s, id2, map[string]any{"decision": "approve", "by": "director"})
	if got := <-done; got.jerr != nil {
		t.Fatalf("second capture: %+v", got.jerr)
	}
	if n := attentionRowCount(t, s, "desktop_action"); n != 2 {
		t.Errorf("want 2 cards for 2 captures, got %d", n)
	}
}

func TestDesktopUIScreenshot_CardDescribesTheRequestAndDeclaresNoGrant(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")
	hostID := seedDesktopUIHost(t, s, defaultTeamID, `{"desktop_ui":true}`)
	startFakeBrowserDesktop(t, s, hostID, func(env *tunnelRequest) (int, string) {
		return okBrowserEnvelope(`{"content":[]}`)
	})

	done := desktopInvokeResult(s, agentID, map[string]any{"host_id": hostID, "tool": "ui_screenshot", "args": map[string]any{"tabId": 3}})
	id := awaitAttentionRow(t, s, "desktop_action", "")

	var payloadJSON, summary string
	if err := s.db.QueryRow(
		`SELECT COALESCE(pending_payload_json,''), summary FROM attention_items WHERE id = ?`, id).
		Scan(&payloadJSON, &summary); err != nil {
		t.Fatalf("read card: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("card payload: %v", err)
	}
	if payload["tool"] != "ui_screenshot" {
		t.Errorf("card tool = %v", payload["tool"])
	}
	// The card states the consent shape it can grant, so a client renders
	// the right buttons instead of inferring them from the kind.
	if grant, _ := payload["session_grant"].(bool); grant {
		t.Error("a screenshot card must declare session_grant:false")
	}
	if !strings.Contains(summary, "ui_screenshot") {
		t.Errorf("summary should name the tool: %q", summary)
	}
	resolveAttention(t, s, id, map[string]any{"decision": "reject", "by": "director"})
	<-done
}

func TestDesktopUIScreenshot_RejectDeniesWithoutRouting(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")
	hostID := seedDesktopUIHost(t, s, defaultTeamID, `{"desktop_ui":true}`)
	fake := startFakeBrowserDesktop(t, s, hostID, func(env *tunnelRequest) (int, string) {
		t.Error("a denied capture must never reach the desktop")
		return http.StatusOK, `{"ok":true}`
	})

	done := desktopInvokeResult(s, agentID, map[string]any{"host_id": hostID, "tool": "ui_screenshot"})
	id := awaitAttentionRow(t, s, "desktop_action", "")
	resolveAttention(t, s, id, map[string]any{"decision": "reject", "reason": "not now", "by": "director"})
	got := <-done
	if got.jerr != nil {
		t.Fatalf("deny should be an agent-visible result, got %+v", got.jerr)
	}
	if !strings.Contains(mcpResultTextBody(t, got.out), "not now") {
		t.Errorf("the denial should carry the principal's reason: %q", mcpResultTextBody(t, got.out))
	}
	if len(fake.seen()) != 0 {
		t.Error("nothing should have been routed")
	}
}

// ── Per-kind grants (the §3.6 review amendment) ──────────────────────

func TestBridgeGrants_AreKeyedByKind(t *testing.T) {
	g := &bridgeGrantStore{}
	g.grant(browserGrantKind, "team", "host", "agent")

	if !g.has(browserGrantKind, "team", "host", "agent") {
		t.Fatal("the grant we just wrote is missing")
	}
	// THE point of the kind dimension: a browser-driving session grant
	// must not silence a desktop card. W3's store had no kind, so a naive
	// "one helper, two kind constants" generalization would have.
	if g.has(desktopGrantKind, "team", "host", "agent") {
		t.Fatal("a browser grant leaked into the desktop class")
	}
	// Neighbours stay untouched.
	if g.has(browserGrantKind, "other-team", "host", "agent") ||
		g.has(browserGrantKind, "team", "other-host", "agent") ||
		g.has(browserGrantKind, "team", "host", "other-agent") {
		t.Fatal("grant key is not scoped to (kind, team, host, agent)")
	}
}

func TestBridgeGrants_RevokeIsKindBlind(t *testing.T) {
	g := &bridgeGrantStore{}
	g.grant(browserGrantKind, "team", "host", "agent")
	g.grant(desktopGrantKind, "team", "host", "agent")
	g.grant(browserGrantKind, "team", "host", "other-agent")

	// Granting is kind-scoped; REVOKING is not. "Revoke" in Settings →
	// Remote driving means this agent no longer touches this desktop —
	// an agent that kept a standing browser grant afterwards would make
	// that pill a lie.
	g.revoke("team", "host", "agent")
	if g.has(browserGrantKind, "team", "host", "agent") || g.has(desktopGrantKind, "team", "host", "agent") {
		t.Fatal("revoke must clear every kind for that agent")
	}
	if !g.has(browserGrantKind, "team", "host", "other-agent") {
		t.Fatal("revoke hit an agent it was not aimed at")
	}

	// An empty agent id clears the whole (team, host) pair, every kind.
	g.grant(desktopGrantKind, "team", "host", "agent")
	g.grant(browserGrantKind, "team", "other-host", "keeper")
	g.revoke("team", "host", "")
	if g.has(desktopGrantKind, "team", "host", "agent") || g.has(browserGrantKind, "team", "host", "other-agent") {
		t.Fatal("host-wide revoke left grants behind")
	}
	if !g.has(browserGrantKind, "team", "other-host", "keeper") {
		t.Fatal("host-wide revoke crossed into another host")
	}
}

// ── Capability reading ───────────────────────────────────────────────

func TestHostCapable_ReadsOneKeyWithoutGuessing(t *testing.T) {
	cases := []struct {
		caps string
		key  string
		want bool
	}{
		{`{"desktop_ui":true}`, "desktop_ui", true},
		{`{"desktop_ui":false}`, "desktop_ui", false},
		{`{"browser_bridge":true}`, "desktop_ui", false},
		{`{}`, "desktop_ui", false},
		{``, "desktop_ui", false},
		{`not json`, "desktop_ui", false},
		// Hand-rolled rows shouldn't silently strand the feature.
		{`{"desktop_ui":"yes"}`, "desktop_ui", true},
		{`{"desktop_ui":1}`, "desktop_ui", true},
		{`{"desktop_ui":0}`, "desktop_ui", false},
		{`{"desktop_ui":""}`, "desktop_ui", false},
	}
	for _, c := range cases {
		if got := hostCapable(c.caps, c.key); got != c.want {
			t.Errorf("hostCapable(%q, %q) = %v, want %v", c.caps, c.key, got, c.want)
		}
	}
	// The browser class reads through the same function.
	if !browserBridgeCapable(`{"browser_bridge":true}`) || browserBridgeCapable(`{"desktop_ui":true}`) {
		t.Error("browserBridgeCapable must read its own key only")
	}
}

func TestDesktopUIGrantable_NeitherActionToolRidesAGrant(t *testing.T) {
	if desktopUIGrantable("ui_screenshot") {
		t.Error("ui_screenshot must never be grantable (plan §3.3)")
	}
	// ADR-064 lane A. The reason differs from ui_screenshot's: a grant here
	// would be the wrong SHAPE. This store keys by (kind, team, host, agent)
	// with no document in it, while the consent the desktop offers is "allow
	// this document for this session" — so a grant minted from that answer
	// would silence the card for every other document the user opens.
	if desktopUIGrantable("author_apply") {
		t.Error("author_apply must not ride a grant whose key has no document in it")
	}
	if !desktopUIGrantable("ui_get_focus") {
		t.Error("the predicate should not refuse everything by default")
	}
}

func TestDesktopUIAuthorTools_ClassifyByWhatTheyDo(t *testing.T) {
	// author_read discloses document BYTES but changes nothing: a read, so it
	// routes without a card (the desktop audits it on every leg instead).
	if desktopUIToolClass("author_read") != "read" {
		t.Errorf("author_read must be a read, got %q", desktopUIToolClass("author_read"))
	}
	// author_apply writes into the user's own work: the gated class.
	if desktopUIToolClass("author_apply") != "action" {
		t.Errorf("author_apply must be the gated class, got %q", desktopUIToolClass("author_apply"))
	}
	// author_render answers in PIXELS and is still a read. The resemblance to
	// ui_screenshot is what makes this worth pinning: a screenshot is a frame of
	// the user's whole screen and is carded on every call, while this draws ONE
	// Author document whose source the same caller could already have fetched
	// with author_read under the same toggle. Classifying it as an action would
	// bury it under a card it does not need; the mistake in the other direction
	// — a screenshot drifting into the read list — is what this pairing guards.
	if desktopUIToolClass("author_render") != "read" {
		t.Errorf("author_render must be a read, got %q", desktopUIToolClass("author_render"))
	}
	if desktopUIToolClass("ui_screenshot") == desktopUIToolClass("author_render") {
		t.Error("a screenshot and a document render must not share a class")
	}
}

func TestDesktopUINavigate_IsItsOwnClass(t *testing.T) {
	// desktop_open ACTUATES — it moves the user's screen — and still
	// routes without a card. The judgement is what an unwanted call
	// costs: an unwanted write has changed the user's work, an unwanted
	// navigation has changed which tab is in front of them and is undone
	// by one click on the banner that names the agent.
	if desktopUIToolClass("desktop_open") != "navigate" {
		t.Errorf("desktop_open must be the navigate class, got %q", desktopUIToolClass("desktop_open"))
	}
	// Not folded into the action list: that list is what the hub cards,
	// and a card on every navigation makes "show me where" cost two round
	// trips and a decision.
	if desktopUIToolClass("desktop_open") == desktopUIToolClass("author_apply") {
		t.Error("a navigation and a document write must not share a class")
	}
	// …and it must be in the enum the tool advertises, or an agent that
	// reads the catalog cannot call it.
	found := false
	for _, n := range desktopUIToolNames() {
		if n == "desktop_open" {
			found = true
		}
	}
	if !found {
		t.Error("desktop_open is missing from desktopUIToolNames — invisible to callers")
	}
}

// ── Agent pointing routes without a card (ADR-062 D-5) ───────────────

func TestDesktopUIHighlight_RoutesWithNoApprovalCard(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")
	hostID := seedDesktopUIHost(t, s, defaultTeamID, `{"desktop_ui":true}`)
	fake := startFakeBrowserDesktop(t, s, hostID, func(env *tunnelRequest) (int, string) {
		return okBrowserEnvelope(`{"content":[{"type":"text","text":"highlighted replay for 8s"}]}`)
	})

	// A highlight is non-actuating annotation: it draws a glow that
	// expires and takes no action with the user's authority. ADR-062 D-5
	// puts its consent in the sharing toggle plus the policy bit — NOT in
	// a card. Gating it would make a pointing conversation unusable, and
	// would train the principal to click through approvals.
	args, _ := json.Marshal(map[string]any{
		"host_id": hostID,
		"tool":    "ui_highlight",
		"args":    map[string]any{"ref": "ui://replay?dataset_id=ds_1", "note": "this episode"},
	})
	out, jerr := s.mcpDesktopUIInvoke(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("highlight: %+v", jerr)
	}
	if !strings.Contains(mcpResultTextBody(t, out), "highlighted") {
		t.Errorf("unexpected result: %q", mcpResultTextBody(t, out))
	}
	if n := attentionRowCount(t, s, "desktop_action"); n != 0 {
		t.Errorf("a highlight raised %d approval cards; want 0 (ADR-062 D-5)", n)
	}
	if len(fake.seen()) != 1 || fake.seen()[0].Kind != desktopTunnelKind {
		t.Errorf("highlight must route on %s: %+v", desktopTunnelKind, fake.seen())
	}
	// …and it does not silently become a grant either.
	if s.bridgeGrants.has(desktopGrantKind, defaultTeamID, hostID, agentID) {
		t.Error("a card-free tool must not record a grant")
	}
}

func TestDesktopUIToolClasses_AreExhaustiveAndDisjoint(t *testing.T) {
	// The class decides whether a call is gated, so a tool that fell out
	// of every list would route ungated by accident. Stated as a test:
	// every catalog name classifies, and none classifies twice.
	seen := map[string]int{}
	for _, n := range desktopUIToolNames() {
		seen[n]++
		if desktopUIToolClass(n) == "" {
			t.Errorf("%s is in the catalog but classifies as nothing", n)
		}
	}
	for n, count := range seen {
		if count != 1 {
			t.Errorf("%s appears %d times across the class lists", n, count)
		}
	}
	if desktopUIToolClass("ui_screenshot") != "action" {
		t.Error("ui_screenshot must be the gated class")
	}
	if desktopUIToolClass("ui_highlight") != "annotate" {
		t.Error("ui_highlight must be annotate — routed, but not a read")
	}
	if desktopUIToolClass("ui_get_focus") != "read" {
		t.Error("ui_get_focus must be a read")
	}
}

// An approved screenshot reaches the agent as an image, not as base64 prose
// (vision-parity E4). This is the same relay fix the browser class gets, on
// the path the asymmetry was actually named for: a captured screen is the one
// bridge result that is worthless when flattened into text.
func TestDesktopUIScreenshot_ReturnsARealImageBlock(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")
	hostID := seedDesktopUIHost(t, s, defaultTeamID, `{"desktop_ui":true}`)
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB"
	startFakeBrowserDesktop(t, s, hostID, func(env *tunnelRequest) (int, string) {
		return okBrowserToolResult(`{"type":"image","data":"` + png + `","mimeType":"image/png"}`)
	})

	done := desktopInvokeResult(s, agentID, map[string]any{"host_id": hostID, "tool": "ui_screenshot"})
	id := awaitAttentionRow(t, s, "desktop_action", "")
	resolveAttention(t, s, id, map[string]any{"decision": "approve", "by": "director"})
	got := <-done
	if got.jerr != nil {
		t.Fatalf("ui_screenshot: %+v", got.jerr)
	}

	m, ok := got.out.(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %T", got.out)
	}
	blocks, ok := m["content"].([]any)
	if !ok || len(blocks) != 1 {
		t.Fatalf("want one content block, got %+v", m["content"])
	}
	blk, _ := blocks[0].(map[string]any)
	if blk["type"] != "image" || blk["data"] != png {
		t.Fatalf("captured screen must arrive as an image block carrying its bytes; got %+v", blk)
	}
	if txt, _ := blk["text"].(string); strings.Contains(txt, png) {
		t.Error("the PNG is inside a text block — the JSON wrapper is still in the path")
	}
}
