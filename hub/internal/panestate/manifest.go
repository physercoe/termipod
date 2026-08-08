// Package panestate classifies an agent's screen into working / blocked /
// idle from declarative per-engine manifests, so host-runner can say "this
// agent needs you" for engines that have no structured M4 adapter.
//
// Today the only signal for those engines is IdleDetector: one global prompt
// regex plus a 90-second content-hash stall. It misses every modern TUI
// approval panel — codex parked on "Allow command?" raises nothing.
//
// The rules are data (manifests/vendor + manifests/overlay), the same shape
// as ADR-010 frame profiles: an interpreter plus a fixture corpus, so a new
// engine is a TOML file rather than a Go adapter.
//
// This package is a PURE LIBRARY. It reads no panes and posts no events;
// wiring is P2/P3 (plan D-8, teeth before wiring).
//
// Plan: docs/plans/pane-state-manifests.md P1.
package panestate

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// State is the classification a rule assigns.
type State string

const (
	StateIdle    State = "idle"
	StateWorking State = "working"
	StateBlocked State = "blocked"
	StateUnknown State = "unknown"
)

// FallbackKnownAgentIdle is the reason recorded when a manifest matched no
// rule at all. Upstream's semantics, kept verbatim: a KNOWN agent whose
// screen matches nothing is idle, not unknown — "strict blocked" means we
// only ever claim blocked on positive evidence, so the residual risk is a
// MISSED blocked state (today's behaviour) rather than a false attention.
const FallbackKnownAgentIdle = "default_known_agent_idle_fallback"

// Validation caps, ported from upstream. They bound the cost of evaluating a
// manifest that arrives from outside the binary (P5 will fetch these over the
// wire), and they are checked at load so a bad manifest fails loudly at
// startup rather than per-tick.
const (
	MaxRulesPerManifest = 128
	MaxGateDepth        = 8
	MaxTotalGates       = 512
	MaxMatchersPerGate  = 32
	MaxTotalMatchers    = 1024
	MaxMatcherChars     = 512
)

// Manifest is one engine's rule set.
type Manifest struct {
	ID               string   `toml:"id"`
	Version          string   `toml:"version"`
	MinEngineVersion int      `toml:"min_engine_version"`
	UpdatedAt        string   `toml:"updated_at"`
	Aliases          []string `toml:"aliases"`
	Rules            []Rule   `toml:"rules"`

	// Source is where this manifest was loaded from ("vendor" / "overlay"),
	// filled by the loader. Reported by explain so a surface can say which
	// copy answered.
	Source string `toml:"-"`
}

// Rule is one (gate -> state) pair. The matcher fields are inlined at the
// rule level as well as being available in nested gates, which is how the
// vendored manifests are written.
type Rule struct {
	ID       string `toml:"id"`
	State    State  `toml:"state"`
	Priority int    `toml:"priority"`
	Region   string `toml:"region"`

	// Presentation hints. VisibleBlocker is the load-bearing one: P3 lets a
	// rule carrying it raise attention even for an agent whose structured
	// adapter authors state, because a live permission dialog on screen is
	// something the hooks never reported (plan D-2).
	VisibleIdle     bool `toml:"visible_idle"`
	VisibleBlocker  bool `toml:"visible_blocker"`
	VisibleWorking  bool `toml:"visible_working"`
	SkipStateUpdate bool `toml:"skip_state_update"`

	Gate
}

// Gate is a boolean combination of matchers. `all` and nested gates recurse;
// `any` is ignored when empty (an empty `any` is not "match nothing").
type Gate struct {
	All       []Gate   `toml:"all"`
	Any       []Gate   `toml:"any"`
	Not       []Gate   `toml:"not"`
	Contains  []string `toml:"contains"`
	Regex     []string `toml:"regex"`
	LineRegex []string `toml:"line_regex"`
}

// compiledGate is a Gate with its regexes compiled and its `contains`
// needles lowercased once, at load.
type compiledGate struct {
	all       []compiledGate
	any       []compiledGate
	not       []compiledGate
	contains  []string // already lowercased
	regex     []*regexp.Regexp
	lineRegex []*regexp.Regexp
	// notes records dialect translations applied to this gate's patterns, so
	// explain can say the pattern that ran is not the pattern in the file.
	notes []TranslationNote
}

// compiledManifest pairs a manifest with its compiled rule gates, in file
// order — evaluation zips the two, and file order is the documented
// tie-break for equal priorities.
type compiledManifest struct {
	manifest Manifest
	gates    []compiledGate
}

// ParseManifest decodes and validates one manifest TOML.
//
// Unknown keys are an ERROR, not a warning. Upstream uses serde's
// deny_unknown_fields for the same reason: a manifest written against a newer
// schema than this evaluator understands must fail loudly, because the
// alternative is a rule that silently never fires — which is precisely the
// failure class this whole plan exists to remove.
func ParseManifest(data []byte, source string) (Manifest, error) {
	var m Manifest
	meta, err := toml.Decode(string(data), &m)
	if err != nil {
		return Manifest{}, fmt.Errorf("panestate: parse: %w", err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		return Manifest{}, fmt.Errorf(
			"panestate: manifest %q carries keys this evaluator does not implement: %s "+
				"(refusing rather than ignoring them — an unread key is a rule that never fires)",
			m.ID, strings.Join(keys, ", "))
	}
	m.Source = source
	if err := validateManifest(&m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func validateManifest(m *Manifest) error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("panestate: manifest has no id")
	}
	if len(m.Rules) > MaxRulesPerManifest {
		return fmt.Errorf("panestate: manifest %q has %d rules, max is %d",
			m.ID, len(m.Rules), MaxRulesPerManifest)
	}
	seen := make(map[string]struct{}, len(m.Rules))
	totals := &budget{}
	for i := range m.Rules {
		r := &m.Rules[i]
		if strings.TrimSpace(r.ID) == "" {
			return fmt.Errorf("panestate: manifest %q rule %d has no id", m.ID, i)
		}
		if _, dup := seen[r.ID]; dup {
			return fmt.Errorf("panestate: manifest %q has duplicate rule id %q", m.ID, r.ID)
		}
		seen[r.ID] = struct{}{}
		switch r.State {
		case StateIdle, StateWorking, StateBlocked, StateUnknown:
		case "":
			// Upstream treats an absent state as `unknown`; normalize at load
			// so evaluation never has to.
			r.State = StateUnknown
		default:
			return fmt.Errorf("panestate: manifest %q rule %q has unknown state %q",
				m.ID, r.ID, r.State)
		}
		if r.Region == "" {
			r.Region = RegionWholeRecent
		}
		if err := ValidateRegion(r.Region); err != nil {
			return fmt.Errorf("panestate: manifest %q rule %q: %w", m.ID, r.ID, err)
		}
		// A rule with no matcher at all would match every screen and, at a
		// high enough priority, pin the agent to one state forever.
		if isEmptyGate(&r.Gate) {
			return fmt.Errorf("panestate: manifest %q rule %q has no matchers "+
				"(it would match every screen)", m.ID, r.ID)
		}
		ctx := fmt.Sprintf("manifest %q rule %q", m.ID, r.ID)
		if err := validateGate(&r.Gate, ctx, 1, totals); err != nil {
			return err
		}
	}
	return nil
}

// budget accumulates the per-manifest totals that are capped across all rules
// rather than per gate.
type budget struct {
	gates    int
	matchers int
}

func validateGate(g *Gate, ctx string, depth int, totals *budget) error {
	if depth > MaxGateDepth {
		return fmt.Errorf("panestate: %s exceeds max gate depth %d", ctx, MaxGateDepth)
	}
	totals.gates++
	if totals.gates > MaxTotalGates {
		return fmt.Errorf("panestate: %s exceeds max total gates %d", ctx, MaxTotalGates)
	}
	direct := len(g.Contains) + len(g.Regex) + len(g.LineRegex)
	if direct > MaxMatchersPerGate {
		return fmt.Errorf("panestate: %s has %d direct matchers, max is %d",
			ctx, direct, MaxMatchersPerGate)
	}
	totals.matchers += direct
	if totals.matchers > MaxTotalMatchers {
		return fmt.Errorf("panestate: %s exceeds max total matchers %d", ctx, MaxTotalMatchers)
	}
	for _, set := range [][]string{g.Contains, g.Regex, g.LineRegex} {
		for _, s := range set {
			if len(s) > MaxMatcherChars {
				return fmt.Errorf("panestate: %s has a matcher of %d chars, max is %d",
					ctx, len(s), MaxMatcherChars)
			}
			if s == "" {
				return fmt.Errorf("panestate: %s has an empty matcher", ctx)
			}
		}
	}
	for _, pat := range append(append([]string{}, g.Regex...), g.LineRegex...) {
		if _, _, err := compileTranslated(pat); err != nil {
			return fmt.Errorf("panestate: %s: %w", ctx, err)
		}
	}
	for i := range g.All {
		if err := validateGate(&g.All[i], ctx+" all", depth+1, totals); err != nil {
			return err
		}
	}
	for i := range g.Any {
		if err := validateGate(&g.Any[i], ctx+" any", depth+1, totals); err != nil {
			return err
		}
	}
	for i := range g.Not {
		if err := validateGate(&g.Not[i], ctx+" not", depth+1, totals); err != nil {
			return err
		}
	}
	return nil
}

func isEmptyGate(g *Gate) bool {
	return len(g.All) == 0 && len(g.Any) == 0 && len(g.Not) == 0 &&
		len(g.Contains) == 0 && len(g.Regex) == 0 && len(g.LineRegex) == 0
}

// compileGate lowercases `contains` once and compiles the regexes. `contains`
// is case-INSENSITIVE (matched against a lowercased region) while `regex` and
// `line_regex` are case-SENSITIVE against the raw region — upstream's split,
// and the manifests are authored to it.
func compileGate(g *Gate) (compiledGate, error) {
	out := compiledGate{}
	for _, c := range g.Contains {
		out.contains = append(out.contains, strings.ToLower(c))
	}
	for _, pat := range g.Regex {
		re, notes, err := compileTranslated(pat)
		if err != nil {
			return compiledGate{}, err
		}
		out.regex = append(out.regex, re)
		out.notes = append(out.notes, notes...)
	}
	for _, pat := range g.LineRegex {
		re, notes, err := compileTranslated(pat)
		if err != nil {
			return compiledGate{}, err
		}
		out.lineRegex = append(out.lineRegex, re)
		out.notes = append(out.notes, notes...)
	}
	for i := range g.All {
		c, err := compileGate(&g.All[i])
		if err != nil {
			return compiledGate{}, err
		}
		out.notes = append(out.notes, c.notes...)
		out.all = append(out.all, c)
	}
	for i := range g.Any {
		c, err := compileGate(&g.Any[i])
		if err != nil {
			return compiledGate{}, err
		}
		out.notes = append(out.notes, c.notes...)
		out.any = append(out.any, c)
	}
	for i := range g.Not {
		c, err := compileGate(&g.Not[i])
		if err != nil {
			return compiledGate{}, err
		}
		out.notes = append(out.notes, c.notes...)
		out.not = append(out.not, c)
	}
	return out, nil
}

func compileManifest(m Manifest) (compiledManifest, error) {
	cm := compiledManifest{manifest: m, gates: make([]compiledGate, 0, len(m.Rules))}
	for i := range m.Rules {
		g, err := compileGate(&m.Rules[i].Gate)
		if err != nil {
			return compiledManifest{}, fmt.Errorf("panestate: manifest %q rule %q: %w",
				m.ID, m.Rules[i].ID, err)
		}
		cm.gates = append(cm.gates, g)
	}
	return cm, nil
}

// parseRegionCount reads `name(N)` and returns N. ok is false when the spec
// is not that shape or N is not a plain positive integer.
func parseRegionCount(spec, name string) (int, bool) {
	rest, ok := strings.CutPrefix(spec, name)
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutPrefix(rest, "(")
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutSuffix(rest, ")")
	if !ok {
		return 0, false
	}
	if rest == "" || strings.HasPrefix(rest, "0") {
		return 0, false
	}
	for _, b := range []byte(rest) {
		if b < '0' || b > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 || n > maxRegionLineCount {
		return 0, false
	}
	return n, true
}
