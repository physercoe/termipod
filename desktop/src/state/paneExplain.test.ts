import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  matcherSummary,
  orderedRules,
  readPaneExplain,
  readPaneExplainError,
  type PaneRuleEvidence,
} from './paneExplain.ts';

/// A record shaped exactly as `panestate.ExplainResult` marshals it. Written
/// from the Go struct tags rather than from what the reader expects, so a
/// rename on either side shows up here as a failure rather than as a blank card.
const LIVE_RECORD = {
  mode: 'live',
  agent_id: 'ag-1',
  pane_id: '%7',
  host_id: 'host-1',
  family: 'codex',
  screen_bytes: 96,
  screen_lines: 3,
  osc_title: 'my project',
  explain: {
    manifest_id: 'codex',
    manifest_version: '1',
    source: 'vendor',
    state: 'blocked',
    matched_rule: { id: 'live_strong_blocker', priority: 980, region: 'whole_recent', state: 'blocked' },
    visible_blocker: true,
    rules: [
      {
        id: 'low_prio_idle',
        priority: 100,
        region: 'bottom_lines(3)',
        state: 'idle',
        matched: false,
        evidence: {
          contains: ['? for shortcuts'],
          region_bytes: 40,
          region_preview: '› 1. Yes, proceed',
        },
      },
      {
        id: 'live_strong_blocker',
        priority: 980,
        region: 'whole_recent',
        state: 'blocked',
        matched: true,
        evidence: {
          line_regex: ['^\\s*1\\.\\s*Yes'],
          any_count: 2,
          region_bytes: 96,
          region_preview: '• Working (4s • esc to interrupt)',
        },
      },
      {
        id: 'mid_prio_working',
        priority: 500,
        region: 'whole_recent',
        state: 'working',
        matched: true,
        evidence: { contains: ['working'], region_bytes: 96, region_preview: '• Working' },
      },
    ],
  },
};

test('readPaneExplain maps every field the card renders', () => {
  const v = readPaneExplain(LIVE_RECORD);
  assert.ok(v !== null);
  assert.equal(v.mode, 'live');
  assert.equal(v.agentId, 'ag-1');
  assert.equal(v.paneId, '%7');
  assert.equal(v.hostId, 'host-1');
  assert.equal(v.family, 'codex');
  assert.equal(v.screenBytes, 96);
  assert.equal(v.screenLines, 3);
  assert.equal(v.oscTitle, 'my project');
  assert.equal(v.manifestId, 'codex');
  assert.equal(v.manifestVersion, '1');
  assert.equal(v.source, 'vendor');
  assert.equal(v.state, 'blocked');
  assert.equal(v.matchedRuleId, 'live_strong_blocker');
  assert.equal(v.visibleBlocker, true);
  assert.equal(v.visibleIdle, false);
  assert.equal(v.rules.length, 3);

  const winner = v.rules.find((r) => r.id === 'live_strong_blocker');
  assert.ok(winner !== undefined);
  assert.equal(winner.matched, true);
  assert.equal(winner.priority, 980);
  assert.deepEqual(winner.evidence.lineRegex, ['^\\s*1\\.\\s*Yes']);
  assert.equal(winner.evidence.anyCount, 2);
  assert.equal(winner.evidence.regionPreview, '• Working (4s • esc to interrupt)');
});

test('an unmatched rule keeps its evidence — that is the whole point', () => {
  const v = readPaneExplain(LIVE_RECORD);
  const missed = v!.rules.find((r) => r.id === 'low_prio_idle');
  assert.ok(missed !== undefined);
  assert.equal(missed.matched, false);
  // Without the preview, "did not match" is a claim a reader cannot check.
  assert.equal(missed.evidence.regionPreview, '› 1. Yes, proceed');
  assert.deepEqual(missed.evidence.contains, ['? for shortcuts']);
});

test('mode never guesses: anything but "live" reads as supplied', () => {
  assert.equal(readPaneExplain({ ...LIVE_RECORD, mode: 'supplied' })!.mode, 'supplied');
  // A missing or unrecognised mode must not read as "live" — that would let a
  // hypothetical be presented as a fact about a running agent.
  assert.equal(readPaneExplain({ ...LIVE_RECORD, mode: '' })!.mode, 'supplied');
  assert.equal(readPaneExplain({ ...LIVE_RECORD, mode: 'LIVE' })!.mode, 'supplied');
});

test('an unrecognised state degrades to unknown rather than being printed raw', () => {
  const v = readPaneExplain({ ...LIVE_RECORD, explain: { ...LIVE_RECORD.explain, state: 'exploded' } });
  assert.equal(v!.state, 'unknown');
});

test('a body with no evaluation is not a view', () => {
  assert.equal(readPaneExplain(null), null);
  assert.equal(readPaneExplain({}), null);
  assert.equal(readPaneExplain('nope'), null);
  // The hub's refusal shape carries no `explain`, so it must not parse as one.
  assert.equal(readPaneExplain({ error: 'unmapped_family', family: 'kimi-code-ts' }), null);
});

test('readPaneExplainError picks out the refusal a caller can act on', () => {
  const e = readPaneExplainError({ error: 'unmapped_family', family: 'kimi-code-ts', detail: 'no manifest' });
  assert.deepEqual(e, { code: 'unmapped_family', family: 'kimi-code-ts', detail: 'no manifest' });
  assert.equal(readPaneExplainError(LIVE_RECORD), null);
});

test('orderedRules puts the winner first, then descending priority', () => {
  const v = readPaneExplain(LIVE_RECORD)!;
  assert.deepEqual(
    orderedRules(v).map((r) => r.id),
    ['live_strong_blocker', 'mid_prio_working', 'low_prio_idle'],
  );
});

test('the winner outranks a higher-priority rule that did not match', () => {
  // The case that separates "winner first" from "sort by priority": in
  // LIVE_RECORD the winner also has the top priority, so both rules produce
  // the same order and the hoist is invisible. Here a non-matching rule sits
  // above it — which is normal, since most rules do not match — and the answer
  // must still lead.
  const v = readPaneExplain({
    ...LIVE_RECORD,
    explain: {
      ...LIVE_RECORD.explain,
      rules: [
        ...LIVE_RECORD.explain.rules,
        { id: 'unmatched_top', priority: 9999, matched: false, evidence: {} },
      ],
    },
  })!;
  assert.deepEqual(orderedRules(v).map((r) => r.id)[0], 'live_strong_blocker');
  assert.deepEqual(
    orderedRules(v).map((r) => r.id),
    ['live_strong_blocker', 'unmatched_top', 'mid_prio_working', 'low_prio_idle'],
  );
});

test('orderedRules is stable, so equal priorities keep file order', () => {
  const v = readPaneExplain({
    ...LIVE_RECORD,
    explain: {
      ...LIVE_RECORD.explain,
      matched_rule: undefined,
      rules: [
        { id: 'first', priority: 500, matched: false, evidence: {} },
        { id: 'second', priority: 500, matched: false, evidence: {} },
        { id: 'third', priority: 500, matched: false, evidence: {} },
      ],
    },
  })!;
  // File order IS the manifest's tie-break; a list that reordered ties would
  // misrepresent why a rule lost.
  assert.deepEqual(orderedRules(v).map((r) => r.id), ['first', 'second', 'third']);
});

test('matcherSummary says what a rule wanted, including nested-only rules', () => {
  const base: PaneRuleEvidence = {
    contains: [], regex: [], lineRegex: [],
    allCount: 0, anyCount: 0, notCount: 0, regionBytes: 0, regionPreview: '',
  };
  assert.equal(matcherSummary({ ...base, contains: ['yes'] }), 'contains "yes"');
  assert.equal(matcherSummary({ ...base, regex: ['a.b'] }), 'regex /a.b/');
  assert.equal(matcherSummary({ ...base, lineRegex: ['^x'] }), 'line /^x/');
  // A rule whose logic is entirely nested has no matchers of its own; the
  // counts are what stop that from reading as "no conditions at all".
  assert.equal(matcherSummary({ ...base, allCount: 2, notCount: 1 }), 'all×2 not×1');
  assert.equal(matcherSummary(base), '');
});
