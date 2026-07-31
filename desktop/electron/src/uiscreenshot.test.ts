/// Tests for the D3 bridge surface (docs/plans/desktop-ui-context-and-pointing.md
/// §3.3): `ui_screenshot`'s catalog gating, target resolution, error shaping
/// and — the part that is easy to get wrong — the audit posture. The policy
/// itself (what refuses, how a decision reads) is uicapture.test.ts; the
/// capture provider is faked here exactly like the CDP backend. `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  dispatchHubInvoke,
  handleMcpMessage,
  READ_TOOLS,
  shouldMirrorAudit,
  type BridgeAuditEntry,
  type BridgeBackend,
  type BridgeTarget,
  type McpServerDeps,
  type UiCaptureRequest,
  type UiCaptureResult,
} from './browserbridge.ts';

const KIMIWEB_URL = 'http://127.0.0.1:17331/#token=secret-in-the-fragment';

const TABS: BridgeTarget[] = [
  { tabId: 7, url: 'https://arxiv.org/abs/2401.00001', title: 'a paper', partition: 'persist:webtab', bridge: 'full' },
  { tabId: 9, url: KIMIWEB_URL, title: 'Kimi', partition: 'kimiweb', bridge: 'read' },
];

const backend: BridgeBackend = {
  listTargets: () => TABS,
  sendCommand: async () => {
    throw new Error('unexpected CDP call — ui_screenshot captures main-side, never over the guest debugger');
  },
};

const PNG_B64 = 'aGVsbG8=';

interface Harness {
  deps: McpServerDeps;
  seen: UiCaptureRequest[];
  audit: BridgeAuditEntry[];
}

function harness(opts: { sharing?: boolean; result?: UiCaptureResult; provider?: boolean } = {}): Harness {
  const seen: UiCaptureRequest[] = [];
  const audit: BridgeAuditEntry[] = [];
  const result = opts.result ?? { ok: true, data_b64: PNG_B64, width: 800, height: 600 };
  const deps: McpServerDeps = {
    backend,
    serverInfo: { name: 'termipod-browser', version: '0.0.0-test' },
    uiFocusAvailable: () => opts.sharing !== false,
    getUiFocus: () => ({ surface: 'read', captured_at: 'x' }),
    onAction: (e) => audit.push(e),
    ...(opts.provider === false
      ? {}
      : {
          captureUi: async (req: UiCaptureRequest): Promise<UiCaptureResult> => {
            seen.push(req);
            return result;
          },
        }),
  };
  return { deps, seen, audit };
}

interface ToolResult {
  content: Array<{ type?: string; text?: string; data?: string; mimeType?: string }>;
  isError?: boolean;
}

async function call(deps: McpServerDeps, args: Record<string, unknown> = {}): Promise<ToolResult> {
  const res = await handleMcpMessage(
    { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'ui_screenshot', arguments: args } },
    deps,
    { scope: 'read', agentId: 'ag_1', agentHandle: 'kimi-1' },
  );
  return res?.result as ToolResult;
}

// ── Catalog + consent gating ─────────────────────────────────────────────────

test('ui_screenshot is a READ-SCOPE tool gated by the sharing toggle', async () => {
  // Read scope on purpose: the local kimi-code loop holds only the read token,
  // and the consent that matters for a capture is the per-call card, not a
  // spawn flag. (Its ACTION-ness lives in the approval + audit, below.)
  assert.ok(READ_TOOLS.some((t) => t.name === 'ui_screenshot'));

  const on = await handleMcpMessage({ jsonrpc: '2.0', id: 1, method: 'tools/list' }, harness().deps, { scope: 'read', agentId: null });
  const listed = (on?.result as { tools: Array<{ name: string; description: string }> }).tools;
  const tool = listed.find((t) => t.name === 'ui_screenshot');
  assert.ok(tool !== undefined, 'toggle on must list the tool');
  // The description is the only steering an agent gets: it must say the
  // approval is per-call and point at the cheaper representations (§3.3/D-4).
  assert.match(tool.description, /approval card/);
  assert.match(tool.description, /ui_get_focus/);
  assert.match(tool.description, /browser_snapshot/);

  const off = await handleMcpMessage({ jsonrpc: '2.0', id: 2, method: 'tools/list' }, harness({ sharing: false }).deps, {
    scope: 'read',
    agentId: null,
  });
  const hidden = (off?.result as { tools: Array<{ name: string }> }).tools;
  assert.ok(!hidden.some((t) => t.name === 'ui_screenshot'), 'toggle off must hide the tool');
});

test('the toggle gate binds the CALL, not only the catalog', async () => {
  const h = harness({ sharing: false });
  const out = await call(h.deps);
  assert.equal(out.isError, true);
  assert.match(out.content[0]?.text ?? '', /UI_UNAVAILABLE/);
  assert.equal(h.seen.length, 0, 'a refused call never reaches the capture provider');
});

test('a build with no capture provider refuses instead of throwing', async () => {
  const out = await call(harness({ provider: false }).deps);
  assert.equal(out.isError, true);
  assert.match(out.content[0]?.text ?? '', /UI_UNAVAILABLE/);
});

// ── Target resolution ────────────────────────────────────────────────────────

test('no tabId captures the desktop window; the caller identity rides along', async () => {
  const h = harness();
  const out = await call(h.deps);
  assert.equal(out.isError, undefined);
  assert.deepEqual(out.content, [{ type: 'image', data: PNG_B64, mimeType: 'image/png' }]);
  assert.deepEqual(h.seen, [{ tabId: null, url: null, partition: null, agentId: 'ag_1', agentHandle: 'kimi-1', via: 'local' }]);
});

test('a tabId resolves against the live registry, fragment-stripped', async () => {
  const h = harness();
  await call(h.deps, { tabId: 9 });
  // The guest URL reaches the approval card, so it must be stripped on the way
  // — a kimiweb hash carries a bearer (ADR-059 D-5).
  assert.deepEqual(h.seen[0], {
    tabId: 9,
    url: 'http://127.0.0.1:17331/',
    partition: 'kimiweb',
    agentId: 'ag_1',
    agentHandle: 'kimi-1',
    via: 'local',
  });
});

test('an unknown or malformed tabId is refused before the provider runs', async () => {
  const h = harness();
  const gone = await call(h.deps, { tabId: 12345 });
  assert.equal(gone.isError, true);
  assert.match(gone.content[0]?.text ?? '', /TARGET_GONE/);

  const bad = await call(h.deps, { tabId: 'seven' });
  assert.equal(bad.isError, true);
  assert.match(bad.content[0]?.text ?? '', /INVALID_PARAMS/);
  assert.equal(h.seen.length, 0);
});

test('a provider refusal surfaces its code and message verbatim', async () => {
  const h = harness({ result: { ok: false, code: 'CAPTURE_REFUSED', message: "surface 'vault' refuses pixel capture" } });
  const out = await call(h.deps);
  assert.equal(out.isError, true);
  assert.match(out.content[0]?.text ?? '', /CAPTURE_REFUSED: surface 'vault' refuses pixel capture/);
});

// ── Audit posture (the part that is easy to get wrong) ───────────────────────

test('a LOCAL ui_screenshot is audited — unlike a local browser read', async () => {
  const h = harness();
  await call(h.deps);
  assert.equal(h.audit.length, 1, 'exactly one entry per call');
  assert.equal(h.audit[0]?.tool, 'ui_screenshot');
  assert.equal(h.audit[0]?.via, 'local');
  assert.equal(h.audit[0]?.ok, true);
  assert.equal(h.audit[0]?.agent_id, 'ag_1');

  // The contrast that makes the rule visible: a local browser READ is not
  // audited at all (same-machine, high frequency — the ring would churn).
  const reads = harness();
  await handleMcpMessage(
    { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'browser_list_tabs', arguments: {} } },
    reads.deps,
    { scope: 'read', agentId: 'ag_1' },
  );
  assert.equal(reads.audit.length, 0);
});

test('a failed capture is audited too, with its code', async () => {
  const h = harness({ result: { ok: false, code: 'CAPTURE_DENIED', message: 'the desktop user denied this screenshot' } });
  await call(h.deps);
  assert.equal(h.audit.length, 1);
  assert.equal(h.audit[0]?.ok, false);
  assert.equal(h.audit[0]?.error, 'CAPTURE_DENIED');
});

test('ui_screenshot mirrors to the hub on every leg; ui_get_focus does not', () => {
  const entry = (tool: string, via: 'local' | 'hub'): BridgeAuditEntry => ({
    ts: '2026-07-31T00:00:00Z',
    tool,
    agent_id: 'ag_1',
    via,
    tab_id: null,
    url: null,
    partition: null,
    args: {},
    ok: true,
    error: null,
  });
  // A capture is an action for audit purposes even though it sits in
  // READ_TOOLS — the hub mirror is where the user's own event stream records
  // that a frame of their screen was handed over.
  assert.equal(shouldMirrorAudit(entry('ui_screenshot', 'hub')), true);
  assert.equal(shouldMirrorAudit(entry('ui_screenshot', 'local')), true);
  // Hub-leg reads stay ring-only (the hub already knows it routed them).
  assert.equal(shouldMirrorAudit(entry('ui_get_focus', 'hub')), false);
  assert.equal(shouldMirrorAudit(entry('browser_snapshot', 'hub')), false);
});

// ── The hub leg ──────────────────────────────────────────────────────────────

test('a hub-relayed capture is marked via:hub so the desktop raises no second card', async () => {
  const h = harness();
  const out = await dispatchHubInvoke(h.deps, { tool: 'ui_screenshot', args: {}, agent_id: 'ag_9', agent_handle: 'remote-1' }, new Set(), 'desktop');
  assert.equal(out.ok, true);
  assert.equal(h.seen[0]?.via, 'hub', 'the hub gated this call already (D5) — a second card would double-prompt');
  assert.equal(h.seen[0]?.agentId, 'ag_9');
  assert.equal(h.seen[0]?.agentHandle, 'remote-1');
});

test('a revoked agent cannot capture, and never reaches the provider', async () => {
  const h = harness();
  const out = await dispatchHubInvoke(h.deps, { tool: 'ui_screenshot', args: {}, agent_id: 'ag_9' }, new Set(['ag_9']), 'desktop');
  assert.equal(out.ok, false);
  assert.equal(h.seen.length, 0);
});
