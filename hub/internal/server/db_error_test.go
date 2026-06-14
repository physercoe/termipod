package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestMapDBError(t *testing.T) {
	t.Run("nil is 200", func(t *testing.T) {
		if st, msg := mapDBError(nil); st != http.StatusOK || msg != "" {
			t.Fatalf("nil → (%d,%q), want (200,\"\")", st, msg)
		}
	})

	t.Run("ErrNoRows is 404, even wrapped", func(t *testing.T) {
		if st, _ := mapDBError(sql.ErrNoRows); st != http.StatusNotFound {
			t.Fatalf("ErrNoRows → %d, want 404", st)
		}
		wrapped := fmt.Errorf("load agent: %w", sql.ErrNoRows)
		if st, _ := mapDBError(wrapped); st != http.StatusNotFound {
			t.Fatalf("wrapped ErrNoRows → %d, want 404", st)
		}
	})

	t.Run("context cancel is 408", func(t *testing.T) {
		if st, _ := mapDBError(context.Canceled); st != http.StatusRequestTimeout {
			t.Fatalf("Canceled → %d, want 408", st)
		}
		if st, _ := mapDBError(context.DeadlineExceeded); st != http.StatusRequestTimeout {
			t.Fatalf("DeadlineExceeded → %d, want 408", st)
		}
	})

	t.Run("opaque error is 500 with generic message (no leak)", func(t *testing.T) {
		st, msg := mapDBError(errors.New("near \"SELCT\": syntax error in table secret_tbl"))
		if st != http.StatusInternalServerError {
			t.Fatalf("opaque → %d, want 500", st)
		}
		if msg != "internal error" {
			t.Fatalf("opaque message %q leaks; want generic \"internal error\"", msg)
		}
	})

	t.Run("real UNIQUE constraint violation is 409, message does not leak SQL", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer db.Close()
		ctx := context.Background()
		if _, err := db.ExecContext(ctx, `CREATE TABLE t (id TEXT PRIMARY KEY)`); err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO t (id) VALUES ('x')`); err != nil {
			t.Fatalf("insert 1: %v", err)
		}
		_, dupErr := db.ExecContext(ctx, `INSERT INTO t (id) VALUES ('x')`)
		if dupErr == nil {
			t.Fatal("expected a UNIQUE/PRIMARY KEY constraint error on the duplicate insert")
		}
		st, msg := mapDBError(dupErr)
		if st != http.StatusConflict {
			t.Fatalf("constraint → %d, want 409 (raw err was %q)", st, dupErr)
		}
		if msg != "constraint violation" {
			t.Fatalf("constraint message %q; want generic \"constraint violation\"", msg)
		}
	})
}
