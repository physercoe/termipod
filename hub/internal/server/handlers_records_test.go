package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/termipod/hub/internal/auth"
)

// Decision records (compare-wall/decisions plan §4.1 + §5's hub-test list):
// CRUD, the status transitions, supersede chains, link validation, provenance
// derived from the token, and team scope on every read.

func seedRecordProject(t *testing.T, s *Server, team, name string) string {
	t.Helper()
	id := NewID()
	if _, err := s.writeDB.Exec(`
		INSERT INTO projects (id, team_id, name, created_at, kind)
		VALUES (?, ?, ?, ?, 'goal')`, id, team, name, NowUTC()); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return id
}

func TestRecordCRUDAndLinks(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	team := defaultTeamID
	project := seedRecordProject(t, s, team, "records-crud")

	body := recordBody{
		Kind:   "finding",
		Title:  "lr=3e-4 beats 1e-3",
		BodyMD: "Five seeds each; the gap is outside the band.",
		Links: []recordLink{
			{Kind: "run", ID: "run_1", Note: "the winner"},
			{Kind: "episode", ID: "ep_9"},
		},
	}
	if err := validateRecordBody(&body, true); err != nil {
		t.Fatalf("validate: %v", err)
	}
	created, err := s.createRecord(ctx, team, project, body, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Status != recordProposed {
		t.Errorf("a new record starts %q, want proposed", created.Status)
	}
	if len(created.Links) != 2 || created.Links[0].Kind != "run" || created.Links[0].Note != "the winner" {
		t.Errorf("links did not round-trip: %+v", created.Links)
	}

	got, err := s.getRecordByID(ctx, team, created.ID)
	if err != nil || got.Title != body.Title {
		t.Fatalf("get: %v %+v", err, got)
	}

	// Filters.
	if rs, _ := s.listRecords(ctx, team, recordFilter{ProjectID: project}); len(rs) != 1 {
		t.Errorf("project filter: want 1, got %d", len(rs))
	}
	if rs, _ := s.listRecords(ctx, team, recordFilter{Kind: "decision"}); len(rs) != 0 {
		t.Errorf("kind filter should exclude the finding, got %d", len(rs))
	}
	if rs, _ := s.listRecords(ctx, team, recordFilter{Status: recordProposed}); len(rs) != 1 {
		t.Errorf("status filter: want 1, got %d", len(rs))
	}
}

func TestRecordBodyValidation(t *testing.T) {
	cases := []struct {
		name string
		body recordBody
		want string
	}{
		{"unknown kind", recordBody{Kind: "musing", Title: "x"}, "kind must be one of"},
		{"no title", recordBody{Kind: "decision"}, "title is required"},
		{
			// The evidence vocabulary is closed because a link kind with no
			// dereference is a dead chip.
			"unknown link kind",
			recordBody{Kind: "decision", Title: "x", Links: []recordLink{{Kind: "tweet", ID: "1"}}},
			"not one of",
		},
		{
			"link with no id",
			recordBody{Kind: "decision", Title: "x", Links: []recordLink{{Kind: "run", Note: "the good one"}}},
			"id is required",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := c.body
			err := validateRecordBody(&b, true)
			if err == nil {
				t.Fatalf("expected a refusal")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
	// A body with no kind creates a decision; a PATCH with no kind does not
	// silently become one (creating=false), because a finding must not turn
	// into a decision by omission.
	b := recordBody{Title: "x"}
	if err := validateRecordBody(&b, true); err != nil || b.Kind != "decision" {
		t.Errorf("create default: err=%v kind=%q", err, b.Kind)
	}
	p := recordBody{Title: "x"}
	if err := validateRecordBody(&p, false); err == nil {
		t.Error("a patch with an empty kind should be refused, not defaulted")
	}
}

func TestRecordStatusTransitions(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	team := defaultTeamID
	project := seedRecordProject(t, s, team, "records-status")

	rec, err := s.createRecord(ctx, team, project, recordBody{Kind: "decision", Title: "use adamw"}, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// proposed → accepted is the one patchable move.
	accepted, err := s.patchRecord(ctx, team, rec.ID, []byte(`{"status":"accepted"}`))
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if accepted.Status != recordAccepted {
		t.Fatalf("status = %q, want accepted", accepted.Status)
	}

	// …and nothing else is. 'superseded' is a consequence, not a setting.
	_, err = s.patchRecord(ctx, team, rec.ID, []byte(`{"status":"superseded"}`))
	var te errRecordTransition
	if !errors.As(err, &te) {
		t.Fatalf("expected a transition refusal, got %v", err)
	}
	if !strings.Contains(te.msg, "/supersede") {
		t.Errorf("refusal should name the way forward: %q", te.msg)
	}

	// An accepted record is history: it supersedes, it does not disappear.
	if err := s.deleteRecord(ctx, team, rec.ID); !errors.As(err, &te) {
		t.Fatalf("deleting an accepted record should be refused, got %v", err)
	}
	// A proposal can still be dismissed.
	draft, _ := s.createRecord(ctx, team, project, recordBody{Kind: "decision", Title: "draft"}, "")
	if err := s.deleteRecord(ctx, team, draft.ID); err != nil {
		t.Errorf("deleting a proposal: %v", err)
	}
}

func TestRecordSupersedeRetiresOnlyOnAcceptance(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	team := defaultTeamID
	project := seedRecordProject(t, s, team, "records-supersede")

	first, _ := s.createRecord(ctx, team, project, recordBody{Kind: "decision", Title: "lr=1e-3"}, "")
	if _, err := s.patchRecord(ctx, team, first.ID, []byte(`{"status":"accepted"}`)); err != nil {
		t.Fatalf("accept first: %v", err)
	}

	// The successor is a PROPOSAL. Until it is accepted, the decision it hopes
	// to replace is still the decision — retiring it now would mark a live
	// decision dead on the strength of an unreviewed suggestion.
	successor, err := s.createRecord(ctx, team, project, recordBody{Kind: "decision", Title: "lr=3e-4"}, first.ID)
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if successor.SupersedesID != first.ID {
		t.Errorf("edge lost: %+v", successor)
	}
	stillFirst, _ := s.getRecordByID(ctx, team, first.ID)
	if stillFirst.Status != recordAccepted {
		t.Fatalf("predecessor is %q while its successor is only proposed", stillFirst.Status)
	}

	// Accepting the successor is what retires it.
	if _, err := s.patchRecord(ctx, team, successor.ID, []byte(`{"status":"accepted"}`)); err != nil {
		t.Fatalf("accept successor: %v", err)
	}
	retired, _ := s.getRecordByID(ctx, team, first.ID)
	if retired.Status != recordSuperseded {
		t.Errorf("predecessor is %q, want superseded", retired.Status)
	}

	// A superseded record is closed for edits — the chain moves forward only.
	_, err = s.patchRecord(ctx, team, first.ID, []byte(`{"title":"rewriting history"}`))
	var te errRecordTransition
	if !errors.As(err, &te) {
		t.Fatalf("expected a refusal editing a superseded record, got %v", err)
	}
}

func TestRecordProvenanceComesFromTheToken(t *testing.T) {
	s, _ := newTestServer(t)
	team := defaultTeamID
	project := seedRecordProject(t, s, team, "records-provenance")

	agentID := NewID()
	if _, err := s.writeDB.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		VALUES (?, ?, 'worker.alpha', 'claude-code', 'running', ?)`, agentID, team, NowUTC()); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	// An agent's token says who it is; the body is not consulted.
	agentCtx := auth.WithToken(context.Background(), &auth.Token{
		Kind: "agent", ScopeJSON: `{"team":"` + team + `","handle":"worker.alpha"}`,
	})
	// The body LIES about its provenance and about being pre-accepted.
	body := recordBody{Kind: "finding", Title: "seeded", Status: recordAccepted}
	rec, err := s.createRecord(agentCtx, team, project, body, "")
	if err != nil {
		t.Fatalf("create as agent: %v", err)
	}
	if rec.CreatedByKind != "agent" {
		t.Errorf("created_by_kind = %q, want agent", rec.CreatedByKind)
	}
	if rec.CreatedByID != agentID {
		t.Errorf("created_by_id = %q, want the agent's id %q", rec.CreatedByID, agentID)
	}
	if rec.Status != recordProposed {
		t.Errorf("an agent's record is %q — agents propose, the director accepts", rec.Status)
	}

	// The director's own writes are attributed to the director, and may be
	// accepted on creation.
	userRec, err := s.createRecord(context.Background(), team, project,
		recordBody{Kind: "decision", Title: "mine", Status: recordAccepted}, "")
	if err != nil {
		t.Fatalf("create as user: %v", err)
	}
	if userRec.CreatedByKind != "user" || userRec.CreatedByID != "" {
		t.Errorf("director provenance: %+v", userRec)
	}
	if userRec.Status != recordAccepted {
		t.Errorf("the director may accept on creation, got %q", userRec.Status)
	}

	// An agent whose handle no longer resolves is still an AGENT — dropping to
	// "user" would misattribute the write to the director.
	ghostCtx := auth.WithToken(context.Background(), &auth.Token{
		Kind: "agent", ScopeJSON: `{"team":"` + team + `","handle":"worker.gone"}`,
	})
	ghost, err := s.createRecord(ghostCtx, team, project, recordBody{Kind: "finding", Title: "orphan"}, "")
	if err != nil {
		t.Fatalf("create as ghost agent: %v", err)
	}
	if ghost.CreatedByKind != "agent" || ghost.CreatedByID != "" {
		t.Errorf("unresolved agent provenance: %+v", ghost)
	}
}

func TestRecordsAreScopedThroughTheirProject(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	project := seedRecordProject(t, s, defaultTeamID, "records-scope")
	rec, _ := s.createRecord(ctx, defaultTeamID, project, recordBody{Kind: "decision", Title: "secret"}, "")

	otherTeam := NewID()
	if _, err := s.writeDB.Exec(`INSERT INTO teams (id, name, created_at) VALUES (?, 'other', ?)`,
		otherTeam, NowUTC()); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if _, err := s.getRecordByID(ctx, otherTeam, rec.ID); err == nil {
		t.Error("another team read a record by id")
	}
	if rs, _ := s.listRecords(ctx, otherTeam, recordFilter{}); len(rs) != 0 {
		t.Errorf("another team listed %d records", len(rs))
	}
	// …including the write paths, which resolve the same edge.
	if err := s.deleteRecord(ctx, otherTeam, rec.ID); err == nil {
		t.Error("another team deleted a record")
	}
	if _, err := s.patchRecord(ctx, otherTeam, rec.ID, []byte(`{"title":"x"}`)); err == nil {
		t.Error("another team patched a record")
	}
}

// The propose-verb posture must hold on the MUTATING paths too, not only on
// create: teamGate checks the token's team and not its kind, so an agent
// bearer reaches PATCH and DELETE. Before recordAgentGate an agent could
// self-accept its own proposal — and, via /supersede plus a self-accept,
// retire the director's accepted decision.
func TestRecordAgentsNeverAcceptNorTouchOthers(t *testing.T) {
	s, _ := newTestServer(t)
	team := defaultTeamID
	project := seedRecordProject(t, s, team, "records-agent-gate")

	agentA := NewID()
	agentB := NewID()
	for _, a := range []struct{ id, handle string }{{agentA, "worker.a"}, {agentB, "worker.b"}} {
		if _, err := s.writeDB.Exec(`
			INSERT INTO agents (id, team_id, handle, kind, status, created_at)
			VALUES (?, ?, ?, 'claude-code', 'running', ?)`, a.id, team, a.handle, NowUTC()); err != nil {
			t.Fatalf("seed agent: %v", err)
		}
	}
	ctxA := auth.WithToken(context.Background(), &auth.Token{
		Kind: "agent", ScopeJSON: `{"team":"` + team + `","handle":"worker.a"}`,
	})
	ctxB := auth.WithToken(context.Background(), &auth.Token{
		Kind: "agent", ScopeJSON: `{"team":"` + team + `","handle":"worker.b"}`,
	})
	userCtx := context.Background()

	prop, err := s.createRecord(ctxA, team, project, recordBody{Kind: "finding", Title: "agent proposal"}, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 1. The self-accept: the exact escalation the gate exists to refuse.
	var fe errRecordForbidden
	if _, err := s.patchRecord(ctxA, team, prop.ID, []byte(`{"status":"accepted"}`)); !errors.As(err, &fe) {
		t.Fatalf("agent self-accept: err = %v, want errRecordForbidden", err)
	}
	if got, _ := s.getRecordByID(userCtx, team, prop.ID); got.Status != recordProposed {
		t.Fatalf("status moved to %q under a refused accept", got.Status)
	}

	// 2. An agent may edit its OWN proposal's content…
	if _, err := s.patchRecord(ctxA, team, prop.ID, []byte(`{"title":"agent proposal, clarified"}`)); err != nil {
		t.Fatalf("agent editing its own proposal: %v", err)
	}
	// …and another agent may not.
	if _, err := s.patchRecord(ctxB, team, prop.ID, []byte(`{"title":"hijacked"}`)); !errors.As(err, &fe) {
		t.Fatalf("agent editing another agent's proposal: err = %v, want errRecordForbidden", err)
	}

	// 3. Delete: an agent withdraws only its own proposal.
	if err := s.deleteRecord(ctxB, team, prop.ID); !errors.As(err, &fe) {
		t.Fatalf("agent deleting another agent's proposal: err = %v, want errRecordForbidden", err)
	}
	userProp, _ := s.createRecord(userCtx, team, project, recordBody{Kind: "decision", Title: "the director's proposal"}, "")
	if err := s.deleteRecord(ctxA, team, userProp.ID); !errors.As(err, &fe) {
		t.Fatalf("agent deleting the director's proposal: err = %v, want errRecordForbidden", err)
	}
	if err := s.deleteRecord(ctxA, team, prop.ID); err != nil {
		t.Fatalf("agent withdrawing its own proposal: %v", err)
	}

	// 4. The supersede escalation end-to-end: the successor an agent proposes
	//    against an accepted decision stays a proposal it cannot promote, and
	//    the predecessor stays accepted.
	accepted, err := s.createRecord(userCtx, team, project,
		recordBody{Kind: "decision", Title: "the decision", Status: recordAccepted}, "")
	if err != nil {
		t.Fatalf("create accepted: %v", err)
	}
	successor, err := s.createRecord(ctxA, team, project, recordBody{Kind: "decision", Title: "revision"}, accepted.ID)
	if err != nil {
		t.Fatalf("agent proposing a successor: %v", err)
	}
	if _, err := s.patchRecord(ctxA, team, successor.ID, []byte(`{"status":"accepted"}`)); !errors.As(err, &fe) {
		t.Fatalf("agent accepting its own successor: err = %v, want errRecordForbidden", err)
	}
	if got, _ := s.getRecordByID(userCtx, team, accepted.ID); got.Status != recordAccepted {
		t.Fatalf("the accepted decision was retired by a refused self-accept (status %q)", got.Status)
	}
	// An agent may not rewrite history either — accepted records are not its
	// to edit, even its own.
	if _, err := s.patchRecord(ctxA, team, accepted.ID, []byte(`{"title":"rewritten"}`)); !errors.As(err, &fe) {
		t.Fatalf("agent editing an accepted record: err = %v, want errRecordForbidden", err)
	}
	// A ghost agent (handle no longer resolves) owns nothing and fails closed.
	ghostCtx := auth.WithToken(context.Background(), &auth.Token{
		Kind: "agent", ScopeJSON: `{"team":"` + team + `","handle":"worker.gone"}`,
	})
	ghost, err := s.createRecord(ghostCtx, team, project, recordBody{Kind: "finding", Title: "orphan"}, "")
	if err != nil {
		t.Fatalf("ghost create: %v", err)
	}
	if err := s.deleteRecord(ghostCtx, team, ghost.ID); !errors.As(err, &fe) {
		t.Fatalf("ghost agent deleting its unattributed proposal: err = %v, want errRecordForbidden", err)
	}

	// The director still does everything the log allows.
	if _, err := s.patchRecord(userCtx, team, successor.ID, []byte(`{"status":"accepted"}`)); err != nil {
		t.Fatalf("director accepting the successor: %v", err)
	}
	if got, _ := s.getRecordByID(userCtx, team, accepted.ID); got.Status != recordSuperseded {
		t.Fatalf("predecessor after director acceptance: %q, want superseded", got.Status)
	}
}
