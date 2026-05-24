// apply_template_install.go — ADR-030 W8 propose apply function for
// the `template.install` governed-action kind. Wraps the existing
// installProposedTemplate so the same path handles both:
//
//   - propose(kind="template.install", ...) — the new ADR-030 verb.
//   - The legacy `template_proposal` path. The W8 decide-handler
//     refactor routes this through the same Apply with
//     ProposeApplyContext.Via = "alias_legacy" — same backward-compat
//     story as apply_agent_spawn.go.
//
// `change_spec` is the {category, name, blob_sha256, rationale?,
// proposed_by?} payload installProposedTemplate already understands;
// no shape translation needed. `target_ref` is cosmetic — template
// install operates above project scope per pre-W1 decision #4's
// no-project-id case.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

func init() {
	RegisterProposeKind(ProposeKind{
		Kind:     "template.install",
		Validate: validateTemplateInstall,
		DryRun:   dryRunTemplateInstall,
		Apply:    applyTemplateInstall,
		Rollback: rollbackTemplateInstall,
	})
}

type templateInstallSpec struct {
	Category   string `json:"category"`
	Name       string `json:"name"`
	BlobSHA256 string `json:"blob_sha256"`
	Rationale  string `json:"rationale,omitempty"`
	ProposedBy string `json:"proposed_by,omitempty"`
}

func parseTemplateInstall(changeSpec json.RawMessage) (templateInstallSpec, error) {
	var p templateInstallSpec
	if len(changeSpec) == 0 {
		return p, errors.New("change_spec required")
	}
	if err := json.Unmarshal(changeSpec, &p); err != nil {
		return p, fmt.Errorf("change_spec: %w", err)
	}
	if p.Category == "" {
		return p, errors.New("change_spec.category required")
	}
	if p.Name == "" {
		return p, errors.New("change_spec.name required")
	}
	if p.BlobSHA256 == "" {
		return p, errors.New("change_spec.blob_sha256 required")
	}
	return p, nil
}

// validateTemplateInstall is a pure shape check — blob presence is
// verified at Apply time when we know the data root.
func validateTemplateInstall(_ context.Context, _ *Server, _, changeSpec json.RawMessage) error {
	_, err := parseTemplateInstall(changeSpec)
	return err
}

// dryRunTemplateInstall stat's the blob so the preview can show
// the body size (useful for "review the diff size before
// approving"). Returns blob-missing in the preview rather than
// erroring; that's a soft signal the proposer can act on.
func dryRunTemplateInstall(_ context.Context, s *Server, _, changeSpec json.RawMessage) (json.RawMessage, error) {
	p, err := parseTemplateInstall(changeSpec)
	if err != nil {
		return nil, err
	}
	preview := map[string]any{
		"category":    p.Category,
		"name":        p.Name,
		"blob_sha256": p.BlobSHA256,
	}
	if info, err := os.Stat(s.blobPath(p.BlobSHA256)); err == nil {
		preview["blob_bytes"] = info.Size()
		preview["blob_present"] = true
	} else {
		preview["blob_present"] = false
	}
	return json.Marshal(preview)
}

// applyTemplateInstall delegates to installProposedTemplate (which
// reads the blob + writes the file) and emits a template.install
// audit row with the propose lineage on meta. The legacy path
// (template_proposal kind) emitted no separate audit row — the
// install was implied by `attention.decide`. Adding one here is a
// regression of behaviour the activity feed renderer was tolerant
// of; the audit-meta `via` discriminator lets us subscribe new
// renderers to the install event without confusing the legacy
// timeline.
func applyTemplateInstall(
	ctx context.Context, s *Server, ac ProposeApplyContext, _, changeSpec json.RawMessage,
) (json.RawMessage, error) {
	p, err := parseTemplateInstall(changeSpec)
	if err != nil {
		return nil, err
	}
	team := ac.Team
	if team == "" {
		return nil, errors.New("template.install: apply context missing team")
	}
	// installProposedTemplate parses its own JSON, so we re-marshal
	// the parsed struct rather than passing the raw change_spec —
	// that way unrecognised fields in the propose-side spec don't
	// confuse the installer.
	installerPayload, _ := json.Marshal(map[string]any{
		"category":    p.Category,
		"name":        p.Name,
		"blob_sha256": p.BlobSHA256,
	})
	installed, err := s.installProposedTemplate(string(installerPayload))
	if err != nil {
		return nil, fmt.Errorf("template.install: %w", err)
	}

	via := ac.ViaOrDefault()
	meta := map[string]any{
		"category":    p.Category,
		"name":        p.Name,
		"blob_sha256": p.BlobSHA256,
		"via":         via,
		"by_tier":     ac.AssignedTier,
		"propose_id":  ac.AttentionID,
	}
	if ac.DeciderHandle != "" {
		meta["by_actor"] = ac.DeciderHandle
	}
	if p.Rationale != "" {
		meta["rationale"] = p.Rationale
	}
	if p.ProposedBy != "" {
		meta["proposed_by"] = p.ProposedBy
	}
	s.recordAudit(ctx, team, "template.install", "template",
		p.Category+"/"+p.Name,
		fmt.Sprintf("install %s/%s via %s", p.Category, p.Name, via), meta)

	// `installed` is already a JSON-encoded {kind, category, name,
	// path, bytes}. Return it verbatim as the executed payload.
	return installed, nil
}

// rollbackTemplateInstall deletes the installed file. The MVP
// scope does NOT restore a prior version even if one existed in
// the blob store (the apply path doesn't capture it, and a backup
// store would change the apply contract). The original blob stays
// in the blob store so a future re-propose with the same sha256
// works.
//
// `originalExecuted` carries the absolute path the installer
// wrote — that's our target. The category/name come from the
// originalSpec so the audit row carries the right target_id.
func rollbackTemplateInstall(
	ctx context.Context, s *Server, ac ProposeApplyContext, originalSpec, originalExecuted json.RawMessage,
) (json.RawMessage, error) {
	var origExec struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(originalExecuted, &origExec); err != nil {
		return nil, fmt.Errorf("rollback: parse original_executed: %w", err)
	}
	if origExec.Path == "" {
		return nil, errors.New("rollback: original_executed missing path")
	}
	var origSpec templateInstallSpec
	if err := json.Unmarshal(originalSpec, &origSpec); err != nil {
		return nil, fmt.Errorf("rollback: parse original_spec: %w", err)
	}
	team := ac.Team
	if team == "" {
		return nil, errors.New("template.install rollback: apply context missing team")
	}
	removed := true
	if err := os.Remove(origExec.Path); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("rollback remove %s: %w", origExec.Path, err)
		}
		// File already gone — treat as successful rollback; the
		// audit row notes it.
		removed = false
	}

	via := ac.ViaOrDefault()
	meta := map[string]any{
		"category":   origSpec.Category,
		"name":       origSpec.Name,
		"path":       origExec.Path,
		"removed":    removed,
		"via":        via,
		"by_tier":    ac.AssignedTier,
		"propose_id": ac.AttentionID,
		"rollback":   true,
	}
	if ac.DeciderHandle != "" {
		meta["by_actor"] = ac.DeciderHandle
	}
	s.recordAudit(ctx, team, "template.uninstall", "template",
		origSpec.Category+"/"+origSpec.Name,
		fmt.Sprintf("uninstall %s/%s (rollback)", origSpec.Category, origSpec.Name),
		meta)
	return json.Marshal(map[string]any{
		"kind":     "template_uninstall",
		"category": origSpec.Category,
		"name":     origSpec.Name,
		"path":     origExec.Path,
		"removed":  removed,
		"rollback": true,
	})
}
