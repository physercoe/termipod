package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func directiveTraceRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	r.Get("/v1/teams/{team}/directives/{task}/trace", s.handleDirectiveTrace)
	return r
}

func TestDirectiveTrace_ShowsHopsInOrder(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-trace")

	root := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO tasks (id, project_id, title, status, created_at, updated_at)
		VALUES (?, ?, 'the mission', 'in_progress',
		        '2026-05-19T01:00:00Z', '2026-05-19T01:00:00Z')`, root, proj); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	child := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO tasks (id, project_id, parent_task_id, title, status,
		                   terminal_reason, created_at, completed_at, updated_at)
		VALUES (?, ?, ?, 'a subtask', 'done', 'completed',
		        '2026-05-19T02:00:00Z', '2026-05-19T04:00:00Z', '2026-05-19T04:00:00Z')`,
		child, proj, root); err != nil {
		t.Fatalf("seed child: %v", err)
	}
	q := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO attention_items (id, project_id, scope_kind, kind, summary, cause, created_at)
		VALUES (?, ?, 'project', 'help_request', 'need input', ?, '2026-05-19T03:00:00Z')`,
		q, proj, child); err != nil {
		t.Fatalf("seed question: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO audit_events (id, team_id, ts, actor_token_id, actor_kind, actor_handle,
		                          action, target_kind, target_id, summary, meta_json)
		VALUES (?, ?, '2026-05-19T03:30:00Z', NULL, 'system', NULL,
		        'loop.stall_escalated', 'task', ?, 'stalled at the subtask', '{}')`,
		NewID(), defaultTeamID, child); err != nil {
		t.Fatalf("seed audit: %v", err)
	}

	rr := httptest.NewRecorder()
	directiveTraceRouter(s).ServeHTTP(rr, httptest.NewRequest(
		"GET", "/v1/teams/"+defaultTeamID+"/directives/"+root+"/trace", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}

	var resp struct {
		Trace []traceEvent `json:"trace"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// root opened, child opened, question raised, [STALL], child closed.
	if len(resp.Trace) != 5 {
		t.Fatalf("trace has %d events, want 5: %+v", len(resp.Trace), resp.Trace)
	}
	for i := 1; i < len(resp.Trace); i++ {
		if resp.Trace[i].TS < resp.Trace[i-1].TS {
			t.Errorf("trace not time-ordered at %d: %+v", i, resp.Trace)
		}
	}
	var sawStall bool
	for _, e := range resp.Trace {
		if e.Kind == "loop.stall_escalated" {
			sawStall = true
			if !e.Stall || !strings.Contains(e.Summary, "[STALL]") {
				t.Errorf("stall hop not marked: %+v", e)
			}
		}
	}
	if !sawStall {
		t.Error("the stall escalation is missing from the trace")
	}
}

func TestDirectiveTrace_NotFound(t *testing.T) {
	s, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	directiveTraceRouter(s).ServeHTTP(rr, httptest.NewRequest(
		"GET", "/v1/teams/"+defaultTeamID+"/directives/nonexistent/trace", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}
