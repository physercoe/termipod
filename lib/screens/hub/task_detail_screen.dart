import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import '../../widgets/hub_offline_banner.dart';
import 'task_edit_sheet.dart';

/// Full-screen task detail. Shows title, body, status, and a status picker
/// at the top. Edits patch the task and refetch before rendering so stale
/// state doesn't linger on the screen.
class TaskDetailScreen extends ConsumerStatefulWidget {
  final String projectId;
  final String taskId;
  final Map<String, dynamic>? initial;
  const TaskDetailScreen({
    super.key,
    required this.projectId,
    required this.taskId,
    this.initial,
  });

  @override
  ConsumerState<TaskDetailScreen> createState() => _TaskDetailScreenState();
}

class _TaskDetailScreenState extends ConsumerState<TaskDetailScreen> {
  Map<String, dynamic>? _task;
  String? _error;
  bool _loading = true;
  DateTime? _staleSince;

  static const _statuses = ['todo', 'in_progress', 'blocked', 'done'];

  @override
  void initState() {
    super.initState();
    if (widget.initial != null) {
      _task = widget.initial;
      _loading = false;
    }
    _refresh();
  }

  Future<void> _refresh() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final cached =
          await client.getTaskCached(widget.projectId, widget.taskId);
      if (!mounted) return;
      setState(() {
        _task = cached.body;
        _staleSince = cached.staleSince;
        _loading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  Future<void> _openEditSheet() async {
    final task = _task;
    if (task == null) return;
    final updated = await showModalBottomSheet<Map<String, dynamic>>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => TaskEditSheet(
        projectId: widget.projectId,
        task: task,
      ),
    );
    if (updated != null && mounted) {
      setState(() => _task = updated);
    }
  }

  Future<void> _setStatus(String s) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final row = await client.patchTask(
        widget.projectId,
        widget.taskId,
        status: s,
      );
      if (!mounted) return;
      setState(() => _task = row);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Status change failed: $e')),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Task'),
        actions: [
          IconButton(
            icon: const Icon(Icons.edit_outlined),
            tooltip: 'Edit task',
            onPressed: _task == null ? null : _openEditSheet,
          ),
        ],
      ),
      body: Column(
        children: [
          HubOfflineBanner(staleSince: _staleSince, onRetry: _refresh),
          Expanded(
            child: _loading
                ? const Center(child: CircularProgressIndicator())
                : _error != null
                    ? Center(
                        child: Padding(
                          padding: const EdgeInsets.all(24),
                          child: Text(_error!,
                              style: GoogleFonts.jetBrainsMono(
                                  color: DesignColors.error, fontSize: 12)),
                        ),
                      )
                    : _body(),
          ),
        ],
      ),
    );
  }

  Widget _body() {
    final task = _task ?? const <String, dynamic>{};
    final title = (task['title'] ?? '').toString();
    final body = (task['body_md'] ?? '').toString();
    final status = (task['status'] ?? 'todo').toString();
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return RefreshIndicator(
      onRefresh: _refresh,
      child: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Text(title,
              style: GoogleFonts.spaceGrotesk(
                  fontSize: 20, fontWeight: FontWeight.w700)),
          const SizedBox(height: 10),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final s in _statuses)
                ChoiceChip(
                  label: Text(s),
                  selected: status == s,
                  onSelected: (sel) {
                    if (sel) _setStatus(s);
                  },
                ),
            ],
          ),
          const SizedBox(height: 16),
          Container(
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(
              color: isDark
                  ? DesignColors.surfaceDark
                  : DesignColors.surfaceLight,
              borderRadius: BorderRadius.circular(8),
              border: Border.all(
                color: isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight,
              ),
            ),
            child: _TaskBody(body: body),
          ),
        ],
      ),
    );
  }
}

/// Renders a task body either as markdown (when it contains common
/// markdown markers) or as mono-spaced plain text. Mirrors the
/// heuristic already used by the Documents viewer so behaviour is
/// consistent across task/doc bodies.
class _TaskBody extends StatelessWidget {
  final String body;
  const _TaskBody({required this.body});

  @override
  Widget build(BuildContext context) {
    if (body.isEmpty) {
      return Text(
        '(no body)',
        style: GoogleFonts.spaceGrotesk(
          fontSize: 13,
          height: 1.4,
          color: DesignColors.textMuted,
        ),
      );
    }
    final looksMd =
        RegExp(r'(^|\n)(#|- |\* |\d+\. |```|> )').hasMatch(body);
    if (looksMd) {
      return MarkdownBody(
        data: body,
        selectable: true,
        styleSheet: MarkdownStyleSheet(
          p: GoogleFonts.spaceGrotesk(fontSize: 13, height: 1.4),
          code: GoogleFonts.jetBrainsMono(fontSize: 12),
          h1: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
          h2: GoogleFonts.spaceGrotesk(
              fontSize: 16, fontWeight: FontWeight.w700),
          h3: GoogleFonts.spaceGrotesk(
              fontSize: 14, fontWeight: FontWeight.w700),
        ),
      );
    }
    return SelectableText(
      body,
      style: GoogleFonts.spaceGrotesk(fontSize: 13, height: 1.4),
    );
  }
}
