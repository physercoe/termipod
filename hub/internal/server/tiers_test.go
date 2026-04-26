package server

import "testing"

// Anchor the tier vocabulary so a future PR that bumps a tool's
// tier has to also update this test — preventing silent
// reclassification (which §6.5.6 calls out as the failure mode
// to avoid). Adding new tools just adds rows; removing a tool
// must also remove its row here.
func TestTierFor(t *testing.T) {
	cases := []struct {
		tool string
		want string
	}{
		// Reads — never reach the user.
		{"get_feed", TierTrivial},
		{"list_channels", TierTrivial},
		{"search", TierTrivial},
		{"journal_read", TierTrivial},
		{"get_project_doc", TierTrivial},
		{"get_attention", TierTrivial},
		{"get_event", TierTrivial},
		{"get_task", TierTrivial},
		{"get_parent_thread", TierTrivial},
		{"list_agents", TierTrivial},
		{"get_audit", TierTrivial},
		{"permission_prompt", TierTrivial},

		// Routine — within scope; auto-allowed but audited.
		{"journal_append", TierRoutine},
		{"request_approval", TierRoutine},
		{"request_decision", TierRoutine},
		{"attach", TierRoutine},
		{"update_own_task_status", TierRoutine},
		{"pause_self", TierRoutine},

		// Significant — inline approval card.
		{"post_message", TierSignificant},     // team broadcast
		{"post_excerpt", TierSignificant},     // team broadcast
		{"delegate", TierSignificant},         // redirects another agent
		{"templates_propose", TierSignificant},
		{"templates.propose", TierSignificant},
		{"shutdown_self", TierSignificant},

		// claude-code surface — read tools.
		{"Read", TierTrivial},
		{"Glob", TierTrivial},
		{"Grep", TierTrivial},
		{"WebSearch", TierTrivial},
		{"NotebookRead", TierTrivial},
		{"TodoRead", TierTrivial},
		{"BashOutput", TierTrivial},
		{"AskUserQuestion", TierTrivial},
		{"ExitPlanMode", TierTrivial},

		// claude-code surface — write/effect tools.
		{"Edit", TierRoutine},
		{"Write", TierRoutine},
		{"MultiEdit", TierRoutine},
		{"NotebookEdit", TierRoutine},
		{"TodoWrite", TierRoutine},
		{"WebFetch", TierRoutine},
		{"Bash", TierRoutine}, // pattern-aware allowlist lands with W1.A
		{"KillBash", TierRoutine},
		{"SlashCommand", TierRoutine},
		{"Task", TierSignificant}, // spawns sub-agent

		// Unknown name → routine (catch-all per §6.5.6 q4). Never silently
		// trivial (would skip user attention) or strategic (would block).
		{"definitely-not-a-real-tool", TierRoutine},
	}
	for _, tc := range cases {
		got := tierFor(tc.tool)
		if got != tc.want {
			t.Errorf("tierFor(%q) = %q; want %q", tc.tool, got, tc.want)
		}
	}
}

// Catalog assertions: every entry in mcpToolDefs() must have a
// resolved tier embedded in its def map (the field added at the
// top of mcpToolDefs). Catches a future PR that adds a new tool
// without registering it in toolTiers.
func TestEveryCatalogEntryHasTier(t *testing.T) {
	for _, def := range mcpToolDefs() {
		name, _ := def["name"].(string)
		if name == "" {
			t.Errorf("tool def has no name: %v", def)
			continue
		}
		tier, _ := def["tier"].(string)
		if tier == "" {
			t.Errorf("tool %q has no tier", name)
		}
		switch tier {
		case TierTrivial, TierRoutine, TierSignificant, TierStrategic:
		default:
			t.Errorf("tool %q has unknown tier %q", name, tier)
		}
	}
}
