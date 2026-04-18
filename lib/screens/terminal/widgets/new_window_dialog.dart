import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../l10n/app_localizations.dart';
import '../../../theme/design_colors.dart';

/// ウィンドウ名入力ダイアログ
class NewWindowDialog extends StatefulWidget {
  final List<String> existingWindowNames;

  const NewWindowDialog({super.key, required this.existingWindowNames});

  @override
  State<NewWindowDialog> createState() => _NewWindowDialogState();
}

class _NewWindowDialogState extends State<NewWindowDialog> {
  final _formKey = GlobalKey<FormState>();
  final _nameController = TextEditingController();

  @override
  void dispose() {
    _nameController.dispose();
    super.dispose();
  }

  String? _validateWindowName(String? value) {
    if (value == null || value.isEmpty) {
      return null;
    }
    if (value.length > 50) {
      return 'Window name must be 50 characters or less';
    }
    if (!RegExp(r'^[a-zA-Z0-9_-]+$').hasMatch(value)) {
      return 'Only letters, numbers, - and _ allowed';
    }
    if (widget.existingWindowNames.contains(value)) {
      return 'Window "$value" already exists';
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
    return AlertDialog(
      title: Text(
        AppLocalizations.of(context)!.newWindowTitle,
        style: GoogleFonts.spaceGrotesk(
          fontWeight: FontWeight.w700,
        ),
      ),
      content: Form(
        key: _formKey,
        child: TextFormField(
          controller: _nameController,
          autofocus: true,
          maxLength: 50,
          decoration: InputDecoration(
            labelText: AppLocalizations.of(context)!.newWindowTitle,
            hintText: AppLocalizations.of(context)!.newWindowHint,
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
          validator: _validateWindowName,
          onFieldSubmitted: (_) => _submit(),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: Text(AppLocalizations.of(context)!.buttonCancel),
        ),
        FilledButton(
          onPressed: _submit,
          child: Text(AppLocalizations.of(context)!.buttonCreate),
        ),
      ],
    );
  }
}
