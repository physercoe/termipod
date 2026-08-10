package panestate

import "strings"

// ExplainResult is what `host.pane_explain` answers with, and what the hub's
// supplied-screen mode answers with too (plan P4). One type for both so the
// two modes cannot describe the same evaluation differently — the desktop card
// renders whichever it got without asking which it was.
//
// It is `Explain` plus the context `Explain` cannot know: which agent and pane
// were read, which family mapped to which manifest (D-3), and how big the
// screen was. Those answer the questions a human debugging a rule actually
// asks first — "did it read the pane I meant, and did it use the manifest I
// meant" — before any rule detail matters.
//
// **What it deliberately does NOT carry: the screen.** Region previews inside
// the rules are bounded at maxPreviewChars each; the full capture never leaves
// the host. That is the difference between this and a screenshot, and it is
// why the byte/line counts are here — they let a reader see that the pane was
// 60 rows without shipping 60 rows.
type ExplainResult struct {
	// Mode is "live" (a pane was captured) or "supplied" (the caller handed
	// over the screen text). A reader must be able to tell whether the answer
	// describes a real pane right now or a hypothetical.
	Mode string `json:"mode"`

	AgentID string `json:"agent_id,omitempty"`
	PaneID  string `json:"pane_id,omitempty"`
	HostID  string `json:"host_id,omitempty"`

	// Family is the agent kind that was mapped; ManifestID is what D-3's table
	// mapped it to. Both travel because "wrong manifest" and "no manifest" are
	// the two most common wrong answers, and neither is visible from the rules.
	Family string `json:"family"`

	ScreenBytes int `json:"screen_bytes"`
	ScreenLines int `json:"screen_lines"`
	// OSCTitle is the tmux `#{pane_title}` the `osc_title` region reads. It is
	// short by construction and it is the one input whose absence silently
	// disables a whole class of rules, so it travels verbatim rather than as a
	// count — bounded anyway, against a pathological title.
	OSCTitle string `json:"osc_title,omitempty"`

	Explain Explain `json:"explain"`
}

// NewExplainResult assembles the record around one evaluation.
func NewExplainResult(mode, family string, in Input, ex Explain) ExplainResult {
	return ExplainResult{
		Mode:        mode,
		Family:      family,
		ScreenBytes: len(in.Screen),
		ScreenLines: countLines(in.Screen),
		OSCTitle:    boundedPreview(in.OSCTitle),
		Explain:     ex,
	}
}

// countLines counts screen rows the way a reader counts them: a trailing
// newline does not add an empty last row, and an empty screen is 0 rows rather
// than 1. Used only for the "did it read what I meant" sanity line.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}
