package server

// Artifact File Manifest V1 (AFM-V1) — Go mirror of the Dart
// `ArtifactFileManifest` defined in
// `lib/services/artifact_manifest/artifact_manifest.dart`.
//
// Schema locked in `docs/plans/canvas-viewer.md` (2026-05-11).
// `code-bundle` and `canvas-app` artifacts share this multi-file body.
// Mobile parser is the canonical implementation; this mirror exists so
// the demo seeder emits a typed struct rather than a hand-rolled
// `map[string]any`, and so the create handler can enforce a body cap.

// ArtifactBodyMaxBytes is the per-kind body cap for `code-bundle` and
// `canvas-app` artifacts (Q12 of docs/plans/canvas-viewer.md, locked
// 2026-05-11). Generous for SVG/D3 work, well under the global blob
// cap (`maxBlobBytes` = 25 MiB), fits the agent_events payload
// envelope without strain.
const ArtifactBodyMaxBytes = 10 * 1024 * 1024 // 10 MiB

// ArtifactFileManifestV1 is the wire shape for AFM-V1 bodies. The
// Files slice must contain at least one entry; consumers reject
// empty manifests.
type ArtifactFileManifestV1 struct {
	Version int              `json:"version"`
	Entry   string           `json:"entry,omitempty"`
	Files   []ArtifactFileV1 `json:"files"`
}

// ArtifactFileV1 is a single file inside an AFM-V1 body. Mime is
// optional in the wire form — when omitted, the consumer derives it
// from the path extension (see Dart `mimeForPath`).
type ArtifactFileV1 struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	MIME    string `json:"mime,omitempty"`
}

// artifactBodyCapped reports whether the given kind is subject to the
// per-kind 10 MiB body cap. Kept as a named helper so future kinds
// that adopt AFM-V1 (e.g., a future `notebook` artifact) can opt in
// without touching `handleCreateArtifact`.
func artifactBodyCapped(kind string) bool {
	switch kind {
	case "code-bundle", "canvas-app":
		return true
	}
	return false
}
