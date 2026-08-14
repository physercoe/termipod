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
import { parseResumeTable, type ResumeTable } from './resumerecipes.ts';
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

/// Where a generated registry sits. Packaged: an electron-builder
/// extraResource beside dist/; dev: the checked-in file under
/// `desktop/electron/resources/`. Same two-brancher as `stdioBridgePath()`.
function resourcePath(name: string): string {
  return app.isPackaged
    ? path.join(process.resourcesPath, name)
    : path.join(__dirname, '..', 'resources', name);
}

export function familiesArtifactPath(): string {
  return resourcePath('agent_families.generated.json');
}

/// The N1 resume-recipe table, read to rebind a session after a restart.
export function resumeRecipesArtifactPath(): string {
  return resourcePath('resume_recipes.generated.json');
}

let service: LocalAgentService | null = null;
const watchers = new Set<WebContents>();

/// The renderer event name. Payload: `{ session_id, event }`.
export const LOCAL_AGENT_EVENT = 'localagent-event';

function getService(): LocalAgentService {
  if (service !== null) return service;

  let families: Family[];
  let resumeTable: ResumeTable;
  try {
    families = parseFamilies(readFileSync(familiesArtifactPath(), 'utf-8'));
    // Loaded up front rather than at the moment of a rebind. A table that
    // failed to ship would otherwise surface as "your session would not
    // reattach", weeks later, on the one path nobody exercises by accident.
    resumeTable = parseResumeTable(readFileSync(resumeRecipesArtifactPath(), 'utf-8'));
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
    resumeTable,
    env: process.env,
    homeDir: os.homedir(),
    dataDir: app.getPath('userData'),
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
  // Read persisted sessions back before the first list() can be answered, so a
  // dock opened after a restart sees the transcripts rather than an empty
  // picker that fills in later.
  service.reload();
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

  /// Reattach a restored session's engine child without sending anything.
  ///
  /// `localagent_input` rebinds on its own, so this is not on the critical
  /// path — it exists for the case where the director wants the agent warm
  /// before typing, and so a surface can report a failed reattach at a moment
  /// it chose rather than in the middle of a message.
  localagent_rebind: (args) => getService().rebind(requireString(args, 'session_id')),

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
