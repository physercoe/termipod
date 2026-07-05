import { create } from 'zustand';

/// Lightweight i18n for the desktop shell (en + zh, mirroring the mobile app's
/// languages). A flat key→string dictionary per language; `useT()` returns a
/// lookup bound to the current language with English fallback. This is the web
/// analogue of gen-l10n — the vocabulary presets (ADR-048) can layer on later.
export type Lang = 'en' | 'zh';

type Dict = Record<string, string>;

const en: Dict = {
  'connect.title': 'Connect to a hub',
  'connect.url': 'Hub URL',
  'connect.team': 'Team',
  'connect.token': 'Token',
  'connect.connecting': 'Connecting…',
  'connect.connect': 'Connect',

  'shell.admin': 'Admin',
  'shell.settings': 'Settings',
  'region.attention': 'Attention',
  'region.activity': 'Activity · Audit console',
  'region.agent': 'Agent',
  'region.project': 'Project',

  'cmd.audit': 'Show activity / audit console',
  'cmd.refreshFleet': 'Refresh fleet',
  'cmd.refreshApprovals': 'Refresh approvals',
  'cmd.admin': 'Open admin & governance',
  'cmd.settings': 'Open settings',
  'cmd.disconnect': 'Disconnect from hub',

  'palette.placeholder': 'Type a command…',
  'palette.noMatches': 'No matches',

  'status.running': 'running',
  'status.paused': 'paused',
  'status.needYou': 'need you',
  'status.hosts': 'hosts',

  'nav.fleet': 'Fleet',
  'nav.projects': 'Projects',
  'nav.noAgents': 'No agents.',
  'nav.noProjects': 'No projects.',
  'common.loading': 'Loading…',

  'audit.loading': 'Loading audit…',
  'audit.time': 'Time',
  'audit.action': 'Action',
  'audit.actor': 'Actor',
  'audit.target': 'Target',
  'audit.empty': 'No audit events.',

  'tx.transcript': 'Transcript',
  'tx.digest': 'Digest',
  'tx.pause': 'Pause',
  'tx.resume': 'Resume',
  'tx.stop': 'Stop',
  'tx.terminate': 'Terminate',
  'tx.archive': 'Archive',
  'tx.sendPlaceholder': 'Send to agent…',
  'tx.send': 'Send',
  'tx.noEvents': 'No events yet.',
  'tx.loadingDigest': 'Loading digest…',

  'att.loading': 'Loading approvals…',
  'att.empty': 'Nothing needs you.',
  'att.approve': 'Approve',
  'att.reject': 'Reject',
  'att.override': 'Override',
  'att.replyPlaceholder': 'Reply…',
  'att.answer': 'Answer',
  'att.dismiss': 'Dismiss',

  'kanban.todo': 'To do',
  'kanban.in_progress': 'In progress',
  'kanban.blocked': 'Blocked',
  'kanban.done': 'Done',
  'kanban.cancelled': 'Cancelled',
  'kanban.loading': 'Loading tasks…',

  'admin.team': 'Team',
  'admin.hosts': 'Hosts',
  'admin.agents': 'Agents',
  'admin.close': 'Close',
  'admin.members': 'Members',
  'admin.policy': 'Policy',
  'admin.noMembers': 'No members.',
  'admin.host': 'Host',
  'admin.status': 'Status',
  'admin.actions': 'Actions',
  'admin.ping': 'Ping',
  'admin.restart': 'Restart',
  'admin.update': 'Update',
  'admin.shutdown': 'Shutdown',
  'admin.kill': 'Kill',
  'admin.noHosts': 'No hosts (or token lacks operator scope).',
  'admin.noAgents': 'No agents (or token lacks operator scope).',

  'confirm.confirm': 'Confirm?',

  'settings.title': 'Settings',
  'settings.appearance': 'Appearance',
  'settings.theme': 'Theme',
  'settings.language': 'Language',
  'settings.connection': 'Connection',
  'settings.disconnect': 'Disconnect',
  'theme.dark': 'Dark',
  'theme.light': 'Light',
  'theme.system': 'System',
};

const zh: Dict = {
  'connect.title': '连接到 Hub',
  'connect.url': 'Hub 地址',
  'connect.team': '团队',
  'connect.token': '令牌',
  'connect.connecting': '连接中…',
  'connect.connect': '连接',

  'shell.admin': '管理',
  'shell.settings': '设置',
  'region.attention': '待办',
  'region.activity': '活动 · 审计台',
  'region.agent': '代理',
  'region.project': '项目',

  'cmd.audit': '显示活动 / 审计台',
  'cmd.refreshFleet': '刷新舰队',
  'cmd.refreshApprovals': '刷新审批',
  'cmd.admin': '打开管理与治理',
  'cmd.settings': '打开设置',
  'cmd.disconnect': '断开 Hub 连接',

  'palette.placeholder': '输入命令…',
  'palette.noMatches': '无匹配',

  'status.running': '运行中',
  'status.paused': '已暂停',
  'status.needYou': '需处理',
  'status.hosts': '主机',

  'nav.fleet': '舰队',
  'nav.projects': '项目',
  'nav.noAgents': '暂无代理。',
  'nav.noProjects': '暂无项目。',
  'common.loading': '加载中…',

  'audit.loading': '加载审计…',
  'audit.time': '时间',
  'audit.action': '动作',
  'audit.actor': '执行者',
  'audit.target': '目标',
  'audit.empty': '暂无审计事件。',

  'tx.transcript': '对话',
  'tx.digest': '摘要',
  'tx.pause': '暂停',
  'tx.resume': '恢复',
  'tx.stop': '停止',
  'tx.terminate': '终止',
  'tx.archive': '归档',
  'tx.sendPlaceholder': '发送给代理…',
  'tx.send': '发送',
  'tx.noEvents': '暂无事件。',
  'tx.loadingDigest': '加载摘要…',

  'att.loading': '加载审批…',
  'att.empty': '无待办。',
  'att.approve': '批准',
  'att.reject': '拒绝',
  'att.override': '覆盖',
  'att.replyPlaceholder': '回复…',
  'att.answer': '回答',
  'att.dismiss': '忽略',

  'kanban.todo': '待办',
  'kanban.in_progress': '进行中',
  'kanban.blocked': '受阻',
  'kanban.done': '完成',
  'kanban.cancelled': '已取消',
  'kanban.loading': '加载任务…',

  'admin.team': '团队',
  'admin.hosts': '主机',
  'admin.agents': '代理',
  'admin.close': '关闭',
  'admin.members': '成员',
  'admin.policy': '策略',
  'admin.noMembers': '暂无成员。',
  'admin.host': '主机',
  'admin.status': '状态',
  'admin.actions': '操作',
  'admin.ping': 'Ping',
  'admin.restart': '重启',
  'admin.update': '更新',
  'admin.shutdown': '关机',
  'admin.kill': '终止',
  'admin.noHosts': '无主机（或令牌无操作员权限）。',
  'admin.noAgents': '无代理（或令牌无操作员权限）。',

  'confirm.confirm': '确认？',

  'settings.title': '设置',
  'settings.appearance': '外观',
  'settings.theme': '主题',
  'settings.language': '语言',
  'settings.connection': '连接',
  'settings.disconnect': '断开连接',
  'theme.dark': '深色',
  'theme.light': '浅色',
  'theme.system': '跟随系统',
};

const DICTS: Record<Lang, Dict> = { en, zh };

const LS_KEY = 'termipod.lang';

function initialLang(): Lang {
  try {
    const v = localStorage.getItem(LS_KEY);
    if (v === 'en' || v === 'zh') return v;
  } catch {
    /* ignore */
  }
  return 'en';
}

interface LangState {
  lang: Lang;
  setLang: (l: Lang) => void;
}

export const useLang = create<LangState>((set) => ({
  lang: initialLang(),
  setLang: (lang) => {
    try {
      localStorage.setItem(LS_KEY, lang);
    } catch {
      /* ignore */
    }
    set({ lang });
  },
}));

/// Returns `t(key)` bound to the current language, with English fallback.
export function useT(): (key: string) => string {
  const lang = useLang((s) => s.lang);
  return (key) => DICTS[lang][key] ?? en[key] ?? key;
}
