/// Tests for the coworking A1 bridge surface (agent-desktop-coworking.md §1,
/// ADR-064): `author_read` / `author_apply`'s catalog gating, argument
/// narrowing, audit posture and annotations. The consent policy is
/// author.test.ts; the provider is faked here exactly like the CDP backend and
/// the capture provider. `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  AUTHOR_TOOL_NAMES,
  DESKTOP_ACTION_TOOL_NAMES,
  DESKTOP_AUDITED_TOOL_NAMES,
  DESKTOP_GATED_TOOL_NAMES,
  dispatchHubInvoke,
  handleMcpMessage,
  READ_TOOLS,
  redactBridgeArgs,
  tunnelClassForTool,
  type AuthorBridgeRequest,
  type AuthorBridgeResult,
  type BridgeAuditEntry,
  type BridgeBackend,
  type McpServerDeps,
} from './browserbridge.ts';

const backend: BridgeBackend = {
  listTargets: () => [],
  sendCommand: async () => {
    throw new Error('unexpected CDP call — the author tools never touch a guest');
  },
};

interface Harness {
  deps: McpServerDeps;
  seen: AuthorBridgeRequest[];
  audit: BridgeAuditEntry[];
}

function harness(opts: { sharing?: boolean; result?: AuthorBridgeResult; provider?: boolean } = {}): Harness {
  const seen: AuthorBridgeRequest[] = [];
  const audit: BridgeAuditEntry[] = [];
  const result = opts.result ?? { ok: true, text: 'done' };
  const deps: McpServerDeps = {
    backend,
    serverInfo: { name: 'termipod-browser', version: '0.0.0-test' },
    uiFocusAvailable: () => opts.sharing !== false,
    onAction: (e) => audit.push(e),
    ...(opts.provider === false
      ? {}
      : {
          authorBridge: async (req: AuthorBridgeRequest): Promise<AuthorBridgeResult> => {
            seen.push(req);
            return result;
          },
        }),
  };
  return { deps, seen, audit };
}

interface ToolResult {
  content: Array<{ type: string; text?: string; data?: string; mimeType?: string }>;
  isError?: true;
}

async function call(
  deps: McpServerDeps,
  name: string,
  args: Record<string, unknown>,
  ctx?: { scope: 'read' | 'full'; agentId: string | null; via?: 'local' | 'hub'; agentHandle?: string },
): Promise<ToolResult> {
  const res = await handleMcpMessage(
    { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name, arguments: args } },
    deps,
    ctx ?? { scope: 'read', agentId: 'ag_1', agentHandle: 'kimi-1' },
  );
  return res?.result as ToolResult;
}

test('every author tool lives in READ_TOOLS, so a read-scoped agent can reach them', () => {
  // The kimi loop holds only the read token. `author_apply` is action CLASS
  // (consent + audit) but read SCOPE, the same split ui_screenshot ships —
  // otherwise co-authoring would need a respawn flag the local arm never has.
  const names = READ_TOOLS.map((t) => t.name);
  assert.ok(names.includes('author_read'));
  assert.ok(names.includes('author_apply'));
  assert.ok(names.includes('author_render'));
  assert.ok(names.includes('author_guide'));
  assert.deepEqual([...AUTHOR_TOOL_NAMES].sort(), ['author_apply', 'author_guide', 'author_read', 'author_render']);
});

test('author_guide is gated but not audited, because it discloses nothing', () => {
  // It rides the same toggle as its siblings — one capability sentence — but
  // the audit log answers "what did an agent learn about me?", and this verb
  // answers with static reference text identical for every caller. Rows that
  // disclose nothing make the rows that do harder to find.
  assert.ok(DESKTOP_GATED_TOOL_NAMES.has('author_guide'));
  assert.equal(DESKTOP_AUDITED_TOOL_NAMES.has('author_guide'), false);
  assert.equal(DESKTOP_ACTION_TOOL_NAMES.has('author_guide'), false);
});

test('the sharing toggle hides BOTH author tools from every catalog', async () => {
  const off = harness({ sharing: false });
  const res = await handleMcpMessage({ jsonrpc: '2.0', id: 1, method: 'tools/list' }, off.deps);
  const names = (res?.result as { tools: Array<{ name: string }> }).tools.map((t) => t.name);
  assert.equal(names.includes('author_read'), false);
  assert.equal(names.includes('author_apply'), false);

  const on = harness();
  const res2 = await handleMcpMessage({ jsonrpc: '2.0', id: 1, method: 'tools/list' }, on.deps);
  const names2 = (res2?.result as { tools: Array<{ name: string }> }).tools.map((t) => t.name);
  assert.ok(names2.includes('author_read'));
  assert.ok(names2.includes('author_apply'));
  // Every gated tool is gated together — the set IS the filter, so a lane that
  // adds a tool cannot forget the catalog.
  for (const n of DESKTOP_GATED_TOOL_NAMES) assert.ok(names2.includes(n), n);
});

test('the gate lives at the tool too, not only in the catalog filter', async () => {
  // A stateless caller can call a tool it never listed. The hub tunnel leg does
  // exactly that.
  const off = harness({ sharing: false });
  for (const name of ['author_read', 'author_apply', 'author_guide']) {
    const out = await call(off.deps, name, { body: 'x', kind: 'diagram' });
    assert.equal(out.isError, true, name);
    assert.match(out.content[0]?.text ?? '', /UI_UNAVAILABLE/, name);
  }
  assert.equal(off.seen.length, 0, 'the provider was reached with sharing off');
});

test('author_guide answers from main, with no provider and no document', async () => {
  // The distinction that puts it in its own dispatcher arm: the other three
  // verbs are about a document that exists and need the renderer, so a build
  // with no provider refuses them. A guide that went unavailable with no
  // window open would be missing exactly when an agent is working out what to
  // send.
  const none = harness({ provider: false });
  const idx = await call(none.deps, 'author_guide', { kind: 'diagram' });
  assert.notEqual(idx.isError, true);
  assert.match(idx.content[0]?.text ?? '', /shapes/);

  const lib = await call(none.deps, 'author_guide', { kind: 'diagram', topic: 'aws4', filter: 'lambda' });
  assert.match(lib.content[0]?.text ?? '', /lambda/);

  // And it never reaches the author provider, even when there is one.
  const on = harness();
  await call(on.deps, 'author_guide', { kind: 'table' });
  assert.equal(on.seen.length, 0, 'author_guide reached the document provider');

  const bad = await call(none.deps, 'author_guide', { kind: 'spreadsheet' });
  assert.equal(bad.isError, true);
  assert.match(bad.content[0]?.text ?? '', /UNKNOWN_KIND/);
});

test('a build with no provider refuses instead of pretending', async () => {
  const none = harness({ provider: false });
  const out = await call(none.deps, 'author_read', {});
  assert.equal(out.isError, true);
  assert.match(out.content[0]?.text ?? '', /AUTHOR_UNAVAILABLE/);
});

test('author_apply narrows its arguments: body required, mode allowlisted', async () => {
  const h = harness();
  const noBody = await call(h.deps, 'author_apply', { document_id: 'doc_a' });
  assert.equal(noBody.isError, true);
  assert.match(noBody.content[0]?.text ?? '', /INVALID_PARAMS/);

  // A mode outside the set is refused BY NAME rather than falling back to
  // 'replace', which would commit whatever the caller sent as the document.
  const bad = await call(h.deps, 'author_apply', { body: '[]', mode: 'merge' });
  assert.equal(bad.isError, true);
  assert.match(bad.content[0]?.text ?? '', /INVALID_PARAMS/);
  assert.match(bad.content[0]?.text ?? '', /'replace', 'append' or 'ops'/);
  assert.equal(h.seen.length, 0, 'a refused argument still reached the provider');

  const ok = await call(h.deps, 'author_apply', { body: '# hi', mode: 'append', reason: 'why', document_id: 'doc_a' });
  assert.notEqual(ok.isError, true);
  assert.deepEqual(h.seen[0], {
    op: 'apply',
    documentId: 'doc_a',
    mode: 'append',
    body: '# hi',
    operations: [],
    format: 'svg',
    reason: 'why',
    agentId: 'ag_1',
    agentHandle: 'kimi-1',
    via: 'local',
  });
});

test("mode 'ops' takes operations, and each entry is narrowed before it is forwarded (D1)", async () => {
  const h = harness();
  const ok = await call(h.deps, 'author_apply', {
    mode: 'ops',
    document_id: 'doc_a',
    operations: [{ operation: 'delete', cell_id: 'n1' }, { operation: 'update', cell_id: 'n2', new_xml: '<mxCell id="n2"/>' }],
  });
  assert.notEqual(ok.isError, true);
  assert.equal(h.seen[0]?.mode, 'ops');
  assert.equal(h.seen[0]?.body, '', 'an ops call must not carry a body downstream');
  assert.deepEqual(h.seen[0]?.operations, [
    { operation: 'delete', cell_id: 'n1', new_xml: '' },
    { operation: 'update', cell_id: 'n2', new_xml: '<mxCell id="n2"/>' },
  ]);

  // The narrowing message names the entry and the field, because "operations is
  // invalid" costs a round trip that "operations[0].new_xml is required" does
  // not.
  const missing = await call(h.deps, 'author_apply', { mode: 'ops', operations: [{ operation: 'add', cell_id: 'n3' }] });
  assert.equal(missing.isError, true);
  assert.match(missing.content[0]?.text ?? '', /operations\[0\]\.new_xml is required/);

  // Both arguments at once is a model that has not decided which write it is
  // making; honouring one silently picks for it, and the loser is a document.
  const both = await call(h.deps, 'author_apply', { mode: 'ops', body: '<mxfile/>', operations: [{ operation: 'delete', cell_id: 'n1' }] });
  assert.equal(both.isError, true);
  assert.match(both.content[0]?.text ?? '', /operations, not body/);

  assert.equal(h.seen.length, 1, 'a refused ops call still reached the provider');
});

test('an omitted document_id reaches the provider as null, not as ""', async () => {
  const h = harness();
  await call(h.deps, 'author_read', {});
  assert.equal(h.seen[0]?.documentId, null);
  await call(h.deps, 'author_read', { document_id: '' });
  assert.equal(h.seen[1]?.documentId, null);
});

test('the leg reaches the provider — it decides who asks for consent', async () => {
  const h = harness();
  await dispatchHubInvoke(h.deps, { tool: 'author_apply', args: { body: 'x' }, agent_id: 'ag_9', agent_handle: 'remote-1' }, new Set(), 'desktop');
  assert.equal(h.seen[0]?.via, 'hub');
  assert.equal(h.seen[0]?.agentId, 'ag_9');
  assert.equal(h.seen[0]?.agentHandle, 'remote-1');
});

test('a provider refusal keeps its own code — the agent needs to know which', async () => {
  const h = harness({ result: { ok: false, code: 'INVALID_TABLE', message: 'not a table document' } });
  const out = await call(h.deps, 'author_apply', { body: '{ oops' });
  assert.equal(out.isError, true);
  assert.match(out.content[0]?.text ?? '', /INVALID_TABLE: not a table document/);
});

test('both author tools are audited on the LOCAL leg, unlike a browser read', async () => {
  const h = harness();
  await call(h.deps, 'author_read', {});
  await call(h.deps, 'author_apply', { body: 'x' });
  assert.deepEqual(h.audit.map((e) => e.tool), ['author_read', 'author_apply']);
  assert.deepEqual(h.audit.map((e) => e.via), ['local', 'local']);
  assert.ok(DESKTOP_AUDITED_TOOL_NAMES.has('author_read'));
  assert.ok(DESKTOP_AUDITED_TOOL_NAMES.has('author_apply'));

  // A same-machine browser read is NOT audited (high frequency, the ring would
  // churn) — the contrast is the point: document bytes are not page bytes.
  const h2 = harness();
  await call(h2.deps, 'browser_list_tabs', {});
  assert.equal(h2.audit.length, 0);
});

test('author_read stays readOnlyHint TRUE; author_apply is false', async () => {
  const h = harness();
  const res = await handleMcpMessage({ jsonrpc: '2.0', id: 1, method: 'tools/list' }, h.deps);
  const tools = (res?.result as { tools: Array<{ name: string; annotations: Record<string, unknown> }> }).tools;
  const read = tools.find((t) => t.name === 'author_read');
  const apply = tools.find((t) => t.name === 'author_apply');
  // The audit set and the action set are deliberately different: folding
  // author_read into DESKTOP_ACTION_TOOL_NAMES to get the audit row would have
  // annotated a read as a mutation, which is the one direction of this hint
  // that can cause harm (ADR-063 D5).
  assert.equal(read?.annotations.readOnlyHint, true);
  assert.equal(apply?.annotations.readOnlyHint, false);
  assert.equal(DESKTOP_ACTION_TOOL_NAMES.has('author_read'), false);
  assert.equal(DESKTOP_ACTION_TOOL_NAMES.has('author_apply'), true);
});

test('the document body never reaches the audit ring', () => {
  const args = redactBridgeArgs('author_apply', {
    document_id: 'doc_a',
    body: 'the user’s entire manuscript',
    reason: 'r'.repeat(400),
    operations: [{ operation: 'add', cell_id: 'n1', new_xml: '<mxCell id="n1" value="the user’s own diagram"/>' }],
  });
  // The ring is 50 entries in memory AND a hub agent_events row. It records
  // that an edit happened and how big it was, never the work itself.
  assert.match(String(args.body), /^<redacted \d+ chars>$/);
  // The COUNT survives — "1 operation" and "40 operations" are different rows
  // to a person reading the audit view, and neither is content.
  assert.equal(args.operations, '<redacted 1 operation>');
  assert.equal(redactBridgeArgs('author_apply', { operations: [1, 2] }).operations, '<redacted 2 operations>');
  assert.equal(args.document_id, 'doc_a', 'the id must survive — it is what makes the row readable');
  assert.ok(String(args.reason).length <= 220);
});

test('both author tools ride the desktop tunnel class, never the browser one', () => {
  assert.equal(tunnelClassForTool('author_read'), 'desktop');
  assert.equal(tunnelClassForTool('author_apply'), 'desktop');
  // Defense in depth against a hub that routed by the wrong kind: the hub
  // cards desktop.invoke actions and browser.invoke never raises that card, so
  // an author_apply arriving as a browser envelope would be an unapproved
  // write.
  const h = harness();
  return dispatchHubInvoke(h.deps, { tool: 'author_apply', args: { body: 'x' }, agent_id: 'ag_9' }, new Set(), 'browser').then((out) => {
    assert.equal(out.ok, false);
    if (!out.ok) assert.match(out.error, /tool_kind_mismatch/);
    assert.equal(h.seen.length, 0);
  });
});

test('a revoked agent cannot reach the author tools at all', async () => {
  const h = harness();
  const out = await dispatchHubInvoke(
    h.deps,
    { tool: 'author_read', args: {}, agent_id: 'ag_9' },
    new Set(['ag_9']),
    'desktop',
  );
  assert.equal(out.ok, false);
  assert.equal(h.seen.length, 0);
});

// ── author_render ───────────────────────────────────────────────────────────

test('author_render rides the same toggle and the same audit as author_read', async () => {
  assert.ok(AUTHOR_TOOL_NAMES.has('author_render'));
  assert.ok(DESKTOP_GATED_TOOL_NAMES.has('author_render'));
  // Audited on every leg — it discloses the user's document.
  assert.ok(DESKTOP_AUDITED_TOOL_NAMES.has('author_render'));
  // …and NOT an action. It is a read that answers in pixels: no card, and the
  // readOnlyHint stays true, which is the direction of that hint that cannot
  // cause harm (ADR-063 D5).
  assert.equal(DESKTOP_ACTION_TOOL_NAMES.has('author_render'), false);

  const off = harness({ sharing: false });
  const refused = await call(off.deps, 'author_render', {});
  assert.equal(refused.isError, true);
  assert.match(refused.content[0]?.text ?? '', /UI_UNAVAILABLE/);
  assert.equal(off.seen.length, 0);
});

test('author_render returns the caption first and the image second', async () => {
  const h = harness({ result: { ok: true, text: 'rendered “Flow” as svg', image: { base64: 'PHN2Zy8+', mimeType: 'image/svg+xml' } } });
  const out = await call(h.deps, 'author_render', { document_id: 'doc_f', format: 'svg' });
  assert.notEqual(out.isError, true);
  // An MCP image block carries no caption. A client that renders only the
  // leading block still learns which document this is a picture of.
  assert.equal(out.content[0]?.type, 'text');
  assert.match(out.content[0]?.text ?? '', /Flow/);
  assert.equal(out.content[1]?.type, 'image');
  assert.equal(out.content[1]?.data, 'PHN2Zy8+');
  assert.equal(out.content[1]?.mimeType, 'image/svg+xml');
  assert.equal(h.seen[0]?.op, 'render');
  assert.equal(h.seen[0]?.format, 'svg');
});

test('author_render defaults to svg and refuses an unknown format by name', async () => {
  const h = harness();
  await call(h.deps, 'author_render', {});
  assert.equal(h.seen[0]?.format, 'svg', 'an omitted format must not cost a raster image');

  const bad = await call(h.deps, 'author_render', { format: 'jpeg' });
  assert.equal(bad.isError, true);
  // Silently substituting png would teach the agent nothing, and the next call
  // asks for jpeg again.
  assert.match(bad.content[0]?.text ?? '', /format must be 'svg' or 'png'/);
  assert.equal(h.seen.length, 1);
});

test('author_render keeps readOnlyHint true, unlike author_apply', async () => {
  const h = harness();
  const res = await handleMcpMessage({ jsonrpc: '2.0', id: 1, method: 'tools/list' }, h.deps);
  const tools = (res?.result as { tools: Array<{ name: string; annotations: Record<string, unknown> }> }).tools;
  assert.equal(tools.find((t) => t.name === 'author_render')?.annotations.readOnlyHint, true);
  assert.equal(tunnelClassForTool('author_render'), 'desktop');
});

test('a provider that answers ok with no image is a refusal, not an empty picture', async () => {
  // A client renders an empty image block as a broken graphic and an agent
  // reads it as "the document is blank". This is the tool layer's half; the
  // host's half refuses the same shape.
  const h = harness({ result: { ok: true, text: 'rendered' } });
  const out = await call(h.deps, 'author_render', {});
  assert.notEqual(out.isError, true);
  assert.equal(out.content.length, 1, 'no image block is emitted without image bytes');
  assert.equal(out.content[0]?.type, 'text');
});

test('all three author tools are audited on the local leg', async () => {
  const h = harness();
  await call(h.deps, 'author_render', {});
  assert.deepEqual(h.audit.map((e) => e.tool), ['author_render']);
  assert.deepEqual(h.audit.map((e) => e.via), ['local']);
});
