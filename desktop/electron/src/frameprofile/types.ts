/// Frame-profile shapes — the TypeScript mirror of the Go structs in
/// `hub/internal/agentfamilies/families.go` (ADR-010).
///
/// These field names are the `json:` tags on those structs, not the `yaml:`
/// ones, because a profile reaches this side as JSON — either marshalled from
/// the hub's parsed YAML or served by it. The two tag sets agree today; the
/// json ones are what actually crosses the wire, so those are what is mirrored
/// here. A field added to `families.go` and not added here is silently dropped
/// by the interpreter, which is why the parity fixture carries the whole
/// profile rather than a hand-copied subset: an unmirrored field changes the
/// emitted events, and that fails.

/// One `(match → emit)` translation rule.
export interface Rule {
  /// Literal-equality predicate over the frame. Keys default to top-level
  /// fields; a dotted key (`params.item.type`) walks nested objects. Empty or
  /// absent matches any frame, at the lowest possible specificity.
  match?: Record<string, unknown>;
  /// Expression yielding an array to iterate. Each element becomes the inner
  /// scope with the current frame as the outer scope (`$$.`).
  for_each?: string;
  /// Gates the emit on a non-null expression result.
  when_present?: string;
  emit: Emit;
  /// Fire once each against the inner scope during a `for_each`. Only
  /// meaningful when `for_each` is set.
  sub_rules?: Rule[];
}

/// The agent_event row a rule produces.
export interface Emit {
  kind: string;
  /// Defaults to `agent` when absent.
  producer?: string;
  /// Per-field expression map.
  payload?: Record<string, string>;
  /// Whole-payload passthrough. Mutually exclusive with the three above; when
  /// both are set this one wins.
  payload_expr?: string;
  /// Payload fields whose value is a map keyed by data, re-shaped per value.
  payload_maps?: Record<string, MapProjection>;
  /// Payload fields whose value is a list, re-shaped per element.
  payload_lists?: Record<string, ListProjection>;
}

/// Re-shapes every VALUE of a source map, preserving the source's keys.
export interface MapProjection {
  source: string;
  fields?: Record<string, string>;
}

/// Re-shapes every ELEMENT of a source array, preserving order.
export interface ListProjection {
  source: string;
  fields?: Record<string, string>;
}

export interface FrameProfile {
  description?: string;
  profile_version?: number;
  rules?: Rule[];
}

/// A translated agent_event: the `(kind, producer, payload)` tuple the hub's
/// `EmittedEvent` carries. Timestamps and sequence numbers are the caller's
/// job on both sides.
export interface EmittedEvent {
  kind: string;
  producer: string;
  payload: Record<string, unknown>;
}

/// The scope an expression resolves against — one decoded JSON frame, or the
/// element of one during a `for_each`. `null` is a legal scope: every path
/// into it resolves to null, which is how `$$.` behaves outside a `for_each`.
export type Scope = Record<string, unknown> | null;
