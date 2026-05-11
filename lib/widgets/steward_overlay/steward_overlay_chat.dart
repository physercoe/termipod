import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../screens/home_screen.dart';
import '../../services/deep_link/uri_router.dart';
import '../../theme/design_colors.dart';
import '../image_attach/composer_image_attach.dart';
import '../multimodal_attach/composer_multimodal_attach.dart';
import '../text_attach/composer_text_attach.dart';
import 'steward_overlay_chips.dart';
import 'steward_overlay_controller.dart';

/// Compact chat surface that lives inside the expanded
/// [StewardOverlay] panel. Connects to the team's general steward
/// (via [stewardOverlayControllerProvider]) and renders a minimal
/// transcript + a single-line input.
///
/// Intentionally **not** a clone of `agent_feed.dart` — that widget
/// is 4500+ lines of full-fidelity transcript rendering. The overlay
/// chat is for short directives ("show me X", "open Y"); when the
/// user wants a deep transcript they can open the steward's session
/// chat from the Sessions tab.
class StewardOverlayChat extends ConsumerStatefulWidget {
  /// Called when the user explicitly closes the chat (e.g. the
  /// header X). Lets the parent collapse the overlay panel.
  final VoidCallback onCloseRequested;

  const StewardOverlayChat({super.key, required this.onCloseRequested});

  @override
  ConsumerState<StewardOverlayChat> createState() => _StewardOverlayChatState();
}

class _StewardOverlayChatState extends ConsumerState<StewardOverlayChat> {
  // Wave 2 W4 — image-attach capability. Resolved once after the
  // overlay binds an agentId, then kept stable. Reading it through
  // a State field (rather than `ref.watch` inside _ChatInput) keeps
  // the input subtree off the rebuild path that triggered the
  // v1.0.466 IME bugs.
  bool _canAttachImages = false;
  // W7.2 — per-modality capability flags. Same family/mode join as
  // _canAttachImages; resolved once per bound agent.
  bool _canAttachPdfs = false;
  bool _canAttachAudio = false;
  bool _canAttachVideo = false;
  String? _resolvedForAgentId;

  /// Sends user input via the steward controller. Hoisted out of the
  /// build tree as a stable function reference so `_ChatInput`'s
  /// State doesn't think the callback identity changed across builds.
  Future<void> _sendMessage(
    String text,
    List<Map<String, String>>? images, {
    Map<String, String>? pdf,
    Map<String, String>? audio,
    Map<String, String>? video,
  }) async {
    final controller = ref.read(stewardOverlayControllerProvider.notifier);
    await controller.sendUserMessage(
      text,
      images: images,
      pdf: pdf,
      audio: audio,
      video: video,
    );
  }

  Future<void> _resolveCapabilityIfNeeded(String? agentId) async {
    if (agentId == null || agentId.isEmpty) return;
    if (_resolvedForAgentId == agentId) return;
    _resolvedForAgentId = agentId;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final agent = await client.getAgent(agentId);
      final cached = await client.listAgentFamiliesCached();
      final families = cached.body
          .map((e) => e.cast<String, dynamic>())
          .toList();
      final drivingMode =
          (agent['mode'] ?? agent['driving_mode'])?.toString();
      final kind = agent['kind']?.toString();
      final canImg = resolveCanAttachImages(
        kind: kind, drivingMode: drivingMode, families: families,
      );
      final canPdf = resolveCanAttachPdfs(
        kind: kind, drivingMode: drivingMode, families: families,
      );
      final canAud = resolveCanAttachAudio(
        kind: kind, drivingMode: drivingMode, families: families,
      );
      final canVid = resolveCanAttachVideo(
        kind: kind, drivingMode: drivingMode, families: families,
      );
      if (!mounted) return;
      if (canImg != _canAttachImages ||
          canPdf != _canAttachPdfs ||
          canAud != _canAttachAudio ||
          canVid != _canAttachVideo) {
        setState(() {
          _canAttachImages = canImg;
          _canAttachPdfs = canPdf;
          _canAttachAudio = canAud;
          _canAttachVideo = canVid;
        });
      }
    } catch (_) {
      // Swallow — affordance stays hidden on transient lookup
      // failure; same fallback as agent_compose.
    }
  }

  @override
  Widget build(BuildContext context) {
    // **Crucial scoping decision** — there is no `ref.watch` here.
    // SSE events flow into `stewardOverlayControllerProvider` at high
    // frequency (every text chunk, every tool_call, every system
    // event); if this State watched the provider directly the whole
    // subtree (including `_ChatInput`'s TextField) would be rebuilt
    // on every event. Even with a stable controller, that triggers
    // Flutter's `_updateRemoteEditingValueIfNeeded` IME poke, which
    // GBoard interprets as a composition reset and rebounds with its
    // cached predictive word — visible bug: deleted text returning.
    //
    // Instead, the rebuild scope is narrowed to a single Consumer
    // around the messages region. The input below sits OUTSIDE that
    // Consumer's invalidation set, so SSE events never traverse the
    // TextField subtree at all. (Belt-and-suspenders: the input also
    // disables predictive composition + autocorrect; see
    // `_ChatInputState.build`.)
    return const Column(
      children: [
        Expanded(child: _MessagesRegion()),
        Divider(height: 1),
        // Quick-action chip strip — sibling to the input, lives
        // outside the messages-region Consumer so SSE traffic
        // doesn't reach it. Watches snippetsProvider for user-edit
        // changes only (rare).
        StewardOverlayChips(),
        _ChatInputSlot(),
      ],
    );
  }
}

/// Sibling-isolated slot for the chat input. Lives outside the
/// messages-region Consumer so SSE-driven rebuilds don't reach the
/// TextField subtree. Reads `_StewardOverlayChatState._sendMessage`
/// from the nearest ancestor of that type via the `context`.
///
/// `agentId` is watched via `.select` so the slot only rebuilds the
/// once (null → set) when the overlay binds an agent; SSE traffic
/// changes other parts of the state and doesn't touch this slot.
class _ChatInputSlot extends ConsumerWidget {
  const _ChatInputSlot();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final parent = context.findAncestorStateOfType<_StewardOverlayChatState>();
    if (parent == null) {
      return const SizedBox.shrink();
    }
    final agentId = ref.watch(
      stewardOverlayControllerProvider.select((s) => s.agentId),
    );
    // Trigger the capability resolve once we have an agentId. Guarded
    // inside the parent so repeat builds are no-ops.
    if (agentId != null && agentId.isNotEmpty) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        parent._resolveCapabilityIfNeeded(agentId);
      });
    }
    return _ChatInput(
      key: const ValueKey('steward-overlay-chat-input'),
      onSend: parent._sendMessage,
      canAttachImages: parent._canAttachImages,
      canAttachPdfs: parent._canAttachPdfs,
      canAttachAudio: parent._canAttachAudio,
      canAttachVideo: parent._canAttachVideo,
    );
  }
}

/// Messages region — the only widget in the chat panel that watches
/// the high-frequency steward provider. Loading / error / empty / list
/// branches all live here; the input below is unaffected by any of
/// these transitions.
class _MessagesRegion extends ConsumerStatefulWidget {
  const _MessagesRegion();

  @override
  ConsumerState<_MessagesRegion> createState() => _MessagesRegionState();
}

class _MessagesRegionState extends ConsumerState<_MessagesRegion> {
  final _scrollCtrl = ScrollController();

  @override
  void dispose() {
    _scrollCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(stewardOverlayControllerProvider);
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    if (state.error != null && state.agentId == null) {
      return _ErrorView(error: state.error!);
    }
    if (state.agentId == null) {
      return const Center(child: CircularProgressIndicator());
    }
    if (state.messages.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            'Tell the steward what you want to see.\n'
            'Examples:\n'
            '  • "Show me the steward insights view"\n'
            '  • "Open project X"\n'
            '  • "Take me to activity"\n',
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              color: muted,
            ),
          ),
        ),
      );
    }
    return ListView.separated(
      controller: _scrollCtrl,
      padding: const EdgeInsets.fromLTRB(12, 12, 12, 12),
      itemCount: state.messages.length,
      separatorBuilder: (_, __) => const SizedBox(height: 8),
      itemBuilder: (_, i) => _MessageBubble(
        msg: state.messages[i],
        muted: muted,
      ),
    );
  }
}

/// Isolated input box for the overlay chat. Owns its own
/// [TextEditingController] + sending flag so that frequent parent
/// rebuilds (driven by SSE events into `stewardOverlayControllerProvider`)
/// don't reach the TextField subtree.
///
/// Two QA bugs from v1.0.466 motivated the extraction:
///   (a) deleting some text and retyping caused the deleted text
///       to come back — symptomatic of the controller being
///       reset/repainted with stale value across rapid rebuilds.
///   (b) tapping a new cursor position then typing made the cursor
///       jump to the end — same root cause: external value
///       reapplication resets selection.
/// Hoisting the controller into a non-Consumer State (no
/// `ref.watch`) breaks the rebuild path; the input only rebuilds
/// when its OWN setState fires.
class _ChatInput extends StatefulWidget {
  /// Called with the trimmed text + optional image + per-modality
  /// attachments when the user submits. Should throw on failure so the
  /// input can restore the text for retry.
  final Future<void> Function(
    String text,
    List<Map<String, String>>? images, {
    Map<String, String>? pdf,
    Map<String, String>? audio,
    Map<String, String>? video,
  }) onSend;

  /// Whether the active agent's family declares `prompt_image[mode]`
  /// true. Controls visibility of the paperclip affordance.
  final bool canAttachImages;

  /// W7.2 — per-modality capability flags (PDF / audio / video).
  final bool canAttachPdfs;
  final bool canAttachAudio;
  final bool canAttachVideo;

  const _ChatInput({
    super.key,
    required this.onSend,
    this.canAttachImages = false,
    this.canAttachPdfs = false,
    this.canAttachAudio = false,
    this.canAttachVideo = false,
  });

  @override
  State<_ChatInput> createState() => _ChatInputState();
}

class _ChatInputState extends State<_ChatInput> {
  final _ctrl = TextEditingController();
  // Owning the focus node here (rather than letting the framework mint
  // a transient one per build) keeps the IME connection stable across
  // parent rebuilds — same reasoning as the dedicated controller. A
  // freshly-minted FocusNode every build can race the IME attach/detach
  // cycle and exacerbate the predictive-restore bug below.
  final _focus = FocusNode();
  bool _sending = false;
  // Wave 2 W4 — pending image attachments queued for the next send.
  // Each entry is `{mime_type, data}` with data base64-encoded; rides
  // alongside the text body in postAgentInput's images param (W4.1).
  bool _attaching = false;
  bool _attachingText = false;
  bool _attachingMultimodal = false;
  String? _attachError;
  final List<Map<String, String>> _pendingImages = [];
  final Map<MultimodalKind, Map<String, String>> _pendingMultimodal = {};

  @override
  void dispose() {
    _focus.dispose();
    _ctrl.dispose();
    super.dispose();
  }

  Future<void> _pickImage() async {
    if (_attaching || _sending) return;
    if (_pendingImages.length >= kMaxImagesPerTurn) {
      setState(() => _attachError = 'Max $kMaxImagesPerTurn images per turn');
      return;
    }
    setState(() {
      _attaching = true;
      _attachError = null;
    });
    try {
      final attachment = await pickAndCompressImage();
      if (attachment == null) return;
      if (!mounted) return;
      setState(() => _pendingImages.add(attachment.toJson()));
    } on ComposerImageAttachError catch (e) {
      if (!mounted) return;
      setState(() => _attachError = e.message);
    } catch (e) {
      if (!mounted) return;
      setState(() => _attachError = 'Attach failed: $e');
    } finally {
      if (mounted) setState(() => _attaching = false);
    }
  }

  void _removePendingImage(int index) {
    if (index < 0 || index >= _pendingImages.length) return;
    setState(() => _pendingImages.removeAt(index));
  }

  /// W7.1 — pick a code/text file and splice its bytes into the
  /// composer text as a fenced code block. Engine-agnostic (works on
  /// every driver because the inline path stays inside the prompt
  /// body) so no capability gate.
  Future<void> _pickTextFile() async {
    if (_attachingText || _sending) return;
    setState(() {
      _attachingText = true;
      _attachError = null;
    });
    try {
      final att = await pickAndInlineTextFile();
      if (att == null) return;
      if (!mounted) return;
      final value = _ctrl.value;
      final sel = value.selection.isValid
          ? value.selection
          : TextSelection.collapsed(offset: value.text.length);
      final next = value.text.replaceRange(sel.start, sel.end, att.markdown);
      final cursor = sel.start + att.markdown.length;
      _ctrl.value = TextEditingValue(
        text: next,
        selection: TextSelection.collapsed(offset: cursor),
      );
      _focus.requestFocus();
    } on TextAttachError catch (e) {
      if (!mounted) return;
      setState(() => _attachError = e.message);
    } catch (e) {
      if (!mounted) return;
      setState(() => _attachError = 'Attach failed: $e');
    } finally {
      if (mounted) setState(() => _attachingText = false);
    }
  }

  /// W7.2 — pick a PDF / audio / video file and queue it as a
  /// multimodal attachment. If the family supports >1 modality the
  /// picker shows a kind sheet first.
  Future<void> _pickMultimodal() async {
    if (_attachingMultimodal || _sending) return;
    final kinds = <MultimodalKind>[
      if (widget.canAttachPdfs) MultimodalKind.pdf,
      if (widget.canAttachAudio) MultimodalKind.audio,
      if (widget.canAttachVideo) MultimodalKind.video,
    ];
    if (kinds.isEmpty) return;
    MultimodalKind? chosen;
    if (kinds.length == 1) {
      chosen = kinds.first;
    } else {
      chosen = await showModalBottomSheet<MultimodalKind>(
        context: context,
        builder: (sheetCtx) => SafeArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              for (final k in kinds)
                ListTile(
                  leading: Icon(_iconForMultimodal(k)),
                  title: Text('Attach ${k.label}'),
                  onTap: () => Navigator.pop(sheetCtx, k),
                ),
            ],
          ),
        ),
      );
    }
    if (chosen == null) return;
    setState(() {
      _attachingMultimodal = true;
      _attachError = null;
    });
    try {
      final att = await pickMultimodalFile(chosen);
      if (att == null) return;
      if (!mounted) return;
      setState(() => _pendingMultimodal[chosen!] = att.toJson());
    } on MultimodalAttachError catch (e) {
      if (!mounted) return;
      setState(() => _attachError = e.message);
    } catch (e) {
      if (!mounted) return;
      setState(() => _attachError = 'Attach failed: $e');
    } finally {
      if (mounted) setState(() => _attachingMultimodal = false);
    }
  }

  IconData _iconForMultimodal(MultimodalKind k) {
    switch (k) {
      case MultimodalKind.pdf:
        return Icons.picture_as_pdf_outlined;
      case MultimodalKind.audio:
        return Icons.audiotrack;
      case MultimodalKind.video:
        return Icons.movie_outlined;
    }
  }

  void _removePendingMultimodal(MultimodalKind k) {
    if (!_pendingMultimodal.containsKey(k)) return;
    setState(() => _pendingMultimodal.remove(k));
  }

  Future<void> _send() async {
    final text = _ctrl.text.trim();
    final hasImages = _pendingImages.isNotEmpty;
    final pdf = _pendingMultimodal[MultimodalKind.pdf];
    final audio = _pendingMultimodal[MultimodalKind.audio];
    final video = _pendingMultimodal[MultimodalKind.video];
    final hasMultimodal = pdf != null || audio != null || video != null;
    if (text.isEmpty && !hasImages && !hasMultimodal) return;
    // Clear BEFORE the await so anything the user types during the
    // network round-trip isn't wiped when the future resolves. On
    // failure we restore the original text so the user can retry.
    final stagedImages = hasImages
        ? List<Map<String, String>>.from(_pendingImages)
        : null;
    final stagedMultimodal =
        Map<MultimodalKind, Map<String, String>>.from(_pendingMultimodal);
    _ctrl.clear();
    setState(() {
      _sending = true;
      _pendingImages.clear();
      _pendingMultimodal.clear();
    });
    try {
      await widget.onSend(
        text,
        stagedImages,
        pdf: stagedMultimodal[MultimodalKind.pdf],
        audio: stagedMultimodal[MultimodalKind.audio],
        video: stagedMultimodal[MultimodalKind.video],
      );
    } catch (e) {
      if (mounted) {
        // Only restore if the user hasn't started typing something
        // new — preserving their fresh input is the higher priority.
        if (_ctrl.text.isEmpty) {
          _ctrl.text = text;
        }
        if (stagedImages != null) {
          setState(() => _pendingImages.addAll(stagedImages));
        }
        if (stagedMultimodal.isNotEmpty) {
          setState(() => _pendingMultimodal.addAll(stagedMultimodal));
        }
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Send failed: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final canAttach = widget.canAttachImages;
    final canAttachMulti = widget.canAttachPdfs ||
        widget.canAttachAudio ||
        widget.canAttachVideo;
    return Padding(
      padding: const EdgeInsets.fromLTRB(8, 8, 8, 8),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (_pendingImages.isNotEmpty)
            ComposerImageThumbnailStrip(
              images: _pendingImages,
              onRemove: _removePendingImage,
            ),
          if (_pendingMultimodal.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(bottom: 6, left: 4, right: 4),
              child: Wrap(
                spacing: 6,
                runSpacing: 4,
                children: [
                  for (final entry in _pendingMultimodal.entries)
                    InputChip(
                      avatar: Icon(_iconForMultimodal(entry.key), size: 14),
                      label: Text(
                        entry.value['filename'] ?? entry.key.label,
                        style: const TextStyle(fontSize: 11),
                        overflow: TextOverflow.ellipsis,
                      ),
                      onDeleted: () => _removePendingMultimodal(entry.key),
                    ),
                ],
              ),
            ),
          if (_attachError != null)
            Padding(
              padding: const EdgeInsets.only(bottom: 4, left: 4),
              child: Text(
                _attachError!,
                style: const TextStyle(
                    fontSize: 11, color: DesignColors.error),
              ),
            ),
          Row(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              if (canAttach)
                IconButton(
                  tooltip: _pendingImages.length >= kMaxImagesPerTurn
                      ? 'Max $kMaxImagesPerTurn images per turn'
                      : 'Attach image (${_pendingImages.length}/$kMaxImagesPerTurn)',
                  onPressed: (_sending ||
                          _attaching ||
                          _pendingImages.length >= kMaxImagesPerTurn)
                      ? null
                      : _pickImage,
                  icon: _attaching
                      ? const SizedBox(
                          width: 18,
                          height: 18,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.image_outlined, size: 22),
                  padding: EdgeInsets.zero,
                  visualDensity: VisualDensity.compact,
                  constraints:
                      const BoxConstraints(minWidth: 36, minHeight: 36),
                ),
              // W7.1 — code/text inline attach. Always rendered;
              // engine-agnostic.
              IconButton(
                tooltip: 'Attach code or text file',
                onPressed: (_sending || _attachingText) ? null : _pickTextFile,
                icon: _attachingText
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.attach_file, size: 22),
                padding: EdgeInsets.zero,
                visualDensity: VisualDensity.compact,
                constraints:
                    const BoxConstraints(minWidth: 36, minHeight: 36),
              ),
              // W7.2 — PDF / audio / video attach. Visible when the
              // family declares at least one of the prompt_pdf /
              // prompt_audio / prompt_video flags.
              if (canAttachMulti)
                IconButton(
                  tooltip: 'Attach PDF, audio, or video',
                  onPressed: (_sending || _attachingMultimodal)
                      ? null
                      : _pickMultimodal,
                  icon: _attachingMultimodal
                      ? const SizedBox(
                          width: 18,
                          height: 18,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.upload_file_outlined, size: 22),
                  padding: EdgeInsets.zero,
                  visualDensity: VisualDensity.compact,
                  constraints:
                      const BoxConstraints(minWidth: 36, minHeight: 36),
                ),
              Expanded(
            // **IME-friendly defaults.** Earlier revisions set
            // `autocorrect: false` + `enableSuggestions: false` as
            // belt-and-suspenders for the v1.0.466 deleted-text-
            // returning bug. v1.0.472 fixed that bug architecturally
            // via rebuild-scope isolation (see `_StewardOverlayChatState`
            // doc), so the defensive flags are no longer load-bearing —
            // and they actively break CJK input. Android maps them to
            // `TYPE_TEXT_FLAG_NO_AUTO_CORRECT` /
            // `TYPE_TEXT_FLAG_NO_SUGGESTIONS`; Chinese / Japanese /
            // Korean IMEs (Sogou, Gboard-CN, Baidu, Mozc, etc.)
            // interpret no-suggestions as a hard signal to fall back
            // to Latin-only mode because their candidate display IS
            // the suggestion surface. Result: a system keyboard
            // appears, but the user's selected IME refuses to engage
            // its CJK composition pipeline. v1.0.479 QA: "there is
            // keyboard but not my input method." Drop both flags.
            //
            // **Do not add `autofillHints: const []`.** An empty
            // autofillHints list is poisoned: on some Android+Gboard
            // combinations it signals AutofillManager that the field
            // is managed by autofill but has no hints, and the IME
            // fails to attach. `null` (the default, achieved by
            // omitting the line entirely) is the correct shape.
            child: TextField(
              controller: _ctrl,
              focusNode: _focus,
              minLines: 1,
              maxLines: 4,
              keyboardType: TextInputType.multiline,
              textInputAction: TextInputAction.newline,
              decoration: InputDecoration(
                isDense: true,
                hintText: 'Ask the steward…',
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(10),
                ),
                contentPadding: const EdgeInsets.symmetric(
                    horizontal: 12, vertical: 10),
              ),
            ),
          ),
              const SizedBox(width: 6),
              IconButton(
                icon: _sending
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.send, color: DesignColors.primary),
                onPressed: _sending ? null : _send,
              ),
            ],
          ),
        ],
      ),
    );
  }
}

/// Hard-truncate threshold for steward text bubbles. Beyond this we
/// show a leading slice with a "… see full session" suffix; the full
/// text lives in the Sessions screen. Keeps the overlay's directive
/// purpose clear — it's a recent-context view, not a transcript.
const int _bubbleTruncateAt = 240;

class _MessageBubble extends StatelessWidget {
  final OverlayChatMessage msg;
  final Color muted;
  const _MessageBubble({required this.msg, required this.muted});

  @override
  Widget build(BuildContext context) {
    // Intent events render as a compact past-tense pill with
    // tap-through, NOT a regular chat bubble — it's the durable
    // record of what the steward DID, not what it said.
    if (msg.intentAction != null) {
      return _IntentPill(action: msg.intentAction!, muted: muted, ts: msg.ts);
    }

    final fromUser = msg.role == OverlayChatRole.user;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = fromUser
        ? DesignColors.primary.withValues(alpha: 0.18)
        : (isDark
            ? DesignColors.backgroundDark.withValues(alpha: 0.5)
            : DesignColors.backgroundLight);

    // Compact-mode truncation (W4). Steward replies can run long;
    // the overlay only needs the gist for recent context. Users who
    // want the full reply tap "Open full session" in the header.
    final raw = msg.text;
    final shown = raw.length > _bubbleTruncateAt
        ? '${raw.substring(0, _bubbleTruncateAt)}… '
        : raw;
    final truncated = raw.length > _bubbleTruncateAt;

    return Align(
      alignment: fromUser ? Alignment.centerRight : Alignment.centerLeft,
      child: ConstrainedBox(
        constraints: BoxConstraints(
          maxWidth: MediaQuery.of(context).size.width * 0.78,
        ),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          decoration: BoxDecoration(
            color: bg,
            borderRadius: BorderRadius.circular(10),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                shown,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 13,
                  color: isDark ? Colors.white : DesignColors.textPrimary,
                ),
              ),
              if (truncated) ...[
                const SizedBox(height: 4),
                Text(
                  '… open full session for the rest',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 11,
                    color: muted,
                    fontStyle: FontStyle.italic,
                  ),
                ),
              ],
              if (msg.note != null && msg.note!.isNotEmpty) ...[
                const SizedBox(height: 4),
                Text(
                  msg.note!,
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: muted,
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

/// Renders a `mobile.intent` event as a compact past-tense pill —
/// "Steward → Insights · 14:32" with an arrow icon and tap-through
/// to re-fire the URI. Distinct from a free-form chat bubble because
/// it represents what the steward DID, not what it said. v1 only
/// exercises navigation; future actions (create / edit / write) will
/// use the same pill shape with different verbs/icons via
/// `OverlayIntentAction.verb`.
class _IntentPill extends ConsumerWidget {
  final OverlayIntentAction action;
  final DateTime ts;
  final Color muted;
  const _IntentPill({
    required this.action,
    required this.ts,
    required this.muted,
  });

  String _hhmm(DateTime t) {
    final local = t.toLocal();
    final hh = local.hour.toString().padLeft(2, '0');
    final mm = local.minute.toString().padLeft(2, '0');
    return '$hh:$mm';
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Align(
      alignment: Alignment.center,
      child: ConstrainedBox(
        constraints: BoxConstraints(
          maxWidth: MediaQuery.of(context).size.width * 0.86,
        ),
        child: InkWell(
          borderRadius: BorderRadius.circular(20),
          onTap: () => _refire(context, ref),
          child: Container(
            padding:
                const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
            decoration: BoxDecoration(
              color: DesignColors.primary.withValues(alpha: 0.10),
              borderRadius: BorderRadius.circular(20),
              border: Border.all(
                color: DesignColors.primary.withValues(alpha: 0.30),
                width: 1,
              ),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(
                  Icons.alt_route,
                  size: 14,
                  color: DesignColors.primary.withValues(alpha: 0.85),
                ),
                const SizedBox(width: 6),
                Flexible(
                  child: Text(
                    'Steward ${action.verb} ${action.target}',
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 12,
                      fontWeight: FontWeight.w500,
                      color: isDark
                          ? Colors.white.withValues(alpha: 0.9)
                          : DesignColors.textPrimary,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                const SizedBox(width: 6),
                Text(
                  '· ${_hhmm(ts)}',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: muted,
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  /// Re-fires the URI via the same router used by live intents. Lets
  /// the user tap a past pill to revisit where the steward sent them.
  void _refire(BuildContext context, WidgetRef ref) {
    final uri = Uri.tryParse(action.uri);
    if (uri == null) return;
    final hub = ref.read(hubProvider).value;
    navigateToUri(
      context,
      uri,
      hub: hub,
      setTab: (i) => ref.read(currentTabProvider.notifier).setTab(i),
    );
  }
}

class _ErrorView extends StatelessWidget {
  final String error;
  const _ErrorView({required this.error});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.error_outline,
                color: Colors.redAccent, size: 28),
            const SizedBox(height: 8),
            Text(
              error,
              textAlign: TextAlign.center,
              style: GoogleFonts.spaceGrotesk(fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }
}
