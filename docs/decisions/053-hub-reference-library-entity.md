# 053. Hub-owned reference library entity (agent-accessible)

> **Type:** decision
> **Status:** Accepted (2026-07-11) — implements the "Hub `Reference` entity"
> step specced in
> [`discussions/reference-library-and-reading.md`](../discussions/reference-library-and-reading.md)
> §4.2. Graduates the desktop's device-local reference library (round 1) to a
> hub-owned entity so **AI agents** can read and mutate it.
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** hub build 2026-07-11 (`reference_items` migration 0062)

**TL;DR.** The research reference library becomes a **hub-owned entity**
(`reference_items` table) exposed over **REST** (`/v1/teams/{team}/references`)
and **MCP** (`reference_list` · `reference_get` · `reference_create` ·
`reference_update` · `reference_delete`). Metadata only — the data-ownership law
(blueprint §4) is preserved: the hub holds reference *fields*; PDF **bytes** stay
on the device (a linked Zotero `storage/` folder) or go through the blob store,
never inline. Agents (steward or worker) can now read the director's library, add
papers they discover, annotate, and prune — the same store the desktop reads.

## Context

The desktop Read surface (J1) shipped a Zotero-shaped reference library that was
**device-local** (`localStorage`, `desktop/src/state/library.ts`) — a deliberate
round-1 posture. But a device-local store is unreachable by the fleet: agents run
on hosts, coordinated by the hub, and cannot see a browser's `localStorage`. The
director asked for the library to be **agent-accessible with full CRUD**. Per the
data-ownership law, the correct home for that metadata is the hub.

## Decision

**D-1. Entity + table.** A `Reference` is a first-class hub entity, stored in
`reference_items` (the physical name is suffixed because `REFERENCES` is a SQL
keyword; the entity, REST path, and tools all use "reference"). It is a clean
projection of the desktop `Reference` shape: `type` (article | preprint | book |
report | webpage | note), `title`, `authors[]`, `year`, `venue`, `doi`,
`arxiv_id`, `url`, `pdf_url`, `abstract`, `tldr`, `citation_count`, `source`,
`external_id` (dedupe key, e.g. `zotero:<key>`), `tags[]`, `collections[]`
(names), `notes`, `body_markdown`, `details{}` (long-tail source fields),
`zotero_storage{key,file}` (attachment coordinates — **not bytes**). Team-scoped
via `team_id` (like `hosts`).

**D-2. REST surface.** Team-scoped CRUD at `/v1/teams/{team}/references`:
`GET` (list, filters `collection`/`tag`/`source`/`q`), `POST` (create),
`GET/PATCH/DELETE /{ref}`. `PATCH` is partial — fields present override, absent
keep. (`handlers_references.go`.)

**D-3. MCP surface.** Five native tools on the same store methods
(`mcp_references.go`, registered in `native_tools.go`): `reference_list` /
`reference_get` (read-only, `TierTrivial`), `reference_create` /
`reference_update` / `reference_delete` (`TierRoutine`). All worker-eligible.
Registered in the native registry with catalog entry + dispatcher + handler in
lockstep (ADR-033), so the tools are visible to agents and covered by the
tool-registry sweep tests.

**D-4. Metadata only.** No PDF bytes in the hub. `zotero_storage` carries the
`{key,file}` coordinates; bytes resolve from the director's linked storage folder
(desktop) or, later, the content-addressed blob store — preserving the
ownership-law split (hub = names, hosts/devices = bytes).

## Consequences

- Agents can curate the library: a steward dispatched "find and add the 10 most-
  cited papers on X" can `reference_create` them; a worker can `reference_update`
  notes/tags after reading. This is the substrate for the Elicit/Undermind-style
  agent extraction/recall the design doc targets.
- **Desktop sync is a follow-up.** This ADR delivers the hub entity + agent
  surface. Wiring the desktop library to push/pull the hub (replacing or
  mirroring `localStorage`) is the next step, tracked against the design doc.
- **Collections are name-strings** for now (stored per-reference), not a separate
  entity — the design doc's open question on a first-class Collection entity
  stays open.
- Flutter mobile read surface is out of scope here (design doc §4.2 lists it as a
  parallel consumer once the entity exists).

## Related

- [`discussions/reference-library-and-reading.md`](../discussions/reference-library-and-reading.md)
  — the library design + build sequence this implements.
- [ADR-033](033-tool-catalog-naming-and-registration.md) — the two-registry MCP tool catalog
  the `reference_*` tools register into.
- [ADR-050](050-desktop-workbench-delivery-model.md) — the desktop workbench whose
  Read surface (J1) is the first consumer.
