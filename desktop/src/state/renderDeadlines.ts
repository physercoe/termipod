/// The two deadlines on `author_render`'s diagram leg, side by side because
/// only their ORDER makes the slow-export failure mode legible.
///
/// Dependency-free on purpose (the `ui_policy.ts` discipline): the export half
/// belongs to the renderer's draw.io adapter and the transport half to the
/// Electron main process, and the main-side tsconfig has no DOM — so the one
/// module both may import can import nothing itself.

/// How long the draw.io adapter waits for an `export` reply. Generous —
/// draw.io rasterizes a large sheet on the main thread — and bounded, because
/// the alternative is a caller parked on an iframe that will never reply.
export const EXPORT_TIMEOUT_MS = 20_000;

/// How long main waits for the renderer to answer a `render` op — its OWN
/// deadline, not the 15s every other author op gets, because a render legally
/// contains a whole `EXPORT_TIMEOUT_MS` plus rasterizing and encoding.
///
/// The invariant (pinned in renderDoc.test.ts) is transport > export: the
/// adapter's deadline must fire while main is still listening, or its message
/// ("draw.io did not answer within 20s") is composed for nobody, the agent
/// gets the generic transport timeout instead, and the one-in-flight export
/// lock outlives the call it served — a retry in that window is told another
/// export is in flight when nothing is being waited on.
export const RENDER_TRANSPORT_TIMEOUT_MS = 25_000;
