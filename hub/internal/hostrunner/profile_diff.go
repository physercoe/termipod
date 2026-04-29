// Profile-vs-legacy event diff (ADR-010 Phase 1.6 + 1.5).
//
// Used by:
//   - Production: the driver's "both" frame_translator mode runs the
//     legacy translator in shadow and the profile authoritatively,
//     then logs any divergence via DiffEvents so an operator can spot
//     profile gaps as they happen.
//   - Test: the parity test in profile_parity_test.go reads
//     testdata/profiles/<engine>/corpus.jsonl, runs both translators
//     against every frame, and fails on any DiffEvents return.
//
// Both call sites pass the same ParityIgnoreFields map, so a known
// gap (modelUsage inner-key renaming, overage_disabled boolean
// projection, …) is documented once and respected everywhere.
package hostrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
)

// ParityIgnoreFields lists payload field names where the legacy and
// profile-driven translators are known to disagree by design until a
// future grammar extension lands. Adding a field here is a deliberate
// policy decision; the comment on each entry explains why.
//
//   - by_model: legacy normalizeTurnResult walks modelUsage's per-
//     model map and renames the inner camelCase keys (inputTokens →
//     input, …). The v1 grammar has no map-iter construct; by_model
//     passes through with the original keys. See plan §7.
//   - overage_disabled: legacy translateRateLimit derives a boolean
//     from `firstNonNil(reason) != nil`. The v1 grammar has no
//     "is-this-non-nil" predicate at expression level; the profile
//     emits the reason string directly and lets the mobile renderer
//     derive the boolean. Mobile reads `reason`, not `overage_disabled`,
//     so this is a wire-shape difference without functional impact.
var ParityIgnoreFields = map[string]string{
	"by_model":         "v1 grammar lacks map-iter; modelUsage passes through verbatim",
	"overage_disabled": "v1 grammar lacks bool-from-nullable; mobile reads reason directly",
}

// DiffEvents compares the events produced by the legacy translator
// (captured into []EmittedEvent via capturingPoster) to those
// produced by ApplyProfile. Returns "" when they match modulo the
// ignoreFields map, or a multi-line human-readable summary that an
// AI-agent maintainer can act on.
//
// The diff format:
//
//	count differs:    legacy=N  profile=M
//	event[i] kind:    legacy=…  profile=…
//	event[i].field:   legacy=…  profile=…  (extra/missing/mismatch)
//
// Order matters: legacy emits events in a fixed order
// (per-block first, then usage; turn.result before completion); the
// profile must produce them in the same order. If a future change
// makes order non-deterministic we'll need a multiset compare, but
// that's a regression we want to surface, not paper over.
func DiffEvents(legacy, profile []EmittedEvent, ignoreFields map[string]string) string {
	var b strings.Builder
	if len(legacy) != len(profile) {
		fmt.Fprintf(&b, "  count differs: legacy=%d  profile=%d\n",
			len(legacy), len(profile))
		fmt.Fprintf(&b, "  legacy kinds:  %v\n", kindsOf(legacy))
		fmt.Fprintf(&b, "  profile kinds: %v\n", kindsOf(profile))
		return b.String()
	}
	for i := range legacy {
		L := legacy[i]
		P := profile[i]
		if L.Kind != P.Kind {
			fmt.Fprintf(&b, "  event[%d] kind:     legacy=%q  profile=%q\n",
				i, L.Kind, P.Kind)
		}
		if L.Producer != P.Producer {
			fmt.Fprintf(&b, "  event[%d] producer: legacy=%q  profile=%q\n",
				i, L.Producer, P.Producer)
		}
		keys := unionPayloadKeys(L.Payload, P.Payload)
		for _, k := range keys {
			if _, ignore := ignoreFields[k]; ignore {
				continue
			}
			lv, lOK := L.Payload[k]
			pv, pOK := P.Payload[k]
			switch {
			case lOK && !pOK:
				fmt.Fprintf(&b, "  event[%d].%s missing in profile: legacy=%v\n",
					i, k, lv)
			case pOK && !lOK:
				fmt.Fprintf(&b, "  event[%d].%s extra in profile: profile=%v\n",
					i, k, pv)
			case !reflect.DeepEqual(lv, pv):
				fmt.Fprintf(&b, "  event[%d].%s differs: legacy=%v  profile=%v\n",
					i, k, lv, pv)
			}
		}
	}
	return b.String()
}

func kindsOf(events []EmittedEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}

func unionPayloadKeys(a, b map[string]any) []string {
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// MustEncodeJSON is exported because the parity test uses it to
// surface the offending frame in error messages alongside the diff.
// Panics on encoding errors are deliberate — we're stringifying
// already-decoded JSON, so a failure here is a bug, not user input.
func MustEncodeJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// capturingPoster implements AgentEventPoster by appending each event
// to an in-memory slice. Used in the driver's "both" frame_translator
// mode to shadow-run the legacy translator without writing to the
// real DB; the captured events are diffed against the profile output
// and any divergence is logged.
//
// Thread-safety: a single shadow run is serialized inside translate()
// (one goroutine per frame), but we still hold a mutex for hygiene
// in case a future caller composes the poster across goroutines.
type capturingPoster struct {
	mu     sync.Mutex
	events []EmittedEvent
}

func (c *capturingPoster) PostAgentEvent(_ context.Context, _, kind, producer string, payload any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, _ := payload.(map[string]any)
	if m == nil {
		// Legacy translateRateLimit / similar always pass map[string]any,
		// but a defensive nil avoids a panic if a future caller forgets.
		m = map[string]any{}
	}
	c.events = append(c.events, EmittedEvent{
		Kind:     kind,
		Producer: producer,
		Payload:  m,
	})
	return nil
}
