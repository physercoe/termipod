package server

import (
	"context"
	"encoding/json"
	"strings"
)

// Agent-event payload externalization (hub-scaling lever 1, store-separation
// discussion §2). agent_events.payload_json holds the transcript inline, so a
// single oversized field — most often an `attach` tool_call carrying multi-MB
// base64 `content`/`content_base64` — bloats the row and never leaves (no
// retention). The blob store already exists (handlers_blobs.go: content-
// addressed, deduped, 25 MiB cap) and the `blob:sha256/<hex>` ref scheme is
// already understood across artifacts, A2A parts, and the mobile viewers; the
// gap is that using it was *advisory* (the agent had to choose the path
// marker). This externalizes large string leaves at the hub boundary so the
// discipline is enforced, not hoped for.
//
// On ingest, any JSON string value over payloadExternalizeThreshold is written
// to the blob store and replaced in place with its `blob:sha256/<hex>` ref. It
// is LOSSLESS (the sha reconstructs the exact bytes; blobs are durable and in
// backup.go) and content-addressed (identical payloads dedup). Reads stay
// lazy: a consumer fetches the bytes via GET /v1/blobs/<sha> only when needed —
// the transcript carries a short ref instead of multi-MB base64.
//
// It does NOT disturb the digest fold: the fold's json_extract paths
// ($.name, $.id, $.status, $.is_error, token counts) are all small scalars,
// never the externalized big leaf. Trade-off: an externalized field's text is
// no longer in the FTS index (you don't full-text-search multi-MB base64) and
// object key order is normalized by re-marshal (JSON key order is not
// semantically significant; no consumer depends on it).
const payloadExternalizeThreshold = 64 * 1024 // 64 KiB per string leaf

// blobRefPrefix is the content-addressed ref scheme already used for artifact
// URIs, A2A file parts, and document references (and resolved by the mobile
// viewers to /v1/blobs/<sha>).
const blobRefPrefix = "blob:sha256/"

// externalizeLargePayload returns payload with every oversized string leaf
// replaced by a blob ref, or the original payload unchanged when nothing
// qualifies (the common case: a small event short-circuits on the length
// check, never parsing JSON). Best-effort: any blob-store error leaves that
// leaf inline (a fat row beats a dropped event), logged by the caller path.
func (s *Server) externalizeLargePayload(ctx context.Context, payload string) string {
	// Fast path: if the whole payload is under the threshold, no single leaf
	// can exceed it — skip parsing entirely on the ingest hot path.
	if len(payload) <= payloadExternalizeThreshold {
		return payload
	}
	// UseNumber keeps numbers as their exact source text so re-marshal can't
	// corrupt a large int64 (token counts, cents) via float64 round-trip.
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return payload // not JSON we can walk; leave verbatim
	}
	changed := false
	nv := s.externalizeValue(ctx, v, &changed)
	if !changed {
		return payload
	}
	b, err := json.Marshal(nv)
	if err != nil {
		return payload
	}
	return string(b)
}

// externalizeValue recursively replaces oversized string leaves with blob refs,
// setting *changed when it does. Numbers (json.Number), bools, and null fall
// through unchanged.
func (s *Server) externalizeValue(ctx context.Context, v any, changed *bool) any {
	switch t := v.(type) {
	case string:
		if len(t) <= payloadExternalizeThreshold || strings.HasPrefix(t, blobRefPrefix) {
			return t
		}
		sha, err := s.storeBlob(ctx, []byte(t), "application/octet-stream")
		if err != nil {
			s.log.Warn("payload externalize", "err", err, "bytes", len(t))
			return t // keep inline rather than lose data
		}
		*changed = true
		return blobRefPrefix + sha
	case map[string]any:
		for k, val := range t {
			t[k] = s.externalizeValue(ctx, val, changed)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = s.externalizeValue(ctx, val, changed)
		}
		return t
	default:
		return v
	}
}
