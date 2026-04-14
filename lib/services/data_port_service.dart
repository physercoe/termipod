import 'dart:convert';

import 'package:shared_preferences/shared_preferences.dart';

import '../services/keychain/secure_storage.dart';
import '../services/version_info.dart';

enum ImportCategory { connections, sshKeys, snippets, history, actionBar, settings }

class ImportResult {
  final int connectionsAdded;
  final int connectionsSkipped;
  final int keysAdded;
  final int keysSkipped;
  final int snippetsAdded;
  final int passwordsAdded;
  final int historyMerged;
  final bool settingsImported;
  final bool profilesImported;

  const ImportResult({
    this.connectionsAdded = 0,
    this.connectionsSkipped = 0,
    this.keysAdded = 0,
    this.keysSkipped = 0,
    this.snippetsAdded = 0,
    this.passwordsAdded = 0,
    this.historyMerged = 0,
    this.settingsImported = false,
    this.profilesImported = false,
  });
}

class BackupSummary {
  final int connections;
  final int keys;
  final int snippets;
  final int historyItems;
  final bool hasSettings;
  final bool hasProfiles;

  const BackupSummary({
    this.connections = 0,
    this.keys = 0,
    this.snippets = 0,
    this.historyItems = 0,
    this.hasSettings = false,
    this.hasProfiles = false,
  });
}

class DataPortService {
  final SecureStorageService _secureStorage;

  DataPortService(this._secureStorage);

  // -----------------------------------------------------------------------
  // Export
  // -----------------------------------------------------------------------

  Future<Map<String, dynamic>> exportData() async {
    final prefs = await SharedPreferences.getInstance();

    return {
      'format': 'termipod-backup',
      'version': 1,
      'exportedAt': DateTime.now().toUtc().toIso8601String(),
      'appVersion': VersionInfo.version,
      'data': {
        'connections': await _exportConnections(prefs),
        'sshKeys': await _exportSshKeys(prefs),
        'passwords': await _exportPasswords(prefs),
        'snippets': _exportSnippets(prefs),
        'history': _exportHistory(prefs),
        'actionBar': _exportActionBar(prefs),
        'settings': _exportSettings(prefs),
      },
    };
  }

  Future<List<dynamic>> _exportConnections(SharedPreferences prefs) async {
    final raw = prefs.getString('connections');
    if (raw == null) return [];
    return jsonDecode(raw) as List;
  }

  Future<Map<String, dynamic>> _exportSshKeys(SharedPreferences prefs) async {
    final raw = prefs.getString('ssh_keys_meta');
    final meta = raw != null ? jsonDecode(raw) as List : [];

    final privateKeys = <String, String>{};
    final passphrases = <String, String>{};

    for (final km in meta) {
      final id = (km as Map<String, dynamic>)['id'] as String;
      final pk = await _secureStorage.getPrivateKey(id);
      if (pk != null) privateKeys[id] = pk;
      final pp = await _secureStorage.getPassphrase(id);
      if (pp != null) passphrases[id] = pp;
    }

    return {
      'meta': meta,
      'privateKeys': privateKeys,
      'passphrases': passphrases,
    };
  }

  Future<Map<String, String>> _exportPasswords(SharedPreferences prefs) async {
    final raw = prefs.getString('connections');
    if (raw == null) return {};
    final connections = jsonDecode(raw) as List;

    final passwords = <String, String>{};
    for (final c in connections) {
      final id = (c as Map<String, dynamic>)['id'] as String;
      final pw = await _secureStorage.getPassword(id);
      if (pw != null) passwords[id] = pw;
      // Also export jump host password if applicable
      final jumpId = '${id}_jump';
      final jumpPw = await _secureStorage.getPassword(jumpId);
      if (jumpPw != null) passwords[jumpId] = jumpPw;
    }

    return passwords;
  }

  Map<String, dynamic> _exportSnippets(SharedPreferences prefs) {
    final items = prefs.getString('snippets');
    final overrides = prefs.getString('snippet_preset_overrides');
    final deleted = prefs.getString('snippet_deleted_presets');

    return {
      'items': items != null ? jsonDecode(items) : [],
      'presetOverrides': overrides != null ? jsonDecode(overrides) : {},
      'deletedPresets': deleted != null ? jsonDecode(deleted) : [],
    };
  }

  List<String> _exportHistory(SharedPreferences prefs) {
    final raw = prefs.getString('settings_action_bar_command_history');
    if (raw == null) return [];
    return (jsonDecode(raw) as List).cast<String>();
  }

  Map<String, dynamic> _exportActionBar(SharedPreferences prefs) {
    return {
      'activeProfile': prefs.getString('settings_action_bar_active_profile'),
      'customProfiles': prefs.getString('settings_action_bar_custom_profiles') != null
          ? jsonDecode(prefs.getString('settings_action_bar_custom_profiles')!)
          : [],
      'deletedPresets': prefs.getString('settings_action_bar_deleted_presets') != null
          ? jsonDecode(prefs.getString('settings_action_bar_deleted_presets')!)
          : [],
      'composeMode': prefs.getBool('settings_action_bar_compose_mode') ?? true,
      'panelProfiles': prefs.getString('settings_action_bar_panel_profiles') != null
          ? jsonDecode(prefs.getString('settings_action_bar_panel_profiles')!)
          : {},
    };
  }

  Map<String, dynamic> _exportSettings(SharedPreferences prefs) {
    final result = <String, dynamic>{};
    for (final key in _settingsKeys) {
      final value = prefs.get(key);
      if (value != null) result[key] = value;
    }
    return result;
  }

  // -----------------------------------------------------------------------
  // Import
  // -----------------------------------------------------------------------

  static BackupSummary summarize(Map<String, dynamic> backup) {
    final data = backup['data'] as Map<String, dynamic>? ?? {};
    final connections = data['connections'] as List? ?? [];
    final sshKeys = data['sshKeys'] as Map<String, dynamic>? ?? {};
    final keyMeta = sshKeys['meta'] as List? ?? [];
    final snippets = data['snippets'] as Map<String, dynamic>? ?? {};
    final snippetItems = snippets['items'] as List? ?? [];
    final history = data['history'] as List? ?? [];
    final settings = data['settings'] as Map<String, dynamic>? ?? {};
    final actionBar = data['actionBar'] as Map<String, dynamic>? ?? {};

    return BackupSummary(
      connections: connections.length,
      keys: keyMeta.length,
      snippets: snippetItems.length,
      historyItems: history.length,
      hasSettings: settings.isNotEmpty,
      hasProfiles: (actionBar['customProfiles'] as List? ?? []).isNotEmpty,
    );
  }

  static void validate(Map<String, dynamic> backup) {
    if (backup['format'] != 'termipod-backup') {
      throw const FormatException('Not a TermiPod backup file');
    }
    final version = backup['version'];
    if (version is! int || version > 1) {
      throw FormatException(
        'Unsupported backup version: $version (this app supports version 1)',
      );
    }
    if (backup['data'] is! Map<String, dynamic>) {
      throw const FormatException('Missing data section in backup file');
    }
  }

  Future<ImportResult> importData(
    Map<String, dynamic> backup, {
    Set<ImportCategory> categories = const {
      ImportCategory.connections,
      ImportCategory.sshKeys,
      ImportCategory.snippets,
      ImportCategory.history,
      ImportCategory.actionBar,
      ImportCategory.settings,
    },
  }) async {
    validate(backup);
    final data = backup['data'] as Map<String, dynamic>;
    final prefs = await SharedPreferences.getInstance();

    var result = const ImportResult();

    if (categories.contains(ImportCategory.connections)) {
      result = await _importConnections(data, prefs, result);
    }
    if (categories.contains(ImportCategory.sshKeys)) {
      result = await _importSshKeys(data, result);
    }
    if (categories.contains(ImportCategory.snippets)) {
      result = _importSnippets(data, prefs, result);
    }
    if (categories.contains(ImportCategory.history)) {
      result = _importHistory(data, prefs, result);
    }
    if (categories.contains(ImportCategory.actionBar)) {
      result = _importActionBar(data, prefs, result);
    }
    if (categories.contains(ImportCategory.settings)) {
      result = _importSettings(data, prefs, result);
    }

    return result;
  }

  Future<ImportResult> _importConnections(
    Map<String, dynamic> data,
    SharedPreferences prefs,
    ImportResult result,
  ) async {
    final incoming = (data['connections'] as List? ?? [])
        .cast<Map<String, dynamic>>();
    if (incoming.isEmpty) return result;

    final raw = prefs.getString('connections');
    final existing = raw != null
        ? (jsonDecode(raw) as List).cast<Map<String, dynamic>>()
        : <Map<String, dynamic>>[];

    final existingIds = existing.map((c) => c['id'] as String).toSet();

    var added = 0;
    var skipped = 0;
    var passwordsAdded = 0;

    for (final conn in incoming) {
      final id = conn['id'] as String;
      if (existingIds.contains(id)) {
        skipped++;
        continue;
      }
      existing.add(conn);
      added++;
    }

    await prefs.setString('connections', jsonEncode(existing));

    // Import passwords for new connections
    final passwords = (data['passwords'] as Map<String, dynamic>? ?? {})
        .cast<String, String>();
    for (final entry in passwords.entries) {
      final existingPw = await _secureStorage.getPassword(entry.key);
      if (existingPw == null) {
        await _secureStorage.savePassword(entry.key, entry.value);
        passwordsAdded++;
      }
    }

    return ImportResult(
      connectionsAdded: added,
      connectionsSkipped: skipped,
      keysAdded: result.keysAdded,
      keysSkipped: result.keysSkipped,
      snippetsAdded: result.snippetsAdded,
      passwordsAdded: passwordsAdded,
      historyMerged: result.historyMerged,
      settingsImported: result.settingsImported,
      profilesImported: result.profilesImported,
    );
  }

  Future<ImportResult> _importSshKeys(
    Map<String, dynamic> data,
    ImportResult result,
  ) async {
    final sshKeys = data['sshKeys'] as Map<String, dynamic>? ?? {};
    final incomingMeta = (sshKeys['meta'] as List? ?? [])
        .cast<Map<String, dynamic>>();
    if (incomingMeta.isEmpty) return result;

    final prefs = await SharedPreferences.getInstance();
    final raw = prefs.getString('ssh_keys_meta');
    final existing = raw != null
        ? (jsonDecode(raw) as List).cast<Map<String, dynamic>>()
        : <Map<String, dynamic>>[];

    final existingIds = existing.map((k) => k['id'] as String).toSet();
    final privateKeys = (sshKeys['privateKeys'] as Map<String, dynamic>? ?? {})
        .cast<String, String>();
    final passphrases = (sshKeys['passphrases'] as Map<String, dynamic>? ?? {})
        .cast<String, String>();

    var added = 0;
    var skipped = 0;

    for (final meta in incomingMeta) {
      final id = meta['id'] as String;
      if (existingIds.contains(id)) {
        skipped++;
        continue;
      }
      existing.add(meta);
      added++;

      if (privateKeys.containsKey(id)) {
        await _secureStorage.savePrivateKey(id, privateKeys[id]!);
      }
      if (passphrases.containsKey(id)) {
        await _secureStorage.savePassphrase(id, passphrases[id]!);
      }
    }

    await prefs.setString('ssh_keys_meta', jsonEncode(existing));

    return ImportResult(
      connectionsAdded: result.connectionsAdded,
      connectionsSkipped: result.connectionsSkipped,
      keysAdded: added,
      keysSkipped: skipped,
      snippetsAdded: result.snippetsAdded,
      passwordsAdded: result.passwordsAdded,
      historyMerged: result.historyMerged,
      settingsImported: result.settingsImported,
      profilesImported: result.profilesImported,
    );
  }

  ImportResult _importSnippets(
    Map<String, dynamic> data,
    SharedPreferences prefs,
    ImportResult result,
  ) {
    final snippetsData = data['snippets'] as Map<String, dynamic>? ?? {};
    final incoming = (snippetsData['items'] as List? ?? [])
        .cast<Map<String, dynamic>>();

    final raw = prefs.getString('snippets');
    final existing = raw != null
        ? (jsonDecode(raw) as List).cast<Map<String, dynamic>>()
        : <Map<String, dynamic>>[];

    final existingIds = existing.map((s) => s['id'] as String).toSet();

    var added = 0;
    for (final snippet in incoming) {
      final id = snippet['id'] as String;
      if (existingIds.contains(id)) continue;
      existing.add(snippet);
      added++;
    }

    prefs.setString('snippets', jsonEncode(existing));

    // Merge preset overrides
    final incomingOverrides = snippetsData['presetOverrides'] as Map<String, dynamic>? ?? {};
    if (incomingOverrides.isNotEmpty) {
      final existingRaw = prefs.getString('snippet_preset_overrides');
      final existingOverrides = existingRaw != null
          ? jsonDecode(existingRaw) as Map<String, dynamic>
          : <String, dynamic>{};
      for (final entry in incomingOverrides.entries) {
        existingOverrides.putIfAbsent(entry.key, () => entry.value);
      }
      prefs.setString('snippet_preset_overrides', jsonEncode(existingOverrides));
    }

    // Merge deleted preset IDs
    final incomingDeleted = (snippetsData['deletedPresets'] as List? ?? []).cast<String>();
    if (incomingDeleted.isNotEmpty) {
      final existingRaw = prefs.getString('snippet_deleted_presets');
      final existingDeleted = existingRaw != null
          ? (jsonDecode(existingRaw) as List).cast<String>().toSet()
          : <String>{};
      existingDeleted.addAll(incomingDeleted);
      prefs.setString('snippet_deleted_presets', jsonEncode(existingDeleted.toList()));
    }

    return ImportResult(
      connectionsAdded: result.connectionsAdded,
      connectionsSkipped: result.connectionsSkipped,
      keysAdded: result.keysAdded,
      keysSkipped: result.keysSkipped,
      snippetsAdded: added,
      passwordsAdded: result.passwordsAdded,
      historyMerged: result.historyMerged,
      settingsImported: result.settingsImported,
      profilesImported: result.profilesImported,
    );
  }

  ImportResult _importHistory(
    Map<String, dynamic> data,
    SharedPreferences prefs,
    ImportResult result,
  ) {
    final incoming = (data['history'] as List? ?? []).cast<String>();
    if (incoming.isEmpty) return result;

    final raw = prefs.getString('settings_action_bar_command_history');
    final existing = raw != null
        ? (jsonDecode(raw) as List).cast<String>()
        : <String>[];

    final existingSet = existing.toSet();
    final newItems = incoming.where((cmd) => !existingSet.contains(cmd)).toList();

    final merged = [...existing, ...newItems];
    if (merged.length > 200) merged.removeRange(200, merged.length);

    prefs.setString('settings_action_bar_command_history', jsonEncode(merged));

    return ImportResult(
      connectionsAdded: result.connectionsAdded,
      connectionsSkipped: result.connectionsSkipped,
      keysAdded: result.keysAdded,
      keysSkipped: result.keysSkipped,
      snippetsAdded: result.snippetsAdded,
      passwordsAdded: result.passwordsAdded,
      historyMerged: newItems.length,
      settingsImported: result.settingsImported,
      profilesImported: result.profilesImported,
    );
  }

  ImportResult _importActionBar(
    Map<String, dynamic> data,
    SharedPreferences prefs,
    ImportResult result,
  ) {
    final ab = data['actionBar'] as Map<String, dynamic>? ?? {};
    if (ab.isEmpty) return result;

    // Active profile
    final activeProfile = ab['activeProfile'] as String?;
    if (activeProfile != null) {
      prefs.setString('settings_action_bar_active_profile', activeProfile);
    }

    // Custom profiles: merge by id
    final incomingProfiles = (ab['customProfiles'] as List? ?? [])
        .cast<Map<String, dynamic>>();
    if (incomingProfiles.isNotEmpty) {
      final existingRaw = prefs.getString('settings_action_bar_custom_profiles');
      final existing = existingRaw != null
          ? (jsonDecode(existingRaw) as List).cast<Map<String, dynamic>>()
          : <Map<String, dynamic>>[];
      final existingIds = existing.map((p) => p['id'] as String).toSet();
      for (final profile in incomingProfiles) {
        if (!existingIds.contains(profile['id'])) {
          existing.add(profile);
        }
      }
      prefs.setString('settings_action_bar_custom_profiles', jsonEncode(existing));
    }

    // Deleted presets: union
    final incomingDeleted = (ab['deletedPresets'] as List? ?? []).cast<String>();
    if (incomingDeleted.isNotEmpty) {
      final existingRaw = prefs.getString('settings_action_bar_deleted_presets');
      final existing = existingRaw != null
          ? (jsonDecode(existingRaw) as List).cast<String>().toSet()
          : <String>{};
      existing.addAll(incomingDeleted);
      prefs.setString('settings_action_bar_deleted_presets', jsonEncode(existing.toList()));
    }

    // Compose mode
    if (ab.containsKey('composeMode')) {
      prefs.setBool('settings_action_bar_compose_mode', ab['composeMode'] as bool);
    }

    // Panel profiles: merge (keep existing on conflict)
    final incomingPanels = (ab['panelProfiles'] as Map<String, dynamic>? ?? {})
        .cast<String, String>();
    if (incomingPanels.isNotEmpty) {
      final existingRaw = prefs.getString('settings_action_bar_panel_profiles');
      final existing = existingRaw != null
          ? (jsonDecode(existingRaw) as Map<String, dynamic>).cast<String, String>()
          : <String, String>{};
      for (final entry in incomingPanels.entries) {
        existing.putIfAbsent(entry.key, () => entry.value);
      }
      prefs.setString('settings_action_bar_panel_profiles', jsonEncode(existing));
    }

    return ImportResult(
      connectionsAdded: result.connectionsAdded,
      connectionsSkipped: result.connectionsSkipped,
      keysAdded: result.keysAdded,
      keysSkipped: result.keysSkipped,
      snippetsAdded: result.snippetsAdded,
      passwordsAdded: result.passwordsAdded,
      historyMerged: result.historyMerged,
      settingsImported: result.settingsImported,
      profilesImported: true,
    );
  }

  ImportResult _importSettings(
    Map<String, dynamic> data,
    SharedPreferences prefs,
    ImportResult result,
  ) {
    final settings = data['settings'] as Map<String, dynamic>? ?? {};
    if (settings.isEmpty) return result;

    for (final entry in settings.entries) {
      if (!_settingsKeys.contains(entry.key)) continue;
      final value = entry.value;
      if (value is bool) {
        prefs.setBool(entry.key, value);
      } else if (value is int) {
        prefs.setInt(entry.key, value);
      } else if (value is double) {
        prefs.setDouble(entry.key, value);
      } else if (value is String) {
        prefs.setString(entry.key, value);
      }
    }

    return ImportResult(
      connectionsAdded: result.connectionsAdded,
      connectionsSkipped: result.connectionsSkipped,
      keysAdded: result.keysAdded,
      keysSkipped: result.keysSkipped,
      snippetsAdded: result.snippetsAdded,
      passwordsAdded: result.passwordsAdded,
      historyMerged: result.historyMerged,
      settingsImported: true,
      profilesImported: result.profilesImported,
    );
  }

  // -----------------------------------------------------------------------
  // Settings keys whitelist
  // -----------------------------------------------------------------------

  static const _settingsKeys = <String>{
    'settings_dark_mode',
    'settings_font_size',
    'settings_font_family',
    'settings_biometric_auth',
    'settings_notifications',
    'settings_vibration',
    'settings_keep_screen_on',
    'settings_scrollback',
    'settings_min_font_size',
    'settings_adjust_mode',
    'settings_direct_input_enabled',
    'settings_use_custom_keyboard',
    'settings_show_terminal_cursor',
    'settings_invert_pane_nav',
    'settings_file_remote_path',
    'settings_file_path_format',
    'settings_file_auto_enter',
    'settings_file_bracketed_paste',
    'settings_image_remote_path',
    'settings_image_output_format',
    'settings_image_jpeg_quality',
    'settings_image_resize_preset',
    'settings_image_max_width',
    'settings_image_max_height',
    'settings_image_path_format',
    'settings_image_auto_enter',
    'settings_image_bracketed_paste',
    'settings_locale',
    'settings_nav_pad_mode',
    'settings_nav_pad_dpad_style',
    'settings_nav_pad_repeat_rate',
    'settings_nav_pad_haptic',
    'settings_nav_pad_buttons',
    'settings_file_download_path',
    'settings_floating_pad_enabled',
    'settings_floating_pad_size',
    'settings_floating_pad_center_key',
  };
}
