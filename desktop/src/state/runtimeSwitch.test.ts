import assert from 'node:assert/strict';
import test from 'node:test';
import type { Entity } from '../hub/types.ts';
import {
  anyPillVisible,
  modeModelStateFromEvents,
  switchOptions,
  switchPills,
  type SwitchPill,
} from './runtimeSwitch.ts';

/// The registry rows the desktop reads, matching what the hub publishes at
/// `GET /agent-families` (and what `agent_families.generated.json` carries).
const FAMILIES: Entity[] = [
  {
    family: 'claude-code',
    runtime_mode_switch: { M1: 'respawn', M2: 'respawn' },
    runtime_switch_fields: { model: true, mode: false },
  },
  {
    family: 'codex',
    runtime_mode_switch: { M1: 'respawn', M2: 'respawn' },
    runtime_switch_fields: { model: false, mode: false },
  },
  {
    family: 'gemini-cli',
    runtime_mode_switch: { M1: 'rpc', M2: 'per_turn_argv' },
    runtime_switch_fields: { model: true, mode: true },
  },
];

const sys = (payload: Entity): Entity => ({ kind: 'system', payload });
const pill = (pills: SwitchPill[], field: 'mode' | 'model'): SwitchPill => {
  const p = pills.find((x) => x.field === field);
  assert.ok(p !== undefined, `no ${field} pill`);
  return p;
};

test('each of the four fields is captured from the latest event carrying it', () => {
  // The separating input, and the reason this function exists. The hub posts
  // a synthetic system event after a successful set_model that carries ONLY
  // the new currentModelId; the list lives on the older session/new event.
  // A reducer that took the list from the same event as the id would end up
  // with a current model and an empty list — and the picker would vanish the
  // moment the director first used it.
  const events: Entity[] = [
    sys({
      currentModeId: 'default',
      availableModes: [{ id: 'default', name: 'Default' }, { id: 'yolo', name: 'YOLO' }],
      currentModelId: 'gemini-2',
      availableModels: [{ modelId: 'gemini-2', name: 'Gemini 2' }, { modelId: 'gemini-3', name: 'Gemini 3' }],
    }),
    sys({ currentModelId: 'gemini-3' }),
  ];
  const st = modeModelStateFromEvents(events);
  assert.equal(st.currentModel, 'gemini-3', 'the newer id must win');
  assert.equal(st.availableModels.length, 2, 'the older list must survive the id-only event');
  assert.equal(st.currentMode, 'default');
  assert.equal(st.availableModes.length, 2);
});

test('a feed with no advertisement yields nothing rather than empty strings', () => {
  const st = modeModelStateFromEvents([{ kind: 'text', payload: { body: 'hi' } }]);
  assert.equal(st.currentMode, undefined);
  assert.equal(st.currentModel, undefined);
  assert.deepEqual(st.availableModes, []);
});

test('model entries key on modelId, mode entries on id', () => {
  // kimi ships models with only `modelId`. An id-only reader collapsed every
  // option to '' and the press guard swallowed them all, silently.
  const models = switchOptions([{ modelId: 'kimi-code/k3', name: 'K3' }, { modelId: 'k2' }]);
  assert.deepEqual(models.map((o) => o.id), ['kimi-code/k3', 'k2']);
  assert.equal(models[1].label, 'k2', 'label falls back to the id');
  const modes = switchOptions([{ id: 'plan', name: 'Plan', description: 'read only' }]);
  assert.deepEqual(modes, [{ id: 'plan', label: 'Plan', description: 'read only' }]);
  assert.deepEqual(switchOptions([{ name: 'no id at all' }]), [], 'an option with no id is not offerable');
});

test('claude M2: model is typeable and restarts the agent; mode is read-only', () => {
  // The pair that separates the field mask from the route. Both fields route
  // "respawn" for this family, so a mask that mirrored the route would give
  // the same answer twice. `--model` is in every claude template's cmd;
  // `--permission-mode` is in none, so the hub refuses it — and the pill must
  // not offer a button whose only outcome is a 422.
  const pills = switchPills(
    'claude-code',
    'M2',
    FAMILIES,
    { model: 'claude-opus-5', permission_mode: 'acceptEdits' },
    [],
  );
  const model = pill(pills, 'model');
  assert.equal(model.kind, 'type', 'switchable, but claude advertises no model list');
  assert.equal(model.current, 'claude-opus-5');
  assert.equal(model.respawns, true, 'the respawn route must reach the confirm step');

  const mode = pill(pills, 'mode');
  assert.equal(mode.kind, 'readonly');
  assert.equal(mode.current, 'acceptEdits', 'still worth showing what is in effect');
  assert.equal(mode.options.length, 0);
});

test('codex: model is read-only from session.init, mode is hidden entirely', () => {
  // codex's session.init carries a model and no permission mode, and neither
  // field is switchable (`--approval-policy` is not a codex flag; `codex
  // app-server` takes no --model). So: show the one we know, offer neither.
  const pills = switchPills('codex', 'M2', FAMILIES, { model: 'gpt-5-codex' }, []);
  assert.equal(pill(pills, 'model').kind, 'readonly');
  assert.equal(pill(pills, 'model').current, 'gpt-5-codex');
  assert.equal(pill(pills, 'mode').kind, 'hidden');
  assert.equal(anyPillVisible(pills), true);
});

test('an ACP agent that advertises lists gets a picker, with no respawn warning', () => {
  const events: Entity[] = [
    sys({
      currentModeId: 'default',
      availableModes: [{ id: 'default', name: 'Default' }],
      currentModelId: 'gemini-3',
      availableModels: [{ modelId: 'gemini-3', name: 'Auto (Gemini 3)' }],
    }),
  ];
  const pills = switchPills('gemini-cli', 'M1', FAMILIES, undefined, events);
  const model = pill(pills, 'model');
  assert.equal(model.kind, 'pick');
  assert.equal(model.currentLabel, 'Auto (Gemini 3)', 'the advertised name, not the raw id');
  assert.equal(model.respawns, false, 'rpc switches in place — no restart to preview');
  assert.equal(pill(pills, 'mode').kind, 'pick');
});

test('the advertisement outranks session.init for what is in effect now', () => {
  // session.init is the handshake frame and never changes again; the
  // advertisement is what moves mid-session. Reading init first would pin the
  // pill to the launch value forever.
  const pills = switchPills(
    'gemini-cli',
    'M1',
    FAMILIES,
    { model: 'stale-from-handshake' },
    [sys({ currentModelId: 'gemini-3', availableModels: [{ modelId: 'gemini-3', name: 'G3' }] })],
  );
  assert.equal(pill(pills, 'model').current, 'gemini-3');
});

test('an unknown engine and an empty registry grant nothing', () => {
  for (const [engine, families] of [
    ['no-such-engine', FAMILIES],
    ['claude-code', [] as Entity[]],
    [undefined, FAMILIES],
  ] as const) {
    const pills = switchPills(engine, 'M2', families, { model: 'x' }, []);
    assert.equal(pill(pills, 'model').kind, 'readonly', 'a known value still shows');
    assert.equal(pill(pills, 'mode').kind, 'hidden');
  }
  assert.equal(anyPillVisible(switchPills('no-such-engine', 'M2', FAMILIES, undefined, [])), false);
});

test('a driving mode the family never declared switches nothing', () => {
  // claude-code declares M1 and M2 only. An M4 agent must not inherit M2's
  // route by proximity — the hub answers "unsupported" for the missing key
  // and the pill has to agree.
  const pills = switchPills('claude-code', 'M4', FAMILIES, { model: 'claude-opus-5' }, []);
  assert.equal(pill(pills, 'model').kind, 'readonly');
});

test('a family that declares a route but no field mask offers no switch', () => {
  // The no-affordance-by-default rule: a new family cannot inherit a
  // capability by omitting the declaration.
  const families: Entity[] = [{ family: 'mystery', runtime_mode_switch: { M2: 'rpc' } }];
  const pills = switchPills('mystery', 'M2', families, { model: 'm' }, []);
  assert.equal(pill(pills, 'model').kind, 'readonly');
  assert.equal(pill(pills, 'mode').kind, 'hidden');
});
