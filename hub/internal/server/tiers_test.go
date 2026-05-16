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

// Every entry in mcpToolDefs() must have an explicit row in toolTiers.
// The previous version of this test read def["tier"] (which tierFor
// always populates via its TierRoutine fallback), so a new tool that
// no one classified shipped silent. We now check toolTiers directly
// so the test fails when a new entry slips past the table.
func TestEveryCatalogEntryHasTier(t *testing.T) {
	for _, def := range mcpToolDefs() {
		name, _ := def["name"].(string)
		if name == "" {
			t.Errorf("tool def has no name: %v", def)
			continue
		}
		// Read the table directly — bypass the TierRoutine fallback.
		tier, present := toolTiers[name]
		if !present {
			t.Errorf("tool %q registered in tools/list but missing from "+
				"toolTiers (add an explicit row to tiers.go)", name)
			continue
		}
		switch tier {
		case TierTrivial, TierRoutine, TierSignificant, TierStrategic:
		default:
			t.Errorf("tool %q has unknown tier %q", name, tier)
		}
	}
}

// Every case in dispatchTool's switch must appear in mcpToolDefs(),
// otherwise the agent's MCP client sees "no such tool". The bug that
// caught us was request_project_steward — dispatcher case + handler
// both shipped (ADR-025 W4) but the tools/list entry was missed, so
// claude-code reported "No such tool available". The list below
// mirrors dispatchTool in mcp.go; aliases are documented separately.
func TestEveryDispatcherCaseAdvertised(t *testing.T) {
	// Canonical names: every entry must appear in mcpToolDefs().
	dispatcherCases := []string{
		// mcpToolDefsBase
		"post_message",
		"get_feed",
		"list_channels",
		"search",
		"journal_append",
		"journal_read",
		"get_project_doc",
		"get_attention",
		"post_excerpt",
		// mcpToolDefsExtra
		"delegate",
		"request_approval",
		"request_select",
		"request_help",
		"request_project_steward",
		"attach",
		"get_event",
		"get_task",
		"get_parent_thread",
		"list_agents",
		"update_own_task_status",
		"templates_propose",
		"pause_self",
		"shutdown_self",
		"get_audit",
		"permission_prompt",
		// orchestrationToolDefs
		"agents.fanout",
		"agents.gather",
		"reports.post",
	}
	// Back-compat aliases the dispatcher accepts but the registry does
	// not advertise (advertising them would surface duplicates in the
	// agent's tool picker). Keep tightly scoped — every entry needs a
	// rename trail in code comments.
	knownAliases := map[string]struct{}{
		"request_decision":  {}, // legacy alias for request_select (v1.0.295 rename)
		"templates.propose": {}, // legacy alias for templates_propose (dot/underscore)
	}

	advertised := map[string]struct{}{}
	for _, def := range mcpToolDefs() {
		if name, _ := def["name"].(string); name != "" {
			advertised[name] = struct{}{}
		}
	}
	for _, name := range dispatcherCases {
		if _, ok := advertised[name]; !ok {
			t.Errorf("dispatcher routes %q but tools/list does not advertise it "+
				"(add an entry in mcp.go / mcp_more.go / mcp_orchestrate.go)", name)
		}
		if _, isAlias := knownAliases[name]; isAlias {
			t.Errorf("alias %q listed as canonical — move it to knownAliases", name)
		}
	}
}
