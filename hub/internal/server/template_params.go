package server

import (
	"fmt"
	"regexp"
	"strings"
)

// Template parameter substitution (#27, #29).
//
// Project templates carry `{placeholder}` tokens in their goal text and in
// per-phase criteria bodies (e.g. "{target_framework} installed …"). Those
// tokens are resolved against the concrete project's parameters_json — once,
// at create / hydration time, server-side — so every consumer (mobile,
// criteria UI, documents) reads the same already-resolved text. Previously
// nothing resolved them: the goal and criteria bodies surfaced raw
// `{target_framework}` syntax to the director.

// templateParamPattern matches a single `{identifier}` token. The grammar is
// deliberately tiny — an ASCII identifier wrapped in single braces — so it
// can't collide with JSON, code snippets, or `${…}`/`{{…}}` constructs that
// legitimately appear in criterion bodies.
var templateParamPattern = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// substituteTemplateParams replaces each `{key}` in text with the matching
// value from params (stringified via fmt.Sprint). Unknown keys are left
// verbatim — substitution is best-effort and one-way; a missing parameter
// should not corrupt the surrounding text. Returns text unchanged when it
// has no params or no `{`.
func substituteTemplateParams(text string, params map[string]any) string {
	if text == "" || len(params) == 0 || !strings.Contains(text, "{") {
		return text
	}
	return templateParamPattern.ReplaceAllStringFunc(text, func(tok string) string {
		key := tok[1 : len(tok)-1]
		if v, ok := params[key]; ok {
			return fmt.Sprint(v)
		}
		return tok
	})
}

// substituteParamsInMap walks a decoded YAML/JSON map in place and resolves
// `{key}` tokens in every string leaf (recursing into nested maps and slices).
// Used to resolve a hydrated criterion's `body` — typically body.text — before
// it is persisted as JSON.
func substituteParamsInMap(m map[string]any, params map[string]any) {
	if len(params) == 0 {
		return
	}
	for k, v := range m {
		m[k] = substituteParamsInValue(v, params)
	}
}

func substituteParamsInValue(v any, params map[string]any) any {
	switch vv := v.(type) {
	case string:
		return substituteTemplateParams(vv, params)
	case map[string]any:
		substituteParamsInMap(vv, params)
		return vv
	case []any:
		for i, e := range vv {
			vv[i] = substituteParamsInValue(e, params)
		}
		return vv
	default:
		return v
	}
}
