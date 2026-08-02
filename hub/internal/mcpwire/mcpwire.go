// Package mcpwire holds the response-shape vocabulary of the MCP
// revisions we speak, and the stamping helpers our four servers apply to
// every result (ADR-063 D3).
//
// It is the sibling of internal/mcpver: mcpver answers "which revision
// is this exchange", mcpwire answers "what shape does a result of that
// revision have". They are separate because the version set changes when
// a revision ships and the wire vocabulary changes when we adopt one —
// different reasons to edit, so different files.
//
// Everything here is ADDITIVE-FIRST (ADR-063 D3): these fields are
// stamped on every response regardless of the negotiated version,
// because a client on an older revision ignores JSON keys it does not
// know. That keeps one code path for responses, and confines version
// branching to the places where meaning actually differs (request-side
// semantics). The 2026-07-28 spec explicitly blesses the reverse
// direction too — clients "MUST treat results from earlier-protocol
// servers that omit `resultType` as `complete`" — so stamping it early
// can never be misread.
//
// The desktop browser bridge is the fourth server and cannot import Go;
// it mirrors these constants in browserbridge.ts.
package mcpwire

// ResultTypeComplete is the `resultType` every ordinary result carries
// (2026-07-28 SEP-2322). The other value in the spec, "input_required",
// belongs to Multi Round-Trip Requests — a server returns it to ask the
// client for more input mid-call, and the client retries with
// `inputResponses`.
//
// We never return it. Our one shaped-like-MRTR flow (the browser-bridge
// approval card, which blocks a tools/call while an attention card
// waits) is plan lane B1, gated on the first engine that negotiates
// 2026-07-28. Until then every result we produce is complete by
// construction, which is why this is a constant and not a parameter.
const ResultTypeComplete = "complete"

// Cache scopes for CacheableResult (2026-07-28 SEP-2549).
const (
	// CacheScopePrivate: only the requesting client may cache this.
	// Every list we serve is scoped to ONE agent's token — the hub
	// filters tools by the caller's role, and the desktop bridge hides
	// the action tools from a read-scoped session — so a shared
	// intermediary caching one agent's catalog and serving it to
	// another would hand out a surface the second agent is not
	// entitled to. "private" is a correctness requirement here, not a
	// tuning knob; nothing we serve is ever CacheScopePublic.
	CacheScopePrivate = "private"
	CacheScopePublic  = "public"
)

// ListTTLMillis is the freshness hint on cacheable list results.
//
// Five minutes. A catalog changes only when someone flips the desktop
// sharing toggle, reinstalls hooks, or ships a new hub build — human-
// paced events, none of them sub-minute. The number is a bound on how
// long a client may show a stale catalog, and it exists because we
// advertise `listChanged: false` everywhere: we have no push channel to
// correct a stale list, so the TTL is the only thing that ever does.
// Plan lane B5 (`subscriptions/listen`) is what would let us shorten or
// retire it.
const ListTTLMillis = 300000

// Standard MCP HTTP headers (2026-07-28 SEP-2243). On Streamable HTTP
// these let a gateway, WAF, or audit ring route and meter a request
// without parsing its body — which is exactly what our relays are for.
const (
	// HeaderProtocolVersion carries the revision an exchange is
	// speaking. On a request it is the client's declaration; on a
	// response it is what the server actually served.
	HeaderProtocolVersion = "MCP-Protocol-Version"
	// HeaderMethod / HeaderName mirror the JSON-RPC method and, for
	// tools/call, the tool name.
	HeaderMethod = "Mcp-Method"
	HeaderName   = "Mcp-Name"
)

// Reserved `_meta` keys (2026-07-28 SEP-2575). The stateless core
// retired the initialize handshake: a request carries its own version,
// identity, and capabilities in `_meta`, and a result carries the
// server's identity back the same way.
const (
	MetaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	MetaClientInfo         = "io.modelcontextprotocol/clientInfo"
	MetaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	MetaServerInfo         = "io.modelcontextprotocol/serverInfo"
)

// StampResult marks an ordinary result complete. Returns its argument so
// it can wrap a result literal at the point of construction:
//
//	return mcpwire.StampResult(map[string]any{"content": …})
//
// A nil map is returned untouched rather than panicking: some methods
// legitimately answer with no result body, and a stamping helper must
// never be the thing that takes a server down.
func StampResult(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	m["resultType"] = ResultTypeComplete
	return m
}

// StampCacheableList adds the CacheableResult fields to a list result,
// on top of StampResult. Applies to tools/list, resources/list,
// resources/read, prompts/list and resources/templates/list — the five
// the spec names — plus server/discover, whose result the client's
// response-cache layer reads the same way.
func StampCacheableList(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	StampResult(m)
	m["ttlMs"] = ListTTLMillis
	m["cacheScope"] = CacheScopePrivate
	return m
}

// WithServerInfo attaches the server's identity to a result's `_meta`,
// which is where 2026-07-28 moved it from the initialize response.
//
// Note what this is NOT for: the spec is explicit that serverInfo and
// clientInfo are "self-reported and intended for display, logging, and
// debugging — do not use them for behavior or security decisions". Ours
// is authenticated by the token on the transport, never by this name.
func WithServerInfo(m map[string]any, name, version string) map[string]any {
	if m == nil {
		return nil
	}
	meta, _ := m["_meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		m["_meta"] = meta
	}
	meta[MetaServerInfo] = map[string]any{"name": name, "version": version}
	return m
}

// AttachStructuredContent adds `structuredContent` to a tool result when
// — and only when — the marshaled value is a JSON object.
//
// The restriction is the point, not a shortcut. Our responses are
// version-blind (additive-first, D3), and `structuredContent` is a key
// the 2025 revisions already KNOW — as an object type
// (`{ [key: string]: unknown }` in the 2025-06-18/2025-11-25 schemas).
// SEP-2106 widening it to any JSON value is 2026-07-28-only, so an
// array-valued list result stamped onto a 2025-11-25 exchange is a
// shape that revision's schema rejects — handed to the one client class
// (strict validators, agy 1.0.1) whose teardown this whole lane exists
// to prevent. Unknown keys are safely ignored; known keys with a
// widened type are not. Objects were legal from the key's first
// appearance, so they are the additive-safe subset; everything else
// keeps the text block only.
func AttachStructuredContent(m map[string]any, v any, marshaled []byte) map[string]any {
	if m == nil || v == nil {
		return m
	}
	if len(marshaled) > 0 && marshaled[0] == '{' {
		m["structuredContent"] = v
	}
	return m
}

// ValidHeaderValue reports whether s is safe to send as an HTTP header
// value: non-empty printable ASCII (plus space and tab), which covers
// every JSON-RPC method and canonical tool name we ever stamp.
//
// The relays check this before stamping Mcp-Method/Mcp-Name from a
// client-held frame. Both Go's and Node's HTTP clients reject a header
// value carrying control bytes at send time — so without this check a
// PARSEABLE frame whose method or tool name embeds `\r\n` dies at the
// relay with a transport error instead of reaching the server that
// would have answered it with a proper JSON-RPC error. The relay's
// contract is byte pump first, labeller second: an unstampable value is
// simply not stamped, and the frame still goes through.
func ValidHeaderValue(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if b := s[i]; (b < 0x20 && b != '\t') || b == 0x7f || b > 0x7f {
			return false
		}
	}
	return true
}

// ToolAnnotations renders the MCP-standard `annotations` object for one
// tool catalog entry (ADR-063 D5 — our registry's truth, dual-published).
//
// Dual-published, not migrated: our custom keys (`concurrency_safe`,
// `side_effecting`, `tier`, `see_also`, …) stay exactly as they are. They
// carry a richer vocabulary than the spec's hints, and our own clients
// read them. What changes is that a spec-aware client no longer has to
// know our vocabulary to learn the one fact that drives its auto-approve
// UX: does this tool mutate anything.
//
// What is deliberately NOT here:
//
//   - `destructiveHint`. Nothing in our registries means "destructive" —
//     ReadOnly=false covers creating a document and terminating an agent
//     alike, and Tier is permission weight, not blast radius. The spec
//     already defaults the hint to true for a non-readOnly tool, which
//     is the fail-safe reading, so omitting it costs nothing. Asserting
//     it FALSE is the only direction that can cause harm — it invites a
//     client to auto-approve a mutating call — and no field we have
//     would justify that claim. Treat any future change here as a
//     security review item.
//   - `idempotentHint` / `openWorldHint`. Same reason: no key means
//     either, and a guess published as a hint is worse than silence.
func ToolAnnotations(name string, readOnly bool) map[string]any {
	return map[string]any{
		"title":        ToolTitle(name),
		"readOnlyHint": readOnly,
	}
}

// ToolTitle derives a human-readable label from a canonical tool name:
// `documents_list` → `Documents list`, `host.ping` → `Host ping`.
//
// Neither registry has a display-name field, and publishing `title`
// identical to `name` would be noise — so the title is the name made
// readable, derived deterministically rather than authored as a second
// string that would drift from the first.
func ToolTitle(name string) string {
	if name == "" {
		return ""
	}
	out := []rune(name)
	for i, r := range out {
		if r == '_' || r == '-' || r == '.' {
			out[i] = ' '
		}
	}
	if out[0] >= 'a' && out[0] <= 'z' {
		out[0] -= 'a' - 'A'
	}
	return string(out)
}

// RequestMeta is the per-request `_meta` envelope, decoded from a
// request's params. Every field is optional — a 2025-era client sends
// none of them, and the zero value is the correct reading of "this
// client told us nothing about itself".
type RequestMeta struct {
	ProtocolVersion string `json:"io.modelcontextprotocol/protocolVersion"`
	ClientInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"io.modelcontextprotocol/clientInfo"`
}

// ClientLabel renders the declared client identity for an audit line, or
// "" when the client declared none. Display and logging only — see
// WithServerInfo on why this must never gate behaviour.
func (m RequestMeta) ClientLabel() string {
	if m.ClientInfo.Name == "" {
		return ""
	}
	if m.ClientInfo.Version == "" {
		return m.ClientInfo.Name
	}
	return m.ClientInfo.Name + "/" + m.ClientInfo.Version
}
