import { existsSync, mkdirSync, readFileSync, writeFileSync, chmodSync } from 'node:fs';
import { homedir } from 'node:os';
import { dirname, join } from 'node:path';

/**
 * Hub connection settings. Mirrors the mobile app's HubConfig so the two
 * clients can keep their endpoint names in sync.
 */
export interface HubConfig {
  baseUrl: string;
  teamId: string;
  token: string;
}

const DEFAULT_PATH = join(homedir(), '.config', 'termipod', 'hub-tui.json');

/**
 * Load from (in order): CLI flags already parsed into argv, env vars, then
 * the on-disk config. Missing values come back empty — callers decide
 * whether to prompt.
 */
export function loadConfig(argv: {
  url?: string;
  team?: string;
  token?: string;
}): HubConfig {
  const disk = readDisk();
  return {
    baseUrl: argv.url ?? process.env.HUB_URL ?? disk.baseUrl ?? '',
    teamId: argv.team ?? process.env.HUB_TEAM ?? disk.teamId ?? 'team',
    token: argv.token ?? process.env.HUB_TOKEN ?? disk.token ?? '',
  };
}

export function saveConfig(cfg: HubConfig, path = DEFAULT_PATH): void {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, JSON.stringify(cfg, null, 2), 'utf8');
  // Token is in plain text — lock the file down so at least the rest of
  // the machine can't read it.
  try {
    chmodSync(path, 0o600);
  } catch {
    // Windows or an unusual FS — fall through; we tried.
  }
}

export function isValid(c: HubConfig): boolean {
  return c.baseUrl.length > 0 && c.teamId.length > 0 && c.token.length > 0;
}

function readDisk(path = DEFAULT_PATH): Partial<HubConfig> {
  if (!existsSync(path)) return {};
  try {
    const raw = readFileSync(path, 'utf8');
    const parsed = JSON.parse(raw);
    return {
      baseUrl: typeof parsed.baseUrl === 'string' ? parsed.baseUrl : undefined,
      teamId: typeof parsed.teamId === 'string' ? parsed.teamId : undefined,
      token: typeof parsed.token === 'string' ? parsed.token : undefined,
    };
  } catch {
    return {};
  }
}
