package server

import (
	"fmt"

	"github.com/termipod/hub/internal/events"
)

// validateEventParts checks that the parts array on a
// channels.post_event call has at least one part and that each part
// declares a kind plus the matching payload field.
//
// The Part shape supports several payload variants (text, data, file,
// image, excerpt) discriminated by the kind field. A part with kind
// "text" and an empty text payload is the silent-failure case the
// pre-bundle handler accepted — the event lands but renders blank.
//
// Returns "" when valid, a structured error otherwise.
func validateEventParts(parts []events.Part) string {
	if len(parts) == 0 {
		return "parts must be a non-empty array; each part needs a kind and matching payload"
	}
	for i, p := range parts {
		if p.Kind == "" {
			return fmt.Sprintf("parts[%d]: kind required", i)
		}
		switch p.Kind {
		case "text":
			if p.Text == "" {
				return fmt.Sprintf("parts[%d]: kind=text requires non-empty text", i)
			}
		case "file":
			if p.File == nil || p.File.URI == "" {
				return fmt.Sprintf("parts[%d]: kind=file requires file.uri", i)
			}
		case "image":
			if p.Image == nil || p.Image.URI == "" {
				return fmt.Sprintf("parts[%d]: kind=image requires image.uri", i)
			}
		case "excerpt":
			if p.Excerpt == nil {
				return fmt.Sprintf("parts[%d]: kind=excerpt requires excerpt payload", i)
			}
		case "data":
			if len(p.Data) == 0 {
				return fmt.Sprintf("parts[%d]: kind=data requires non-empty data", i)
			}
		default:
			// Unknown kinds are tolerated for forward-compat — a new
			// kind shipped by the client before the hub knows about it
			// shouldn't be rejected. The kind field itself is required
			// (caught above) so an empty-body event still fails.
		}
	}
	return ""
}
