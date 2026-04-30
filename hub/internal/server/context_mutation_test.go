package server

import "testing"

// TestDetectContextMutation_ClaudeCommands pins the claude-code
// command-set: /compact, /clear, /rewind map to the three typed
// kinds mobile renderers will dispatch off. Test data also asserts
// the verb (the human-readable label the renderer puts on the chip)
// stays stable; renaming verbs would silently break any UX work
// already keyed on the existing strings.
func TestDetectContextMutation_ClaudeCommands(t *testing.T) {
	cases := []struct {
		body     string
		wantKind string
		wantVerb string
	}{
		{"/compact", "context.compacted", "compact"},
		{"/clear", "context.cleared", "clear"},
		{"/rewind", "context.rewound", "rewind"},
		// Trailing whitespace / arguments don't suppress the match.
		{"/compact   ", "context.compacted", "compact"},
		{"/compact please summarize aggressively", "context.compacted", "compact"},
		// Leading whitespace doesn't either — trim handles it.
		{"  /clear", "context.cleared", "clear"},
	}
	for _, tc := range cases {
		got, ok := detectContextMutation("claude-code", tc.body)
		if !ok {
			t.Errorf("body=%q: expected match, got none", tc.body)
			continue
		}
		if got.Kind != tc.wantKind {
			t.Errorf("body=%q: kind=%q want %q", tc.body, got.Kind, tc.wantKind)
		}
		if got.Verb != tc.wantVerb {
			t.Errorf("body=%q: verb=%q want %q", tc.body, got.Verb, tc.wantVerb)
		}
	}
}

// TestDetectContextMutation_NonLeadingSlash — the user discussing a
// slash command mid-sentence ("the /compact didn't help") must not
// trigger a marker. Slash commands fire only when they're the first
// token, matching the engines' own REPL behaviour.
func TestDetectContextMutation_NonLeadingSlash(t *testing.T) {
	bodies := []string{
		"the /compact command didn't help",
		"hello world",
		"",
		"   ",
		"explain why /clear fails",
		// Looks like a slash but isn't one of ours.
		"/help",
		"/foo",
		"/compactnot", // longer token; exact-match required
	}
	for _, body := range bodies {
		if _, ok := detectContextMutation("claude-code", body); ok {
			t.Errorf("body=%q: expected no match", body)
		}
	}
}

// TestDetectContextMutation_GeminiCommands pins the gemini-cli set:
// `/compress` is gemini's name for compact (different verb, same
// effect), and `/clear` is shared. Gemini doesn't ship `/rewind`,
// so it must NOT match.
func TestDetectContextMutation_GeminiCommands(t *testing.T) {
	got, ok := detectContextMutation("gemini-cli", "/compress")
	if !ok {
		t.Fatal("/compress on gemini-cli: no match")
	}
	if got.Kind != "context.compacted" {
		t.Errorf("kind=%q want context.compacted", got.Kind)
	}
	if got.Verb != "compress" {
		t.Errorf("verb=%q want compress", got.Verb)
	}

	got, ok = detectContextMutation("gemini-cli", "/clear")
	if !ok {
		t.Fatal("/clear on gemini-cli: no match")
	}
	if got.Kind != "context.cleared" {
		t.Errorf("kind=%q want context.cleared", got.Kind)
	}

	if _, ok := detectContextMutation("gemini-cli", "/rewind"); ok {
		t.Error("/rewind on gemini-cli should not match (no such command)")
	}
}

// TestDetectContextMutation_UnknownEngine — for engines whose
// command set we haven't audited (codex today), no match. The
// alternative — emitting a speculative marker on every slash — is
// noisy and incorrect: codex's slash vocabulary is engine-defined
// and we shouldn't claim observability we don't have.
func TestDetectContextMutation_UnknownEngine(t *testing.T) {
	if _, ok := detectContextMutation("codex", "/compact"); ok {
		t.Error("codex agent_kind should not match anything (TBD per ADR-014 OQ-4)")
	}
	if _, ok := detectContextMutation("", "/compact"); ok {
		t.Error("empty agent_kind should not match")
	}
}

// TestDetectContextMutation_CaseSensitive — claude's REPL is
// case-sensitive; we follow. `/COMPACT` is not a valid claude command
// and shouldn't fire the marker.
func TestDetectContextMutation_CaseSensitive(t *testing.T) {
	if _, ok := detectContextMutation("claude-code", "/COMPACT"); ok {
		t.Error("/COMPACT (uppercase) should not match — claude is case-sensitive")
	}
	if _, ok := detectContextMutation("claude-code", "/Compact"); ok {
		t.Error("/Compact (titlecase) should not match")
	}
}
