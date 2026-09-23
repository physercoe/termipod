# Mobile SSH browser

> **Type:** reference
> **Status:** Current (2026-09-23)
> **Audience:** contributors and reviewers
> **Last verified vs code:** 2026.824.751-alpha + mobile recovery changes
> **Freshness:** rolling

**TL;DR.** SSH transport health is independent of terminal commands. Tunnel
browser storage is isolated even on Android WebViews without multiple profiles.

## SSH recovery boundaries

Authentication completes before optional tmux discovery or polling-shell startup.
Terminal routes call `prepareTerminal`; web-only routes do not start shells.
Discovery and shell startup have separate bounded budgets. A terminal setup
failure must not discard an authenticated connection.
<!-- verify symbol lib/services/ssh/ssh_client.dart prepareTerminal -->

Resume, reopening and keep-alive share one protocol-level probe. A pending reply
gets two five-second windows, not another shell command or another SSH request.
Transport EOF is observed directly. Genuine dial/authentication failures still
follow the provider's retry policy; this does not promise one attempt on a
network that is actually unavailable.

Command deadlines cover queueing, channel creation and output. Timed-out queued
commands are not sent; late channels and failed channels are closed. An uncertain
persistent-shell command is never automatically replayed on a new channel.
<!-- verify file lib/services/ssh/command_executor.dart -->

## Android browser compatibility

When `MULTI_PROFILE` is available, each embedded browser uses its own named
WebView profile. Otherwise, Android 9/API 28 and newer use a native Activity in
the private `:tunnel_browser` process. Before creating WebView, that process
selects a fresh UUID-based data-directory suffix. It never uses the main
process's default WebView storage.
<!-- verify file android/app/src/main/kotlin/com/remoteagent/termipod/CompatTunnelActivity.kt -->

The compatibility Activity owns one browsing lifetime. Closing it ends only
its browser process, not Flutter or SSH. Browser data is not reused; abandoned
compatibility directories are removed before the next compatibility browser is
initialized. This is storage isolation, not a separate Android UID or an
additional network sandbox. Storage cleanup is not a secure-erasure guarantee.

The Flutter process keeps the loopback listener and SSH foreground service.
Native foreground-resume events request SSH recovery without automatically
reloading a website. Page reload remains an explicit user action. The
compatibility screen supplies close, back, forward and reload controls.

Both Android modes restrict main-frame navigation, including POST requests, to
the selected loopback HTTP origin. Neither exposes a JavaScript-native bridge,
grants file access, nor bypasses TLS checks. Subresources can still contact the
network directly; the browser is not a general-purpose web proxy.
<!-- verify file android/app/src/main/kotlin/com/remoteagent/termipod/TunnelOrigin.kt -->

Unsupported capabilities and initialization failures have distinct messages.
Provider package/version are exposed by the capability query for diagnosis.
Android below API 28 still needs a provider with multiple-profile support.

## Verification boundaries

Dart tests exercise the real wrapper with controlled protocol transports,
command-channel timeout cleanup, concurrent reopening and delayed replies.
Widget tests cover compatibility-mode selection and initialization errors.
Android JVM tests cover the shared navigation policy; CI builds Android and
iOS and runs Android release lint. Physical-device checks remain necessary for
WebView process lifecycle, cookie separation, rotation and background/resume.
