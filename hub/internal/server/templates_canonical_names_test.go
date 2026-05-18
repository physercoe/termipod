package server

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/termipod/hub"
	"github.com/termipod/hub/internal/hubmcpserver"
)

// TestBundledTemplatesUseCanonicalNames — ADR-033 W6.4 drift-lock. The
// bundled agent + prompt templates must reference tools by their
// canonical snake_case name, not a deprecated alias. The aliases still
// resolve at dispatch, so this is hygiene — but a contributor reading a
// template should see the name tools/list advertises as canonical.
//
// The deprecated-alias set is derived from both ToolSpec registries, so
// it cannot drift out of sync with the catalog.
func TestBundledTemplatesUseCanonicalNames(t *testing.T) {
	aliasCanonical := map[string]string{}
	collect := func(specs []hubmcpserver.ToolSpec) {
		for _, s := range specs {
			for _, a := range s.Aliases {
				aliasCanonical[a] = s.Name
			}
		}
	}
	collect(hubmcpserver.ToolRegistry())
	collect(nativeToolRegistry())
	if len(aliasCanonical) == 0 {
		t.Fatal("no deprecated aliases collected — registries empty?")
	}

	parts := make([]string, 0, len(aliasCanonical))
	for a := range aliasCanonical {
		parts = append(parts, regexp.QuoteMeta(a))
	}
	// Bound the match to a whole tool token: the alias must not be
	// preceded by a word char or `.` (so agents.get is not flagged
	// inside a longer dotted compound such as templates.agents.get).
	// The trailing `\b` keeps documents.create from matching inside
	// documents.created. RE2 has no look-behind, so the leading bound
	// is a capture group; only group 2 — the alias — is reported.
	re := regexp.MustCompile(`(^|[^A-Za-z0-9_.])(` + strings.Join(parts, "|") + `)\b`)

	for _, dir := range []string{"templates/agents", "templates/prompts"} {
		entries, err := fs.ReadDir(hub.TemplatesFS, dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			path := dir + "/" + e.Name()
			body, err := fs.ReadFile(hub.TemplatesFS, path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			for _, m := range re.FindAllStringSubmatch(string(body), -1) {
				alias := m[2]
				t.Errorf("%s references deprecated tool alias %q — use the canonical %q",
					path, alias, aliasCanonical[alias])
			}
		}
	}
}
