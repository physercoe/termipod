import React, { useEffect, useState } from 'react';
import { Box, Text, useInput } from 'ink';
import SelectInput from 'ink-select-input';
import Spinner from 'ink-spinner';

import type { HubClient } from '../client.js';
import { streamEvents, type SseEvent } from '../sse.js';

interface Props {
  client: HubClient;
  onExit: () => void;
}

interface Project {
  id: string;
  name: string;
}
interface Channel {
  id: string;
  name: string;
}

type Phase =
  | { kind: 'pickProject'; projects: Project[] | null }
  | { kind: 'pickChannel'; projectId: string; channels: Channel[] | null }
  | { kind: 'stream'; projectId: string; channelId: string };

/**
 * Live event feed. Walks the user through project → channel selection, then
 * opens an SSE subscription. `q` always backs up a step.
 */
export function Feed({ client, onExit }: Props) {
  const [phase, setPhase] = useState<Phase>({ kind: 'pickProject', projects: null });
  const [events, setEvents] = useState<SseEvent[]>([]);
  const [error, setError] = useState<string | null>(null);

  // Phase bootstrapping — each transition fires a load.
  useEffect(() => {
    if (phase.kind === 'pickProject' && phase.projects === null) {
      void client.listProjects().then(
        (ps) =>
          setPhase({
            kind: 'pickProject',
            projects: ps as unknown as Project[],
          }),
        (e) => setError(String(e)),
      );
    }
    if (phase.kind === 'pickChannel' && phase.channels === null) {
      void client.listChannels(phase.projectId).then(
        (cs) =>
          setPhase({
            kind: 'pickChannel',
            projectId: phase.projectId,
            channels: cs as unknown as Channel[],
          }),
        (e) => setError(String(e)),
      );
    }
  }, [phase, client]);

  // SSE lifecycle. Re-runs if the target channel changes.
  useEffect(() => {
    if (phase.kind !== 'stream') return;
    const ctrl = new AbortController();
    setEvents([]);
    (async () => {
      try {
        for await (const evt of streamEvents(client, phase.projectId, phase.channelId, {
          signal: ctrl.signal,
        })) {
          // Prepend + cap at 200 to keep the render list cheap on slow TTYs.
          setEvents((prev) => [evt, ...prev].slice(0, 200));
        }
      } catch (e) {
        if (!ctrl.signal.aborted) setError(String(e));
      }
    })();
    return () => ctrl.abort();
  }, [phase, client]);

  useInput((input, key) => {
    if (input === 'q' || key.escape) {
      if (phase.kind === 'stream') {
        setPhase({ kind: 'pickChannel', projectId: phase.projectId, channels: null });
      } else if (phase.kind === 'pickChannel') {
        setPhase({ kind: 'pickProject', projects: null });
      } else {
        onExit();
      }
    }
  });

  if (error) {
    return (
      <Box flexDirection="column">
        <Text color="red">{error}</Text>
        <Text dimColor>q to go back</Text>
      </Box>
    );
  }

  if (phase.kind === 'pickProject') {
    if (!phase.projects)
      return (
        <Text>
          <Spinner type="dots" /> loading projects…
        </Text>
      );
    if (phase.projects.length === 0)
      return <Text dimColor>No projects. Create one with hub-cli and retry.</Text>;
    return (
      <Box flexDirection="column">
        <Text bold>Pick a project</Text>
        <SelectInput
          items={phase.projects.map((p) => ({ label: p.name, value: p.id }))}
          onSelect={(item) =>
            setPhase({ kind: 'pickChannel', projectId: item.value, channels: null })
          }
        />
      </Box>
    );
  }

  if (phase.kind === 'pickChannel') {
    if (!phase.channels)
      return (
        <Text>
          <Spinner type="dots" /> loading channels…
        </Text>
      );
    if (phase.channels.length === 0)
      return (
        <Box flexDirection="column">
          <Text dimColor>No channels in this project.</Text>
          <Text dimColor>q to back out</Text>
        </Box>
      );
    return (
      <Box flexDirection="column">
        <Text bold>Pick a channel</Text>
        <SelectInput
          items={phase.channels.map((c) => ({ label: c.name, value: c.id }))}
          onSelect={(item) =>
            setPhase({
              kind: 'stream',
              projectId: phase.projectId,
              channelId: item.value,
            })
          }
        />
      </Box>
    );
  }

  // phase.kind === 'stream'
  return (
    <Box flexDirection="column">
      <Box marginBottom={1}>
        <Text bold>Feed · </Text>
        <Text dimColor>channel={phase.channelId.slice(0, 8)} · q to unsubscribe</Text>
      </Box>
      {events.length === 0 ? (
        <Text>
          <Spinner type="dots" /> waiting for events…
        </Text>
      ) : (
        events.slice(0, 20).map((evt, i) => <FeedLine key={lineKey(evt, i)} evt={evt} />)
      )}
    </Box>
  );
}

function lineKey(evt: SseEvent, i: number): string {
  const id = evt.id;
  return typeof id === 'string' ? id : String(i);
}

function FeedLine({ evt }: { evt: SseEvent }) {
  const type = (evt.type as string) ?? 'message';
  const from = (evt.from_id as string) ?? '';
  const ts = (evt.ts as string) ?? '';
  const preview = previewFromParts((evt.parts as unknown[]) ?? []);
  return (
    <Box flexDirection="column" marginBottom={1}>
      <Text>
        <Text color="cyan">[{type}]</Text> <Text dimColor>{ts}</Text>{' '}
        {from && <Text color="yellow">{from}</Text>}
      </Text>
      {preview && <Text>  {preview}</Text>}
    </Box>
  );
}

function previewFromParts(parts: unknown[]): string {
  for (const raw of parts) {
    if (!raw || typeof raw !== 'object') continue;
    const p = raw as Record<string, unknown>;
    if (p.kind === 'text' && typeof p.text === 'string') {
      const t = p.text.trim();
      if (t) return t;
    } else if (p.kind === 'excerpt' && p.excerpt && typeof p.excerpt === 'object') {
      const ex = p.excerpt as Record<string, unknown>;
      const content = typeof ex.content === 'string' ? ex.content : '';
      const first = content.split('\n').find((l) => l.trim().length > 0) ?? '';
      const range =
        typeof ex.line_from === 'number' && typeof ex.line_to === 'number'
          ? ` L${ex.line_from}-${ex.line_to}`
          : '';
      return `[excerpt${range}] ${first.trim()}`.trim();
    } else if (p.kind === 'file') {
      const f = p.file as Record<string, unknown> | undefined;
      return `[file] ${f?.uri ?? ''}`;
    } else if (p.kind === 'image') {
      return '[image]';
    }
  }
  return '';
}
