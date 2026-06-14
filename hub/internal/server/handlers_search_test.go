package server

import (
	"net/http"
	"testing"
)

// Malformed FTS5 query syntax (e.g. unbalanced quotes) → 400, not 500.
func TestSearch_MalformedQueryReturns400(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, _ := doReq(t, s, token, http.MethodGet,
		"/v1/search?q=%22", nil)
	if status != http.StatusBadRequest {
		t.Errorf("malformed q: status=%d; want 400", status)
	}
}
