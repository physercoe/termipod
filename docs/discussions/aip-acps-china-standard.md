# AIP / ACPs (China) — what it is, US/EU comparators, termipod fitness

> **Type:** discussion
> **Status:** **Post-MVP** (decision logged 2026-05-12). Drafted
> 2026-05-12. Three additive wedges scoped (§6) — none in MVP scope;
> revisit when China-market posture becomes a real requirement.
> **Audience:** principal + future contributors evaluating cross-org
> interop scope for termipod.
> **Last verified vs code:** v1.0.508
> **Sibling discussions:** —

**TL;DR.** "AIP" as the user encountered it is the **Agent Interconnection
Protocol** umbrella out of **Beijing University of Posts and
Telecommunications** (BUPT) with **China Electronics Standardization
Institute** (CESI) support — v1.0 in 2025, v2.0 March 2026. The
substrate it ships is a suite of eight specs collectively called
**ACPs** (Agent Collaboration Protocols). It is the implementation
referenced by the **May 8 2026 Cyberspace Administration of China**
policy that mandates nationwide agent-interconnect standards, so the
"first national-level standard" framing is fair — backed by a standards
body (CESI) and named in a central-government policy. **No US or EU
counterpart at the same altitude exists**; Western efforts are
industry-led under Linux Foundation (MCP / A2A / IBM-ACP). **Termipod's
current design covers the Tooling layer well** (MCP) and stores
shape-agnostic agent cards already, but is missing three things if
"AIP-compliant" ever becomes a requirement: AIC-style identity, an
ACS-shape agent-card surface, and capability-search (ADP). RabbitMQ
group mode is explicitly **not** a fit — our hub-mediated A2A relay
covers the same role through a different topology.

---

## 1. What "AIP" actually is

The umbrella organization at github.com/AIP-PUB hosts **eight repos**
plus the demo the user pointed at:
`Agent-Interconnection-Protocol-Project` (the spec), `ACPs-SDK`,
`ACPs-CA-Client`, `ACPs-CA-Server`, `ACPs-Registry-Server`,
`ACPs-Discovery-Server`, `ACPs-CA-Challenge`, and the
`ACPs-Demo-Project` we'll dissect below.

- **Lead:** BUPT School of AI (北京邮电大学人工智能学院).
- **Standards-body support:** **CESI** (中国电子技术标准化研究院) — the
  MIIT-affiliated body that drafts mandatory IT/AI national standards.
- **Authors (paper + project):** Liu Jun, Gao Ge, Li Ke, Chen Keliang,
  Yu Ke, Hu Xiaofeng, Ma Di. The arxiv reference is
  [`2505.13523`](https://arxiv.org/abs/2505.13523) "ACPs: Agent
  Collaboration Protocols for the Internet of Agents" (2025-05-18).
- **Public policy peg:** The May 8 2026 CAC policy explicitly names
  AIP as the interconnect standard the country should converge on.

No formal `GB/T` national-standard number is visible in the public
materials yet, but the CESI + CAC linkage means this is the de-facto
official national push — different in kind from Western
industry-consortium efforts.

> Naming confusion to flag: **"AIP" is overloaded**. It is both the
> *umbrella project* AND the *seventh sub-spec* inside ACPs ("Agent
> Interaction Protocol", lower-case nesting). When the policy paper
> says "AIP", it means the umbrella. When the demo's
> `acps_sdk.aip` package logs at `WARNING`, it means the interaction
> sub-spec.

## 2. The 8-protocol stack

Lifted directly from the AIP-PUB README of the spec repo:

| # | Spec | Role |
|---|------|------|
| 1 | **ACPs Overview** | Umbrella + glossary |
| 2 | **AIC** — Agent Identity Code | OID-encoded agent ID: `1.2.156.3088.0001.00001.SC64YN.Z5LSGY.1.0NMQ`. The `1.2.156` prefix is China's ISO OID arc, so the ID is **PKI-rooted, not human-string**. |
| 3 | **ACS** — Agent Capability Specification | JSON agent-card shape (very close to A2A's Agent Card — see §3.1) |
| 4 | **ATR** — Agent Trusted Registration | CA-bound enrollment: agent presents AIC + cert to a registry, gets blessed into the trust bundle |
| 5 | **AIA** — Agent Identity Authentication | mTLS handshake between agents using the ATR-issued certs |
| 6 | **ADP** — Agent Discovery Protocol | Separate service: capability-search → returns matching agent cards |
| 7 | **AIP** — Agent Interaction Protocol | The wire protocol — RPC (point-to-point sync) + Group (RabbitMQ async multi-party) |
| 8 | **DSP** — Data Synchronization Protocol | Cross-agent state sync (not exercised in the demo) |

So the stack is essentially: **PKI identity → cert-rooted agent ID →
JSON capability card → CA enrollment → mTLS handshake → discovery →
wire RPC → state sync**. That's a full enterprise SOA shape, with
identity baked in at the bottom.

## 3. The demo's reference architecture

[`AIP-PUB/ACPs-Demo-Project`](https://github.com/AIP-PUB/ACPs-Demo-Project)
implements a *tourism assistant* scenario:

```
       ┌──────────────┐
       │   Web app    │  (vanilla JS, polls Leader)
       └──────┬───────┘
              │ HTTP /api/v1/submit, /api/v1/result/{sid}
              ▼
       ┌──────────────┐
       │    Leader    │  (FastAPI, tourism orchestrator)
       │              │  intent → plan → dispatch → aggregate
       └─┬─────┬────┬─┘
         │     │    │  Direct RPC (mTLS HTTP)         ┌──────────────┐
         │     │    └────────────────────────────────►│ china_hotel  │
         │     │       Group (RabbitMQ exchanges)     ├──────────────┤
         │     └────────────────────────────────────►│ china_transp │
         │                                           ├──────────────┤
         │                                           │ beijing_food │
         │                                           ├──────────────┤
         │                                           │ beijing_urban│
         │                                           ├──────────────┤
         │                                           │ beijing_rural│
         │                                           └──────┬───────┘
         │                                                  ▲
         │       ┌────────────────────────┐                 │
         └──────►│ ADP discovery server    │─────matches────┘
                 │ (separate process,      │
                 │  capability search)     │
                 └────────────────────────┘
```

Concrete dimensions from `leader/atr/acs.json` +
`leader/config.example.toml`:

- **ACS shape** (verbatim minus the long description):
  ```json
  {
    "aic": "1.2.156.3088.0001.00001.SC64YN.Z5LSGY.1.0NMQ",
    "active": true, "protocolVersion": "02.00",
    "name": "旅游助理智能体", "version": "1.0.0",
    "webAppUrl": "http://localhost:59200",
    "provider": {
      "organization": "北京邮电大学",
      "department": "人工智能学院",
      "url": "https://ai.bupt.edu.cn",
      "license": "京ICP备14033833号-1"
    },
    "securitySchemes": {
      "mtls": {"type":"mutualTLS",
               "x-caChallengeBaseUrl":"http://localhost:8004/acps-atr-v2"}
    },
    "endPoints": [], "capabilities": {...},
    "defaultInputModes":[], "defaultOutputModes":[], "skills":[]
  }
  ```
  This is **strikingly A2A-Agent-Card-shaped** (name / version /
  provider / securitySchemes / capabilities / skills). The
  China-specific additions are the `aic` OID + the
  `x-caChallengeBaseUrl` pointing at the ATR CA endpoint + an
  ICP license number (Chinese site-license registration).
- **Transport:** FastAPI + Uvicorn on each agent; mTLS terminated at
  the FastAPI side.
- **Leader↔Partner direct RPC:** synchronous HTTP `/rpc` on the
  partner's port. Partner config in `leader/atr/trust-bundle.pem`.
- **Group mode:** RabbitMQ — host/port/user/password configured;
  exchange-per-conversation pattern via `group_handler.py` (25kB) and
  `group_executor.py`. Used when several partners must collaborate
  asynchronously on the same session.
- **Partner runtime:** `generic_runner.py` (47kB) drives a three-phase
  lifecycle (Decision → Analysis → Production) entirely from prompt
  templates in `prompts.toml`. **No Python is written per agent**;
  every partner is a config-only deployment.
- **LLMs in config:** `dmxapi.com` proxy with `doubao-seed-1-6-flash`,
  `Doubao-pro-32k`, `qwen3-max`. China-domestic-LLM-first by default
  but OpenAI-API-compatible so any backend can substitute.
- **Leader API:** `POST /api/v1/submit` → 202 + `session_id`; client
  polls `GET /api/v1/result/{session_id}`; states are
  `pending | running | awaiting_input | completed | failed`.

The Leader's `assistant/` directory comment in the README sums it up:
*"Session management, AIP protocol communication, task state machines,
and LLM call abstraction."*

## 4. US / EU comparators

There is **no nation-level standard at the same altitude in the US or
EU**. The Western equivalents are industry-led and roll up to a
neutral foundation:

| Protocol | Origin | Role | Status Q1 2026 |
|---|---|---|---|
| **MCP** (Model Context Protocol) | Anthropic | Agent ↔ tool | Donated to Linux Foundation; ~97M downloads; cross-vendor adoption |
| **A2A** (Agent-to-Agent) | Google | Agent ↔ agent | Donated to Linux Foundation; 50+ partners |
| **ACP** (Agent Communication Protocol) | IBM / BeeAI | REST-based agent messaging | Linux Foundation |
| **AGNTCY** | Cisco-led coalition | Cross-vendor registry + orchestration | Industry alliance |
| **IETF `draft-singla-agent-identity-protocol-00`** | Indie | Decentralized agent identity / delegation | Very early draft |
| **NIST AI RMF** | US federal | Risk/governance framework | Not an interop protocol |
| **EU AI Act** | EU regulation | Risk classification / transparency obligations | Not an interop protocol |

There is also a parallel academic effort from the **Institute of
Automation, Chinese Academy of Sciences**:
[`ScienceOne-AI/Agent-Interaction-Protocol`](https://github.com/ScienceOne-AI/Agent-Interaction-Protocol)
— a gRPC-based scientific-domain agent protocol. Separate from
BUPT/CESI's ACPs.

**The strategic gap is governance, not technology.** ACPs/AIP and A2A
are remarkably similar in shape — the China card schema is essentially
an A2A Agent Card with mandatory PKI identity. The difference is who
blesses the protocol:

- **China:** top-down — university → standards body (CESI) → ministry
  (MIIT/CAC) → national policy. Mandatory identity rooted in PKI.
- **US/EU:** bottom-up — vendor → consortium → Linux Foundation.
  Identity left to the application.

## 5. Termipod fitness review

Mapping each ACPs spec to what termipod has today:

| ACPs spec | Termipod analogue | Fit |
|---|---|---|
| **AIC** (Identity Code) | `agents.id` UUID; bearer tokens | **Gap** — no PKI, no OID arc, no cross-org identity story. Tokens are team-scoped only. |
| **ACS** (Capability Spec) | `agents` table + `a2a/cards` endpoint (stores card_json as `RawMessage`, see [`handlers_a2a*.go`](../../hub/internal/server/)). Shape is whatever the host-runner sends; today it's [A2A-Agent-Card-shaped](https://a2a-protocol.org/latest/). | **Close** — termipod is already shape-agnostic at the wire; serving an ACS dialect is a serialization decision, not an architectural change. |
| **ATR** (Trusted Registration) | Steward bootstrap + agent-row create on host-runner attach (host-runner is the trust anchor; no CA challenge) | **Gap** — termipod's trust model is hub-token + per-host-runner ownership, not PKI-anchored. Acceptable for single-team; insufficient for cross-org. |
| **AIA** (Identity Authentication) | TLS hub↔mobile, plaintext or TLS host-runner↔hub depending on deploy; no peer-to-peer mTLS between agents | **Gap** — agents don't authenticate each other directly; the hub vouches. |
| **ADP** (Discovery) | `GET /v1/teams/{team}/a2a/cards?handle=<h>` + the `agents` list endpoint | **Partial** — exact-handle lookup works; capability/skill search does not. |
| **AIP-the-protocol** (Interaction) | A2A relay through the hub: `/a2a/relay/<host>/<agent>` rewrites the agent-card `url` so off-box peers hit the hub, which tunnels to the NAT'd host-runner (see blueprint §3.3 + ADR-003). No RabbitMQ. | **Different topology** — termipod chose hub-mediation explicitly because GPU hosts sit behind NAT. ACPs' Group/RabbitMQ mode assumes all parties can reach the broker; that's a non-starter for our deploy shape. The hub relay covers the same *role* (multi-party coordination) without needing RabbitMQ. |
| **Tooling** (implicit) | **MCP** — hub exposes MCP-server for steward tool surface; per-engine MCP configs (Codex `.mcp.json`, gemini `settings.json`, claude-code) | **Strong** — termipod's MCP integration is more mature than what the demo ships (the demo has no separate tool-call protocol; tools live inside prompt templates). |
| **DSP** (Data Sync) | Hub DB is single source of truth; no peer-to-peer state sync | **N/A** — termipod's hub-mediated design means there's nothing to synchronize cross-agent. |

### Two architectural truths

1. **Termipod is a hub-mediated control surface, not a peer-to-peer
   interop substrate.** ACPs is the inverse — peer-to-peer agents
   meeting through a PKI + discovery service. The two answer
   different questions. ACPs answers "how do agents from different
   orgs find and trust each other?" Termipod answers "how does a
   principal direct a fleet of their own agents across hosts?" Both
   can coexist; one isn't a replacement for the other.

2. **The card shape is already 80% aligned.** Termipod stores
   agent_card payloads as opaque JSON on `/v1/a2a/cards`. If we ever
   wanted to publish ACPs-compliant cards, we just adopt the ACS
   field names where they overlap with A2A's (most of them) and add
   `aic` + `securitySchemes.mtls.x-caChallengeBaseUrl`. No DB
   migration needed.

## 6. Gaps that would matter, if we cared

If a future requirement landed — say, "termipod must federate with
ACPs-compliant agents from another university or vendor" — these are
the three wedges that would close the gap. **None are blocked by the
current architecture.**

### Wedge A: Identity (AIC + ATR)

- Generate AIC-shaped OIDs (`1.2.156.X.Y.Z.W` or our own ISO arc).
- Add a `cert_pem` column on `agents` (already have keys table for
  SSH; same primitive).
- Stand up a minimal ATR endpoint or front it with a real CA
  (smallstep / cfssl). Issuing cert at agent-row create.
- Plumb `cert_path` through host-runner spawn for outbound mTLS.

Estimate: ~500 LOC + ops setup for the CA.

### Wedge B: ACS publish surface

- `GET /v1/teams/{team}/agents/{id}/acs` returns the agent row
  re-serialized as an ACS card (mapping our handle/capabilities/skills
  fields to ACS field names).
- Static / well-known endpoint at `/.well-known/acps/acs.json` for
  service-level discovery.

Estimate: ~150 LOC + an OpenAPI schema entry.

### Wedge C: ADP capability search

- Extend `/v1/a2a/cards` to accept `?skill=` / `?capability=` /
  `?protocol=` query params. Today it's `?handle=` only.
- Add a tiny in-memory inverted index over the card_json blobs (no
  ES, no Postgres FTS — agent count won't justify it).
- Optionally expose a separate `/v1/discovery` endpoint at the AIP
  shape so ACPs clients can hit it without learning our routes.

Estimate: ~250 LOC.

**Explicit non-goal: RabbitMQ.** The hub relay already covers the
multi-party coordination role at zero infra cost. Adopting RabbitMQ
would add a service termipod doesn't operate, for a topology we
deliberately rejected (peer-to-peer through a broker assumes all
parties can reach it).

## 7. Recommendation — **Post-MVP** (decision 2026-05-12)

**Scope decision:** the three wedges in §6 are explicitly **post-MVP**.
Termipod is a single-team principal-directing-agents control surface;
the ACPs target audience (cross-org agent federation under PKI
identity) is not our product surface. The work to "support ACPs"
would be entirely additive and would not change what termipod is for,
so deferring it carries zero opportunity cost on the MVP arc.

For **the watch list**: track two things —

1. **The A2A Agent Card schema.** Termipod's `a2a/cards` endpoint
   already stores it shape-agnostically. If A2A's schema stabilizes,
   that's the single normalization that auto-aligns us with ACS too
   (since ACS is A2A-shaped with PKI fields bolted on).
2. **Whether ACPs picks up cross-border traction.** If termipod
   eventually wants Chinese-market posture, AIP-compliant card output
   is a 4-day project, not a re-architecture. The hub design doesn't
   need to change.

Open question if/when this re-enters scope: do we adopt the AIC OID
arc (1.2.156.…) or mint our own under a non-national prefix? The
former buys China-policy compliance; the latter keeps termipod
nationally neutral. Probably the latter, with a documented
configuration toggle for installs that need to bind to the Chinese
trust chain.

---

## Sources

- [`AIP-PUB/Agent-Interconnection-Protocol-Project`](https://github.com/AIP-PUB/Agent-Interconnection-Protocol-Project) — spec repo (BUPT + CESI)
- [`AIP-PUB/ACPs-Demo-Project`](https://github.com/AIP-PUB/ACPs-Demo-Project) — reference demo (tourism assistant)
- [arXiv 2505.13523](https://arxiv.org/abs/2505.13523) — "ACPs: Agent Collaboration Protocols for the Internet of Agents" (Liu et al., 2025-05-18)
- [TBS News — "China issues new rules to advance AI agent innovation"](https://www.tbsnews.net/tech/china-issues-new-rules-advance-ai-agent-innovation-1433961) (CAC + NDRC + MIIT, 2026-05-08 policy)
- [Telegraph — "China Is Building the Administrative Operating System for Autonomous AI"](https://telegraph.com/china-administrative-operating-system-autonomous-ai/) (commentary, 2026-05-11)
- [`a2a-protocol.org`](https://a2a-protocol.org/latest/) — A2A spec (Google → Linux Foundation)
- [`getstream.io/blog/ai-agent-protocols/`](https://getstream.io/blog/ai-agent-protocols/) — "Top AI Agent Protocols in 2026 — MCP, A2A, ACP & More"
- [`zylos.ai/research/2026-03-26-agent-interoperability-protocols-mcp-a2a-acp-convergence`](https://zylos.ai/research/2026-03-26-agent-interoperability-protocols-mcp-a2a-acp-convergence) — convergence analysis (Q1 2026)
- [`ScienceOne-AI/Agent-Interaction-Protocol`](https://github.com/ScienceOne-AI/Agent-Interaction-Protocol) — parallel CAS effort (gRPC, scientific scenarios)
- [IETF `draft-singla-agent-identity-protocol-00`](https://datatracker.ietf.org/doc/draft-singla-agent-identity-protocol/00/) — early IETF identity-only draft
