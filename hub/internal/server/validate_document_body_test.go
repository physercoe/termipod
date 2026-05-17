package server

import "testing"

func TestValidateDocumentBody_AcceptsRealContent(t *testing.T) {
	for _, in := range []string{
		"Hello world",
		"# Heading\n\nParagraph.",
		"x",
	} {
		if reason := validateDocumentBody(in); reason != "" {
			t.Errorf("real content %q rejected: %q", in, reason)
		}
	}
}

func TestValidateDocumentBody_RejectsWhitespaceOnly(t *testing.T) {
	for _, in := range []string{
		"",
		" ",
		"  \n\t  \n",
	} {
		if reason := validateDocumentBody(in); reason == "" {
			t.Errorf("whitespace input %q should be rejected", in)
		}
	}
}
