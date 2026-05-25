package pricing

import (
	"context"
	"database/sql"
	"sort"
	"testing"

	_ "modernc.org/sqlite"
)

// TestCostFromTokens — the per-message arithmetic. Sanity-checks the
// per-million division on all four token classes independently and
// in combination. No DB needed.
func TestCostFromTokens(t *testing.T) {
	r := Rate{
		InputPerMillion:      15.00,
		OutputPerMillion:     75.00,
		CacheReadPerMillion:  1.50,
		CacheWritePerMillion: 18.75,
	}

	cases := []struct {
		name  string
		tc    TokenCounts
		want  float64
	}{
		{"zero", TokenCounts{}, 0},
		{"input only — 1M = $15", TokenCounts{Input: 1_000_000}, 15.00},
		{"output only — 1M = $75", TokenCounts{Output: 1_000_000}, 75.00},
		{"cache_read only — 1M = $1.50", TokenCounts{CacheRead: 1_000_000}, 1.50},
		{"cache_write only — 1M = $18.75", TokenCounts{CacheWrite: 1_000_000}, 18.75},
		{"all four classes",
			TokenCounts{Input: 100_000, Output: 50_000, CacheRead: 200_000, CacheWrite: 10_000},
			// 0.1*15 + 0.05*75 + 0.2*1.5 + 0.01*18.75
			// = 1.5 + 3.75 + 0.3 + 0.1875
			// = 5.7375
			5.7375},
		// Sub-1M sample — typical per-message rate is hundreds of
		// tokens; verify the division is exact, not lossy.
		{"500 input tokens", TokenCounts{Input: 500}, 15.0 * 500 / 1_000_000.0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CostFromTokens(c.tc, r)
			if !floatNear(got, c.want, 1e-9) {
				t.Errorf("CostFromTokens(%v) = %v, want %v", c.tc, got, c.want)
			}
		})
	}
}

// TestSessionCostEmptySession — well-formed inputs, no events.
// Returns zero-valued result, no error.
func TestSessionCostEmptySession(t *testing.T) {
	db := newTestDB(t)
	loader := newEmbeddedLoader(t)

	res, err := SessionCost(context.Background(), db, loader, "ses-empty")
	if err != nil {
		t.Fatalf("SessionCost: %v", err)
	}
	if res.TotalUSD != 0 {
		t.Errorf("TotalUSD = %v, want 0", res.TotalUSD)
	}
	if len(res.Breakdown) != 0 {
		t.Errorf("Breakdown non-empty: %v", res.Breakdown)
	}
	if len(res.Missing) != 0 {
		t.Errorf("Missing non-empty: %v", res.Missing)
	}
	// Embedded snapshot date must propagate even for empty sessions
	// — the chip tooltip wants to show it.
	if res.SnapshotDate == "" {
		t.Error("SnapshotDate empty for embedded-tier loader")
	}
}

// TestSessionCostSingleMessage — one usage event, single model from
// the embedded table. Total USD must equal CostFromTokens for that
// row exactly.
func TestSessionCostSingleMessage(t *testing.T) {
	db := newTestDB(t)
	loader := newEmbeddedLoader(t)

	const sesID = "ses-001"
	insertUsage(t, db, sesID, "claude-opus-4-7",
		1000, // input
		500,  // output
		2000, // cache_read
		100,  // cache_write
	)

	res, err := SessionCost(context.Background(), db, loader, sesID)
	if err != nil {
		t.Fatalf("SessionCost: %v", err)
	}
	// opus-4-7 rates: 15/75/1.5/18.75 → for 1000/500/2000/100 tokens:
	// 1000*15/1e6 + 500*75/1e6 + 2000*1.5/1e6 + 100*18.75/1e6
	// = 0.015 + 0.0375 + 0.003 + 0.001875
	// = 0.057375
	want := 0.057375
	if !floatNear(res.TotalUSD, want, 1e-9) {
		t.Errorf("TotalUSD = %v, want %v", res.TotalUSD, want)
	}
	if usd, ok := res.Breakdown["claude-opus-4-7"]; !ok || !floatNear(usd, want, 1e-9) {
		t.Errorf("Breakdown[opus] = %v ok=%v, want %v", usd, ok, want)
	}
	if len(res.Missing) != 0 {
		t.Errorf("Missing = %v, want empty", res.Missing)
	}
	tk := res.Tokens["claude-opus-4-7"]
	if tk.Input != 1000 || tk.Output != 500 || tk.CacheRead != 2000 || tk.CacheWrite != 100 {
		t.Errorf("Tokens = %+v, want {1000,500,2000,100}", tk)
	}
}

// TestSessionCostMultiMessageAggregatesPerModel — three events on
// one model, plus events on a second model. Per-model rows must sum;
// total is the sum of both per-model contributions.
func TestSessionCostMultiMessageAggregatesPerModel(t *testing.T) {
	db := newTestDB(t)
	loader := newEmbeddedLoader(t)

	const sesID = "ses-multi"
	// 3 opus events (input only for simplicity)
	insertUsage(t, db, sesID, "claude-opus-4-7", 1000, 0, 0, 0)
	insertUsage(t, db, sesID, "claude-opus-4-7", 2000, 0, 0, 0)
	insertUsage(t, db, sesID, "claude-opus-4-7", 3000, 0, 0, 0)
	// 1 sonnet event
	insertUsage(t, db, sesID, "claude-sonnet-4-6", 100_000, 1_000, 0, 0)

	res, err := SessionCost(context.Background(), db, loader, sesID)
	if err != nil {
		t.Fatalf("SessionCost: %v", err)
	}
	// opus: 6000 input tokens * 15/1e6 = $0.09
	wantOpus := 6000 * 15.0 / 1_000_000.0
	// sonnet: 100_000 input * 3/1e6 + 1_000 output * 15/1e6
	//        = 0.30 + 0.015 = 0.315
	wantSonnet := 100_000*3.0/1_000_000.0 + 1_000*15.0/1_000_000.0
	wantTotal := wantOpus + wantSonnet

	if !floatNear(res.TotalUSD, wantTotal, 1e-9) {
		t.Errorf("TotalUSD = %v, want %v", res.TotalUSD, wantTotal)
	}
	if !floatNear(res.Breakdown["claude-opus-4-7"], wantOpus, 1e-9) {
		t.Errorf("Breakdown[opus] = %v, want %v", res.Breakdown["claude-opus-4-7"], wantOpus)
	}
	if !floatNear(res.Breakdown["claude-sonnet-4-6"], wantSonnet, 1e-9) {
		t.Errorf("Breakdown[sonnet] = %v, want %v",
			res.Breakdown["claude-sonnet-4-6"], wantSonnet)
	}
	// Tokens table must show both rows summed.
	if res.Tokens["claude-opus-4-7"].Input != 6000 {
		t.Errorf("Tokens[opus].Input = %v, want 6000",
			res.Tokens["claude-opus-4-7"].Input)
	}
	if res.Tokens["claude-sonnet-4-6"].Output != 1000 {
		t.Errorf("Tokens[sonnet].Output = %v, want 1000",
			res.Tokens["claude-sonnet-4-6"].Output)
	}
}

// TestSessionCostUnknownModelDegrades — model id absent from the
// embedded table contributes nothing to TotalUSD, is omitted from
// Breakdown, and lands in Missing. Tokens still tracks it (for the
// chip tooltip's "model not priced" affordance).
func TestSessionCostUnknownModelDegrades(t *testing.T) {
	db := newTestDB(t)
	loader := newEmbeddedLoader(t)

	const sesID = "ses-unknown"
	insertUsage(t, db, sesID, "claude-opus-4-7", 1_000_000, 0, 0, 0)        // priced
	insertUsage(t, db, sesID, "claude-future-99", 500_000, 200_000, 0, 0)   // unpriced
	insertUsage(t, db, sesID, "claude-mystery", 100, 0, 0, 0)               // unpriced

	res, err := SessionCost(context.Background(), db, loader, sesID)
	if err != nil {
		t.Fatalf("SessionCost: %v", err)
	}
	want := 15.0 // 1M input tokens of opus = exactly $15
	if !floatNear(res.TotalUSD, want, 1e-9) {
		t.Errorf("TotalUSD = %v, want %v (unpriced models must not contribute)",
			res.TotalUSD, want)
	}
	if _, ok := res.Breakdown["claude-future-99"]; ok {
		t.Error("Breakdown leaked unpriced model")
	}
	if len(res.Breakdown) != 1 {
		t.Errorf("Breakdown rows = %d, want 1", len(res.Breakdown))
	}

	// Missing must be sorted for determinism.
	want_missing := []string{"claude-future-99", "claude-mystery"}
	got_missing := append([]string(nil), res.Missing...)
	sort.Strings(got_missing)
	if !stringSliceEq(got_missing, want_missing) {
		t.Errorf("Missing = %v, want %v", res.Missing, want_missing)
	}

	// Tokens entries for unpriced models still present (so tooltip
	// can render token totals even when USD is blank).
	if res.Tokens["claude-future-99"].Input != 500_000 {
		t.Errorf("Tokens[future-99].Input = %v, want 500000",
			res.Tokens["claude-future-99"].Input)
	}
}

// TestSessionCostUnscopedAgentEvents — events for OTHER sessions on
// the same DB must not leak into the target session's total.
func TestSessionCostUnscopedAgentEvents(t *testing.T) {
	db := newTestDB(t)
	loader := newEmbeddedLoader(t)

	insertUsage(t, db, "ses-A", "claude-opus-4-7", 1_000_000, 0, 0, 0)
	insertUsage(t, db, "ses-B", "claude-opus-4-7", 5_000_000, 0, 0, 0)

	resA, _ := SessionCost(context.Background(), db, loader, "ses-A")
	resB, _ := SessionCost(context.Background(), db, loader, "ses-B")
	if !floatNear(resA.TotalUSD, 15.0, 1e-9) {
		t.Errorf("ses-A TotalUSD = %v, want 15.0", resA.TotalUSD)
	}
	if !floatNear(resB.TotalUSD, 75.0, 1e-9) {
		t.Errorf("ses-B TotalUSD = %v, want 75.0", resB.TotalUSD)
	}
}

// TestSessionCostIgnoresOtherKinds — only `usage` events count;
// `status_line`, `text`, `tool_call` etc. on the same session must be
// ignored even when they carry tokens-like keys.
func TestSessionCostIgnoresOtherKinds(t *testing.T) {
	db := newTestDB(t)
	loader := newEmbeddedLoader(t)

	const sesID = "ses-filter"
	insertUsage(t, db, sesID, "claude-opus-4-7", 1_000, 0, 0, 0)
	// Plant a status_line row with bogus token fields — must NOT count.
	insertEvent(t, db, sesID, "status_line",
		`{"model":{"id":"claude-opus-4-7"},"input_tokens":999999999}`)
	// Plant a `text` row — also must NOT count.
	insertEvent(t, db, sesID, "text",
		`{"model":"claude-opus-4-7","input_tokens":999999999}`)

	res, err := SessionCost(context.Background(), db, loader, sesID)
	if err != nil {
		t.Fatalf("SessionCost: %v", err)
	}
	want := 1000 * 15.0 / 1_000_000.0
	if !floatNear(res.TotalUSD, want, 1e-9) {
		t.Errorf("TotalUSD = %v, want %v", res.TotalUSD, want)
	}
}

// TestSessionCostNilGuards — nil db / nil loader are programming
// errors and return without panicking.
func TestSessionCostNilGuards(t *testing.T) {
	loader := newEmbeddedLoader(t)
	if _, err := SessionCost(context.Background(), nil, loader, "ses"); err == nil {
		t.Error("SessionCost(nil db) returned no error")
	}
	db := newTestDB(t)
	if _, err := SessionCost(context.Background(), db, nil, "ses"); err == nil {
		t.Error("SessionCost(nil loader) returned no error")
	}
	res, err := SessionCost(context.Background(), db, loader, "")
	if err != nil {
		t.Errorf("SessionCost(empty session) errored: %v", err)
	}
	if res.TotalUSD != 0 {
		t.Errorf("SessionCost(empty session) total = %v, want 0", res.TotalUSD)
	}
}

// TestSessionCostMissingModelPayload — usage event without a model
// field (e.g. pre-W2 historical rows) is dropped silently from the
// sum and does NOT land in Missing (it's a data gap, not an unpriced
// model situation).
func TestSessionCostMissingModelPayload(t *testing.T) {
	db := newTestDB(t)
	loader := newEmbeddedLoader(t)

	const sesID = "ses-nomodel"
	insertEvent(t, db, sesID, "usage",
		`{"input_tokens":1000,"output_tokens":100}`)
	insertUsage(t, db, sesID, "claude-opus-4-7", 1000, 0, 0, 0)

	res, err := SessionCost(context.Background(), db, loader, sesID)
	if err != nil {
		t.Fatalf("SessionCost: %v", err)
	}
	// Only the opus-4-7 row counts; total = 1000*15/1e6 = 0.015.
	want := 0.015
	if !floatNear(res.TotalUSD, want, 1e-9) {
		t.Errorf("TotalUSD = %v, want %v", res.TotalUSD, want)
	}
	if len(res.Missing) != 0 {
		t.Errorf("Missing = %v, want empty (no model id is not an unpriced model)",
			res.Missing)
	}
}

// --- helpers ---------------------------------------------------------

func newEmbeddedLoader(t *testing.T) *Loader {
	t.Helper()
	t.Setenv(envOverridePath, "")
	t.Setenv(envHubData, t.TempDir())
	return NewLoader(nil)
}

// newTestDB creates an in-memory sqlite with a minimal agent_events
// table — just the columns SessionCost touches. We don't run the hub
// migration set here because it pulls in agents/sessions/projects
// schemas the compute pass doesn't need.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`
		CREATE TABLE agent_events (
			id           TEXT PRIMARY KEY,
			agent_id     TEXT NOT NULL,
			seq          INTEGER NOT NULL,
			ts           TEXT NOT NULL,
			kind         TEXT NOT NULL,
			producer     TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			session_id   TEXT
		);
		CREATE INDEX idx_agent_events_session ON agent_events(session_id, ts);
	`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// seq is package-level so test inserts get monotonic seq across all
// helper calls within a single test process — mirrors the hub's own
// per-agent monotonicity invariant.
var nextSeq int

func insertUsage(t *testing.T, db *sql.DB, sessionID, model string,
	in, out, cr, cw int64,
) {
	t.Helper()
	payload := jsonObj(map[string]any{
		"model":         model,
		"input_tokens":  in,
		"output_tokens": out,
		"cache_read":    cr,
		"cache_create":  cw,
		"engine":        "claude-code",
	})
	insertEvent(t, db, sessionID, "usage", payload)
}

func insertEvent(t *testing.T, db *sql.DB, sessionID, kind, payload string) {
	t.Helper()
	nextSeq++
	_, err := db.Exec(`
		INSERT INTO agent_events
		  (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"evt-"+sessionID+"-"+itoa(nextSeq),
		"agent-x", nextSeq, "2026-05-25T00:00:00Z",
		kind, "agent", payload, sessionID,
	)
	if err != nil {
		t.Fatal(err)
	}
}

// --- tiny utilities (no fmt to keep import surface minimal) ---------

func floatNear(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func stringSliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// jsonObj formats a simple map as JSON without importing encoding/json
// from the test file (keeps the failure surface limited to compute.go's
// json decoder). Only handles string-key maps with int64 / string /
// float64 values — enough for the usage payloads we construct here.
func jsonObj(m map[string]any) string {
	var sb []byte
	sb = append(sb, '{')
	first := true
	// Stable key order so a future failing test diff is readable.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !first {
			sb = append(sb, ',')
		}
		first = false
		sb = append(sb, '"')
		sb = append(sb, k...)
		sb = append(sb, '"', ':')
		switch v := m[k].(type) {
		case string:
			sb = append(sb, '"')
			sb = append(sb, v...)
			sb = append(sb, '"')
		case int64:
			sb = append(sb, itoa(int(v))...)
		case int:
			sb = append(sb, itoa(v)...)
		default:
			// Best-effort: fall back to a quoted string of the type tag.
			sb = append(sb, '"', '?', '"')
		}
	}
	sb = append(sb, '}')
	return string(sb)
}
