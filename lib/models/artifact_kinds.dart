import 'package:flutter/material.dart';

/// Closed artifact-kind registry (wave 2 W1 of the artifact-type-registry
/// plan). Mirrors `hub/internal/server/artifact_kinds.go` — the hub is
/// authoritative for which kinds round-trip; this enum exists so the
/// mobile UI has a typed slug to dispatch viewers, filter pills, and
/// chip colours against.
///
/// To add a kind, extend both this enum AND the Go-side
/// `validArtifactKinds` map. The plan at
/// `docs/plans/artifact-type-registry.md` lists the MVP 11 and the
/// industry-grounding triangulation that earned each one inclusion.
enum ArtifactKind {
  proseDocument('prose-document'),
  codeBundle('code-bundle'),
  tabular('tabular'),
  image('image'),
  audio('audio'),
  video('video'),
  pdf('pdf'),
  diagram('diagram'),
  canvasApp('canvas-app'),
  externalBlob('external-blob'),
  metricChart('metric-chart');

  final String slug;
  const ArtifactKind(this.slug);

  static ArtifactKind? fromSlug(String? slug) {
    if (slug == null || slug.isEmpty) return null;
    for (final k in ArtifactKind.values) {
      if (k.slug == slug) return k;
    }
    return null;
  }
}

/// Pre-W1 free-form kind strings the hub used to accept. Kept in sync
/// with `backfillLegacyArtifactKind` so the mobile UI maps cached
/// rows the same way the hub remaps create calls. Treats anything
/// outside the closed set as `externalBlob` once mapping fails.
const Map<String, ArtifactKind> kLegacyArtifactKindAliases = {
  'checkpoint': ArtifactKind.externalBlob,
  'dataset': ArtifactKind.externalBlob,
  'other': ArtifactKind.externalBlob,
  'eval_curve': ArtifactKind.metricChart,
  'log': ArtifactKind.proseDocument,
  'report': ArtifactKind.proseDocument,
  'figure': ArtifactKind.image,
  'sample': ArtifactKind.image,
};

/// Per-kind UI spec — what to label the chip with, which icon to pick,
/// and which MIME hint to send when the mobile UI itself creates an
/// artifact (e.g. attaching an image from the composer). The colour
/// roles map onto DesignColors at the call site so dark/light themes
/// both work without rewiring the spec table.
class ArtifactKindSpec {
  final ArtifactKind kind;
  final String label;
  final IconData icon;
  final String? mimeHint;
  final String colorRole; // 'primary' | 'cyan' | 'green' | 'magenta'…

  const ArtifactKindSpec({
    required this.kind,
    required this.label,
    required this.icon,
    required this.colorRole,
    this.mimeHint,
  });
}

const Map<ArtifactKind, ArtifactKindSpec> kArtifactKindSpecs = {
  ArtifactKind.proseDocument: ArtifactKindSpec(
    kind: ArtifactKind.proseDocument,
    label: 'prose',
    icon: Icons.notes,
    colorRole: 'primary',
    mimeHint: 'text/markdown',
  ),
  ArtifactKind.codeBundle: ArtifactKindSpec(
    kind: ArtifactKind.codeBundle,
    label: 'code',
    icon: Icons.code,
    colorRole: 'cyan',
    mimeHint: 'application/vnd.termipod.code+zip',
  ),
  ArtifactKind.tabular: ArtifactKindSpec(
    kind: ArtifactKind.tabular,
    label: 'table',
    icon: Icons.table_chart_outlined,
    colorRole: 'green',
    mimeHint: 'application/json',
  ),
  ArtifactKind.image: ArtifactKindSpec(
    kind: ArtifactKind.image,
    label: 'image',
    icon: Icons.image_outlined,
    colorRole: 'magenta',
    mimeHint: 'image/png',
  ),
  ArtifactKind.audio: ArtifactKindSpec(
    kind: ArtifactKind.audio,
    label: 'audio',
    icon: Icons.audiotrack,
    colorRole: 'orange',
    mimeHint: 'audio/mp3',
  ),
  ArtifactKind.video: ArtifactKindSpec(
    kind: ArtifactKind.video,
    label: 'video',
    icon: Icons.movie_outlined,
    colorRole: 'red',
    mimeHint: 'video/mp4',
  ),
  ArtifactKind.pdf: ArtifactKindSpec(
    kind: ArtifactKind.pdf,
    label: 'pdf',
    icon: Icons.picture_as_pdf_outlined,
    colorRole: 'primary',
    mimeHint: 'application/pdf',
  ),
  ArtifactKind.diagram: ArtifactKindSpec(
    kind: ArtifactKind.diagram,
    label: 'diagram',
    icon: Icons.account_tree_outlined,
    colorRole: 'cyan',
    mimeHint: 'image/svg+xml',
  ),
  ArtifactKind.canvasApp: ArtifactKindSpec(
    kind: ArtifactKind.canvasApp,
    label: 'canvas',
    icon: Icons.web_asset_outlined,
    colorRole: 'magenta',
    mimeHint: 'text/html',
  ),
  ArtifactKind.externalBlob: ArtifactKindSpec(
    kind: ArtifactKind.externalBlob,
    label: 'blob',
    icon: Icons.link,
    colorRole: 'muted',
  ),
  ArtifactKind.metricChart: ArtifactKindSpec(
    kind: ArtifactKind.metricChart,
    label: 'chart',
    icon: Icons.show_chart,
    colorRole: 'cyan',
    mimeHint: 'application/vnd.termipod.metrics+json',
  ),
};

/// Resolve a raw kind slug (which may be a legacy alias or empty) to its
/// spec. Falls back to `externalBlob` so the UI always has something to
/// render rather than a `?` chip.
ArtifactKindSpec artifactKindSpecFor(String? slug) {
  final direct = ArtifactKind.fromSlug(slug);
  if (direct != null) return kArtifactKindSpecs[direct]!;
  final legacy = slug == null ? null : kLegacyArtifactKindAliases[slug];
  if (legacy != null) return kArtifactKindSpecs[legacy]!;
  return kArtifactKindSpecs[ArtifactKind.externalBlob]!;
}
