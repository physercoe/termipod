import 'package:flutter/material.dart';
import 'package:termipod/l10n/app_localizations.dart';

/// テーマ選択ダイアログ
class ThemeDialog extends StatelessWidget {
  final bool isDarkMode;

  const ThemeDialog({
    super.key,
    required this.isDarkMode,
  });

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text(AppLocalizations.of(context)!.settingTheme),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          RadioListTile<bool>(
            title: Text(AppLocalizations.of(context)!.themeDark),
            value: true,
            groupValue: isDarkMode,
            onChanged: (value) {
              if (value != null) {
                Navigator.pop(context, value);
              }
            },
          ),
          RadioListTile<bool>(
            title: Text(AppLocalizations.of(context)!.themeLight),
            value: false,
            groupValue: isDarkMode,
            onChanged: (value) {
              if (value != null) {
                Navigator.pop(context, value);
              }
            },
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: Text(AppLocalizations.of(context)!.buttonCancel),
        ),
      ],
    );
  }
}
