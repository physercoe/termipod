// schema_validate.go — minimal JSON-Schema validator for the MCP
// dispatcher boundary. Both dispatchers (authority `handleToolsCall`
// and native `dispatchTool`) call ValidateArgs before invoking the
// handler so a tool whose `required:` declaration is unmet, whose
// enum is violated, or whose type is wrong is rejected with a clean
// -32602 (invalid params) — instead of falling into the handler and
// landing weird downstream effects (the agents.spawn host_id incident
// being the load-bearing example).
//
// Scope is the subset of draft-2020-12 the catalog actually uses today:
// `type`, `required`, `properties` (recursively), `enum`, `minimum`,
// `items`, `minItems`. The validator is intentionally narrow — when a
// schema later wants `oneOf` / `if-then` to express conditional
// requirements (e.g. documents.create's content_inline ⊕ artifact_id),
// the support lands here as an additive change with new test cases.
//
// Why hand-rolled rather than an off-the-shelf jsonschema library: the
// hub stays pure-Go-no-cgo (modernc.org/sqlite), the schemas live in
// our repo so we control the subset, and ~120 LOC keeps the dependency
// graph + supply-chain surface tighter. The cost is that exotic
// features must be added as needed.
package hubmcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// ValidateArgs validates args against the JSON-Schema in schemaJSON
// and returns an error describing the first violation. nil/empty
// schemaJSON returns nil (the legacy "no schema declared" case stays
// permissive — a tool that wants validation must declare it). nil args
// is treated identically to an empty map; a schema with required
// fields will then reject.
func ValidateArgs(schemaJSON json.RawMessage, args map[string]any) error {
	if len(schemaJSON) == 0 {
		return nil
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		// A malformed schema is a developer-side bug; fail open so a
		// broken tool definition doesn't take down dispatch for
		// every other tool. The error log lives at the call site.
		return nil
	}
	if args == nil {
		args = map[string]any{}
	}
	return validateValue("", args, schema)
}

// validateValue recursively checks value against schema. path is the
// dotted JSON pointer the caller has accumulated so error messages
// point at the offending field (`task.title`, `parts[0].kind`, …).
func validateValue(path string, value any, schema map[string]any) error {
	if t, ok := schema["type"].(string); ok {
		if err := checkType(path, value, t); err != nil {
			return err
		}
	}
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		matched := false
		for _, candidate := range enum {
			if equalJSON(value, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s: must be one of %v", labelOr(path, "value"), enum)
		}
	}
	if min, ok := numericField(schema, "minimum"); ok {
		if n, isNum := toFloat(value); isNum && n < min {
			return fmt.Errorf("%s: must be >= %v", labelOr(path, "value"), min)
		}
	}
	// Object: recurse into declared properties, enforce required[].
	if obj, ok := value.(map[string]any); ok {
		if reqRaw, found := schema["required"]; found {
			for _, name := range toStringSlice(reqRaw) {
				v, present := obj[name]
				if !present || isEmpty(v) {
					return fmt.Errorf("%s: %q is required", labelOr(path, "args"), name)
				}
			}
		}
		if props, ok := schema["properties"].(map[string]any); ok {
			for key, propSchemaRaw := range props {
				propSchema, ok := propSchemaRaw.(map[string]any)
				if !ok {
					continue
				}
				v, present := obj[key]
				if !present {
					continue // required check above already fired if needed
				}
				child := key
				if path != "" {
					child = path + "." + key
				}
				if err := validateValue(child, v, propSchema); err != nil {
					return err
				}
			}
		}
	}
	// Array: minItems + per-item recursion.
	if arr, ok := value.([]any); ok {
		if minItems, ok := numericField(schema, "minItems"); ok {
			if float64(len(arr)) < minItems {
				return fmt.Errorf("%s: must have >= %d item(s)", labelOr(path, "array"), int(minItems))
			}
		}
		if itemsSchema, ok := schema["items"].(map[string]any); ok {
			for i, item := range arr {
				if err := validateValue(fmt.Sprintf("%s[%d]", path, i), item, itemsSchema); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// checkType enforces the JSON-Schema `type` keyword. JSON numbers
// decode to float64 in Go's default decoder, so a value that is
// declared `integer` is accepted when its float64 has no fractional
// part — otherwise the validator would reject perfectly valid wire
// frames like `phase_idx: 0`.
func checkType(path string, value any, want string) error {
	got := jsonTypeOf(value)
	if got == want {
		return nil
	}
	if want == "integer" && got == "number" {
		if n, ok := value.(float64); ok && n == math.Trunc(n) {
			return nil
		}
	}
	if want == "number" && got == "integer" {
		return nil
	}
	return fmt.Errorf("%s: expected %s, got %s", labelOr(path, "value"), want, got)
}

func jsonTypeOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		return "number"
	case int, int64, int32:
		return "integer"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}

// isEmpty mirrors the "field present but empty" idiom most handlers
// already enforce by hand: `if args["foo"].(string) == "" { return
// "foo required" }`. Keeping the semantics consistent here means the
// new dispatcher gate accepts the same set of payloads the manual
// per-handler check would have accepted.
func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok && s == "" {
		return true
	}
	if a, ok := v.([]any); ok && len(a) == 0 {
		return true
	}
	if m, ok := v.(map[string]any); ok && len(m) == 0 {
		return true
	}
	return false
}

func equalJSON(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if af, aok := toFloat(a); aok {
		if bf, bok := toFloat(b); bok {
			return af == bf
		}
	}
	return a == b
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func numericField(schema map[string]any, name string) (float64, bool) {
	v, ok := schema[name]
	if !ok {
		return 0, false
	}
	return toFloat(v)
}

func toStringSlice(v any) []string {
	switch arr := v.(type) {
	case []any:
		out := make([]string, 0, len(arr))
		for _, x := range arr {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return arr
	}
	return nil
}

func labelOr(path, fallback string) string {
	if path != "" {
		return path
	}
	return fallback
}

// ErrSchemaValidation is the sentinel a caller may check to distinguish
// schema-side rejections from other invalid-params errors. Most callers
// just propagate the error verbatim — only the dispatcher hint-emission
// would need to branch.
var ErrSchemaValidation = errors.New("schema validation failed")
