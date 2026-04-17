import 'dart:async';
import 'dart:convert';
import 'dart:io';

/// What a checker returns when a newer build is available (or null if
/// the app is already current / the check couldn't complete).
class UpdateInfo {
  /// Version tag of the remote release, stripped of the leading `v`.
  /// e.g. `1.0.27-alpha`.
  final String version;

  /// Free-form notes from the release body. May be empty.
  final String releaseNotes;

  /// Direct platform install URL (APK for Android, store page for iOS).
  /// Null when the release has no artifact for the current platform —
  /// the UI should fall back to [releasePageUrl].
  final String? downloadUrl;

  /// Human-browsable release page, used as fallback + "view details".
  final String releasePageUrl;

  const UpdateInfo({
    required this.version,
    required this.releaseNotes,
    required this.downloadUrl,
    required this.releasePageUrl,
  });
}

/// Pluggable update source. The GitHub checker ships today; store
/// checkers (App Store, Play Store) slot in behind the same interface
/// once we publish to those channels.
abstract class UpdateChecker {
  /// Returns an [UpdateInfo] if the remote version is strictly newer
  /// than [currentVersion]; null if we're current or the check failed.
  /// Implementations should swallow network errors and return null —
  /// the caller handles the "couldn't check" UX separately via
  /// [checkForUpdateOrThrow] if they want to distinguish the two.
  Future<UpdateInfo?> checkForUpdate(String currentVersion);

  /// Same as [checkForUpdate] but rethrows network / parse errors so
  /// the UI can show a specific failure reason.
  Future<UpdateInfo?> checkForUpdateOrThrow(String currentVersion);
}

/// Checker that reads `/releases/latest` from the GitHub REST API.
///
/// Uses `dart:io` HttpClient directly — we don't want to pull in the
/// `http` package just for a single unauthenticated GET. GitHub's
/// unauthenticated rate limit (60 req/h/IP) is plenty for manual
/// "Check for updates" presses.
class GitHubUpdateChecker implements UpdateChecker {
  /// owner/repo slug, e.g. `physercoe/termipod`.
  final String repo;

  /// On Android we look for the APK asset. The naming convention in
  /// this repo is `termipod-v<ver>-android.apk`, but we match on the
  /// extension to stay resilient to renames.
  final String platformAssetSuffix;

  const GitHubUpdateChecker({
    required this.repo,
    required this.platformAssetSuffix,
  });

  @override
  Future<UpdateInfo?> checkForUpdate(String currentVersion) async {
    try {
      return await checkForUpdateOrThrow(currentVersion);
    } catch (_) {
      return null;
    }
  }

  @override
  Future<UpdateInfo?> checkForUpdateOrThrow(String currentVersion) async {
    final uri = Uri.parse('https://api.github.com/repos/$repo/releases/latest');
    final client = HttpClient();
    try {
      client.connectionTimeout = const Duration(seconds: 10);
      final req = await client.getUrl(uri);
      req.headers.set(HttpHeaders.acceptHeader, 'application/vnd.github+json');
      req.headers.set(HttpHeaders.userAgentHeader, 'termipod-update-checker');
      final res = await req.close().timeout(const Duration(seconds: 15));
      if (res.statusCode != 200) {
        throw HttpException('GitHub API returned ${res.statusCode}');
      }
      final body = await res.transform(utf8.decoder).join();
      final json = jsonDecode(body) as Map<String, dynamic>;

      final tagName = (json['tag_name'] as String?) ?? '';
      final remoteVersion = _stripLeadingV(tagName);
      if (remoteVersion.isEmpty) return null;

      if (_compareVersions(remoteVersion, currentVersion) <= 0) {
        return null;
      }

      String? downloadUrl;
      final assets = json['assets'];
      if (assets is List) {
        for (final asset in assets) {
          if (asset is Map<String, dynamic>) {
            final name = (asset['name'] as String?) ?? '';
            if (name.toLowerCase().endsWith(platformAssetSuffix)) {
              downloadUrl = asset['browser_download_url'] as String?;
              break;
            }
          }
        }
      }

      return UpdateInfo(
        version: remoteVersion,
        releaseNotes: (json['body'] as String?)?.trim() ?? '',
        downloadUrl: downloadUrl,
        releasePageUrl: (json['html_url'] as String?) ??
            'https://github.com/$repo/releases/latest',
      );
    } finally {
      client.close(force: true);
    }
  }
}

/// Placeholder for an iOS App Store checker. Slots in via [UpdateService]
/// once we ship through TestFlight/App Store. Currently unused — iOS
/// falls through to the GitHub checker.
///
/// Implementation sketch: hit
/// `https://itunes.apple.com/lookup?bundleId=com.remoteagent.termipod`,
/// parse the `version` field, compare, and return a store-page URL as
/// the [UpdateInfo.downloadUrl] (which the UI opens via url_launcher).
class AppStoreUpdateChecker implements UpdateChecker {
  final String bundleId;
  const AppStoreUpdateChecker({required this.bundleId});

  @override
  Future<UpdateInfo?> checkForUpdate(String currentVersion) async => null;

  @override
  Future<UpdateInfo?> checkForUpdateOrThrow(String currentVersion) async =>
      throw UnimplementedError('App Store update check not yet wired up');
}

/// Placeholder for a Google Play checker. Google's in-app update API
/// needs a native Android plugin (`in_app_update` or similar) — we'll
/// swap that in when the app is listed on Play.
class PlayStoreUpdateChecker implements UpdateChecker {
  const PlayStoreUpdateChecker();

  @override
  Future<UpdateInfo?> checkForUpdate(String currentVersion) async => null;

  @override
  Future<UpdateInfo?> checkForUpdateOrThrow(String currentVersion) async =>
      throw UnimplementedError('Play Store update check not yet wired up');
}

/// Picks the checker for the current platform + distribution channel.
///
/// Today everything routes to GitHub since that's our only release
/// channel. When we start shipping to the App Store / Play Store,
/// swap the relevant branch without touching callers.
class UpdateService {
  static const String _repo = 'physercoe/termipod';

  static UpdateChecker defaultChecker() {
    if (Platform.isAndroid) {
      return const GitHubUpdateChecker(
        repo: _repo,
        platformAssetSuffix: '.apk',
      );
    }
    if (Platform.isIOS) {
      // TODO: return AppStoreUpdateChecker once we're on TestFlight/App Store.
      // Until then, point iOS users at the GitHub release page (the
      // unsigned .ipa won't install directly without sideloading — the
      // UI falls back to opening the release page).
      return const GitHubUpdateChecker(
        repo: _repo,
        platformAssetSuffix: '.ipa',
      );
    }
    // Desktop / other: point at the GitHub release page as a generic
    // fallback. No downloadUrl will match, so the dialog shows the
    // "View release" action only.
    return const GitHubUpdateChecker(
      repo: _repo,
      platformAssetSuffix: '__no_desktop_asset__',
    );
  }
}

/// Strips a leading `v` or `V` from a tag name.
String _stripLeadingV(String tag) {
  if (tag.isEmpty) return tag;
  final c = tag.codeUnitAt(0);
  if (c == 0x76 || c == 0x56) return tag.substring(1);
  return tag;
}

/// Three-way compare of semver-ish strings like `1.0.27-alpha`.
///
/// - Compares numeric major.minor.patch first.
/// - If those are equal, a build *without* a prerelease suffix
///   (e.g. `1.0.27`) is considered newer than one *with* a suffix
///   (e.g. `1.0.27-alpha`) — matching SemVer precedence.
/// - Within prereleases, a simple lexicographic compare is used; good
///   enough for our `-alpha` / `-beta` cadence without pulling in
///   pub_semver for one comparison.
///
/// Returns negative if `a < b`, positive if `a > b`, zero if equal.
int _compareVersions(String a, String b) {
  final aParts = _splitVersion(a);
  final bParts = _splitVersion(b);

  for (int i = 0; i < 3; i++) {
    final cmp = aParts.nums[i].compareTo(bParts.nums[i]);
    if (cmp != 0) return cmp;
  }

  // Numeric core equal — prerelease discrimination.
  final aPre = aParts.pre;
  final bPre = bParts.pre;
  if (aPre.isEmpty && bPre.isEmpty) return 0;
  if (aPre.isEmpty) return 1; // release beats prerelease
  if (bPre.isEmpty) return -1;
  return aPre.compareTo(bPre);
}

class _VersionParts {
  final List<int> nums; // length 3: major, minor, patch
  final String pre;     // prerelease suffix or ''
  const _VersionParts(this.nums, this.pre);
}

_VersionParts _splitVersion(String v) {
  // CI ships APP_VERSION as the raw tag name (e.g. "v1.0.27-alpha"),
  // while remote tag_name goes through _stripLeadingV before reaching
  // here. Normalize both sides so int.tryParse('v1') doesn't fall back
  // to 0 and make the local build look older than itself.
  final normalized = _stripLeadingV(v);
  var core = normalized;
  var pre = '';
  final dash = normalized.indexOf('-');
  if (dash >= 0) {
    core = normalized.substring(0, dash);
    pre = normalized.substring(dash + 1);
  }
  final parts = core.split('.');
  final nums = <int>[0, 0, 0];
  for (int i = 0; i < 3 && i < parts.length; i++) {
    nums[i] = int.tryParse(parts[i]) ?? 0;
  }
  return _VersionParts(nums, pre);
}
