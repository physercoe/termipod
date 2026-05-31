package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type ctxKey int

const (
	tokenCtx ctxKey = iota
	inProcessCtx
)

// WithInProcessDispatch marks a request context as originating from the
// hub's own in-process authority-tool dispatch (the chiRouterTransport
// self-call), not the network. Such a request has already passed the
// MCP role check before being forwarded to the REST routes, so the
// bearer-kind allowlist (F-01) exempts it — an agent token is the
// legitimate credential there. A Go context value cannot be injected by
// a network client, so this is unspoofable from the wire.
func WithInProcessDispatch(ctx context.Context) context.Context {
	return context.WithValue(ctx, inProcessCtx, true)
}

func isInProcessDispatch(ctx context.Context) bool {
	v, _ := ctx.Value(inProcessCtx).(bool)
	return v
}

type Token struct {
	ID        string
	Kind      string
	ScopeJSON string
}

// ScopeTeam returns the `team` field of the token's scope_json, or "" if
// the scope is empty/unparseable/teamless. Every legitimate bearer kind
// (owner, user, host, agent) carries a team in its scope at mint time
// (handlers_tokens.go, handlers_agents.go, cmd/hub-server), so an empty
// result means a malformed or pre-team-scope token — the team gate
// (ADR-037 D1) treats that as "no team binding" and fails closed.
func (t *Token) ScopeTeam() string {
	if t == nil {
		return ""
	}
	var sc struct {
		Team string `json:"team"`
	}
	_ = json.Unmarshal([]byte(t.ScopeJSON), &sc)
	return sc.Team
}

// NewToken returns a freshly-generated 32-byte token, base64url-encoded.
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the lowercase hex SHA-256 of token. Storage is hash-only.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// InsertToken writes a new token row. Returns (id, nil) on success.
func InsertToken(ctx context.Context, db *sql.DB, kind, scopeJSON, plaintext string, id string, now string) error {
	hash := HashToken(plaintext)
	_, err := db.ExecContext(ctx, `
		INSERT INTO auth_tokens (id, kind, token_hash, scope_json, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		id, kind, hash, scopeJSON, now,
	)
	return err
}

// DBExec is the subset of *sql.DB / *sql.Tx that RevokeAgentTokens needs;
// the helper accepts either so callers in a transaction can revoke
// inline (handleSpawn's session-swap) and out-of-tx callers can still
// reach it (handlePatchAgent).
type DBExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// RevokeAgentTokens marks every live agent-kind token bound to agentID
// as revoked. Called from the agent-terminate paths so a dead agent's
// bearer can no longer hit /mcp/{token} or post events. Idempotent:
// already-revoked rows are skipped via the `revoked_at IS NULL` clause.
// Returns the number of rows revoked.
func RevokeAgentTokens(ctx context.Context, exec DBExec, agentID, now string) (int64, error) {
	if agentID == "" {
		return 0, nil
	}
	res, err := exec.ExecContext(ctx, `
		UPDATE auth_tokens
		   SET revoked_at = ?
		 WHERE kind = 'agent'
		   AND revoked_at IS NULL
		   AND json_extract(scope_json, '$.agent_id') = ?`,
		now, agentID,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Middleware returns an HTTP middleware that enforces bearer-token auth.
// Tokens are looked up by sha256 hash; revoked and expired tokens are rejected.
// The matched token is attached to the request context via FromContext.
func Middleware(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Allow unauthenticated /v1/_info for client bootstrap.
			if r.URL.Path == "/v1/_info" {
				next.ServeHTTP(w, r)
				return
			}
			raw := extractBearer(r)
			if raw == "" {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}
			tok, err := lookup(r.Context(), db, raw)
			if err != nil {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			// F-01: only human (owner|user) and deputy (host) tokens are
			// legitimate bearer credentials. An agent's door is
			// /mcp/{token} (mounted outside this middleware); agent
			// operations against the REST API are relayed by the
			// host-runner under its host token + X-Agent-Id, so an agent
			// token presented as a bearer here is either a misuse or a
			// stolen credential trying to reach privileged routes
			// (policy/template/spawn/admin). Allowlist the bearer kinds
			// and fail closed for agent + any future kind.
			switch tok.Kind {
			case "operator", "owner", "user", "host":
				// legitimate bearer kinds. `operator` is the hub root
				// (ADR-037 D2) — team-transcendent, the only credential
				// for /v1/admin/*; `owner` is the per-team principal.
			default:
				// agent (+ any unknown kind): rejected on the network,
				// but the hub's own in-process authority dispatch
				// (already role-checked at the MCP layer) forwards an
				// agent token here legitimately — exempt it.
				if !isInProcessDispatch(r.Context()) {
					http.Error(w,
						"token kind not permitted for bearer auth (agents authenticate via /mcp/{token})",
						http.StatusForbidden)
					return
				}
			}
			ctx := context.WithValue(r.Context(), tokenCtx, tok)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

// ResolveBearer resolves the request's Authorization bearer to a Token
// without enforcing auth — used by unauthed endpoints (e.g. /a2a/relay)
// that still want to attribute the caller when one is present. Returns
// (nil, nil) when no bearer is supplied; (nil, err) on a malformed or
// revoked token; (*Token, nil) on a valid one.
func ResolveBearer(ctx context.Context, db *sql.DB, r *http.Request) (*Token, error) {
	raw := extractBearer(r)
	if raw == "" {
		return nil, nil
	}
	return lookup(ctx, db, raw)
}

func lookup(ctx context.Context, db *sql.DB, raw string) (*Token, error) {
	hash := HashToken(raw)
	var (
		id, kind, scope string
		expiresAt       sql.NullString
		revokedAt       sql.NullString
	)
	err := db.QueryRowContext(ctx, `
		SELECT id, kind, scope_json, expires_at, revoked_at
		FROM auth_tokens
		WHERE token_hash = ?`, hash).Scan(&id, &kind, &scope, &expiresAt, &revokedAt)
	if err != nil {
		return nil, err
	}
	if revokedAt.Valid {
		return nil, errors.New("revoked")
	}
	if expiresAt.Valid {
		t, err := time.Parse(time.RFC3339Nano, expiresAt.String)
		if err == nil && time.Now().After(t) {
			return nil, errors.New("expired")
		}
	}
	return &Token{ID: id, Kind: kind, ScopeJSON: scope}, nil
}

// FromContext returns the authenticated token attached by Middleware.
func FromContext(ctx context.Context) (*Token, bool) {
	t, ok := ctx.Value(tokenCtx).(*Token)
	return t, ok
}

// WithToken attaches tok to ctx under the same key Middleware uses, so a
// downstream handler sees it via FromContext. Production auth flows go
// through Middleware; this is the explicit constructor (the counterpart
// to FromContext) for tests and any future in-process caller that needs
// to assemble an authenticated context without the HTTP layer.
func WithToken(ctx context.Context, tok *Token) context.Context {
	return context.WithValue(ctx, tokenCtx, tok)
}
