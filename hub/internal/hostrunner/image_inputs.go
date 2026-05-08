// ADR-021 Phase 4 — image content block extraction.
//
// The hub validates inbound `images: [{mime_type, data}]` payloads
// once (W4.1) and persists them onto agent_events.payload_json. Each
// driver re-shapes the same canonical list into its engine-native
// content block (Anthropic image_source, OpenAI input_image, ACP
// image). extractImageInputs is the shared decoder so the per-driver
// shape mappers in W4.2-W4.4 don't repeat the type-assertion ladder.
package hostrunner

// imageInput is the decoded form. We don't carry the raw bytes — the
// drivers just relay the base64 string downstream — so a tiny struct
// is enough.
type imageInput struct {
	mime string
	data string
}

// extractImageInputs reads payload["images"] (the shape produced by
// json.Unmarshal of the hub-side []imageInput → []any of map[string]any)
// and returns the validated subset. Malformed entries (missing mime or
// data) are dropped silently because the hub's W4.1 validator already
// rejected them at ingest; if one slipped through, dropping is safer
// than passing nonsense to the engine.
func extractImageInputs(payload map[string]any) []imageInput {
	raw, ok := payload["images"].([]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make([]imageInput, 0, len(raw))
	for _, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		mime, _ := m["mime_type"].(string)
		data, _ := m["data"].(string)
		if mime == "" || data == "" {
			continue
		}
		out = append(out, imageInput{mime: mime, data: data})
	}
	return out
}
