import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/vault_provider.dart';
import '../../services/vault/vault_service.dart';

/// Cross-device key-vault sync (ADR-052 D-4). The director sets up sync on one
/// device (minting a recovery code), then joins other devices with that code.
/// All crypto is on-device; the hub only holds opaque ciphertext.
class VaultSyncScreen extends ConsumerStatefulWidget {
  const VaultSyncScreen({super.key});

  @override
  ConsumerState<VaultSyncScreen> createState() => _VaultSyncScreenState();
}

class _VaultSyncScreenState extends ConsumerState<VaultSyncScreen> {
  VaultStatus? _status;
  bool _loading = true;
  bool _busy = false;

  VaultService get _service => ref.read(vaultServiceProvider);

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    VaultStatus next;
    try {
      next = await _service.status();
    } catch (_) {
      next = const VaultStatus(offline: true);
    }
    if (!mounted) return;
    setState(() {
      _status = next;
      _loading = false;
    });
  }

  /// Runs an action with a busy spinner + success/failure snackbar, then reloads.
  Future<void> _run(Future<void> Function() action) async {
    final messenger = ScaffoldMessenger.of(context);
    final l10n = AppLocalizations.of(context)!;
    setState(() => _busy = true);
    try {
      await action();
      messenger.showSnackBar(
        SnackBar(content: Text(l10n.vaultSyncComplete)),
      );
    } catch (e) {
      messenger.showSnackBar(
        SnackBar(content: Text(l10n.vaultSyncFailed(e.toString()))),
      );
    } finally {
      if (mounted) setState(() => _busy = false);
      await _load();
    }
  }

  Future<void> _enable() async {
    final messenger = ScaffoldMessenger.of(context);
    final l10n = AppLocalizations.of(context)!;
    setState(() => _busy = true);
    String? code;
    try {
      code = await _service.enable();
    } catch (e) {
      messenger.showSnackBar(
        SnackBar(content: Text(l10n.vaultSyncFailed(e.toString()))),
      );
    } finally {
      if (mounted) setState(() => _busy = false);
    }
    if (code != null && mounted) await _showRecoveryCode(code);
    await _load();
  }

  Future<void> _resetRecovery() async {
    final messenger = ScaffoldMessenger.of(context);
    final l10n = AppLocalizations.of(context)!;
    setState(() => _busy = true);
    String? code;
    try {
      code = await _service.resetRecoveryCode();
    } catch (e) {
      messenger.showSnackBar(
        SnackBar(content: Text(l10n.vaultSyncFailed(e.toString()))),
      );
    } finally {
      if (mounted) setState(() => _busy = false);
    }
    if (code != null && mounted) await _showRecoveryCode(code);
    await _load();
  }

  Future<void> _join() async {
    final code = await _promptForCode();
    if (code == null || code.isEmpty) return;
    await _run(() => _service.joinWithRecovery(code));
  }

  Future<void> _syncNow() => _run(() async {
        await _service.push();
        await _service.pullAndRestore();
      });

  Future<void> _disable() async {
    final l10n = AppLocalizations.of(context)!;
    final ok = await _confirm(
      l10n.vaultSyncDisableDialogTitle,
      l10n.vaultSyncDisableDialogBody,
      l10n.vaultSyncDisable,
    );
    if (ok != true) return;
    await _run(() => _service.disable());
  }

  Future<void> _revoke(String deviceId) async {
    final l10n = AppLocalizations.of(context)!;
    final ok = await _confirm(
      l10n.vaultSyncRevokeDialogTitle,
      l10n.vaultSyncRevokeDialogBody,
      l10n.vaultSyncRevoke,
    );
    if (ok != true) return;
    await _run(() => _service.revokeDevice(deviceId));
  }

  // ---- dialogs --------------------------------------------------------------

  Future<void> _showRecoveryCode(String code) async {
    final l10n = AppLocalizations.of(context)!;
    await showDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.vaultSyncRecoveryDialogTitle),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(l10n.vaultSyncRecoveryDialogBody),
            const SizedBox(height: 16),
            SelectableText(
              code,
              style: const TextStyle(
                fontFamily: 'monospace',
                fontSize: 16,
                letterSpacing: 1.5,
              ),
            ),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () {
              Clipboard.setData(ClipboardData(text: code));
              ScaffoldMessenger.of(ctx).showSnackBar(
                SnackBar(content: Text(l10n.vaultSyncCopied)),
              );
            },
            child: Text(l10n.vaultSyncCopy),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: Text(l10n.vaultSyncDoneBtn),
          ),
        ],
      ),
    );
  }

  Future<String?> _promptForCode() async {
    final l10n = AppLocalizations.of(context)!;
    final controller = TextEditingController();
    return showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.vaultSyncJoinDialogTitle),
        content: TextField(
          controller: controller,
          autofocus: true,
          decoration: InputDecoration(labelText: l10n.vaultSyncCodeLabel),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: Text(l10n.vaultSyncCancel),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(controller.text.trim()),
            child: Text(l10n.vaultSyncConfirm),
          ),
        ],
      ),
    );
  }

  Future<bool?> _confirm(String title, String body, String confirmLabel) {
    final l10n = AppLocalizations.of(context)!;
    return showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(title),
        content: Text(body),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: Text(l10n.vaultSyncCancel),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: Text(confirmLabel),
          ),
        ],
      ),
    );
  }

  // ---- build ----------------------------------------------------------------

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    return Scaffold(
      appBar: AppBar(title: Text(l10n.vaultSyncTitle)),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : RefreshIndicator(
              onRefresh: _load,
              child: ListView(
                padding: const EdgeInsets.all(16),
                children: _buildBody(l10n, _status!),
              ),
            ),
    );
  }

  List<Widget> _buildBody(AppLocalizations l10n, VaultStatus s) {
    if (s.offline) {
      return [
        Card(
          child: ListTile(
            leading: const Icon(Icons.cloud_off),
            title: Text(l10n.vaultSyncOffline),
            subtitle: Text(l10n.vaultSyncOfflineHint),
          ),
        ),
      ];
    }

    final widgets = <Widget>[
      _statusCard(l10n, s),
      const SizedBox(height: 16),
    ];

    if (s.enrolledLocally) {
      widgets.addAll([
        FilledButton.icon(
          onPressed: _busy ? null : _syncNow,
          icon: const Icon(Icons.sync),
          label: Text(l10n.vaultSyncNow),
        ),
        const SizedBox(height: 8),
        OutlinedButton.icon(
          onPressed: _busy ? null : _resetRecovery,
          icon: const Icon(Icons.vpn_key),
          label: Text(l10n.vaultSyncResetRecovery),
        ),
        const SizedBox(height: 8),
        OutlinedButton.icon(
          onPressed: _busy ? null : _disable,
          icon: const Icon(Icons.link_off),
          label: Text(l10n.vaultSyncDisable),
        ),
      ]);
      if (s.devices.isNotEmpty) {
        widgets.add(const SizedBox(height: 24));
        widgets.add(Text(
          l10n.vaultSyncDevicesHeader,
          style: Theme.of(context).textTheme.titleMedium,
        ));
        for (final d in s.devices) {
          final id = d['device_id'] as String? ?? '';
          final name = d['device_name'] as String?;
          final isThis = id == s.thisDeviceId;
          widgets.add(ListTile(
            leading: const Icon(Icons.devices),
            title: Text(name != null && name.isNotEmpty ? name : id),
            subtitle: isThis ? Text(l10n.vaultSyncThisDevice) : null,
            trailing: isThis
                ? null
                : TextButton(
                    onPressed: _busy ? null : () => _revoke(id),
                    child: Text(l10n.vaultSyncRevoke),
                  ),
          ));
        }
      }
    } else if (s.remoteExists) {
      widgets.add(FilledButton.icon(
        onPressed: _busy ? null : _join,
        icon: const Icon(Icons.login),
        label: Text(l10n.vaultSyncJoin),
      ));
    } else {
      widgets.add(FilledButton.icon(
        onPressed: _busy ? null : _enable,
        icon: const Icon(Icons.cloud_upload),
        label: Text(l10n.vaultSyncEnable),
      ));
    }

    if (_busy) {
      widgets.add(const SizedBox(height: 16));
      widgets.add(const Center(child: CircularProgressIndicator()));
    }
    return widgets;
  }

  Widget _statusCard(AppLocalizations l10n, VaultStatus s) {
    final rows = <Widget>[
      ListTile(
        leading: Icon(s.enrolledLocally ? Icons.lock : Icons.lock_open),
        title: Text(
          s.enrolledLocally ? l10n.vaultSyncOnTitle : l10n.vaultSyncOffTitle,
        ),
        subtitle: s.enrolledLocally
            ? null
            : Text(s.remoteExists
                ? l10n.vaultSyncRemoteReady
                : l10n.vaultSyncNotSetUp),
      ),
    ];
    if (s.version != null) {
      rows.add(ListTile(
        dense: true,
        leading: const Icon(Icons.tag),
        title: Text(l10n.vaultSyncVersion(s.version!)),
      ));
    }
    if (s.remoteExists) {
      rows.add(ListTile(
        dense: true,
        leading: const Icon(Icons.devices),
        title: Text(l10n.vaultSyncDeviceCount(s.devices.length)),
      ));
    }
    rows.add(ListTile(
      dense: true,
      leading: Icon(s.recoverySet ? Icons.check_circle : Icons.error_outline),
      title: Text(
        s.recoverySet ? l10n.vaultSyncRecoverySet : l10n.vaultSyncRecoveryUnset,
      ),
    ));
    return Card(child: Column(children: rows));
  }
}
