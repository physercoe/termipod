package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mobileIntentSetup spins a fresh hub with a team + a running general
// steward and returns the handles each test needs. Uses the same
// pattern as insightsSetup but with the steward kind set to
// `steward.general.v1` so findRunningGeneralSteward picks it up.
func mobileIntentSetup(t *testing.T) (s *Server, token, team, agentID string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	tok, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	srv, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	const testTeam = "mobile-intent-test"
	now := NowUTC()
	if _, err := srv.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		testTeam, testTeam, now); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	stewardAgent := NewID()
	if _, err := srv.db.Exec(
		`INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		 VALUES (?, ?, ?, ?, 'running', ?)`,
		stewardAgent, testTeam, generalStewardHandle, generalStewardKind, now,
	); err != nil {
		t.Fatalf("seed steward: %v", err)
	}
	return srv, tok, testTeam, stewardAgent
}

func postMobileIntent(t *testing.T, srv *Server, token, team string, body map[string]any) (int, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost,
		"/v1/teams/"+team+"/mobile/intent", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.router.ServeHTTP(rr, req)
	return rr.Code, rr.Body.String()
}

// TestMobileIntent_PublishesToStewardChannel: a navigate intent
// reaches a subscriber on the general steward's bus channel.
func TestMobileIntent_PublishesToStewardChannel(t *testing.T) {
	srv, tok, team, stewardID := mobileIntentSetup(t)

	// Subscribe BEFORE the publish so we don't race with the bus.
	sub := srv.bus.Subscribe(agentBusKey(stewardID))
	defer srv.bus.Unsubscribe(agentBusKey(stewardID), sub)

	uri := "termipod://project/abc/documents/def/sections/methods"
	status, body := postMobileIntent(t, srv, tok, team, map[string]any{"uri": uri})
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}

	select {
	case evt := <-sub:
		if got := evt["kind"]; got != "mobile.intent" {
			t.Errorf("evt.kind=%v, want mobile.intent", got)
		}
		if got := evt["uri"]; got != uri {
			t.Errorf("evt.uri=%v, want %s", got, uri)
		}
		if got := evt["intent"]; got != "navigate" {
			t.Errorf("evt.intent=%v, want navigate", got)
		}
		if got := evt["agent_id"]; got != stewardID {
			t.Errorf("evt.agent_id=%v, want %s", got, stewardID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event published within 2s")
	}
}

// TestMobileIntent_StampsSessionID: a navigate intent published while
// the steward has an active session must carry the session_id in the
// event envelope. The mobile overlay subscribes with `?session=<id>`,
// and `handleStreamAgentEvents` filters out events whose session_id
// doesn't match. Without this stamp the overlay never sees the event;
// pill never renders, navigation never fires (v1.0.479 regression fix).
func TestMobileIntent_StampsSessionID(t *testing.T) {
	srv, tok, team, stewardID := mobileIntentSetup(t)

	sessionID := NewID()
	now := NowUTC()
	if _, err := srv.db.Exec(`
		INSERT INTO sessions (id, team_id, title, scope_kind, current_agent_id, status, opened_at, last_active_at)
		VALUES (?, ?, 'general', 'team', ?, 'active', ?, ?)`,
		sessionID, team, stewardID, now, now,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	sub := srv.bus.Subscribe(agentBusKey(stewardID))
	defer srv.bus.Unsubscribe(agentBusKey(stewardID), sub)

	status, body := postMobileIntent(t, srv, tok, team,
		map[string]any{"uri": "termipod://projects"})
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}

	select {
	case evt := <-sub:
		if got := evt["session_id"]; got != sessionID {
			t.Errorf("evt.session_id=%v, want %s", got, sessionID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event published within 2s")
	}
}

// TestMobileIntent_RecordsAuditEvent: every navigate lands an
// audit row so the user (and future audit feed UI) can review
// what the steward did.
func TestMobileIntent_RecordsAuditEvent(t *testing.T) {
	srv, tok, team, stewardID := mobileIntentSetup(t)

	uri := "termipod://activity?filter=stuck"
	status, body := postMobileIntent(t, srv, tok, team, map[string]any{"uri": uri})
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}

	var (
		action     string
		targetKind string
		targetID   string
		metaJSON   string
	)
	err := srv.db.QueryRow(`
		SELECT action, COALESCE(target_kind,''), COALESCE(target_id,''),
		       COALESCE(meta_json,'{}')
		  FROM audit_events
		 WHERE team_id = ? AND action = 'mobile.intent'
		 ORDER BY ts DESC LIMIT 1`, team).Scan(
		&action, &targetKind, &targetID, &metaJSON,
	)
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if targetKind != "agent" || targetID != stewardID {
		t.Errorf("audit target=%s/%s, want agent/%s", targetKind, targetID, stewardID)
	}
	if !strings.Contains(metaJSON, uri) {
		t.Errorf("audit meta=%s, want to contain uri %s", metaJSON, uri)
	}
}

// TestMobileIntent_RejectsBadScheme: unknown URI scheme → 400.
func TestMobileIntent_RejectsBadScheme(t *testing.T) {
	srv, tok, team, _ := mobileIntentSetup(t)

	for _, uri := range []string{
		"https://example.com/page",
		"app://internal/route",
		"random-string",
	} {
		status, body := postMobileIntent(t, srv, tok, team, map[string]any{"uri": uri})
		if status != http.StatusBadRequest {
			t.Errorf("uri=%q status=%d, want 400 (body=%s)", uri, status, body)
		}
	}
}

// TestMobileIntent_NoStewardRunning: with no general steward, the
// endpoint must 424 (failed dependency) rather than 500 — surfaces
// the real reason so the steward MCP tool can retry after ensure.
func TestMobileIntent_NoStewardRunning(t *testing.T) {
	srv, tok, _, _ := mobileIntentSetup(t)

	// Different team that has no steward.
	if _, err := srv.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES ('lonely', 'lonely', ?)`,
		NowUTC(),
	); err != nil {
		t.Fatalf("seed lonely team: %v", err)
	}
	status, body := postMobileIntent(t, srv, tok, "lonely",
		map[string]any{"uri": "termipod://project/abc"})
	if status != http.StatusFailedDependency {
		t.Errorf("status=%d, want 424 (body=%s)", status, body)
	}
}

// TestMobileIntent_RequiresURI: missing/empty uri → 400.
func TestMobileIntent_RequiresURI(t *testing.T) {
	srv, tok, team, _ := mobileIntentSetup(t)

	status, body := postMobileIntent(t, srv, tok, team, map[string]any{})
	if status != http.StatusBadRequest {
		t.Errorf("missing uri: status=%d, want 400 (body=%s)", status, body)
	}

	status, body = postMobileIntent(t, srv, tok, team, map[string]any{"uri": ""})
	if status != http.StatusBadRequest {
		t.Errorf("empty uri: status=%d, want 400 (body=%s)", status, body)
	}
}
