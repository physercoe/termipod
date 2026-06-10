# Themed vocabulary overlay + i18n full sweep

> **Type:** plan
> **Status:** Current (2026-06-10) — approved; coding starts next session
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.815

**TL;DR.** Close issue #138 (the app shows hardcoded English in zh mode
across ~90 files) **and** build the vocabulary-preset runtime ([ADR-048])
in one program. gen-l10n keeps owning language (en/zh); a new `VocabPack`
owns role wording, swappable by preset (tech / business / political /
research) in both languages. Ship the preset picker + the steward/
principal/agent axes first to unblock the tester, then sweep the rest of
the strings area by area. No local Flutter SDK → CI builds; two local lint
gates (`lint-arb.sh`, `lint-vocab.sh`) plus director device-tests per area.

---

## Why now

Two drivers converge (see [ADR-048] for the full record):

1. **A tester is blocked** — "steward" / 管家 is wrong for their domain and
   there is no way to re-word the role. Short-board bug, not polish.
2. **Issue #138** — ~90 screen/widget files (~600–900 literals) never call
   `AppLocalizations`, so zh users see English. The sweep touches every
   role-bound string anyway; doing it alongside the preset wedge migrates
   each string once.

State verified 2026-06-10: gen-l10n is wired (`l10n.yaml`, delegate +
locale override in `main.dart`), 618 en/zh keys exist but the ARB is stale
(May 23) and hardcodes the `tech` wording. There is **no** runtime preset
swap. Only ~75 of ~600 keys are role-bound; the rest are always-neutral.

## Architecture (from [ADR-048])

- **Two orthogonal dimensions.** Language = gen-l10n (en/zh). Preset
  wording = `VocabPack`, resolved by `(preset, language)` → `axis → forms`.
  4 presets × 2 languages × ~21 axes — a compact table, not duplicated ARB.
- **Neutral strings** → plain ARB (en/zh).
- **Role-bound strings** → ARB template with a `{role}` placeholder, filled
  by `vocab.term(Axis.x)` at render. English axes carry the grammatical
  forms (title/lower × singular/plural); zh carries one. Per-preset full
  keys are the fallback where English grammar can't compose.
- **Preset = client setting** now (`settings_provider` enum +
  `vocabProvider` + settings picker), hub-served per-team later. Hub stays
  neutral (kinds/ids are data; presets are display-only).
- **Source format:** a typed in-repo `VocabPack` table now (testable,
  lintable), structured to graduate to a hub-served pack without touching
  call sites. (Rejected: per-preset `.arb` packs — ×8 ARB duplication.)

## The vocabulary matrix (authoritative working set)

Columns: **tech** (default) · **business** = company · **political** =
policy · **research** = academy. en / zh. Headline role axes are
director-set ([ADR-048] §Decision-5); the remaining axes below are the
working proposal WS-A implements and `vocabulary.md` §2 adopts.

| axis | tech | business 公司 | political 政策 | research 学术 |
|---|---|---|---|---|
| `role.steward` | Steward 管家 | Manager 经理 | Secretary 秘书 | Supervisor 主管 |
| `role.agent` | Agent 智能体 | Specialist 专员 | Operative 干事 | Researcher 研究员 |
| `role.principal` | Owner 负责人 | Boss 老板 | Leader 领导 | PI 课题组负责人 |
| `role.council` | Review board 评审组 | Committee 委员会 | Council 委员会 | Review panel 评审小组 |
| `entity.team` | Team 团队 | Org 组织 | Office 办公室 | Group 课题组 |
| `entity.project` | Project 项目 | Initiative 项目 | Operation 行动 | Study 课题 |
| `entity.workspace` | Service 服务 | Department 部门 | Bureau 局 | Lab 实验室 |
| `entity.task` | Ticket 工单 | Action item 行动项 | Action 事项 | Step 步骤 |
| `entity.plan` | Roadmap 路线图 | Roadmap 路线图 | Strategy 策略 | Protocol 方案 |
| `entity.run` | Build 构建 | Execution 执行 | Operation 行动 | Trial 试验 |
| `entity.schedule` | Schedule 计划 | Cadence 节奏 | Calendar 日程 | Schedule 计划 |
| `entity.template` | Pipeline 流水线 | Playbook 手册 | Playbook 手册 | Protocol 方案 |
| `entity.channel` | Channel 频道 | Channel 频道 | War room 指挥室 | Notebook 记录本 |
| `entity.review` | Review 评审 | Approval 审批 | Sign-off 会签 | Peer review 同行评审 |
| `entity.document` | Doc 文档 | Brief 简报 | Memo 备忘 | Paper 论文 |
| `entity.output` | Artifact 产物 | Deliverable 交付物 | Output 成果 | Result 结果 |
| `surface.attention` | Inbox 收件箱 | Action items 待办事项 | Briefings 简报 | Inbox 收件箱 |
| `surface.approval` | Approval 审批 | Approval 审批 | Sign-off 会签 | Sign-off 会签 |
| `surface.directive` | Spec 规格 | Directive 指令 | Directive 指令 | Hypothesis 假设 |
| `surface.brief` | Digest 摘要 | Daily brief 每日简报 | Briefing 简报 | Lab notes 实验记录 |
| `entity.host` | Host 主机 | Host 主机 | Host 主机 | Host 主机 |

`entity.host` and the SSH/tmux/terminal vocabulary stay **neutral** (one
en + one zh, never preset-swapped) per `vocabulary.md` §1. The zh in the
non-headline rows is a proposal for director review during WS-A, not yet
locked.

## Workstreams (each its own CI-gated PR)

**WS-A — Vocabulary-preset foundation (MVP slice, unblocks the tester).**
- `VocabPack` data: all 4 presets × en+zh for the 21 axes (en grammatical
  forms; zh single) as a typed in-repo table.
- `vocabProvider`, preset enum in `settings_provider`, settings picker.
- `lint-vocab.sh` (8-pack axis completeness + form/parity) wired to CI.
- Rewrite `vocabulary.md` §2 (corrected en presets + zh columns), flip its
  status to *implemented per ADR-048*, add the glossary entry for
  "vocabulary preset".
- Wire `role.steward / role.principal / role.agent` into the steward-heavy
  surfaces (Sessions category headers "General/Project/Domain steward",
  steward badge/config, Me page) so the picker visibly re-words them.
- Tests: resolution per `(preset, language)`, fallback (preset→tech,
  language→en), all-axes-covered.

**WS-B — i18n Phase-0 tooling** (can ride with WS-A): `lint-arb.sh` (valid
JSON, en/zh key-set equality, per-key placeholder consistency, orphan
detection); the `areaComponentRole` key-naming convention + a canonical
common-action set (`commonCancel/Save/Delete/Close/…`) folding existing
duplicates; the context-free-helper → enum pattern (helpers like
`stewardCategoryLabel` return enums; widgets map enum → l10n/vocab at
render, keeping `sessions_list_controller` pure and unit-tested).

**WS-C…H — full string sweep, by area** (each a PR): apply the triage —
neutral → ARB en/zh; role-bound → `{role}` template + `vocab.term(axis)`,
axis-tagged. Order by visibility:

1. **Sessions** (`sessions_screen` + controller helpers) — worst offender
   (~118 literals) and the headline role terms; proves the pattern. Note
   "session" and "detached" are tech-neutral, *not* role-bound.
2. **Shared widgets** that ride every screen (`agent_actions_menu`,
   `agent_compose`, `steward_overlay/*`, `view_switcher`, `session_header`,
   `run_report_card`).
3. **Transcript / Feed / Insight** (`insight_transcript`, `transcript/*`).
4. **Team** (`templates_screen`, `agent_families_screen`, …).
5. **Projects** (33 files — sub-batched).
6. **Tail** — Settings / Hub / Me-Approval / Vault-Keys / Hosts + remaining
   widgets.

## Verification & discipline

- **No local Flutter SDK** → gen-l10n / analyze / test / build run in CI
  (`ci.yml`). The local gates are `lint-arb.sh` + `lint-vocab.sh`.
- **Per PR:** lint locally → CI analyze + test + build green → director
  device-tests that area **across presets and across en/zh** → merge →
  sync `main` before the next branch.
- **Serialize** on `lib/l10n/*.arb` and the `VocabPack` table (like the
  design-token baseline) to avoid merge collisions.

## Closes / advances

- Closes #138 (string coverage) as the sweep completes.
- Implements [ADR-048] (vocabulary-preset runtime).
- First slice of the post-MVP content-pack vision
  (`discussions/post-mvp-domain-packs.md`), graduated to MVP.

[ADR-048]: ../decisions/048-themed-vocabulary-overlay.md
