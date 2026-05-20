package hubmcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestValidateArgs_RequiredFields covers the most important guarantee:
// when a schema lists required[] fields, ValidateArgs rejects calls
// missing them. Each table row is one tool from the catalog so the
// regression also doubles as a smoke audit that the existing schemas
// fire on absent fields.
func TestValidateArgs_RequiredFields(t *testing.T) {
	cases := []struct {
		name    string
		schema  string
		args    map[string]any
		wantErr string // substring; empty = expect nil
	}{
		{
			name:    "agents.spawn — host_id missing",
			schema:  `{"type":"object","required":["child_handle","kind","spawn_spec_yaml","host_id"],"properties":{"child_handle":{"type":"string"},"kind":{"type":"string"},"spawn_spec_yaml":{"type":"string"},"host_id":{"type":"string"}}}`,
			args:    map[string]any{"child_handle": "@worker", "kind": "claude-code", "spawn_spec_yaml": "backend:\n  cmd: x\n"},
			wantErr: `"host_id" is required`,
		},
		{
			name:    "agents.spawn — host_id present (passes)",
			schema:  `{"type":"object","required":["child_handle","kind","spawn_spec_yaml","host_id"],"properties":{"child_handle":{"type":"string"},"kind":{"type":"string"},"spawn_spec_yaml":{"type":"string"},"host_id":{"type":"string"}}}`,
			args:    map[string]any{"child_handle": "@worker", "kind": "claude-code", "spawn_spec_yaml": "x", "host_id": "01JABC"},
			wantErr: "",
		},
		{
			name:    "documents.create — title missing",
			schema:  `{"type":"object","required":["project_id","kind","title"],"properties":{"project_id":{"type":"string"},"kind":{"type":"string"},"title":{"type":"string"}}}`,
			args:    map[string]any{"project_id": "01J", "kind": "memo"},
			wantErr: `"title" is required`,
		},
		{
			name:    "plans.steps.create — phase_idx allowed at zero",
			schema:  `{"type":"object","required":["plan","phase_idx","step_idx","kind"],"properties":{"plan":{"type":"string"},"phase_idx":{"type":"integer","minimum":0},"step_idx":{"type":"integer","minimum":0},"kind":{"type":"string"}}}`,
			args:    map[string]any{"plan": "p", "phase_idx": float64(0), "step_idx": float64(0), "kind": "shell"},
			wantErr: "",
		},
		{
			name:    "post_message — channel_id empty string is treated as missing",
			schema:  `{"type":"object","required":["channel_id","text"],"properties":{"channel_id":{"type":"string"},"text":{"type":"string"}}}`,
			args:    map[string]any{"channel_id": "", "text": "hi"},
			wantErr: `"channel_id" is required`,
		},
		{
			name:    "no schema — accepts anything",
			schema:  ``,
			args:    map[string]any{"foo": "bar"},
			wantErr: "",
		},
		{
			name:    "nil args + required schema — rejects",
			schema:  `{"type":"object","required":["x"],"properties":{"x":{"type":"string"}}}`,
			args:    nil,
			wantErr: `"x" is required`,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateArgs(json.RawMessage(tc.schema), tc.args)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected ok, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

// TestValidateArgs_EnumAndType exercises the type checker (notably the
// "JSON integer arrives as float64" case) and the enum keyword.
func TestValidateArgs_EnumAndType(t *testing.T) {
	cases := []struct {
		name    string
		schema  string
		args    map[string]any
		wantErr string
	}{
		{
			name:    "enum — valid",
			schema:  `{"type":"object","properties":{"kind":{"type":"string","enum":["goal","standing"]}}}`,
			args:    map[string]any{"kind": "goal"},
			wantErr: "",
		},
		{
			name:    "enum — rejected",
			schema:  `{"type":"object","properties":{"kind":{"type":"string","enum":["goal","standing"]}}}`,
			args:    map[string]any{"kind": "ad-hoc"},
			wantErr: "must be one of",
		},
		{
			name:    "type mismatch — string declared, number sent",
			schema:  `{"type":"object","properties":{"title":{"type":"string"}}}`,
			args:    map[string]any{"title": float64(42)},
			wantErr: "expected string, got number",
		},
		{
			name:    "integer accepts whole float (JSON-wire reality)",
			schema:  `{"type":"object","properties":{"n":{"type":"integer","minimum":0}}}`,
			args:    map[string]any{"n": float64(5)},
			wantErr: "",
		},
		{
			name:    "integer rejects fractional float",
			schema:  `{"type":"object","properties":{"n":{"type":"integer"}}}`,
			args:    map[string]any{"n": float64(3.14)},
			wantErr: "expected integer",
		},
		{
			name:    "minimum — below threshold",
			schema:  `{"type":"object","properties":{"n":{"type":"integer","minimum":1}}}`,
			args:    map[string]any{"n": float64(0)},
			wantErr: "must be >= 1",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateArgs(json.RawMessage(tc.schema), tc.args)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected ok, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

// TestValidateArgs_NestedAndArray exercises object recursion (the
// `task: {title}` subschema inside agents.spawn) and array items
// (`parts:[{kind}]` inside channels.post_event).
func TestValidateArgs_NestedAndArray(t *testing.T) {
	// agents.spawn-ish nested subschema: task.title is required.
	spawnLike := `{
		"type":"object",
		"required":["child_handle"],
		"properties":{
			"child_handle":{"type":"string"},
			"task":{"type":"object","required":["title"],"properties":{"title":{"type":"string"}}}
		}
	}`
	t.Run("nested object — missing required subfield", func(t *testing.T) {
		err := ValidateArgs(json.RawMessage(spawnLike), map[string]any{
			"child_handle": "@w",
			"task":         map[string]any{"body_md": "doing it"},
		})
		if err == nil || !strings.Contains(err.Error(), `task: "title" is required`) {
			t.Fatalf("expected nested required error, got: %v", err)
		}
	})
	t.Run("nested object — required satisfied", func(t *testing.T) {
		err := ValidateArgs(json.RawMessage(spawnLike), map[string]any{
			"child_handle": "@w",
			"task":         map[string]any{"title": "Investigate flakes"},
		})
		if err != nil {
			t.Fatalf("expected ok, got: %v", err)
		}
	})

	// post_event-ish: parts is an array of objects, each requiring `kind`.
	postLike := `{
		"type":"object",
		"required":["channel","parts"],
		"properties":{
			"channel":{"type":"string"},
			"parts":{"type":"array","minItems":1,"items":{"type":"object","required":["kind"],"properties":{"kind":{"type":"string"}}}}
		}
	}`
	t.Run("array items — empty parts rejected by minItems", func(t *testing.T) {
		err := ValidateArgs(json.RawMessage(postLike), map[string]any{
			"channel": "team-default",
			"parts":   []any{},
		})
		// parts is empty — treated as missing-required by isEmpty before
		// we even reach minItems. Either error message is fine; both
		// communicate "parts must have content".
		if err == nil {
			t.Fatal("expected error on empty parts, got nil")
		}
	})
	t.Run("array items — item missing required field", func(t *testing.T) {
		err := ValidateArgs(json.RawMessage(postLike), map[string]any{
			"channel": "team-default",
			"parts":   []any{map[string]any{"text": "hi"}},
		})
		if err == nil || !strings.Contains(err.Error(), `parts[0]: "kind" is required`) {
			t.Fatalf("expected array-item required error, got: %v", err)
		}
	})
	t.Run("array items — well-formed payload", func(t *testing.T) {
		err := ValidateArgs(json.RawMessage(postLike), map[string]any{
			"channel": "team-default",
			"parts":   []any{map[string]any{"kind": "text", "text": "hi"}},
		})
		if err != nil {
			t.Fatalf("expected ok, got: %v", err)
		}
	})
}
