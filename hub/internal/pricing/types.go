// Package pricing provides hub-side computation of session-cumulative
// dollar cost for claude-code agents (ADR-036 D8 chip 2). The dollar
// amounts are *imputed* against Anthropic's public API rate sheet — they
// are NOT what the operator is actually billed (most users are on a
// subscription plan whose billing is independent of per-token usage).
//
// The rate table is intentionally externalised per ADR-036 D10:
//
//   - Operator on-disk override under `$HUB_DATA/pricing/claude.yaml`
//     (env `TERMIPOD_PRICING_CLAUDE` overrides the path); mtime-reloaded
//     on access.
//   - Embedded default `claude_default.yaml` compiled in via `//go:embed`,
//     snapshot-dated in the file header.
//   - Per-model fallback: unknown model in both tiers → the contribution
//     is dropped from the total and the model id is surfaced via the
//     Missing field on a SessionCost result, so the chip can degrade
//     ("blank > wrong" per ADR-036 D9 discipline).
//
// Consumers obtain a Table via the package-level Resolve() and either
// inspect it directly or call SessionCost(ctx, db, sessionID) to fold
// the per-message `usage` events of a session into a USD figure with
// per-model breakdown.
package pricing

import (
	"errors"
	"fmt"
)

// Table is the parsed pricing table. The zero value is unusable —
// callers always get one back from a Loader's Resolve() (loader.go) or
// the package-level Resolve() shorthand.
type Table struct {
	// Version is the YAML schema version. Currently 1; bump when the
	// shape of Rate changes incompatibly. The loader rejects unknown
	// versions and falls through to the next tier with a warning.
	Version int `yaml:"version"`

	// SnapshotDate is the YYYY-MM-DD the rates were captured from
	// Anthropic's pricing page. Surfaced in the chip tooltip so a
	// user spots stale config.
	SnapshotDate string `yaml:"snapshot_date"`

	// SourceURL is the public pricing page the snapshot came from.
	// Optional; surfaced in the tooltip for operator forensics.
	SourceURL string `yaml:"source_url,omitempty"`

	// Models maps a model id (the value present on a usage event's
	// `model` field, e.g. `claude-opus-4-7`) to its per-million-token
	// rates. An empty map is a parse error.
	Models map[string]Rate `yaml:"models"`

	// Origin records which tier resolved this table — used by tests
	// and by the warning-audit dispatch so an operator can tell
	// whether their override was actually picked up. Not serialised.
	Origin Origin `yaml:"-"`
}

// Rate is the per-model rate sheet entry. All four fields are USD per
// one million tokens. A model with zero rates is treated as "unknown"
// at compute time (we don't multiply by zero — we drop and surface).
type Rate struct {
	InputPerMillion      float64 `yaml:"input_per_million"`
	OutputPerMillion     float64 `yaml:"output_per_million"`
	CacheReadPerMillion  float64 `yaml:"cache_read_per_million"`
	CacheWritePerMillion float64 `yaml:"cache_write_per_million"`
}

// Origin labels which of the three tiers the active Table came from.
// Surfaced in the chip tooltip and to tests.
type Origin string

const (
	// OriginOperator means the table came from the env-overridable
	// on-disk path under $HUB_DATA/pricing/claude.yaml.
	OriginOperator Origin = "operator"

	// OriginEmbedded means the embedded `claude_default.yaml` was used
	// (no override file or it failed to parse).
	OriginEmbedded Origin = "embedded"
)

// ErrUnknownModel is returned by Table.RateFor when the model id has
// no entry. Callers SHOULD treat the contribution as zero and record
// the model id for the caller-supplied missing-model callback.
var ErrUnknownModel = errors.New("pricing: unknown model")

// RateFor returns the per-million rates for a model id, or
// ErrUnknownModel if the id is absent. A nil receiver returns
// ErrUnknownModel; callers must not crash on a nil table.
func (t *Table) RateFor(model string) (Rate, error) {
	if t == nil || len(t.Models) == 0 {
		return Rate{}, ErrUnknownModel
	}
	r, ok := t.Models[model]
	if !ok {
		return Rate{}, ErrUnknownModel
	}
	return r, nil
}

// Validate enforces minimum invariants: the schema version is
// supported, there is at least one model, every model has at least one
// non-zero rate. Returns a descriptive error suitable for an audit row
// summary. Called by the loader after YAML decode.
func (t *Table) Validate() error {
	if t == nil {
		return errors.New("pricing: nil table")
	}
	if t.Version != 1 {
		return fmt.Errorf("pricing: unsupported schema version %d (want 1)", t.Version)
	}
	if len(t.Models) == 0 {
		return errors.New("pricing: empty models map")
	}
	for id, r := range t.Models {
		if r.InputPerMillion == 0 && r.OutputPerMillion == 0 &&
			r.CacheReadPerMillion == 0 && r.CacheWritePerMillion == 0 {
			return fmt.Errorf("pricing: model %q has all-zero rates", id)
		}
	}
	return nil
}

// TokenCounts is the input to a single per-message contribution to a
// session's cost. The names mirror the on-the-wire `usage` event
// payload at hub/internal/drivers/local_log_tail/claude_code/mapper.go
// usageFromMessage so the compute pass can populate them by direct
// field copy.
type TokenCounts struct {
	Input       int64
	Output      int64
	CacheRead   int64
	CacheWrite  int64
}

// CostFromTokens returns the dollar contribution of one usage event at
// the supplied rate. Caller has already classified the model. Returns
// zero on nil counts.
func CostFromTokens(t TokenCounts, r Rate) float64 {
	if t == (TokenCounts{}) {
		return 0
	}
	const million = 1_000_000.0
	in := float64(t.Input) * r.InputPerMillion / million
	out := float64(t.Output) * r.OutputPerMillion / million
	cr := float64(t.CacheRead) * r.CacheReadPerMillion / million
	cw := float64(t.CacheWrite) * r.CacheWritePerMillion / million
	return in + out + cr + cw
}
