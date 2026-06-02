# Export agent-run traces to OTLP (Jaeger / Collector / Phoenix)

> **Type:** how-to
> **Status:** Current (2026-06-02)
> **Audience:** operators
> **Last verified vs code:** v1.0.788

**TL;DR.** The hub can project every agent run into **OpenTelemetry
traces** and ship them to a trace backend you run, so you get the
familiar waterfall / flamegraph view of a session: **one trace per
session, one span per turn, one child span per tool call**, with GenAI
attributes (model, tokens, cost) and errors as span events. Turn on the
exporter with a single flag —
`hub-server serve --otlp-endpoint http://localhost:4318` — and point it
at Jaeger, an OpenTelemetry Collector, or any OTLP/HTTP receiver. It is
**off by default**: the hub already stores the events; a backend buys
query/viz UX, not storage (ADR-038 §4).

This guide covers what gets exported, how to turn it on, what backend to
point it at, how to verify it, and the wire-format caveat.

---

## 1. What gets exported

The exporter is a **direct projection** of rows the hub already stores
(the per-agent turn index + the tool/error events) — no re-derivation,
no guesswork. The mapping:

| OTLP concept   | TermiPod concept | ID derivation                         |
|----------------|------------------|---------------------------------------|
| **Trace**      | Session          | `sha256(session_id)[:16]`             |
| **Span**       | Turn             | `sha256(session_id\|turn_id)[:8]`     |
| **Child span** | Tool call        | `sha256(session_id\|tool_call_id)[:8]`|

- A resumed session that spans several agents shares **one trace** — all
  of its turns line up on the same timeline.
- **Turn spans** carry OTel **GenAI** attributes: `gen_ai.system` (the
  engine kind, e.g. `claude-code`), `gen_ai.usage.input_tokens` /
  `output_tokens`, and `cost_usd`, plus `termipod.turn.idx`,
  `termipod.tool_count`, `termipod.error_count`, `termipod.agent_id`.
  Status is `OK` for a successful turn, `ERROR` otherwise.
- **Tool spans** are children of their enclosing turn, named after the
  tool (`bash`, `edit`, …), timed `[tool_call → tool_result]`, with
  `gen_ai.tool.name`. A failed tool gets `ERROR` status plus an
  `exception` span event.
- **Errors** become `exception` span events on the enclosing turn.
- **Span IDs are deterministic**, so re-exporting a session is
  **idempotent** — the backend dedupes by ID and just updates the span.

### Cadence — idle *and* terminal, no streaming

The exporter sweeps every **30 s** and ships any session whose newest
**closed** turn advanced since the last export. Because a turn closes
when the agent goes idle *and* when it terminates, both watermark points
are covered, and the deterministic IDs mean each sweep just re-ships the
grown prefix. **A long-running agent that never terminates still
exports** — you do not need per-turn live streaming (that is post-MVP).
Open (in-flight) turns are skipped until they close.

---

## 2. Turn it on

Add the flag to `hub-server serve`:

```bash
hub-server serve \
  --listen 127.0.0.1:8443 \
  --otlp-endpoint http://localhost:4318
```

| Flag                  | Default        | Meaning                                              |
|-----------------------|----------------|------------------------------------------------------|
| `--otlp-endpoint`     | `""` (off)     | OTLP/HTTP **base** URL. The exporter POSTs to `<endpoint>/v1/traces`. |
| `--otlp-service-name` | `termipod-hub` | `service.name` on the exported spans.                |

On start you will see a log line confirming the resolved target:

```
otlp trace export enabled  endpoint=http://localhost:4318/v1/traces service=termipod-hub
```

> **Note the port.** OTLP/**HTTP** is `4318`; OTLP/**gRPC** is `4317`.
> This exporter speaks HTTP — use `4318` (or your Collector's HTTP
> receiver port).

---

## 3. Point it at a backend

Any OTLP/HTTP trace receiver works. Common choices:

### Jaeger (all-in-one)

Jaeger has a built-in OTLP receiver.

```bash
docker run --rm -p 16686:16686 -p 4318:4318 \
  jaegertracing/all-in-one:latest
# hub: --otlp-endpoint http://localhost:4318
# UI:  http://localhost:16686
```

### OpenTelemetry Collector

Run a Collector with an `otlp` receiver and forward to wherever you
like. Point the hub at the Collector's HTTP port (`4318` by default).
This is also the way to reach a **protobuf-only** backend (see §5).

### Phoenix (Arize)

Phoenix exposes an OTLP endpoint (default `http://localhost:6006`, with
traces under `/v1/traces`). Point the hub at the base URL:

```bash
hub-server serve --otlp-endpoint http://localhost:6006
```

If your backend needs auth headers (a hosted Phoenix, for instance),
front it with a Collector that injects the header — the hub exporter
sends no auth header of its own in this release (it is built for a
trusted same-host/VPC backend).

---

## 4. Verify it

1. Start the hub with `--otlp-endpoint` pointed at your backend and
   confirm the `otlp trace export enabled` log line.
2. Run (or replay) an agent so at least one **turn closes** — let the
   agent go idle or terminate it.
3. Within ~30 s, open the backend UI and search for service
   `termipod-hub`. You should see a trace per session; expand it to see
   the turn spans and their tool-call children.
4. Quick smoke test without a real agent — capture the POST with `nc`:

   ```bash
   # terminal 1: a throwaway receiver that 200s everything on :4318
   while true; do printf 'HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n' \
     | nc -l -p 4318 -q1; done
   # terminal 2: run the hub with --otlp-endpoint http://localhost:4318
   ```

   You will see the JSON body of an `ExportTraceServiceRequest` arrive
   on each sweep once a turn has closed.

---

## 5. Wire format — OTLP/HTTP **JSON**

The exporter sends OTLP over HTTP with `Content-Type: application/json`
(the JSON-protobuf encoding defined by the OTLP spec). This keeps the
hub free of the protobuf dependency tree and is accepted by **Jaeger**
and the **OpenTelemetry Collector** out of the box.

A backend that accepts **only** protobuf OTLP will reject the JSON
body — put an **OpenTelemetry Collector in front** (it ingests JSON and
re-exports protobuf to the downstream). The hub logs a non-2xx response
with the backend's error snippet, so a format rejection is visible:

```
otlp export: ship  session=... err=otlp export: 415 Unsupported Media Type: ...
```

---

## 6. Cost & privacy

- **Cost.** Export is a periodic read of rows the hub already holds plus
  one HTTP POST per changed session per sweep. No new storage, no
  per-event overhead on the hot path.
- **What leaves the hub.** Span **metadata** only — turn/tool timing,
  tool *names*, token/cost counts, error class + message. Prompt and
  tool-argument **bodies are not exported** (the projection reads the
  turn index and event envelopes, not message content). Treat the
  backend with the same trust as your logs.

---

## Related

- [`decisions/038-per-run-event-digest.md`](../decisions/038-per-run-event-digest.md)
  — §4 specifies the projection.
- [`plans/agent-run-analysis-mode.md`](../plans/agent-run-analysis-mode.md)
  — P3 is this exporter; the mobile Insights view is the in-app analysis
  surface over the same turn index.
- [`how-to/surface-run-metrics.md`](surface-run-metrics.md) — the other
  operator observability surface (training curves on the Runs tab).
