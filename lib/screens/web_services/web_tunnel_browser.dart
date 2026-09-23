import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../l10n/app_localizations.dart';
import '../../providers/ssh_provider.dart';
import '../../providers/web_tunnel_provider.dart';

/// Native containers isolate cookies/storage from other tunnels and canvases.
/// Pages receive no JS bridge; this channel belongs only to Flutter controls.
class WebTunnelBrowser extends ConsumerStatefulWidget {
  const WebTunnelBrowser({
    super.key,
    required this.connectionId,
    required this.tunnel,
  });
  final String connectionId;
  final WebTunnel tunnel;
  @override
  ConsumerState<WebTunnelBrowser> createState() => _WebTunnelBrowserState();
}

class _WebTunnelBrowserState extends ConsumerState<WebTunnelBrowser>
    with WidgetsBindingObserver {
  MethodChannel? _channel;
  bool? _supported;
  bool _back = false, _forward = false;
  String? _message;
  bool _needsReload = false;
  bool _compat = false;
  bool _compatOpening = false;
  String? _providerDetails;
  static const _browserChannel = MethodChannel('termipod/web_browser');
  static const _compatChannel = MethodChannel('termipod/web_browser_compat');

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _checkSupport();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _restore();
    });
  }

  Future<void> _checkSupport() async {
    var supported = false;
    try {
      if (defaultTargetPlatform == TargetPlatform.android) {
        final capability = await _browserChannel
            .invokeMapMethod<String, dynamic>('capabilities');
        _compat = capability?['mode'] == 'process';
        supported = capability?['mode'] == 'profile' || _compat;
        _providerDetails = [
          capability?['provider'],
          capability?['version'],
        ].whereType<String>().join(' ');
      } else {
        supported =
            await _browserChannel.invokeMethod<bool>('supported') ?? false;
      }
      _message = null;
    } on PlatformException {
      _message = 'browserInitFailed';
    } on MissingPluginException {
      _message = 'browserInitFailed';
    }
    if (mounted) setState(() => _supported = supported);
    if (mounted && supported && _compat) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _openCompat();
      });
    }
  }

  Future<void> _openCompat() async {
    if (_compatOpening) return;
    _compatOpening = true;
    _compatChannel.setMethodCallHandler((call) async {
      if (mounted && call.method == 'resume') await _restore();
    });
    try {
      await _restore();
      if (!mounted) return;
      if (!ref
          .read(webTunnelProvider(widget.connectionId))
          .any((t) => t.id == widget.tunnel.id))
        return;
      final l10n = AppLocalizations.of(context)!;
      await _browserChannel.invokeMethod<void>('openCompat', {
        'url': widget.tunnel.uri.toString(),
        'close': l10n.buttonClose,
        'back': l10n.webBack,
        'forward': l10n.webForward,
        'reload': l10n.buttonRefresh,
        'loadFailed': l10n.webLoadFailed,
        'navigationBlocked': l10n.webNavigationBlocked,
      });
      if (mounted) Navigator.of(context).pop();
    } on PlatformException {
      if (mounted) setState(() => _message = 'browserInitFailed');
    } on MissingPluginException {
      if (mounted) setState(() => _message = 'browserInitFailed');
    } finally {
      _compatOpening = false;
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) _restore();
  }

  Future<void> _restore() async {
    if (!ref
        .read(webTunnelProvider(widget.connectionId))
        .any((t) => t.id == widget.tunnel.id))
      return;
    final ssh = ref.read(sshProvider(widget.connectionId).notifier);
    final connection = ssh.lastConnection;
    final options = ssh.lastOptions;
    if (connection == null || options == null) return;
    try {
      await ssh.ensureConnected(connection, () async => options);
    } catch (_) {
      /* The connection status and explicit Retry show recovery. */
    }
  }

  Future<void> _command(String command) async {
    try {
      await _channel?.invokeMethod<void>(command);
    } on PlatformException {
      if (mounted) setState(() => _message = 'loadFailed');
    } on MissingPluginException {
      /* View has already closed. */
    }
  }

  void _created(int id) {
    final channel = MethodChannel('termipod/web_browser/$id');
    _channel = channel;
    channel.setMethodCallHandler((call) async {
      if (!mounted) return;
      if (call.method == 'state') {
        final args = Map<Object?, Object?>.from(call.arguments as Map);
        setState(() {
          _back = args['back'] == true;
          _forward = args['forward'] == true;
        });
      } else if (call.method == 'error') {
        setState(() => _message = call.arguments as String?);
      }
    });
    _command('load');
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _channel?.setMethodCallHandler(null);
    if (_compat) _compatChannel.setMethodCallHandler(null);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    ref.listen(sshProvider(widget.connectionId), (previous, next) {
      if (previous?.isConnected == true && !next.isConnected)
        setState(() => _needsReload = true);
    });
    final ssh = ref.watch(sshProvider(widget.connectionId));
    final active = ref
        .watch(webTunnelProvider(widget.connectionId))
        .any((t) => t.id == widget.tunnel.id);
    final args = {'url': widget.tunnel.uri.toString()};
    return Scaffold(
      appBar: AppBar(
        title: Text(widget.tunnel.name),
        actions: [
          IconButton(
            tooltip: l10n.webBack,
            onPressed: _back && active && ssh.isConnected
                ? () => _command('back')
                : null,
            icon: const Icon(Icons.arrow_back),
          ),
          IconButton(
            tooltip: l10n.webForward,
            onPressed: _forward && active && ssh.isConnected
                ? () => _command('forward')
                : null,
            icon: const Icon(Icons.arrow_forward),
          ),
          IconButton(
            tooltip: l10n.buttonRefresh,
            onPressed: active && ssh.isConnected
                ? () {
                    setState(() {
                      _message = null;
                      _needsReload = false;
                    });
                    _command('reload');
                  }
                : null,
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: Column(
        children: [
          if (!active)
            Padding(
              padding: const EdgeInsets.all(12),
              child: Text(l10n.webStopped),
            )
          else if (!ssh.isConnected)
            ListTile(
              title: Text(l10n.webRestoring),
              trailing: TextButton(
                onPressed: () => ref
                    .read(sshProvider(widget.connectionId).notifier)
                    .reconnectNow(),
                child: Text(l10n.buttonRetry),
              ),
            )
          else if (_needsReload)
            Padding(
              padding: const EdgeInsets.all(12),
              child: Text(l10n.webReloadAfterRecovery),
            ),
          if (_message != null)
            Padding(
              padding: const EdgeInsets.all(12),
              child: Text(
                _message == 'navigationBlocked'
                    ? l10n.webNavigationBlocked
                    : _message == 'browserInitFailed'
                    ? l10n.webBrowserInitializationFailed
                    : l10n.webLoadFailed,
              ),
            ),
          Expanded(
            child: !active
                ? const SizedBox.shrink()
                : _supported == null
                ? const Center(child: CircularProgressIndicator())
                : _supported == false || _compat
                ? Center(
                    child: Padding(
                      padding: const EdgeInsets.all(24),
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          if (!_compat && _message == null)
                            Text(l10n.webBrowserUnsupported),
                          if (_providerDetails?.isNotEmpty == true)
                            Text(_providerDetails!),
                          TextButton(
                            onPressed: _checkSupport,
                            child: Text(l10n.buttonRetry),
                          ),
                        ],
                      ),
                    ),
                  )
                : AbsorbPointer(
                    absorbing: !ssh.isConnected,
                    child: defaultTargetPlatform == TargetPlatform.iOS
                        ? UiKitView(
                            viewType: 'termipod/web_browser',
                            creationParams: args,
                            creationParamsCodec: const StandardMessageCodec(),
                            onPlatformViewCreated: _created,
                          )
                        : AndroidView(
                            viewType: 'termipod/web_browser',
                            creationParams: args,
                            creationParamsCodec: const StandardMessageCodec(),
                            onPlatformViewCreated: _created,
                          ),
                  ),
          ),
        ],
      ),
    );
  }
}
