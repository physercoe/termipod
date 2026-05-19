package server

import (
	"encoding/json"
	"testing"
)

func TestComposeMessage_RoundTrip(t *testing.T) {
	from := MessageEndpoint{Role: RolePrincipal}
	to := MessageEndpoint{Role: RolePeerSteward, Handle: "research-steward", AgentID: "agt_1"}
	thread := MessageThread{Transport: TransportSession, ID: "ses_1"}

	env := composeMessage(from, to, KindDirective, "ship the thing", "tsk_root", thread)

	// Marshal as the flat payload, then parse back — the round trip a
	// driver / the admission pipeline performs.
	raw, err := json.Marshal(env.PayloadMap())
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	got, err := parseEnvelope(raw)
	if err != nil {
		t.Fatalf("parseEnvelope: %v", err)
	}
	if got != env {
		t.Errorf("round trip mismatch:\n got = %+v\nwant = %+v", got, env)
	}
}

func TestComposeMessage_EveryKindAndRole(t *testing.T) {
	kinds := []string{KindDirective, KindQuestion, KindReport, KindNotification}
	roles := []string{RolePrincipal, RolePeerSteward, RolePeerWorker, RoleSystem}
	thread := MessageThread{Transport: TransportA2A, ID: "req_1"}

	for _, k := range kinds {
		for _, r := range roles {
			env := composeMessage(
				MessageEndpoint{Role: r},
				MessageEndpoint{Role: RolePeerWorker, Handle: "w"},
				k, "body", "", thread)
			if env.Kind != k {
				t.Errorf("kind=%s role=%s: got kind %q", k, r, env.Kind)
			}
			if !validEnvelopeKind(env.Kind) || !validEnvelopeRole(r) || !validEnvelopeTransport(env.Thread.Transport) {
				t.Errorf("kind=%s role=%s: composed an invalid envelope", k, r)
			}
		}
	}
}

func TestComposeMessage_UnknownKindFallsBackToNotification(t *testing.T) {
	env := composeMessage(
		MessageEndpoint{Role: RoleSystem},
		MessageEndpoint{Role: RolePeerSteward},
		"reply", "body", "", MessageThread{Transport: TransportSession, ID: "s"})
	if env.Kind != KindNotification {
		t.Errorf("unknown kind: got %q, want notification (never directive)", env.Kind)
	}
}

func TestPayloadMap_OmitsEmptyCause(t *testing.T) {
	env := composeMessage(
		MessageEndpoint{Role: RolePrincipal},
		MessageEndpoint{Role: RolePeerSteward},
		KindDirective, "go", "", MessageThread{Transport: TransportSession, ID: "s"})
	if _, ok := env.PayloadMap()["cause"]; ok {
		t.Error("untied envelope: cause key should be omitted from the payload")
	}

	tied := composeMessage(
		MessageEndpoint{Role: RolePrincipal},
		MessageEndpoint{Role: RolePeerSteward},
		KindDirective, "go", "tsk_1", MessageThread{Transport: TransportSession, ID: "s"})
	if tied.PayloadMap()["cause"] != "tsk_1" {
		t.Error("tied envelope: cause should be present in the payload")
	}
}
