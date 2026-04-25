import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../l10n/app_localizations.dart';
import '../../../theme/design_colors.dart';

/// Dialog for creating a tmux session. Shared between the connection-list
/// "New Session" button (offline session bring-up) and the in-terminal
/// session selector (mid-flight bring-up). Validation, default-name
/// generation, and l10n keys live here so the two flows stay in sync.
class NewSessionDialog extends StatefulWidget {
  final List<String> existingSessionNames;

  const NewSessionDialog({super.key, required this.existingSessionNames});

  @override
  State<NewSessionDialog> createState() => _NewSessionDialogState();
}

class _NewSessionDialogState extends State<NewSessionDialog> {
  final _formKey = GlobalKey<FormState>();
  late final TextEditingController _nameController;

  @override
  void initState() {
    super.initState();
    _nameController = TextEditingController(text: _generateDefaultName());
  }

  @override
  void dispose() {
    _nameController.dispose();
    super.dispose();
  }

  String _generateDefaultName() {
    int index = 1;
    while (widget.existingSessionNames.contains('session-$index')) {
      index++;
    }
    return 'session-$index';
  }

  String? _validateSessionName(String? value) {
    final l10n = AppLocalizations.of(context)!;
    if (value == null || value.isEmpty) {
      return l10n.sessionNameEmptyError;
    }
    if (!RegExp(r'^[a-zA-Z0-9_.-]+$').hasMatch(value)) {
      return l10n.sessionNameInvalidError;
    }
    if (widget.existingSessionNames.contains(value)) {
      return l10n.sessionNameExistsError(value);
    }
    return null;
  }

  void _submit() {
    if (_formKey.currentState!.validate()) {
      Navigator.pop(context, _nameController.text);
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;
    final l10n = AppLocalizations.of(context)!;
    return AlertDialog(
      title: Text(
        l10n.newSession,
        style: GoogleFonts.spaceGrotesk(
          fontWeight: FontWeight.w700,
        ),
      ),
      content: Form(
        key: _formKey,
        child: TextFormField(
          controller: _nameController,
          autofocus: true,
          decoration: InputDecoration(
            labelText: l10n.newSession,
            hintText: l10n.sessionNameHint,
            hintStyle: GoogleFonts.jetBrainsMono(
              fontSize: 14,
              color: isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
            ),
            filled: true,
            fillColor: isDark ? DesignColors.inputDark : DesignColors.inputLight,
            border: OutlineInputBorder(
              borderRadius: BorderRadius.circular(12),
              borderSide: BorderSide.none,
            ),
            focusedBorder: OutlineInputBorder(
              borderRadius: BorderRadius.circular(12),
              borderSide: BorderSide(color: colorScheme.primary),
            ),
            errorBorder: OutlineInputBorder(
              borderRadius: BorderRadius.circular(12),
              borderSide: const BorderSide(color: DesignColors.error),
            ),
            focusedErrorBorder: OutlineInputBorder(
              borderRadius: BorderRadius.circular(12),
              borderSide: const BorderSide(color: DesignColors.error),
            ),
          ),
          style: GoogleFonts.jetBrainsMono(fontSize: 14),
          validator: _validateSessionName,
          onFieldSubmitted: (_) => _submit(),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: Text(l10n.buttonCancel),
        ),
        FilledButton(
          onPressed: _submit,
          child: Text(l10n.buttonCreate),
        ),
      ],
    );
  }
}
