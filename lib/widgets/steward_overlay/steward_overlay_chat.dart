import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../providers/voice_settings_provider.dart';
import '../../screens/home_screen.dart';
import '../../services/deep_link/uri_router.dart';
import '../../services/voice/cloud_stt.dart';
import '../../services/voice/recording_controller.dart';
import '../../services/voice/voice_recording_session.dart';
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
    final voiceSettings = ref.watch(voiceSettingsProvider);
    final voiceNotifier = ref.read(voiceSettingsProvider.notifier);
    Future<VoiceRecordingSession?> voiceStarter() async {
      final apiKey = await voiceNotifier.readApiKey();
      if (apiKey == null || apiKey.isEmpty) return null;
      return VoiceRecordingSession(
        recording: RecordingController(),
        cloudStt: AlibabaWebSocketStt(
          apiKey: apiKey,
          region: voiceSettings.region,
          model: voiceSettings.model,
        ),
        languageHints: voiceSettings.languageHints,
      );
    }

    return _ChatInput(
      key: const ValueKey('steward-overlay-chat-input'),
      onSend: parent._sendMessage,
      canAttachImages: parent._canAttachImages,
      canAttachPdfs: parent._canAttachPdfs,
      canAttachAudio: parent._canAttachAudio,
      canAttachVideo: parent._canAttachVideo,
      voiceEnabled: voiceSettings.isReady,
      voiceStarter: voiceStarter,
      voiceAutoSendOnHold: () =>
          ref.read(voiceSettingsProvider).autoSendPuckTranscripts,
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

  /// Last message count we know about. Tracked so the scroll-to-end
  /// fires on the very first frame (panel open) AND on every length
  /// growth (new SSE message arriving while the panel is open).
  int _lastSeenLength = -1;

  @override
  void dispose() {
    _scrollCtrl.dispose();
    super.dispose();
  }

  /// Defer the jump-to-end to after the current frame so the
  /// ScrollController has bound to its list and `maxScrollExtent` is
  /// known. Cheap no-op when there's no client yet.
  void _scheduleScrollToEnd() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !_scrollCtrl.hasClients) return;
      final max = _scrollCtrl.position.maxScrollExtent;
      _scrollCtrl.jumpTo(max);
    });
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
      _lastSeenLength = 0;
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
    // Schedule a jump-to-end whenever the message count changes — the
    // very first build (panel just opened) hits this branch with
    // `_lastSeenLength == -1`, so the user lands on the latest
    // exchange instead of the top of the cached history.
    if (state.messages.length != _lastSeenLength) {
      _lastSeenLength = state.messages.length;
      _scheduleScrollToEnd();
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

  /// Path C voice input — when true AND the input is empty, the send
  /// icon is replaced by a long-press mic affordance. Plumbed from the
  /// `voiceSettingsProvider.isReady` check in [_ChatInputSlot] so the
  /// input stays a non-Consumer widget.
  final bool voiceEnabled;

  /// Async factory returning a configured [VoiceRecordingSession] (or
  /// null if the API key is missing). The starter reads the API key
  /// from secure storage at long-press time so the secret never lives
  /// in widget state.
  final Future<VoiceRecordingSession?> Function()? voiceStarter;

  /// On-demand getter for the auto-send-on-hold setting. Plumbed in
  /// from [_ChatInputSlot] so the State remains a non-Consumer (no
  /// ref.watch) and the v1.0.466 IME rebuild path stays clear.
  final bool Function()? voiceAutoSendOnHold;

  const _ChatInput({
    super.key,
    required this.onSend,
    this.canAttachImages = false,
    this.canAttachPdfs = false,
    this.canAttachAudio = false,
    this.canAttachVideo = false,
    this.voiceEnabled = false,
    this.voiceStarter,
    this.voiceAutoSendOnHold,
  });

  @override
  State<_ChatInput> createState() => _ChatInputState();
}

/// Which voice surface owns the currently-running session. Events from
/// the shared [VoiceRecordingSession] route to the right handler via
/// this discriminator.
enum _VoiceFlow {
  /// No session active.
  none,

  /// Long-press on the in-panel "Hold to speak" surface that replaces
  /// the text field while `_voiceComposeMode` is on. Auto-sends on
  /// release per `voiceSettings.autoSendPuckTranscripts`.
  holdToSpeak,

  /// Tap-toggle on the inline mic suffix inside the text field. Streams
  /// partials/finals directly into `_ctrl`; second tap stops.
  inlineStreaming,
}

/// Whether the user's first non-empty content came from the keyboard
/// or voice. Once set it doesn't change for the widget's lifetime. The
/// inline mic affordance is hidden when this is `keyboard` — the user
/// has signalled a typing preference, so the prompt shouldn't keep
/// inviting them to dictate.
enum _FirstInputSource { none, voice, keyboard }

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

  // Path C voice input.
  //
  // Three orthogonal surfaces live in this widget:
  //
  //   1. **Voice toggle** (left of the row, mirrors the puck) — taps
  //      flip `_voiceComposeMode`. When on, the text field is replaced
  //      by a `Hold to speak` gesture surface; long-press dictates
  //      and (per voiceSettings.autoSendPuckTranscripts) either
  //      auto-sends or drops the transcript into the input for review.
  //   2. **Hold-to-speak surface** (center, only while
  //      `_voiceComposeMode` is on) — long-press start/end/move
  //      drives Mode-A semantics in-panel.
  //   3. **Inline streaming mic** (TextField suffix, only while
  //      `_voiceComposeMode` is off and the input is empty) —
  //      tap-toggle streams partials/finals directly into `_ctrl`.
  //
  // All three reuse a single session field; `_activeFlow` records
  // which surface owns the currently-running session so events route
  // to the right handler.
  bool _voiceComposeMode = false;
  _VoiceFlow _activeFlow = _VoiceFlow.none;
  _FirstInputSource _firstInputSource = _FirstInputSource.none;

  VoiceRecordingSession? _voiceSession;
  StreamSubscription<VoiceSessionEvent>? _voiceSub;
  bool _voiceStarting = false;
  // Recording-state of the hold-to-speak surface — drives the red
  // pulse + transcript preview inside the gesture box.
  String _holdTranscript = '';
  bool _holdRecording = false;
  // Snapshot of the input text at the moment voice recording started.
  // Restored on cancel so the user doesn't lose what they typed.
  String _voiceSavedText = '';

  /// Drives the inline-mic suffix icon visibility. **Critical** — the
  /// suffix icon depends on `_ctrl.text.isEmpty`, so we'd ordinarily
  /// wrap the TextField in a `ListenableBuilder` listening to `_ctrl`.
  /// We don't, because that fires on every keystroke and rebuilds the
  /// TextField → re-runs `EditableText.didUpdateWidget` → can poke
  /// the IME's `setEditingState` and bounce the predictive word cache
  /// (root cause of the v1.0.466 deleted-text-returning bug, AND its
  /// v1.0.539 regression). Instead we keep a `ValueNotifier<bool>`
  /// here, recompute on `_ctrl` notifications, and let the notifier's
  /// `==` check dedupe to fire only when the emptiness state actually
  /// flips. The TextField itself isn't wrapped in any per-keystroke
  /// builder; only the small ValueListenableBuilder inside its
  /// `suffixIcon` slot rebuilds.
  final ValueNotifier<bool> _showInlineMicHint = ValueNotifier(false);

  @override
  void initState() {
    super.initState();
    // Recompute on every text change. The notifier dedupes via `==`,
    // so most keystrokes (where emptiness doesn't flip) don't trigger
    // a listener notification at all — keeping the suffix icon stable
    // and the TextField widget unrebuilt.
    _ctrl.addListener(_recomputeShowInlineMicHint);
    _recomputeShowInlineMicHint();
  }

  void _recomputeShowInlineMicHint() {
    final isEmpty = _ctrl.text.isEmpty;
    final streaming = _activeFlow == _VoiceFlow.inlineStreaming &&
        _voiceSession != null;
    final hintAvailable = widget.voiceEnabled &&
        isEmpty &&
        _firstInputSource != _FirstInputSource.keyboard;
    final show = hintAvailable || streaming;
    if (_showInlineMicHint.value != show) {
      _showInlineMicHint.value = show;
    }
  }

  @override
  void didUpdateWidget(covariant _ChatInput oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.voiceEnabled != widget.voiceEnabled) {
      _recomputeShowInlineMicHint();
    }
  }

  @override
  void dispose() {
    _voiceSub?.cancel();
    _voiceSession?.dispose();
    _ctrl.removeListener(_recomputeShowInlineMicHint);
    _showInlineMicHint.dispose();
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

  // ===== Voice — shared session lifecycle =====

  /// Builds + starts a session, attaching the event subscription. Sets
  /// `_activeFlow` BEFORE start() so the first event can dispatch
  /// correctly. Returns true on success; surfaces a SnackBar + cleans
  /// up on failure.
  Future<bool> _startVoiceSession(_VoiceFlow flow) async {
    if (_voiceSession != null || _voiceStarting) return false;
    if (widget.voiceStarter == null) return false;
    setState(() => _voiceStarting = true);
    VoiceRecordingSession? session;
    try {
      session = await widget.voiceStarter!.call();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Voice unavailable: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _voiceStarting = false);
    }
    if (session == null || !mounted) {
      await session?.dispose();
      return false;
    }
    _voiceSavedText = _ctrl.text;
    _voiceSession = session;
    _activeFlow = flow;
    _recomputeShowInlineMicHint();
    _voiceSub = session.events.listen(_onVoiceEvent);
    try {
      await session.start();
      if (mounted) setState(() {});
      return true;
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Mic unavailable: $e')),
        );
      }
      await _cleanupVoiceSession();
      return false;
    }
  }

  void _onVoiceEvent(VoiceSessionEvent e) {
    if (!mounted) return;
    switch (_activeFlow) {
      case _VoiceFlow.holdToSpeak:
        _handleHoldToSpeakEvent(e);
      case _VoiceFlow.inlineStreaming:
        _handleInlineStreamingEvent(e);
      case _VoiceFlow.none:
        // Defensive — clean up if a stray event arrives after cleanup.
        _cleanupVoiceSession();
    }
  }

  void _handleHoldToSpeakEvent(VoiceSessionEvent e) {
    switch (e.kind) {
      case VoiceSessionEventKind.transcriptUpdated:
        setState(() => _holdTranscript = e.text);
      case VoiceSessionEventKind.completed:
        final text = e.text.trim();
        if (text.isNotEmpty) {
          _markFirstInputIfNone(_FirstInputSource.voice);
          final autoSend = widget.voiceAutoSendOnHold?.call() ?? false;
          if (autoSend) {
            // Route through the regular _send so any staged
            // attachments ride along — same outbound shape as a
            // keyboard send.
            _ctrl.text = text;
            // Schedule on a microtask so cleanup state settles before
            // _send awaits the network call.
            scheduleMicrotask(_send);
          } else {
            // Drop transcript into the input + revert to text mode so
            // the user can review + tap send.
            _ctrl.value = TextEditingValue(
              text: text,
              selection: TextSelection.collapsed(offset: text.length),
            );
            setState(() => _voiceComposeMode = false);
          }
        }
        _cleanupVoiceSession();
      case VoiceSessionEventKind.cancelled:
        _cleanupVoiceSession();
      case VoiceSessionEventKind.maxDurationReached:
        // Session auto-calls stop(); a completed event will follow.
        break;
      case VoiceSessionEventKind.error:
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Voice error: ${e.error}')),
        );
        _cleanupVoiceSession();
    }
  }

  void _handleInlineStreamingEvent(VoiceSessionEvent e) {
    switch (e.kind) {
      case VoiceSessionEventKind.transcriptUpdated:
      case VoiceSessionEventKind.completed:
        if (e.text.isNotEmpty) {
          _markFirstInputIfNone(_FirstInputSource.voice);
        }
        _ctrl.value = TextEditingValue(
          text: e.text,
          selection: TextSelection.collapsed(offset: e.text.length),
        );
        if (e.kind == VoiceSessionEventKind.completed) {
          _cleanupVoiceSession();
        }
      case VoiceSessionEventKind.cancelled:
        _ctrl.value = TextEditingValue(
          text: _voiceSavedText,
          selection:
              TextSelection.collapsed(offset: _voiceSavedText.length),
        );
        _cleanupVoiceSession();
      case VoiceSessionEventKind.maxDurationReached:
        break;
      case VoiceSessionEventKind.error:
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Voice error: ${e.error}')),
        );
        _cleanupVoiceSession();
    }
  }

  Future<void> _cleanupVoiceSession() async {
    await _voiceSub?.cancel();
    _voiceSub = null;
    final s = _voiceSession;
    _voiceSession = null;
    _activeFlow = _VoiceFlow.none;
    _holdTranscript = '';
    _holdRecording = false;
    _recomputeShowInlineMicHint();
    if (mounted) setState(() {});
    await s?.dispose();
  }

  void _markFirstInputIfNone(_FirstInputSource src) {
    if (_firstInputSource == _FirstInputSource.none) {
      _firstInputSource = src;
      _recomputeShowInlineMicHint();
    }
  }

  // ===== Voice toggle (left of the row) =====

  Future<void> _toggleVoiceComposeMode() async {
    // Cancel any active session before swapping surfaces — leaving a
    // session running while the UI changes underneath causes the
    // stream listener to fire into the wrong handler.
    if (_voiceSession != null) {
      await _voiceSession?.cancel();
    }
    if (!mounted) return;
    setState(() => _voiceComposeMode = !_voiceComposeMode);
  }

  // ===== Hold-to-speak (center, voice compose mode) =====

  Future<void> _onHoldToSpeakStart(LongPressStartDetails _) async {
    if (!_voiceComposeMode) return;
    setState(() => _holdRecording = true);
    final ok = await _startVoiceSession(_VoiceFlow.holdToSpeak);
    if (!ok && mounted) setState(() => _holdRecording = false);
  }

  Future<void> _onHoldToSpeakEnd(LongPressEndDetails _) async {
    if (_voiceSession != null && _activeFlow == _VoiceFlow.holdToSpeak) {
      await _voiceSession?.stop();
    }
    if (mounted) setState(() => _holdRecording = false);
  }

  void _onHoldToSpeakMoveUpdate(LongPressMoveUpdateDetails d) {
    if (d.offsetFromOrigin.distance > 80) {
      _voiceSession?.cancel();
    }
  }

  // ===== Inline streaming mic (TextField suffix) =====

  Future<void> _onInlineMicTap() async {
    if (_activeFlow == _VoiceFlow.inlineStreaming &&
        _voiceSession != null) {
      // Tap-toggle off: stop the running session; completed event
      // commits the final transcript.
      await _voiceSession?.stop();
      return;
    }
    if (_voiceSession != null) {
      // Another flow is active; ignore the tap.
      return;
    }
    await _startVoiceSession(_VoiceFlow.inlineStreaming);
  }

  void _onTextFieldChanged(String _) {
    // `onChanged` only fires from user keyboard input — programmatic
    // `_ctrl.value = …` writes (from voice events) don't trigger it.
    // So a single call here is enough to fingerprint the user's first
    // input as keyboard-driven. Once set the inline mic hides whenever
    // the field is empty, signalling "this user prefers typing".
    _markFirstInputIfNone(_FirstInputSource.keyboard);
  }

  // ===== Center-surface builders =====

  Widget _buildTextField() {
    // **IME-friendly defaults.** Earlier revisions set
    // `autocorrect: false` + `enableSuggestions: false` as
    // belt-and-suspenders for the v1.0.466 deleted-text-returning
    // bug. v1.0.472 fixed that bug architecturally via rebuild-
    // scope isolation; v1.0.541 reapplied the same lesson here
    // (no per-keystroke ListenableBuilder around this TextField).
    // The defensive flags break CJK input — drop both.
    //
    // **Do not add `autofillHints: const []`.** An empty list
    // poisons some Android+Gboard combinations; `null` is correct.
    return TextField(
      controller: _ctrl,
      focusNode: _focus,
      minLines: 1,
      maxLines: 4,
      keyboardType: TextInputType.multiline,
      textInputAction: TextInputAction.newline,
      onChanged: _onTextFieldChanged,
      decoration: InputDecoration(
        isDense: true,
        hintText: 'Ask the steward…',
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(10),
        ),
        contentPadding: const EdgeInsets.symmetric(
            horizontal: 12, vertical: 10),
        // The suffix is reactive — but only the IconButton inside the
        // ValueListenableBuilder rebuilds on emptiness flips. The
        // outer TextField is stable across every keystroke.
        suffixIcon: ValueListenableBuilder<bool>(
          valueListenable: _showInlineMicHint,
          builder: (context, show, _) {
            if (!show) return const SizedBox.shrink();
            final streaming = _activeFlow == _VoiceFlow.inlineStreaming &&
                _voiceSession != null;
            return IconButton(
              tooltip: streaming ? 'Stop dictation' : 'Start dictation',
              icon: Icon(
                streaming ? Icons.mic : Icons.mic_none,
                size: 20,
                color:
                    streaming ? DesignColors.error : DesignColors.primary,
              ),
              onPressed:
                  (_sending || _voiceStarting) ? null : _onInlineMicTap,
              padding: EdgeInsets.zero,
              visualDensity: VisualDensity.compact,
              constraints:
                  const BoxConstraints(minWidth: 32, minHeight: 32),
            );
          },
        ),
        suffixIconConstraints:
            const BoxConstraints(minWidth: 32, minHeight: 32),
      ),
    );
  }

  Widget _buildHoldToSpeakSurface() {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final recording = _holdRecording;
    final starting = _voiceStarting;
    final hint = starting
        ? 'Starting…'
        : recording
            ? (_holdTranscript.isEmpty ? 'Listening…' : _holdTranscript)
            : 'Hold to speak';
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onLongPressStart: _onHoldToSpeakStart,
      onLongPressEnd: _onHoldToSpeakEnd,
      onLongPressMoveUpdate: _onHoldToSpeakMoveUpdate,
      child: Container(
        constraints: const BoxConstraints(minHeight: 44),
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color: recording
              ? DesignColors.error.withValues(alpha: 0.10)
              : (isDark
                  ? Colors.white.withValues(alpha: 0.04)
                  : Colors.black.withValues(alpha: 0.03)),
          borderRadius: BorderRadius.circular(10),
          border: Border.all(
            color: recording
                ? DesignColors.error.withValues(alpha: 0.55)
                : (isDark
                    ? Colors.white24
                    : Colors.black26),
          ),
        ),
        child: Row(
          children: [
            Icon(
              recording ? Icons.mic : Icons.mic_none,
              size: 18,
              color: recording ? DesignColors.error : DesignColors.primary,
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                hint,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 13,
                  fontWeight:
                      recording ? FontWeight.w600 : FontWeight.w500,
                  color: recording
                      ? DesignColors.error
                      : (isDark
                          ? Colors.white.withValues(alpha: 0.78)
                          : DesignColors.textPrimary
                              .withValues(alpha: 0.7)),
                ),
              ),
            ),
          ],
        ),
      ),
    );
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
              // Voice toggle — sits in what used to be the leftmost
              // attach position. Tap flips `_voiceComposeMode`; the
              // icon mirrors the active mode (keyboard ↔ mic). Hidden
              // when the voice setting is off so the row collapses to
              // a pure-text composer.
              if (widget.voiceEnabled)
                IconButton(
                  tooltip: _voiceComposeMode
                      ? 'Switch to keyboard'
                      : 'Switch to voice',
                  onPressed: (_sending || _voiceStarting)
                      ? null
                      : _toggleVoiceComposeMode,
                  icon: Icon(
                    _voiceComposeMode ? Icons.keyboard : Icons.mic_none,
                    size: 22,
                    color: _voiceComposeMode
                        ? DesignColors.primary
                        : null,
                  ),
                  padding: EdgeInsets.zero,
                  visualDensity: VisualDensity.compact,
                  constraints:
                      const BoxConstraints(minWidth: 36, minHeight: 36),
                ),
              // Center surface — either the text field (with optional
              // inline streaming mic suffix) or the hold-to-speak
              // gesture box, depending on `_voiceComposeMode`.
              Expanded(
                child: _voiceComposeMode
                    ? _buildHoldToSpeakSurface()
                    : _buildTextField(),
              ),
              const SizedBox(width: 6),
              // Attach buttons — moved from the left of the row to the
              // right, so the steward composer reads
              // `[voice] [field] [attach] [send]` instead of the old
              // `[attach] [field] [send/mic]` shape that conflated the
              // voice button with send.
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
              IconButton(
                tooltip: 'Send',
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
    unawaited(navigateToUri(
      context,
      uri,
      hub: hub,
      setTab: (i) => ref.read(currentTabProvider.notifier).setTab(i),
      refreshHub: () async {
        await ref.read(hubProvider.notifier).refreshAll();
        return ref.read(hubProvider).value;
      },
    ));
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
