// Package mcpver is the single place the Go tree declares which MCP wire
// revisions it speaks, and the single function that answers a client's
// ask (ADR-063 D1/D2).
//
// Before this package the same allow-list and the same negotiation
// function lived as three byte-identical copies — hub/internal/server
// (the /mcp/{token} endpoint), hub/internal/hubmcpserver (the stdio
// daemon), hub/internal/hostrunner (the UDS gateway) — each carrying a
// comment naming one of the others as canon. Three copies of a
// four-string list is a version bump waiting to be applied twice.
//
// The desktop browser bridge is the fourth server and cannot import Go;
// it mirrors this set in one exported constant
// (desktop/electron/src/browserbridge.ts). The two are pinned to each
// other by versions.json in this directory, which both languages' tests
// read — see TestSupportedMatchesFixture here and the parity test in
// browserbridge.test.ts. Adding a revision is one line here, one line
// there, and one fixture entry; a contributor who does fewer than three
// fails CI on both sides.
package mcpver

// Floor is the revision we answer with when the client's ask is one we
// do not implement — the oldest revision every MCP client understands.
//
// It is deliberately NOT "the newest thing we support". A server that
// answers an unknown ask with its own newest version is claiming
// semantics the client may then rely on; answering with the floor is
// the only honest thing a server can say about a revision it has not
// implemented (ADR-063 D2).
const Floor = "2024-11-05"

// supported is the set of revisions we speak, oldest first.
//
// Why echoing matters, and why this list is not decorative: the
// v1.0.649 incident (docs/changelog.md) — agy 1.0.1 sends
// `protocolVersion: 2025-11-25` and treats a downgrade in the
// initialize response as a FATAL protocol error. The transport dies
// with "client is closing: invalid request", the MCP surface vanishes
// mid-session, and the agent falls back to crawling the repo from the
// filesystem. So a revision whose semantics are no-ops for us belongs
// in this list: refusing to echo it costs a working session and buys
// nothing.
//
// 2026-07-28 is here because lane U implements its response side —
// resultType, ttlMs/cacheScope, structuredContent, server/discover,
// per-request _meta, MCP-Protocol-Version headers. Its request side
// (MRTR `inputResponses`) is unreachable: a client only sends those in
// reply to an `input_required` result, and no server of ours ever
// returns one. Claiming a revision whose request side we could not
// honour would be the anti-pattern D2 forbids; this one we can.
var supported = []string{
	"2024-11-05",
	"2025-03-26",
	"2025-06-18",
	"2025-11-25",
	"2026-07-28",
}

// Supported returns the revisions we speak, oldest first. The caller
// gets a copy — server/discover hands this straight to a client, and a
// mutation there must not reach the package's own set.
func Supported() []string {
	out := make([]string, len(supported))
	copy(out, supported)
	return out
}

// IsSupported reports whether we implement the given revision.
func IsSupported(v string) bool {
	for _, s := range supported {
		if s == v {
			return true
		}
	}
	return false
}

// Negotiate answers a client's requested protocol version: echo it when
// we know it, otherwise the floor. An empty ask (no protocolVersion in
// params, or no params at all) also gets the floor — it carries no
// claim to downgrade from.
//
// This is the whole of ADR-063 D2. Note what it never does: return the
// client's ask unchecked. The desktop bridge did exactly that before
// U2, which is worse than downgrading — a blind echo promises every
// semantic of a revision the server has never heard of.
func Negotiate(clientAsk string) string {
	return NegotiateFirst(clientAsk)
}

// NegotiateFirst is Negotiate over an ordered preference list: the first
// ask we implement wins, and the floor answers when none does.
//
// A single request can declare its revision in more than one place once
// 2026-07-28 is in play — the initialize params, the
// MCP-Protocol-Version header, and the `_meta` envelope — and they need
// not agree. Ranking them beats picking one and hoping: a client that
// declares a revision we know, anywhere, gets it.
func NegotiateFirst(asks ...string) string {
	for _, a := range asks {
		if IsSupported(a) {
			return a
		}
	}
	return Floor
}
