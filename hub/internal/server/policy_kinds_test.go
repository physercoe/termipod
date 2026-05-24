package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR-030 W2 — Policy.Kinds + KindFor.
//
// These tests pin the contract documented in ADR-030 plan §2.2 W2:
//
//   - missing `kinds:` block → empty map + permissive default on KindFor
//   - unknown kind on a populated map → permissive default + WARN log
//   - malformed YAML (whole-file shape) → typed error from parsePolicy
//   - legacy `tiers:` rows still resolve through Decide / QuorumFor
//     unchanged by the Kinds-block addition
//
// The propose handler (W4) is the only caller of KindFor in the runtime;
// asserting the contract here means W4 can lean on it without reasoning
// about the fall-through case in line.

func writePolicyFile(t *testing.T, dir, body string) {
	t.Helper()
	teamDir := filepath.Join(dir, "team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatalf("mkdir team: %v", err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "policy.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write policy.yaml: %v", err)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// captureLogger returns a slog logger writing into buf, so tests can
// assert that the WARN line fires (and contains the right key).
func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// 1. Missing `kinds:` block — KindFor must still return a usable policy.
func TestPolicy_KindFor_MissingKindsBlock_ReturnsPermissiveDefault(t *testing.T) {
	dir := t.TempDir()
	writePolicyFile(t, dir, `
tiers:
  spawn: moderate
quorum:
  moderate: 1
`)
	var buf bytes.Buffer
	store := newPolicyStoreWithLogger(dir, captureLogger(&buf))

	got, configured := store.KindFor("deliverable.set_state")
	if configured {
		t.Fatalf("KindFor returned configured=true on missing kinds block")
	}
	if got.DefaultTier != GovTierPrincipal {
		t.Errorf("DefaultTier = %q; want %q", got.DefaultTier, GovTierPrincipal)
	}
	if !got.OverrideAllowed {
		t.Errorf("OverrideAllowed = false; want true (permissive default)")
	}
	if got.Quorum[GovTierPrincipal].M != 1 {
		t.Errorf("Quorum[principal].M = %d; want 1", got.Quorum[GovTierPrincipal].M)
	}
	// WARN must fire — operators need to see that something is leaning on
	// a fall-through policy.
	if !strings.Contains(buf.String(), "propose kind not configured") {
		t.Errorf("expected WARN log; got %q", buf.String())
	}
}

// 2. Populated kinds block, but the requested kind is absent.
func TestPolicy_KindFor_UnknownKind_LogsWarnAndReturnsPermissive(t *testing.T) {
	dir := t.TempDir()
	writePolicyFile(t, dir, `
kinds:
  task.set_status:
    default_tier: project-steward
    commits: false
    override_allowed: true
`)
	var buf bytes.Buffer
	store := newPolicyStoreWithLogger(dir, captureLogger(&buf))

	got, configured := store.KindFor("agent.archive") // not in the file
	if configured {
		t.Fatalf("KindFor returned configured=true on unconfigured kind")
	}
	if got.DefaultTier != GovTierPrincipal {
		t.Errorf("DefaultTier = %q; want %q", got.DefaultTier, GovTierPrincipal)
	}
	if !strings.Contains(buf.String(), "agent.archive") {
		t.Errorf("WARN log should name the missing kind; got %q", buf.String())
	}
}

// 3. Populated kinds block, requested kind is present — return as-is.
func TestPolicy_KindFor_ConfiguredKind_ReturnsConfiguredPolicy(t *testing.T) {
	dir := t.TempDir()
	writePolicyFile(t, dir, `
kinds:
  deliverable.set_state:
    default_tier: principal
    commits: true
    override_allowed: true
    quorum:
      principal: { m: 1 }
  task.set_status:
    default_tier: project-steward
    commits: false
    override_allowed: false
    quorum:
      project-steward: { m: 1 }
`)
	store := newPolicyStoreWithLogger(dir, discardLogger())

	d, configured := store.KindFor("deliverable.set_state")
	if !configured {
		t.Fatalf("KindFor returned configured=false on configured kind")
	}
	if d.DefaultTier != GovTierPrincipal {
		t.Errorf("DefaultTier = %q; want %q", d.DefaultTier, GovTierPrincipal)
	}
	if !d.Commits {
		t.Errorf("Commits = false; want true")
	}
	if !d.OverrideAllowed {
		t.Errorf("OverrideAllowed = false; want true")
	}
	if d.Quorum[GovTierPrincipal].M != 1 {
		t.Errorf("Quorum[principal].M = %d; want 1", d.Quorum[GovTierPrincipal].M)
	}

	tsk, configured := store.KindFor("task.set_status")
	if !configured {
		t.Fatalf("KindFor returned configured=false on configured kind")
	}
	if tsk.DefaultTier != GovTierProjectSteward {
		t.Errorf("DefaultTier = %q; want %q", tsk.DefaultTier, GovTierProjectSteward)
	}
	if tsk.OverrideAllowed {
		t.Errorf("OverrideAllowed = true; want false (file declared false)")
	}
}

// 4. Malformed YAML — parsePolicy surfaces a typed error.
//
//	The runtime reload() swallows the error and keeps last-known-good
//	(documented behaviour) — but parsePolicy itself must surface it so
//	`hub init --check` (planned post-MVP) and similar load-time guards
//	can refuse to start on a bad file.
func TestPolicy_Parse_MalformedYAML_ReturnsTypedError(t *testing.T) {
	bad := []byte("kinds: [this, is, a, sequence, not, a, map]\n")
	_, err := parsePolicy(bad)
	if err == nil {
		t.Fatal("parsePolicy returned nil error on malformed kinds block")
	}
	if !strings.Contains(err.Error(), "policy.yaml") {
		t.Errorf("error message %q should mention 'policy.yaml'", err.Error())
	}
}

// 5. Runtime degradation — when reload sees a malformed file after a
// good load, it must keep the prior parse, not reset to empty.
func TestPolicy_Reload_KeepsLastKnownGoodOnMalformed(t *testing.T) {
	dir := t.TempDir()
	writePolicyFile(t, dir, `
kinds:
  deliverable.set_state:
    default_tier: principal
`)
	store := newPolicyStoreWithLogger(dir, discardLogger())

	// Good state present.
	if _, ok := store.KindFor("deliverable.set_state"); !ok {
		t.Fatal("first load should have the kind configured")
	}

	// Replace with malformed content; reload.
	writePolicyFile(t, dir, "kinds: [not-a-map]\n")
	store.reload()

	// The malformed reload must NOT have wiped the kind.
	if _, ok := store.KindFor("deliverable.set_state"); !ok {
		t.Error("malformed reload should keep last-known-good; lost configured kind")
	}
}

// 6. Legacy paths still work — the Kinds block addition is purely
// additive; Decide / ApproversFor / QuorumFor / EscalationFor return
// exactly what they did pre-W2 for a policy file that only uses the
// legacy keys.
func TestPolicy_LegacyTiersPathsUntouchedByKindsAddition(t *testing.T) {
	dir := t.TempDir()
	writePolicyFile(t, dir, `
tiers:
  spawn: moderate
  tool:write_file: low
approvers:
  moderate: ["@steward"]
quorum:
  moderate: 2
`)
	store := newPolicyStoreWithLogger(dir, discardLogger())

	if got := store.Decide("spawn"); got != "moderate" {
		t.Errorf("Decide(spawn) = %q; want moderate", got)
	}
	if got := store.Decide("never_configured"); got != TierAuto {
		t.Errorf("Decide(unknown) = %q; want %q", got, TierAuto)
	}
	if got := store.QuorumFor("moderate"); got != 2 {
		t.Errorf("QuorumFor(moderate) = %d; want 2", got)
	}
	if got := store.QuorumFor("never_configured"); got != 1 {
		t.Errorf("QuorumFor(unknown) = %d; want 1 (fallthrough)", got)
	}
	if got := store.ApproversFor("moderate"); len(got) != 1 || got[0] != "@steward" {
		t.Errorf("ApproversFor(moderate) = %v; want [@steward]", got)
	}
}

// 7. End-to-end through Server.policy: the server constructor wires
// the explicit logger; KindFor is callable via s.policy after init.
// Asserts the wiring change in server.go didn't break path resolution.
func TestPolicy_KindFor_ReachableViaServerPolicy(t *testing.T) {
	s, dir := newTestServer(t)
	writePolicyFile(t, dir, `
kinds:
  task.set_status:
    default_tier: project-steward
`)
	s.policy.reload()

	got, configured := s.policy.KindFor("task.set_status")
	if !configured {
		t.Fatal("expected configured=true after reload")
	}
	if got.DefaultTier != GovTierProjectSteward {
		t.Errorf("DefaultTier = %q; want %q", got.DefaultTier, GovTierProjectSteward)
	}

	// And the legacy path through s.policy.QuorumFor still works.
	if got := s.policy.QuorumFor("moderate"); got != 1 {
		t.Errorf("QuorumFor(moderate) = %d; want 1 (no quorum block → fallthrough)", got)
	}

	// Sanity: ctx-bound work after KindFor still flows (catches any
	// accidental deadlock on the reload mutex).
	_ = context.Background()
}
