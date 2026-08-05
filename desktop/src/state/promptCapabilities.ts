import { obj, str, type Entity } from '../hub/types.ts';
import type { AttachKind } from '../ui/attach.ts';

/// F3 — what the agent on the other end can actually be handed.
///
/// The desktop composer has offered image / pdf / audio / video attach
/// unconditionally since it shipped. Mobile has consulted the family
/// registry's `prompt_*[mode]` flags since ADR-021 D5, and the difference is
/// not cosmetic: an engine that cannot take an image gets one anyway, spends a
/// round trip, and answers about a file it never saw. Absence of the
/// affordance is the honest state.
///
/// This is the desktop port of mobile's `_resolvePromptFlag`
/// (`lib/widgets/image_attach/composer_image_attach.dart:132`), joining the
/// engine family and the resolved driving mode against the registry.

export interface PromptCapabilities {
  image: boolean;
  pdf: boolean;
  audio: boolean;
  video: boolean;
}

/// Nothing attachable — the answer while the registry is still loading, and
/// for an engine it has never heard of.
export const NO_PROMPT_CAPABILITIES: PromptCapabilities = {
  image: false,
  pdf: false,
  audio: false,
  video: false,
};

const FLAG_KEY: Record<keyof PromptCapabilities, string> = {
  image: 'prompt_image',
  pdf: 'prompt_pdf',
  audio: 'prompt_audio',
  video: 'prompt_video',
};

/// The driving mode an agent record reports. The hub serializes the RESOLVED
/// mode as `mode`; older payloads carry `driving_mode`. Both are accepted and
/// `mode` wins, matching mobile (`agent_compose.dart:156`).
///
/// M4 is the default when neither is present, because M4 is the rung an
/// unresolved agent falls back to — and it is the rung where every modality
/// flag is false, so an unknown mode grants nothing.
export function drivingModeOf(agent: Entity | undefined): string {
  if (agent === undefined) return 'M4';
  const mode = str(agent, 'mode') ?? str(agent, 'driving_mode');
  return mode !== undefined && mode !== '' ? mode : 'M4';
}

/// Resolve all four flags for one engine + mode against the registry list
/// (`GET /agent-families`). A family the registry doesn't list, a flag it
/// doesn't declare, and a flag declared false are all the same answer: no.
///
/// Note this is keyed on the engine FAMILY — see `agentEngine`, which reads
/// `backend.kind` rather than `agent.kind`. Mobile passes `agent.kind` here
/// and gets away with it only because mobile-spawned agents carry the engine
/// there; a template-spawned steward carries its persona, matches no family,
/// and silently loses every attach affordance.
export function promptCapabilities(
  engine: string | undefined,
  mode: string,
  families: readonly Entity[],
): PromptCapabilities {
  if (engine === undefined || engine === '') return NO_PROMPT_CAPABILITIES;
  const family = families.find((f) => str(f, 'family') === engine);
  // Not a correctness gate — a family with no maps resolves to all-false
  // anyway, and a mutation that deletes this line changes no answer. It is
  // here because "the registry has never heard of this engine" and "this
  // engine declines every modality" are different facts that happen to have
  // the same consequence, and the next reader should not have to derive that.
  if (family === undefined) return NO_PROMPT_CAPABILITIES;
  const flag = (key: keyof PromptCapabilities): boolean => {
    const map = obj(family, FLAG_KEY[key]);
    return map !== undefined && map[mode] === true;
  };
  return { image: flag('image'), pdf: flag('pdf'), audio: flag('audio'), video: flag('video') };
}

/// Whether a classified attachment may be staged. `text` is always allowed:
/// it is inlined into the message body as a fenced code block, so it rides the
/// ordinary text channel and needs no engine capability at all.
export function attachAllowed(kind: AttachKind, caps: PromptCapabilities): boolean {
  switch (kind) {
    case 'text':
      return true;
    case 'image':
      return caps.image;
    case 'pdf':
      return caps.pdf;
    case 'audio':
      return caps.audio;
    case 'video':
      return caps.video;
  }
}

/// True when the composer has any binary channel at all. With none, the attach
/// button is pointless — a picker whose every result is refused.
export function anyAttachable(caps: PromptCapabilities): boolean {
  return caps.image || caps.pdf || caps.audio || caps.video;
}
