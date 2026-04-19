#!/usr/bin/env bun
import React, { useState } from 'react';
import { Box, Text, render, useApp, useInput } from 'ink';

import { HubClient } from './client.js';
import { isValid, loadConfig, saveConfig, type HubConfig } from './config.js';
import { Feed } from './views/Feed.js';
import { Pending } from './views/Pending.js';

type Tab = 'menu' | 'pending' | 'feed';

function parseArgv(argv: string[]): { url?: string; team?: string; token?: string } {
  const out: { url?: string; team?: string; token?: string } = {};
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--url') out.url = argv[++i];
    else if (a === '--team') out.team = argv[++i];
    else if (a === '--token') out.token = argv[++i];
  }
  return out;
}

function App({ cfg }: { cfg: HubConfig }) {
  const { exit } = useApp();
  const [tab, setTab] = useState<Tab>('menu');
  const client = new HubClient(cfg);

  useInput((input, key) => {
    if (tab !== 'menu') return; // sub-views handle their own keys
    if (input === 'q' || key.escape || (key.ctrl && input === 'c')) exit();
    else if (input === 'p') setTab('pending');
    else if (input === 'f') setTab('feed');
  });

  if (tab === 'pending') {
    return <Pending client={client} onExit={() => setTab('menu')} />;
  }
  if (tab === 'feed') {
    return <Feed client={client} onExit={() => setTab('menu')} />;
  }

  return (
    <Box flexDirection="column">
      <Text bold>Termipod Hub · TUI</Text>
      <Text dimColor>
        {cfg.baseUrl} · team={cfg.teamId}
      </Text>
      <Box marginTop={1} flexDirection="column">
        <Text>
          <Text color="cyan">p</Text> — Pending (attention items)
        </Text>
        <Text>
          <Text color="cyan">f</Text> — Feed (live event stream)
        </Text>
        <Text>
          <Text color="cyan">q</Text> — Quit
        </Text>
      </Box>
    </Box>
  );
}

function Bootstrap() {
  return (
    <Box flexDirection="column">
      <Text color="red" bold>
        hub-tui is not configured.
      </Text>
      <Text>Set any of:</Text>
      <Text>  --url / --team / --token (CLI flags)</Text>
      <Text>  HUB_URL / HUB_TEAM / HUB_TOKEN (env)</Text>
      <Text>  ~/.config/termipod/hub-tui.json</Text>
      <Text dimColor>
        Then re-run. (Interactive setup TODO — see the slice 5 backlog.)
      </Text>
    </Box>
  );
}

async function main() {
  const argv = parseArgv(process.argv.slice(2));
  const cfg = loadConfig(argv);
  if (!isValid(cfg)) {
    render(<Bootstrap />);
    return;
  }
  // Persist CLI/env config so the next run is zero-config.
  try {
    saveConfig(cfg);
  } catch {
    // Non-fatal; we still run with the config we got.
  }
  render(<App cfg={cfg} />);
}

void main();
