package agentfamilies

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAll_ParsesEmbeddedYAML asserts the embedded YAML is well-formed
// and contains the three families the blueprint pins down today
// (claude-code / gemini-cli / codex — dominant-vendor coverage; aider
// was retired 2026-04-29 per project decision to track only major
// vendor products). If a future PR retires or renames a family this
// test fails loudly — that failure is the prompt to update callers
// (resolver/spawn_mode/probe).
func TestAll_ParsesEmbeddedYAML(t *testing.T) {
	got, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) < 3 {
		t.Fatalf("expected ≥3 families, got %d", len(got))
	}
	want := map[string]bool{
		"claude-code": false, "gemini-cli": false,
		"codex": false,
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
	if len(views) < 3 {
		t.Fatalf("expected ≥3 embedded families, got %d", len(views))
	}
	for _, v := range views {
		if v.Source != SourceEmbedded {
			t.Errorf("family %q should be embedded; got %q", v.Family.Family, v.Source)
		}
	}
}

// TestFrameProfile_YAMLRoundTrip locks the FrameProfile schema by
// asserting a representative profile encodes + decodes back to an
// equivalent struct. ADR-010's contract is "the YAML *is* the
// schema" — if a future struct rename breaks this round-trip, the
// failure surfaces here before it silently breaks every operator's
// overlay.
func TestFrameProfile_YAMLRoundTrip(t *testing.T) {
	dir := t.TempDir()
	yamlBody := `family: claude-code
bin: claude
version_flag: --version
supports: [M2]
frame_profile:
  profile_version: 1
  rules:
    - match: { type: rate_limit_event }
      emit:
        kind: rate_limit
        producer: agent
        payload:
          window: "$.rate_limit_info.rateLimitType || $.rateLimitType"
          status: "$.rate_limit_info.status || $.status"
    - match: { type: assistant }
      for_each: $.message.content
      sub_rules:
        - match: { type: text }
          emit:
            kind: text
            producer: agent
            payload:
              text: $.text
              message_id: $$.message.id
        - match: { type: tool_use }
          emit:
            kind: tool_call
            producer: agent
            payload:
              id: $.id
              name: $.name
              input: $.input
`
	must(t, os.WriteFile(filepath.Join(dir, "claude-code.yaml"),
		[]byte(yamlBody), 0o644))

	r := New(dir)
	got, ok := r.ByName("claude-code")
	if !ok {
		t.Fatal("claude-code missing after overlay write")
	}
	fp := got.FrameProfile
	if fp == nil {
		t.Fatal("frame_profile not parsed")
	}
	if fp.ProfileVersion != 1 {
		t.Errorf("profile_version = %d; want 1", fp.ProfileVersion)
	}
	if len(fp.Rules) != 2 {
		t.Fatalf("rules count = %d; want 2", len(fp.Rules))
	}

	// Rule 0: rate_limit_event → rate_limit. Match key + emit shape.
	r0 := fp.Rules[0]
	if r0.Match["type"] != "rate_limit_event" {
		t.Errorf("rule[0].match[type] = %v; want rate_limit_event", r0.Match["type"])
	}
	if r0.Emit.Kind != "rate_limit" {
		t.Errorf("rule[0].emit.kind = %q; want rate_limit", r0.Emit.Kind)
	}
	if r0.Emit.Payload["window"] !=
		"$.rate_limit_info.rateLimitType || $.rateLimitType" {
		t.Errorf("rule[0].emit.payload[window] = %q; expression should round-trip verbatim",
			r0.Emit.Payload["window"])
	}

	// Rule 1: assistant.content[] dispatch via sub_rules.
	r1 := fp.Rules[1]
	if r1.ForEach != "$.message.content" {
		t.Errorf("rule[1].for_each = %q; want $.message.content", r1.ForEach)
	}
	if len(r1.SubRules) != 2 {
		t.Fatalf("rule[1].sub_rules count = %d; want 2", len(r1.SubRules))
	}
	if r1.SubRules[0].Emit.Kind != "text" {
		t.Errorf("sub_rule[0].emit.kind = %q; want text", r1.SubRules[0].Emit.Kind)
	}
	if r1.SubRules[1].Emit.Kind != "tool_call" {
		t.Errorf("sub_rule[1].emit.kind = %q; want tool_call", r1.SubRules[1].Emit.Kind)
	}
	if r1.SubRules[0].Emit.Payload["message_id"] != "$$.message.id" {
		t.Errorf("outer-scope expression $$.message.id should round-trip; got %q",
			r1.SubRules[0].Emit.Payload["message_id"])
	}
}

// TestFrameProfile_EmbeddedClaudeCode — the canonical claude-code
// profile is the load-bearing example. Verify it parses, declares a
// schema version, and covers the surfaces driver_stdio.go::translate()
// covers today (system.init / rate_limit / assistant / user /
// result / error). Per-rule semantics are exercised by
// profile_translate_test.go and profile_eval_test.go; this test
// is the structural contract.
func TestFrameProfile_EmbeddedClaudeCode(t *testing.T) {
	f, ok := ByName("claude-code")
	if !ok {
		t.Fatal("claude-code missing from registry")
	}
	fp := f.FrameProfile
	if fp == nil {
		t.Fatal("claude-code embedded profile not loaded")
	}
	if fp.ProfileVersion != 1 {
		t.Errorf("profile_version = %d; want 1", fp.ProfileVersion)
	}
	if fp.Description == "" {
		t.Error("description should be set on canonical profiles (agent-readability)")
	}

	// Each canonical kind must be reachable from at least one rule
	// or sub-rule. Drives a "did we forget to translate X?" check.
	wantKinds := map[string]bool{
		"session.init": false,
		"rate_limit":   false,
		"system":       false,
		"text":         false,
		"tool_call":    false,
		"usage":        false,
		"tool_result":  false,
		"turn.result":  false,
		"completion":   false,
		"error":        false,
	}
	var walk func(rules []Rule)
	walk = func(rules []Rule) {
		for _, r := range rules {
			if _, ok := wantKinds[r.Emit.Kind]; ok {
				wantKinds[r.Emit.Kind] = true
			}
			walk(r.SubRules)
		}
	}
	walk(fp.Rules)
	for kind, seen := range wantKinds {
		if !seen {
			t.Errorf("canonical kind %q not produced by any rule", kind)
		}
	}
}

// TestFrameProfile_OtherEnginesStillNil — codex / gemini-cli don't
// ship profiles in v1 (Phase 3 of the migration plan); they fall
// through to the legacy translator. Lock that contract so a future
// PR doesn't accidentally enable a half-finished profile.
func TestFrameProfile_OtherEnginesStillNil(t *testing.T) {
	for _, name := range []string{"codex", "gemini-cli"} {
		f, ok := ByName(name)
		if !ok {
			t.Fatalf("%s missing from registry", name)
		}
		if f.FrameProfile != nil {
			t.Errorf("%s ships embedded with FrameProfile=%+v; should still be nil in v1",
				name, f.FrameProfile)
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
