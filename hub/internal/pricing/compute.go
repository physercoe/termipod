package pricing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
)

// SessionCost computes the imputed USD for one session by aggregating
// `agent_events.kind='usage'` rows and applying the resolved Table.
//
// Returns:
//   - TotalUSD: sum across all models present in the session's usage
//     events. Models missing from the table contribute zero (their
//     ids land in Missing).
//   - Breakdown: per-model USD breakdown for models the table knows.
//     Useful for the chip tooltip to mirror /usage's "Usage by model"
//     block.
//   - Tokens: per-model token totals (sum across all usage events) so
//     a caller can show "in/out/cache" alongside USD.
//   - Missing: models seen on usage events but absent from the table.
//     Caller (server) decides whether to warn-audit per first-sighting.
//   - SnapshotDate: which pricing snapshot the totals reflect (passed
//     through so the chip tooltip can show "rates as of YYYY-MM-DD").
//   - Origin: which tier resolved (operator vs embedded). Tests use it.
//
// Returns an error only on a database failure; an empty session
// returns a zero-valued Result with no error. A nil db is a programming
// error and is treated as a fatal validation problem.
func SessionCost(
	ctx context.Context,
	db *sql.DB,
	loader *Loader,
	sessionID string,
) (Result, error) {
	if db == nil {
		return Result{}, fmt.Errorf("pricing: nil db")
	}
	if loader == nil {
		return Result{}, fmt.Errorf("pricing: nil loader")
	}
	if sessionID == "" {
		return Result{}, nil
	}

	tbl := loader.Resolve()
	res := Result{
		Breakdown:    map[string]float64{},
		Tokens:       map[string]TokenCounts{},
		SnapshotDate: tbl.SnapshotDate,
		Origin:       tbl.Origin,
	}

	// Aggregate token counts per model first (so multiple events for
	// the same model land in one Breakdown row). The query is bounded
	// by the session-scoped index from migration 0026.
	rows, err := db.QueryContext(ctx, `
		SELECT payload_json
		  FROM agent_events
		 WHERE session_id = ? AND kind = 'usage'`, sessionID)
	if err != nil {
		return Result{}, fmt.Errorf("pricing: query usage events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var payloadJSON string
		if err := rows.Scan(&payloadJSON); err != nil {
			return Result{}, fmt.Errorf("pricing: scan usage row: %w", err)
		}
		model, tc := decodeUsagePayload(payloadJSON)
		if model == "" {
			// Pre-W2 usage rows that didn't carry `model` can't be
			// attributed to a rate — drop the contribution but don't
			// warn (it's a normal historical case, not an operator
			// problem).
			continue
		}
		agg := res.Tokens[model]
		agg.Input += tc.Input
		agg.Output += tc.Output
		agg.CacheRead += tc.CacheRead
		agg.CacheWrite += tc.CacheWrite
		res.Tokens[model] = agg
	}
	if err := rows.Err(); err != nil {
		return Result{}, fmt.Errorf("pricing: iterate usage rows: %w", err)
	}

	// Resolve rates and compute. Sorted iteration so Breakdown +
	// Missing have deterministic ordering for tests and tooltips.
	models := make([]string, 0, len(res.Tokens))
	for m := range res.Tokens {
		models = append(models, m)
	}
	sort.Strings(models)
	for _, m := range models {
		tc := res.Tokens[m]
		rate, err := tbl.RateFor(m)
		if err != nil {
			res.Missing = append(res.Missing, m)
			continue
		}
		usd := CostFromTokens(tc, rate)
		res.Breakdown[m] = usd
		res.TotalUSD += usd
	}
	return res, nil
}

// Result is the return shape of SessionCost. The chip serialiser in
// the server package converts this to the wire shape — see
// handlers_session_cost.go (ADR-036 D8 D10).
type Result struct {
	// TotalUSD is the sum across all known-model contributions.
	TotalUSD float64

	// Breakdown holds per-model USD. Keys are a subset of Tokens'
	// keys (excludes Missing). Always non-nil; empty for an empty
	// session.
	Breakdown map[string]float64

	// Tokens holds per-model token totals for every model SEEN in
	// the session, including ones that landed in Missing. The chip
	// tooltip surfaces tokens for missing models even when USD can't
	// be computed.
	Tokens map[string]TokenCounts

	// Missing lists model ids seen on usage events but absent from
	// the pricing table. Sorted for determinism. Server emits one
	// warning audit per first-sighting per spawn (per ADR-036 D10).
	Missing []string

	// SnapshotDate is the active table's snapshot date.
	SnapshotDate string

	// Origin labels which tier of the loader served the table.
	Origin Origin
}

// decodeUsagePayload extracts (model, tokens) from a usage event's
// JSON payload. Tolerant — missing fields default to zero; an
// unparseable payload returns ("", zero) so the row is dropped.
//
// Wire shape (from mapper.go usageFromMessage):
//
//	{"input_tokens":N, "output_tokens":N, "cache_read":N,
//	 "cache_create":N, "model":"claude-opus-4-7", "engine":"claude-code",
//	 "context_window":1000000}
//
// Note the JSONL-side names `cache_read` / `cache_create` (not
// `cache_read_input_tokens` / `cache_creation_input_tokens` —
// usageFromMessage renames them before posting).
func decodeUsagePayload(s string) (string, TokenCounts) {
	if s == "" {
		return "", TokenCounts{}
	}
	var p struct {
		Model       string `json:"model"`
		Input       int64  `json:"input_tokens"`
		Output      int64  `json:"output_tokens"`
		CacheRead   int64  `json:"cache_read"`
		CacheCreate int64  `json:"cache_create"`
	}
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return "", TokenCounts{}
	}
	return p.Model, TokenCounts{
		Input:      p.Input,
		Output:     p.Output,
		CacheRead:  p.CacheRead,
		CacheWrite: p.CacheCreate,
	}
}
