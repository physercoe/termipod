// run_config_diff.go — the comparison wall's config comparer, server side
// (docs/plans/desktop-compare-wall-and-decisions.md §3.2/§3.5).
//
// The wall answers "which config mattered?" by flattening every selected run's
// config to dotted leaves and showing only the keys that differ. §3.5 gives
// agents the same answer under the same row model — "one row model, two
// consumers" — which is why the flattening rules here are not merely similar
// to the desktop's but IDENTICAL, down to how a float prints.
//
// The two implementations are pinned to each other by a shared fixture:
// testdata/config_diff_fixture.json is read by TestConfigDiff_SharedFixture
// here AND by compareRuns.test.ts on the desktop side. Change one flattener
// and the other language's test fails — the only thing that keeps two
// implementations of one contract honest.
//
// Two sources per run, deliberately unioned rather than picked between:
// `runs.config_json` is what was registered when the run was created, and the
// `/config` digest is what the training script actually loaded. When they
// disagree the logged one wins (it is what ran) and the key is reported as a
// conflict rather than swallowed.

package hubmcpserver

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// cfgEntry is one leaf of a flattened config.
type cfgEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// cfgRun is one run's flattened config, in the caller's run order.
type cfgRun struct {
	ID      string
	Entries []cfgEntry
}

// cfgDiffRow is one comparer row: a key, one cell per run (nil = the key is
// ABSENT for that run, which is not the same as present-and-empty), and
// whether every run agrees.
type cfgDiffRow struct {
	Key       string    `json:"key"`
	Values    []*string `json:"values"`
	Identical bool      `json:"identical"`
}

// jsNumber formats a float the way ECMAScript's Number::toString does, because
// the desktop's flattener renders leaves with JavaScript's own number→string
// and a row model that disagrees on "0.0001" vs "1e-04" is not one row model.
//
// The JS rule: decimal notation while |x| is in [1e-6, 1e21), exponential
// outside it, shortest round-trip digits either way, and an exponent written
// without leading zeros. Go's 'f'/-1 and 'e'/-1 give the same digits; only the
// exponent padding differs.
func jsNumber(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0" // JS prints -0 as "0" too
	}
	if abs := math.Abs(f); abs >= 1e21 || abs < 1e-6 {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		i := strings.IndexByte(s, 'e')
		if i < 0 {
			return s
		}
		mant, exp := s[:i], s[i+1:]
		sign := ""
		if exp != "" && (exp[0] == '+' || exp[0] == '-') {
			sign, exp = string(exp[0]), exp[1:]
		}
		exp = strings.TrimLeft(exp, "0")
		if exp == "" {
			exp = "0"
		}
		return mant + "e" + sign + exp
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func cfgScalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return jsNumber(t)
	case nil:
		return "null"
	}
	return fmt.Sprint(v)
}

func cfgWalk(v any, path string, out *[]cfgEntry) {
	switch t := v.(type) {
	case []any:
		if len(t) == 0 {
			if path != "" {
				*out = append(*out, cfgEntry{Key: path, Value: "[]"})
			}
			return
		}
		for i, el := range t {
			cfgWalk(el, fmt.Sprintf("%s[%d]", path, i), out)
		}
	case map[string]any:
		if len(t) == 0 {
			if path != "" {
				*out = append(*out, cfgEntry{Key: path, Value: "{}"})
			}
			return
		}
		for k, el := range t {
			p := k
			if path != "" {
				p = path + "." + k
			}
			cfgWalk(el, p, out)
		}
	default:
		// A scalar at the ROOT has no key to hang on and is dropped: a config
		// that is a bare number is not a config, and inventing a key for it
		// would put a phantom row in the comparer.
		if path != "" {
			*out = append(*out, cfgEntry{Key: path, Value: cfgScalar(v)})
		}
	}
}

func cfgFlattenValue(v any) []cfgEntry {
	out := []cfgEntry{}
	cfgWalk(v, "", &out)
	// Sorted so the row order is stable across runs whose configs were written
	// with different key order (Go map iteration is randomised, so this sort is
	// also what makes the output deterministic at all).
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// cfgFlattenText flattens a config that arrives as JSON TEXT — the shape the
// runs table stores (`runOut.ConfigJSON` is a string). Unparseable text
// flattens to nothing rather than failing the call: one run with a hand-edited
// config must not blank the whole comparison.
func cfgFlattenText(s string) []cfgEntry {
	if strings.TrimSpace(s) == "" {
		return []cfgEntry{}
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return []cfgEntry{}
	}
	return cfgFlattenValue(v)
}

// cfgFlattenRaw flattens a config that arrives as a JSON VALUE — the shape the
// /config digest returns. A JSON string is unwrapped once, so the same helper
// serves both wire shapes.
func cfgFlattenRaw(raw json.RawMessage) []cfgEntry {
	if len(raw) == 0 {
		return []cfgEntry{}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return []cfgEntry{}
	}
	if s, ok := v.(string); ok {
		return cfgFlattenText(s)
	}
	return cfgFlattenValue(v)
}

// cfgMerge unions two flattened configs, the LOGGED one winning on a
// collision, and returns the keys they disagreed on. The disagreement is a
// finding ("what we said we would run" vs "what ran"), so it is named rather
// than swallowed; reconciling it properly is the A4 provenance triad.
func cfgMerge(registered, logged []cfgEntry) ([]cfgEntry, []string) {
	byKey := make(map[string]string, len(registered)+len(logged))
	for _, e := range registered {
		byKey[e.Key] = e.Value
	}
	conflicts := []string{}
	for _, e := range logged {
		if prev, ok := byKey[e.Key]; ok && prev != e.Value {
			conflicts = append(conflicts, e.Key)
		}
		byKey[e.Key] = e.Value
	}
	out := make([]cfgEntry, 0, len(byKey))
	for k, v := range byKey {
		out = append(out, cfgEntry{Key: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	sort.Strings(conflicts)
	return out, conflicts
}

// cfgDiffRows builds the sorted key union with one cell per run.
//
// `identical` counts an ABSENT key as a value: two runs where only one sets
// `resume_from` DIFFER, even though only one of them has anything to show.
// Hiding that row (the desktop's default hides identical ones) would hide the
// actual difference between the runs.
func cfgDiffRows(runs []cfgRun) []cfgDiffRow {
	maps := make([]map[string]string, len(runs))
	keys := map[string]struct{}{}
	for i, r := range runs {
		m := make(map[string]string, len(r.Entries))
		for _, e := range r.Entries {
			m[e.Key] = e.Value
			keys[e.Key] = struct{}{}
		}
		maps[i] = m
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	rows := make([]cfgDiffRow, 0, len(sorted))
	for _, k := range sorted {
		values := make([]*string, len(maps))
		identical := true
		for i, m := range maps {
			if v, ok := m[k]; ok {
				vv := v
				values[i] = &vv
			}
			if i > 0 && !sameCell(values[0], values[i]) {
				identical = false
			}
		}
		rows = append(rows, cfgDiffRow{Key: k, Values: values, Identical: identical})
	}
	return rows
}

func sameCell(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
