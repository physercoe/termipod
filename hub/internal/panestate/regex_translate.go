package panestate

import (
	"fmt"
	"regexp"
	"strings"
)

// Regex dialect translation: Rust `regex` (upstream) -> Go RE2.
//
// The vendored manifests are byte-exact and are never hand-edited (plan D-1),
// so where the two engines' pattern syntax differs the translation happens
// HERE, at compile time, and is recorded — not applied silently and not
// papered over by editing the vendored file.
//
// 9 of the 58 vendored patterns need it. Both rules below are pinned by
// TestRegexTranslations, and every applied translation is attached to the
// compiled rule so P4's explain surface can show that the pattern it ran is
// not byte-identical to the pattern in the file.
//
// A pattern that still fails to compile after translation is a load ERROR.
// We do not drop the rule: a manifest that half-loads is a detector with a
// silent hole, which is the failure mode this whole plan removes.

var (
	// \uXXXX and \u{XXXX} are Rust-regex spellings of a code point. RE2
	// spells it \x{XXXX}. Faithful and total — same code point, same
	// semantics.
	reUnicodeEscape = regexp.MustCompile(`\\u\{?([0-9A-Fa-f]{1,6})\}?`)

	// \p{Alphabetic} is a Unicode *binary property*. RE2 supports general
	// categories and scripts but not binary properties, so there is no exact
	// equivalent.
	//
	// Unicode defines Alphabetic = L + Nl + Other_Alphabetic. [\p{L}\p{Nl}]
	// captures the first two exactly and drops Other_Alphabetic, which is
	// combining vowel signs and similar marks (Mn/Mc). Every vendored use is
	// of the form `\p{Alphabetic}+\w*ing\b` or `\p{Alphabetic}` immediately
	// after a spinner glyph — i.e. matching the first letters of an English
	// status word like "Thinking" — so the dropped set cannot occur there.
	//
	// This is an APPROXIMATION and is labelled as one. It is recorded per
	// rule rather than assumed harmless, because "harmless because X" is its
	// own claim and X is about the manifests as they are TODAY.
	reAlphabetic = regexp.MustCompile(`\\p\{Alphabetic\}`)
)

// TranslationNote records one applied dialect fix.
type TranslationNote struct {
	Rule     string // which translation rule fired
	Original string
	Result   string
	// Exact is false when the translation changes the matched set, however
	// narrowly. A reviewer should be able to find every inexact one by
	// filtering on this field.
	Exact bool
}

// translateRegex rewrites an upstream pattern into RE2 syntax, returning the
// new pattern and any notes. A pattern needing no translation returns
// unchanged with no notes.
func translateRegex(pattern string) (string, []TranslationNote) {
	var notes []TranslationNote
	out := pattern

	if reUnicodeEscape.MatchString(out) {
		translated := reUnicodeEscape.ReplaceAllString(out, `\x{$1}`)
		notes = append(notes, TranslationNote{
			Rule:     `\uXXXX -> \x{XXXX}`,
			Original: out,
			Result:   translated,
			Exact:    true,
		})
		out = translated
	}

	if reAlphabetic.MatchString(out) {
		translated := reAlphabetic.ReplaceAllString(out, `[\p{L}\p{Nl}]`)
		notes = append(notes, TranslationNote{
			Rule:     `\p{Alphabetic} -> [\p{L}\p{Nl}] (drops Other_Alphabetic; RE2 has no binary properties)`,
			Original: out,
			Result:   translated,
			Exact:    false,
		})
		out = translated
	}

	return out, notes
}

// compileTranslated compiles a pattern through the dialect translation,
// reporting what it had to change.
func compileTranslated(pattern string) (*regexp.Regexp, []TranslationNote, error) {
	translated, notes := translateRegex(pattern)
	re, err := regexp.Compile(translated)
	if err != nil {
		if len(notes) > 0 {
			return nil, notes, fmt.Errorf(
				"pattern %q (translated to %q) does not compile under RE2: %w",
				pattern, translated, err)
		}
		return nil, nil, fmt.Errorf("pattern %q does not compile under RE2: %w", pattern, err)
	}
	return re, notes, nil
}

// describeNotes renders translation notes for an error or explain surface.
func describeNotes(notes []TranslationNote) string {
	if len(notes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(notes))
	for _, n := range notes {
		parts = append(parts, n.Rule)
	}
	return strings.Join(parts, "; ")
}
