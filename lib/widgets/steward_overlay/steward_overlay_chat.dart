import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/connection_provider.dart' show connectionsProvider;
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
import 'voice_recording_hud.dart';

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

class _ChatInputState extends State<_ChatInput> {
  final _ctrl = TextEditingController();
  // Owning the focus node here (rather than letting the framework mint
  // a transient one per build) keeps the IME connection stable across
  // parent rebuilds — same reasoning as the dedicated controller. A
  // freshly-minted FocusNode every build can race the IME attach/detach
  // cycle and exacerbate the predictive-restore bug below.
  final _focus = FocusNode(debugLabel: 'overlayChatInput');

  // **v1.0.561 — ghost FocusNode for IME re-attach.** The overlay
  // TextField suffers an undocumented Flutter limitation in
  // MaterialApp.builder mounted contexts: within a long-lived
  // InputConnection, `setEditingState` pushes from Dart don't
  // propagate cursor/text updates to the IME. Symptoms: cursor jumps
  // to end on input after a cursor-move; programmatic writes (voice)
  // wiped on subsequent typing. v1.0.555–560 chased rebuild storms,
  // IME flags, Scaffold plumbing, and explicit FocusScope — none
  // moved the bug. User's diagnostic clue: tapping a sibling
  // TextField then tapping back makes editing work for one round
  // trip. Mechanism: switching focus between distinct
  // TextField/EditableText pairs creates fresh InputConnections,
  // which forces the IME to drop its stale cache.
  //
  // The ghost TextField below is parked offscreen (Positioned at
  // -1000,-1000) inside this widget's Stack. When we detect a
  // moment that previously caused desync (cursor-only change via
  // user tap, or programmatic _ctrl mutation from voice / pick), we
  // briefly focus the ghost then refocus the real input on
  // postFrame — automating the user's manual workaround.
  final _ghostController = TextEditingController();
  final _ghostFocus = FocusNode(debugLabel: 'overlayChatInputGhost');
  TextEditingValue? _lastCtrlValue;
  bool _programmaticMutation = false;
  bool _isResyncing = false;

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
  //      flip `_voiceComposeMode`. The button is purely a mode
  //      switcher (Icons.record_voice_over_outlined ↔
  //      Icons.keyboard_alt_outlined) and is visually distinct from
  //      the inline recording-toggle mic (`mic_none` / `stop_circle`)
  //      so testers don't conflate the two.
  //   2. **Hold-to-speak surface** (center, only while
  //      `_voiceComposeMode` is on) — long-press start/end/move
  //      drives Mode-A semantics in-panel. While held, the same Mode A
  //      HUD floats above the input row with timer + transcript.
  //   3. **Inline streaming mic** (TextField suffix, always present
  //      when voiceEnabled is true) — tap-toggle streams partials/
  //      finals; on completion the transcript is *appended* to any
  //      pre-existing typed text rather than replacing it.
  //
  // All three reuse a single session field; `_activeFlow` records
  // which surface owns the currently-running session so events route
  // to the right handler.
  bool _voiceComposeMode = false;
  _VoiceFlow _activeFlow = _VoiceFlow.none;

  VoiceRecordingSession? _voiceSession;
  StreamSubscription<VoiceSessionEvent>? _voiceSub;
  bool _voiceStarting = false;
  // Recording-state of the hold-to-speak surface — drives the red
  // pulse + transcript preview inside the gesture box AND the floating
  // Mode A HUD above the input row.
  String _holdTranscript = '';
  bool _holdRecording = false;
  Duration _holdElapsed = Duration.zero;
  DateTime? _holdStartTime;
  Timer? _holdTickTimer;
  // Snapshot of the input text at the moment voice recording started.
  // Restored on cancel so the user doesn't lose what they typed; on
  // completion the dictated transcript is appended to the snapshot.
  String _voiceSavedText = '';

  // **No `_ctrl.addListener` here.** Earlier revisions registered a
  // listener that drove the inline-mic suffix icon (visible only when
  // the field was empty). Even a deduped ValueNotifier-backed listener
  // turned out to bounce Gboard's predictive cache on the
  // first-keystroke transition, re-emitting `setEditingState` and
  // making deleted characters reappear (v1.0.466 / v1.0.539 / v1.0.541
  // repeats of the same bug). The reliable fix is to keep the suffix
  // entirely static across text changes — the inline mic is always
  // present when voice is enabled, and its appearance only depends on
  // `_activeFlow` / `_voiceStarting`, both of which mutate through
  // setState (user-driven events, never keystrokes).

  // v1.0.548 cached the TextField widget identity to short-circuit
  // Flutter's Element.updateChild on rebuild. That defended against a
  // hypothesised rebuild path, but the IME delete bug persisted —
  // proving the bug wasn't about widget identity. Cache removed in
  // v1.0.551 to keep this file simpler; the architectural fixes that
  // do matter live elsewhere (no _ctrl listener; v1.0.549 panel-
  // position freeze; the enableIMEPersonalizedLearning flag on the
  // TextField itself).

  @override
  void initState() {
    super.initState();
    _lastCtrlValue = _ctrl.value;
    _ctrl.addListener(_onCtrlChanged);
  }

  /// Detect the conditions that cause IME state desync and trigger a
  /// ghost-focus-bounce to force InputConnection re-creation.
  ///
  /// Three triggers:
  ///   - **Cursor-only change** (text unchanged, selection changed):
  ///     user tapped to move the cursor. Within an active
  ///     InputConnection, the IME doesn't pick up the new selection;
  ///     subsequent typing appends at the IME's stale cursor.
  ///   - **Programmatic mutation** (flagged before voice/pick writes):
  ///     voice or attach handlers wrote to `_ctrl.value`. The IME
  ///     doesn't see the new text via setEditingState; subsequent
  ///     typing wipes the programmatic write.
  ///   - **Delete or replace** (v1.0.562 — text changed but isn't a
  ///     pure append, AND composing isn't involved): user backspaced
  ///     or used a selection-replace. The IME's cache appears to stay
  ///     at the pre-deletion state in our MaterialApp.builder context,
  ///     so the next keystroke restores the deleted text and appends
  ///     after. **Composing-involved transitions are skipped** so CJK
  ///     candidate selection (composing "ni" → committed "你") still
  ///     works — CJK commits change `composing.isValid` between before
  ///     and after, which we use as the discriminator.
  ///
  /// Bounce: focus ghost FocusNode then refocus real one on postFrame.
  /// `_isResyncing` debounces re-entry.
  void _onCtrlChanged() {
    final cur = _ctrl.value;
    final last = _lastCtrlValue;
    _lastCtrlValue = cur;
    if (last == null || _isResyncing) return;
    if (!_focus.hasFocus) return; // not actively editing

    final textChanged = last.text != cur.text;
    final isPureAppend = textChanged &&
        cur.text.length > last.text.length &&
        cur.text.startsWith(last.text);
    final involvesComposing =
        last.composing.isValid || cur.composing.isValid;
    final cursorOnly = !textChanged && last.selection != cur.selection;
    final isDeleteOrReplace =
        textChanged && !isPureAppend && !involvesComposing;
    final wasProgrammatic = _programmaticMutation;
    _programmaticMutation = false;

    if (cursorOnly || wasProgrammatic || isDeleteOrReplace) {
      _bounceFocusForImeResync();
    }
  }

  void _bounceFocusForImeResync() {
    _isResyncing = true;
    _ghostFocus.requestFocus();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) {
        _isResyncing = false;
        return;
      }
      _focus.requestFocus();
      _isResyncing = false;
    });
  }

  @override
  void dispose() {
    _holdTickTimer?.cancel();
    _voiceSub?.cancel();
    _voiceSession?.dispose();
    _ctrl.removeListener(_onCtrlChanged);
    _ghostController.dispose();
    _ghostFocus.dispose();
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
      _programmaticMutation = true;
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
          final autoSend = widget.voiceAutoSendOnHold?.call() ?? false;
          if (autoSend) {
            // Route through the regular _send so any staged
            // attachments ride along — same outbound shape as a
            // keyboard send.
            _programmaticMutation = true;
            _ctrl.text = text;
            // Schedule on a microtask so cleanup state settles before
            // _send awaits the network call.
            scheduleMicrotask(_send);
          } else {
            // Drop transcript into the input + revert to text mode so
            // the user can review + tap send.
            _programmaticMutation = true;
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
        // **Append, don't replace.** Earlier revisions wrote
        // `_ctrl.text = e.text` directly, which clobbered any text the
        // user had typed before tapping the inline mic. Now the saved
        // prefix is preserved verbatim and the dictated transcript is
        // concatenated with a single space separator (only when the
        // prefix doesn't already end in whitespace).
        final dictated = e.text;
        final base = _voiceSavedText;
        final needsSeparator =
            base.isNotEmpty && dictated.isNotEmpty && !base.endsWith(' ');
        final combined = needsSeparator ? '$base $dictated' : '$base$dictated';
        _programmaticMutation = true;
        _ctrl.value = TextEditingValue(
          text: combined,
          selection: TextSelection.collapsed(offset: combined.length),
        );
        if (e.kind == VoiceSessionEventKind.completed) {
          _cleanupVoiceSession();
        }
      case VoiceSessionEventKind.cancelled:
        _programmaticMutation = true;
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
    _holdTickTimer?.cancel();
    _holdTickTimer = null;
    _holdStartTime = null;
    await _voiceSub?.cancel();
    _voiceSub = null;
    final s = _voiceSession;
    _voiceSession = null;
    _activeFlow = _VoiceFlow.none;
    _holdTranscript = '';
    _holdRecording = false;
    _holdElapsed = Duration.zero;
    if (mounted) setState(() {});
    await s?.dispose();
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
    _holdTickTimer?.cancel();
    _holdStartTime = DateTime.now();
    _holdTickTimer = Timer.periodic(const Duration(seconds: 1), (_) {
      if (!mounted || _holdStartTime == null) return;
      setState(() {
        _holdElapsed = DateTime.now().difference(_holdStartTime!);
      });
    });
    setState(() {
      _holdRecording = true;
      _holdElapsed = Duration.zero;
      _holdTranscript = '';
    });
    final ok = await _startVoiceSession(_VoiceFlow.holdToSpeak);
    if (!ok && mounted) {
      _holdTickTimer?.cancel();
      _holdTickTimer = null;
      _holdStartTime = null;
      setState(() => _holdRecording = false);
    }
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

  // ===== Center-surface builders =====

  Widget _buildTextField() {
    // **IME-friendly defaults.** Earlier revisions set
    // `autocorrect: false` + `enableSuggestions: false` as
    // belt-and-suspenders for the v1.0.466 deleted-text-returning
    // bug. v1.0.472 fixed that bug architecturally via rebuild-
    // scope isolation. The defensive flags break CJK input.
    //
    // **Do not add `autofillHints: const []`.** An empty list
    // poisons some Android+Gboard combinations; `null` is correct.
    //
    // **No `onChanged` callback.** Anything that reacts to a
    // keystroke risks bouncing the IME's predictive-cache state.
    // The earlier `_onTextFieldChanged` only set a one-shot
    // first-input-source flag for the inline-mic hint — both the
    // flag and the hint are gone (v1.0.545); the inline mic is now
    // always present when voice is enabled.
    //
    // **Suffix is a plain IconButton.** Earlier revisions wrapped
    // it in a ValueListenableBuilder driven by a `_ctrl` listener so
    // the mic could hide once the field became non-empty. The
    // listener fired per keystroke, and on Android+Gboard that
    // path repeatedly bounced setEditingState — the exact v1.0.466
    // shape. Keeping the suffix entirely static across text
    // changes is the only mechanism that survives all the IME
    // edge cases. Mic state ride entirely on `_activeFlow` /
    // `_voiceStarting`, which only flip via user-driven setState.
    final streaming = _activeFlow == _VoiceFlow.inlineStreaming &&
        _voiceSession != null;
    final voiceEnabled = widget.voiceEnabled;
    final disabled = _sending || _voiceStarting;
    return TextField(
      controller: _ctrl,
      focusNode: _focus,
      minLines: 1,
      maxLines: 4,
      keyboardType: TextInputType.multiline,
      textInputAction: TextInputAction.newline,
      // **v1.0.558 — strip the four defensive IME flags.** v1.0.551/.552
      // added `enableIMEPersonalizedLearning: false`, `autocorrect: false`,
      // `smartDashesType.disabled`, `smartQuotesType.disabled` to fight
      // the recurring "deleted text returns + cursor jumps to end" bug.
      // None of those fixes moved the bug. Tester clue in v1.0.557:
      // repro is "type, tap cursor mid-text, type new char — old content
      // restored + char appended at end", which is an IME state desync
      // (cursor-position not respected by the IME), NOT a Flutter-side
      // rebuild storm. Working session-compose (`agent_compose.dart`) has
      // ZERO of these flags and works correctly across CJK + English on
      // WeChat input method + Gboard. Hypothesis: the flag stack
      // (especially `IME_FLAG_NO_PERSONALIZED_LEARNING` +
      // `IME_FLAG_NO_AUTOCORRECT` together) pushes some Android IMEs
      // into a non-standard EditorInfo mode that stops respecting
      // `setEditingState` cursor updates. Reverting to a bare TextField
      // matches the proven-good compose pattern.
      decoration: InputDecoration(
        isDense: true,
        hintText: 'Ask the steward…',
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(10),
        ),
        contentPadding: const EdgeInsets.symmetric(
            horizontal: 12, vertical: 10),
        suffixIcon: voiceEnabled
            ? IconButton(
                tooltip: streaming ? 'Stop dictation' : 'Start dictation',
                icon: Icon(
                  streaming ? Icons.stop_circle : Icons.mic_none,
                  size: 20,
                  color: streaming
                      ? DesignColors.error
                      : DesignColors.primary,
                ),
                onPressed: disabled ? null : _onInlineMicTap,
                padding: EdgeInsets.zero,
                visualDensity: VisualDensity.compact,
                constraints:
                    const BoxConstraints(minWidth: 32, minHeight: 32),
              )
            : null,
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
          _programmaticMutation = true;
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
    // The compose box's hold-to-speak gesture is the same Mode A
    // semantics as the puck long-press; show the same RECORDING HUD
    // floating above the input row so the user gets identical "you
    // are LIVE" feedback wherever they triggered Mode A from.
    final showHoldHud = _holdRecording || _voiceStarting;
    return Stack(
      clipBehavior: Clip.none,
      children: [
        // **Ghost TextField for IME re-attach trick.** Parked offscreen
        // at (-1000, -1000) with 1x1 size. Used by
        // `_bounceFocusForImeResync` to force the IME to drop its stale
        // InputConnection cache by transferring focus to a distinct
        // TextField/EditableText and back. See `_onCtrlChanged` and the
        // v1.0.561 field-doc on `_ghostFocus` for full rationale. Stack
        // has `clipBehavior: Clip.none` already, so the offscreen
        // position renders without being clipped away (which would
        // prevent the EditableText from being laid out and focusable).
        Positioned(
          left: -1000,
          top: -1000,
          width: 1,
          height: 1,
          child: IgnorePointer(
            child: TextField(
              controller: _ghostController,
              focusNode: _ghostFocus,
              decoration: const InputDecoration.collapsed(hintText: ''),
            ),
          ),
        ),
        Padding(
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
            // **Stable keys on every child — v1.0.553 fix.** Without
            // keys, Flutter matches children by position. When
            // `voiceEnabled` async-loads to true (from
            // `voiceSettingsProvider._load()`) OR `canAttach*` resolves
            // to true (from `_resolveCapabilityIfNeeded` finishing the
            // post-frame callback), the Row's children list count
            // changes, every child shifts by one index, and Flutter
            // destroys-then-rebuilds the `Expanded(TextField)` to fit
            // the new position. That destroyed-and-rebuilt EditableText
            // reopens its IME connection, sends `setEditingState` with
            // the current `_ctrl.value`, and clobbers whatever the
            // user was typing — visible as "type hello, backspace 5×,
            // type x, field shows hellox" + "tap before 'o', type a,
            // cursor jumps to end + a goes after." Both are the EXACT
            // v1.0.466 signature; the recurring "isolation" fixes
            // missed this path because the Row was simple in v1.0.467
            // and only grew dynamic when voice / image / multimodal
            // attach features arrived.
            //
            // With ValueKey'd children, Flutter matches by key, the
            // Expanded element survives index shifts, and the
            // TextField/EditableText stays mounted. State preserved,
            // IME connection preserved, no `setEditingState` clobber.
            children: [
              if (widget.voiceEnabled)
                IconButton(
                  key: const ValueKey('chat-row-voice-toggle'),
                  tooltip: _voiceComposeMode
                      ? 'Switch to keyboard input'
                      : 'Switch to voice input',
                  onPressed: (_sending || _voiceStarting)
                      ? null
                      : _toggleVoiceComposeMode,
                  icon: Icon(
                    _voiceComposeMode
                        ? Icons.keyboard_alt_outlined
                        : Icons.record_voice_over_outlined,
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
              // gesture box, depending on `_voiceComposeMode`. Keyed
              // because this is THE one that mustn't remount.
              Expanded(
                key: const ValueKey('chat-row-input'),
                child: _voiceComposeMode
                    ? _buildHoldToSpeakSurface()
                    : _buildTextField(),
              ),
              const SizedBox(
                  key: ValueKey('chat-row-spacer'), width: 6),
              if (canAttach)
                IconButton(
                  key: const ValueKey('chat-row-attach-image'),
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
                key: const ValueKey('chat-row-attach-text'),
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
                  key: const ValueKey('chat-row-attach-multimodal'),
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
                key: const ValueKey('chat-row-send'),
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
    ),
        if (showHoldHud && _voiceComposeMode)
          Positioned(
            left: 0,
            right: 0,
            bottom: 60,
            child: IgnorePointer(
              child: Align(
                alignment: Alignment.center,
                child: VoiceRecordingHud(
                  transcript: _holdTranscript,
                  elapsed: _holdElapsed,
                ),
              ),
            ),
          ),
      ],
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
    final connections = ref.read(connectionsProvider).connections;
    unawaited(navigateToUri(
      context,
      uri,
      hub: hub,
      connections: connections,
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
