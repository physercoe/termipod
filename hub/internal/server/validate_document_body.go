package server

import "strings"

// validateDocumentBody checks that the content_inline body of a
// documents.create call is non-empty after trimming whitespace. The
// existing handler already enforces the XOR with artifact_id; this
// validator catches the whitespace-only case that would otherwise
// create a doc with no readable content.
//
// Returns "" when valid, a structured error otherwise.
func validateDocumentBody(content string) string {
	if strings.TrimSpace(content) == "" {
		return "content_inline must be non-empty (whitespace-only is rejected)"
	}
	return ""
}
