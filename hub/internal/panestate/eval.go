package panestate

import (
	"regexp"
	"strings"
)

// regexpMatcher is the subset of *regexp.Regexp this file needs; named so the
// line-matching helper reads without a pointer-to-package-type in its
// signature.
type regexpMatcher = regexp.Regexp

// Explain is the full evaluation record: the answer plus the evidence for it.
// Every rule is evaluated on every pass — not short-circuited at the first
// match — because P4's `host.pane_explain` needs to show why the rules that
// did NOT match did not match. Manifests are ~5-14 rules, so the cost is
// noise next to the `capture-pane` subprocess that produced the screen.
// The json tags make this the WIRE shape of `host.pane_explain` (P4) as well
// as the evaluator's internal record — one type rather than two that drift.
// The cost of that choice is that a field added here for evaluation would ship
// to a client by default, so `TestExplainWireKeysAreDeliberate` pins the exact
// key set: adding a field is fine, adding it silently is not.
type Explain struct {
	ManifestID      string `json:"manifest_id"`
	ManifestVersion string `json:"manifest_version"`
	Source          string `json:"source"` // "vendor" | "overlay"

	State       State        `json:"state"`
	MatchedRule *MatchedRule `json:"matched_rule,omitempty"`
	// FallbackReason is set when no rule matched. A known agent whose screen
	// matches nothing is idle, and this records that it was a fallback rather
	// than a positive classification.
	FallbackReason string `json:"fallback_reason,omitempty"`

	VisibleIdle     bool `json:"visible_idle,omitempty"`
	VisibleBlocker  bool `json:"visible_blocker,omitempty"`
	VisibleWorking  bool `json:"visible_working,omitempty"`
	SkipStateUpdate bool `json:"skip_state_update,omitempty"`
	// SkippedUpdateReason names the rule that froze the state, so a surface
	// can say "held by transcript-viewer rule" instead of showing a stale
	// state with no explanation.
	SkippedUpdateReason string `json:"skipped_update_reason,omitempty"`

	EvaluatedRules []EvaluatedRule `json:"rules"`
}

// MatchedRule is the winning rule.
type MatchedRule struct {
	ID       string `json:"id"`
	Priority int    `json:"priority"`
	Region   string `json:"region"`
	State    State  `json:"state"`
}

// EvaluatedRule is one rule's outcome plus bounded evidence.
type EvaluatedRule struct {
	ID       string   `json:"id"`
	Priority int      `json:"priority"`
	Region   string   `json:"region"`
	State    State    `json:"state"`
	Matched  bool     `json:"matched"`
	Evidence Evidence `json:"evidence"`
}

// Evidence is what the rule looked for and what it looked at. The region
// preview is bounded because this travels to a UI and a pane is 24x200.
type Evidence struct {
	Contains      []string `json:"contains,omitempty"`
	Regex         []string `json:"regex,omitempty"`
	LineRegex     []string `json:"line_regex,omitempty"`
	AllCount      int      `json:"all_count,omitempty"`
	AnyCount      int      `json:"any_count,omitempty"`
	NotCount      int      `json:"not_count,omitempty"`
	RegionBytes   int      `json:"region_bytes"`
	RegionPreview string   `json:"region_preview"`
}

const maxPreviewChars = 240

func boundedPreview(s string) string {
	runes := []rune(s)
	if len(runes) <= maxPreviewChars {
		return s
	}
	return string(runes[:maxPreviewChars]) + "..."
}

// Evaluate classifies one screen against one compiled manifest.
//
// Winner is the highest `priority` among matching rules; ties go to the
// EARLIEST rule in file order. That tie-break is upstream's and it is load
// bearing — reordering a manifest's rules can change the answer, which is why
// the vendored files are byte-exact and a re-vendor runs the fixture corpus.
func (cm compiledManifest) Evaluate(in Input) Explain {
	ex := Explain{
		ManifestID:      cm.manifest.ID,
		ManifestVersion: cm.manifest.Version,
		Source:          cm.manifest.Source,
		EvaluatedRules:  make([]EvaluatedRule, 0, len(cm.manifest.Rules)),
	}
	winner := -1
	for i := range cm.manifest.Rules {
		r := &cm.manifest.Rules[i]
		region := in.Resolve(r.Region)
		matched := gateMatches(&cm.gates[i], region, strings.ToLower(region))
		ex.EvaluatedRules = append(ex.EvaluatedRules, EvaluatedRule{
			ID:       r.ID,
			Priority: r.Priority,
			Region:   r.Region,
			State:    r.State,
			Matched:  matched,
			Evidence: Evidence{
				Contains:      r.Contains,
				Regex:         r.Regex,
				LineRegex:     r.LineRegex,
				AllCount:      len(r.All),
				AnyCount:      len(r.Any),
				NotCount:      len(r.Not),
				RegionBytes:   len(region),
				RegionPreview: boundedPreview(region),
			},
		})
		if !matched {
			continue
		}
		// Strictly greater: an equal priority leaves the earlier rule in
		// place, which is the file-order tie-break.
		if winner < 0 || r.Priority > cm.manifest.Rules[winner].Priority {
			winner = i
		}
	}

	if winner < 0 {
		ex.State = StateIdle
		ex.FallbackReason = FallbackKnownAgentIdle
		return ex
	}

	r := &cm.manifest.Rules[winner]
	ex.State = r.State
	ex.MatchedRule = &MatchedRule{
		ID: r.ID, Priority: r.Priority, Region: r.Region, State: r.State,
	}
	// The visible_* hints are only meaningful when they agree with the state
	// the rule assigned — upstream ANDs them the same way, so a rule marked
	// visible_blocker that resolved to `working` does not raise attention.
	ex.VisibleIdle = r.VisibleIdle && r.State == StateIdle
	ex.VisibleBlocker = r.VisibleBlocker && r.State == StateBlocked
	ex.VisibleWorking = r.VisibleWorking && r.State == StateWorking
	ex.SkipStateUpdate = r.SkipStateUpdate
	if r.SkipStateUpdate {
		ex.SkippedUpdateReason = "matched_rule:" + r.ID
	}
	return ex
}

// gateMatches is the boolean evaluation.
//
//   - contains:   ALL must be present, case-insensitively
//   - regex:      ALL must match the region, case-sensitively
//   - line_regex: ALL must match at least one LINE of the region
//   - all:        ALL nested gates must match
//   - any:        at least one nested gate must match — but an EMPTY `any` is
//     vacuously true, not false. That asymmetry is deliberate:
//     a rule that declares only `contains` should not be killed
//     by its absent `any`.
//   - not:        NO nested gate may match
func gateMatches(g *compiledGate, text, lowerText string) bool {
	for _, needle := range g.contains {
		if !strings.Contains(lowerText, needle) {
			return false
		}
	}
	for _, re := range g.regex {
		if !re.MatchString(text) {
			return false
		}
	}
	for _, re := range g.lineRegex {
		if !matchesAnyLine(re, text) {
			return false
		}
	}
	for i := range g.all {
		if !gateMatches(&g.all[i], text, lowerText) {
			return false
		}
	}
	if len(g.any) > 0 {
		hit := false
		for i := range g.any {
			if gateMatches(&g.any[i], text, lowerText) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	for i := range g.not {
		if gateMatches(&g.not[i], text, lowerText) {
			return false
		}
	}
	return true
}

func matchesAnyLine(re *regexpMatcher, text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if re.MatchString(strings.TrimSuffix(line, "\r")) {
			return true
		}
	}
	return false
}
