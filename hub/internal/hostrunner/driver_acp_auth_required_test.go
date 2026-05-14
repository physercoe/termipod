package hostrunner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestACPDriver_AuthRequired_EmitsAttentionAndWrapsError covers the
// ADR-026 W3 contract: when the ACP daemon returns an AUTH_REQUIRED
// error from session/new, the driver
//
//   1. emits an `attention_request` agent_event with kind=auth_required
//      and engine-specific remediation text, AND
//   2. returns an error from Start() whose message references both the
//      engine host AND the login command.
//
// Without (1) the mobile transcript would surface no actionable card;
// without (2) the spawn-failure toast would just say "rpc error
// -32603: AUTH_REQUIRED" with no remediation. Both surfaces matter.
func TestACPDriver_AuthRequired_EmitsAttentionAndWrapsError(t *testing.T) {
	driverIn, agentOut := io.Pipe()
	agentIn, driverOut := io.Pipe()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID:          "agent-kimi-auth",
		Poster:           poster,
		Stdin:            driverOut,
		Stdout:           driverIn,
		Closer:           func() { _ = agentOut.Close(); _ = agentIn.Close() },
		Log:              slog.Default(),
		HandshakeTimeout: 2 * time.Second,
		WriteTimeout:     1 * time.Second,
		EngineKind:       "kimi-code",
	}

	agent := newFakeACPAgent(t, agentIn, agentOut, "sess-unused")
	agent.failSessionNewWithAuth = true
	go agent.serve()

	startErr := drv.Start(context.Background())
	if startErr == nil {
		t.Fatal("Start should have failed when session/new returns AUTH_REQUIRED")
	}
	// Error must reference both the auth-required class AND the
	// engine-specific remediation. Operator-actionable on first read.
	msg := startErr.Error()
	if !strings.Contains(msg, "authentication required") {
		t.Errorf("error message should call out auth — got: %v", msg)
	}
	if !strings.Contains(msg, "kimi login") {
		t.Errorf("error message should include the `kimi login` remediation — got: %v", msg)
	}

	// The attention_request agent_event must have been posted.
	var saw bool
	for _, ev := range poster.snapshot() {
		if ev.Kind != "attention_request" {
			continue
		}
		kind, _ := ev.Payload["kind"].(string)
		if kind != "auth_required" {
			continue
		}
		saw = true
		if engine, _ := ev.Payload["engine_kind"].(string); engine != "kimi-code" {
			t.Errorf("attention event engine_kind = %q; want kimi-code", engine)
		}
		remed, _ := ev.Payload["remediation"].(string)
		if !strings.Contains(remed, "kimi login") {
			t.Errorf("attention remediation should mention `kimi login`: %v", remed)
		}
		reason, _ := ev.Payload["reason"].(string)
		if !strings.Contains(strings.ToUpper(reason), "AUTH_REQUIRED") {
			t.Errorf("attention reason should carry the daemon's raw message: %v", reason)
		}
	}
	if !saw {
		t.Errorf("no attention_request/auth_required event posted; events = %+v", poster.snapshot())
	}

	drv.Stop()
}

// TestACPDriver_AuthRequired_EngineNeutralFallback ensures EngineKind=""
// still gets a usable remediation string (post-auth wedge for engines
// we add later without a Go change).
func TestACPDriver_AuthRequired_EngineNeutralFallback(t *testing.T) {
	d := &ACPDriver{EngineKind: ""}
	remed := d.authRequiredRemediation()
	if !strings.Contains(strings.ToLower(remed), "authenticate") {
		t.Errorf("fallback remediation must mention authentication; got: %v", remed)
	}
	if strings.Contains(remed, "kimi login") || strings.Contains(remed, "gemini auth") {
		t.Errorf("fallback remediation must not be engine-specific; got: %v", remed)
	}
}

// TestIsAuthRequiredError covers the substring detector. Direction of
// false positives matters here — we'd rather miss a misformatted
// AUTH_REQUIRED than wrap an unrelated error with login text.
func TestIsAuthRequiredError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"kimi style", errors.New("rpc error -32603: AUTH_REQUIRED: Run kimi login"), true},
		{"lowercase prose", errors.New("authentication required to call session/new"), true},
		{"mixed case", errors.New("Auth_Required"), true},
		{"unrelated", errors.New("rpc error -32000: stale cursor"), false},
		{"empty", errors.New(""), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAuthRequiredError(tc.err); got != tc.want {
				t.Errorf("isAuthRequiredError(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}
