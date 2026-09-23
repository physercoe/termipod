import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../l10n/app_localizations.dart';
import '../../providers/connection_provider.dart';
import '../../providers/ssh_provider.dart';
import '../../providers/web_tunnel_provider.dart';
import '../../services/ssh/connection_options.dart';
import 'web_tunnel_browser.dart';

class WebServicesScreen extends ConsumerStatefulWidget {
  const WebServicesScreen({super.key, required this.connectionId});
  final String connectionId;
  @override
  ConsumerState<WebServicesScreen> createState() => _WebServicesScreenState();
}

class _WebServicesScreenState extends ConsumerState<WebServicesScreen> {
  bool _busy = false;
  String? _error;

  Future<void> _start() async {
    final target = await showDialog<_ServiceTarget>(
      context: context,
      builder: (_) => const _ServiceDialog(),
    );
    if (target == null || !mounted) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final connection = ref
          .read(connectionsProvider.notifier)
          .getById(widget.connectionId);
      if (connection == null) throw StateError('Connection not found');
      await ref
          .read(sshProvider(widget.connectionId).notifier)
          .ensureConnected(connection, () => loadSshOptions(connection));
      if (!mounted) return;
      final tunnel = await ref
          .read(webTunnelProvider(widget.connectionId).notifier)
          .start(
            remoteHost: target.host,
            remotePort: target.port,
            path: target.path,
            name: target.name,
          );
      if (!mounted) return;
      _open(tunnel);
    } catch (e) {
      if (mounted) setState(() => _error = e.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _open(WebTunnel tunnel) {
    Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) =>
            WebTunnelBrowser(connectionId: widget.connectionId, tunnel: tunnel),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final tunnels = ref.watch(webTunnelProvider(widget.connectionId));
    final ssh = ref.watch(sshProvider(widget.connectionId));
    final connection = ref
        .watch(connectionsProvider)
        .connections
        .where((c) => c.id == widget.connectionId)
        .firstOrNull;
    return Scaffold(
      appBar: AppBar(
        title: Text(l10n.webServices),
        actions: [
          if (ssh.isConnected || ssh.isReconnecting)
            IconButton(
              tooltip: l10n.disconnectLabel,
              icon: const Icon(Icons.power_settings_new),
              onPressed: () => ref
                  .read(sshProvider(widget.connectionId).notifier)
                  .disconnect(),
            ),
        ],
      ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _busy ? null : _start,
        icon: const Icon(Icons.add),
        label: Text(l10n.webAddService),
      ),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          if (connection != null)
            Text(
              connection.name,
              style: Theme.of(context).textTheme.titleMedium,
            ),
          Text(l10n.webServicesHelp),
          if (_busy) const LinearProgressIndicator(),
          if (_error != null)
            Text(
              _error!,
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          if (tunnels.isEmpty)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 24),
              child: Text(l10n.webNoServices),
            ),
          for (final tunnel in tunnels)
            Card(
              child: ListTile(
                leading: const Icon(Icons.language),
                title: Text(tunnel.name),
                subtitle: Text(
                  '${tunnel.remoteHost}:${tunnel.remotePort} · ${ssh.isConnected ? l10n.webReady : l10n.webRestoring}',
                ),
                onTap: () => _open(tunnel),
                trailing: IconButton(
                  tooltip: l10n.buttonStop,
                  icon: const Icon(Icons.stop_circle_outlined),
                  onPressed: () => ref
                      .read(webTunnelProvider(widget.connectionId).notifier)
                      .stop(tunnel.id),
                ),
              ),
            ),
          const SizedBox(height: 88),
        ],
      ),
    );
  }
}

class _ServiceTarget {
  const _ServiceTarget(this.host, this.port, this.path, this.name);
  final String host, path, name;
  final int port;
}

class _ServiceDialog extends StatefulWidget {
  const _ServiceDialog();
  @override
  State<_ServiceDialog> createState() => _ServiceDialogState();
}

class _ServiceDialogState extends State<_ServiceDialog> {
  final _form = GlobalKey<FormState>();
  final _port = TextEditingController(text: '8080');
  final _host = TextEditingController(text: '127.0.0.1');
  final _path = TextEditingController(text: '/');
  final _name = TextEditingController();
  @override
  void dispose() {
    _port.dispose();
    _host.dispose();
    _path.dispose();
    _name.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    return AlertDialog(
      title: Text(l10n.webAddService),
      content: SingleChildScrollView(
        child: Form(
          key: _form,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextFormField(
                controller: _port,
                autofocus: true,
                keyboardType: TextInputType.number,
                decoration: InputDecoration(labelText: l10n.webRemotePort),
                validator: (value) {
                  final port = int.tryParse(value?.trim() ?? '');
                  return port == null || port < 1 || port > 65535
                      ? l10n.webInvalidPort
                      : null;
                },
              ),
              ExpansionTile(
                title: Text(l10n.webAdvanced),
                children: [
                  TextFormField(
                    controller: _host,
                    decoration: InputDecoration(labelText: l10n.webRemoteHost),
                    validator: (value) =>
                        value == null ||
                            value.trim().isEmpty ||
                            RegExp(r'[\s/\\]').hasMatch(value.trim())
                        ? l10n.webInvalidHost
                        : null,
                  ),
                  TextFormField(
                    controller: _path,
                    decoration: InputDecoration(labelText: l10n.webPath),
                  ),
                  TextFormField(
                    controller: _name,
                    decoration: InputDecoration(labelText: l10n.webName),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: Text(l10n.buttonCancel),
        ),
        FilledButton(
          onPressed: () {
            if (_form.currentState!.validate())
              Navigator.pop(
                context,
                _ServiceTarget(
                  _host.text.trim(),
                  int.parse(_port.text.trim()),
                  _path.text,
                  _name.text,
                ),
              );
          },
          child: Text(l10n.webStartOpen),
        ),
      ],
    );
  }
}
