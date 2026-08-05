package agentfamilies

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestSchema_DocumentsEveryLoaderField pins the JSON Schema next to the
// YAML against the structs that actually parse it.
//
// `agent_families.schema.json` is an editor contract only — the loader
// is `yaml.Unmarshal`, which ignores it, so nothing at runtime notices
// when the two drift. They had drifted twice by the time this test was
// written: the family-level capability maps (`prompt_image` and
// friends) and `payload_maps` both shipped as Go fields whose YAML the
// schema rejected, because `additionalProperties: false` makes every
// unlisted field an error. The visible symptom is backwards — the
// schema flags the *correct* file, so an author learns to ignore it,
// and after that it documents nothing.
//
// The invariant asserted is the one that broke: every `yaml:` tag the
// loader accepts is a property the schema declares. The reverse
// direction (a schema property with no Go field) is checked too — it
// means the schema promises a knob the loader silently drops.
func TestSchema_DocumentsEveryLoaderField(t *testing.T) {
	raw, err := os.ReadFile("agent_families.schema.json")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema struct {
		Defs map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}

	// Go type → the `$defs` entry that documents it. MapProjection and
	// ListProjection share one entry: they are the same shape over a
	// map and over an array, and duplicating it would let the two
	// descriptions disagree.
	cases := []struct {
		def string
		typ reflect.Type
	}{
		{"Family", reflect.TypeOf(Family{})},
		{"Incompat", reflect.TypeOf(Incompat{})},
		{"LaunchMode", reflect.TypeOf(LaunchMode{})},
		{"FrameProfile", reflect.TypeOf(FrameProfile{})},
		{"Rule", reflect.TypeOf(Rule{})},
		{"Emit", reflect.TypeOf(Emit{})},
		{"Projection", reflect.TypeOf(MapProjection{})},
		{"Projection", reflect.TypeOf(ListProjection{})},
	}

	for _, tc := range cases {
		def, ok := schema.Defs[tc.def]
		if !ok {
			t.Errorf("$defs.%s missing — %s has no schema entry", tc.def, tc.typ.Name())
			continue
		}
		fields := map[string]bool{}
		for i := 0; i < tc.typ.NumField(); i++ {
			name := yamlFieldName(tc.typ.Field(i).Tag.Get("yaml"))
			if name == "" || name == "-" {
				continue
			}
			fields[name] = true
			if _, ok := def.Properties[name]; !ok {
				t.Errorf("%s.%s has yaml:%q but $defs.%s declares no such property — "+
					"a YAML file using it fails the schema's additionalProperties:false",
					tc.typ.Name(), tc.typ.Field(i).Name, name, tc.def)
			}
		}
		for name := range def.Properties {
			if !fields[name] {
				t.Errorf("$defs.%s declares %q but %s has no field with that yaml tag — "+
					"the schema promises a knob the loader drops on the floor",
					tc.def, name, tc.typ.Name())
			}
		}
	}
}

// yamlFieldName takes the field name out of a `yaml:"name,omitempty"`
// tag. Returns "" for an absent tag (yaml.v3 then lowercases the Go
// field name, which no schema property is expected to match — such a
// field is a bug in its own right, so leaving it uncovered here would
// hide it rather than report it; every field in this package tags
// explicitly).
func yamlFieldName(tag string) string {
	if tag == "" {
		return ""
	}
	if i := strings.IndexByte(tag, ','); i >= 0 {
		tag = tag[:i]
	}
	return strings.TrimSpace(tag)
}
