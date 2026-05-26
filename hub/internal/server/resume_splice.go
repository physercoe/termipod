package server

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// spliceClaudeResume rewrites the rendered spawn_spec_yaml so its
// `backend.cmd` carries `--resume <id>` immediately after the `claude`
// binary token, letting the spawned process reattach to its prior
// engine session. ADR-014.
//
// Behaviour:
//   - sessionID empty → return spec unchanged.
//   - YAML parse fails or `backend.cmd` missing → return spec
//     unchanged. The resume still proceeds (cold-start) — better than
//     a 500.
//   - cmd already contains `--resume <sessionID>` → idempotent no-op.
//   - cmd contains a different `--resume <other>` → strip the prior
//     flag (handles edge cases where the rendered cmd was carried over
//     from a debug template) and splice in the current one.
//   - cmd doesn't start with the token `claude` → leave it alone. We
//     only know how to splice claude-code's flag shape; other engines
//     should be wired through their own driver-side resume mechanism
//     (gemini's --resume, codex's thread/resume).
//
// The function preserves comments and ordering on a best-effort basis
// via yaml.v3's Node API. yaml.v3's Marshal does normalize scalar
// quoting in some cases; that's acceptable here since the output is
// only used to seed the next agent_spawns row, never round-tripped
// against a human-edited template.
func spliceClaudeResume(specYAML, sessionID string) string {
	if specYAML == "" || sessionID == "" {
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
	updated, ok := rewriteClaudeResumeFlag(cmdNode.Value, sessionID)
	if !ok {
		return specYAML
	}
	if updated == cmdNode.Value {
		return specYAML
	}
	cmdNode.Value = updated
	out, err := yaml.Marshal(&root)
	if err != nil {
		return specYAML
	}
	return string(out)
}

// rewriteClaudeResumeFlag returns the cmd string with `--resume
// <sessionID>` spliced in after the `claude` binary token, plus a
// boolean indicating whether the caller should use the result. The
// boolean is false when the cmd doesn't look like a claude invocation
// — we don't try to guess where to splice flags for unfamiliar shapes.
func rewriteClaudeResumeFlag(cmd, sessionID string) (string, bool) {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return cmd, false
	}
	tokens := strings.Fields(trimmed)
	if len(tokens) == 0 || !isClaudeBin(tokens[0]) {
		return cmd, false
	}
	// Strip any existing --resume <value> pair. The cmd we read from
	// `sessions.spawn_spec_yaml` is the original rendered template, so
	// in steady state this is a no-op; the strip is here for the rare
	// case where an operator hand-edited a template to bake one in.
	stripped := make([]string, 0, len(tokens)+2)
	stripped = append(stripped, tokens[0])
	skip := false
	for _, tok := range tokens[1:] {
		if skip {
			skip = false
			continue
		}
		if tok == "--resume" {
			skip = true
			continue
		}
		if strings.HasPrefix(tok, "--resume=") {
			continue
		}
		stripped = append(stripped, tok)
	}
	// Splice --resume <id> directly after the bin token.
	out := make([]string, 0, len(stripped)+2)
	out = append(out, stripped[0], "--resume", sessionID)
	out = append(out, stripped[1:]...)
	return strings.Join(out, " "), true
}

// spliceACPResume injects (or replaces) a top-level `resume_session_id`
// scalar in the rendered spawn_spec_yaml. Two driver families consume
// the spliced field via the same `SpawnSpec.ResumeSessionID` accessor:
//
//   - ACPDriver (gemini-cli, kimi-code): calls session/load with this
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

// spliceAntigravityResume rewrites backend.cmd so it carries
// `--conversation <id>` immediately after the `agy` binary token, letting
// the respawned process reattach to its prior conversation (ADR-035 D8).
// agy resumes interactively via this flag; the headless `-p` form hangs,
// so the M4 launch path (which drives agy interactively) is the only one
// that uses this. Defensive shape mirrors spliceClaudeResume.
func spliceAntigravityResume(specYAML, sessionID string) string {
	if specYAML == "" || sessionID == "" {
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
	updated, ok := rewriteAntigravityResumeFlag(cmdNode.Value, sessionID)
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

// rewriteAntigravityResumeFlag splices `--conversation <id>` after the
// `agy` bin token, stripping any prior --conversation pair. Returns false
// when the cmd doesn't look like an agy invocation (we don't guess where
// to splice flags for unfamiliar shapes). The cmd commonly leads with a
// `cd <workdir> && agy …` prefix, so we scan for the `agy` token rather
// than requiring it be first.
func rewriteAntigravityResumeFlag(cmd, sessionID string) (string, bool) {
	tokens := strings.Fields(strings.TrimSpace(cmd))
	binIdx := -1
	for i, t := range tokens {
		if isAgyBin(t) {
			binIdx = i
			break
		}
	}
	if binIdx < 0 {
		return cmd, false
	}
	// Strip any existing --conversation <value> / --conversation=<value>
	// that appears after the bin token.
	out := make([]string, 0, len(tokens)+2)
	out = append(out, tokens[:binIdx+1]...)
	skip := false
	for _, tok := range tokens[binIdx+1:] {
		if skip {
			skip = false
			continue
		}
		if tok == "--conversation" {
			skip = true
			continue
		}
		if strings.HasPrefix(tok, "--conversation=") {
			continue
		}
		out = append(out, tok)
	}
	// Splice --conversation <id> directly after the bin token.
	spliced := make([]string, 0, len(out)+2)
	spliced = append(spliced, out[:binIdx+1]...)
	spliced = append(spliced, "--conversation", sessionID)
	spliced = append(spliced, out[binIdx+1:]...)
	return strings.Join(spliced, " "), true
}

// isAgyBin returns true when tok names the Antigravity CLI — bare `agy`
// or an absolute path ending in `/agy`.
func isAgyBin(tok string) bool {
	if tok == "agy" {
		return true
	}
	if idx := strings.LastIndex(tok, "/"); idx >= 0 && tok[idx+1:] == "agy" {
		return true
	}
	return false
}

// isClaudeBin returns true when tok names the claude-code CLI. Allows
// either the bare `claude` or an absolute path ending in `/claude`,
// the two shapes templates ship today.
func isClaudeBin(tok string) bool {
	if tok == "claude" {
		return true
	}
	if idx := strings.LastIndex(tok, "/"); idx >= 0 && tok[idx+1:] == "claude" {
		return true
	}
	return false
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
