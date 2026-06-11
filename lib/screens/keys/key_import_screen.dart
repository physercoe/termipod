import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:uuid/uuid.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/key_provider.dart';

/// SSH key import screen.
class KeyImportScreen extends ConsumerStatefulWidget {
  const KeyImportScreen({super.key});

  @override
  ConsumerState<KeyImportScreen> createState() => _KeyImportScreenState();
}

class _KeyImportScreenState extends ConsumerState<KeyImportScreen> {
  final _formKey = GlobalKey<FormState>();
  final _nameController = TextEditingController();
  final _privateKeyController = TextEditingController();
  final _passphraseController = TextEditingController();
  bool _isImporting = false;
  String? _selectedFilePath;
  String? _pemValidationError;
  bool _isEncrypted = false;
  bool _showPassphrase = false;

  @override
  void dispose() {
    _nameController.dispose();
    _privateKeyController.dispose();
    _passphraseController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(AppLocalizations.of(context)!.importKeyScreenTitle),
      ),
      body: Form(
        key: _formKey,
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [
            TextFormField(
              controller: _nameController,
              decoration: InputDecoration(
                labelText: AppLocalizations.of(context)!.keyNameLabel,
                hintText: AppLocalizations.of(context)!.keyNameHint,
              ),
              validator: (value) {
                if (value == null || value.isEmpty) {
                  return AppLocalizations.of(context)!.keyNameEmptyError;
                }
                return null;
              },
            ),
            const SizedBox(height: 16),
            OutlinedButton.icon(
              onPressed: _pickFile,
              icon: const Icon(Icons.file_upload),
              label: Text(_selectedFilePath != null
                  ? _selectedFilePath!.split('/').last
                  : AppLocalizations.of(context)!.selectPrivateKeyFile),
            ),
            const SizedBox(height: 8),
            Text(
              AppLocalizations.of(context)!.orPastePrivateKey,
              style: TextStyle(color: Theme.of(context).colorScheme.outline),
            ),
            const SizedBox(height: 8),
            TextFormField(
              controller: _privateKeyController,
              decoration: InputDecoration(
                labelText: AppLocalizations.of(context)!.privateKeyPemLabel,
                hintText: AppLocalizations.of(context)!.pemFormatHint,
                alignLabelWithHint: true,
                errorText: _pemValidationError,
              ),
              maxLines: 8,
              style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
              onChanged: _onPemChanged,
              validator: (value) {
                if ((value == null || value.isEmpty) &&
                    _selectedFilePath == null) {
                  return AppLocalizations.of(context)!.selectFileOrPastePem;
                }
                if (_pemValidationError != null) {
                  return _pemValidationError;
                }
                return null;
              },
            ),
            if (_isEncrypted || _showPassphrase) ...[
              const SizedBox(height: 16),
              TextFormField(
                controller: _passphraseController,
                decoration: InputDecoration(
                  labelText: _isEncrypted
                      ? AppLocalizations.of(context)!.passphraseRequired
                      : AppLocalizations.of(context)!.passphraseOptional,
                  hintText: _isEncrypted
                      ? AppLocalizations.of(context)!.passphraseDecryptHint
                      : AppLocalizations.of(context)!.passphraseEmptyHint,
                ),
                obscureText: true,
                validator: (value) {
                  if (_isEncrypted && (value == null || value.isEmpty)) {
                    return AppLocalizations.of(context)!.passphraseRequiredError;
                  }
                  return null;
                },
              ),
            ],
            if (!_isEncrypted && !_showPassphrase) ...[
              const SizedBox(height: 8),
              TextButton(
                onPressed: () {
                  setState(() {
                    _showPassphrase = true;
                  });
                },
                child: Text(AppLocalizations.of(context)!.addPassphrase),
              ),
            ],
            const SizedBox(height: 32),
            FilledButton(
              onPressed: _isImporting ? null : _import,
              child: _isImporting
                  ? const SizedBox(
                      width: 20,
                      height: 20,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : Text(AppLocalizations.of(context)!.buttonImport),
            ),
          ],
        ),
      ),
    );
  }

  void _onPemChanged(String value) {
    if (value.isEmpty) {
      setState(() {
        _pemValidationError = null;
        _isEncrypted = false;
      });
      return;
    }

    final keyService = ref.read(sshKeyServiceProvider);

    // Basic PEM-format validation.
    if (!value.contains('-----BEGIN') || !value.contains('-----END')) {
      setState(() {
        _pemValidationError = AppLocalizations.of(context)!.invalidPemFormat;
        _isEncrypted = false;
      });
      return;
    }

    try {
      // Check whether the key is encrypted.
      final isEncrypted = keyService.isEncrypted(value);
      setState(() {
        _pemValidationError = null;
        _isEncrypted = isEncrypted;
        if (isEncrypted) {
          _showPassphrase = true;
        }
      });
    } catch (e) {
      setState(() {
        _pemValidationError = AppLocalizations.of(context)!.invalidPemFormat;
        _isEncrypted = false;
      });
    }
  }

  Future<void> _pickFile() async {
    try {
      final result = await FilePicker.platform.pickFiles(
        type: FileType.any,
        allowMultiple: false,
        withData: true,
      );

      if (result != null && result.files.isNotEmpty) {
        final file = result.files.first;

        // Read the file content.
        String content;
        if (file.bytes != null) {
          content = String.fromCharCodes(file.bytes!);
        } else {
          // Read from the file path (desktop).
          setState(() {
            _pemValidationError =
                AppLocalizations.of(context)!.couldNotReadFile;
          });
          return;
        }

        setState(() {
          _selectedFilePath = file.path ?? file.name;
          _privateKeyController.text = content;
        });

        // Validate the PEM.
        _onPemChanged(content);
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(AppLocalizations.of(context)!.pickFileFailed(e.toString())),
            backgroundColor: Theme.of(context).colorScheme.error,
          ),
        );
      }
    }
  }

  Future<void> _import() async {
    if (!_formKey.currentState!.validate()) return;

    setState(() {
      _isImporting = true;
    });

    try {
      final keyService = ref.read(sshKeyServiceProvider);
      final storage = ref.read(secureStorageProvider);
      final keysNotifier = ref.read(keysProvider.notifier);

      final pemContent = _privateKeyController.text.trim();
      final passphrase = _passphraseController.text.isNotEmpty
          ? _passphraseController.text
          : null;
      final name = _nameController.text.trim();
      final keyId = const Uuid().v4();

      // Parse the PEM.
      final keyPair = await keyService.parseFromPem(
        pemContent,
        passphrase: passphrase,
      );

      // Save the private key to secure storage.
      await storage.savePrivateKey(keyId, pemContent);

      // Save the passphrase if present.
      if (passphrase != null) {
        await storage.savePassphrase(keyId, passphrase);
      }

      // Save the metadata to the keys notifier.
      final meta = SshKeyMeta(
        id: keyId,
        name: name,
        type: keyPair.type,
        publicKey: keyPair.publicKeyString,
        fingerprint: keyPair.fingerprint,
        hasPassphrase: passphrase != null || _isEncrypted,
        createdAt: DateTime.now(),
        comment: name,
        source: KeySource.imported,
      );
      await keysNotifier.add(meta);

      if (mounted) {
        Navigator.pop(context);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(AppLocalizations.of(context)!.keyImportedSuccess(name))),
        );
      }
    } on FormatException catch (e) {
      // Invalid PEM format or passphrase error.
      if (mounted) {
        final message = e.message.contains('passphrase')
            ? AppLocalizations.of(context)!.wrongPassphrase
            : AppLocalizations.of(context)!.invalidKeyFormat(e.message);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(message),
            backgroundColor: Theme.of(context).colorScheme.error,
          ),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(AppLocalizations.of(context)!.importKeyFailed(e.toString())),
            backgroundColor: Theme.of(context).colorScheme.error,
          ),
        );
      }
    } finally {
      if (mounted) {
        setState(() {
          _isImporting = false;
        });
      }
    }
  }
}
