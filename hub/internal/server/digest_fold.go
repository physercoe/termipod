package server

import (
	"fmt"
	"strings"
	"time"
)

// Per-run event digest computation (ADR-038 §1-§3). The hub (Go) is the
// source of truth for the canonical run summary; /v1/insights and the mobile
// transcript lens reconcile to it.
//
// One stateful `digestFolder` drives both consumers so they cannot diverge:
//   - brute force (computeAgentDigest) feeds every event in order — used by
//     the one-time lazy backfill and by the shared test vector;
//   - incremental (handlers_agent_events POST) reconstructs the folder from
//     the persisted digest row + the open turn row, steps ONE event, and
//     persists the deltas in the same transaction as the agent_events insert.
//
// Because both paths call the identical `step`, the incremental digest equals
// a brute-force scan at every watermark by construction.

const (
	// Bumped to 2 in the lens-as-query work (ADR-039): errors now keep a much
	// larger seq list (maxDigestErrorSeqs) so the mobile Errors lens can render
	// the *whole-run* error list, not a 25-cap sample. The bump makes
	// ensureAgentDigest refold already-sealed digests so they pick up the
	// fuller list (see digestIsStale's caller).
	//
	// v3 adds per-error SampleLabels (the failing tool's name) so the mobile
	// Errors lens can headline each row with the tool ("Bash") instead of the
	// generic class ("Tool error"); the bump refolds sealed digests to fill it.
	//
	// v4 widens SampleLabels coverage: when a failing tool_result /
	// tool_call_update id doesn't resolve to a recorded tool_call (engines vary
	// in which id field they carry), errorSampleLabel falls back to the tool
	// name on the failing event itself (toolNameFromPayload). The bump refolds
	// sealed digests so previously-unlabelled tool errors gain their headline.
	//
	// v5 adds per-error SampleOrdinals + per-turn StartOrdinal (ADR-042): the
	// session-unique session_ordinal anchor alongside the per-agent seq, so the
	// Insight Navigator lands on the right row after a resume (seq collides
	// across the session's agents; the ordinal does not). The bump refolds
	// sealed digests so their turn/error anchors gain the ordinal.
	digestSchemaVersion = 5
	// Cap the per-tool sample-seq lists so a pathological run can't bloat the
	// JSON blob. Tool samples are navigation anchors, not a complete index
	// (agent_turns + the kind-filtered listing are that).
	maxDigestSampleSeqs = 25
	// Errors get a far larger cap: they are the analysis-critical lens and are
	// bounded in practice (a run has dozens, rarely hundreds, of errors), so we
	// keep the whole list to back the Insight Errors lens as a complete,
	// navigable list (ADR-039). Still capped so a degenerate error-storm run
	// can't unbound the errors_json blob; ~200 ints + ts strings/class is a few
	// KiB. error_count remains the exact whole-run total regardless of the cap.
	maxDigestErrorSeqs = 200
)

// latencyBoundsMs are the fixed log-scale (≈×2.5, OTel-exponential-aligned)
// upper bounds for the turn-latency histogram, in milliseconds. Fixed bounds
// make the per-agent histograms mergeable by summing bucket counts (ADR-038
// §5). `counts` carries len(bounds)+1 buckets — the trailing one is the
// > last-bound overflow. The exact boundaries are a tuning detail (ADR-038
// open questions); the shape (fixed log-scale) is settled.
var latencyBoundsMs = []int64{100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000, 120000, 300000}

// foldEvent is the minimal projection of an agent_event the digest folds.
type foldEvent struct {
	Seq int64
	// Ordinal is the event's session_ordinal (ADR-042) — its dense,
	// session-unique position. 0 when the event has no session. Recorded
	// alongside Seq on turn/error anchors so the session-scoped Insight surface
	// can land across a resume boundary, where Seq collides.
	Ordinal  int64
	Kind     string
	TS       string
	Producer string
	// SessionID is the event's session (denormalized onto the turn it opens so
	// the OTLP watermark stays a single-store read post-split — ADR-045 step 4).
	// "" for a session-less agent.
	SessionID string
	Payload   map[string]any
}

type byModelAgg struct {
	In          int64   `json:"in"`
	Out         int64   `json:"out"`
	CacheRead   int64   `json:"cache_read"`
	CacheCreate int64   `json:"cache_create"`
	CostUSD     float64 `json:"cost_usd,omitempty"`
}

type errorClassAgg struct {
	Count      int64   `json:"count"`
	SampleSeqs []int64 `json:"sample_seqs"`
	// SampleTSs is aligned 1:1 with SampleSeqs — the timestamp of each sampled
	// error event. Lets the mobile analysis surface jump to an error via the
	// (ts, seq) random-access reset instead of the bounded page-walk (turns
	// already carry start_ts; this closes the same gap for errors). Older
	// digests folded before this field stay seq-only and degrade to page-walk.
	SampleTSs []string `json:"sample_ts,omitempty"`
	// SampleOrdinals is aligned 1:1 with SampleSeqs — the session_ordinal
	// (ADR-042) of each sampled error. The session-unique anchor the Insight
	// Navigator lands on, so an error jump resolves the right row even after a
	// resume (seq collides across the session's agents). Older (pre-v5) digests
	// fold without it and degrade to the seq/ts anchor.
	SampleOrdinals []int64 `json:"sample_ordinals,omitempty"`
	// SampleLabels is aligned 1:1 with SampleSeqs — a short headline for each
	// sampled error. For a tool failure it is the failing tool's resolved name
	// ("Bash", "Edit"); for an `error:<type>` it is the type; for a failed turn
	// it is "" (the class label carries the meaning). Lets the mobile Errors
	// lens distinguish rows that would otherwise all read "Tool error". Older
	// (pre-v3) digests fold without it and degrade to the class label.
	SampleLabels []string `json:"sample_labels,omitempty"`
}

type toolAgg struct {
	Calls      int64   `json:"calls"`
	Failed     int64   `json:"failed"`
	SampleSeqs []int64 `json:"sample_seqs"`
}

type latencyHist struct {
	Bounds []int64 `json:"bounds"`
	Counts []int64 `json:"counts"`
}

func newLatencyHist() latencyHist {
	return latencyHist{
		Bounds: append([]int64(nil), latencyBoundsMs...),
		Counts: make([]int64, len(latencyBoundsMs)+1),
	}
}

// add records one duration (ms) into its bucket.
func (h *latencyHist) add(durMs int64) {
	if len(h.Counts) != len(h.Bounds)+1 {
		*h = newLatencyHist()
	}
	for i, b := range h.Bounds {
		if durMs <= b {
			h.Counts[i]++
			return
		}
	}
	h.Counts[len(h.Counts)-1]++
}

// agentDigest is the in-memory shape of one agent_event_digests row.
type agentDigest struct {
	AgentID       string
	TeamID        string
	SchemaVersion int
	WatermarkSeq  int64
	EventCount    int64
	TurnCount     int64
	FirstTS       string
	LastTS        string
	DurationMs    int64
	CostUSD       float64
	ByModel       map[string]*byModelAgg
	ErrorCount    int64
	Errors        map[string]*errorClassAgg
	ToolTotal     int64
	ToolFailed    int64
	Tools         map[string]*toolAgg
	Latency       latencyHist
	Outcome       string
}

func newAgentDigest(agentID, teamID string) *agentDigest {
	return &agentDigest{
		AgentID:       agentID,
		TeamID:        teamID,
		SchemaVersion: digestSchemaVersion,
		ByModel:       map[string]*byModelAgg{},
		Errors:        map[string]*errorClassAgg{},
		Tools:         map[string]*toolAgg{},
		Latency:       newLatencyHist(),
	}
}

// turnRow is the in-memory shape of one agent_turns row.
type turnRow struct {
	TurnID   string
	Idx      int
	StartSeq int64
	// StartOrdinal is the turn's start event session_ordinal (ADR-042) — the
	// session-unique anchor the Insight Navigator lands on. 0 for a session-less
	// agent or a pre-v5 digest.
	StartOrdinal int64
	StartTS      string
	// SessionID is the session of the turn's start event (ADR-045 step 4) — the
	// denormalized anchor the OTLP export groups by. "" for a session-less agent.
	SessionID  string
	EndSeq     int64
	EndTS      string
	DurationMs int64
	Status     string
	CostUSD    float64
	InTokens   int64
	OutTokens  int64
	ToolCount  int64
	ToolFailed int64
	ErrorCount int64
}

// toolNameResolver maps a tool-call id to its tool name. The brute-force
// folder resolves from an in-memory map of calls seen this run; the
// incremental folder resolves with a bounded DB lookup (rare — only on a
// tool failure). Both yield the same name, so the digests agree.
type toolNameResolver func(id string) string

// digestFolder is the shared state machine. For brute force it holds the
// whole run; for incremental it is reconstructed from the persisted row +
// open turn and stepped once.
type digestFolder struct {
	digest   *agentDigest
	open     *turnRow // the currently-open turn, nil between turns
	nextIdx  int      // idx to assign the next opened turn
	closed   []turnRow
	lastTS   string
	resolve  toolNameResolver
	callName map[string]string // brute-force id→name; nil for incremental
}

func newDigestFolder(d *agentDigest) *digestFolder {
	f := &digestFolder{digest: d, callName: map[string]string{}}
	f.resolve = func(id string) string {
		if n, ok := f.callName[id]; ok {
			return n
		}
		return ""
	}
	return f
}

// computeAgentDigest folds an ordered event slice into a digest + the full
// turn list (brute force). Used by the lazy backfill and the shared test
// vector.
func computeAgentDigest(agentID, teamID string, events []foldEvent) (*agentDigest, []turnRow) {
	f := newDigestFolder(newAgentDigest(agentID, teamID))
	for _, e := range events {
		f.step(e)
	}
	turns := append([]turnRow(nil), f.closed...)
	if f.open != nil {
		turns = append(turns, *f.open)
	}
	return f.digest, turns
}

// step folds exactly one event. The body is the single source of truth for
// every digest and turn field.
func (f *digestFolder) step(e foldEvent) {
	d := f.digest
	d.EventCount++
	d.WatermarkSeq = e.Seq
	if d.FirstTS == "" {
		d.FirstTS = e.TS
	}
	d.LastTS = e.TS
	d.DurationMs = tsDeltaMs(d.FirstTS, d.LastTS)
	f.lastTS = e.TS

	// Turn boundary handling. turn.start opens explicitly; any event with no
	// open turn opens a synthetic one (covers engines not yet emitting
	// turn.start — ADR-038 §3). turn.result closes the open turn.
	if e.Kind == "turn.start" {
		if f.open != nil {
			// The hub inserts the user's input.text (which opens a *synthetic*
			// turn) before the input router dispatches the prompt and the
			// driver emits turn.start — so the open turn here is normally the
			// synthetic one this turn.start belongs to. Adopt it (assign the
			// real turn_id, keep start_seq at the prompt) rather than
			// close+reopen, which would leave a spurious empty 1-event turn in
			// the index. Only a *real* already-open turn (a turn.start with no
			// intervening turn.result — genuinely unusual) is closed first.
			if isSyntheticTurnID(f.open.TurnID) {
				if id := turnIDOf(e); id != "" {
					f.open.TurnID = id
				}
				return
			}
			f.closeTurn("", e.TS) // unusual: a start with no prior result
		}
		f.openTurn(turnIDOf(e), e.Seq, e.Ordinal, e.TS, e.SessionID)
		return
	}
	if f.open == nil && e.Kind != "turn.result" {
		f.openTurn(turnIDOf(e), e.Seq, e.Ordinal, e.TS, e.SessionID)
	}

	// Per-event accumulation onto both the digest and the open turn.
	switch e.Kind {
	case "usage":
		// Usage events are claude's per-assistant-message increments. They
		// feed only the OPEN turn's running token estimate so a long
		// single-turn "goal mode" run stays fresh (ADR-038 §2); the
		// authoritative per-model + per-turn totals come from
		// turn.result.by_model at close (below), so the digest's by_model is
		// never double-counted across usage + turn.result.
		if f.open != nil {
			f.open.InTokens += readNumber(e.Payload, "input_tokens")
			f.open.OutTokens += readNumber(e.Payload, "output_tokens")
		}
	case "tool_call":
		d.ToolTotal++
		name := stringOf(e.Payload["name"])
		if name == "" {
			name = "unknown"
		}
		if id := eventToolID(e.Kind, e.Payload); id != "" && f.callName != nil {
			f.callName[id] = name
		}
		t := f.tool(name)
		t.Calls++
		addSample(&t.SampleSeqs, e.Seq)
		if f.open != nil {
			f.open.ToolCount++
		}
	case "turn.result":
		// Fold cost / by_model / tokens, then close the turn.
		cost := readFloat(e.Payload, "cost_usd")
		d.CostUSD += cost
		var turnIn, turnOut int64
		if bm, ok := e.Payload["by_model"].(map[string]any); ok {
			for model, raw := range bm {
				entry, _ := raw.(map[string]any)
				if entry == nil {
					continue
				}
				mIn := readNumber(entry, "input")
				mOut := readNumber(entry, "output")
				mCr := readNumber(entry, "cache_read")
				mCc := readNumber(entry, "cache_create")
				mCost := readFloat(entry, "cost_usd")
				f.addModel(model, mIn, mOut, mCr, mCc, mCost)
				turnIn += mIn
				turnOut += mOut
			}
		}
		d.TurnCount++
		status := turnResultStatus(e.Payload)
		if f.open != nil {
			if turnIn > 0 {
				f.open.InTokens = turnIn
			}
			if turnOut > 0 {
				f.open.OutTokens = turnOut
			}
			f.open.CostUSD += cost
			f.open.Status = status
		}
		// Canonical error: a failed turn counts once.
		if class, ok := canonicalErrorClass(e); ok {
			f.recordError(class, e.Seq, e.Ordinal, e.TS, f.errorSampleLabel(e))
		}
		f.closeTurn(status, e.TS)
		return
	}

	// Canonical-error classification for non-turn events (the open turn is
	// guaranteed non-nil here because we opened one above).
	if class, ok := canonicalErrorClass(e); ok {
		f.recordError(class, e.Seq, e.Ordinal, e.TS, f.errorSampleLabel(e))
		if isToolFailure(e) {
			d.ToolFailed++
			if f.open != nil {
				f.open.ToolFailed++
			}
			if id := eventToolID(e.Kind, e.Payload); id != "" {
				if name := f.resolve(id); name != "" {
					t := f.tool(name)
					t.Failed++
					addSample(&t.SampleSeqs, e.Seq)
				}
			}
		}
	}
}

func (f *digestFolder) openTurn(turnID string, seq, ordinal int64, ts, sessionID string) {
	if turnID == "" {
		turnID = fmt.Sprintf("syn-%d", seq)
	}
	f.open = &turnRow{
		TurnID:       turnID,
		Idx:          f.nextIdx,
		StartSeq:     seq,
		StartOrdinal: ordinal,
		StartTS:      ts,
		SessionID:    sessionID,
	}
	f.nextIdx++
}

func (f *digestFolder) closeTurn(status, endTS string) {
	if f.open == nil {
		return
	}
	f.open.EndSeq = f.digest.WatermarkSeq
	f.open.EndTS = endTS
	if status != "" {
		f.open.Status = status
	}
	dur := tsDeltaMs(f.open.StartTS, endTS)
	f.open.DurationMs = dur
	if dur >= 0 {
		f.digest.Latency.add(dur)
	}
	// f.open.ErrorCount has been accumulated by recordError across the turn
	// (every canonical error, including the failed-turn status and tool
	// failures, bumps it as it happens) — leave it as is.
	f.closed = append(f.closed, *f.open)
	f.open = nil
}

func (f *digestFolder) addModel(model string, in, out, cr, cc int64, cost float64) {
	m := f.digest.ByModel[model]
	if m == nil {
		m = &byModelAgg{}
		f.digest.ByModel[model] = m
	}
	m.In += in
	m.Out += out
	m.CacheRead += cr
	m.CacheCreate += cc
	m.CostUSD += cost
}

func (f *digestFolder) tool(name string) *toolAgg {
	t := f.digest.Tools[name]
	if t == nil {
		t = &toolAgg{}
		f.digest.Tools[name] = t
	}
	return t
}

func (f *digestFolder) recordError(class string, seq, ordinal int64, ts, label string) {
	f.digest.ErrorCount++
	if f.open != nil {
		// Every canonical error during the turn (including its failed-turn
		// status and any tool failures) is tallied on the open turn as it
		// happens; closeTurn leaves the total untouched.
		f.open.ErrorCount++
	}
	c := f.digest.Errors[class]
	if c == nil {
		c = &errorClassAgg{}
		f.digest.Errors[class] = c
	}
	c.Count++
	addSampleTS(&c.SampleSeqs, &c.SampleOrdinals, &c.SampleTSs, &c.SampleLabels, seq, ordinal, ts, label)
}

// errorSampleLabel derives the per-sample headline for an error event: the
// failing tool's resolved name for a tool failure, the error type for an
// `error:<type>`, else "". Uses f.resolve so brute-force (in-memory map) and
// incremental (DB lookup) agree — keeping incremental == brute (ADR-038). When
// the tool-call lookup misses (engines vary in which id field a tool_result /
// tool_call_update carries, so resolve can come up empty), it falls back to a
// tool name carried on the failing event itself — so the Errors outline still
// headlines with the tool, not the generic class.
func (f *digestFolder) errorSampleLabel(e foldEvent) string {
	if id := eventToolID(e.Kind, e.Payload); id != "" {
		if name := f.resolve(id); name != "" {
			return name
		}
		if name := toolNameFromPayload(e.Payload); name != "" {
			return name
		}
	}
	if e.Kind == "error" {
		return stringOf(e.Payload["type"])
	}
	return ""
}

// --- shared classification predicates -------------------------------------

// canonicalErrorSQLPredicate is the SQL form of canonicalErrorClass — the
// SAME per-event union (kind='error' ∪ tool_result.is_error ∪ failed
// tool_call_update ∪ failed turn.result), so a time-windowed /v1/insights
// count reconciles with the digest's error_count (which folds the Go form).
// Operates on the `kind` and `payload_json` columns of agent_events; keep it
// pinned to canonicalErrorClass.
const canonicalErrorSQLPredicate = `(
	kind = 'error'
	OR (kind = 'tool_result' AND json_extract(payload_json, '$.is_error') = 1)
	OR (kind = 'tool_call_update' AND json_extract(payload_json, '$.status') IN ('failed','error'))
	OR (kind = 'turn.result' AND COALESCE(json_extract(payload_json, '$.status'), 'success') <> 'success')
)`

// canonicalErrorClass classifies an event into the canonical error taxonomy,
// counting each failure-signal event once (ADR-038 §1). The union is:
// kind=='error' ∪ tool_result.is_error ∪ tool_call_update failed ∪
// turn.result.status != 'success'. No cross-event dedup: in practice an
// engine emits either a failed tool_result OR a failed tool_call_update for a
// given call, not both, so per-event counting matches the director's log.
func canonicalErrorClass(e foldEvent) (string, bool) {
	switch e.Kind {
	case "error":
		if t := stringOf(e.Payload["type"]); t != "" {
			return "error:" + t, true
		}
		return "error", true
	case "tool_result":
		if boolOf(e.Payload["is_error"]) {
			return "tool_error", true
		}
	case "tool_call_update":
		if toolUpdateFailed(e.Payload) {
			return "tool_error", true
		}
	case "turn.result":
		if isFailedTurn(e.Payload) {
			return "failed_turn", true
		}
	}
	return "", false
}

// isToolFailure reports whether an event is a per-tool failure signal (so the
// tool_failed scalar + per-tool breakdown advance).
func isToolFailure(e foldEvent) bool {
	switch e.Kind {
	case "tool_result":
		return boolOf(e.Payload["is_error"])
	case "tool_call_update":
		return toolUpdateFailed(e.Payload)
	}
	return false
}

func toolUpdateFailed(p map[string]any) bool {
	st := stringOf(p["status"])
	return st == "failed" || st == "error"
}

// turnResultStatus mirrors readInsightsErrors: absent status reads as
// 'success' (claude turn.result carries terminal_reason, not status).
func turnResultStatus(p map[string]any) string {
	if s := stringOf(p["status"]); s != "" {
		return s
	}
	return "success"
}

func isFailedTurn(p map[string]any) bool {
	return turnResultStatus(p) != "success"
}

// eventToolID extracts the tool-call id an event refers to. tool_call uses
// `id`; tool_result uses `tool_use_id`; tool_call_update uses `toolCallId`.
func eventToolID(kind string, p map[string]any) string {
	switch kind {
	case "tool_call":
		if id := stringOf(p["id"]); id != "" {
			return id
		}
		return stringOf(p["toolCallId"])
	case "tool_result":
		if id := stringOf(p["tool_use_id"]); id != "" {
			return id
		}
		if id := stringOf(p["toolCallId"]); id != "" {
			return id
		}
		return stringOf(p["id"])
	case "tool_call_update":
		if id := stringOf(p["toolCallId"]); id != "" {
			return id
		}
		return stringOf(p["id"])
	}
	return ""
}

// toolNameFromPayload reads a tool name carried directly on an event payload,
// across the field spellings engines use (`name` for claude/native, `title` /
// `toolName` for ACP tool_call_update). Used as the errorSampleLabel fallback
// when the tool-call id lookup misses, so a failed tool_result / tool_call_update
// can still headline with its tool.
func toolNameFromPayload(p map[string]any) string {
	for _, k := range []string{"name", "tool_name", "toolName", "title"} {
		if n := stringOf(p[k]); n != "" {
			return n
		}
	}
	return ""
}

func turnIDOf(e foldEvent) string {
	if id := stringOf(e.Payload["turn_id"]); id != "" {
		return id
	}
	return ""
}

// isSyntheticTurnID reports whether a turn id was minted by openTurn's fallback
// (no engine-supplied turn_id) rather than carried by an explicit turn.start.
// Used so a later turn.start can adopt the synthetic turn the hub-injected
// input.text opened. Kept in lockstep with openTurn's "syn-%d" format.
func isSyntheticTurnID(id string) bool {
	return strings.HasPrefix(id, "syn-")
}

// --- small value helpers ---------------------------------------------------

func stringOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}

func readFloat(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	}
	return 0
}

func addSample(dst *[]int64, seq int64) {
	if len(*dst) >= maxDigestSampleSeqs {
		return
	}
	*dst = append(*dst, seq)
}

// addSampleTS appends a (seq, ordinal, ts, label) sample keeping the four
// slices aligned 1:1. Used only by the error path (recordError + the session
// error-class merge), so it caps at maxDigestErrorSeqs — the Errors lens wants
// the whole-run list, not a 25-cap sample. A missing ordinal/ts/label is
// appended as 0/"" so the indices never drift.
func addSampleTS(seqs, ords *[]int64, tss, labels *[]string, seq, ord int64, ts, label string) {
	if len(*seqs) >= maxDigestErrorSeqs {
		return
	}
	*seqs = append(*seqs, seq)
	*ords = append(*ords, ord)
	*tss = append(*tss, ts)
	*labels = append(*labels, label)
}

// tsDeltaMs returns end-start in milliseconds for two RFC3339 timestamps, or
// 0 if either is unparseable / negative.
func tsDeltaMs(start, end string) int64 {
	if start == "" || end == "" {
		return 0
	}
	st, err1 := time.Parse(time.RFC3339Nano, start)
	en, err2 := time.Parse(time.RFC3339Nano, end)
	if err1 != nil || err2 != nil {
		return 0
	}
	d := en.Sub(st).Milliseconds()
	if d < 0 {
		return 0
	}
	return d
}

// histogramPercentile estimates the q-th percentile (0..1) from merged
// histogram counts via linear interpolation within the containing bucket.
func histogramPercentile(h latencyHist, q float64) int64 {
	var total int64
	for _, c := range h.Counts {
		total += c
	}
	if total == 0 {
		return 0
	}
	target := q * float64(total)
	var cum int64
	for i, c := range h.Counts {
		prev := cum
		cum += c
		if float64(cum) < target {
			continue
		}
		// Bucket i contains the target rank. Interpolate between its lower
		// and upper bound.
		var lo, hi int64
		if i == 0 {
			lo = 0
		} else {
			lo = h.Bounds[i-1]
		}
		if i < len(h.Bounds) {
			hi = h.Bounds[i]
		} else {
			hi = h.Bounds[len(h.Bounds)-1] * 2 // overflow bucket: extrapolate
		}
		if c == 0 {
			return hi
		}
		frac := (target - float64(prev)) / float64(c)
		return lo + int64(frac*float64(hi-lo))
	}
	return h.Bounds[len(h.Bounds)-1]
}

// mergeLatencyHist sums b into a (both must share latencyBoundsMs).
func mergeLatencyHist(a *latencyHist, b latencyHist) {
	if len(a.Counts) != len(latencyBoundsMs)+1 {
		*a = newLatencyHist()
	}
	for i := range b.Counts {
		if i < len(a.Counts) {
			a.Counts[i] += b.Counts[i]
		}
	}
}
