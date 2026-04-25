package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	hub "github.com/termipod/hub"
	"gopkg.in/yaml.v3"
)

// Template rendering for spawn specs. We interpolate a small, fixed set of
// variables so spawn YAML can reference state that only exists at spawn
// time — most notably the parent agent's journal and the principal handle
// requesting the spawn. Rendering is pre-YAML: the result is then persisted
// and handed to host-runner verbatim.
//
// Variable shape: {{name}}. We intentionally do not support expressions,
// filters, or control flow — spawn specs should stay declarative. If a var
// is not defined for a given spawn (e.g. {{journal}} with no parent), it
// expands to an empty string rather than leaving the placeholder behind;
// that way the rendered YAML is always parseable even when data is missing.
//
// Security: only data already visible to the caller feeds these variables.
// {{journal}} reads from the parent agent's markdown file — the parent is
// owned by the same team as the spawner, so no cross-team leak. We never
// expand tokens, passwords, or anything from flutter_secure_storage; those
// don't live on the hub.

// Names may contain dots so prompts can reference structured-looking
// identifiers like {{principal.handle}}. The hub does not implement true
// nested lookups — the dotted form is just a different key — but it lets
// templates read more naturally.
var tmplVarRe = regexp.MustCompile(`\{\{\s*([a-z_][a-z0-9_.]*)\s*\}\}`)

// buildSpawnVars assembles the variable map shared by renderSpawnSpec and
// resolveContextFiles so the same {{handle}}/{{principal.handle}} bindings
// expand to the same values in both the spec YAML and the prompt body.
func (s *Server) buildSpawnVars(ctx context.Context, team string, in spawnIn, principal string) (map[string]string, error) {
	resolvedPrincipal := firstNonEmpty(principal, "@principal")
	vars := map[string]string{
		"handle":           in.ChildHandle,
		"kind":             in.Kind,
		"team":             team,
		"now":              time.Now().UTC().Format(time.RFC3339),
		"principal":        resolvedPrincipal,
		"principal.handle": strings.TrimPrefix(resolvedPrincipal, "@"),
	}
	if in.ParentID != "" {
		parentHandle, journal, err := s.lookupParentContext(ctx, team, in.ParentID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		vars["parent_handle"] = parentHandle
		vars["journal"] = journal
	}
	return vars, nil
}

// expandVars replaces every {{name}} occurrence in s with vars[name];
// missing keys collapse to the empty string so the output stays parseable.
func expandVars(s string, vars map[string]string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	return tmplVarRe.ReplaceAllStringFunc(s, func(match string) string {
		m := tmplVarRe.FindStringSubmatch(match)
		if m == nil {
			return match
		}
		return vars[m[1]]
	})
}

// renderSpawnSpec expands {{var}} placeholders in spec using values derived
// from the Server state + the authenticated request. The returned string is
// ready to persist as spawn_spec_yaml.
//
// Context is used for DB reads (journal lookup); on any sub-lookup failure we
// substitute the empty string and keep going — partial data beats a spawn
// hard-failing because the parent happens to have no journal yet.
func (s *Server) renderSpawnSpec(ctx context.Context, team string, in spawnIn, principal string) (string, error) {
	if !strings.Contains(in.SpawnSpec, "{{") {
		return in.SpawnSpec, nil
	}
	vars, err := s.buildSpawnVars(ctx, team, in, principal)
	if err != nil {
		return "", err
	}
	return expandVars(in.SpawnSpec, vars), nil
}

// resolveContextFiles loads the prompt file referenced by a rendered spawn
// spec (`prompt: <filename>`), expands {{var}} placeholders in it using the
// same bindings as the spec, and inlines the result into the spec under
// `context_files.CLAUDE.md`. The host-runner launcher writes context_files
// entries to the agent's workdir before launch — that's how Claude Code
// (and any other agent that reads CLAUDE.md on startup) sees the steward
// persona without the hub having to copy files around itself.
//
// personaSeed is a user-supplied addendum (typed into the mobile bootstrap
// sheet) appended under a "Persona override" section so the agent picks
// up both the templated body and the operator's customization. Empty
// seed = no addendum.
//
// Behaviour:
//   - No `prompt:` field, no override, no seed → return unchanged.
//   - Spec already declares `context_files.CLAUDE.md` → respect it (an
//     explicit override beats the templated default). The seed is
//     ignored in this branch — explicit override means the operator
//     wrote CLAUDE.md by hand and shouldn't get surprise concatenation.
//   - Otherwise: read the prompt body (from disk overlay → embedded FS),
//     expand vars, optionally append the seed, and emit a context_files
//     block.
//   - Prompt file not found on disk overlay or embedded FS → error.
func (s *Server) resolveContextFiles(rendered string, vars map[string]string, personaSeed string) (string, error) {
	var head struct {
		Prompt       string            `yaml:"prompt"`
		ContextFiles map[string]string `yaml:"context_files"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &head); err != nil {
		// Spec isn't shaped like our header — leave it alone rather than
		// 400'ing a spawn that may parse fine on the host-runner side.
		return rendered, nil
	}
	if _, hasOverride := head.ContextFiles["CLAUDE.md"]; hasOverride {
		return rendered, nil
	}
	hasSeed := strings.TrimSpace(personaSeed) != ""
	if head.Prompt == "" && !hasSeed {
		return rendered, nil
	}

	var body string
	if head.Prompt != "" {
		raw, err := s.readPromptTemplate(head.Prompt)
		if err != nil {
			return "", fmt.Errorf("read prompt %q: %w", head.Prompt, err)
		}
		body = expandVars(raw, vars)
	}
	if hasSeed {
		body = appendPersonaSeed(body, personaSeed)
	}

	extra, err := yaml.Marshal(map[string]any{
		"context_files": map[string]string{"CLAUDE.md": body},
	})
	if err != nil {
		return "", err
	}
	sep := "\n"
	if strings.HasSuffix(rendered, "\n") || rendered == "" {
		sep = ""
	}
	return rendered + sep + string(extra), nil
}

// appendPersonaSeed concatenates the operator's seed onto the templated
// CLAUDE.md under a clearly-labeled section so the agent (and a human
// reading the materialized file) can tell the two apart.
func appendPersonaSeed(body, seed string) string {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return body
	}
	const header = "## Persona override"
	if body == "" {
		return header + "\n\n" + seed + "\n"
	}
	sep := "\n\n"
	if strings.HasSuffix(body, "\n\n") {
		sep = ""
	} else if strings.HasSuffix(body, "\n") {
		sep = "\n"
	}
	return body + sep + header + "\n\n" + seed + "\n"
}

// readPromptTemplate prefers <dataRoot>/team/templates/prompts/<name> so
// user edits win, then falls back to the embedded built-in.
func (s *Server) readPromptTemplate(name string) (string, error) {
	if !safePromptName(name) {
		return "", fmt.Errorf("invalid prompt name %q", name)
	}
	path := filepath.Join(s.cfg.DataRoot, "team", "templates", "prompts", name)
	data, err := os.ReadFile(path)
	if err == nil {
		return string(data), nil
	}
	if !errors.Is(err, fs.ErrNotExist) && !os.IsNotExist(err) {
		return "", err
	}
	embedded, err := fs.ReadFile(hub.TemplatesFS, "templates/prompts/"+name)
	if err != nil {
		return "", err
	}
	return string(embedded), nil
}

func safePromptName(n string) bool {
	if n == "" || strings.ContainsAny(n, `/\`) || strings.HasPrefix(n, ".") {
		return false
	}
	return true
}

// lookupParentContext returns (handle, journalContent). Missing journal file
// is reported as (handle, "", nil) rather than an error — a parent without
// notes is common early in a task.
func (s *Server) lookupParentContext(ctx context.Context, team, parentID string) (string, string, error) {
	var handle string
	err := s.db.QueryRowContext(ctx,
		`SELECT handle FROM agents WHERE team_id = ? AND id = ?`, team, parentID).Scan(&handle)
	if err != nil {
		return "", "", err
	}
	path, err := s.journalPath(team, handle)
	if err != nil {
		return handle, "", nil
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return handle, "", nil
	}
	if err != nil {
		return handle, "", err
	}
	return handle, string(body), nil
}

// principalFromScope pulls the caller handle out of an auth token's scope
// JSON. Tokens issued with -handle carry scope.handle (e.g. "physercoe"); we
// prefer that so templates rendering {{principal}} point at a real human.
// Older tokens fall back to `@role` (e.g. `@principal`, `@host`).
func principalFromScope(scopeJSON string) string {
	if scopeJSON == "" {
		return "@principal"
	}
	var s struct {
		Role   string `json:"role"`
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal([]byte(scopeJSON), &s); err != nil {
		return "@principal"
	}
	if s.Handle != "" {
		return "@" + s.Handle
	}
	if s.Role != "" {
		return "@" + s.Role
	}
	return "@principal"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
