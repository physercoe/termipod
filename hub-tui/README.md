# hub-tui

Terminal client for a running **Termipod Hub** — what the mobile app shows,
but over SSH on a laptop. Built with [Ink](https://github.com/vadimdemedes/ink)
on [Bun](https://bun.sh), so the whole thing starts in ~30ms and fits in a
single `bun run`.

## Why

The mobile app is great for glance-reading; the terminal is where operators
actually live. Same REST + SSE endpoints, different viewport.

Initial views:

- **Pending** — open `attention_items`, approve / reject inline
- **Feed** — live SSE stream of a chosen channel

Planned: Tasks, Templates, post-excerpt jump-back.

## Running

```sh
# one-time
cd hub-tui
bun install

# configure (interactive — or export env vars, see below)
bun run src/index.tsx

# non-interactive
export HUB_URL=http://localhost:8443
export HUB_TEAM=team
export HUB_TOKEN=<paste bearer>
bun run src/index.tsx
```

Config precedence: CLI flags > env vars > `~/.config/termipod/hub-tui.json`.
The token is stored in the config file on disk — mode 0600, no keychain
integration yet. This is a terminal tool on a machine you already trust;
the mobile app's flutter_secure_storage story doesn't apply.

## Layout

```
src/
  index.tsx        — entrypoint, top-level view switcher
  config.ts        — load/save hub URL + token
  client.ts        — REST wrapper around the hub HTTP API
  sse.ts           — Bun-native SSE reader for /stream endpoints
  views/
    Pending.tsx    — attention items + approve/reject
    Feed.tsx       — live event feed for one channel
```

## Design notes

- **No dependency on `ink-big-text` / fancy widgets.** Plain text lists
  render faster and survive over-SSH line buffering.
- **REST client mirrors `lib/services/hub/hub_client.dart`.** Same endpoints,
  same method names where practical, so the two clients evolve together.
- **SSE reader is hand-rolled.** `fetch`'s body is an async iterable on Bun,
  which is enough to parse `data: ...\n\n` frames without a library.
