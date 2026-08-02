package mcpwire

import "testing"

func TestStampResult_MarksComplete(t *testing.T) {
	got := StampResult(map[string]any{"content": []any{}})
	if got["resultType"] != ResultTypeComplete {
		t.Errorf("resultType = %v, want %q", got["resultType"], ResultTypeComplete)
	}
}

// A stamping helper must never be the thing that takes a server down.
func TestStampers_TolerateNil(t *testing.T) {
	if StampResult(nil) != nil {
		t.Error("StampResult(nil) should stay nil")
	}
	if StampCacheableList(nil) != nil {
		t.Error("StampCacheableList(nil) should stay nil")
	}
	if WithServerInfo(nil, "a", "b") != nil {
		t.Error("WithServerInfo(nil, …) should stay nil")
	}
}

func TestStampCacheableList_IsCompleteAndPrivate(t *testing.T) {
	got := StampCacheableList(map[string]any{"tools": []any{}})
	if got["resultType"] != ResultTypeComplete {
		t.Errorf("a cacheable list is still a complete result; got %v", got["resultType"])
	}
	if got["ttlMs"] != ListTTLMillis {
		t.Errorf("ttlMs = %v, want %d", got["ttlMs"], ListTTLMillis)
	}
	// Never public: every list we serve is scoped to one caller's token,
	// so a shared intermediary caching it would hand one agent's surface
	// to another.
	if got["cacheScope"] != CacheScopePrivate {
		t.Errorf("cacheScope = %v, want %q", got["cacheScope"], CacheScopePrivate)
	}
}

// WithServerInfo must merge into an existing `_meta`, not replace it —
// a result that already carries other reserved keys would lose them.
func TestWithServerInfo_PreservesExistingMeta(t *testing.T) {
	got := WithServerInfo(map[string]any{
		"_meta": map[string]any{"vendor/custom": "keep me"},
	}, "termipod-hub", "1.2.3")
	meta := got["_meta"].(map[string]any)
	if meta["vendor/custom"] != "keep me" {
		t.Error("WithServerInfo clobbered an existing _meta key")
	}
	info, ok := meta[MetaServerInfo].(map[string]any)
	if !ok {
		t.Fatalf("no serverInfo under %s: %v", MetaServerInfo, meta)
	}
	if info["name"] != "termipod-hub" || info["version"] != "1.2.3" {
		t.Errorf("serverInfo = %v", info)
	}
}

// A non-map `_meta` (a client echoing junk, a future shape we do not
// know) must not panic the stamp — it is replaced, not merged into.
func TestWithServerInfo_ReplacesAnUnusableMeta(t *testing.T) {
	got := WithServerInfo(map[string]any{"_meta": "not a map"}, "n", "v")
	if _, ok := got["_meta"].(map[string]any); !ok {
		t.Fatalf("_meta = %#v, want a map", got["_meta"])
	}
}

func TestToolTitle(t *testing.T) {
	cases := map[string]string{
		"documents_list":          "Documents list",
		"host.ping":               "Host ping",
		"project-channels_create": "Project channels create",
		"":                        "",
		// Already capitalized, and a leading non-letter: both pass
		// through rather than being mangled.
		"Search":  "Search",
		"2fa_get": "2fa get",
	}
	for in, want := range cases {
		if got := ToolTitle(in); got != want {
			t.Errorf("ToolTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToolAnnotations_PublishesOnlyWhatWeKnow(t *testing.T) {
	ann := ToolAnnotations("documents_list", true)
	if ann["readOnlyHint"] != true {
		t.Errorf("readOnlyHint = %v", ann["readOnlyHint"])
	}
	if ann["title"] != "Documents list" {
		t.Errorf("title = %v", ann["title"])
	}
	// destructiveHint / idempotentHint / openWorldHint are never
	// published: nothing in either registry means any of them, the spec
	// already defaults destructiveHint to true for a mutating tool, and
	// asserting FALSE is the one direction that invites a client to
	// auto-approve something it should not.
	for _, k := range []string{"destructiveHint", "idempotentHint", "openWorldHint"} {
		if _, present := ToolAnnotations("agents_spawn", false)[k]; present {
			t.Errorf("%s is published; nothing justifies the claim", k)
		}
	}
}

func TestRequestMeta_ClientLabel(t *testing.T) {
	var m RequestMeta
	if got := m.ClientLabel(); got != "" {
		t.Errorf("a client that declared nothing should label as empty; got %q", got)
	}
	m.ClientInfo.Name = "kimi-cli"
	if got := m.ClientLabel(); got != "kimi-cli" {
		t.Errorf("ClientLabel() = %q", got)
	}
	m.ClientInfo.Version = "2.1"
	if got := m.ClientLabel(); got != "kimi-cli/2.1" {
		t.Errorf("ClientLabel() = %q", got)
	}
}

// The additive-safety boundary: `structuredContent` is a key the 2025
// revisions know AS AN OBJECT ({ [key: string]: unknown } in their
// schemas); SEP-2106's any-JSON widening is 2026-07-28-only. Since our
// responses are version-blind, only the shape legal in every revision
// that knows the key may be stamped — an array on a 2025-11-25 exchange
// is exactly the strict-validator teardown food this lane exists to
// stop serving.
func TestAttachStructuredContent_ObjectsOnly(t *testing.T) {
	obj := AttachStructuredContent(map[string]any{}, map[string]any{"id": "x"}, []byte(`{"id":"x"}`))
	if _, ok := obj["structuredContent"]; !ok {
		t.Error("an object value must be attached")
	}
	for name, tc := range map[string]struct {
		v any
		b string
	}{
		"array":  {[]any{map[string]any{"id": "x"}}, `[{"id":"x"}]`},
		"string": {"hi", `"hi"`},
		"number": {2, `2`},
		"null":   {nil, `null`},
	} {
		got := AttachStructuredContent(map[string]any{}, tc.v, []byte(tc.b))
		if _, ok := got["structuredContent"]; ok {
			t.Errorf("%s: a non-object value must stay text-only", name)
		}
	}
	if AttachStructuredContent(nil, map[string]any{}, []byte(`{}`)) != nil {
		t.Error("AttachStructuredContent(nil, …) should stay nil")
	}
}

// The relays consult this before stamping a client-held string into an
// HTTP header: both Go's and Node's clients reject control bytes at
// send time, so an unchecked stamp turns a parseable-but-hostile frame
// into a relay-side transport error instead of a server-side JSON-RPC
// answer.
func TestValidHeaderValue(t *testing.T) {
	for _, ok := range []string{"tools/call", "documents_list", "host.ping", "a b\tc"} {
		if !ValidHeaderValue(ok) {
			t.Errorf("%q should be stampable", ok)
		}
	}
	for _, bad := range []string{"", "evil\r\nX-Inject: 1", "nul\x00", "höst.ping"} {
		if ValidHeaderValue(bad) {
			t.Errorf("%q must not be stampable", bad)
		}
	}
}
