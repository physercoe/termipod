import { obj, str, type Entity } from '../hub/types.ts';

/// R6 — what the model / permission-mode pills may offer, and for which of
/// them a click can actually succeed.
///
/// Two independent questions decide a pill, and conflating them is what kept
/// this feature dead:
///
///  1. **What is in effect now?** Two sources, in priority order: the agent's
///     own ACP advertisement (`currentModelId` / `currentModeId` system
///     events, M1 engines) and the merged `session.init` frame (`model`,
///     `permission_mode` — what claude M2 reports). The advertisement wins
///     when both are present because it is the one that changes mid-session.
///
///  2. **Can it be changed?** The family registry answers, in two parts:
///     `runtime_mode_switch[drivingMode]` is HOW a switch travels, and
///     `runtime_switch_fields[field]` is WHETHER this field can travel at
///     all. The second was added in R6 after measuring that three of the four
///     (family, field) pairs the Companion drives could never succeed —
///     claude's `--permission-mode` is in no spawn template, and codex's
///     `--approval-policy` is not a codex flag. Before that, every one of
///     those clicks was a 422 the UI would have had no way to predict.
///
/// The two answers are deliberately not merged: knowing the current model is
/// worth showing even when it cannot be changed, and mobile's picker — which
/// hides itself whenever the agent advertises nothing — leaves a claude
/// session with no model indicator at all. A read-only pill is the honest
/// middle state, and the one this port adds.

/// The four fields an ACP agent advertises, captured independently.
export interface ModeModelState {
  currentMode?: string;
  availableModes: readonly Entity[];
  currentModel?: string;
  availableModels: readonly Entity[];
}

/// Scan the feed backwards for the most recent value of each of the four
/// fields, INDEPENDENTLY — a port of mobile's `modeModelStateFromEvents`
/// (`feed_reducer.dart:548`) including the W7c fix that is the whole point of
/// the function.
///
/// The hub posts a synthetic `system` event carrying only the new
/// `currentModeId` / `currentModelId` after a set_mode/set_model RPC
/// succeeds (`driver_acp.go:1969`); the `available*` lists live on the older
/// session/new event. Capturing the list from the same event branch as the id
/// — the obvious reading — means the first switch leaves `currentModel` set
/// with `availableModels` empty, and the picker hides itself immediately
/// after the director uses it.
export function modeModelStateFromEvents(events: readonly Entity[]): ModeModelState {
  const out: ModeModelState = { availableModes: [], availableModels: [] };
  let haveModes = false;
  let haveModels = false;
  for (let i = events.length - 1; i >= 0; i--) {
    const e = events[i];
    if (str(e, 'kind') !== 'system') continue;
    const p = obj(e, 'payload');
    if (p === undefined) continue;
    if (out.currentMode === undefined && typeof p['currentModeId'] === 'string') {
      out.currentMode = p['currentModeId'];
    }
    if (!haveModes && Array.isArray(p['availableModes'])) {
      out.availableModes = (p['availableModes'] as unknown[]).filter(isEntity);
      haveModes = true;
    }
    if (out.currentModel === undefined && typeof p['currentModelId'] === 'string') {
      out.currentModel = p['currentModelId'];
    }
    if (!haveModels && Array.isArray(p['availableModels'])) {
      out.availableModels = (p['availableModels'] as unknown[]).filter(isEntity);
      haveModels = true;
    }
    if (out.currentMode !== undefined && out.currentModel !== undefined && haveModes && haveModels) {
      break;
    }
  }
  return out;
}

function isEntity(v: unknown): v is Entity {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

/// One option in a pill's menu. `id` is what rides the wire as
/// `mode_id` / `model_id`.
export interface SwitchOption {
  id: string;
  label: string;
  description?: string;
}

/// ACP spells the two lists differently: model entries carry `modelId`, mode
/// entries carry `id`. Mobile learned this the hard way — kimi's models ship
/// only `modelId`, so an `id`-only reader collapsed every option to the empty
/// string and the tap handler's non-empty guard swallowed every press with no
/// hub-side log to show for it (`session_details_sheet.dart:806`).
export function switchOptions(raw: readonly Entity[]): SwitchOption[] {
  const out: SwitchOption[] = [];
  for (const o of raw) {
    const id = str(o, 'modelId') ?? str(o, 'id') ?? '';
    if (id === '') continue;
    const label = str(o, 'name') ?? id;
    const description = str(o, 'description');
    out.push(description === undefined ? { id, label } : { id, label, description });
  }
  return out;
}

/// How a pill behaves.
///   - `hidden`   — nothing known and nothing offerable; render no pill.
///   - `readonly` — we know what is in effect but the hub would refuse a
///                  change. Shown, not clickable, with the reason on hover.
///   - `pick`     — the agent advertised options; a menu.
///   - `type`     — switchable, but nobody advertised a vocabulary (claude's
///                  `--model` takes an alias or a full name and lists neither),
///                  so the director types the value.
export type PillKind = 'hidden' | 'readonly' | 'pick' | 'type';

export interface SwitchPill {
  field: 'mode' | 'model';
  kind: PillKind;
  /// The id in effect, when anything reported one.
  current?: string;
  /// Display text for `current` — an advertised option's `name` when the id
  /// matches one, else the id itself.
  currentLabel?: string;
  options: SwitchOption[];
  /// True when committing this change restarts the agent (the `respawn`
  /// route). The click must preview that, not just do it — a model flip
  /// terminating the agent is exactly the kind of consequence IAA exists to
  /// put in front of the director first.
  respawns: boolean;
}

const NO_FIELDS: Entity = {};

/// Resolve both pills for one agent. `families` is `GET /agent-families`;
/// `sessionInit` is the merged session.init frame; `events` is the feed.
///
/// Keyed on the engine FAMILY (`agentEngine`, i.e. `backend.kind`) for the
/// same reason F3's capabilities are: a template-spawned steward carries its
/// persona in `agent.kind`, matches no family, and would silently lose every
/// affordance.
export function switchPills(
  engine: string | undefined,
  drivingMode: string,
  families: readonly Entity[],
  sessionInit: Entity | undefined,
  events: readonly Entity[],
): SwitchPill[] {
  const family = engine === undefined || engine === '' ? undefined : families.find((f) => str(f, 'family') === engine);
  const routeRaw = family === undefined ? undefined : obj(family, 'runtime_mode_switch')?.[drivingMode];
  const route = typeof routeRaw === 'string' ? routeRaw : '';
  const fields = (family === undefined ? undefined : obj(family, 'runtime_switch_fields')) ?? NO_FIELDS;
  const advertised = modeModelStateFromEvents(events);

  // A family that declares no route for this driving mode, or declares
  // "unsupported", switches nothing — the same answer the hub gives.
  const routed = route === 'rpc' || route === 'respawn' || route === 'per_turn_argv';
  const respawns = route === 'respawn';

  const build = (
    field: 'mode' | 'model',
    current: string | undefined,
    rawOptions: readonly Entity[],
  ): SwitchPill => {
    const options = switchOptions(rawOptions);
    const switchable = routed && fields[field] === true;
    let kind: PillKind;
    if (switchable && options.length > 0) kind = 'pick';
    else if (switchable) kind = 'type';
    else if (current !== undefined && current !== '') kind = 'readonly';
    else kind = 'hidden';
    const matched = current === undefined ? undefined : options.find((o) => o.id === current);
    const pill: SwitchPill = { field, kind, options, respawns };
    if (current !== undefined && current !== '') {
      pill.current = current;
      pill.currentLabel = matched?.label ?? current;
    }
    return pill;
  };

  return [
    build(
      'model',
      advertised.currentModel ?? str(sessionInit ?? {}, 'model'),
      advertised.availableModels,
    ),
    build(
      'mode',
      advertised.currentMode ?? str(sessionInit ?? {}, 'permission_mode'),
      advertised.availableModes,
    ),
  ];
}

/// Whether the row renders at all.
export function anyPillVisible(pills: readonly SwitchPill[]): boolean {
  return pills.some((p) => p.kind !== 'hidden');
}
