package panestate

import (
	"fmt"
	"strings"
)

// Region names. A rule's `region` narrows the text its matchers see — the
// difference between "the word 'Allow' appears somewhere on screen" and "the
// prompt box currently says Allow".
//
// Only the regions the vendored manifests actually use are implemented.
// Upstream has several more (before_current_prompt_marker, above_prompt_box,
// current_prompt_block_marker, …) that no vendored rule references. They are
// deliberately NOT stubbed: an unimplemented region must fail validation, not
// resolve to empty text. Upstream returns "" for an unknown region, which
// turns a mis-typed or newer-schema region into a rule that silently never
// fires — the exact failure this plan exists to remove. If a re-vendor
// introduces one, ValidateRegion fails loudly and it gets implemented here.
const (
	RegionWholeRecent            = "whole_recent"
	RegionOSCTitle               = "osc_title"
	RegionOSCProgress            = "osc_progress"
	RegionAfterLastPromptMarker  = "after_last_prompt_marker"
	RegionAfterLastHorizontalRul = "after_last_horizontal_rule"
	RegionPromptBoxBody          = "prompt_box_body"

	regionBottomLines         = "bottom_lines"
	regionBottomNonEmptyLines = "bottom_non_empty_lines"
	regionTopNonEmptyLines    = "top_non_empty_lines"
)

// maxRegionLineCount bounds the N in bottom_lines(N) etc.
const maxRegionLineCount = 65535

// Input is what the evaluator classifies: a screen snapshot plus the strings
// the terminal reported out of band.
type Input struct {
	// Screen is the bottom-anchored capture. Plan D-4 makes the geometry a
	// contract: the vendored rules were authored against upstream's last-24-
	// rows snapshot, so P2 must trim to that before calling here.
	Screen string
	// OSCTitle comes from tmux `#{pane_title}`.
	OSCTitle string
	// OSCProgress is ALWAYS EMPTY under tmux — tmux does not surface OSC 9;4
	// progress to a client. Rules referencing it therefore never match, which
	// the schema tolerates by design (plan D-4: documented, not worked
	// around). Three vendored rules reference it and are inert for us.
	OSCProgress string
}

// ValidateRegion reports whether a region spec is one this evaluator
// implements. Called at load, so a bad region is a startup failure.
func ValidateRegion(spec string) error {
	switch strings.TrimSpace(spec) {
	case RegionWholeRecent, RegionOSCTitle, RegionOSCProgress,
		RegionAfterLastPromptMarker, RegionAfterLastHorizontalRul, RegionPromptBoxBody:
		return nil
	}
	trimmed := strings.TrimSpace(spec)
	for _, name := range []string{regionBottomLines, regionBottomNonEmptyLines, regionTopNonEmptyLines} {
		if _, ok := parseRegionCount(trimmed, name); ok {
			return nil
		}
		// Distinguish "wrong count" from "unknown name" so the error is
		// actionable — `bottom_lines(0)` and `bottom_lines(x)` are typos, not
		// unimplemented features.
		if strings.HasPrefix(trimmed, name+"(") {
			return fmt.Errorf("region %q has an invalid line count (want a positive integer <= %d)",
				spec, maxRegionLineCount)
		}
	}
	return fmt.Errorf("region %q is not implemented by this evaluator "+
		"(refusing rather than resolving it to empty text, which would make the rule never fire)", spec)
}

// Resolve returns the text a rule's matchers should see.
//
// Unknown regions cannot reach here — ValidateRegion rejects them at load —
// so the default arm is unreachable in practice and returns empty for safety.
func (in Input) Resolve(spec string) string {
	trimmed := strings.TrimSpace(spec)
	switch trimmed {
	case RegionOSCTitle:
		return in.OSCTitle
	case RegionOSCProgress:
		return in.OSCProgress
	case RegionWholeRecent:
		return in.Screen
	case RegionAfterLastPromptMarker:
		return afterLastPromptMarker(in.Screen)
	case RegionAfterLastHorizontalRul:
		return afterLastHorizontalRule(in.Screen)
	case RegionPromptBoxBody:
		return promptBoxBody(in.Screen)
	}
	if n, ok := parseRegionCount(trimmed, regionBottomNonEmptyLines); ok {
		return bottomNonEmptyLines(in.Screen, n)
	}
	if n, ok := parseRegionCount(trimmed, regionBottomLines); ok {
		return bottomLines(in.Screen, n)
	}
	if n, ok := parseRegionCount(trimmed, regionTopNonEmptyLines); ok {
		return topNonEmptyLines(in.Screen, n)
	}
	return ""
}

// splitLines returns each line along with its start offset in content.
//
// Offsets are computed from the real separators rather than assumed to be one
// byte per line (upstream sums `line.len() + 1`, which mis-slices CRLF
// content). tmux `capture-pane -p -J` emits LF, so the two agree on every
// input we actually see; this is simply the version that stays correct if
// that ever changes.
func splitLines(content string) (lines []string, offsets []int) {
	if content == "" {
		return nil, nil
	}
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			line := content[start:i]
			line = strings.TrimSuffix(line, "\r")
			lines = append(lines, line)
			offsets = append(offsets, start)
			start = i + 1
		}
	}
	if start <= len(content)-1 || (start == len(content) && len(lines) == 0) {
		lines = append(lines, content[start:])
		offsets = append(offsets, start)
	}
	return lines, offsets
}

func bottomLines(content string, n int) string {
	lines, offsets := splitLines(content)
	if len(lines) == 0 {
		return ""
	}
	start := len(lines) - n
	if start < 0 {
		start = 0
	}
	return content[offsets[start]:]
}

func bottomNonEmptyLines(content string, n int) string {
	lines, offsets := splitLines(content)
	found := 0
	startIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		found++
		startIdx = i
		if found == n {
			break
		}
	}
	if startIdx < 0 {
		return ""
	}
	return content[offsets[startIdx]:]
}

func topNonEmptyLines(content string, n int) string {
	lines, offsets := splitLines(content)
	found := 0
	endIdx := -1
	for i := range lines {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		found++
		endIdx = i
		if found == n {
			break
		}
	}
	if endIdx < 0 {
		return ""
	}
	// Through the end of line endIdx, i.e. the start of the next line.
	if endIdx+1 < len(offsets) {
		return content[:offsets[endIdx+1]]
	}
	return content
}

// codexPromptLine is upstream's marker for codex's input chevron.
func codexPromptLine(line string) bool {
	return line == "›" || strings.HasPrefix(line, "› ")
}

func afterLastPromptMarker(content string) string {
	lines, offsets := splitLines(content)
	for i := len(lines) - 1; i >= 0; i-- {
		if codexPromptLine(lines[i]) {
			if i+1 < len(offsets) {
				return content[offsets[i+1]:]
			}
			return ""
		}
	}
	// No marker at all → the whole screen, not empty. A screen that has not
	// drawn its prompt yet must still be classifiable.
	return content
}

// isHorizontalRule matches a box-drawing rule line. A single U+2500 followed
// by other text is not a rule (that is a bullet or a tree glyph); three or
// more are, regardless of what follows.
func isHorizontalRule(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	runes := []rune(trimmed)
	ruleChars := 0
	for _, r := range runes {
		if r != '─' {
			break
		}
		ruleChars++
	}
	if ruleChars == 0 {
		return false
	}
	suffix := strings.TrimLeft(string(runes[ruleChars:]), " \t")
	return suffix == "" || ruleChars >= 3
}

func afterLastHorizontalRule(content string) string {
	lines, offsets := splitLines(content)
	lastEnd := 0
	for i, line := range lines {
		if isHorizontalRule(line) {
			if i+1 < len(offsets) {
				lastEnd = offsets[i+1]
			} else {
				lastEnd = len(content)
			}
		}
	}
	return content[lastEnd:]
}

// promptBoxBody returns the text between the last two horizontal rules — the
// inside of a bottom-anchored input box. Empty when the screen has fewer than
// two rules.
func promptBoxBody(content string) string {
	lines, offsets := splitLines(content)
	// The top border is the SECOND rule counting from the bottom.
	borders := 0
	top := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if isHorizontalRule(lines[i]) {
			borders++
			if borders == 2 {
				top = i
				break
			}
		}
	}
	if top < 0 || top+1 >= len(lines) {
		return ""
	}
	start := offsets[top+1]
	end := len(content)
	for i := top + 1; i < len(lines); i++ {
		if isHorizontalRule(lines[i]) {
			end = offsets[i]
			break
		}
	}
	if start > end {
		return ""
	}
	return content[start:end]
}
