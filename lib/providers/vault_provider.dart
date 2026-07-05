import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../services/vault/vault_service.dart';

/// The zero-knowledge key-vault orchestrator (ADR-052 D-4). Stateless service;
/// the sync screen drives it and manages its own loading/error UI state.
final vaultServiceProvider = Provider<VaultService>((ref) => VaultService(ref));
