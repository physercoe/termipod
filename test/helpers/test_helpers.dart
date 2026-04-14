import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:termipod/l10n/app_localizations.dart';

/// L10n delegates and locales for tests that render widgets using AppLocalizations.
const testLocalizationsDelegates = [
  AppLocalizations.delegate,
  GlobalMaterialLocalizations.delegate,
  GlobalWidgetsLocalizations.delegate,
  GlobalCupertinoLocalizations.delegate,
];

const testSupportedLocales = AppLocalizations.supportedLocales;
