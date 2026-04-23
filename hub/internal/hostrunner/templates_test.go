package hostrunner

import (
	"testing"
	"testing/fstest"
)

// TestLoadAgentTemplates proves the host-runner's per-kind template index
// is built purely from YAML — dropping a new file with a distinct
// `template:` key is sufficient to expose its skills and backend.cmd to
// the runner, with no code change.
func TestLoadAgentTemplates(t *testing.T) {
	fsys := fstest.MapFS{
		"agents/custom.v1.yaml": &fstest.MapFile{
			Data: []byte(`template: agents.custom
backend:
  cmd: "greeter --loop"
skills:
  - id: greet
    name: greet
    description: "Say hello"
    tags: [social]
`),
		},
		"agents/bare.v1.yaml": &fstest.MapFile{
			Data: []byte(`template: agents.bare
`),
		},
		"agents/bad.v1.yaml": &fstest.MapFile{
			Data: []byte("this: is: not: yaml"),
		},
	}
	tpl, err := loadAgentTemplates(fsys, "agents")
	if err != nil {
		t.Fatalf("loadAgentTemplates: %v", err)
	}

	if got := tpl.Skills("agents.custom"); len(got) != 1 || got[0].ID != "greet" {
		t.Errorf("custom skills: got %+v", got)
	}
	if got := tpl.BackendCmd("agents.custom"); got != "greeter --loop" {
		t.Errorf("custom backend.cmd: got %q", got)
	}

	// bare template: index entry exists but skills/cmd are empty.
	if got := tpl.Skills("agents.bare"); len(got) != 0 {
		t.Errorf("bare skills: got %+v, want empty", got)
	}
	if got := tpl.BackendCmd("agents.bare"); got != "" {
		t.Errorf("bare backend.cmd: got %q, want empty", got)
	}

	// Unknown kind returns zero values rather than erroring — callers
	// treat that as "no template override, use launcher default".
	if got := tpl.Skills("agents.unknown"); got != nil {
		t.Errorf("unknown skills: got %+v, want nil", got)
	}
	if got := tpl.BackendCmd("agents.unknown"); got != "" {
		t.Errorf("unknown backend.cmd: got %q, want empty", got)
	}

	// A nil receiver is tolerated so tests and early-init code can call
	// through without an allocation. This matters because defaults() may
	// install a zero-value *agentTemplates when the loader errors — we
	// don't want those call sites to panic.
	var zero *agentTemplates
	if got := zero.Skills("anything"); got != nil {
		t.Errorf("nil Skills: got %+v, want nil", got)
	}
	if got := zero.BackendCmd("anything"); got != "" {
		t.Errorf("nil BackendCmd: got %q, want empty", got)
	}
}
