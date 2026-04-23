package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// seedTestBlob inserts a blobs row with the given sha so run_images
// FK references can resolve without hitting the disk-backed upload path.
func seedTestBlob(t *testing.T, s *Server, sha string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT OR IGNORE INTO blobs (sha256, scope_path, size, mime, created_at)
		VALUES (?, '', 1, 'image/png', ?)`, sha, NowUTC()); err != nil {
		t.Fatalf("seed blob %s: %v", sha, err)
	}
}

func TestPostRunImages_InsertsAndLists(t *testing.T) {
	s, token := newA2ATestServer(t)
	runID := seedTestRun(t, s, defaultTeamID)
	for _, sha := range []string{"aaa000", "bbb111", "ccc222", "aaa-updated"} {
		seedTestBlob(t, s, sha)
	}

	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID + "/images"

	status, body := doReq(t, s, token, http.MethodPost, base, map[string]any{
		"images": []map[string]any{
			{"metric_name": "samples/generations", "step": 0,
				"blob_sha": "aaa000", "caption": "step 0"},
			{"metric_name": "samples/generations", "step": 500,
				"blob_sha": "bbb111"},
			{"metric_name": "samples/attention", "step": 500,
				"blob_sha": "ccc222"},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("post: status=%d body=%s", status, body)
	}

	status, body = doReq(t, s, token, http.MethodGet, base, nil)
	if status != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", status, body)
	}
	var rows []runImageOut
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}

	// Filter by metric.
	status, body = doReq(t, s, token, http.MethodGet,
		base+"?metric=samples/generations", nil)
	if status != http.StatusOK {
		t.Fatalf("get filtered: status=%d body=%s", status, body)
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode filtered: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("filtered: got %d rows, want 2", len(rows))
	}

	// Re-POST the same (run, metric, step) with a different sha: UPSERT
	// should update in place, not create a new row.
	status, _ = doReq(t, s, token, http.MethodPost, base, map[string]any{
		"images": []map[string]any{
			{"metric_name": "samples/generations", "step": 0,
				"blob_sha": "aaa-updated", "caption": "updated"},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("re-post: status=%d", status)
	}
	status, body = doReq(t, s, token, http.MethodGet,
		base+"?metric=samples/generations", nil)
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode after upsert: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("after upsert: got %d rows, want 2 (step 0 should have been updated, not inserted)", len(rows))
	}
	var step0 *runImageOut
	for i := range rows {
		if rows[i].Step == 0 {
			step0 = &rows[i]
			break
		}
	}
	if step0 == nil || step0.BlobSHA != "aaa-updated" {
		t.Fatalf("step 0 sha not updated; rows=%+v", rows)
	}
}

func TestPostRunImages_Validation(t *testing.T) {
	s, token := newA2ATestServer(t)
	runID := seedTestRun(t, s, defaultTeamID)
	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID + "/images"

	// empty images[]
	status, _ := doReq(t, s, token, http.MethodPost, base,
		map[string]any{"images": []any{}})
	if status != http.StatusBadRequest {
		t.Errorf("empty images: got %d, want 400", status)
	}

	// missing metric_name
	status, _ = doReq(t, s, token, http.MethodPost, base, map[string]any{
		"images": []map[string]any{{"step": 1, "blob_sha": "x"}},
	})
	if status != http.StatusBadRequest {
		t.Errorf("missing metric_name: got %d, want 400", status)
	}

	// missing blob_sha
	status, _ = doReq(t, s, token, http.MethodPost, base, map[string]any{
		"images": []map[string]any{{"metric_name": "m", "step": 1}},
	})
	if status != http.StatusBadRequest {
		t.Errorf("missing blob_sha: got %d, want 400", status)
	}

	// unknown run — tested *before* a valid (run, blob) pair ever hits the
	// insert path, so we only need a sha that passes the "required" check.
	bogus := "/v1/teams/" + defaultTeamID + "/runs/nope/images"
	status, _ = doReq(t, s, token, http.MethodPost, bogus, map[string]any{
		"images": []map[string]any{{"metric_name": "m", "step": 1, "blob_sha": "x"}},
	})
	if status != http.StatusNotFound {
		t.Errorf("unknown run: got %d, want 404", status)
	}
}
