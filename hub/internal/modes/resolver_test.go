package modes

import (
	"errors"
	"strings"
	"testing"
)

func TestResolve_HappyPath_Requested(t *testing.T) {
	r, err := Resolve(Input{
		AgentKind:     "claude-code",
		Requested:     "M2",
		Billing:       BillingSubscription,
		HostInstalled: true,
		HostSupports:  []string{"M1", "M2", "M4"},
	})
	if err != nil {
		t.Fatalf("want ok; got %v", err)
	}
	if r.Mode != "M2" {
		t.Fatalf("Mode = %q; want M2", r.Mode)
	}
	if !strings.Contains(r.Reason, "requested") {
		t.Fatalf("Reason %q should say 'requested'", r.Reason)
	}
}

func TestResolve_OverrideWins(t *testing.T) {
	r, err := Resolve(Input{
		AgentKind:     "claude-code",
		Requested:     "M2",
		FallbackModes: []string{"M4"},
		Override:      "M4",
		HostInstalled: true,
		HostSupports:  []string{"M1", "M2", "M4"},
	})
	if err != nil {
		t.Fatalf("want ok; got %v", err)
	}
	if r.Mode != "M4" {
		t.Fatalf("Mode = %q; want M4", r.Mode)
	}
	if !strings.Contains(r.Reason, "override") {
		t.Fatalf("Reason %q should say 'override'", r.Reason)
	}
}

func TestResolve_OverrideIsStrict_NoFallback(t *testing.T) {
	// Override names a mode the host doesn't support. Must fail — we
	// don't fall back to the template candidates when an override is set.
	_, err := Resolve(Input{
		AgentKind:     "aider",
		Requested:     "M4",
		FallbackModes: []string{"M4"},
		Override:      "M1",
		HostInstalled: true,
		HostSupports:  []string{"M2", "M4"},
	})
	if err == nil {
		t.Fatal("want error for strict override; got nil")
	}
	var e *Error
	if !errors.As(err, &e) || len(e.Reasons) != 1 {
		t.Fatalf("want single-reason Error; got %v", err)
	}
	if !strings.Contains(e.Reasons[0], "does not support M1") {
		t.Fatalf("reason %q should mention unsupported M1", e.Reasons[0])
	}
}

func TestResolve_FallsBackOnUnsupportedRequested(t *testing.T) {
	// Gemini CLI doesn't expose M2; template asks for M2 but lists M1 as
	// a fallback — we should land on M1 with a dropped-candidate note.
	r, err := Resolve(Input{
		AgentKind:     "gemini-cli",
		Requested:     "M2",
		FallbackModes: []string{"M1", "M4"},
		HostInstalled: true,
		HostSupports:  []string{"M1", "M4"},
	})
	if err != nil {
		t.Fatalf("want ok; got %v", err)
	}
	if r.Mode != "M1" {
		t.Fatalf("Mode = %q; want M1", r.Mode)
	}
	if !strings.Contains(r.Reason, "fallback") {
		t.Fatalf("Reason %q should say 'fallback'", r.Reason)
	}
	if !strings.Contains(r.Reason, "M2") {
		t.Fatalf("Reason %q should record dropped M2", r.Reason)
	}
}

func TestResolve_ClaudeCodeM1UnderSubscriptionBlocked(t *testing.T) {
	// The blueprint billing caveat: Agent SDK (which M1's ACP adapter
	// wraps) only supports api_key billing.
	r, err := Resolve(Input{
		AgentKind:     "claude-code",
		Requested:     "M1",
		FallbackModes: []string{"M2"},
		Billing:       BillingSubscription,
		HostInstalled: true,
		HostSupports:  []string{"M1", "M2", "M4"},
	})
	if err != nil {
		t.Fatalf("want ok via fallback; got %v", err)
	}
	if r.Mode != "M2" {
		t.Fatalf("Mode = %q; want M2", r.Mode)
	}
	if !strings.Contains(r.Reason, "M1") {
		t.Fatalf("Reason %q should record dropped M1", r.Reason)
	}
}

func TestResolve_ClaudeCodeM1UnderAPIKeyAllowed(t *testing.T) {
	r, err := Resolve(Input{
		AgentKind:     "claude-code",
		Requested:     "M1",
		Billing:       BillingAPIKey,
		HostInstalled: true,
		HostSupports:  []string{"M1", "M2", "M4"},
	})
	if err != nil {
		t.Fatalf("want ok; got %v", err)
	}
	if r.Mode != "M1" {
		t.Fatalf("Mode = %q; want M1", r.Mode)
	}
}

func TestResolve_NotInstalledFailsAllCandidates(t *testing.T) {
	_, err := Resolve(Input{
		AgentKind:     "codex",
		Requested:     "M1",
		FallbackModes: []string{"M4"},
		HostInstalled: false,
	})
	if err == nil {
		t.Fatal("want error when not installed; got nil")
	}
	var e *Error
	if !errors.As(err, &e) || len(e.Reasons) != 2 {
		t.Fatalf("want 2 rejection reasons; got %v", err)
	}
	for _, r := range e.Reasons {
		if !strings.Contains(r, "not installed") {
			t.Fatalf("reason %q should mention not installed", r)
		}
	}
}

func TestResolve_NoRequestedNoOverride(t *testing.T) {
	_, err := Resolve(Input{
		AgentKind:     "claude-code",
		HostInstalled: true,
		HostSupports:  []string{"M1", "M2", "M4"},
	})
	if err == nil {
		t.Fatal("want error when nothing requested; got nil")
	}
	var e *Error
	if !errors.As(err, &e) || !strings.Contains(e.Error(), "no mode requested") {
		t.Fatalf("want 'no mode requested'; got %v", err)
	}
}

func TestResolve_NormalizesAndDedups(t *testing.T) {
	// Lower-case + whitespace + duplicate — all normalised away.
	r, err := Resolve(Input{
		AgentKind:     "claude-code",
		Requested:     " m2 ",
		FallbackModes: []string{"M2", "M4"},
		HostInstalled: true,
		HostSupports:  []string{"M2", "M4"},
	})
	if err != nil {
		t.Fatalf("want ok; got %v", err)
	}
	if r.Mode != "M2" {
		t.Fatalf("Mode = %q; want M2", r.Mode)
	}
}

func TestResolve_UnknownModeStringIsIgnored(t *testing.T) {
	// "M3" is explicitly not a mode (§5.3.1 note about headless one-shot).
	// It should be filtered out, not produce a wrong-mode rejection.
	r, err := Resolve(Input{
		AgentKind:     "claude-code",
		Requested:     "M3",
		FallbackModes: []string{"M2"},
		HostInstalled: true,
		HostSupports:  []string{"M2", "M4"},
	})
	if err != nil {
		t.Fatalf("want fallback to M2; got %v", err)
	}
	if r.Mode != "M2" {
		t.Fatalf("Mode = %q; want M2", r.Mode)
	}
}
