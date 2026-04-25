package agentfamilies

import (
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
