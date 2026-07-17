//! Shared HTTP client construction with explicit proxy control.
//!
//! Every outbound `reqwest` client in the app funnels through here so the
//! Network settings tab's per-connection proxy toggles are honoured uniformly.
//! `proxy` is the resolved proxy URL the frontend passes when a connection's
//! toggle is ON (`Some`), or `None` when it's OFF / unset.
//!
//! `None` yields a client with proxying explicitly DISABLED (`.no_proxy()`) — a
//! real direct connection — rather than reqwest's default of silently picking up
//! the `*_PROXY` environment variables. That keeps a toggled-off connection
//! genuinely direct; the env/system proxy is instead surfaced to the frontend
//! via `system_proxy` and sent back through `proxy` only when the toggle is on.

/// A `reqwest::ClientBuilder` pre-configured with the caller's proxy choice. The
/// caller adds its own headers / timeout / redirect policy, then `.build()`s.
pub(crate) fn client_builder(proxy: Option<&str>) -> reqwest::ClientBuilder {
    let b = reqwest::Client::builder();
    match proxy.map(str::trim).filter(|p| !p.is_empty()) {
        Some(p) => match reqwest::Proxy::all(p) {
            Ok(px) => b.proxy(px),
            // A malformed proxy URL must not silently fall back to env proxies;
            // treat it as direct so behaviour stays predictable.
            Err(_) => b.no_proxy(),
        },
        None => b.no_proxy(),
    }
}
