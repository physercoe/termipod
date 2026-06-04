package server

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/termipod/hub"
	"github.com/termipod/hub/internal/hubmcpserver"
)

// TestBundledTemplatesUseCanonicalNames — WS1.1 drift-lock (was the ADR-033
// W6.4 alias check). WS1.1 retired every dotted/legacy tool alias, so calling
// one now 404s. Bundled agent + prompt + plan + envelope templates must
// therefore reference tools ONLY by their canonical snake_case name — a
// dotted reference would steer an agent into a guaranteed-404 call.
//
// The forbidden set is the authority Backends (the dotted REST-adapter names,
// derived from the registry so it can't drift) plus the handful of native
// dotted aliases WS1.1 removed (those are gone from the registry, so they're
// listed explicitly — a small, closed set that won't grow).
func TestBundledTemplatesUseCanonicalNames(t *testing.T) {
	dottedCanonical := map[string]string{}
	for _, s := range hubmcpserver.ToolRegistry() {
		if strings.Contains(s.Backend, ".") {
			dottedCanonical[s.Backend] = s.Name
		}
	}
	for dotted, canonical := range map[string]string{
		"agents.fanout":     "agents_fanout",
		"agents.gather":     "agents_gather",
		"reports.post":      "reports_post",
		"templates.propose": "templates_propose",
		"tools.get":         "tools_get",
		"request_decision":  "request_select",
	} {
		dottedCanonical[dotted] = canonical
	}
	if len(dottedCanonical) == 0 {
		t.Fatal("no dotted tool names collected — registry empty?")
	}

	parts := make([]string, 0, len(dottedCanonical))
	for a := range dottedCanonical {
		parts = append(parts, regexp.QuoteMeta(a))
	}
	// Bound the match to a whole tool token: the name must not be preceded by
	// a word char or `.` (so agents.get is not flagged inside a longer dotted
	// compound such as templates.agents.get, and the template-ID namespace
	// `agents.steward` — not a Backend — never matches). The trailing `\b`
	// keeps documents.create from matching inside documents.created. RE2 has
	// no look-behind, so the leading bound is a capture group; only group 2 —
	// the dotted name — is reported.
	re := regexp.MustCompile(`(^|[^A-Za-z0-9_.])(` + strings.Join(parts, "|") + `)\b`)

	for _, dir := range []string{"templates/agents", "templates/prompts", "templates/plans", "templates/envelope"} {
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
				dotted := m[2]
				t.Errorf("%s references retired dotted tool name %q — use the canonical %q (the dotted form 404s post-WS1.1)",
					path, dotted, dottedCanonical[dotted])
			}
		}
	}
}
