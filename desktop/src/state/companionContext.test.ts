/// Tests for the surface-context provider registry (state/companionContext.ts)
/// feeding the dock companion: register / replace / focus order / unregister /
/// insert passthrough. The registry backs the unified assistant dock — the
/// companion reads the ACTIVE provider (the most-recently-registered surface).
/// Run locally: `node --test src/state/companionContext.test.ts` from
/// `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  activeProvider,
  useCompanionContext,
  type CompanionContextProvider,
} from './companionContext.ts';

function provider(label: string, block = `ctx:${label}`): CompanionContextProvider {
  return { label, build: () => block };
}

function reset(): void {
  useCompanionContext.setState({ providers: {}, order: [] });
}

test('register: the first provider is active', () => {
  reset();
  useCompanionContext.getState().register('read', provider('paper'));
  assert.equal(activeProvider(useCompanionContext.getState())?.label, 'paper');
});

test('focus order: the most-recently-registered surface wins', () => {
  reset();
  const { register } = useCompanionContext.getState();
  register('read', provider('paper'));
  register('author', provider('doc'));
  assert.equal(activeProvider(useCompanionContext.getState())?.label, 'doc');
  // A selection change on Read re-registers it → Read tops the stack again.
  register('read', provider('paper-2'));
  assert.equal(activeProvider(useCompanionContext.getState())?.label, 'paper-2');
});

test('replace: re-registering a job swaps the provider, no duplicate', () => {
  reset();
  const { register } = useCompanionContext.getState();
  register('author', provider('doc'));
  register('author', provider('doc-renamed'));
  const s = useCompanionContext.getState();
  assert.deepEqual(s.order, ['author']);
  assert.equal(activeProvider(s)?.label, 'doc-renamed');
});

test('unregister: drops the provider and falls back to the next-most-recent', () => {
  reset();
  const { register, unregister } = useCompanionContext.getState();
  register('read', provider('paper'));
  register('author', provider('doc'));
  unregister('author');
  assert.equal(activeProvider(useCompanionContext.getState())?.label, 'paper');
  unregister('read');
  assert.equal(activeProvider(useCompanionContext.getState()), null);
  // Unregistering an unknown job is a no-op (state identity preserved).
  const before = useCompanionContext.getState();
  unregister('nope');
  assert.equal(useCompanionContext.getState(), before);
});

test('insert passthrough: the active provider carries its insert target', () => {
  reset();
  const inserted: string[] = [];
  useCompanionContext.getState().register('author', {
    label: 'doc',
    build: () => 'ctx',
    insert: (text) => inserted.push(text),
  });
  const p = activeProvider(useCompanionContext.getState());
  assert.equal(typeof p?.insert, 'function');
  p?.insert?.('reply text');
  assert.deepEqual(inserted, ['reply text']);
});

test('a provider without insert stays insert-less (structured bodies)', () => {
  reset();
  useCompanionContext.getState().register('author', provider('diagram'));
  assert.equal(activeProvider(useCompanionContext.getState())?.insert, undefined);
});
