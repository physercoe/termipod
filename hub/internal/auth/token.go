package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

type ctxKey int

const (
	tokenCtx ctxKey = iota
)

type Token struct {
	ID        string
	Kind      string
	ScopeJSON string
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
