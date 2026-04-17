/// Provides version information injected at build time.
///
/// Priority:
/// 1. APP_VERSION (CI sets from release tag via --dart-define)
/// 2. GIT_REF (branch@commit hash)
/// 3. Pubspec version (hardcoded fallback)
class VersionInfo {
  static const String _appVersion = String.fromEnvironment('APP_VERSION');
  static const String _gitRef = String.fromEnvironment('GIT_REF');

  /// Fallback version matching pubspec.yaml — update when bumping version.
  static const String _pubspecVersion = '1.0.29-alpha';

  static String get version {
    if (_appVersion.isNotEmpty) return _stripLeadingV(_appVersion);
    if (_gitRef.isNotEmpty) return _gitRef;
    return _pubspecVersion;
  }

  /// CI passes APP_VERSION as the raw tag name (e.g. "v1.0.27-alpha").
  /// The rest of the app assumes a clean semver — drop the leading v
  /// here so callers don't have to special-case it (broken update
  /// comparison, "vv1.0.28-alpha" in feedback email subject, etc.).
  static String _stripLeadingV(String s) {
    if (s.isEmpty) return s;
    final c = s.codeUnitAt(0);
    return (c == 0x76 || c == 0x56) ? s.substring(1) : s;
  }
}
