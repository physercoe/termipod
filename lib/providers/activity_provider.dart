import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'hub_provider.dart';

/// Recent audit events for the Activity digest.
///
/// Fetches up to 200 rows from `/v1/teams/{team}/audit` newest-first.
/// Both the Activity tab (firehose list) and the Me tab (digest mirror
/// under "Since you were last here") consume this provider per
/// `docs/ia-redesign.md` §6.3.
final recentAuditProvider =
    FutureProvider.autoDispose<List<Map<String, dynamic>>>((ref) async {
  final client = ref.watch(hubProvider.notifier).client;
  if (client == null) return const [];
  return client.listAuditEvents(limit: 200);
});
