import 'dart:developer' as developer;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:uuid/uuid.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/active_session_provider.dart';
import '../../providers/connection_provider.dart';
import '../../providers/key_provider.dart';
import '../../services/keychain/secure_storage.dart';
import '../../services/ssh/ssh_client.dart';
import '../../theme/design_colors.dart';

/// 接続編集画面
///
/// On create, Navigator.pop returns the new connection id (String) on
/// success, `null` on cancel. Callers that want to record a side-binding
/// (e.g. hub host → local connection) can `await` the pop to get the id.
/// On edit, pop returns `true` on save / `null` on cancel — the id is
/// already known to the caller.
class ConnectionFormScreen extends ConsumerStatefulWidget {
  final String? connectionId;

  /// Pre-fill for a new connection. Accepted keys (all optional):
  /// `name`, `host`, `port` (int or String), `username`. Ignored on edit.
  /// Used by the hub's host detail sheet to seed a form from the host's
  /// non-secret `ssh_hint_json`.
  final Map<String, dynamic>? initialHint;

  const ConnectionFormScreen({
    super.key,
    this.connectionId,
    this.initialHint,
  });

  bool get isEditing => connectionId != null;

  @override
  ConsumerState<ConnectionFormScreen> createState() => _ConnectionFormScreenState();
}

class _ConnectionFormScreenState extends ConsumerState<ConnectionFormScreen> {
  final _formKey = GlobalKey<FormState>();
  final _nameController = TextEditingController();
  final _hostController = TextEditingController();
  final _portController = TextEditingController(text: '22');
  final _usernameController = TextEditingController();
  final _passwordController = TextEditingController();
  final _tmuxPathController = TextEditingController();
  final _deepLinkIdController = TextEditingController();

  // Jump host controllers
  final _jumpHostController = TextEditingController();
  final _jumpPortController = TextEditingController(text: '22');
  final _jumpUsernameController = TextEditingController();

  // Proxy controllers
  final _proxyHostController = TextEditingController();
  final _proxyPortController = TextEditingController(text: '1080');
  final _proxyUsernameController = TextEditingController();
  final _proxyPasswordController = TextEditingController();

  String _authMethod = 'password';
  String? _selectedKeyId;
  bool _isSaving = false;
  bool _isTesting = false;
  bool _obscurePassword = true;
  bool _useJumpHost = false;
  String _jumpAuthMethod = 'password';
  String? _jumpSelectedKeyId;
  bool _useProxy = false;
  String _terminalMode = 'tmux'; // 'tmux' or 'raw'

  @override
  void initState() {
    super.initState();
    if (widget.isEditing) {
      _loadExistingConnection();
    } else if (widget.initialHint != null) {
      _applyInitialHint(widget.initialHint!);
    }
  }

  void _applyInitialHint(Map<String, dynamic> hint) {
    final name = hint['name']?.toString().trim() ?? '';
    final host = hint['host']?.toString().trim() ?? '';
    final user = hint['username']?.toString().trim() ?? '';
    if (name.isNotEmpty) _nameController.text = name;
    if (host.isNotEmpty) _hostController.text = host;
    if (user.isNotEmpty) _usernameController.text = user;
    final port = hint['port'];
    if (port is int) {
      _portController.text = port.toString();
    } else if (port is String && port.trim().isNotEmpty) {
      _portController.text = port.trim();
    }
  }

  void _loadExistingConnection() {
    final connection = ref.read(connectionsProvider.notifier).getById(widget.connectionId!);
    if (connection != null) {
      _nameController.text = connection.name;
      _hostController.text = connection.host;
      _portController.text = connection.port.toString();
      _usernameController.text = connection.username;
      _authMethod = connection.authMethod;
      _selectedKeyId = connection.keyId;
      _tmuxPathController.text = connection.tmuxPath ?? '';
      _terminalMode = connection.terminalMode ?? 'tmux';
      _deepLinkIdController.text = connection.deepLinkId ?? '';
      // Jump host
      if (connection.jumpHost != null) {
        _useJumpHost = true;
        _jumpHostController.text = connection.jumpHost!;
        _jumpPortController.text = (connection.jumpPort ?? 22).toString();
        _jumpUsernameController.text = connection.jumpUsername ?? '';
        _jumpAuthMethod = connection.jumpAuthMethod ?? 'password';
        _jumpSelectedKeyId = connection.jumpKeyId;
      }
      // Proxy
      if (connection.proxyHost != null) {
        _useProxy = true;
        _proxyHostController.text = connection.proxyHost!;
        _proxyPortController.text = (connection.proxyPort ?? 1080).toString();
        _proxyUsernameController.text = connection.proxyUsername ?? '';
        _proxyPasswordController.text = connection.proxyPassword ?? '';
      }
    }
  }

  @override
  void dispose() {
    _nameController.dispose();
    _hostController.dispose();
    _portController.dispose();
    _usernameController.dispose();
    _passwordController.dispose();
    _tmuxPathController.dispose();
    _deepLinkIdController.dispose();
    _jumpHostController.dispose();
    _jumpPortController.dispose();
    _jumpUsernameController.dispose();
    _proxyHostController.dispose();
    _proxyPortController.dispose();
    _proxyUsernameController.dispose();
    _proxyPasswordController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final keysState = ref.watch(keysProvider);

    return Scaffold(
      appBar: _buildAppBar(),
      body: Stack(
        children: [
          // Background grid pattern
          Positioned.fill(
            child: Container(
              decoration: BoxDecoration(
                gradient: RadialGradient(
                  center: Alignment.topRight,
                  radius: 1.5,
                  colors: [
                    DesignColors.primary.withValues(alpha: 0.1),
                    Colors.transparent,
                  ],
                ),
              ),
            ),
          ),
          // Form content
          Form(
            key: _formKey,
            child: ListView(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 120),
              children: [
                _buildServerSection(),
                const SizedBox(height: 24),
                _buildAuthSection(keysState),
                const SizedBox(height: 24),
                _buildJumpHostSection(keysState),
                const SizedBox(height: 24),
                _buildProxySection(),
              ],
            ),
          ),
          // Bottom action button
          _buildBottomAction(),
        ],
      ),
    );
  }

  AppBar _buildAppBar() {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return AppBar(
      surfaceTintColor: Colors.transparent,
      leading: TextButton(
        onPressed: () => Navigator.of(context).pop(),
        child: Text(
          l10n.buttonCancel,
          style: GoogleFonts.spaceGrotesk(
            color: isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
            fontWeight: FontWeight.w500,
          ),
        ),
      ),
      leadingWidth: 80,
      title: Text(
        widget.isEditing ? l10n.editConnection : l10n.addConnection,
        style: GoogleFonts.spaceGrotesk(
          fontWeight: FontWeight.w700,
          letterSpacing: -0.5,
        ),
      ),
      centerTitle: true,
      actions: [
        TextButton(
          onPressed: _isSaving ? null : _save,
          child: _isSaving
              ? const SizedBox(
                  width: 20,
                  height: 20,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : Text(
                  l10n.buttonSave,
                  style: GoogleFonts.spaceGrotesk(
                    color: colorScheme.primary,
                    fontWeight: FontWeight.w700,
                  ),
                ),
        ),
        const SizedBox(width: 8),
      ],
    );
  }

  Widget _buildSectionHeader(String title) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.only(left: 4, bottom: 8),
      child: Text(
        title.toUpperCase(),
        style: GoogleFonts.spaceGrotesk(
          fontSize: 11,
          fontWeight: FontWeight.w700,
          letterSpacing: 1.5,
          color: mutedColor,
        ),
      ),
    );
  }

  Widget _buildServerSection() {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _buildSectionHeader(l10n.sectionServer),
        Container(
          decoration: BoxDecoration(
            color: colorScheme.surface,
            borderRadius: BorderRadius.circular(16),
            border: Border.all(color: colorScheme.outline.withValues(alpha: 0.2)),
          ),
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // Connection name
              _buildFieldLabel(l10n.fieldConnectionName),
              const SizedBox(height: 8),
              _buildNameInput(),
              const SizedBox(height: 16),
              // Host field
              _buildFieldLabel(l10n.fieldHost),
              const SizedBox(height: 8),
              _buildHostInput(),
              const SizedBox(height: 16),
              // Port & Username row
              Row(
                children: [
                  Expanded(
                    flex: 1,
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        _buildFieldLabel(l10n.fieldPort),
                        const SizedBox(height: 8),
                        _buildPortInput(),
                      ],
                    ),
                  ),
                  const SizedBox(width: 16),
                  Expanded(
                    flex: 2,
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        _buildFieldLabel(l10n.fieldUsername),
                        const SizedBox(height: 8),
                        _buildUsernameInput(),
                      ],
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 16),
              // Terminal mode selector
              _buildFieldLabel(l10n.terminalMode),
              const SizedBox(height: 8),
              SegmentedButton<String>(
                segments: [
                  ButtonSegment(value: 'tmux', label: Text(l10n.terminalModeTmux)),
                  ButtonSegment(value: 'raw', label: Text(l10n.terminalModeRaw)),
                ],
                selected: {_terminalMode},
                onSelectionChanged: (v) => setState(() => _terminalMode = v.first),
              ),
              const SizedBox(height: 4),
              Text(
                l10n.terminalModeDesc,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 10,
                  color: Theme.of(context).brightness == Brightness.dark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
              if (_terminalMode == 'tmux') ...[
                const SizedBox(height: 16),
                // tmux path
                _buildFieldLabel(l10n.fieldTmuxPath),
                const SizedBox(height: 8),
                _buildTmuxPathInput(),
              ],
              const SizedBox(height: 16),
              // Deep Link ID
              _buildFieldLabel(l10n.fieldDeepLinkId),
              const SizedBox(height: 4),
              Text(
                l10n.deepLinkIdDesc,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 10,
                  color: Theme.of(context).brightness == Brightness.dark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
              const SizedBox(height: 8),
              _buildDeepLinkIdInput(),
            ],
          ),
        ),
      ],
    );
  }

  Widget _buildAuthSection(KeysState keysState) {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _buildSectionHeader(l10n.sectionAuthentication),
        Container(
          decoration: BoxDecoration(
            color: colorScheme.surface,
            borderRadius: BorderRadius.circular(16),
            border: Border.all(color: colorScheme.outline.withValues(alpha: 0.2)),
          ),
          padding: const EdgeInsets.all(16),
          child: Column(
            children: [
              _buildAuthMethodToggle(),
              const SizedBox(height: 16),
              if (_authMethod == 'password')
                _buildPasswordInput()
              else
                _buildKeyDropdown(keysState),
            ],
          ),
        ),
      ],
    );
  }

  Widget _buildFieldLabel(String label) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Text(
      label,
      style: GoogleFonts.spaceGrotesk(
        fontSize: 10,
        fontWeight: FontWeight.w500,
        letterSpacing: 1,
        color: isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
      ),
    );
  }

  Widget _buildNameInput() {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final inputColor = isDark ? DesignColors.inputDark : DesignColors.inputLight;
    return TextFormField(
      controller: _nameController,
      style: GoogleFonts.spaceGrotesk(
        fontSize: 16,
        fontWeight: FontWeight.w500,
        color: colorScheme.onSurface,
      ),
      decoration: InputDecoration(
        hintText: l10n.connectionNameHint,
        hintStyle: GoogleFonts.spaceGrotesk(color: mutedColor.withValues(alpha: 0.5)),
        prefixIcon: Icon(Icons.label_outline, color: mutedColor, size: 20),
        filled: true,
        fillColor: inputColor,
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: colorScheme.primary),
        ),
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      ),
      validator: (value) {
        if (value == null || value.isEmpty) {
          return l10n.connectionNameEmptyError;
        }
        return null;
      },
    );
  }

  Widget _buildHostInput() {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final inputColor = isDark ? DesignColors.inputDark : DesignColors.inputLight;
    return TextFormField(
      controller: _hostController,
      keyboardType: TextInputType.url,
      style: GoogleFonts.jetBrainsMono(fontSize: 14, color: colorScheme.onSurface),
      decoration: InputDecoration(
        hintText: l10n.hostHint,
        hintStyle: GoogleFonts.jetBrainsMono(color: mutedColor.withValues(alpha: 0.5)),
        filled: true,
        fillColor: inputColor,
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: colorScheme.primary),
        ),
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        suffixIcon: Container(
          padding: const EdgeInsets.all(12),
          child: Container(
            width: 8,
            height: 8,
            decoration: BoxDecoration(
              color: _hostController.text.isNotEmpty
                  ? DesignColors.success
                  : mutedColor,
              shape: BoxShape.circle,
              boxShadow: _hostController.text.isNotEmpty
                  ? [
                      BoxShadow(
                        color: DesignColors.success.withValues(alpha: 0.6),
                        blurRadius: 8,
                      ),
                    ]
                  : null,
            ),
          ),
        ),
      ),
      validator: (value) {
        if (value == null || value.isEmpty) {
          return l10n.hostEmptyError;
        }
        return null;
      },
      onChanged: (_) => setState(() {}),
    );
  }

  Widget _buildPortInput() {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final inputColor = isDark ? DesignColors.inputDark : DesignColors.inputLight;
    return TextFormField(
      controller: _portController,
      keyboardType: TextInputType.number,
      style: GoogleFonts.jetBrainsMono(fontSize: 14, color: colorScheme.onSurface),
      decoration: InputDecoration(
        hintText: l10n.portHint,
        hintStyle: GoogleFonts.jetBrainsMono(color: mutedColor.withValues(alpha: 0.5)),
        filled: true,
        fillColor: inputColor,
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: colorScheme.primary),
        ),
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      ),
      validator: (value) {
        if (value == null || value.isEmpty) {
          return l10n.portRequiredError;
        }
        final port = int.tryParse(value);
        if (port == null || port < 1 || port > 65535) {
          return l10n.portInvalidError;
        }
        return null;
      },
    );
  }

  Widget _buildUsernameInput() {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final inputColor = isDark ? DesignColors.inputDark : DesignColors.inputLight;
    return TextFormField(
      controller: _usernameController,
      style: GoogleFonts.jetBrainsMono(fontSize: 14, color: colorScheme.onSurface),
      decoration: InputDecoration(
        hintText: l10n.usernameHint,
        hintStyle: GoogleFonts.jetBrainsMono(color: mutedColor.withValues(alpha: 0.5)),
        prefixIcon: Icon(Icons.person_outline, color: mutedColor, size: 20),
        filled: true,
        fillColor: inputColor,
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: colorScheme.primary),
        ),
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      ),
      validator: (value) {
        if (value == null || value.isEmpty) {
          return l10n.usernameEmptyError;
        }
        return null;
      },
    );
  }

  Widget _buildTmuxPathInput() {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final inputColor = isDark ? DesignColors.inputDark : DesignColors.inputLight;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        TextFormField(
          controller: _tmuxPathController,
          style: GoogleFonts.jetBrainsMono(fontSize: 14, color: colorScheme.onSurface),
          decoration: InputDecoration(
            hintText: l10n.tmuxPathHint,
            hintStyle: GoogleFonts.jetBrainsMono(color: mutedColor.withValues(alpha: 0.5)),
            prefixIcon: Icon(Icons.terminal_outlined, color: mutedColor, size: 20),
            filled: true,
            fillColor: inputColor,
            border: OutlineInputBorder(
              borderRadius: BorderRadius.circular(12),
              borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
            ),
            enabledBorder: OutlineInputBorder(
              borderRadius: BorderRadius.circular(12),
              borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
            ),
            focusedBorder: OutlineInputBorder(
              borderRadius: BorderRadius.circular(12),
              borderSide: BorderSide(color: colorScheme.primary),
            ),
            contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
          ),
          validator: (value) {
            if (value != null && value.isNotEmpty && !value.startsWith('/')) {
              return l10n.tmuxPathInvalidError;
            }
            return null;
          },
        ),
        const SizedBox(height: 6),
        Text(
          l10n.tmuxPathDesc,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 11,
            color: mutedColor.withValues(alpha: 0.7),
          ),
        ),
      ],
    );
  }

  Widget _buildDeepLinkIdInput() {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final inputColor = isDark ? DesignColors.inputDark : DesignColors.inputLight;
    return TextFormField(
      controller: _deepLinkIdController,
      style: GoogleFonts.jetBrainsMono(fontSize: 14, color: colorScheme.onSurface),
      decoration: InputDecoration(
        hintText: l10n.deepLinkIdHint,
        hintStyle: GoogleFonts.jetBrainsMono(color: mutedColor.withValues(alpha: 0.5)),
        prefixIcon: Icon(Icons.link, color: mutedColor, size: 20),
        filled: true,
        fillColor: inputColor,
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: colorScheme.primary),
        ),
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      ),
      validator: (value) {
        if (value != null && value.isNotEmpty && value.contains(' ')) {
          return l10n.deepLinkIdSpaceError;
        }
        return null;
      },
    );
  }

  Widget _buildAuthMethodToggle() {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Container(
      decoration: BoxDecoration(
        color: colorScheme.onSurface.withValues(alpha: isDark ? 0.1 : 0.05),
        borderRadius: BorderRadius.circular(8),
      ),
      padding: const EdgeInsets.all(4),
      child: Row(
        children: [
          Expanded(
            child: GestureDetector(
              onTap: () => setState(() => _authMethod = 'password'),
              child: Container(
                padding: const EdgeInsets.symmetric(vertical: 10),
                decoration: BoxDecoration(
                  color: _authMethod == 'password'
                      ? colorScheme.primary
                      : Colors.transparent,
                  borderRadius: BorderRadius.circular(6),
                  boxShadow: _authMethod == 'password'
                      ? [
                          BoxShadow(
                            color: Colors.black.withValues(alpha: 0.3),
                            blurRadius: 4,
                            offset: const Offset(0, 2),
                          ),
                        ]
                      : null,
                ),
                child: Text(
                  l10n.authMethodPassword,
                  textAlign: TextAlign.center,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                    color: _authMethod == 'password'
                        ? colorScheme.onPrimary
                        : mutedColor,
                  ),
                ),
              ),
            ),
          ),
          Expanded(
            child: GestureDetector(
              onTap: () => setState(() => _authMethod = 'key'),
              child: Container(
                padding: const EdgeInsets.symmetric(vertical: 10),
                decoration: BoxDecoration(
                  color: _authMethod == 'key'
                      ? colorScheme.primary
                      : Colors.transparent,
                  borderRadius: BorderRadius.circular(6),
                  boxShadow: _authMethod == 'key'
                      ? [
                          BoxShadow(
                            color: Colors.black.withValues(alpha: 0.3),
                            blurRadius: 4,
                            offset: const Offset(0, 2),
                          ),
                        ]
                      : null,
                ),
                child: Text(
                  l10n.authMethodPrivateKey,
                  textAlign: TextAlign.center,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                    color: _authMethod == 'key'
                        ? colorScheme.onPrimary
                        : mutedColor,
                  ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildPasswordInput() {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final inputColor = isDark ? DesignColors.inputDark : DesignColors.inputLight;
    return TextFormField(
      controller: _passwordController,
      obscureText: _obscurePassword,
      style: GoogleFonts.jetBrainsMono(fontSize: 14, color: colorScheme.onSurface),
      decoration: InputDecoration(
        hintText: l10n.passwordHint,
        hintStyle: GoogleFonts.jetBrainsMono(color: mutedColor.withValues(alpha: 0.5)),
        prefixIcon: Icon(Icons.key, color: mutedColor, size: 20),
        suffixIcon: IconButton(
          icon: Icon(
            _obscurePassword ? Icons.visibility_off : Icons.visibility,
            color: mutedColor,
            size: 20,
          ),
          onPressed: () => setState(() => _obscurePassword = !_obscurePassword),
        ),
        filled: true,
        fillColor: inputColor,
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide.none,
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: colorScheme.primary),
        ),
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      ),
      validator: (value) {
        if (!widget.isEditing && (value == null || value.isEmpty)) {
          return l10n.passwordEmptyError;
        }
        return null;
      },
    );
  }

  Widget _buildKeyDropdown(KeysState keysState) {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final inputColor = isDark ? DesignColors.inputDark : DesignColors.inputLight;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        DropdownButtonFormField<String>(
          value: _selectedKeyId,
          decoration: InputDecoration(
            prefixIcon: Icon(Icons.vpn_key_outlined, color: mutedColor, size: 20),
            filled: true,
            fillColor: inputColor,
            border: OutlineInputBorder(
              borderRadius: BorderRadius.circular(12),
              borderSide: BorderSide.none,
            ),
            contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
          ),
          dropdownColor: colorScheme.surface,
          style: GoogleFonts.spaceGrotesk(fontSize: 14, color: colorScheme.onSurface),
          items: keysState.keys.map((key) {
            return DropdownMenuItem(
              value: key.id,
              child: Text(key.name),
            );
          }).toList(),
          onChanged: (value) => setState(() => _selectedKeyId = value),
          validator: (value) {
            if (_authMethod == 'key' && value == null) {
              return 'Please select an SSH key';
            }
            return null;
          },
          hint: Text(
            keysState.keys.isEmpty ? l10n.keyDropdownNoKeys : l10n.keyDropdownSelect,
            style: GoogleFonts.spaceGrotesk(color: mutedColor),
          ),
        ),
        if (_authMethod == 'key' && keysState.keys.isEmpty) ...[
          const SizedBox(height: 8),
          Text(
            l10n.noKeysAvailableError,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              color: colorScheme.error,
            ),
          ),
        ],
      ],
    );
  }

  Widget _buildJumpHostSection(KeysState keysState) {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final inputColor = isDark ? DesignColors.inputDark : DesignColors.inputLight;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _buildSectionHeader(l10n.sectionJumpHost),
        Container(
          decoration: BoxDecoration(
            color: colorScheme.surface,
            borderRadius: BorderRadius.circular(16),
            border: Border.all(color: colorScheme.outline.withValues(alpha: 0.2)),
          ),
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              SwitchListTile(
                contentPadding: EdgeInsets.zero,
                title: Text(
                  l10n.jumpHostToggle,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 14,
                    fontWeight: FontWeight.w500,
                    color: colorScheme.onSurface,
                  ),
                ),
                value: _useJumpHost,
                onChanged: (v) => setState(() => _useJumpHost = v),
              ),
              if (_useJumpHost) ...[
                const SizedBox(height: 8),
                _buildFieldLabel(l10n.fieldJumpHost),
                const SizedBox(height: 8),
                TextFormField(
                  controller: _jumpHostController,
                  keyboardType: TextInputType.url,
                  style: GoogleFonts.jetBrainsMono(fontSize: 14, color: colorScheme.onSurface),
                  decoration: InputDecoration(
                    hintText: l10n.jumpHostHint,
                    hintStyle: GoogleFonts.jetBrainsMono(color: mutedColor.withValues(alpha: 0.5)),
                    filled: true,
                    fillColor: inputColor,
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(12),
                      borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
                    ),
                    enabledBorder: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(12),
                      borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
                    ),
                    focusedBorder: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(12),
                      borderSide: BorderSide(color: colorScheme.primary),
                    ),
                    contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
                  ),
                  validator: (value) {
                    if (_useJumpHost && (value == null || value.isEmpty)) {
                      return l10n.jumpHostEmptyError;
                    }
                    return null;
                  },
                ),
                const SizedBox(height: 16),
                Row(
                  children: [
                    Expanded(
                      flex: 1,
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          _buildFieldLabel(l10n.fieldJumpPort),
                          const SizedBox(height: 8),
                          TextFormField(
                            controller: _jumpPortController,
                            keyboardType: TextInputType.number,
                            style: GoogleFonts.jetBrainsMono(fontSize: 14, color: colorScheme.onSurface),
                            decoration: InputDecoration(
                              hintText: l10n.jumpPortHint,
                              hintStyle: GoogleFonts.jetBrainsMono(color: mutedColor.withValues(alpha: 0.5)),
                              filled: true,
                              fillColor: inputColor,
                              border: OutlineInputBorder(
                                borderRadius: BorderRadius.circular(12),
                                borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
                              ),
                              enabledBorder: OutlineInputBorder(
                                borderRadius: BorderRadius.circular(12),
                                borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
                              ),
                              focusedBorder: OutlineInputBorder(
                                borderRadius: BorderRadius.circular(12),
                                borderSide: BorderSide(color: colorScheme.primary),
                              ),
                              contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
                            ),
                          ),
                        ],
                      ),
                    ),
                    const SizedBox(width: 16),
                    Expanded(
                      flex: 2,
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          _buildFieldLabel(l10n.fieldJumpUsername),
                          const SizedBox(height: 8),
                          TextFormField(
                            controller: _jumpUsernameController,
                            style: GoogleFonts.jetBrainsMono(fontSize: 14, color: colorScheme.onSurface),
                            decoration: InputDecoration(
                              hintText: l10n.jumpUsernameHint,
                              hintStyle: GoogleFonts.jetBrainsMono(color: mutedColor.withValues(alpha: 0.5)),
                              prefixIcon: Icon(Icons.person_outline, color: mutedColor, size: 20),
                              filled: true,
                              fillColor: inputColor,
                              border: OutlineInputBorder(
                                borderRadius: BorderRadius.circular(12),
                                borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
                              ),
                              enabledBorder: OutlineInputBorder(
                                borderRadius: BorderRadius.circular(12),
                                borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
                              ),
                              focusedBorder: OutlineInputBorder(
                                borderRadius: BorderRadius.circular(12),
                                borderSide: BorderSide(color: colorScheme.primary),
                              ),
                              contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
                            ),
                          ),
                        ],
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 16),
                // Jump host auth method
                _buildFieldLabel(l10n.sectionAuthentication),
                const SizedBox(height: 8),
                Container(
                  decoration: BoxDecoration(
                    color: colorScheme.onSurface.withValues(alpha: isDark ? 0.1 : 0.05),
                    borderRadius: BorderRadius.circular(8),
                  ),
                  padding: const EdgeInsets.all(4),
                  child: Row(
                    children: [
                      Expanded(
                        child: GestureDetector(
                          onTap: () => setState(() => _jumpAuthMethod = 'password'),
                          child: Container(
                            padding: const EdgeInsets.symmetric(vertical: 10),
                            decoration: BoxDecoration(
                              color: _jumpAuthMethod == 'password'
                                  ? colorScheme.primary
                                  : Colors.transparent,
                              borderRadius: BorderRadius.circular(6),
                            ),
                            child: Text(
                              l10n.authMethodPassword,
                              textAlign: TextAlign.center,
                              style: GoogleFonts.spaceGrotesk(
                                fontSize: 12,
                                fontWeight: FontWeight.w700,
                                color: _jumpAuthMethod == 'password'
                                    ? colorScheme.onPrimary
                                    : mutedColor,
                              ),
                            ),
                          ),
                        ),
                      ),
                      Expanded(
                        child: GestureDetector(
                          onTap: () => setState(() => _jumpAuthMethod = 'key'),
                          child: Container(
                            padding: const EdgeInsets.symmetric(vertical: 10),
                            decoration: BoxDecoration(
                              color: _jumpAuthMethod == 'key'
                                  ? colorScheme.primary
                                  : Colors.transparent,
                              borderRadius: BorderRadius.circular(6),
                            ),
                            child: Text(
                              l10n.authMethodPrivateKey,
                              textAlign: TextAlign.center,
                              style: GoogleFonts.spaceGrotesk(
                                fontSize: 12,
                                fontWeight: FontWeight.w700,
                                color: _jumpAuthMethod == 'key'
                                    ? colorScheme.onPrimary
                                    : mutedColor,
                              ),
                            ),
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
                const SizedBox(height: 8),
                if (_jumpAuthMethod == 'key')
                  DropdownButtonFormField<String>(
                    value: _jumpSelectedKeyId,
                    decoration: InputDecoration(
                      prefixIcon: Icon(Icons.vpn_key_outlined, color: mutedColor, size: 20),
                      filled: true,
                      fillColor: inputColor,
                      border: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(12),
                        borderSide: BorderSide.none,
                      ),
                      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
                    ),
                    dropdownColor: colorScheme.surface,
                    style: GoogleFonts.spaceGrotesk(fontSize: 14, color: colorScheme.onSurface),
                    items: keysState.keys.map((key) {
                      return DropdownMenuItem(value: key.id, child: Text(key.name));
                    }).toList(),
                    onChanged: (value) => setState(() => _jumpSelectedKeyId = value),
                    hint: Text(
                      keysState.keys.isEmpty ? l10n.keyDropdownNoKeys : l10n.keyDropdownSelect,
                      style: GoogleFonts.spaceGrotesk(color: mutedColor),
                    ),
                  ),
                // For password auth, the jump host password will be fetched from secure storage
                // using the same pattern as the main connection password.
                // The hint tells the user the jump password is stored with the connection.
              ],
            ],
          ),
        ),
      ],
    );
  }

  Widget _buildProxySection() {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final inputColor = isDark ? DesignColors.inputDark : DesignColors.inputLight;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _buildSectionHeader(l10n.sectionProxy),
        Container(
          decoration: BoxDecoration(
            color: colorScheme.surface,
            borderRadius: BorderRadius.circular(16),
            border: Border.all(color: colorScheme.outline.withValues(alpha: 0.2)),
          ),
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              SwitchListTile(
                contentPadding: EdgeInsets.zero,
                title: Text(
                  l10n.proxyToggle,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 14,
                    fontWeight: FontWeight.w500,
                    color: colorScheme.onSurface,
                  ),
                ),
                value: _useProxy,
                onChanged: (v) => setState(() => _useProxy = v),
              ),
              if (_useProxy) ...[
                const SizedBox(height: 8),
                Row(
                  children: [
                    Expanded(
                      flex: 3,
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          _buildFieldLabel(l10n.fieldProxyHost),
                          const SizedBox(height: 8),
                          TextFormField(
                            controller: _proxyHostController,
                            keyboardType: TextInputType.url,
                            style: GoogleFonts.jetBrainsMono(fontSize: 14, color: colorScheme.onSurface),
                            decoration: InputDecoration(
                              hintText: l10n.proxyHostHint,
                              hintStyle: GoogleFonts.jetBrainsMono(color: mutedColor.withValues(alpha: 0.5)),
                              filled: true,
                              fillColor: inputColor,
                              border: OutlineInputBorder(
                                borderRadius: BorderRadius.circular(12),
                                borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
                              ),
                              enabledBorder: OutlineInputBorder(
                                borderRadius: BorderRadius.circular(12),
                                borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
                              ),
                              focusedBorder: OutlineInputBorder(
                                borderRadius: BorderRadius.circular(12),
                                borderSide: BorderSide(color: colorScheme.primary),
                              ),
                              contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
                            ),
                            validator: (value) {
                              if (_useProxy && (value == null || value.isEmpty)) {
                                return l10n.proxyHostEmptyError;
                              }
                              return null;
                            },
                          ),
                        ],
                      ),
                    ),
                    const SizedBox(width: 16),
                    Expanded(
                      flex: 1,
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          _buildFieldLabel(l10n.fieldProxyPort),
                          const SizedBox(height: 8),
                          TextFormField(
                            controller: _proxyPortController,
                            keyboardType: TextInputType.number,
                            style: GoogleFonts.jetBrainsMono(fontSize: 14, color: colorScheme.onSurface),
                            decoration: InputDecoration(
                              hintText: l10n.proxyPortHint,
                              hintStyle: GoogleFonts.jetBrainsMono(color: mutedColor.withValues(alpha: 0.5)),
                              filled: true,
                              fillColor: inputColor,
                              border: OutlineInputBorder(
                                borderRadius: BorderRadius.circular(12),
                                borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
                              ),
                              enabledBorder: OutlineInputBorder(
                                borderRadius: BorderRadius.circular(12),
                                borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
                              ),
                              focusedBorder: OutlineInputBorder(
                                borderRadius: BorderRadius.circular(12),
                                borderSide: BorderSide(color: colorScheme.primary),
                              ),
                              contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
                            ),
                          ),
                        ],
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 16),
                _buildFieldLabel(l10n.fieldProxyUsername),
                const SizedBox(height: 8),
                TextFormField(
                  controller: _proxyUsernameController,
                  style: GoogleFonts.jetBrainsMono(fontSize: 14, color: colorScheme.onSurface),
                  decoration: InputDecoration(
                    hintText: l10n.proxyUsernameHint,
                    hintStyle: GoogleFonts.jetBrainsMono(color: mutedColor.withValues(alpha: 0.5)),
                    filled: true,
                    fillColor: inputColor,
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(12),
                      borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
                    ),
                    enabledBorder: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(12),
                      borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
                    ),
                    focusedBorder: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(12),
                      borderSide: BorderSide(color: colorScheme.primary),
                    ),
                    contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
                  ),
                ),
                const SizedBox(height: 16),
                _buildFieldLabel(l10n.fieldProxyPassword),
                const SizedBox(height: 8),
                TextFormField(
                  controller: _proxyPasswordController,
                  obscureText: true,
                  style: GoogleFonts.jetBrainsMono(fontSize: 14, color: colorScheme.onSurface),
                  decoration: InputDecoration(
                    hintText: l10n.proxyPasswordHint,
                    hintStyle: GoogleFonts.jetBrainsMono(color: mutedColor.withValues(alpha: 0.5)),
                    filled: true,
                    fillColor: inputColor,
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(12),
                      borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
                    ),
                    enabledBorder: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(12),
                      borderSide: BorderSide(color: colorScheme.outline.withValues(alpha: 0.2)),
                    ),
                    focusedBorder: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(12),
                      borderSide: BorderSide(color: colorScheme.primary),
                    ),
                    contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
                  ),
                ),
              ],
            ],
          ),
        ),
      ],
    );
  }

  Widget _buildBottomAction() {
    final l10n = AppLocalizations.of(context)!;
    final colorScheme = Theme.of(context).colorScheme;
    return Positioned(
      left: 0,
      right: 0,
      bottom: 0,
      child: Container(
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          gradient: LinearGradient(
            begin: Alignment.topCenter,
            end: Alignment.bottomCenter,
            colors: [
              Theme.of(context).scaffoldBackgroundColor.withValues(alpha: 0),
              Theme.of(context).scaffoldBackgroundColor,
              Theme.of(context).scaffoldBackgroundColor,
            ],
          ),
        ),
        child: SafeArea(
          child: SizedBox(
            width: double.infinity,
            height: 56,
            child: ElevatedButton(
              onPressed: _isTesting ? null : _testConnection,
              style: ElevatedButton.styleFrom(
                backgroundColor: colorScheme.primary,
                foregroundColor: colorScheme.onPrimary,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(16),
                ),
                elevation: 0,
              ),
              child: _isTesting
                  ? SizedBox(
                      width: 24,
                      height: 24,
                      child: CircularProgressIndicator(
                        strokeWidth: 2,
                        color: colorScheme.onPrimary,
                      ),
                    )
                  : Row(
                      mainAxisAlignment: MainAxisAlignment.center,
                      children: [
                        const Icon(Icons.terminal, size: 20),
                        const SizedBox(width: 12),
                        Text(
                          l10n.testConnectionButton,
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 16,
                            fontWeight: FontWeight.w700,
                            letterSpacing: -0.5,
                          ),
                        ),
                      ],
                    ),
            ),
          ),
        ),
      ),
    );
  }

  Future<void> _testConnection() async {
    if (!_formKey.currentState!.validate()) {
      return;
    }

    setState(() => _isTesting = true);

    final sshClient = SshClient();
    String? errorMessage;
    bool tmuxInstalled = false;

    try {
      // 認証情報を準備
      String? password;
      String? privateKey;
      String? passphrase;

      final l10n = AppLocalizations.of(context)!;
      if (_authMethod == 'password') {
        password = _passwordController.text;
        if (password.isEmpty) {
          throw SshAuthenticationError(l10n.passwordRequiredError);
        }
      } else if (_authMethod == 'key') {
        if (_selectedKeyId == null) {
          throw SshAuthenticationError(l10n.keyRequiredError);
        }
        final storage = SecureStorageService();
        privateKey = await storage.getPrivateKey(_selectedKeyId!);
        passphrase = await storage.getPassphrase(_selectedKeyId!);
        if (privateKey == null) {
          throw SshAuthenticationError(l10n.privateKeyNotFoundError);
        }
      }

      // SSH接続テスト
      final customTmuxPath = _tmuxPathController.text.trim();
      // Prepare jump host auth if needed
      String? jumpPassword;
      String? jumpPrivateKey;
      String? jumpPassphrase;
      if (_useJumpHost) {
        if (_jumpAuthMethod == 'key' && _jumpSelectedKeyId != null) {
          final storage = SecureStorageService();
          jumpPrivateKey = await storage.getPrivateKey(_jumpSelectedKeyId!);
          jumpPassphrase = await storage.getPassphrase(_jumpSelectedKeyId!);
        } else {
          // For jump host password auth, reuse main password for now
          jumpPassword = password;
        }
      }

      await sshClient.connect(
        host: _hostController.text.trim(),
        port: int.tryParse(_portController.text) ?? 22,
        username: _usernameController.text.trim(),
        options: SshConnectOptions(
          password: password,
          privateKey: privateKey,
          passphrase: passphrase,
          tmuxPath: customTmuxPath.isNotEmpty ? customTmuxPath : null,
          jumpHost: _useJumpHost ? _jumpHostController.text.trim() : null,
          jumpPort: _useJumpHost ? int.tryParse(_jumpPortController.text) ?? 22 : null,
          jumpUsername: _useJumpHost && _jumpUsernameController.text.trim().isNotEmpty
              ? _jumpUsernameController.text.trim() : null,
          jumpPassword: jumpPassword,
          jumpPrivateKey: jumpPrivateKey,
          jumpPassphrase: jumpPassphrase,
          proxyHost: _useProxy ? _proxyHostController.text.trim() : null,
          proxyPort: _useProxy ? int.tryParse(_proxyPortController.text) ?? 1080 : null,
          proxyUsername: _useProxy && _proxyUsernameController.text.trim().isNotEmpty
              ? _proxyUsernameController.text.trim() : null,
          proxyPassword: _useProxy && _proxyPasswordController.text.trim().isNotEmpty
              ? _proxyPasswordController.text.trim() : null,
        ),
      );

      // tmuxがインストールされているか確認
      // connect()内でPersistentShell（対話シェル）経由で絶対パスを検出済み
      tmuxInstalled = sshClient.tmuxPath != null;
    } on SshAuthenticationError catch (e) {
      errorMessage = AppLocalizations.of(context)!.authFailedError(e.message);
    } on SshConnectionError catch (e) {
      errorMessage = AppLocalizations.of(context)!.connectionFailedError(e.message);
    } catch (e) {
      errorMessage = AppLocalizations.of(context)!.generalError(e.toString());
    } finally {
      await sshClient.dispose();
    }

    if (mounted) {
      setState(() => _isTesting = false);

      if (errorMessage != null) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(errorMessage),
            backgroundColor: DesignColors.error,
            duration: const Duration(seconds: 4),
          ),
        );
      } else {
        final l10nMsg = AppLocalizations.of(context)!;
        final message = tmuxInstalled
            ? l10nMsg.connectionSuccessWithTmux
            : l10nMsg.connectionSuccessNoTmux;
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(message),
            backgroundColor: tmuxInstalled ? DesignColors.success : DesignColors.warning,
            duration: const Duration(seconds: 3),
          ),
        );
      }
    }
  }

  Future<void> _save() async {
    developer.log('_save() called', name: 'ConnectionForm');

    if (!_formKey.currentState!.validate()) {
      developer.log('Form validation failed', name: 'ConnectionForm');
      return;
    }

    setState(() => _isSaving = true);
    developer.log('Starting save process...', name: 'ConnectionForm');

    try {
      final connectionId = widget.connectionId ?? const Uuid().v4();
      developer.log('Connection ID: $connectionId (isEditing: ${widget.isEditing})', name: 'ConnectionForm');

      if (_authMethod == 'password' && _passwordController.text.isNotEmpty) {
        developer.log('Saving password to secure storage...', name: 'ConnectionForm');
        final storage = SecureStorageService();
        await storage.savePassword(connectionId, _passwordController.text);
        developer.log('Password saved successfully', name: 'ConnectionForm');
      }

      final saveTmuxPath = _tmuxPathController.text.trim();
      final saveDeepLinkId = _deepLinkIdController.text.trim();
      final connection = Connection(
        id: connectionId,
        name: _nameController.text.trim(),
        host: _hostController.text.trim(),
        port: int.parse(_portController.text),
        username: _usernameController.text.trim(),
        authMethod: _authMethod,
        keyId: _authMethod == 'key' ? _selectedKeyId : null,
        tmuxPath: saveTmuxPath.isNotEmpty ? saveTmuxPath : null,
        terminalMode: _terminalMode == 'tmux' ? null : _terminalMode,
        deepLinkId: saveDeepLinkId.isNotEmpty ? saveDeepLinkId : null,
        createdAt: widget.isEditing
            ? ref.read(connectionsProvider.notifier).getById(connectionId)?.createdAt ?? DateTime.now()
            : DateTime.now(),
        jumpHost: _useJumpHost ? _jumpHostController.text.trim() : null,
        jumpPort: _useJumpHost ? int.tryParse(_jumpPortController.text) ?? 22 : null,
        jumpUsername: _useJumpHost && _jumpUsernameController.text.trim().isNotEmpty
            ? _jumpUsernameController.text.trim() : null,
        jumpAuthMethod: _useJumpHost ? _jumpAuthMethod : null,
        jumpKeyId: _useJumpHost && _jumpAuthMethod == 'key' ? _jumpSelectedKeyId : null,
        proxyHost: _useProxy ? _proxyHostController.text.trim() : null,
        proxyPort: _useProxy ? int.tryParse(_proxyPortController.text) ?? 1080 : null,
        proxyUsername: _useProxy && _proxyUsernameController.text.trim().isNotEmpty
            ? _proxyUsernameController.text.trim() : null,
        proxyPassword: _useProxy && _proxyPasswordController.text.trim().isNotEmpty
            ? _proxyPasswordController.text.trim() : null,
      );
      developer.log('Connection object created: ${connection.name}', name: 'ConnectionForm');

      if (widget.isEditing) {
        developer.log('Updating existing connection...', name: 'ConnectionForm');
        await ref.read(connectionsProvider.notifier).update(connection);
        developer.log('Connection updated successfully', name: 'ConnectionForm');
      } else {
        developer.log('Adding new connection...', name: 'ConnectionForm');
        await ref.read(connectionsProvider.notifier).add(connection);
        developer.log('Connection added successfully', name: 'ConnectionForm');
      }

      // When a connection is switched to raw mode, any existing tmux session
      // entries are now meaningless — purge them so the dashboard and the
      // connection card don't show orphans.
      if (connection.isRawMode) {
        ref
            .read(activeSessionsProvider.notifier)
            .removeSessionsForConnection(connectionId);
      }

      developer.log('Save completed, popping navigator...', name: 'ConnectionForm');
      if (mounted) {
        // On create, return the new id so callers (e.g. the hub host
        // detail sheet) can record a binding. On edit, keep the legacy
        // `true` signal — the id is already known to the caller.
        Navigator.of(context).pop(widget.isEditing ? true : connectionId);
        developer.log('Navigator popped', name: 'ConnectionForm');
      }
    } catch (e, stackTrace) {
      developer.log('Error saving connection: $e', name: 'ConnectionForm', error: e, stackTrace: stackTrace);
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(AppLocalizations.of(context)!.savingConnectionError(e.toString())),
            backgroundColor: DesignColors.error,
          ),
        );
      }
    } finally {
      if (mounted) {
        setState(() => _isSaving = false);
        developer.log('_isSaving set to false', name: 'ConnectionForm');
      }
    }
  }
}
