package hostrunner

import "testing"

// Exercises the env-profile launch injection (env-profiles plan, E1b): the
// shell `export …` clause spliced into every driving mode's `cd … && cmd`, the
// []string form the gemini exec path uses, and that ParseSpec round-trips the
// hub-materialized env_profile_id + env_vars.

func TestEnvExportPrefix(t *testing.T) {
	if got := envExportPrefix(nil); got != "" {
		t.Fatalf("nil map: want empty, got %q", got)
	}
	if got := envExportPrefix(map[string]string{}); got != "" {
		t.Fatalf("empty map: want empty, got %q", got)
	}
	// Sorted keys, single-quoted values (shellEscape), trailing ` && ` so the
	// caller can splice unconditionally.
	got := envExportPrefix(map[string]string{"B_VAR": "two words", "A_VAR": "v1"})
	if want := "export A_VAR='v1' B_VAR='two words' && "; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// A value containing a single quote is escaped, not left to break the shell.
	got = envExportPrefix(map[string]string{"Q": "a'b"})
	if want := `export Q='a'\''b' && `; got != want {
		t.Fatalf("quote escape: got %q want %q", got, want)
	}
	// A malformed key (couldn't come from the validating hub, but a tampered
	// spec could) is dropped rather than injected raw into the shell.
	got = envExportPrefix(map[string]string{"1BAD": "x", "OK": "y"})
	if want := "export OK='y' && "; got != want {
		t.Fatalf("drop bad key: got %q want %q", got, want)
	}
	// All keys malformed → no export clause at all.
	if got := envExportPrefix(map[string]string{"1BAD": "x"}); got != "" {
		t.Fatalf("all bad: want empty, got %q", got)
	}
}

func TestEnvKVList(t *testing.T) {
	if envKVList(nil) != nil {
		t.Fatalf("nil map should yield nil slice")
	}
	got := envKVList(map[string]string{"B": "2", "A": "1"})
	if len(got) != 2 || got[0] != "A=1" || got[1] != "B=2" {
		t.Fatalf("sorted K=V: %v", got)
	}
	// exec.Cmd.Env entries are verbatim — a space in the value is NOT shell-escaped.
	got = envKVList(map[string]string{"X": "a b"})
	if len(got) != 1 || got[0] != "X=a b" {
		t.Fatalf("verbatim value: %v", got)
	}
	// Malformed key dropped.
	if got := envKVList(map[string]string{"1BAD": "x"}); got != nil && len(got) != 0 {
		t.Fatalf("bad key: %v", got)
	}
}

func TestSpecParsesEnvProfileFields(t *testing.T) {
	yaml := "env_profile_id: prof123\n" +
		"env_vars:\n  FOO: bar\n  BAZ: qux\n" +
		"backend:\n  cmd: claude\n"
	spec, err := ParseSpec(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if spec.EnvProfileID != "prof123" {
		t.Fatalf("env_profile_id: %q", spec.EnvProfileID)
	}
	if spec.EnvVars["FOO"] != "bar" || spec.EnvVars["BAZ"] != "qux" {
		t.Fatalf("env_vars: %v", spec.EnvVars)
	}
	// The clause the launcher would splice for this spec.
	if got := envExportPrefix(spec.EnvVars); got != "export BAZ='qux' FOO='bar' && " {
		t.Fatalf("prefix from parsed spec: %q", got)
	}
}
