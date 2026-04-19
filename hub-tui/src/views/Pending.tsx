import React, { useEffect, useState } from 'react';
import { Box, Text, useInput } from 'ink';
import Spinner from 'ink-spinner';

import type { HubClient } from '../client.js';

interface AttentionItem {
  id: string;
  kind: string;
  severity: string;
  summary: string;
  status: string;
  created_at: string;
}

interface Props {
  client: HubClient;
  /** Called when user presses `q` — parent pops the view. */
  onExit: () => void;
}

/**
 * Pending attention items. j/k to move, `a` / `r` to approve / reject,
 * `d` to resolve, `R` to refresh, `q` to go back.
 */
export function Pending({ client, onExit }: Props) {
  const [items, setItems] = useState<AttentionItem[]>([]);
  const [cursor, setCursor] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [flash, setFlash] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const raw = await client.listAttention('open');
      setItems(raw as unknown as AttentionItem[]);
      setCursor((c) => Math.min(c, Math.max(0, raw.length - 1)));
    } catch (e) {
      setError(String(e));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
  }, []);

  useInput((input, key) => {
    if (input === 'q' || key.escape) {
      onExit();
      return;
    }
    if (input === 'j' || key.downArrow) {
      setCursor((c) => Math.min(items.length - 1, c + 1));
      return;
    }
    if (input === 'k' || key.upArrow) {
      setCursor((c) => Math.max(0, c - 1));
      return;
    }
    if (input === 'R') {
      void load();
      return;
    }
    const it = items[cursor];
    if (!it) return;
    if (input === 'a') void act(() => client.decideAttention(it.id, 'approve', { by: '@tui' }), 'approved');
    else if (input === 'r') void act(() => client.decideAttention(it.id, 'reject', { by: '@tui' }), 'rejected');
    else if (input === 'd') void act(() => client.resolveAttention(it.id, { by: '@tui' }), 'resolved');
  });

  async function act(fn: () => Promise<unknown>, verb: string) {
    try {
      await fn();
      setFlash(verb);
      setTimeout(() => setFlash(null), 1200);
      await load();
    } catch (e) {
      setError(String(e));
    }
  }

  if (loading && items.length === 0) {
    return (
      <Box>
        <Text>
          <Spinner type="dots" /> loading attention…
        </Text>
      </Box>
    );
  }

  return (
    <Box flexDirection="column">
      <Box marginBottom={1}>
        <Text bold>Pending attention</Text>
        <Text dimColor>  j/k move · a approve · r reject · d resolve · R refresh · q back</Text>
      </Box>
      {error && (
        <Box marginBottom={1}>
          <Text color="red">{error}</Text>
        </Box>
      )}
      {flash && (
        <Box marginBottom={1}>
          <Text color="green">✓ {flash}</Text>
        </Box>
      )}
      {items.length === 0 ? (
        <Text dimColor>No open items.</Text>
      ) : (
        items.map((it, i) => (
          <Box key={it.id} flexDirection="column" marginBottom={1}>
            <Text
              color={i === cursor ? 'cyan' : undefined}
              inverse={i === cursor}
            >
              {severityGlyph(it.severity)} [{it.kind}] {it.summary}
            </Text>
            <Text dimColor>  id={shortId(it.id)} created={it.created_at}</Text>
          </Box>
        ))
      )}
    </Box>
  );
}

function severityGlyph(s: string): string {
  switch (s) {
    case 'critical':
      return '🛑';
    case 'major':
      return '⚠ ';
    default:
      return '· ';
  }
}

function shortId(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id;
}
