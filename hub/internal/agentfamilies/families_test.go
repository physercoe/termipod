package agentfamilies

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAll_ParsesEmbeddedYAML asserts the embedded YAML is well-formed
// and contains the four families the blueprint pins down today. If a
// future PR retires or renames a family this test fails loudly — that
// failure is the prompt to update callers (resolver/spawn_mode/probe).
func TestAll_ParsesEmbeddedYAML(t *testing.T) {
	got, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) < 4 {
		t.Fatalf("expected ≥4 families, got %d", len(got))
	}
	want := map[string]bool{
		"claude-code": false, "gemini-cli": false,
		"codex": false, "aider": false,
	}
	for _, f := range got {
		if _, ok := want[f.Family]; ok {
			want[f.Family] = true
		}
		if f.Bin == "" {
			t.Errorf("family %q has empty bin", f.Family)
		}
		if len(f.Supports) == 0 {
			t.Errorf("family %q has empty supports", f.Family)
		}
	}
	for k, v := range want {
		if !v {
			t.Errorf("expected family %q in registry", k)
		}
	}
}

// TestByName_KnownAndUnknown covers both halves of the lookup contract:
// known families return data; unknown names return ok=false rather
// than a zero-value Family that would silently work.
func TestByName_KnownAndUnknown(t *testing.T) {
	f, ok := ByName("claude-code")
	if !ok || f.Family != "claude-code" {
		t.Fatalf("claude-code lookup failed: %+v ok=%v", f, ok)
	}
	if len(f.Incompatibilities) == 0 {
		t.Errorf("claude-code should declare at least the M1+subscription incompat")
	}
	if _, ok := ByName("not-a-family"); ok {
		t.Error("unknown family returned ok=true")
	}
}

// TestRegistry_OverlayAddsCustom verifies a brand-new family file in the
// overlay directory shows up alongside the embedded set, tagged custom.
func TestRegistry_OverlayAddsCustom(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "kimi.yaml"), []byte(
		"family: kimi\nbin: kimi\nversion_flag: --version\nsupports: [M2, M4]\n",
	), 0o644))

	r := New(dir)
	views, err := r.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	got, ok := findView(views, "kimi")
	if !ok {
		t.Fatal("kimi not in merged list")
	}
	if got.Source != SourceCustom {
		t.Errorf("kimi source = %q; want custom", got.Source)
	}
	if got.Family.Bin != "kimi" {
		t.Errorf("kimi bin = %q; want kimi", got.Family.Bin)
	}
}

// TestRegistry_OverlayReplacesEmbedded verifies an overlay file whose
// family name matches an embedded entry replaces it wholesale and the
// source flips to override.
func TestRegistry_OverlayReplacesEmbedded(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "claude-code.yaml"), []byte(
		"family: claude-code\nbin: claude-custom\nversion_flag: --version\nsupports: [M4]\n",
	), 0o644))

	r := New(dir)
	views, err := r.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	got, ok := findView(views, "claude-code")
	if !ok {
		t.Fatal("claude-code missing")
	}
	if got.Source != SourceOverride {
		t.Errorf("source = %q; want override", got.Source)
	}
	if got.Family.Bin != "claude-custom" {
		t.Errorf("bin = %q; want claude-custom (overlay should replace)", got.Family.Bin)
	}
	if len(got.Family.Incompatibilities) != 0 {
		t.Errorf("override should have wiped embedded incompatibilities, got %d",
			len(got.Family.Incompatibilities))
	}
}

// TestRegistry_MalformedOverlayIsSkipped ensures a typo'd hand-edit on
// the hub host can't take down the registry — the bad file is logged
// and ignored, the rest of the list still loads.
func TestRegistry_MalformedOverlayIsSkipped(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte(
		"family: broken\n  bad indent\n  not yaml [\n",
	), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "kimi.yaml"), []byte(
		"family: kimi\nbin: kimi\nsupports: [M2]\n",
	), 0o644))

	r := New(dir)
	views, err := r.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if _, ok := findView(views, "broken"); ok {
		t.Error("broken family should have been skipped")
	}
	if _, ok := findView(views, "kimi"); !ok {
		t.Error("kimi should have loaded despite sibling being malformed")
	}
}

// TestRegistry_InvalidateRescans is the load-bearing test for the API
// contract: after the overlay file changes on disk, Invalidate must
// cause the next All() call to see the new content.
func TestRegistry_InvalidateRescans(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	if _, ok := r.ByName("kimi"); ok {
		t.Fatal("kimi should not exist before write")
	}

	must(t, os.WriteFile(filepath.Join(dir, "kimi.yaml"), []byte(
		"family: kimi\nbin: kimi\nsupports: [M2]\n",
	), 0o644))

	if _, ok := r.ByName("kimi"); ok {
		t.Fatal("kimi visible without Invalidate — cache not honoring write barrier")
	}

	r.Invalidate()

	if _, ok := r.ByName("kimi"); !ok {
		t.Fatal("kimi missing after Invalidate")
	}
}

// TestRegistry_OverlayMissingDir treats a missing overlay directory the
// same as embedded-only mode. First boot before any PUT lands here.
func TestRegistry_OverlayMissingDir(t *testing.T) {
	r := New(filepath.Join(t.TempDir(), "does-not-exist"))
	views, err := r.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(views) < 4 {
		t.Fatalf("expected ≥4 embedded families, got %d", len(views))
	}
	for _, v := range views {
		if v.Source != SourceEmbedded {
			t.Errorf("family %q should be embedded; got %q", v.Family.Family, v.Source)
		}
	}
}

func findView(views []View, name string) (View, bool) {
	for _, v := range views {
		if v.Family.Family == name {
			return v, true
		}
	}
	return View{}, false
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
