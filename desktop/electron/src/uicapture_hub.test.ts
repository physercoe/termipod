/// Tests for the ui_screenshot hub round trip (D3 — plan §3.3). Every seam is
/// injected, so raise → poll → dismiss sequencing is provable against a mocked
/// fetch — including the one wire shape that is load-bearing on the hub side:
/// the dismiss body must NOT name a `resolved_by` (the column REFERENCES
/// agents(id); a non-agent value FK-violates, the 500 is swallowed, and the
/// card would loiter in the director's inbox forever).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { approveViaCard, approveViaCardWithOption, dismissCard, raiseCard, teamUrl, type HubLeg } from './uicapture_hub.ts';
import { UI_CAPTURE_POLL_MS } from './uicapture.ts';

const LEG: HubLeg = { baseUrl: 'https://hub.example', teamId: 'team_1', token: 'tok' };
const CARD = { summary: 'agent wants a screenshot', payload: { tool: 'ui_screenshot', session_grant: false } };

interface Call {
  url: string;
  method: string;
  body: unknown;
}

/// A scripted fetch: each entry answers the next request; the log keeps what
/// was sent. `Response`s are real, so .ok/.json() behave like the wire.
function scriptedFetch(script: Array<Response | Error>): { fetchFn: (url: string, init?: RequestInit) => Promise<Response>; calls: Call[] } {
  const calls: Call[] = [];
  return {
    calls,
    fetchFn: (url: string, init?: RequestInit) => {
      calls.push({
        url,
        method: init?.method ?? 'GET',
        body: typeof init?.body === 'string' ? (JSON.parse(init.body) as unknown) : undefined,
      });
      const next = script.shift() ?? new Error('script exhausted');
      return next instanceof Error ? Promise.reject(next) : Promise.resolve(next);
    },
  };
}

function json(status: number, body: unknown): Response {
  // 204 must be bodyless — the Response constructor enforces it.
  if (status === 204) return new Response(null, { status });
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });
}

/// A clock that only sleep advances — the poll loop is deterministic.
function fakeClock(): { now: () => number; sleep: (ms: number) => Promise<void> } {
  let t = 0;
  return { now: () => t, sleep: (ms: number) => ((t += ms), Promise.resolve()) };
}

// ── The FK-sensitive dismiss body ────────────────────────────────────────────

test('dismissCard sends an EMPTY body — resolved_by absent, never a non-agent id', async () => {
  const { fetchFn, calls } = scriptedFetch([json(204, {})]);
  await dismissCard(LEG, 'att_1', fetchFn);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, teamUrl(LEG, '/attention/att_1/resolve'));
  assert.equal(calls[0].method, 'POST');
  // {} on the wire: NULLIF('') → NULL is the hub's human/system-dismiss
  // convention; any value here must be a real agents.id or the UPDATE
  // FK-violates and the card never closes.
  assert.deepEqual(calls[0].body, {});
});

test('dismissCard swallows transport errors (best-effort tidy-up)', async () => {
  const { fetchFn } = scriptedFetch([new Error('conn refused')]);
  await dismissCard(LEG, 'att_1', fetchFn); // must not throw
});

// ── Raising ──────────────────────────────────────────────────────────────────

test('raiseCard posts a desktop_action card and returns its id', async () => {
  const { fetchFn, calls } = scriptedFetch([json(201, { id: 'att_9' })]);
  const id = await raiseCard(LEG, CARD.summary, CARD.payload, 'pi-a', fetchFn);
  assert.equal(id, 'att_9');
  const body = calls[0].body as Record<string, unknown>;
  assert.equal(body.kind, 'desktop_action');
  assert.equal(body.scope_kind, 'team');
  assert.equal(body.actor_handle, 'pi-a');
  assert.deepEqual(body.pending_payload, CARD.payload);
});

test('raiseCard maps hub refusal, bodyless success and transport error to null', async () => {
  for (const script of [[json(500, {})], [json(201, {})], [new Error('down')]]) {
    const { fetchFn } = scriptedFetch(script);
    assert.equal(await raiseCard(LEG, CARD.summary, CARD.payload, 'pi-a', fetchFn), null);
  }
});

// ── The whole approval leg ───────────────────────────────────────────────────

test('approveViaCard: approve resolves without a dismiss', async () => {
  const { fetchFn, calls } = scriptedFetch([
    json(201, { id: 'att_1' }),
    json(200, { status: 'resolved', decisions: [{ decision: 'approve' }] }),
  ]);
  const clock = fakeClock();
  assert.equal(await approveViaCard(LEG, CARD, 'pi-a', { ...clock, fetchFn }), 'approve');
  assert.equal(calls.filter((c) => c.url.endsWith('/resolve')).length, 0);
});

test('approveViaCard: a denied card is denied, and NOT dismissed (it already resolved)', async () => {
  const { fetchFn, calls } = scriptedFetch([
    json(201, { id: 'att_1' }),
    json(200, { status: 'resolved', decisions: [{ decision: 'reject' }] }),
  ]);
  const clock = fakeClock();
  assert.equal(await approveViaCard(LEG, CARD, 'pi-a', { ...clock, fetchFn }), 'denied');
  assert.equal(calls.filter((c) => c.url.endsWith('/resolve')).length, 0);
});

test('approveViaCard: timeout dismisses the card and denies', async () => {
  // Two polls fit in the window; both come back open; then the deadline
  // passes and the leg tidies up after itself.
  const { fetchFn, calls } = scriptedFetch([
    json(201, { id: 'att_1' }),
    json(200, { status: 'open' }),
    json(200, { status: 'open' }),
    json(204, {}),
  ]);
  const clock = fakeClock();
  const verdict = await approveViaCard(LEG, CARD, 'pi-a', { ...clock, fetchFn, timeoutMs: UI_CAPTURE_POLL_MS * 2 });
  assert.equal(verdict, 'timeout');
  // The dismiss is fire-and-forget; let it land before inspecting the log.
  await new Promise((r) => setTimeout(r, 0));
  const dismiss = calls.filter((c) => c.url.endsWith('/attention/att_1/resolve'));
  assert.equal(dismiss.length, 1);
  assert.deepEqual(dismiss[0].body, {});
});

test('approveViaCard: poll transport errors are not decisions — the deadline still governs', async () => {
  const { fetchFn } = scriptedFetch([
    json(201, { id: 'att_1' }),
    new Error('hiccup'),
    new Error('hiccup'),
    json(204, {}),
  ]);
  const clock = fakeClock();
  const verdict = await approveViaCard(LEG, CARD, 'pi-a', { ...clock, fetchFn, timeoutMs: UI_CAPTURE_POLL_MS * 2 });
  assert.equal(verdict, 'timeout');
});

test('approveViaCardWithOption: an approve carries the session option through', async () => {
  const clock = fakeClock();
  const { fetchFn } = scriptedFetch([
    json(200, { id: 'att_1' }),
    json(200, { status: 'resolved', decisions: [{ decision: 'approve', option_id: 'session' }] }),
  ]);
  const out = await approveViaCardWithOption(LEG, CARD, 'pi-a', { ...clock, fetchFn });
  assert.deepEqual(out, { verdict: 'approve', optionId: 'session' });
});

test('approveViaCardWithOption: a denial and a timeout carry NO option', async () => {
  const denied = scriptedFetch([json(200, { id: 'att_1' }), json(200, { status: 'resolved', decisions: [{ decision: 'reject', option_id: 'session' }] })]);
  assert.deepEqual(await approveViaCardWithOption(LEG, CARD, 'pi-a', { ...fakeClock(), fetchFn: denied.fetchFn }), {
    verdict: 'denied',
    optionId: '',
  });

  // A timeout dismisses the card (2 polls inside the window, then the
  // deadline) and grants nothing — the lease must not outlive a decision that
  // was never made.
  const open = scriptedFetch([
    json(200, { id: 'att_1' }),
    json(200, { status: 'open' }),
    json(200, { status: 'open' }),
    json(204, null),
  ]);
  const out = await approveViaCardWithOption(LEG, CARD, 'pi-a', {
    ...fakeClock(),
    fetchFn: open.fetchFn,
    timeoutMs: UI_CAPTURE_POLL_MS * 2,
  });
  assert.deepEqual(out, { verdict: 'timeout', optionId: '' });
});

test('approveViaCard: a hub that refuses the card is raise_failed, and never polled', async () => {
  const { fetchFn, calls } = scriptedFetch([json(500, {})]);
  const clock = fakeClock();
  assert.equal(await approveViaCard(LEG, CARD, 'pi-a', { ...clock, fetchFn }), 'raise_failed');
  assert.equal(calls.length, 1);
});
