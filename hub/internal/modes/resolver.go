// Package modes resolves the concrete agent driving mode at spawn time.
//
// Per blueprint §5.3.2, mode resolution is a pure function over four
// inputs: the template's declared mode (+ fallbacks), the spawner's
// optional override, the host's probed capabilities for this agent
// family, and the user's declared billing context. The output is either
// a concrete mode (M1|M2|M4) or a fail-fast error listing why each
// candidate was rejected — there is no "best effort, hope it works"
// path.
//
// Keeping this logic in a standalone package makes it trivially unit-
// testable and lets both the spawn handler (live path) and a future
// "dry-run" endpoint (previewing what mode a template would pick)
// share the same code.
package modes

import (
	"fmt"
	"strings"
)

// Billing is the declared billing context for this agent family on this
// host. Unknown means "not declared" — resolution proceeds as if no
// billing constraint applies (caller can upgrade to required=true if
// they want strict mode).
type Billing string

const (
	BillingUnknown      Billing = ""
	BillingAPIKey       Billing = "api_key"
	BillingSubscription Billing = "subscription"
)

// Incompat is one mode×billing rejection rule. Resolver consults the
// list attached to the input at spawn time; the data is owned by
// agentfamilies (embedded YAML) so adding a vendor-side billing quirk
// never lands as a resolver patch.
type Incompat struct {
	Mode    string  // M1|M2|M4 (case-insensitive)
	Billing Billing // api_key|subscription
	Reason  string  // human-readable rejection rationale
}

// Input is the resolver's contract. All slices may be nil; Override is
// optional. Modes are case-insensitive on input but normalized to
// upper-case in the result.
type Input struct {
	AgentKind         string   // "claude-code", "gemini-cli", "codex"
	Requested         string   // template.driving_mode — M1|M2|M4
	FallbackModes     []string // template.fallback_modes — ordered
	Override          string   // spawn_request.mode — if set, the *only* candidate
	Billing           Billing
	HostInstalled     bool     // is the agent binary present on this host?
	HostSupports      []string // host.capabilities.agents[kind].supports
	Incompatibilities []Incompat
}

// Result is the resolver's decision. Reason is a short human-readable
// rationale suitable for an audit line; it includes the winning mode
// and, if there was more than one candidate, which earlier candidates
// were dropped.
type Result struct {
	Mode   string
	Reason string
}

// Error carries structured rejection reasons so callers can present them
// individually (e.g. the UI highlights why each fallback failed rather
// than showing only the first message).
type Error struct {
	Reasons []string // one entry per tried candidate, in order
}

func (e *Error) Error() string {
	return "no compatible mode: " + strings.Join(e.Reasons, "; ")
}

// Resolve walks candidates in order and returns the first one that passes
// every check, or an Error listing every rejection.
func Resolve(in Input) (Result, error) {
	candidates := buildCandidates(in)
	if len(candidates) == 0 {
		return Result{}, &Error{Reasons: []string{"no mode requested"}}
	}

	var reasons []string
	for _, m := range candidates {
		if why := rejectReason(m, in); why != "" {
			reasons = append(reasons, fmt.Sprintf("%s: %s", m, why))
			continue
		}
		return Result{Mode: m, Reason: resolveReason(m, in, reasons)}, nil
	}
	return Result{}, &Error{Reasons: reasons}
}

// buildCandidates normalises + dedups the mode list we will try. An
// explicit override short-circuits the template's list — a spawner that
// names a mode is opting in to that mode alone.
func buildCandidates(in Input) []string {
	if ov := normalize(in.Override); ov != "" {
		return []string{ov}
	}
	seen := map[string]bool{}
	var out []string
	add := func(m string) {
		m = normalize(m)
		if m == "" || seen[m] {
			return
		}
		seen[m] = true
		out = append(out, m)
	}
	add(in.Requested)
	for _, m := range in.FallbackModes {
		add(m)
	}
	return out
}

func normalize(m string) string {
	m = strings.TrimSpace(strings.ToUpper(m))
	switch m {
	case "M1", "M2", "M4":
		return m
	}
	return ""
}

// rejectReason returns "" if the mode is compatible with the rest of
// the input, or a short phrase explaining the incompatibility. Checks
// are ordered cheap→expensive so the reason string is the most
// informative one (e.g. "not installed" beats "M1 requires api_key").
func rejectReason(mode string, in Input) string {
	if !in.HostInstalled {
		return fmt.Sprintf("%s not installed on host", in.AgentKind)
	}
	if !contains(in.HostSupports, mode) {
		return fmt.Sprintf("host does not support %s for %s", mode, in.AgentKind)
	}
	if why := billingConflict(mode, in); why != "" {
		return why
	}
	return ""
}

// billingConflict walks Input.Incompatibilities looking for a record
// matching the candidate mode + the declared billing. Reasons are
// authored at the data layer (agentfamilies/agent_families.yaml) so
// the resolver stays family-agnostic — adding a new SDK limitation is
// a YAML edit. An unset (BillingUnknown) declaration never trips a
// conflict since we don't know what to enforce against.
func billingConflict(mode string, in Input) string {
	if in.Billing == BillingUnknown {
		return ""
	}
	for _, ic := range in.Incompatibilities {
		if normalize(ic.Mode) == mode && ic.Billing == in.Billing {
			if ic.Reason != "" {
				return ic.Reason
			}
			return "billing/mode combination disallowed"
		}
	}
	return ""
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// resolveReason renders a one-line rationale for the winning mode. When
// earlier candidates were dropped, they are appended so the audit trail
// captures *why* we ended up here and not at the preferred mode.
func resolveReason(mode string, in Input, dropped []string) string {
	src := "requested"
	if in.Override != "" {
		src = "override"
	} else if len(dropped) > 0 {
		src = "fallback"
	}
	base := fmt.Sprintf("mode=%s (%s) for %s", mode, src, in.AgentKind)
	if len(dropped) > 0 {
		base += " — dropped: " + strings.Join(dropped, ", ")
	}
	return base
}
