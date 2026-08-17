package hostrunner

import (
	"testing"

	"github.com/termipod/hub/internal/agentfamilies"
)

// TestProfile_ClaudeCode_SubagentUsageIsFlagged pins the R5 stamp on the ONE
// kind #374's guard actually reads. digest_fold's token fold and
// transcriptStats' turn accounting skip `subagent:true` USAGE frames — not
// text, not tool_call — and a sub-agent's assistant frames carry
// `message.usage` like every other assistant frame (corpus frame 25 is a real
// recorded one). Stamping the four content kinds but not usage fixes the
// transcript's attribution while leaving the delegated agent's tokens counted
// inside the main turn — the exact inflation R5 set out to remove, surviving
// its own fix.
//
// A hand-built frame rather than the corpus, and semantic assertions rather
// than the pinned fixture, on purpose: the fixture is REGENERATED on drift
// (`-update-frame-fixture`), so a future edit that drops the stamp would
// simply re-pin the bug. This test states the requirement independently, for
// both translators.
func TestProfile_ClaudeCode_SubagentUsageIsFlagged(t *testing.T) {
	f, ok := agentfamilies.ByName("claude-code")
	if !ok || f.FrameProfile == nil {
		t.Fatal("claude-code frame_profile not embedded")
	}

	frameWith := func(parent any) map[string]any {
		return map[string]any{
			"type":               "assistant",
			"parent_tool_use_id": parent,
			"subagent_type":      map[bool]any{true: "general-purpose", false: nil}[parent != nil],
			"message": map[string]any{
				"id":    "msg_1",
				"model": "claude-opus-4-7",
				"content": []any{
					map[string]any{"type": "text", "text": "done"},
				},
				"usage": map[string]any{
					"input_tokens":  float64(7),
					"output_tokens": float64(3),
				},
			},
		}
	}

	usageOf := func(events []EmittedEvent) map[string]any {
		t.Helper()
		for _, e := range events {
			if e.Kind == "usage" {
				return e.Payload
			}
		}
		t.Fatal("no usage event emitted")
		return nil
	}

	for _, tc := range []struct {
		name   string
		parent any
		want   bool
	}{
		// claude sends an explicit null on main-agent frames, not an absent
		// key — the derivation must read the VALUE (IsPresent), or every
		// main-agent usage row flips to subagent:true and the guard starves
		// the real turn counts instead.
		{"sidechain usage is the sub-agent's", "toolu_parent_1", true},
		{"main-agent usage stays the turn's", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := frameWith(tc.parent)
			for path, events := range map[string][]EmittedEvent{
				"profile": ApplyProfile(frame, f.FrameProfile),
				"legacy":  runLegacyTranslate(t, frame),
			} {
				p := usageOf(events)
				if got := p["subagent"]; got != tc.want {
					t.Errorf("%s: usage subagent = %v; want %v", path, got, tc.want)
				}
				if tc.want {
					if got := p["parent_tool_use_id"]; got != tc.parent {
						t.Errorf("%s: usage parent_tool_use_id = %v; want %v", path, got, tc.parent)
					}
				}
			}
		})
	}
}
