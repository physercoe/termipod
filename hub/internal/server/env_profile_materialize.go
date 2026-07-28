package server

import (
	"context"
	"sort"

	"gopkg.in/yaml.v3"
)

// env_profile_materialize.go — hub side of the env-profiles plan (E1). When a
// spawn attaches an env_profiles row, DoSpawn resolves it and splices the
// profile's plain env_vars (and a provenance id) into the rendered
// spawn_spec_yaml so host-runner exports them before the agent cmd.
//
// The values are copied in at spawn time — snapshot semantics, no revision
// machinery: a later edit to the env_profiles row never mutates this (or a
// running) agent's environment. env_vars + setup_script are hub-visible by
// design (blueprint §4); secret_refs are NOT materialized here (they stay in
// the zero-knowledge vault — host-key envelopes land in E3).

// materializeEnvProfile injects the profile's snapshot into specYAML as
// top-level keys — `env_profile_id` (provenance), the `env_vars` mapping, and
// (when set) `setup_script` + `setup_failure_policy` — following the defensive
// yaml.Node idiom of spliceACPResume: a parse failure returns the spec
// unchanged rather than failing the spawn (better a spawn without the profile
// than a 500). Existing keys of the same name are replaced (idempotent
// re-materialize). Empty env_vars / setup_script are omitted so the spec stays
// clean. secret_refs are NOT materialized — they stay in the vault (E3).
func materializeEnvProfile(specYAML string, prof envProfileOut) string {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(specYAML), &root); err != nil {
		return specYAML
	}
	doc := &root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		// Empty / non-mapping spec: synthesize a mapping so the keys have a
		// home. Rare (DoSpawn requires a rendered backend.cmd), but defensive.
		doc.Kind = yaml.MappingNode
		doc.Tag = "!!map"
		doc.Content = nil
	}
	if prof.ID != "" {
		upsertTopKey(doc, "env_profile_id",
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: prof.ID})
	}
	if len(prof.EnvVars) > 0 {
		upsertTopKey(doc, "env_vars", envVarsMapNode(prof.EnvVars))
	}
	if prof.SetupScript != "" {
		upsertTopKey(doc, "setup_script",
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: prof.SetupScript})
		upsertTopKey(doc, "setup_failure_policy",
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: normalizeFailurePolicy(prof.SetupFailurePolicy)})
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return specYAML
	}
	return string(out)
}

// topEnvProfileIDFromYAML parses a yaml doc's top-level `env_profile_id`. Two
// callers share it: a project config_yaml (E2 project-level inherit) and a
// session's spawn_spec_yaml (materializeEnvProfile splices the same key on an
// explicit-profile spawn — the desktop teleport re-seal flow reads it back).
// Empty doc, parse failure, or absent key all yield "".
func topEnvProfileIDFromYAML(configYAML string) string {
	if configYAML == "" {
		return ""
	}
	var doc struct {
		EnvProfileID string `yaml:"env_profile_id"`
	}
	if yaml.Unmarshal([]byte(configYAML), &doc) != nil {
		return ""
	}
	return doc.EnvProfileID
}

// projectEnvProfileID reads a project's config_yaml and returns its declared
// env_profile_id (the profile all spawns in the project inherit unless they set
// their own). A missing project or DB error yields "" — inheritance is
// best-effort and must never fail a spawn.
func (s *Server) projectEnvProfileID(ctx context.Context, projectID string) string {
	var cfg string
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(config_yaml, '') FROM projects WHERE id = ?`, projectID).Scan(&cfg); err != nil {
		return ""
	}
	return topEnvProfileIDFromYAML(cfg)
}

// upsertTopKey sets key→val on a mapping node, replacing an existing pair or
// appending a new one at the end (preserving the order of untouched keys).
func upsertTopKey(doc *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Kind == yaml.ScalarNode && doc.Content[i].Value == key {
			doc.Content[i+1] = val
			return
		}
	}
	doc.Content = append(doc.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		val,
	)
}

// envVarsMapNode builds a block mapping of the env vars with keys in sorted
// order so the rendered spec is deterministic (stable across re-renders and in
// tests). Keys + values are emitted as plain strings; host-runner's
// envExportPrefix shell-escapes the values and re-validates the names.
func envVarsMapNode(env map[string]string) *yaml.Node {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, k := range keys {
		m.Content = append(m.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: env[k]},
		)
	}
	return m
}
