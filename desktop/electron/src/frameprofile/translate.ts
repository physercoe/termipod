/// Profile-driven frame translation (ADR-010) — the TypeScript twin of
/// `hub/internal/hostrunner/profile_translate.go`.
///
/// `applyProfile` takes one decoded stream-json frame and returns zero or more
/// agent_events, driven by a declarative profile instead of hand-written field
/// paths. It is pure and side-effect-free: no I/O, no clock, no ids. Whatever a
/// driver has to add on top — a turn clock, a chain root, a throttle — is a
/// driver-side supplement by definition, because this function cannot express
/// it (see `supplements.ts`, and D-7 in the vision-parity plan).
///
/// The Go and TS interpreters are pinned to each other by a generated fixture:
/// every frame in the hub's corpus, run through both, compared event-for-event
/// (`parity.test.ts`). Parity by construction, not by discipline.

import { evalExpr } from './eval.ts';
import type {
  Emit,
  EmittedEvent,
  FrameProfile,
  ListProjection,
  MapProjection,
  Rule,
  Scope,
} from './types.ts';

/// Evaluate `profile` against `frame` and return the emitted events.
///
/// Dispatch is **most-specific match wins**: among the rules whose match
/// predicate is satisfied, only those tied for the largest match-keyset fire,
/// in declaration order. That lets a profile stack rules for hierarchical
/// dispatch (`{type, subtype}` beats `{type}` beats `{}`) and also run several
/// rules with the *same* match — an assistant frame fires both its `for_each`
/// over content blocks and its `when_present`-gated usage rule.
///
/// A null profile or zero matching rules yields the `kind=raw` fallback (D5).
/// A rule that matches but produces nothing — gated by `when_present`, or a
/// `for_each` over an absent array — is NOT the fallback case: the author chose
/// to skip, and that choice is respected.
export function applyProfile(
  frame: Record<string, unknown>,
  profile: FrameProfile | null | undefined,
): EmittedEvent[] {
  const rules = profile?.rules;
  if (!rules || rules.length === 0) return [rawFallback(frame)];

  let winners: Rule[] = [];
  let bestSize = -1;
  for (const rule of rules) {
    if (!matchesAll(rule.match, frame)) continue;
    const size = rule.match ? Object.keys(rule.match).length : 0;
    if (size > bestSize) {
      bestSize = size;
      winners = [rule];
    } else if (size === bestSize) {
      winners.push(rule);
    }
  }
  if (bestSize < 0) return [rawFallback(frame)];

  const out: EmittedEvent[] = [];
  for (const rule of winners) out.push(...applyRule(rule, frame, null));
  return out;
}

/// Fire one rule against the given (inner, outer) scope. Four shapes:
///
///   - `when_present` set and null      → no-op, the rule is gated
///   - `for_each` set                   → iterate the resolved array, running
///     each sub-rule against each element with the element as inner and the
///     current frame as outer. With no sub-rules the rule's own emit fires per
///     element.
///   - no emit kind                     → no-op
///   - otherwise                        → a single emit at the rule's level
function applyRule(rule: Rule, inner: Scope, outer: Scope): EmittedEvent[] {
  if (rule.when_present) {
    if (evalExpr(rule.when_present, inner, outer) === null) return [];
  }

  if (rule.for_each) {
    const raw = evalExpr(rule.for_each, inner, outer);
    if (!Array.isArray(raw)) return [];
    const out: EmittedEvent[] = [];
    // `inner` becomes the outer scope for each iteration; the element becomes
    // the new inner. Sub-rules see `$.foo` against the element and `$$.bar`
    // against the parent frame.
    for (const item of raw) {
      const itemMap = asMap(item);
      if (itemMap === null) continue;
      if (!rule.sub_rules || rule.sub_rules.length === 0) {
        if (rule.emit?.kind) out.push(buildEmit(rule.emit, itemMap, inner));
        continue;
      }
      for (const sub of rule.sub_rules) {
        if (!matchesAll(sub.match, itemMap)) continue;
        out.push(...applyRule(sub, itemMap, inner));
        break; // first sub-rule match wins per element
      }
    }
    return out;
  }

  if (!rule.emit?.kind) return [];
  return [buildEmit(rule.emit, inner, outer)];
}

/// True when every key in `match` literal-equals the corresponding field of
/// `frame`. An absent or empty match matches any frame.
///
/// Match keys default to top-level fields; a dotted key (`params.item.type`)
/// walks nested objects, which is how a JSON-RPC envelope dispatches on a
/// discriminator one level down from its method.
///
/// Comparison is `===`, so a match value that is an object or array never
/// matches. Every match value across the shipped profiles is a string, and a
/// Go-side test asserts that stays true — because Go compares two `any`s here,
/// where a numeric matcher would hit its int-vs-float64 rule and an object one
/// would panic. Keeping matchers to strings is what makes the two agree.
function matchesAll(
  match: Record<string, unknown> | undefined,
  frame: Record<string, unknown>,
): boolean {
  if (!match) return true;
  const keys = Object.keys(match);
  if (keys.length === 0) return true;
  for (const k of keys) {
    let got: unknown;
    if (!k.includes('.')) {
      if (!Object.prototype.hasOwnProperty.call(frame, k)) return false;
      got = frame[k];
    } else {
      got = walkPathLiteral(frame, k);
      if (got === null) return false;
    }
    if (got !== match[k]) return false;
  }
  return true;
}

/// Resolve a dotted match key as a sequence of map lookups. No `$.` prefix:
/// match keys are bare paths, not expressions, so this deliberately does not
/// route through the expression evaluator.
function walkPathLiteral(frame: Record<string, unknown>, path: string): unknown {
  let cur: unknown = frame;
  for (const seg of path.split('.')) {
    const m = asMap(cur);
    if (m === null) return null;
    if (!Object.prototype.hasOwnProperty.call(m, seg)) return null;
    cur = m[seg];
  }
  return cur === undefined ? null : cur;
}

/// Resolve a rule's emit into a concrete event. Producer defaults to `agent`.
///
/// Payload shape:
///   - `payload_expr` present → the expression resolves to the entire payload.
///     A non-object result yields `{}` defensively rather than throwing, and
///     surfaces as a parity finding.
///   - otherwise → the projections first, then the plain per-field map, so a
///     plain field wins a name collision. It is the simpler declaration, and a
///     profile declaring both for one field has a bug that shouldn't be
///     resolved in the projection's favour.
function buildEmit(emit: Emit, inner: Scope, outer: Scope): EmittedEvent {
  const producer = emit.producer && emit.producer !== '' ? emit.producer : 'agent';
  let payload: Record<string, unknown>;

  if (emit.payload_expr) {
    const v = evalExpr(emit.payload_expr, inner, outer);
    payload = asMap(v) ?? {};
  } else {
    payload = {};
    for (const [name, proj] of Object.entries(emit.payload_maps ?? {})) {
      const v = projectMap(proj, inner, outer);
      if (v !== null) payload[name] = v;
    }
    for (const [name, proj] of Object.entries(emit.payload_lists ?? {})) {
      const v = projectList(proj, inner, outer);
      if (v !== null) payload[name] = v;
    }
    for (const [k, expr] of Object.entries(emit.payload ?? {})) {
      payload[k] = evalExpr(expr, inner, outer);
    }
  }

  return { kind: emit.kind, producer, payload };
}

/// Walk a source map, re-shape each value through the projection's field
/// expressions, keep the source's keys.
///
/// Returns null when the source is absent or isn't an object, so the caller
/// omits the field entirely rather than emitting `{}` — "no per-model data" and
/// "data for zero models" are different claims. Non-object values are skipped:
/// a projection declares the shape it produces, and a value that can't take
/// that shape has no honest representation in it.
function projectMap(
  proj: MapProjection,
  inner: Scope,
  outer: Scope,
): Record<string, unknown> | null {
  const src = asMap(evalExpr(proj.source, inner, outer));
  if (src === null) return null;
  const out: Record<string, unknown> = {};
  for (const [key, raw] of Object.entries(src)) {
    const val = asMap(raw);
    if (val === null) continue;
    const fields: Record<string, unknown> = {};
    for (const [f, expr] of Object.entries(proj.fields ?? {})) {
      fields[f] = evalExpr(expr, val, inner);
    }
    out[key] = fields;
  }
  return out;
}

/// Walk a source array in order, re-shape each element, return one list.
///
/// Returns null when the source is absent or isn't an array — the same
/// absent-vs-empty line `projectMap` draws. An **empty** source array IS
/// projected, to an empty list: "the agent published a plan with no steps" is a
/// claim the engine made, unlike a missing field.
function projectList(proj: ListProjection, inner: Scope, outer: Scope): unknown[] | null {
  const src = evalExpr(proj.source, inner, outer);
  if (!Array.isArray(src)) return null;
  const out: unknown[] = [];
  for (const raw of src) {
    const elem = asMap(raw);
    if (elem === null) continue;
    const fields: Record<string, unknown> = {};
    for (const [f, expr] of Object.entries(proj.fields ?? {})) {
      fields[f] = evalExpr(expr, elem, inner);
    }
    out.push(fields);
  }
  return out;
}

/// The no-rule-matched event. ADR-010 D5: profiles aren't required to declare a
/// catch-all, because operators want forward-compatibility with unprofiled SDK
/// frame types — the transcript keeps the bytes for later profiling.
function rawFallback(frame: Record<string, unknown>): EmittedEvent {
  return { kind: 'raw', producer: 'agent', payload: frame };
}

function asMap(v: unknown): Record<string, unknown> | null {
  if (v === null || typeof v !== 'object' || Array.isArray(v)) return null;
  return v as Record<string, unknown>;
}
