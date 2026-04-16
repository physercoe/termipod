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
  static const String _pubspecVersion = '1.0.9-alpha';

  static String get version {
    if (_appVersion.isNotEmpty) return _appVersion;
    if (_gitRef.isNotEmpty) return _gitRef;
    return _pubspecVersion;
  }
}
