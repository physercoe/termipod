import 'package:flutter/material.dart';

import '../../services/version_info.dart';

/// ライセンス一覧画面
class LicensesScreen extends StatelessWidget {
  const LicensesScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Theme(
      data: Theme.of(context),
      child: LicensePage(
        applicationName: 'TermiPod',
        applicationVersion: VersionInfo.version,
        applicationLegalese: '© 2025 mox',
      ),
    );
  }
}
