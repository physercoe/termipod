package server

import (
	"strings"

	"github.com/termipod/hub/internal/resumerecipes"
	"gopkg.in/yaml.v3"
)

// spliceResume threads a captured engine session id back into a rendered
// spawn spec so the respawned agent reattaches instead of cold-starting.
//
// This is the ONE dispatch. It replaces the two hand-maintained switches that
// used to live at the resume and spec-mutation call sites; they had already
// drifted (the spec-mutation copy never grew antigravity's arm, which was
// latent only because that path gates on flagForField first). Which mechanism
// a family uses is data — hub/internal/resumerecipes/recipes.yaml — so adding
// an engine is a row, not an edit in two files someone has to remember.
//
// Returns the spec unchanged whenever it cannot act: unknown family, empty id,
// unparseable YAML, a cmd it doesn't recognise. That is deliberate and
// pre-existing — a resume that cold-starts loses continuity, which beats a 500
// that loses the respawn.
func spliceResume(specYAML, family, sessionID string) string {
	if specYAML == "" || sessionID == "" {
		return specYAML
	}
	tbl := resumerecipes.MustLoad()
	fam, ok := tbl.FamilyByName(family)
	if !ok {
		return specYAML
	}
	switch fam.Mechanism {
	case resumerecipes.MechanismArgv:
		e, ok := tbl.EngineByID(fam.Engine)
		if !ok {
			return specYAML
		}
		return spliceArgvResume(specYAML, e, sessionID)
	case resumerecipes.MechanismACPLoad, resumerecipes.MechanismAppServer:
		// The cursor rides the protocol, not the cmd: one top-level YAML
		// field that ACPDriver reads as session/load and AppServerDriver as
		// thread/resume.
		return spliceACPResume(specYAML, sessionID)
	default:
		return specYAML
	}
}

// spliceArgvResume rewrites `backend.cmd` so it carries the engine's own
// resume flag right after the binary token. Generalizes what used to be two
// near-identical functions (claude's `--resume`, agy's `--conversation`); the
// flag spelling and style now come from the recipe table.
func spliceArgvResume(specYAML string, e resumerecipes.Engine, sessionID string) string {
	// Validate before splicing. The id arrives verbatim from the engine's own
	// session.init payload and lands in a string tmux runs through a shell, so
	// it is untrusted data in a shell context. An id that fails the envelope
	// (control characters, over-length) is not quotable into safety — refuse it
	// and cold-start instead.
	ref, err := resumerecipes.NewID(sessionID)
	if err != nil {
		return specYAML
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(specYAML), &root); err != nil {
		return specYAML
	}
	cmdNode := findScalar(&root, "backend", "cmd")
	if cmdNode == nil {
		return specYAML
	}
	updated, ok := rewriteResumeFlag(cmdNode.Value, e, ref)
	if !ok || updated == cmdNode.Value {
		return specYAML
	}
	cmdNode.Value = updated
	out, err := yaml.Marshal(&root)
	if err != nil {
		return specYAML
	}
	return string(out)
}

// rewriteResumeFlag strips any prior resume flag for this engine and splices
// the current one directly after the binary token. Returns false when the cmd
// carries no recognisable invocation of that binary — we do not guess where to
// put flags in an unfamiliar command.
//
// The binary token is searched for rather than required first, because a cmd
// may legitimately lead with `cd <workdir> && <bin> …`.
func rewriteResumeFlag(cmd string, e resumerecipes.Engine, ref resumerecipes.SessionRef) (string, bool) {
	tokens := strings.Fields(strings.TrimSpace(cmd))
	binIdx := -1
	for i, t := range tokens {
		// `cd <dir>`'s operand is a directory, not an invocation — and a
		// workdir named after the engine (`cd ~/w/claude && claude …`) would
		// otherwise match on its path suffix and take the flag, breaking the
		// whole command (`cd` refuses extra arguments).
		if i > 0 && tokens[i-1] == "cd" {
			continue
		}
		if isBinToken(t, e.Bin) || (e.WindowsBin != "" && isBinToken(t, e.WindowsBin)) {
			binIdx = i
			break
		}
	}
	if binIdx < 0 {
		return cmd, false
	}
	// A subcommand-style recipe (`codex resume <id>`) is a different
	// invocation, not a flag added to this one — splicing a verb into a cmd
	// that already carries flags would produce something the engine rejects.
	// No family maps to argv with this style today; refuse rather than guess.
	if e.Style == resumerecipes.StyleSubcommand {
		return cmd, false
	}

	head := tokens[:binIdx+1]
	tail := make([]string, 0, len(tokens))
	skip := false
	for _, tok := range tokens[binIdx+1:] {
		if skip {
			skip = false
			continue
		}
		if tok == e.Token {
			skip = true
			continue
		}
		if strings.HasPrefix(tok, e.Token+"=") {
			continue
		}
		tail = append(tail, tok)
	}

	argv, err := e.Argv(ref, "linux")
	if err != nil {
		return cmd, false
	}
	// argv[0] is the bin, which the cmd already has (possibly as an absolute
	// path we must preserve). Take the flag tokens and shell-quote the parts
	// that need it — cmd is a shell string, not an argv slice.
	//
	// Capacity is a hint only (append grows past it), so it is len(tokens)
	// with no arithmetic: this function's input is attacker-influenced, and
	// arithmetic on its length is a size computation CodeQL flags as
	// overflowable. Nothing is bought by the exact figure.
	spliced := make([]string, 0, len(tokens))
	spliced = append(spliced, head...)
	for _, a := range argv[1:] {
		spliced = append(spliced, quoteResumeArg(a, e.Token))
	}
	spliced = append(spliced, tail...)
	return strings.Join(spliced, " "), true
}

// quoteResumeArg quotes the value half of a resume argument. For flag_pair the
// whole token is the value; for flag_equals the `--flag=` prefix must stay
// outside the quotes so the engine still parses it as that flag.
func quoteResumeArg(arg, token string) string {
	if value, ok := strings.CutPrefix(arg, token+"="); ok {
		return token + "=" + resumerecipes.ShellQuote(value)
	}
	return resumerecipes.ShellQuote(arg)
}

// isBinToken reports whether tok names the given binary — bare, or an
// absolute/relative path ending in it.
func isBinToken(tok, bin string) bool {
	if tok == bin {
		return true
	}
	if idx := strings.LastIndex(tok, "/"); idx >= 0 && tok[idx+1:] == bin {
		return true
	}
	return false
}

// spliceClaudeResume splices `--resume <id>` into backend.cmd for the
// claude-code family. ADR-014.
//
// Thin wrapper over the table-driven path: the flag spelling now comes from
// the `claude` recipe rather than from a literal here. Kept as a named
// function because its behaviour is pinned by tests that predate the table.
//
// Behaviour (unchanged):
//   - sessionID empty → spec unchanged.
//   - YAML parse fails or `backend.cmd` missing → spec unchanged. The resume
//     still proceeds (cold-start) — better than a 500.
//   - cmd already carries `--resume <sessionID>` → idempotent no-op.
//   - cmd carries a different `--resume <other>` → prior flag stripped.
//   - cmd names no `claude` binary → left alone.
//
// The function preserves comments and ordering on a best-effort basis via
// yaml.v3's Node API. yaml.v3's Marshal does normalize scalar quoting in some
// cases; that's acceptable here since the output only seeds the next
// agent_spawns row, never a human-edited template.
func spliceClaudeResume(specYAML, sessionID string) string {
	return spliceResume(specYAML, "claude-code", sessionID)
}

// rewriteClaudeResumeFlag is the cmd-level half of spliceClaudeResume, kept
// for the tests that exercise it directly.
func rewriteClaudeResumeFlag(cmd, sessionID string) (string, bool) {
	ref, err := resumerecipes.NewID(sessionID)
	if err != nil {
		return cmd, false
	}
	e, ok := resumerecipes.MustLoad().EngineByID("claude")
	if !ok {
		return cmd, false
	}
	return rewriteResumeFlag(cmd, e, ref)
}

// spliceACPResume injects (or replaces) a top-level `resume_session_id`
// scalar in the rendered spawn_spec_yaml. Two driver families consume
// the spliced field via the same `SpawnSpec.ResumeSessionID` accessor:
//
//   - ACPDriver (gemini-cli, kimi-code-ts): calls session/load with this
//     id instead of session/new when the agent advertises loadSession
//     capability (ADR-021 W1.2).
//   - AppServerDriver (codex): calls `thread/resume` with this id as
//     the `threadId` param instead of `thread/start` so codex
//     reattaches to its prior thread (v1.0.716).
//
// The function name kept its ACP-historical prefix; the operation is
// engine-neutral ("set top-level `resume_session_id`"). Renaming would
// touch every call site without changing behaviour, so we accept the
// slight naming drift.
//
// Behaviour mirrors spliceClaudeResume's defensive shape:
//   - sessionID empty → return spec unchanged.
//   - YAML parse fails → return spec unchanged. The resume still proceeds
//     (cold-start) — better than a 500.
//   - existing resume_session_id with the same value → idempotent no-op.
//   - existing resume_session_id with a different value → overwrite.
//   - field absent → append to the top-level mapping.
//
// Unlike claude's path we don't touch backend.cmd — ACP carries the
// cursor at the protocol level, not the cmd flag level.
func spliceACPResume(specYAML, sessionID string) string {
	if sessionID == "" {
		return specYAML
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(specYAML), &root); err != nil {
		return specYAML
	}
	doc := &root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		// Empty doc: synthesize a mapping with just the resume key.
		// Empty input is rare here (resume requires spawn_spec_yaml to
		// be set), but be defensive.
		doc.Kind = yaml.MappingNode
		doc.Tag = "!!map"
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		k := doc.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == "resume_session_id" {
			v := doc.Content[i+1]
			if v.Kind == yaml.ScalarNode && v.Value == sessionID {
				return specYAML
			}
			v.Kind = yaml.ScalarNode
			v.Tag = "!!str"
			v.Value = sessionID
			out, err := yaml.Marshal(&root)
			if err != nil {
				return specYAML
			}
			return string(out)
		}
	}
	doc.Content = append(doc.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "resume_session_id"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: sessionID},
	)
	out, err := yaml.Marshal(&root)
	if err != nil {
		return specYAML
	}
	return string(out)
}

// spliceAntigravityResume splices `--conversation <id>` into backend.cmd for
// the antigravity family (ADR-035 D8). agy resumes interactively via this
// flag; the headless `-p` form hangs, so the M4 launch path is the only one
// that uses it. Thin wrapper over the table-driven path, same as claude's.
func spliceAntigravityResume(specYAML, sessionID string) string {
	return spliceResume(specYAML, "antigravity", sessionID)
}

// findScalar walks a yaml document tree to a scalar node by following
// a sequence of mapping keys. Returns nil when any key is absent or
// the terminal node isn't a scalar — callers fall back to leaving the
// document untouched in that case.
func findScalar(root *yaml.Node, path ...string) *yaml.Node {
	cur := root
	if cur.Kind == yaml.DocumentNode && len(cur.Content) > 0 {
		cur = cur.Content[0]
	}
	for _, key := range path {
		if cur == nil || cur.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		// Mapping nodes store key/value as alternating Content entries.
		for i := 0; i+1 < len(cur.Content); i += 2 {
			k := cur.Content[i]
			if k.Kind == yaml.ScalarNode && k.Value == key {
				next = cur.Content[i+1]
				break
			}
		}
		cur = next
	}
	if cur == nil || cur.Kind != yaml.ScalarNode {
		return nil
	}
	return cur
}
