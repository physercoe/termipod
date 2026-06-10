import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../l10n/app_localizations.dart';
import '../../providers/hub_provider.dart';
import '../../providers/vocab_provider.dart';
import '../../services/hub/entity_names.dart';
import '../../services/vocab/vocab_axis.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';
import '../../widgets/app_chip.dart';
import '../../widgets/hub_offline_banner.dart';

/// Human-review queue (blueprint §6.8, P2.x).
///
/// Reviews attach to documents (and eventually artifacts) and sit in one of
/// pending / approved / rejected / needs_changes. Directors land here to
/// clear their queue — default filter is `pending` so the list shows the
/// work that actually needs a decision.
class ReviewsScreen extends ConsumerStatefulWidget {
  /// Optional project scope. When null, shows all team reviews.
  final String? projectId;
  const ReviewsScreen({super.key, this.projectId});

  @override
  ConsumerState<ReviewsScreen> createState() => _ReviewsScreenState();
}

class _ReviewsScreenState extends ConsumerState<ReviewsScreen> {
  // null = all statuses; otherwise one of the known review states.
  String? _filter = 'pending';
  String? _projectFilter;
  List<Map<String, dynamic>>? _rows;
  List<Map<String, dynamic>>? _projects;
  // Documents list used solely for resolving review.document_id → title.
  // Hub returns reviews as target-kind+target-id joins (no title column);
  // pairing with listDocumentsCached on the same scope keeps rows
  // human-readable without a server-side join.
  List<Map<String, dynamic>> _docs = const [];
  bool _loading = true;
  String? _error;
  bool _hubMissing = false;
  DateTime? _staleSince;

  static const _filters = <String?>[
    'pending',
    'needs_changes',
    'approved',
    'rejected',
    null,
  ];

  bool get _showProjectFilter => widget.projectId == null;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _hubMissing = true;
      });
      return;
    }
    try {
      final effectiveProject = widget.projectId ?? _projectFilter;
      final reviewsFuture = client.listReviewsCached(
        status: _filter,
        projectId: effectiveProject,
      );
      final docsFuture =
          client.listDocumentsCached(projectId: effectiveProject);
      final projectsFuture = (_showProjectFilter && _projects == null)
          ? client.listProjects()
          : null;
      final cached = await reviewsFuture;
      final rows = cached.body;
      _staleSince = cached.staleSince;
      final docsCached = await docsFuture;
      _docs = docsCached.body;
      if (projectsFuture != null) {
        _projects = await projectsFuture;
      }
      rows.sort((a, b) => (b['created_at'] ?? '')
          .toString()
          .compareTo((a['created_at'] ?? '').toString()));
      if (!mounted) return;
      setState(() {
        _rows = rows;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  Future<void> _pickProject() async {
    final projects = _projects ?? const [];
    final picked = await showModalBottomSheet<_ProjectPick>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => _ProjectFilterSheet(
        projects: projects,
        selectedId: _projectFilter,
      ),
    );
    if (picked == null) return;
    setState(() => _projectFilter = picked.clear ? null : picked.id);
    _load();
  }

  String _projectFilterLabel(String allProjectsLabel) {
    final id = _projectFilter;
    if (id == null) return allProjectsLabel;
    for (final p in _projects ?? const <Map<String, dynamic>>[]) {
      if ((p['id'] ?? '').toString() == id) {
        return (p['name'] ?? id).toString();
      }
    }
    return id;
  }

  Future<void> _openReview(Map<String, dynamic> row) async {
    final id = (row['id'] ?? '').toString();
    if (id.isEmpty) return;
    await showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => _ReviewDetailSheet(reviewId: id, summary: row),
    );
    if (mounted) await _load();
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.watch(vocabularyProvider);
    final reviews = vocab.term(VocabAxis.entityReview);
    final projectTerm = vocab.term(VocabAxis.entityProject);
    final projects = ref.watch(hubProvider).value?.projects ?? const [];
    final scopeName = widget.projectId == null
        ? null
        : projectNameFor(widget.projectId!, projects);
    return Scaffold(
      appBar: AppBar(
        title: Text(
          scopeName == null
              ? reviews.plural
              : '${reviews.plural} · $scopeName',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 16,
            fontWeight: FontWeight.w700,
          ),
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: l10n.buttonRefresh,
            onPressed: _loading ? null : _load,
          ),
        ],
      ),
      body: Column(
        children: [
          _FilterBar(
            filters: _filters,
            selected: _filter,
            onChanged: (v) {
              setState(() => _filter = v);
              _load();
            },
            showProjectFilter: _showProjectFilter,
            projectLabel:
                _projectFilterLabel(l10n.allProjects(projectTerm.pluralLower)),
            projectIsActive: _projectFilter != null,
            onProjectTap: _pickProject,
          ),
          HubOfflineBanner(staleSince: _staleSince, onRetry: _load),
          Expanded(child: _body()),
        ],
      ),
    );
  }

  Widget _body() {
    final l10n = AppLocalizations.of(context)!;
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_hubMissing || _error != null) {
      return Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          _hubMissing ? l10n.hubNotConfigured : _error!,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 12,
            color: DesignColors.error,
          ),
        ),
      );
    }
    final rows = _rows ?? const [];
    if (rows.isEmpty) {
      final reviews =
          ref.watch(vocabularyProvider).term(VocabAxis.entityReview);
      final narrowed = _projectFilter != null;
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            narrowed
                ? l10n.reviewsNoneMatch(reviews.pluralLower)
                : _filter == 'pending'
                    ? l10n.reviewsInboxZero(reviews.pluralLower)
                    : l10n.reviewsNoneWithStatus(reviews.pluralLower),
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 13,
              color: DesignColors.textMuted,
            ),
          ),
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.symmetric(vertical: 4),
        itemCount: rows.length,
        separatorBuilder: (_, __) => const Divider(height: 1),
        itemBuilder: (_, i) {
          final row = rows[i];
          return _ReviewRow(
            row: row,
            projects: ref.watch(hubProvider).value?.projects ?? const [],
            agents: ref.watch(hubProvider).value?.agents ?? const [],
            docs: _docs,
            onTap: () => _openReview(row),
          );
        },
      ),
    );
  }
}

class _FilterBar extends StatelessWidget {
  final List<String?> filters;
  final String? selected;
  final ValueChanged<String?> onChanged;
  final bool showProjectFilter;
  final String projectLabel;
  final bool projectIsActive;
  final VoidCallback onProjectTap;
  const _FilterBar({
    required this.filters,
    required this.selected,
    required this.onChanged,
    required this.showProjectFilter,
    required this.projectLabel,
    required this.projectIsActive,
    required this.onProjectTap,
  });

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      decoration: BoxDecoration(
        border: Border(bottom: BorderSide(color: border)),
      ),
      padding: const EdgeInsets.fromLTRB(8, Spacing.s8, 8, Spacing.s8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: Row(
              children: [
                for (final f in filters) ...[
                  AppChoiceChip(
                    label: f == null
                        ? l10n.filterAll
                        : reviewStatusLabel(l10n, f),
                    selected: f == selected,
                    onTap: () => onChanged(f),
                  ),
                  const SizedBox(width: 6),
                ],
              ],
            ),
          ),
          if (showProjectFilter) ...[
            const SizedBox(height: 6),
            InkWell(
              onTap: onProjectTap,
              borderRadius: Radii.smBorder,
              child: Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: Spacing.s8, vertical: Spacing.s8),
                decoration: BoxDecoration(
                  border: Border.all(
                    color: projectIsActive ? DesignColors.primary : border,
                  ),
                  borderRadius: Radii.smBorder,
                ),
                child: Row(
                  children: [
                    Icon(
                      projectIsActive
                          ? Icons.filter_alt
                          : Icons.filter_alt_outlined,
                      size: 14,
                      color: projectIsActive
                          ? DesignColors.primary
                          : DesignColors.textMuted,
                    ),
                    const SizedBox(width: 6),
                    Expanded(
                      child: Text(
                        projectLabel,
                        overflow: TextOverflow.ellipsis,
                        style: GoogleFonts.jetBrainsMono(
                          fontSize: 11,
                          fontWeight: FontWeight.w600,
                          color: projectIsActive
                              ? DesignColors.primary
                              : DesignColors.textMuted,
                        ),
                      ),
                    ),
                    const Icon(Icons.arrow_drop_down,
                        size: 16, color: DesignColors.textMuted),
                  ],
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _ProjectPick {
  final String? id;
  final bool clear;
  const _ProjectPick({this.id, this.clear = false});
}

class _ProjectFilterSheet extends ConsumerWidget {
  final List<Map<String, dynamic>> projects;
  final String? selectedId;
  const _ProjectFilterSheet({
    required this.projects,
    required this.selectedId,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context)!;
    final projectTerm =
        ref.watch(vocabularyProvider).term(VocabAxis.entityProject);
    return DraggableScrollableSheet(
      initialChildSize: 0.6,
      minChildSize: 0.3,
      maxChildSize: 0.9,
      expand: false,
      builder: (_, scroll) => Container(
        decoration: const BoxDecoration(
          color: DesignColors.surfaceDark,
          borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
        ),
        padding: const EdgeInsets.fromLTRB(8, 12, 8, 16),
        child: ListView.separated(
          controller: scroll,
          itemCount: projects.length + 1,
          separatorBuilder: (_, __) => const Divider(height: 1),
          itemBuilder: (_, i) {
            if (i == 0) {
              return ListTile(
                leading: const Icon(Icons.clear, size: 18),
                title: Text(
                  l10n.allProjects(projectTerm.pluralLower),
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                selected: selectedId == null,
                onTap: () =>
                    Navigator.of(context).pop(const _ProjectPick(clear: true)),
              );
            }
            final p = projects[i - 1];
            final id = (p['id'] ?? '').toString();
            final name = (p['name'] ?? id).toString();
            final kind = (p['kind'] ?? '').toString();
            return ListTile(
              title: Text(
                name,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
              ),
              subtitle: Text(
                [if (kind.isNotEmpty) kind, id].join(' · '),
                style: GoogleFonts.jetBrainsMono(
                  fontSize: FontSizes.label,
                  color: DesignColors.textMuted,
                ),
              ),
              selected: selectedId == id,
              onTap: () => Navigator.of(context).pop(_ProjectPick(id: id)),
            );
          },
        ),
      ),
    );
  }
}

class _ReviewRow extends ConsumerWidget {
  final Map<String, dynamic> row;
  final List<Map<String, dynamic>> projects;
  final List<Map<String, dynamic>> agents;
  final List<Map<String, dynamic>> docs;
  final VoidCallback onTap;
  const _ReviewRow({
    required this.row,
    required this.projects,
    required this.agents,
    required this.docs,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context)!;
    final reviews = ref.watch(vocabularyProvider).term(VocabAxis.entityReview);
    final state = _state(row);
    final projectId = (row['project_id'] ?? '').toString();
    final projectName =
        projectId.isEmpty ? '' : projectNameFor(projectId, projects);
    final targetId =
        (row['document_id'] ?? row['target_id'] ?? '').toString();
    final serverTitle =
        (row['document_title'] ?? row['title'] ?? '').toString();
    final title = serverTitle.isNotEmpty
        ? serverTitle
        : (targetId.isEmpty
            ? l10n.reviewFallbackTitle(reviews.lower)
            : documentTitleFor(targetId, docs));
    final requesterId = (row['requester_agent_id'] ??
            row['requester_handle'] ??
            row['reviewer_handle'] ??
            '')
        .toString();
    final requester = requesterId.isEmpty
        ? ''
        : agentHandleFor(requesterId, agents, fallback: requesterId);
    final created = (row['created_at'] ?? '').toString();
    return ListTile(
      onTap: onTap,
      title: Row(
        children: [
          ReviewStatusChip(state: state),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              title,
              overflow: TextOverflow.ellipsis,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
        ],
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 2),
        child: Text(
          [
            if (projectName.isNotEmpty) projectName,
            if (requester.isNotEmpty) requester,
            if (created.isNotEmpty) created,
          ].join(' · '),
          style: GoogleFonts.jetBrainsMono(
            fontSize: FontSizes.label,
            color: DesignColors.textMuted,
          ),
        ),
      ),
      trailing: const Icon(Icons.chevron_right, size: 20),
    );
  }
}

String _state(Map<String, dynamic> row) =>
    (row['state'] ?? row['status'] ?? '').toString().toLowerCase();

/// Maps a wire review status onto its localized label. Unknown states fall
/// back to the raw wire token so new server states still render.
String reviewStatusLabel(AppLocalizations l10n, String wire) {
  switch (wire.toLowerCase().replaceAll('-', '_')) {
    case 'pending':
      return l10n.reviewStatusPending;
    case 'needs_changes':
      return l10n.reviewStatusNeedsChanges;
    case 'approved':
      return l10n.reviewStatusApproved;
    case 'rejected':
      return l10n.reviewStatusRejected;
    default:
      return wire;
  }
}

class ReviewStatusChip extends StatelessWidget {
  final String state;
  const ReviewStatusChip({super.key, required this.state});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final s = state.toLowerCase();
    final color = switch (s) {
      'approved' => DesignColors.success,
      'rejected' => DesignColors.error,
      'needs_changes' || 'needs-changes' => DesignColors.warning,
      'pending' => DesignColors.terminalCyan,
      _ => DesignColors.textMuted,
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: Spacing.s8, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        s.isEmpty ? '?' : reviewStatusLabel(l10n, s),
        style: GoogleFonts.jetBrainsMono(
          fontSize: FontSizes.label,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
    );
  }
}

class _ReviewDetailSheet extends ConsumerStatefulWidget {
  final String reviewId;
  final Map<String, dynamic> summary;
  const _ReviewDetailSheet({
    required this.reviewId,
    required this.summary,
  });

  @override
  ConsumerState<_ReviewDetailSheet> createState() => _ReviewDetailSheetState();
}

class _ReviewDetailSheetState extends ConsumerState<_ReviewDetailSheet> {
  Map<String, dynamic>? _review;
  Map<String, dynamic>? _document;
  bool _loading = true;
  bool _deciding = false;
  String? _error;
  bool _hubMissing = false;
  final TextEditingController _noteCtrl = TextEditingController();

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _noteCtrl.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _hubMissing = true;
      });
      return;
    }
    try {
      final r = (await client.getReviewCached(widget.reviewId)).body;
      Map<String, dynamic>? doc;
      final docId = (r['document_id'] ?? r['target_id'] ?? '').toString();
      final kind = (r['target_kind'] ?? 'document').toString();
      if (docId.isNotEmpty && (kind == 'document' || kind.isEmpty)) {
        try {
          doc = (await client.getDocumentCached(docId)).body;
        } catch (_) {
          // Document fetch failure shouldn't block the decide flow.
          doc = null;
        }
      }
      if (!mounted) return;
      setState(() {
        _review = r;
        _document = doc;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = '$e';
        _loading = false;
      });
    }
  }

  Future<void> _decide(String decision) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final l10n = AppLocalizations.of(context)!;
    final reviewTerm =
        ref.read(vocabularyProvider).term(VocabAxis.entityReview);
    setState(() => _deciding = true);
    final note = _noteCtrl.text.trim();
    try {
      await client.decideReview(
        widget.reviewId,
        decision: decision,
        note: note.isEmpty ? null : note,
      );
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(l10n.reviewDecidedSnack(
              reviewTerm.title, reviewStatusLabel(l10n, decision))),
        ),
      );
      Navigator.of(context).pop();
    } catch (e) {
      if (!mounted) return;
      setState(() => _deciding = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(l10n.decideFailedStatus('$e'))),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return DraggableScrollableSheet(
      initialChildSize: 0.75,
      minChildSize: 0.4,
      maxChildSize: 0.95,
      expand: false,
      builder: (_, scroll) => Container(
        decoration: const BoxDecoration(
          color: DesignColors.surfaceDark,
          borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
        ),
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
        child: _body(scroll),
      ),
    );
  }

  Widget _body(ScrollController scroll) {
    final l10n = AppLocalizations.of(context)!;
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_hubMissing || _error != null) {
      return Padding(
        padding: const EdgeInsets.all(16),
        child: Text(
          _hubMissing ? l10n.hubNotConfigured : _error!,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 12,
            color: DesignColors.error,
          ),
        ),
      );
    }
    final vocab = ref.watch(vocabularyProvider);
    final reviews = vocab.term(VocabAxis.entityReview);
    final documents = vocab.term(VocabAxis.entityDocument);
    final output = vocab.term(VocabAxis.entityOutput);
    final r = _review ?? widget.summary;
    final state = _state(r);
    final isPending = state == 'pending' || state.isEmpty;
    final agents = ref.watch(hubProvider).value?.agents ?? const [];
    final docTitle =
        (_document?['title'] ?? r['document_title'] ?? '').toString().isNotEmpty
            ? (_document?['title'] ?? r['document_title']).toString()
            : l10n.reviewDocFallbackTitle(documents.lower);
    final docKind = (_document?['kind'] ?? '').toString();
    final docContent = (_document?['content_inline'] ?? '').toString();
    final requesterId =
        (r['requester_agent_id'] ?? r['requester_handle'] ?? '').toString();
    final requester = requesterId.isEmpty
        ? ''
        : agentHandleFor(requesterId, agents, fallback: requesterId);
    final decidedBy =
        (r['decided_by_user_id'] ?? r['decided_by'] ?? '').toString();
    final decidedAt = (r['decided_at'] ?? '').toString();
    final existingNote = (r['comment'] ?? r['note'] ?? '').toString();

    return ListView(
      controller: scroll,
      children: [
        Center(
          child: Container(
            width: 36,
            height: 4,
            margin: const EdgeInsets.only(bottom: 12),
            decoration: BoxDecoration(
              color: DesignColors.borderDark,
              borderRadius: Radii.xsBorder,
            ),
          ),
        ),
        Row(
          children: [
            ReviewStatusChip(state: state),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                docTitle,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 16,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
          ],
        ),
        const SizedBox(height: 12),
        if (docKind.isNotEmpty) _kv(l10n.reviewMetaKind, docKind),
        if (requester.isNotEmpty) _kv(l10n.reviewMetaRequester, requester),
        if (existingNote.isNotEmpty) _kv(l10n.reviewMetaNote, existingNote),
        if (decidedBy.isNotEmpty) _kv(l10n.reviewMetaDecidedBy, decidedBy),
        if (decidedAt.isNotEmpty) _kv(l10n.reviewMetaDecidedAt, decidedAt),
        const SizedBox(height: 8),
        if (docContent.isNotEmpty) ...[
          _sectionLabel(documents.title),
          Container(
            padding: const EdgeInsets.all(Spacing.s8),
            decoration: BoxDecoration(
              color: Colors.black.withValues(alpha: 0.25),
              borderRadius: Radii.smBorder,
              border: Border.all(color: DesignColors.borderDark),
            ),
            child: _DocumentBody(content: docContent),
          ),
          const SizedBox(height: 12),
        ] else if (_document != null &&
            (_document!['artifact_id'] ?? '').toString().isNotEmpty) ...[
          _sectionLabel(documents.title),
          Text(
            l10n.reviewStoredAsOutput(
                output.lower, '${_document!['artifact_id']}'),
            style: GoogleFonts.jetBrainsMono(
              fontSize: 11,
              color: DesignColors.textMuted,
            ),
          ),
          const SizedBox(height: 12),
        ],
        if (isPending) ...[
          _sectionLabel(l10n.fieldNoteOptional),
          TextField(
            controller: _noteCtrl,
            minLines: 2,
            maxLines: 4,
            enabled: !_deciding,
            style: GoogleFonts.jetBrainsMono(fontSize: 12),
            decoration: InputDecoration(
              hintText: l10n.reviewNoteHint,
              hintStyle: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: DesignColors.textMuted,
              ),
              border: const OutlineInputBorder(),
              isDense: true,
            ),
          ),
          const SizedBox(height: 12),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              FilledButton.icon(
                onPressed: _deciding ? null : () => _decide('approved'),
                icon: const Icon(Icons.check, size: 18),
                label: Text(l10n.buttonApprove),
                style: FilledButton.styleFrom(
                  backgroundColor: DesignColors.success,
                ),
              ),
              OutlinedButton.icon(
                onPressed: _deciding ? null : () => _decide('needs_changes'),
                icon: const Icon(Icons.edit_note, size: 18),
                label: Text(l10n.buttonRequestChanges),
                style: OutlinedButton.styleFrom(
                  foregroundColor: DesignColors.warning,
                  side: const BorderSide(color: DesignColors.warning),
                ),
              ),
              OutlinedButton.icon(
                onPressed: _deciding ? null : () => _decide('rejected'),
                icon: const Icon(Icons.close, size: 18),
                label: Text(l10n.buttonReject),
                style: OutlinedButton.styleFrom(
                  foregroundColor: DesignColors.error,
                  side: const BorderSide(color: DesignColors.error),
                ),
              ),
            ],
          ),
        ] else
          Text(
            l10n.reviewAlreadyDecided(
                reviews.title, reviewStatusLabel(l10n, state)),
            style: GoogleFonts.jetBrainsMono(
              fontSize: 11,
              color: DesignColors.textMuted,
            ),
          ),
      ],
    );
  }

  Widget _kv(String k, String v) => Padding(
        padding: const EdgeInsets.only(bottom: 4),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 90,
              child: Text(
                k,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: DesignColors.textMuted,
                ),
              ),
            ),
            Expanded(
              child: SelectableText(
                v,
                style: GoogleFonts.jetBrainsMono(fontSize: 11),
              ),
            ),
          ],
        ),
      );

  Widget _sectionLabel(String label) => Padding(
        padding: const EdgeInsets.only(bottom: Spacing.s8),
        child: Text(
          label,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 11,
            fontWeight: FontWeight.w700,
            color: DesignColors.textMuted,
            letterSpacing: 0.5,
          ),
        ),
      );
}

/// Renders a reviewed document's inline content. Reviewed docs are usually
/// markdown (policy notes, proposals, design briefs) — fall back to mono
/// plain text when the content doesn't look like markdown so logs or config
/// diffs stay readable.
class _DocumentBody extends StatelessWidget {
  final String content;
  const _DocumentBody({required this.content});

  @override
  Widget build(BuildContext context) {
    final looksMd =
        RegExp(r'(^|\n)(#|- |\* |\d+\. |```|> )').hasMatch(content);
    if (looksMd) {
      return MarkdownBody(
        data: content,
        selectable: true,
        styleSheet: MarkdownStyleSheet(
          p: GoogleFonts.spaceGrotesk(fontSize: 13, height: 1.4),
          h1: GoogleFonts.spaceGrotesk(
              fontSize: FontSizes.title, fontWeight: FontWeight.w700),
          h2: GoogleFonts.spaceGrotesk(
              fontSize: FontSizes.subtitle, fontWeight: FontWeight.w700),
          h3: GoogleFonts.spaceGrotesk(
              fontSize: 14, fontWeight: FontWeight.w700),
          code: GoogleFonts.jetBrainsMono(
            fontSize: 12,
            backgroundColor: Colors.black.withValues(alpha: 0.35),
          ),
          codeblockDecoration: BoxDecoration(
            color: Colors.black.withValues(alpha: 0.4),
            borderRadius: BorderRadius.circular(4),
          ),
        ),
      );
    }
    return SelectableText(
      content,
      style: GoogleFonts.jetBrainsMono(fontSize: 12),
    );
  }
}
