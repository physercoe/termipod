import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:webview_flutter/webview_flutter.dart';

import '../../providers/hub_provider.dart';
import '../../services/artifact_manifest/artifact_manifest.dart';
import '../../theme/design_colors.dart';

/// CDN allowlist for sandboxed canvas-app WebViews (W4 of
/// `docs/plans/canvas-viewer.md`, locked 2026-05-11). HTTPS-only; any
/// other host is blocked by the navigation delegate. Adding a CDN is
/// a deliberate code change, not a runtime toggle.
const Set<String> kCanvasAllowedCdnHosts = <String>{
  'cdn.jsdelivr.net',
  'unpkg.com',
  'cdnjs.cloudflare.com',
  'esm.sh',
};

/// Renders a `canvas-app`-kind artifact (wave 2 W2 of
/// artifact-type-registry; canvas-viewer plan W2). The body is an
/// AFM-V1 multi-file manifest — usually HTML + JS + CSS — that we
/// resolve, inline into a single self-contained HTML document, and
/// render inside a sandboxed WebView whose navigation delegate
/// restricts outbound traffic to a small CDN allowlist + data URIs.
///
/// The interaction model is **read-only for the agent**: the user
/// clicks/plays inside the canvas, but no state flows back as agent
/// input. When the user wants the agent to change the canvas, the
/// agent emits a new artifact version (new files) and the screen's
/// manual refresh button re-downloads + re-renders.
class ArtifactCanvasViewer extends ConsumerStatefulWidget {
  final String uri;
  final String? title;

  const ArtifactCanvasViewer({
    super.key,
    required this.uri,
    this.title,
  });

  @override
  ConsumerState<ArtifactCanvasViewer> createState() =>
      _ArtifactCanvasViewerState();
}

class _ArtifactCanvasViewerState extends ConsumerState<ArtifactCanvasViewer> {
  // Lazy so the error path (unsupported URI) and widget tests don't
  // touch the webview_flutter platform channel — keeps tests free of
  // the MissingPluginException that the channel raises in flutter_test.
  WebViewController? _controller;
  String? _error;
  bool _loading = true;
  // True once the WebView fires onPageFinished. Until then, a dark
  // overlay covers the platform view so the user doesn't see the
  // native WebView's default white background while it initialises
  // on the first cold mount (the "white splash" tester report).
  // Second open is fine because the platform-view process is already
  // warm and renders content same-frame.
  bool _pageReady = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final uri = widget.uri;
    if (!uri.startsWith('blob:sha256/')) {
      setState(() {
        _loading = false;
        _error = 'unsupported uri scheme — only hub-served blobs '
            '(blob:sha256/…) render today';
      });
      return;
    }
    final sha = uri.substring('blob:sha256/'.length);
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _error = 'hub not connected';
      });
      return;
    }
    try {
      final bytes = await client.downloadBlobCached(sha);
      if (!mounted) return;
      final decoded = jsonDecode(utf8.decode(bytes));
      final manifest = parseArtifactFileManifest(decoded);
      if (manifest == null) {
        setState(() {
          _loading = false;
          _error = 'canvas bundle parse error — body is not AFM-V1';
        });
        return;
      }
      final html = inlineCanvasBundle(manifest);
      final controller = WebViewController()
        ..setJavaScriptMode(JavaScriptMode.unrestricted)
        // Transparent WebView so the platform view's default white
        // background doesn't paint during cold init. Combined with the
        // dark overlay (see build), this gives zero-flash perceived
        // behaviour even on the very first open.
        ..setBackgroundColor(const Color(0x00000000))
        ..setNavigationDelegate(NavigationDelegate(
          onNavigationRequest: (req) async => decideCanvasNavigation(req.url),
          onPageFinished: (_) {
            if (!mounted) return;
            setState(() => _pageReady = true);
          },
        ));
      await controller.loadHtmlString(html, baseUrl: 'about:blank');
      if (!mounted) return;
      setState(() {
        _controller = controller;
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

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return _CanvasLoadError(message: _error!, uri: widget.uri);
    }
    final controller = _controller;
    if (controller == null) {
      return _CanvasLoadError(
        message: 'canvas not initialised',
        uri: widget.uri,
      );
    }
    // SizedBox.expand pins the WebView to the parent's full constraints
    // — without it, the platform view collapsed to its intrinsic size on
    // some Android builds and rendered as a partial sub-rectangle inside
    // the otherwise-empty Scaffold body (v1.0.508).
    //
    // Stack the WebView under a dark overlay that fades out once
    // onPageFinished fires. Matches the canvas bundle's expected
    // #0d0d0d background so the overlay→content transition is
    // invisible even when colours differ slightly.
    //
    // Theme caveat: the overlay is pinned to the dark palette
    // literal. A light-theme user still sees a brief dark flash on
    // first open. Tracked in
    // `docs/discussions/light-theme-parity.md` as part of the
    // deferred post-MVP light-theme wedge — fix is to read
    // `Theme.of(context).colorScheme.surface` once that audit lands.
    return SizedBox.expand(
      child: Stack(
        fit: StackFit.expand,
        children: [
          WebViewWidget(controller: controller),
          IgnorePointer(
            ignoring: _pageReady,
            child: AnimatedOpacity(
              opacity: _pageReady ? 0.0 : 1.0,
              duration: const Duration(milliseconds: 120),
              child: const ColoredBox(color: DesignColors.canvasDark),
            ),
          ),
        ],
      ),
    );
  }
}

/// Decides whether a navigation URL is permitted inside a canvas-app
/// WebView. Allowed: `about:blank` (the base URL handed to
/// `loadHtmlString`), `data:` URIs (inlined images/fonts), and HTTPS
/// requests against [kCanvasAllowedCdnHosts]. Everything else is
/// blocked at the request boundary. Public so the unit test can
/// exercise it without the webview platform channel.
@visibleForTesting
NavigationDecision decideCanvasNavigation(String url) {
  if (url == 'about:blank' ||
      url.startsWith('about:blank#') ||
      url.startsWith('about:blank?')) {
    return NavigationDecision.navigate;
  }
  if (url.startsWith('data:')) {
    return NavigationDecision.navigate;
  }
  if (url.startsWith('https://')) {
    final uri = Uri.tryParse(url);
    if (uri != null && kCanvasAllowedCdnHosts.contains(uri.host)) {
      return NavigationDecision.navigate;
    }
  }
  return NavigationDecision.prevent;
}

/// Merge an AFM-V1 manifest into a single self-contained HTML
/// document suitable for `WebViewController.loadHtmlString` with
/// `baseUrl: 'about:blank'`. Pure function — public for unit testing.
///
/// Rewrites (Q13 path resolution):
/// 1. `<script src="X">…</script>` → `<script…>…content…</script>`
///    when `X` resolves to a manifest file via [resolveManifestPath].
/// 2. `<link rel="stylesheet" href="Y">` → `<style>…content…</style>`.
/// 3. `<img src="Z">` → `<img src="data:<mime>;base64,…">`.
///
/// Unresolved URLs (CDN, missing) are left untouched; the WebView's
/// navigation delegate then enforces the W4 allowlist. CSS `url(…)`
/// rewriting is explicitly NOT done (Q11 locked) — agents inline
/// image references as `url(data:…)` themselves.
@visibleForTesting
String inlineCanvasBundle(ArtifactFileManifest manifest) {
  final entry = resolveCanvasEntry(manifest);
  if (entry == null) {
    throw const FormatException('canvas-app manifest has no HTML entry');
  }
  final byPath = <String, ArtifactFile>{
    for (final f in manifest.files) f.path: f,
  };
  var html = entry.content;
  html = _inlineScripts(html, byPath);
  html = _inlineStylesheets(html, byPath);
  html = _inlineImages(html, byPath);
  return html;
}

/// Resolves a relative URL against the AFM-V1 manifest's file list.
/// Implements Q13's rules: strip leading `./`, exact-match
/// `files[].path` (case-sensitive POSIX), reject `..` segments,
/// leading `/`, and any scheme. Public for unit testing.
@visibleForTesting
ArtifactFile? resolveManifestPath(
  String url,
  Map<String, ArtifactFile> byPath,
) {
  if (url.startsWith('http:') ||
      url.startsWith('https:') ||
      url.startsWith('data:') ||
      url.startsWith('//')) {
    return null;
  }
  if (url.startsWith('/')) return null;
  var path = url;
  if (path.startsWith('./')) path = path.substring(2);
  if (path.contains('..')) return null;
  return byPath[path];
}

final _kScriptRegex = RegExp(
  r'<script\b([^>]*)>\s*</script>',
  caseSensitive: false,
);
final _kLinkRegex = RegExp(
  r'<link\b([^>]*?)/?>',
  caseSensitive: false,
);
final _kImgRegex = RegExp(
  r'<img\b([^>]*?)/?>',
  caseSensitive: false,
);

String _inlineScripts(String html, Map<String, ArtifactFile> byPath) {
  return html.replaceAllMapped(_kScriptRegex, (m) {
    final attrs = m.group(1) ?? '';
    final src = _extractAttribute(attrs, 'src');
    if (src == null) return m.group(0)!;
    final resolved = resolveManifestPath(src, byPath);
    if (resolved == null) return m.group(0)!;
    final stripped = _stripAttribute(attrs, 'src');
    return '<script$stripped>${resolved.content}</script>';
  });
}

String _inlineStylesheets(String html, Map<String, ArtifactFile> byPath) {
  return html.replaceAllMapped(_kLinkRegex, (m) {
    final attrs = m.group(1) ?? '';
    final rel = _extractAttribute(attrs, 'rel');
    if (rel == null || rel.toLowerCase() != 'stylesheet') {
      return m.group(0)!;
    }
    final href = _extractAttribute(attrs, 'href');
    if (href == null) return m.group(0)!;
    final resolved = resolveManifestPath(href, byPath);
    if (resolved == null) return m.group(0)!;
    return '<style>${resolved.content}</style>';
  });
}

String _inlineImages(String html, Map<String, ArtifactFile> byPath) {
  return html.replaceAllMapped(_kImgRegex, (m) {
    final attrs = m.group(1) ?? '';
    final src = _extractAttribute(attrs, 'src');
    if (src == null) return m.group(0)!;
    final resolved = resolveManifestPath(src, byPath);
    if (resolved == null) return m.group(0)!;
    final b64 = base64Encode(utf8.encode(resolved.content));
    final withoutSrc = _stripAttribute(attrs, 'src');
    return '<img src="data:${resolved.mime};base64,$b64"$withoutSrc>';
  });
}

String? _extractAttribute(String attrs, String name) {
  final re = RegExp(
    r'\b' + name + r'''\s*=\s*("([^"]*)"|'([^']*)')''',
    caseSensitive: false,
  );
  final m = re.firstMatch(attrs);
  if (m == null) return null;
  return m.group(2) ?? m.group(3);
}

String _stripAttribute(String attrs, String name) {
  final re = RegExp(
    r'\s*\b' + name + r'''\s*=\s*("[^"]*"|'[^']*')''',
    caseSensitive: false,
  );
  return attrs.replaceAll(re, '');
}

class _CanvasLoadError extends StatelessWidget {
  final String message;
  final String uri;
  const _CanvasLoadError({required this.message, required this.uri});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.web_asset_outlined,
              size: 36, color: DesignColors.textMuted),
          const SizedBox(height: 12),
          Text(
            'Cannot render canvas',
            style: GoogleFonts.spaceGrotesk(
                fontSize: 14, fontWeight: FontWeight.w700),
          ),
          const SizedBox(height: 6),
          Text(
            message,
            textAlign: TextAlign.center,
            style: GoogleFonts.jetBrainsMono(
                fontSize: 11, color: DesignColors.textMuted),
          ),
          const SizedBox(height: 8),
          SelectableText(
            uri,
            style: GoogleFonts.jetBrainsMono(
                fontSize: 10, color: DesignColors.textMuted),
          ),
        ],
      ),
    );
  }
}

/// Fullscreen route for the canvas viewer with a manual refresh
/// affordance. The mobile cache invalidates lazily: when the agent
/// emits a new artifact version, the user taps refresh and the viewer
/// re-downloads + re-renders. Auto-detection of new shas via
/// `listArtifactsCached` is deferred (Q5) until testers complain.
class ArtifactCanvasViewerScreen extends StatefulWidget {
  final String uri;
  final String title;
  const ArtifactCanvasViewerScreen({
    super.key,
    required this.uri,
    required this.title,
  });

  @override
  State<ArtifactCanvasViewerScreen> createState() =>
      _ArtifactCanvasViewerScreenState();
}

class _ArtifactCanvasViewerScreenState
    extends State<ArtifactCanvasViewerScreen> {
  Key _viewerKey = UniqueKey();

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          widget.title,
          style: GoogleFonts.spaceGrotesk(
              fontSize: 14, fontWeight: FontWeight.w700),
          overflow: TextOverflow.ellipsis,
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh, size: 18),
            tooltip: 'Reload canvas',
            onPressed: () => setState(() => _viewerKey = UniqueKey()),
          ),
        ],
      ),
      body: ArtifactCanvasViewer(
        key: _viewerKey,
        uri: widget.uri,
        title: widget.title,
      ),
    );
  }
}
