package server

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// mutateBackendCmdFlag rewrites the `backend.cmd` string inside a
// rendered spawn_spec_yaml so the named flag's argument is replaced.
// Used by ADR-021 W2.3's respawn-with-spec-mutation flow to flip
// `--model X` to `--model Y` (or `--permission-mode X` to Y) without
// re-rendering the template — the rendered spec already has all
// substitutions resolved, so a string-edit is both faster and safer
// than a re-render with mutated vars (which could change unrelated
// fields).
//
// Behaviour:
//   - Locate the top-level `backend` mapping → its `cmd` scalar.
//   - Tokenise cmd on whitespace (cheap shell-words approximation —
//     the spawn cmds we generate don't quote arguments).
//   - Find the first occurrence of `--<flag>` and replace the *next*
//     token. If the flag isn't present, return errFlagNotInCmd so the
//     caller can decide between "fail closed" (current) and a future
//     "append flag if missing" extension.
//   - Re-emit the YAML with the cmd field rewritten; all other fields
//     are preserved verbatim (including comments, ordering — yaml.v3
//     Node API is whitespace-sensitive).
//
// Why string-edit not template-re-render: re-rendering would re-evaluate
// every {{var}} in the source template, changing fields the user didn't
// touch (e.g. {{handle}} re-resolves through different placeholder
// state). The string-edit is surgical: only `--<flag>` and the next
// token change, period.
func mutateBackendCmdFlag(specYAML, flag, newValue string) (string, error) {
	if flag == "" {
		return "", errors.New("mutator: flag required")
	}
	if newValue == "" {
		return "", errors.New("mutator: newValue required")
	}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(specYAML), &doc); err != nil {
		return "", fmt.Errorf("mutator: parse spec yaml: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return "", errors.New("mutator: spec yaml has no document node")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", errors.New("mutator: spec yaml root is not a mapping")
	}

	backend := findMappingChild(root, "backend")
	if backend == nil {
		return "", errors.New("mutator: spec missing backend mapping")
	}
	cmdNode := findMappingChild(backend, "cmd")
	if cmdNode == nil {
		return "", errors.New("mutator: spec missing backend.cmd")
	}
	if cmdNode.Kind != yaml.ScalarNode {
		return "", errors.New("mutator: backend.cmd is not a scalar")
	}

	rewritten, err := replaceFlagArgument(cmdNode.Value, flag, newValue)
	if err != nil {
		return "", err
	}
	cmdNode.Value = rewritten
	cmdNode.Style = 0 // let the encoder pick the cleanest scalar style

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", fmt.Errorf("mutator: marshal: %w", err)
	}
	return string(out), nil
}

// errFlagNotInCmd is returned when the requested flag isn't present in
// backend.cmd — the caller should not silently proceed because the
// resulting respawn would be a no-op from the user's perspective.
var errFlagNotInCmd = errors.New("mutator: flag not present in backend.cmd")

// replaceFlagArgument finds `--<flag>` in cmd and replaces the next
// token. cmd is treated as whitespace-separated tokens; quoting and
// escaping are not honored because our generated cmds don't use them.
// If the flag is the final token (no argument follows), the
// replacement still appends one — not currently needed but
// forward-compatible with `--<flag>=<value>` style is *not* handled
// (we'd need to detect '=' explicitly).
func replaceFlagArgument(cmd, flag, newValue string) (string, error) {
	tokens := strings.Fields(cmd)
	flagTok := "--" + flag
	for i, tok := range tokens {
		if tok != flagTok {
			continue
		}
		if i+1 >= len(tokens) {
			tokens = append(tokens, newValue)
		} else {
			tokens[i+1] = newValue
		}
		return strings.Join(tokens, " "), nil
	}
	return "", errFlagNotInCmd
}

// findMappingChild looks up a key in a yaml.Node mapping and returns
// the *value* node (or nil if absent). yaml.v3's Node API stores
// mappings as a flat []Node where even indices are keys and odd
// indices are values.
func findMappingChild(parent *yaml.Node, key string) *yaml.Node {
	if parent.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		k := parent.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return parent.Content[i+1]
		}
	}
	return nil
}
