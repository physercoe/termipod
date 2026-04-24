/// Mock data for screenshot generation and widget tests.
///
/// Provides realistic sample data for all providers so screens
/// can be rendered without network or storage access.
library;

import 'package:termipod/providers/active_session_provider.dart';
import 'package:termipod/providers/connection_provider.dart';
import 'package:termipod/providers/input_history_provider.dart';
import 'package:termipod/providers/key_provider.dart';
import 'package:termipod/providers/snippet_provider.dart';

// ---------------------------------------------------------------------------
// Connections
// ---------------------------------------------------------------------------

final mockConnections = [
  Connection(
    id: 'conn-1',
    name: 'prod-api-1',
    host: '10.0.1.42',
    port: 22,
    username: 'deploy',
    authMethod: 'key',
    keyId: 'key-1',
    createdAt: DateTime(2025, 6, 1),
    lastConnectedAt: DateTime(2026, 4, 14, 9, 30),
    deepLinkId: 'prod-api',
  ),
  Connection(
    id: 'conn-2',
    name: 'dev-gpu-box',
    host: '192.168.1.100',
    port: 22,
    username: 'alice',
    authMethod: 'key',
    keyId: 'key-2',
    createdAt: DateTime(2025, 8, 15),
    lastConnectedAt: DateTime(2026, 4, 14, 8, 45),
    jumpHost: 'bastion.example.com',
    jumpPort: 22,
    jumpUsername: 'alice',
    jumpAuthMethod: 'key',
    jumpKeyId: 'key-2',
  ),
  Connection(
    id: 'conn-3',
    name: 'homelab-nas',
    host: '10.10.0.5',
    port: 2222,
    username: 'admin',
    authMethod: 'password',
    createdAt: DateTime(2025, 11, 20),
    lastConnectedAt: DateTime(2026, 4, 13, 22, 10),
    proxyHost: '127.0.0.1',
    proxyPort: 1080,
  ),
  Connection(
    id: 'conn-4',
    name: 'ci-runner',
    host: 'ci.internal.dev',
    port: 22,
    username: 'runner',
    authMethod: 'key',
    keyId: 'key-1',
    terminalMode: 'raw',
    createdAt: DateTime(2026, 1, 5),
    lastConnectedAt: DateTime(2026, 4, 12, 14, 0),
  ),
];

final mockConnectionsState = ConnectionsState(
  connections: mockConnections,
);

// ---------------------------------------------------------------------------
// Active sessions
// ---------------------------------------------------------------------------

final mockActiveSessions = ActiveSessionsState(
  sessions: [
    ActiveSession(
      connectionId: 'conn-1',
      connectionName: 'prod-api-1',
      host: '10.0.1.42',
      sessionName: 'deploy',
      windowCount: 3,
      connectedAt: DateTime(2026, 4, 14, 9, 30),
      isAttached: true,
      lastWindowIndex: 0,
      lastAccessedAt: DateTime(2026, 4, 14, 10, 15),
    ),
    ActiveSession(
      connectionId: 'conn-2',
      connectionName: 'dev-gpu-box',
      host: '192.168.1.100',
      sessionName: 'claude',
      windowCount: 2,
      connectedAt: DateTime(2026, 4, 14, 8, 45),
      isAttached: true,
      lastWindowIndex: 1,
      lastAccessedAt: DateTime(2026, 4, 14, 10, 12),
    ),
    ActiveSession(
      connectionId: 'conn-2',
      connectionName: 'dev-gpu-box',
      host: '192.168.1.100',
      sessionName: 'codex',
      windowCount: 1,
      connectedAt: DateTime(2026, 4, 14, 8, 50),
      isAttached: false,
      lastAccessedAt: DateTime(2026, 4, 14, 9, 30),
    ),
    ActiveSession(
      connectionId: 'conn-3',
      connectionName: 'homelab-nas',
      host: '10.10.0.5',
      sessionName: 'main',
      windowCount: 4,
      connectedAt: DateTime(2026, 4, 13, 22, 10),
      isAttached: true,
      lastWindowIndex: 2,
      lastAccessedAt: DateTime(2026, 4, 13, 23, 45),
    ),
  ],
);

// ---------------------------------------------------------------------------
// SSH keys
// ---------------------------------------------------------------------------

final mockKeysState = KeysState(
  keys: [
    SshKeyMeta(
      id: 'key-1',
      name: 'main-ed25519',
      type: 'ed25519',
      publicKey: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBk2...truncated alice@dev',
      fingerprint: 'SHA256:xR3j8K9pLm2Nt5Qv7Fw1Yz4Ab6Cd8Ef0Gh2Ij4Kl6M',
      createdAt: DateTime(2025, 5, 10),
      comment: 'Primary key for all servers',
      source: KeySource.generated,
    ),
    SshKeyMeta(
      id: 'key-2',
      name: 'gpu-rsa-4096',
      type: 'rsa-4096',
      publicKey: 'ssh-rsa AAAAB3NzaC1yc2EAAAA...truncated alice@gpu',
      fingerprint: 'SHA256:mN3oP5qR7sT9uV1wX3yZ5aB7cD9eF1gH3iJ5kL7mN',
      hasPassphrase: true,
      createdAt: DateTime(2025, 9, 1),
      comment: 'GPU box access with passphrase',
      source: KeySource.generated,
    ),
    SshKeyMeta(
      id: 'key-3',
      name: 'imported-work',
      type: 'ed25519',
      publicKey: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA...truncated work@corp',
      fingerprint: 'SHA256:pQ1rS3tU5vW7xY9zA1bC3dE5fG7hI9jK1lM3nO5pQ',
      createdAt: DateTime(2026, 2, 14),
      comment: 'Imported from work laptop',
      source: KeySource.imported,
    ),
  ],
);

// ---------------------------------------------------------------------------
// Snippets
// ---------------------------------------------------------------------------

final mockSnippetsState = SnippetsState(
  snippets: [
    const Snippet(
      id: 'snip-1',
      name: 'Docker logs',
      content: 'docker logs --tail {{lines}} {{container}}',
      category: 'general',
      variables: [
        SnippetVariable(name: 'lines', defaultValue: '100', hint: 'Number of lines'),
        SnippetVariable(name: 'container', hint: 'Container name or ID'),
      ],
    ),
    const Snippet(
      id: 'snip-2',
      name: 'Git pull + rebase',
      content: 'git pull --rebase origin {{branch}}',
      category: 'general',
      variables: [
        SnippetVariable(name: 'branch', defaultValue: 'main'),
      ],
    ),
    const Snippet(
      id: 'snip-3',
      name: 'Restart service',
      content: 'sudo systemctl restart {{service}}',
      category: 'general',
      variables: [
        SnippetVariable(name: 'service', hint: 'Service name'),
      ],
      sendImmediately: true,
    ),
    const Snippet(
      id: 'snip-4',
      name: 'Tail syslog',
      content: 'tail -f /var/log/syslog | grep -i {{pattern}}',
      category: 'general',
      variables: [
        SnippetVariable(name: 'pattern', defaultValue: 'error', hint: 'Filter pattern'),
      ],
    ),
  ],
);

// ---------------------------------------------------------------------------
// Command history
// ---------------------------------------------------------------------------

const mockInputHistoryState = InputHistoryState(
  items: [
    'docker ps --format "table {{.Names}}\t{{.Status}}"',
    'kubectl get pods -n production',
    'tail -f /var/log/nginx/access.log',
    'htop',
    'git log --oneline -20',
    'systemctl status nginx',
    'df -h',
    'free -m',
    'uptime',
    'ls -la /opt/deploy/',
    'cat /etc/nginx/sites-enabled/api.conf',
    'journalctl -u myapp --since "1 hour ago"',
  ],
);

