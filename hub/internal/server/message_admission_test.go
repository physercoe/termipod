package server

import (
	"context"
	"testing"
)

func validTestEnvelope() MessageEnvelope {
	return MessageEnvelope{
		From:   MessageEndpoint{Role: RolePrincipal},
		To:     MessageEndpoint{Role: RolePeerSteward, Handle: "s", AgentID: "agt_s"},
		Kind:   KindDirective,
		Text:   "go",
		Thread: MessageThread{Transport: TransportSession, ID: "ses"},
	}
}

func TestAdmission_ValidateSchema(t *testing.T) {
	if ae := validateEnvelopeSchema(validTestEnvelope()); ae != nil {
		t.Fatalf("a valid envelope was rejected: %v", ae)
	}

	bad := map[string]func(*MessageEnvelope){
		"bad_kind":        func(e *MessageEnvelope) { e.Kind = "shout" },
		"bad_from_role":   func(e *MessageEnvelope) { e.From.Role = "boss" },
		"to_is_principal": func(e *MessageEnvelope) { e.To.Role = RolePrincipal },
		"to_is_system":    func(e *MessageEnvelope) { e.To.Role = RoleSystem },
		"bad_transport":   func(e *MessageEnvelope) { e.Thread.Transport = "carrier" },
	}
	for name, mutate := range bad {
		env := validTestEnvelope()
		mutate(&env)
		ae := validateEnvelopeSchema(env)
		if ae == nil {
			t.Errorf("%s: expected a schema rejection", name)
			continue
		}
		if ae.Stage != "validate" {
			t.Errorf("%s: stage = %q, want validate", name, ae.Stage)
		}
		if ae.Hint.HintText == "" {
			t.Errorf("%s: rejection carries no hint", name)
		}
	}
}

func TestAdmission_RoutingLegality(t *testing.T) {
	// A worker may not open a directive.
	wd := validTestEnvelope()
	wd.From.Role = RolePeerWorker
	wd.Kind = KindDirective
	if ae := checkRoutingLegality(wd); ae == nil || ae.Stage != "routing" {
		t.Errorf("a worker directive must be denied; got %v", ae)
	}

	// notification is system-only.
	nn := validTestEnvelope()
	nn.From.Role = RolePeerSteward
	nn.Kind = KindNotification
	if ae := checkRoutingLegality(nn); ae == nil {
		t.Error("a non-system notification must be denied")
	}

	// A worker report is legal.
	wr := validTestEnvelope()
	wr.From.Role = RolePeerWorker
	wr.Kind = KindReport
	if ae := checkRoutingLegality(wr); ae != nil {
		t.Errorf("a worker report must be allowed; got %v", ae)
	}
}

func TestAdmission_ContextCauseResolution(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-admission")
	_, taskID := seedAssignerAndTask(t, s, proj, "real task")

	// A cause pointing at a live task admits.
	env := validTestEnvelope()
	env.Cause = taskID
	if ae := s.admitEnvelope(context.Background(), env, false); ae != nil {
		t.Errorf("envelope with a live cause should admit; got %v", ae)
	}

	// A dangling cause is rejected; an agent-origin rejection is
	// recoverable (→ 422 + hint).
	env.Cause = "tsk_does_not_exist"
	ae := s.admitEnvelope(context.Background(), env, true)
	if ae == nil {
		t.Fatal("a dangling cause must be rejected")
	}
	if !ae.AgentRecoverable {
		t.Error("an agent-origin rejection must be recoverable")
	}
	if ae.Hint.HintText == "" {
		t.Error("a recoverable rejection must carry a hint")
	}

	// The same dangling cause from a hub writer is a programming error
	// — not recoverable.
	if ae := s.admitEnvelope(context.Background(), env, false); ae == nil || ae.AgentRecoverable {
		t.Errorf("a hub-composed dangling cause must be a non-recoverable error; got %v", ae)
	}
}
