/// IPC surface for the local agent service (vision-parity L3a).
///
/// The only file in `localagent/` that imports Electron. Everything it exposes
/// is a thin, validating shell over `service.ts` — which is what lets the
/// service itself be driven by `node --test` without a browser or a window.
///
/// Registered into `ipc/dispatch`, where the handler map IS the allowlist: a
/// command with no handler is refused in main, so nothing here is reachable
/// from a renderer that does not name it.

import { app, type WebContents } from 'electron';
import { readFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import type { Handler } from '../ipc/dispatch';
import { emit } from '../events';
import { parseFamilies, type Family } from './families.ts';
import {
  optionalString,
  readInputPayload,
  readPosture,
  readTail,
  requireCursor,
  requireInputKind,
  requireString,
} from './hostargs.ts';
import { LocalAgentService } from './service.ts';

/// Where the generated family registry sits. Packaged: an electron-builder
/// extraResource beside dist/; dev: the checked-in file under
/// `desktop/electron/resources/`. Same two-brancher as `stdioBridgePath()`.
export function familiesArtifactPath(): string {
  return app.isPackaged
    ? path.join(process.resourcesPath, 'agent_families.generated.json')
    : path.join(__dirname, '..', 'resources', 'agent_families.generated.json');
}

let service: LocalAgentService | null = null;
const watchers = new Set<WebContents>();

/// The renderer event name. Payload: `{ session_id, event }`.
export const LOCAL_AGENT_EVENT = 'localagent-event';

function getService(): LocalAgentService {
  if (service !== null) return service;

  let families: Family[];
  try {
    families = parseFamilies(readFileSync(familiesArtifactPath(), 'utf-8'));
  } catch (err) {
    // Deliberately fatal for this feature rather than degraded: without the
    // registry there is no launch contract and no frame profile, so a session
    // created anyway would spawn an interactive child on a pipe and hang. The
    // dock asks `localagent_families` first and gets this error, which is a
    // sentence a person can act on.
    throw new Error(`local agent service unavailable: ${String(err)}`);
  }

  service = new LocalAgentService({
    families,
    env: process.env,
    homeDir: os.homedir(),
  });
  service.subscribe((sessionId, event) => {
    for (const wc of [...watchers]) {
      if (wc.isDestroyed()) {
        watchers.delete(wc);
        continue;
      }
      emit(wc, LOCAL_AGENT_EVENT, { session_id: sessionId, event });
    }
  });
  return service;
}

/// Stop every local session. Called from main's quit path beside the other
/// `disposeAll`s — an engine child outliving the window is a process nobody
/// can see and nobody will reap.
export function disposeLocalAgents(): void {
  service?.disposeAll();
  watchers.clear();
}

export const localAgentHandlers: Record<string, Handler> = {
  /// Which families this build can drive locally. The dock calls this first;
  /// an empty list is the honest "no local sessions available here".
  ///
  /// Projected rather than passed through: the renderer needs the family name
  /// and the `prompt_*` maps its composer gate keys on, and shipping the whole
  /// entry would put an 11-rule frame profile on the wire for a picker.
  localagent_families: () => ({
    families: getService().localFamilies().map((f) => ({
      family: f.family,
      prompt_image: f.prompt_image,
      prompt_pdf: f.prompt_pdf,
    })),
  }),

  localagent_list: () => ({ sessions: getService().list() }),

  // Posture absent stays absent so the SERVICE applies its own default —
  // resolving one here would put the safety decision in two places, and the
  // two would eventually disagree.
  localagent_create: (args) =>
    getService().create({
      family: optionalString(args, 'family'),
      cwd: requireString(args, 'cwd'),
      posture: readPosture(args),
      model: optionalString(args, 'model'),
      configHome: optionalString(args, 'config_home'),
    }),

  localagent_history: (args) => getService().history(requireString(args, 'session_id'), readTail(args)),

  localagent_since: (args) => getService().since(requireString(args, 'session_id'), requireCursor(args)),

  localagent_input: (args) => {
    getService().input(requireString(args, 'session_id'), requireInputKind(args), readInputPayload(args));
    return {};
  },

  localagent_stop: (args) => {
    getService().stop(requireString(args, 'session_id'));
    return {};
  },

  localagent_forget: (args) => ({ forgotten: getService().forget(requireString(args, 'session_id')) }),

  /// Start/stop delivering live events to the calling renderer. Watching is
  /// per-WebContents rather than per-session: the renderer already filters by
  /// `session_id`, and one channel means a dock switching agents does not race
  /// an unsubscribe against a subscribe.
  localagent_watch: (_args, ctx) => {
    getService();
    watchers.add(ctx.sender);
    ctx.sender.once('destroyed', () => watchers.delete(ctx.sender));
    return {};
  },

  localagent_unwatch: (_args, ctx) => {
    watchers.delete(ctx.sender);
    return {};
  },
};
