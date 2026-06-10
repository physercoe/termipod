import 'vocab_axis.dart';
import 'vocab_preset.dart';
import 'vocab_term.dart';

/// The vocabulary data (ADR-048) — one `axis → term` map per
/// `(preset, language)` pack. 4 presets × 2 languages = 8 packs, each
/// covering every [VocabAxis]. This is the typed in-repo source structured to
/// graduate to a hub-served pack without touching call sites.
///
/// English terms carry grammatical forms (`_en` derives the regular cases);
/// Chinese has no case/plural inflection, so one string fills every form
/// (`_zh`). The headline role rows (`role.steward/principal/agent`) are
/// director-set (ADR-048 §Decision-5); the rest follow the program plan's
/// working matrix.
///
/// Invariant: every pack defines every axis. Enforced by `vocab_pack_test.dart`
/// and `scripts/lint-vocab.sh`.
final Map<VocabPreset, Map<String, Map<VocabAxis, VocabTerm>>> kVocabPacks = {
  VocabPreset.tech: {'en': _techEn, 'zh': _techZh},
  VocabPreset.business: {'en': _businessEn, 'zh': _businessZh},
  VocabPreset.political: {'en': _politicalEn, 'zh': _politicalZh},
  VocabPreset.research: {'en': _researchEn, 'zh': _researchZh},
};

/// Regular English noun: derives lower / plural / lower-plural from [title]
/// unless overridden (irregular plurals, acronyms).
VocabTerm _en(String title, {String? lower, String? plural, String? pluralLower}) {
  final l = lower ?? title.toLowerCase();
  final p = plural ?? '${title}s';
  final pl = pluralLower ?? p.toLowerCase();
  return VocabTerm(title, lower: l, plural: p, pluralLower: pl);
}

/// Chinese term: one form fills all (no case, no plural).
VocabTerm _zh(String s) => VocabTerm(s);

// === pack: tech / en ===
final Map<VocabAxis, VocabTerm> _techEn = {
  VocabAxis.roleSteward: _en('Steward'),
  VocabAxis.roleAgent: _en('Agent'),
  VocabAxis.rolePrincipal: _en('Owner'),
  VocabAxis.roleCouncil: _en('Review board'),
  VocabAxis.entityTeam: _en('Team'),
  VocabAxis.entityProject: _en('Project'),
  VocabAxis.entityWorkspace: _en('Service'),
  VocabAxis.entityTask: _en('Ticket'),
  VocabAxis.entityPlan: _en('Roadmap'),
  VocabAxis.entityRun: _en('Build'),
  VocabAxis.entitySchedule: _en('Schedule'),
  VocabAxis.entityTemplate: _en('Pipeline'),
  VocabAxis.entityChannel: _en('Channel'),
  VocabAxis.entityReview: _en('Review'),
  VocabAxis.entityDocument: _en('Doc'),
  VocabAxis.entityOutput: _en('Artifact'),
  VocabAxis.surfaceAttention: _en('Inbox', plural: 'Inboxes'),
  VocabAxis.surfaceApproval: _en('Approval'),
  VocabAxis.surfaceDirective: _en('Spec'),
  VocabAxis.surfaceBrief: _en('Digest'),
  VocabAxis.entityHost: _en('Host'),
};

// === pack: tech / zh ===
final Map<VocabAxis, VocabTerm> _techZh = {
  VocabAxis.roleSteward: _zh('管家'),
  VocabAxis.roleAgent: _zh('智能体'),
  VocabAxis.rolePrincipal: _zh('负责人'),
  VocabAxis.roleCouncil: _zh('评审组'),
  VocabAxis.entityTeam: _zh('团队'),
  VocabAxis.entityProject: _zh('项目'),
  VocabAxis.entityWorkspace: _zh('服务'),
  VocabAxis.entityTask: _zh('工单'),
  VocabAxis.entityPlan: _zh('路线图'),
  VocabAxis.entityRun: _zh('构建'),
  VocabAxis.entitySchedule: _zh('计划'),
  VocabAxis.entityTemplate: _zh('流水线'),
  VocabAxis.entityChannel: _zh('频道'),
  VocabAxis.entityReview: _zh('评审'),
  VocabAxis.entityDocument: _zh('文档'),
  VocabAxis.entityOutput: _zh('产物'),
  VocabAxis.surfaceAttention: _zh('收件箱'),
  VocabAxis.surfaceApproval: _zh('审批'),
  VocabAxis.surfaceDirective: _zh('规格'),
  VocabAxis.surfaceBrief: _zh('摘要'),
  VocabAxis.entityHost: _zh('主机'),
};

// === pack: business / en ===
final Map<VocabAxis, VocabTerm> _businessEn = {
  VocabAxis.roleSteward: _en('Manager'),
  VocabAxis.roleAgent: _en('Specialist'),
  VocabAxis.rolePrincipal: _en('Boss', plural: 'Bosses'),
  VocabAxis.roleCouncil: _en('Committee'),
  VocabAxis.entityTeam: _en('Org'),
  VocabAxis.entityProject: _en('Initiative'),
  VocabAxis.entityWorkspace: _en('Department'),
  VocabAxis.entityTask: _en('Action item'),
  VocabAxis.entityPlan: _en('Roadmap'),
  VocabAxis.entityRun: _en('Execution'),
  VocabAxis.entitySchedule: _en('Cadence'),
  VocabAxis.entityTemplate: _en('Playbook'),
  VocabAxis.entityChannel: _en('Channel'),
  VocabAxis.entityReview: _en('Approval'),
  VocabAxis.entityDocument: _en('Brief'),
  VocabAxis.entityOutput: _en('Deliverable'),
  VocabAxis.surfaceAttention: _en('Action items', lower: 'action items', plural: 'Action items', pluralLower: 'action items'),
  VocabAxis.surfaceApproval: _en('Approval'),
  VocabAxis.surfaceDirective: _en('Directive'),
  VocabAxis.surfaceBrief: _en('Daily brief'),
  VocabAxis.entityHost: _en('Host'),
};

// === pack: business / zh ===
final Map<VocabAxis, VocabTerm> _businessZh = {
  VocabAxis.roleSteward: _zh('经理'),
  VocabAxis.roleAgent: _zh('专员'),
  VocabAxis.rolePrincipal: _zh('老板'),
  VocabAxis.roleCouncil: _zh('委员会'),
  VocabAxis.entityTeam: _zh('组织'),
  VocabAxis.entityProject: _zh('项目'),
  VocabAxis.entityWorkspace: _zh('部门'),
  VocabAxis.entityTask: _zh('行动项'),
  VocabAxis.entityPlan: _zh('路线图'),
  VocabAxis.entityRun: _zh('执行'),
  VocabAxis.entitySchedule: _zh('节奏'),
  VocabAxis.entityTemplate: _zh('手册'),
  VocabAxis.entityChannel: _zh('频道'),
  VocabAxis.entityReview: _zh('审批'),
  VocabAxis.entityDocument: _zh('简报'),
  VocabAxis.entityOutput: _zh('交付物'),
  VocabAxis.surfaceAttention: _zh('待办事项'),
  VocabAxis.surfaceApproval: _zh('审批'),
  VocabAxis.surfaceDirective: _zh('指令'),
  VocabAxis.surfaceBrief: _zh('每日简报'),
  VocabAxis.entityHost: _zh('主机'),
};

// === pack: political / en ===
final Map<VocabAxis, VocabTerm> _politicalEn = {
  VocabAxis.roleSteward: _en('Secretary', plural: 'Secretaries'),
  VocabAxis.roleAgent: _en('Operative'),
  VocabAxis.rolePrincipal: _en('Leader'),
  VocabAxis.roleCouncil: _en('Council'),
  VocabAxis.entityTeam: _en('Office'),
  VocabAxis.entityProject: _en('Operation'),
  VocabAxis.entityWorkspace: _en('Bureau'),
  VocabAxis.entityTask: _en('Action'),
  VocabAxis.entityPlan: _en('Strategy', plural: 'Strategies'),
  VocabAxis.entityRun: _en('Operation'),
  VocabAxis.entitySchedule: _en('Calendar'),
  VocabAxis.entityTemplate: _en('Playbook'),
  VocabAxis.entityChannel: _en('War room'),
  VocabAxis.entityReview: _en('Sign-off'),
  VocabAxis.entityDocument: _en('Memo'),
  VocabAxis.entityOutput: _en('Output'),
  VocabAxis.surfaceAttention: _en('Briefings', lower: 'briefings', plural: 'Briefings', pluralLower: 'briefings'),
  VocabAxis.surfaceApproval: _en('Sign-off'),
  VocabAxis.surfaceDirective: _en('Directive'),
  VocabAxis.surfaceBrief: _en('Briefing'),
  VocabAxis.entityHost: _en('Host'),
};

// === pack: political / zh ===
final Map<VocabAxis, VocabTerm> _politicalZh = {
  VocabAxis.roleSteward: _zh('秘书'),
  VocabAxis.roleAgent: _zh('干事'),
  VocabAxis.rolePrincipal: _zh('领导'),
  VocabAxis.roleCouncil: _zh('委员会'),
  VocabAxis.entityTeam: _zh('办公室'),
  VocabAxis.entityProject: _zh('行动'),
  VocabAxis.entityWorkspace: _zh('局'),
  VocabAxis.entityTask: _zh('事项'),
  VocabAxis.entityPlan: _zh('策略'),
  VocabAxis.entityRun: _zh('行动'),
  VocabAxis.entitySchedule: _zh('日程'),
  VocabAxis.entityTemplate: _zh('手册'),
  VocabAxis.entityChannel: _zh('指挥室'),
  VocabAxis.entityReview: _zh('会签'),
  VocabAxis.entityDocument: _zh('备忘'),
  VocabAxis.entityOutput: _zh('成果'),
  VocabAxis.surfaceAttention: _zh('简报'),
  VocabAxis.surfaceApproval: _zh('会签'),
  VocabAxis.surfaceDirective: _zh('指令'),
  VocabAxis.surfaceBrief: _zh('简报'),
  VocabAxis.entityHost: _zh('主机'),
};

// === pack: research / en ===
final Map<VocabAxis, VocabTerm> _researchEn = {
  VocabAxis.roleSteward: _en('Supervisor'),
  VocabAxis.roleAgent: _en('Researcher'),
  VocabAxis.rolePrincipal: _en('PI', lower: 'PI', plural: 'PIs', pluralLower: 'PIs'),
  VocabAxis.roleCouncil: _en('Review panel'),
  VocabAxis.entityTeam: _en('Group'),
  VocabAxis.entityProject: _en('Study', plural: 'Studies'),
  VocabAxis.entityWorkspace: _en('Lab'),
  VocabAxis.entityTask: _en('Step'),
  VocabAxis.entityPlan: _en('Protocol'),
  VocabAxis.entityRun: _en('Trial'),
  VocabAxis.entitySchedule: _en('Schedule'),
  VocabAxis.entityTemplate: _en('Protocol'),
  VocabAxis.entityChannel: _en('Notebook'),
  VocabAxis.entityReview: _en('Peer review'),
  VocabAxis.entityDocument: _en('Paper'),
  VocabAxis.entityOutput: _en('Result'),
  VocabAxis.surfaceAttention: _en('Inbox', plural: 'Inboxes'),
  VocabAxis.surfaceApproval: _en('Sign-off'),
  VocabAxis.surfaceDirective: _en('Hypothesis', plural: 'Hypotheses'),
  VocabAxis.surfaceBrief: _en('Lab notes', lower: 'lab notes', plural: 'Lab notes', pluralLower: 'lab notes'),
  VocabAxis.entityHost: _en('Host'),
};

// === pack: research / zh ===
final Map<VocabAxis, VocabTerm> _researchZh = {
  VocabAxis.roleSteward: _zh('主管'),
  VocabAxis.roleAgent: _zh('研究员'),
  VocabAxis.rolePrincipal: _zh('课题组负责人'),
  VocabAxis.roleCouncil: _zh('评审小组'),
  VocabAxis.entityTeam: _zh('课题组'),
  VocabAxis.entityProject: _zh('课题'),
  VocabAxis.entityWorkspace: _zh('实验室'),
  VocabAxis.entityTask: _zh('步骤'),
  VocabAxis.entityPlan: _zh('方案'),
  VocabAxis.entityRun: _zh('试验'),
  VocabAxis.entitySchedule: _zh('计划'),
  VocabAxis.entityTemplate: _zh('方案'),
  VocabAxis.entityChannel: _zh('记录本'),
  VocabAxis.entityReview: _zh('同行评审'),
  VocabAxis.entityDocument: _zh('论文'),
  VocabAxis.entityOutput: _zh('结果'),
  VocabAxis.surfaceAttention: _zh('收件箱'),
  VocabAxis.surfaceApproval: _zh('会签'),
  VocabAxis.surfaceDirective: _zh('假设'),
  VocabAxis.surfaceBrief: _zh('实验记录'),
  VocabAxis.entityHost: _zh('主机'),
};
