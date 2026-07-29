package server

import "sort"

// Structural session-integrity issues folded into the per-run digest
// (transcript P5 — docs/plans/transcript-insight-issues.md §2).
//
// The digest's Errors taxonomy (digest_fold.go canonicalErrorClass) counts
// *reported* failures: events an engine explicitly marked failed. This file
// adds the complementary half — failures nothing reported, visible only in the
// SHAPE of the event stream:
//
//   - a tool_call whose result never arrives (the `callToolIdOf` id-shape bug
//     shipped three times; every time the only symptom was a card spinning
//     "running" forever — an unpaired call the system itself never noticed);
//   - a tool_result matching no call;
//   - a turn still open when the run stops;
//   - a turn stopped for an abnormal reason the status field doesn't carry;
//   - a permission gate nobody answered;
//   - the same event kind carrying its tool id under two different key
//     spellings — the canary that makes the recurring bug self-reporting.
//
// Issues stay SEPARATE from Errors — no overlap, no double count — so the
// Errors lens/funnel semantics are untouched (plan §2). Clients merge the two
// lists visually; the digest keeps them apart.
//
// EVERY rule here reads only small structured keys (kind, tool ids, name,
// status, stop reason). None reads a display body — `foldEventCols`
// (digest_store.go) strips $.text/$.content/$.output/... server-side before a
// row reaches the brute-force fold, while the incremental fold sees the raw
// payload, so a rule that read a stripped field would make the two paths
// disagree. TestFoldStripsBodiesWithoutChangingDigest pins that invariant.

// Issue severities, worst first. Kept as strings on the wire so a client can
// render an unknown future severity without a schema bump.
const (
	issueSeverityError   = "error"
	issueSeverityWarning = "warning"
	issueSeverityInfo    = "info"
)

// Issue classes (plan §2). The string is the wire key both clients group by.
const (
	issueMissingToolResult    = "missing_tool_result"
	issueOrphanToolResult     = "orphan_tool_result"
	issueUnansweredPermission = "unanswered_permission"
	issueIncompleteTurn       = "incomplete_turn"
	issueAbnormalStop         = "abnormal_stop"
	issueMixedIDShape         = "mixed_id_shape"
)

// issueSeverities is the class → severity table. A class missing here folds as
// `warning`, so adding a class without updating the table degrades to the
// middle severity rather than dropping out of the worst-severity rollup.
var issueSeverities = map[string]string{
	issueMissingToolResult:    issueSeverityError,
	issueOrphanToolResult:     issueSeverityWarning,
	issueUnansweredPermission: issueSeverityWarning,
	issueIncompleteTurn:       issueSeverityWarning,
	issueAbnormalStop:         issueSeverityWarning,
	issueMixedIDShape:         issueSeverityInfo,
}

func issueSeverityOf(class string) string {
	if s, ok := issueSeverities[class]; ok {
		return s
	}
	return issueSeverityWarning
}

// issueSeverityRank orders severities for the `issue_worst_severity` rollup and
// the clients' severity-first grouping. Unknown → 0, so it never outranks a
// known severity.
var issueSeverityRank = map[string]int{
	issueSeverityError:   3,
	issueSeverityWarning: 2,
	issueSeverityInfo:    1,
}

// issueClassAgg mirrors errorClassAgg (count + 1:1-aligned sample slices) and
// adds the severity, so a client can group without hard-coding the class table.
type issueClassAgg struct {
	Count    int64  `json:"count"`
	Severity string `json:"severity"`
	// SampleSeqs / SampleOrdinals / SampleTSs / SampleLabels are aligned 1:1 —
	// the navigation anchor + headline for each sampled finding, exactly as the
	// Errors lens consumes them (addSampleTS keeps the four in step and caps
	// them at maxDigestErrorSeqs). The anchor points at the *triggering* event
	// — the tool_call that never resolved, the turn that never closed — not at
	// the sweep that noticed (the issue #64 convention).
	SampleSeqs     []int64  `json:"sample_seqs"`
	SampleOrdinals []int64  `json:"sample_ordinals,omitempty"`
	SampleTSs      []string `json:"sample_ts,omitempty"`
	SampleLabels   []string `json:"sample_labels,omitempty"`
}

// worstIssueSeverity returns the highest-ranked severity present, or "" when
// there are no issues — the rollup the client's stat chip tints itself with.
func worstIssueSeverity(issues map[string]*issueClassAgg) string {
	worst, rank := "", 0
	for class, agg := range issues {
		if agg == nil || agg.Count == 0 {
			continue
		}
		sev := agg.Severity
		if sev == "" {
			sev = issueSeverityOf(class)
		}
		if r := issueSeverityRank[sev]; r > rank {
			worst, rank = sev, r
		}
	}
	return worst
}

// maxPendingCalls bounds the tracked open-call set so a pathological run can't
// unbound fold_state_json. A run with more than this many *simultaneously*
// unresolved calls is already reporting missing results by the hundred, so the
// cap costs nothing a reader would act on. Both fold paths share this code, so
// the cap can never make them disagree.
const maxPendingCalls = 1000

// pendingCall is one tool_call awaiting its result — the fold's carry-over
// state for the missing-result sweep. Fields are the call's own navigation
// anchor, so the issue lands on the request, not on the sweep.
type pendingCall struct {
	Name    string `json:"name,omitempty"`
	Seq     int64  `json:"seq"`
	Ordinal int64  `json:"ordinal,omitempty"`
	TS      string `json:"ts,omitempty"`
	// Gate marks a permission/approval gate call, so an unanswered one reports
	// as `unanswered_permission` rather than a plumbing `missing_tool_result`.
	Gate bool `json:"gate,omitempty"`
}

// foldState is the folder's carry-over working state — the bits the incremental
// path must reconstruct EXACTLY to stay equal to a brute-force scan, but which
// are not themselves reportable metrics.
//
// It is persisted as `fold_state_json` on the digest row rather than recomputed
// from the event log, deliberately: the alternative (a set-difference SQL query
// mirroring these rules) would be a SECOND implementation of the pairing logic,
// and two implementations that must agree is precisely the bug class this wedge
// exists to catch. With the state persisted there is exactly one implementation
// — step() — and incremental == brute holds by construction (ADR-038).
type foldState struct {
	// Pending is id → the unresolved call. Swept (and cleared) at every turn
	// close and once more when the run is sealed.
	Pending map[string]*pendingCall `json:"pending,omitempty"`
	// IDShapes is event kind → the payload key spellings seen carrying a tool
	// id, in first-seen order. A kind reaching two spellings raises the
	// mixed_id_shape canary.
	IDShapes map[string][]string `json:"id_shapes,omitempty"`
	// Sealed records that the end-of-run findings have been folded, so the
	// terminal hook is idempotent and a refold reproduces it exactly once.
	Sealed bool `json:"sealed,omitempty"`
}

func newFoldState() *foldState {
	return &foldState{
		Pending:  map[string]*pendingCall{},
		IDShapes: map[string][]string{},
	}
}

// normalize fills the nil maps a JSON round-trip leaves behind (omitempty
// drops an empty map, and Unmarshal leaves an absent key nil).
func (st *foldState) normalize() {
	if st.Pending == nil {
		st.Pending = map[string]*pendingCall{}
	}
	if st.IDShapes == nil {
		st.IDShapes = map[string][]string{}
	}
}

// permissionGateTools are the MCP tools whose call opens an attention item a
// human must answer. A call to one that never returns is a stalled human gate
// (`unanswered_permission`), not a plumbing fault. Mirrors the client-side gate
// list (lib/widgets/transcript/feed_reducer.dart `_kGateToolNames`) minus
// `request_help` — a question is not a permission.
var permissionGateTools = map[string]bool{
	"permission_prompt": true,
	"request_approval":  true,
}

// isPermissionGateTool matches a gate tool by bare name or under any MCP
// server prefix (`mcp__termipod__permission_prompt`), the same two spellings
// the clients accept.
func isPermissionGateTool(name string) bool {
	if name == "" {
		return false
	}
	if permissionGateTools[name] {
		return true
	}
	for g := range permissionGateTools {
		if len(name) > len(g)+2 && name[len(name)-len(g)-2:] == "__"+g {
			return true
		}
	}
	return false
}

// eventToolIDKey extracts the tool-call id an event refers to AND the payload
// key it was carried under.
//
// This is the hub's single table of id-key precedence — eventToolID delegates
// here. Keeping one table is the point: the `callToolIdOf` bug shipped three
// times because each consumer re-derived the precedence for itself (plan §5,
// "both id shapes"). Returning the key alongside the value is also what makes
// the mixed_id_shape canary possible at zero extra cost.
//
//	tool_call         → id, toolCallId
//	tool_result       → tool_use_id, toolCallId, id
//	tool_call_update  → toolCallId, id
func eventToolIDKey(kind string, p map[string]any) (string, string) {
	var keys []string
	switch kind {
	case "tool_call":
		keys = []string{"id", "toolCallId"}
	case "tool_result":
		keys = []string{"tool_use_id", "toolCallId", "id"}
	case "tool_call_update":
		keys = []string{"toolCallId", "id"}
	default:
		return "", ""
	}
	for _, k := range keys {
		if v := stringOf(p[k]); v != "" {
			return v, k
		}
	}
	return "", ""
}

// toolCallIsResolved reports whether an event terminally answers the call it
// refers to. A tool_result IS the result, always terminal. A tool_call_update
// is a partial patch (ACP sends progress updates carrying only content), so it
// resolves the call unless it says the call is still in flight.
//
// Unknown statuses read as terminal — the deliberate direction. Mis-resolving
// costs one missed finding; mis-holding would flag EVERY completed call of an
// engine whose status vocabulary we don't know yet, and a surface that cries
// wolf on every row buries the real findings it exists to show.
func toolCallIsResolved(kind string, p map[string]any) bool {
	if kind != "tool_call_update" {
		return true
	}
	switch stringOf(p["status"]) {
	case "", "pending", "queued", "running", "in_progress":
		return false
	}
	return true
}

// normalTurnStopReasons are the stop/terminal reasons that mean the turn ended
// the way it was supposed to. `cancelled` is here on purpose: a turn the
// director stopped is an intentional end, not an integrity finding.
//
// The set is a denylist of NORMAL rather than an allowlist of abnormal, so a
// reason no engine has shipped yet (a new refusal or budget-exhausted spelling)
// surfaces instead of vanishing. That is the correct default for a surface
// whose whole purpose is that silent failures are invisible — a false positive
// is one visible row anyone can refute, a false negative is exactly the bug we
// keep shipping. Adding a spelling here is the cheap correction.
var normalTurnStopReasons = map[string]bool{
	"":          true,
	"success":   true,
	"end_turn":  true,
	"completed": true,
	"cancelled": true,
}

// abnormalStopReason returns the turn's stop reason when it is not a normal
// end, else "". Reads the two top-level fields the mappers actually write:
// `stop_reason` (ACP — driver_acp.go stamps the raw ACP stopReason) and
// `terminal_reason` (claude M2 — driver_stdio.go normalizeTurnResult lifts the
// result frame's terminal_reason/subtype).
//
// This is load-bearing beyond tidiness: claude's turn.result carries NO
// `status` field, so turnResultStatus reads it as "success" and a run that
// stopped early (hit its turn budget, refused) folds today as a clean turn.
// The digest's error taxonomy can't be widened to catch it without changing
// what error_count means (canonicalErrorSQLPredicate has to keep reconciling
// with /v1/insights), which is exactly why it belongs in the issues layer.
func abnormalStopReason(p map[string]any) string {
	for _, k := range []string{"stop_reason", "terminal_reason"} {
		r := stringOf(p[k])
		if r == "" {
			continue
		}
		if normalTurnStopReasons[r] {
			return ""
		}
		return r
	}
	return ""
}

// sortPendingIDs orders the open-call ids by their call seq (then id) so a
// sweep emits its findings deterministically. Go randomizes map iteration, so
// an unsorted sweep would give the incremental and brute-force folds the same
// counts but a different sample ORDER — an equivalence failure that would only
// show up as a flaky test.
func sortPendingIDs(pending map[string]*pendingCall) []string {
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := pending[ids[i]], pending[ids[j]]
		if a.Seq != b.Seq {
			return a.Seq < b.Seq
		}
		return ids[i] < ids[j]
	})
	return ids
}
