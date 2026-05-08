// AgentCompose — producer='user' input bar for the AgentFeed
// (blueprint P2.2). Sends text + cancel today; approval and attach are
// scaffolded so pending-request UI can hook them up without adding a
// whole new widget later.
import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:image_picker/image_picker.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../providers/hub_provider.dart';
import '../providers/input_history_provider.dart';
import '../providers/snippet_provider.dart';
import '../services/hub/hub_client.dart';
import '../services/image/image_converter.dart';
import '../theme/design_colors.dart';
import 'action_bar/snippet_picker_sheet.dart';

// ADR-021 W4.1 / D5 — image-content-block contract caps. Mirrors the
// hub-side validator so the composer can clamp before sending instead
// of round-tripping a 400. 5 MiB decoded per image, 3 images per turn,
// 1024px max long edge for compression.
const int _maxImagesPerTurn = 3;
const int _maxImageBytes = 5 * 1024 * 1024;
const int _composeImageMaxEdge = 1024;
const int _composeImageJpegQuality = 70;

/// resolveCanAttachImages joins an agent's `kind` + `driving_mode`
/// against the family registry's `prompt_image[mode]` flag (ADR-021
/// D5 / W4.6). Exported `@visibleForTesting` so widget tests can pin
/// the gate without spinning up a fake HubClient.
@visibleForTesting
bool resolveCanAttachImages({
  required String? kind,
  required String? drivingMode,
  required List<Map<String, dynamic>> families,
}) {
  if (kind == null || kind.isEmpty) return false;
  final mode = (drivingMode == null || drivingMode.isEmpty) ? 'M4' : drivingMode;
  for (final f in families) {
    if (f['family'] == kind) {
      final pi = f['prompt_image'];
      if (pi is Map && pi[mode] == true) return true;
      return false;
    }
  }
  return false;
}

/// Sits under AgentFeed and routes text/cancel inputs to the hub's
/// /agents/{id}/input endpoint. The hub persists them as producer='user'
/// agent_events; host-runner's InputRouter then delivers them to the
/// running driver over its native transport (stream-json stdin, tmux
/// send-keys, ACP session/prompt).
///
/// W-UI-4: slashCommands / mentions are sourced from the active
/// session.init payload by AgentFeed and surface as a suggestion chip
/// strip when the user types a `/` or `@` token. Empty lists silently
/// disable the matching prefix — drivers that don't surface those
/// fields just don't get the picker.
class AgentCompose extends ConsumerStatefulWidget {
  final String agentId;
  final List<String> slashCommands;
  final List<String> mentions;
  /// True while the current turn hasn't completed (streaming text,
  /// pending tool result, awaiting turn.result). The composer renders
  /// cancel-instead-of-send only when the user has typed something
  /// AND this is true — i.e. the user wrote their next prompt while
  /// the agent is still answering the previous one and now needs to
  /// interrupt to send. Default false so a screen with no event
  /// stream still shows the normal send button.
  final bool isAgentBusy;
  const AgentCompose({
    super.key,
    required this.agentId,
    this.slashCommands = const [],
    this.mentions = const [],
    this.isAgentBusy = false,
  });

  @override
  ConsumerState<AgentCompose> createState() => _AgentComposeState();
}

class _AgentComposeState extends ConsumerState<AgentCompose> {
  final TextEditingController _ctrl = TextEditingController();
  final FocusNode _focus = FocusNode();
  bool _sending = false;
  String? _error;

  // ADR-021 W4.6 — image-attach state. _canAttachImages is resolved
  // once at mount by joining getAgent (kind, driving_mode) with the
  // family registry's prompt_image[mode] flag; absent or false leaves
  // the affordance hidden so engines that don't support inline
  // images don't surface a misleading button. _pendingImages holds
  // up to _maxImagesPerTurn entries each shaped {mime_type, data}
  // with data already base64-encoded; on send they ride alongside
  // the text body in postAgentInput's images param (W4.1).
  bool _canAttachImages = false;
  bool _attaching = false;
  final List<Map<String, String>> _pendingImages = [];

  @override
  void initState() {
    super.initState();
    // Re-render on every keystroke so the suggestion strip can react to
    // `/` and `@` prefixes at the cursor. The TextField itself doesn't
    // need a setState; the strip computes from controller.value.
    _ctrl.addListener(() => setState(() {}));
    unawaited(_resolveImageAttachAffordance());
  }

  Future<void> _resolveImageAttachAffordance() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final agent = await client.getAgent(widget.agentId);
      final cached = await client.listAgentFamiliesCached();
      final families = cached.body
          .map((e) => e.cast<String, dynamic>())
          .toList();
      final ok = resolveCanAttachImages(
        kind: agent['kind']?.toString(),
        drivingMode: agent['driving_mode']?.toString(),
        families: families,
      );
      if (!mounted) return;
      if (ok != _canAttachImages) {
        setState(() => _canAttachImages = ok);
      }
    } catch (_) {
      // Swallow — a transient lookup failure leaves the affordance
      // hidden, which is the safe default. The user can retry by
      // refocusing the screen (next initState picks up the change
      // if the family registry was the missing piece).
    }
  }

  @override
  void dispose() {
    _ctrl.dispose();
    _focus.dispose();
    super.dispose();
  }

  // Find the token at the current cursor (last whitespace-delimited
  // chunk that ends at selection.end). Returns null when the cursor is
  // mid-text or there's no leading prefix character.
  _PrefixMatch? _activeMatch() {
    final value = _ctrl.value;
    final sel = value.selection;
    if (!sel.isValid || !sel.isCollapsed) return null;
    final upto = value.text.substring(0, sel.end);
    final start = upto.lastIndexOf(RegExp(r'\s'));
    final tokenStart = start + 1;
    final token = upto.substring(tokenStart);
    if (token.isEmpty) return null;
    final lead = token[0];
    if (lead != '/' && lead != '@') return null;
    final query = token.substring(1).toLowerCase();
    final pool = lead == '/' ? widget.slashCommands : widget.mentions;
    if (pool.isEmpty) return null;
    final matches = <String>[];
    for (final c in pool) {
      // Slash commands in claude-code arrive as "/help" — strip the
      // leading slash before comparing so the user can type "/he" and
      // still match "/help" without seeing "//help" suggestions.
      final norm = lead == '/' && c.startsWith('/') ? c.substring(1) : c;
      if (query.isEmpty || norm.toLowerCase().startsWith(query)) {
        matches.add(norm);
      }
      if (matches.length >= 8) break;
    }
    if (matches.isEmpty) return null;
    return _PrefixMatch(
      lead: lead,
      tokenStart: tokenStart,
      tokenEnd: sel.end,
      suggestions: matches,
    );
  }

  void _applySuggestion(_PrefixMatch m, String suggestion) {
    final text = _ctrl.text;
    final replacement = '${m.lead}$suggestion ';
    final next = text.substring(0, m.tokenStart) +
        replacement +
        text.substring(m.tokenEnd);
    final cursor = m.tokenStart + replacement.length;
    _ctrl.value = TextEditingValue(
      text: next,
      selection: TextSelection.collapsed(offset: cursor),
    );
    _focus.requestFocus();
  }

  Future<void> _send() async {
    final body = _ctrl.text.trimRight();
    final hasImages = _pendingImages.isNotEmpty;
    if ((body.isEmpty && !hasImages) || _sending) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() => _error = 'Not connected');
      return;
    }
    setState(() {
      _sending = true;
      _error = null;
    });
    try {
      await client.postAgentInput(
        widget.agentId,
        kind: 'text',
        body: body.isEmpty ? null : body,
        images: hasImages ? List<Map<String, String>>.from(_pendingImages) : null,
      );
      if (body.isNotEmpty) {
        unawaited(ref.read(inputHistoryProvider.notifier).add(body));
      }
      if (!mounted) return;
      _ctrl.clear();
      _pendingImages.clear();
      _focus.requestFocus();
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Send failed (${e.status})');
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Send failed: $e');
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  Future<void> _pickImage() async {
    if (_attaching || _sending) return;
    if (_pendingImages.length >= _maxImagesPerTurn) {
      setState(() => _error = 'Max $_maxImagesPerTurn images per turn');
      return;
    }
    setState(() {
      _attaching = true;
      _error = null;
    });
    try {
      final picker = ImagePicker();
      final picked = await picker.pickImage(source: ImageSource.gallery);
      if (picked == null) return;
      final raw = await picked.readAsBytes();
      // Compress to JPEG at 1024px max edge / 70% quality (per plan
      // §W4.6). Hub validator caps decoded bytes at 5 MiB; the
      // compression target is well under that for typical phone
      // screenshots so we rarely retry.
      final converted = await ImageConverter.convert(
        bytes: raw,
        format: ImageOutputFormat.jpeg,
        jpegQuality: _composeImageJpegQuality,
        autoResize: true,
        maxWidth: _composeImageMaxEdge,
        maxHeight: _composeImageMaxEdge,
      );
      final bytes = converted.bytes;
      if (bytes.length > _maxImageBytes) {
        setState(() => _error =
            'Image too large after compression (${(bytes.length / 1024 / 1024).toStringAsFixed(1)} MiB > 5 MiB)');
        return;
      }
      final mime = _mimeForExtension(converted.extension);
      if (mime == null) {
        setState(() => _error = 'Unsupported image format: ${converted.extension}');
        return;
      }
      if (!mounted) return;
      setState(() {
        _pendingImages.add({
          'mime_type': mime,
          'data': base64Encode(bytes),
        });
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Attach failed: $e');
    } finally {
      if (mounted) setState(() => _attaching = false);
    }
  }

  void _removePendingImage(int index) {
    if (index < 0 || index >= _pendingImages.length) return;
    setState(() => _pendingImages.removeAt(index));
  }

  // _mimeForExtension maps the ImageConverter's lowercase extension
  // (jpg/png/gif) to the hub's accepted mime_type vocabulary (W4.1
  // allowlist). webp/heic aren't produced by the converter's jpeg/png
  // output paths; if a future format lands here we'd extend the map
  // and the W4.1 allowlist together.
  String? _mimeForExtension(String ext) {
    switch (ext) {
      case 'jpg':
      case 'jpeg':
        return 'image/jpeg';
      case 'png':
        return 'image/png';
      case 'gif':
        return 'image/gif';
      default:
        return null;
    }
  }

  /// Insert snippet content at the current cursor position. Mirrors how
  /// the terminal compose handles tap-to-insert: append at the end if
  /// the field is empty, otherwise replace the current selection.
  void _insertSnippet(String content) {
    final value = _ctrl.value;
    final sel = value.selection.isValid
        ? value.selection
        : TextSelection.collapsed(offset: value.text.length);
    final next = value.text.replaceRange(sel.start, sel.end, content);
    final cursor = sel.start + content.length;
    _ctrl.value = TextEditingValue(
      text: next,
      selection: TextSelection.collapsed(offset: cursor),
    );
    _focus.requestFocus();
  }

  /// Save the current compose text as a draft snippet. Mirrors the
  /// terminal compose's "save as snippet" flow (terminal_screen.dart
  /// _handleSaveSnippet): empty → snackbar; otherwise prompt for a
  /// name, save under category 'drafts', clear the field. Once stashed,
  /// the text lives in the snippet library, not in limbo.
  Future<void> _saveAsSnippet() async {
    final l10n = AppLocalizations.of(context)!;
    final text = _ctrl.text;
    if (text.trim().isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(l10n.saveSnippetEmpty)),
      );
      return;
    }
    final nameController = TextEditingController();
    final name = await showDialog<String>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text(l10n.saveAsSnippet),
        content: TextField(
          controller: nameController,
          autofocus: true,
          decoration: InputDecoration(hintText: l10n.snippetName),
          onSubmitted: (v) => Navigator.pop(dialogContext, v.trim()),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext),
            child: Text(l10n.buttonCancel),
          ),
          TextButton(
            onPressed: () => Navigator.pop(
              dialogContext,
              nameController.text.trim(),
            ),
            child: Text(l10n.buttonSave),
          ),
        ],
      ),
    );
    nameController.dispose();
    if (name == null || name.isEmpty) return;
    await ref.read(snippetsProvider.notifier).addSnippet(
          name: name,
          content: text,
          category: 'drafts',
        );
    if (!mounted) return;
    _ctrl.clear();
    _focus.requestFocus();
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(l10n.savedToSnippets)),
    );
  }

  /// Send a snippet's expanded content directly without going through
  /// the input field. Used by the picker's "send immediately" affordance
  /// (double-tap a snippet) so a one-shot prompt doesn't need an extra
  /// tap on Send.
  Future<void> _sendSnippetImmediately(String content) async {
    if (_sending) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() => _error = 'Not connected');
      return;
    }
    setState(() {
      _sending = true;
      _error = null;
    });
    try {
      await client.postAgentInput(widget.agentId, kind: 'text', body: content);
      unawaited(ref.read(inputHistoryProvider.notifier).add(content));
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Send failed (${e.status})');
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Send failed: $e');
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  Future<void> _cancel() async {
    if (_sending) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() => _error = 'Not connected');
      return;
    }
    setState(() {
      _sending = true;
      _error = null;
    });
    try {
      // Reason is optional; keep it human so the feed shows why.
      await client.postAgentInput(widget.agentId,
          kind: 'cancel', reason: 'user requested cancel');
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Cancel failed (${e.status})');
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Cancel failed: $e');
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark
        ? DesignColors.surfaceDark
        : DesignColors.surfaceLight;
    final border = isDark
        ? DesignColors.borderDark
        : DesignColors.borderLight;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;

    final match = _activeMatch();
    return Container(
      decoration: BoxDecoration(
        color: bg,
        border: Border(top: BorderSide(color: border)),
      ),
      padding: const EdgeInsets.fromLTRB(8, 6, 8, 8),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (match != null)
            _SuggestionStrip(
              match: match,
              onTap: (s) => _applySuggestion(match, s),
              border: border,
              muted: muted,
            ),
          if (_pendingImages.isNotEmpty)
            _ImageThumbnailStrip(
              images: _pendingImages,
              onRemove: _removePendingImage,
            ),
          if (_error != null)
            Padding(
              padding: const EdgeInsets.only(bottom: 4, left: 4),
              child: Text(
                _error!,
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 11, color: DesignColors.error),
              ),
            ),
          Row(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              // Bolt = snippet preset (feedback_bolt_icon_ambiguity).
              // Tap → snippet picker. Long-press → save current compose
              // text as a draft snippet. Same gesture map as the
              // action-bar's bolt button (action_bar.dart:139), so the
              // user's muscle memory carries between tmux and steward
              // chat without a separate "stash" button.
              GestureDetector(
                onLongPress: _sending ? null : _saveAsSnippet,
                child: IconButton(
                  tooltip: 'Snippets · long-press to save',
                  onPressed: _sending
                      ? null
                      : () => SnippetPickerSheet.show(
                            context,
                            ref: ref,
                            onInsert: _insertSnippet,
                            onSendImmediately: _sendSnippetImmediately,
                          ),
                  icon: Icon(Icons.bolt, size: 22, color: muted),
                  padding: EdgeInsets.zero,
                  visualDensity: VisualDensity.compact,
                  constraints:
                      const BoxConstraints(minWidth: 36, minHeight: 36),
                ),
              ),
              // ADR-021 W4.6 — image attach. Only rendered when the
              // active agent's family declares prompt_image[mode] == true
              // (claude M2 / codex M2 / gemini M1). For gemini M2
              // exec-per-turn the affordance stays hidden because the
              // driver-side W4.5 strip-and-warn is a fallback for
              // forwarded payloads, not an invitation to send them.
              if (_canAttachImages)
                IconButton(
                  tooltip: _pendingImages.length >= _maxImagesPerTurn
                      ? 'Max $_maxImagesPerTurn images per turn'
                      : 'Attach image (${_pendingImages.length}/$_maxImagesPerTurn)',
                  onPressed: (_sending ||
                          _attaching ||
                          _pendingImages.length >= _maxImagesPerTurn)
                      ? null
                      : _pickImage,
                  icon: _attaching
                      ? SizedBox(
                          width: 18,
                          height: 18,
                          child: CircularProgressIndicator(
                              strokeWidth: 2, color: muted),
                        )
                      : Icon(Icons.image_outlined, size: 22, color: muted),
                  padding: EdgeInsets.zero,
                  visualDensity: VisualDensity.compact,
                  constraints:
                      const BoxConstraints(minWidth: 36, minHeight: 36),
                ),
              // Field metrics matched to action_bar/compose_bar.dart so
              // the steward composer feels the same as the tmux one:
              // unbounded line count up to a 120px ceiling, fontSize 14,
              // and an inline clear button (Icons.close_rounded) that
              // appears only when the field has text.
              Expanded(
                child: ConstrainedBox(
                  constraints: const BoxConstraints(maxHeight: 120),
                  child: TextField(
                    controller: _ctrl,
                    focusNode: _focus,
                    enabled: !_sending,
                    minLines: 1,
                    maxLines: null,
                    keyboardType: TextInputType.multiline,
                    textInputAction: TextInputAction.newline,
                    style: GoogleFonts.jetBrainsMono(fontSize: 14),
                    decoration: InputDecoration(
                      isDense: true,
                      hintText: 'Send to agent…',
                      hintStyle: GoogleFonts.jetBrainsMono(
                          fontSize: 14, color: muted),
                      border: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(6),
                        borderSide: BorderSide(color: border),
                      ),
                      enabledBorder: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(6),
                        borderSide: BorderSide(color: border),
                      ),
                      contentPadding: const EdgeInsets.symmetric(
                          horizontal: 10, vertical: 8),
                      suffixIcon: _ctrl.text.isEmpty
                          ? null
                          : GestureDetector(
                              onTap: () {
                                _ctrl.clear();
                                _focus.requestFocus();
                              },
                              child: Padding(
                                padding: const EdgeInsets.only(right: 4),
                                child: Icon(
                                  Icons.close_rounded,
                                  size: 18,
                                  color: muted,
                                ),
                              ),
                            ),
                      suffixIconConstraints: const BoxConstraints(
                          minWidth: 28, minHeight: 28),
                    ),
                  ),
                ),
              ),
              const SizedBox(width: 4),
              // Single-slot control:
              //   sending      → spinner (local POST in flight)
              //   agent busy   → cancel (red), regardless of field
              //                  content. User may interrupt at any
              //                  time — to send a typed prompt that's
              //                  waiting, or because they spotted the
              //                  agent doing something unexpected and
              //                  want to stop it now.
              //   text + idle  → send (primary)
              //   empty + idle → send (disabled, muted)
              ValueListenableBuilder<TextEditingValue>(
                valueListenable: _ctrl,
                builder: (_, value, _) {
                  // ADR-021 W4.6 — empty body is acceptable when at
                  // least one image is queued; the agent gets an
                  // image-only turn (same shape the hub W4.1 contract
                  // accepts).
                  final empty =
                      value.text.trim().isEmpty && _pendingImages.isEmpty;
                  if (_sending) {
                    return const Padding(
                      padding: EdgeInsets.symmetric(horizontal: 10),
                      child: SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      ),
                    );
                  }
                  if (widget.isAgentBusy) {
                    return IconButton(
                      tooltip: empty
                          ? 'Cancel current turn — interrupt the agent'
                          : 'Cancel current turn — frees the agent to receive your next prompt',
                      onPressed: _cancel,
                      icon: Icon(
                        Icons.stop_circle_outlined,
                        size: 22,
                        color: DesignColors.error,
                      ),
                      padding: EdgeInsets.zero,
                      visualDensity: VisualDensity.compact,
                      constraints: const BoxConstraints(
                          minWidth: 40, minHeight: 40),
                    );
                  }
                  return IconButton(
                    tooltip: 'Send as text input',
                    onPressed: empty ? null : _send,
                    icon: Icon(Icons.send,
                        size: 20,
                        color: empty ? muted : DesignColors.primary),
                    padding: EdgeInsets.zero,
                    visualDensity: VisualDensity.compact,
                    constraints: const BoxConstraints(
                        minWidth: 40, minHeight: 40),
                  );
                },
              ),
            ],
          ),
        ],
      ),
    );
  }
}

/// Resolved prefix-trigger state — what's at the cursor + what to offer.
class _PrefixMatch {
  final String lead; // '/' or '@'
  final int tokenStart;
  final int tokenEnd;
  final List<String> suggestions;
  const _PrefixMatch({
    required this.lead,
    required this.tokenStart,
    required this.tokenEnd,
    required this.suggestions,
  });
}

/// Horizontal chip strip rendered above the composer when a `/` or `@`
/// trigger is active. Capped at the same length as `suggestions` (the
/// match's slice) so a 200-tool driver doesn't blow the row out.
class _SuggestionStrip extends StatelessWidget {
  final _PrefixMatch match;
  final void Function(String) onTap;
  final Color border;
  final Color muted;
  const _SuggestionStrip({
    required this.match,
    required this.onTap,
    required this.border,
    required this.muted,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 6, left: 4),
      child: SizedBox(
        height: 28,
        child: ListView.separated(
          scrollDirection: Axis.horizontal,
          itemCount: match.suggestions.length,
          separatorBuilder: (_, _) => const SizedBox(width: 6),
          itemBuilder: (ctx, i) {
            final s = match.suggestions[i];
            return InkWell(
              onTap: () => onTap(s),
              borderRadius: BorderRadius.circular(4),
              child: Container(
                padding: const EdgeInsets.symmetric(horizontal: 8),
                alignment: Alignment.center,
                decoration: BoxDecoration(
                  borderRadius: BorderRadius.circular(4),
                  border: Border.all(color: border),
                ),
                child: Text(
                  '${match.lead}$s',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 11,
                    color: muted,
                  ),
                ),
              ),
            );
          },
        ),
      ),
    );
  }
}

/// Horizontal thumbnail strip rendered above the text field when the
/// composer has queued one or more images for the next prompt
/// (ADR-021 W4.6). Tap × on a thumbnail to remove it before sending.
/// Bytes are decoded back from the in-memory base64 string, so each
/// thumbnail render is one decode pass — fine at 1024px max edge but
/// not appropriate for arbitrary-resolution input.
class _ImageThumbnailStrip extends StatelessWidget {
  final List<Map<String, String>> images;
  final ValueChanged<int> onRemove;
  const _ImageThumbnailStrip({
    required this.images,
    required this.onRemove,
  });

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 64,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.only(bottom: 6, left: 4, right: 4),
        itemCount: images.length,
        separatorBuilder: (_, _) => const SizedBox(width: 6),
        itemBuilder: (ctx, i) {
          final entry = images[i];
          Uint8List? bytes;
          try {
            bytes = base64Decode(entry['data'] ?? '');
          } catch (_) {
            bytes = null;
          }
          return Stack(
            clipBehavior: Clip.none,
            children: [
              ClipRRect(
                borderRadius: BorderRadius.circular(6),
                child: Container(
                  width: 56,
                  height: 56,
                  color: DesignColors.surfaceDark,
                  child: bytes != null
                      ? Image.memory(bytes,
                          fit: BoxFit.cover,
                          gaplessPlayback: true)
                      : const Icon(Icons.broken_image_outlined),
                ),
              ),
              Positioned(
                top: -6,
                right: -6,
                child: GestureDetector(
                  onTap: () => onRemove(i),
                  child: Container(
                    padding: const EdgeInsets.all(2),
                    decoration: const BoxDecoration(
                      color: DesignColors.error,
                      shape: BoxShape.circle,
                    ),
                    child: const Icon(
                      Icons.close,
                      size: 12,
                      color: Colors.white,
                    ),
                  ),
                ),
              ),
            ],
          );
        },
      ),
    );
  }
}
