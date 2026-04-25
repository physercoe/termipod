import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../l10n/app_localizations.dart';
import '../../../theme/design_colors.dart';

/// Name-input dialog used by both "new window" and "new session" sheets.
/// The validator/labels are parameterised so the same dialog serves both
/// flows without duplicating the form layout.
class NewWindowDialog extends StatefulWidget {
  final List<String> existingWindowNames;
  final String? title;
  final String? hint;
  final String entityLabel;
  final bool requireName;

  const NewWindowDialog({
    super.key,
    required this.existingWindowNames,
    this.title,
    this.hint,
    this.entityLabel = 'Window',
    this.requireName = false,
  });

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
      if (widget.requireName) {
        return '${widget.entityLabel} name is required';
      }
      return null;
    }
    if (value.length > 50) {
      return '${widget.entityLabel} name must be 50 characters or less';
    }
    if (!RegExp(r'^[a-zA-Z0-9_-]+$').hasMatch(value)) {
      return 'Only letters, numbers, - and _ allowed';
    }
    if (widget.existingWindowNames.contains(value)) {
      return '${widget.entityLabel} "$value" already exists';
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
    final title = widget.title ?? AppLocalizations.of(context)!.newWindowTitle;
    final hint = widget.hint ?? AppLocalizations.of(context)!.newWindowHint;
    return AlertDialog(
      title: Text(
        title,
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
            labelText: title,
            hintText: hint,
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
