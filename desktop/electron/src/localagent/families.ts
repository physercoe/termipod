/// The engine family registry, as the local agent service reads it
/// (vision-parity L3).
///
/// This is a TypeScript view over `resources/agent_families.generated.json`,
/// which is marshalled from `hub/internal/agentfamilies/agent_families.yaml` by
/// a Go test that fails when the two disagree
/// (`agentfamilies/artifact_test.go`). So the launch argv the Companion drives
/// claude with, and the frame profile it translates the replies through, are
/// the *same data* the hub uses — which is what makes a hub-less session
/// produce the same transcript as a hub-driven one (plan D-7).
///
/// Only the fields the local service consumes are typed. That is not laziness:
/// an interface listing every field would be a second schema to keep in step
/// with the Go structs, and the generated file carries the rest regardless —
/// unread, not lost.
///
/// Pure: parsing only, no `fs` and no `electron`. Where the file lives on disk
/// is a packaging question, and it is answered in `host.ts` with the same
/// `app.isPackaged` two-brancher the stdio relay uses.

import type { FrameProfile } from '../frameprofile/types.ts';

/// One driving mode's launch contract (ADR-043).
export interface LaunchMode {
  mode_args?: string[];
}

/// One engine family. Field names are the `json:` tags on
/// `agentfamilies.Family`.
export interface Family {
  family: string;
  bin: string;
  supports?: string[];
  launch?: Record<string, LaunchMode>;
  /// Flag-time permission argv per mode. Values may embed `{{mcp_namespace}}`,
  /// which only a hub can resolve — see `permissionFlag` in claudewire.ts for
  /// what the local service does about that.
  permission_modes?: Record<string, string>;
  frame_profile?: FrameProfile;
  /// Per-mode multimodal input capability (ADR-021 D5). The Composer's gate
  /// (F3) reads these from the hub; a local source has no hub to ask, so it
  /// reads them from here.
  prompt_image?: Record<string, boolean>;
  prompt_pdf?: Record<string, boolean>;
}

/// Parse the generated registry. Throws on anything that isn't a JSON array of
/// objects with a `family` — a corrupt artifact should fail loudly at startup,
/// not produce a service that silently knows about no engines.
export function parseFamilies(json: string): Family[] {
  let raw: unknown;
  try {
    raw = JSON.parse(json);
  } catch (err) {
    throw new Error(`agent_families.generated.json is not valid JSON: ${String(err)}`);
  }
  if (!Array.isArray(raw)) {
    throw new Error('agent_families.generated.json must be an array of families');
  }
  const out: Family[] = [];
  for (const entry of raw) {
    if (entry === null || typeof entry !== 'object' || Array.isArray(entry)) continue;
    const fam = entry as Record<string, unknown>;
    if (typeof fam.family !== 'string' || fam.family === '') continue;
    out.push(fam as unknown as Family);
  }
  if (out.length === 0) {
    throw new Error('agent_families.generated.json contained no usable families');
  }
  return out;
}

export function familyByName(families: readonly Family[], name: string): Family | undefined {
  return families.find((f) => f.family === name);
}

/// The engines the local service has a driver for.
///
/// A closed union rather than a string: every branch that builds a driver has
/// to handle each member, so adding an engine is a compile error at each site
/// instead of a silent fallthrough to claude's.
export type LocalEngine = 'claude-code' | 'codex';

/// Which local driver a family gets, or null when it gets none.
///
/// A family qualifies when we have a driver for it AND it declares the mode
/// that driver speaks — the second half matters because the mode set is data,
/// and a family that drops M2 should stop being offered without anyone editing
/// this file.
///
/// Both drivers are M2, and both read `launch.M2.mode_args` from the same
/// registry, but they use it differently: claude's IS its argv, while codex's
/// (`app-server --listen stdio://`) states the launch contract the desktop's
/// spawn rung honours through `codexattach.ts`, which additionally has to
/// FIND the binary (the installer's PATH line lives in `.bashrc`, which a
/// GUI-launched app never sources).
export function localEngine(fam: Family | undefined): LocalEngine | null {
  if (fam === undefined) return null;
  if (fam.family !== 'claude-code' && fam.family !== 'codex') return null;
  const usable = (fam.supports ?? []).includes('M2') && (fam.launch?.M2?.mode_args ?? []).length > 0;
  return usable ? fam.family : null;
}

/// Whether a family can be driven by the local service.
export function supportsLocalDriving(fam: Family | undefined): boolean {
  return localEngine(fam) !== null;
}
