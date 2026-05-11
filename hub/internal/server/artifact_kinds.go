package server

// Closed artifact-kind registry — wave 2 W1 of the artifact-type-registry
// plan. Grounds against Claude Artifacts, ChatGPT Canvas, MCP content,
// Notion blocks, Jupyter MIME bundles, and Cursor file-set bundles.
//
// Each kind earns inclusion if ≥3 of those sources have a direct analog.
// The 11-entry set is the MVP — see docs/plans/artifact-type-registry.md
// for the triangulation table and per-kind viewer/editor/agent-IO matrix.
//
// Validation lives in Go (not a CHECK constraint) so new kinds don't
// require a forward migration — Q3 resolved 2026-05-11. Migration 0039
// is documentation-only + a backfill pass for legacy free-form kinds.

// validArtifactKinds is the closed set accepted by the create handler.
// Adding a kind here:
//
//  1. extend `lib/models/artifact_kinds.dart` so the mobile UI has a
//     matching spec entry (label, icon, mime hint)
//  2. mention it in docs/plans/artifact-type-registry.md if MVP-relevant
//  3. update the round-trip test in artifact_kinds_test.go
var validArtifactKinds = map[string]bool{
	"prose-document": true,
	"code-bundle":    true,
	"tabular":        true,
	"image":          true,
	"audio":          true,
	"video":          true,
	"pdf":            true,
	"diagram":        true,
	"canvas-app":     true,
	"external-blob":  true,
	"metric-chart":   true,
}

// backfillLegacyArtifactKind maps a free-form pre-W1 kind string to the
// closed MVP set. Used by migration 0039's UPDATE pass and by the create
// handler when a caller still sends a legacy name (transitional grace —
// remove after a tester cycle).
//
// The mapping is intentionally lossy: `dataset` and `checkpoint` both
// land at `external-blob`, but the original string is preserved in the
// audit log so forensic queries can reconstruct intent.
func backfillLegacyArtifactKind(legacy string) (string, bool) {
	switch legacy {
	case "checkpoint", "dataset", "other":
		return "external-blob", true
	case "eval_curve":
		return "metric-chart", true
	case "log":
		return "prose-document", true
	case "report":
		return "prose-document", true
	case "figure":
		return "image", true
	case "sample":
		// Multi-modal in the legacy set — `image` is the safest landing
		// because the seed data only emitted image samples. Audio/video
		// samples were never produced in practice.
		return "image", true
	}
	return "", false
}
