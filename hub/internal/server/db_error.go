package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	sqlite "modernc.org/sqlite"
)

// SQLite primary result codes (the low byte of the extended code, which is
// what *sqlite.Error.Code() returns since extendedResultCodes is enabled).
// We map a handful to meaningful HTTP statuses; everything else is a 500.
const (
	sqliteConstraintCode = 19 // SQLITE_CONSTRAINT (UNIQUE / FOREIGN KEY / NOT NULL / CHECK)
	sqliteFullCode       = 13 // SQLITE_FULL (disk/db full)
	sqliteReadonlyCode   = 8  // SQLITE_READONLY
)

// mapDBError translates a database error into a CLIENT-SAFE (status, message)
// pair. This is the #74 fix: raw SQLite/driver text — constraint details,
// table/column names, query fragments, syntax errors — must never reach a
// client response (information disclosure). Known classes get a stable,
// generic message; everything else collapses to 500 "internal error". The
// caller is responsible for logging the real err server-side (see
// (*Server).writeDBErr).
//
//   - sql.ErrNoRows                  → 404 "not found"
//   - context cancel / deadline      → 499-ish (we use 408) "request cancelled"
//   - SQLITE_CONSTRAINT              → 409 "constraint violation"
//   - SQLITE_FULL                    → 507 "storage full"
//   - SQLITE_READONLY               → 503 "database read-only"
//   - anything else (incl. syntax)   → 500 "internal error"
func mapDBError(err error) (int, string) {
	switch {
	case err == nil:
		return http.StatusOK, ""
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound, "not found"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return http.StatusRequestTimeout, "request cancelled"
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() & 0xFF {
		case sqliteConstraintCode:
			return http.StatusConflict, "constraint violation"
		case sqliteFullCode:
			return http.StatusInsufficientStorage, "storage full"
		case sqliteReadonlyCode:
			return http.StatusServiceUnavailable, "database temporarily read-only"
		}
	}
	return http.StatusInternalServerError, "internal error"
}

// writeDBErr is the safe replacement for the pervasive
// `writeErr(w, http.StatusInternalServerError, err.Error())` pattern (#74).
// It maps the error to a client-safe status+message via mapDBError, logs the
// RAW error server-side on any 5xx (so operators keep full diagnostics), and
// writes the sanitized envelope. A nil err is a no-op caller bug — guarded so
// it can't accidentally write a 200 error body.
func (s *Server) writeDBErr(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	status, msg := mapDBError(err)
	if status >= http.StatusInternalServerError && s.log != nil {
		s.log.Error("unhandled db error", "status", status, "err", err)
	}
	writeErr(w, status, msg)
}
