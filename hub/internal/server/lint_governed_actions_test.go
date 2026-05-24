package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ADR-030 W3 — drives scripts/lint-governed-actions.sh through its
// expected paths. Each subtest:
//   - writes a tiny Go fixture that contains a literal
//     RegisterProposeKind call (so the script's static-grep finds it),
//   - optionally writes a policy.yaml fixture under a temp dir,
//   - shells out to the script with --registry-glob and --policy
//     pinned at those fixtures (so the test's view of "registered"
//     and "declared" is fully isolated from the real repo state),
//   - asserts the script's exit code + key stdout/stderr lines.
//
// We invoke the shell script rather than re-implement its logic in Go
// because the contract IS the script — operators run it locally,
// CI runs it. A pure-Go test would let the two implementations drift.

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// .../hub/internal/server/lint_governed_actions_test.go → repo root.
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

// runLint runs the script with arbitrary args + a temp fixtures dir
// as the registry glob. The fixtures dir is created with a single
// .go file holding the desired RegisterProposeKind calls.
func runLint(t *testing.T, fixturesDir, policy string, extra ...string) (string, int) {
	t.Helper()
	root := repoRoot(t)
	script := filepath.Join(root, "scripts", "lint-governed-actions.sh")
	args := []string{script, "--registry-glob", filepath.Join(fixturesDir, "*.go")}
	if policy != "" {
		args = append(args, "--policy", policy)
	}
	args = append(args, extra...)
	cmd := exec.Command("bash", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("script exec: %v\noutput:\n%s", err, out)
		}
	}
	return string(out), code
}

// writeRegistryFixture creates a .go file in dir that contains literal
// RegisterProposeKind blocks for each name in kinds.
func writeRegistryFixture(t *testing.T, dir string, kinds ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("package fake\n\nfunc Register(){\n")
	for _, k := range kinds {
		b.WriteString("RegisterProposeKind(ProposeKind{\n")
		b.WriteString("  Kind: \"")
		b.WriteString(k)
		b.WriteString("\",\n")
		b.WriteString("})\n")
	}
	b.WriteString("}\n")
	if err := os.WriteFile(filepath.Join(dir, "fake_register.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func writeYAMLFixture(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	return path
}

// 1. Empty registry, no policy → trivial clean pass.
func TestLintGovernedActions_EmptyRegistry_NoPolicy_Clean(t *testing.T) {
	dir := t.TempDir()
	// Drop a no-RegisterProposeKind .go file so the glob is non-empty
	// but the registry stays at 0.
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package fake\n"), 0o644); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	out, code := runLint(t, dir, "", "--no-warn-empty")
	if code != 0 {
		t.Fatalf("exit code = %d; want 0\n%s", code, out)
	}
	if !strings.Contains(out, "lint-governed-actions: clean") {
		t.Errorf("expected clean summary; got:\n%s", out)
	}
}

// 2. Registered kind absent from policy → FAIL [registered-no-policy].
func TestLintGovernedActions_RegisteredButNotInPolicy_Fails(t *testing.T) {
	dir := t.TempDir()
	writeRegistryFixture(t, dir, "deliverable.set_state")
	policy := writeYAMLFixture(t, dir, "kinds:\n  task.set_status:\n    default_tier: project-steward\n")
	out, code := runLint(t, dir, policy)
	if code != 1 {
		t.Fatalf("exit code = %d; want 1\n%s", code, out)
	}
	if !strings.Contains(out, "registered-no-policy") {
		t.Errorf("expected registered-no-policy FAIL; got:\n%s", out)
	}
	if !strings.Contains(out, "deliverable.set_state") {
		t.Errorf("FAIL line should name the kind; got:\n%s", out)
	}
}

// 3. Policy declares a kind not registered in code → FAIL
// [policy-no-handler].
func TestLintGovernedActions_PolicyButNoHandler_Fails(t *testing.T) {
	dir := t.TempDir()
	// Empty registry on purpose (only a non-Register file).
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package fake\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	policy := writeYAMLFixture(t, dir,
		"kinds:\n  phase.advance:\n    default_tier: principal\n")
	out, code := runLint(t, dir, policy)
	if code != 1 {
		t.Fatalf("exit code = %d; want 1\n%s", code, out)
	}
	if !strings.Contains(out, "policy-no-handler") {
		t.Errorf("expected policy-no-handler FAIL; got:\n%s", out)
	}
	if !strings.Contains(out, "phase.advance") {
		t.Errorf("FAIL line should name the kind; got:\n%s", out)
	}
}

// 4. Bidirectional match → clean.
func TestLintGovernedActions_BidirectionalMatch_Clean(t *testing.T) {
	dir := t.TempDir()
	writeRegistryFixture(t, dir, "deliverable.set_state", "task.set_status")
	policy := writeYAMLFixture(t, dir, `
kinds:
  deliverable.set_state:
    default_tier: principal
    commits: true
    override_allowed: true
  task.set_status:
    default_tier: project-steward
    commits: false
    override_allowed: true
`)
	out, code := runLint(t, dir, policy)
	if code != 0 {
		t.Fatalf("exit code = %d; want 0\n%s", code, out)
	}
	if !strings.Contains(out, "lint-governed-actions: clean") {
		t.Errorf("expected clean summary; got:\n%s", out)
	}
}

// 5. Bad kind shape (uppercase / dash / no dot) → FAIL.
func TestLintGovernedActions_BadKindShape_Fails(t *testing.T) {
	dir := t.TempDir()
	// Each name violates the pattern differently.
	writeRegistryFixture(t, dir, "Deliverable.SetState", "no-dots", "leading.dot.")
	out, code := runLint(t, dir, "", "--no-warn-empty")
	if code != 1 {
		t.Fatalf("exit code = %d; want 1\n%s", code, out)
	}
	for _, want := range []string{"Deliverable.SetState", "no-dots", "leading.dot."} {
		if !strings.Contains(out, want) {
			t.Errorf("expected bad-kind-shape mention of %q; got:\n%s", want, out)
		}
	}
}

// 6. escalate_on_timeout: true with default_tier: principal → WARN
//
//	(non-blocking — exit 0).
func TestLintGovernedActions_EscalateOnTimeoutWithPrincipalDefault_Warns(t *testing.T) {
	dir := t.TempDir()
	writeRegistryFixture(t, dir, "task.set_status")
	policy := writeYAMLFixture(t, dir, `
kinds:
  task.set_status:
    default_tier: principal
    escalate_on_timeout: true
`)
	out, code := runLint(t, dir, policy)
	if code != 0 {
		t.Fatalf("exit code = %d; want 0 (WARN-only)\n%s", code, out)
	}
	if !strings.Contains(out, "escalate-walks-nowhere") {
		t.Errorf("expected escalate-walks-nowhere WARN; got:\n%s", out)
	}
}

// 7. Empty registry but policy lists kinds → all FAIL as
// policy-no-handler (regression: don't silently pass when the policy
// is non-empty just because the registry is empty).
func TestLintGovernedActions_EmptyRegistry_NonEmptyPolicy_Fails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package fake\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	policy := writeYAMLFixture(t, dir,
		"kinds:\n  agent.spawn:\n    default_tier: principal\n  template.install:\n    default_tier: principal\n")
	out, code := runLint(t, dir, policy)
	if code != 1 {
		t.Fatalf("exit code = %d; want 1\n%s", code, out)
	}
	for _, want := range []string{"agent.spawn", "template.install"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected policy-no-handler mention of %q; got:\n%s", want, out)
		}
	}
}

// 8. WARN-on-empty-policy fires when registry is non-empty and no
// policy is found — but only when --no-warn-empty is NOT passed.
func TestLintGovernedActions_NonEmptyRegistry_NoPolicy_Warns(t *testing.T) {
	dir := t.TempDir()
	writeRegistryFixture(t, dir, "task.set_status")
	out, code := runLint(t, dir, "")
	if code != 0 {
		t.Fatalf("exit code = %d; want 0\n%s", code, out)
	}
	if !strings.Contains(out, "WARN [no-policy]") {
		t.Errorf("expected no-policy WARN; got:\n%s", out)
	}
}

// 9. Registry skeleton is queryable in-process: ListProposeKinds
// returns the names in sorted order after RegisterProposeKind.
// Pairs with the script test — these two tests together pin BOTH
// the runtime registry (Go) AND the static-grep view of it (bash).
func TestProposeKindsRegistry_ListSorted(t *testing.T) {
	saved := snapshotProposeKindsForTest()
	t.Cleanup(func() { restoreProposeKindsForTest(saved) })
	resetProposeKindsForTest()
	RegisterProposeKind(ProposeKind{Kind: "deliverable.set_state"})
	RegisterProposeKind(ProposeKind{Kind: "task.set_status"})
	RegisterProposeKind(ProposeKind{Kind: "phase.advance"})

	got := ListProposeKinds()
	want := []string{"deliverable.set_state", "phase.advance", "task.set_status"}
	if len(got) != len(want) {
		t.Fatalf("ListProposeKinds len = %d; want %d (%v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("[%d] = %q; want %q (full: %v)", i, got[i], w, got)
		}
	}

	if _, ok := LookupProposeKind("task.set_status"); !ok {
		t.Error("LookupProposeKind(task.set_status) = false; want true")
	}
	if _, ok := LookupProposeKind("nope"); ok {
		t.Error("LookupProposeKind(nope) = true; want false")
	}
}

// 10. RegisterProposeKind with empty Kind panics — guard against
// accidental empty-string registration eating the map's zero-value
// slot.
func TestProposeKindsRegistry_PanicsOnEmptyKind(t *testing.T) {
	saved := snapshotProposeKindsForTest()
	t.Cleanup(func() { restoreProposeKindsForTest(saved) })
	resetProposeKindsForTest()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("RegisterProposeKind with empty Kind did not panic")
		}
	}()
	RegisterProposeKind(ProposeKind{Kind: ""})
}
