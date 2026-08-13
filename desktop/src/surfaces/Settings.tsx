import { useEffect, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react';
import { useLang, useT, type Lang } from '../i18n';
import { isShell, openExternal } from '../platform';
import { invoke } from '../bridge';
import { cacheSizeBytes, clearCache } from '../state/queryClient';
import { comboFromEvent, formatCombo, isBindable, useKeybindings, type BindingAction } from '../state/keybindings';
import { listProfiles, removeProfile, type HubProfile } from '../state/profiles';
import { useSession } from '../state/session';
import { useTheme, type ThemePref } from '../state/theme';
import {
  UI_FONT_SCALE_MAX,
  UI_FONT_SCALE_MIN,
  UI_FONT_SCALE_STEP,
  useAppearance,
  type UiFontFamily,
} from '../state/appearance';
import { getVoiceApiKey, getVoiceModel, setVoiceApiKey, setVoiceModel, VOICE_MODELS } from '../voice/settings';
import { activeRootLabel, useAttachmentConfig } from '../state/attachments';
import { PROXY_CONNS, useProxy, type ProxyConn } from '../state/proxy';
import { PasswordInput } from '../ui/PasswordInput';
import { Icon } from '../ui/Icon';
import { useConfirm } from '../ui/ConfirmModal';
import { AssistantSettings } from './AssistantSettings';
import { UpdateSection } from './UpdateSection';
import { VaultManager } from './VaultManager';
import { VaultPanel } from './VaultPanel';

const REPO_URL = 'https://github.com/physercoe/termipod';

/// Where user-added reference attachments are copied. Read-only display of the
/// active root (a linked Zotero storage folder wins, else custom, else default),
/// with a folder picker to set a custom location and a reset.
function AttachmentLocation(): JSX.Element {
  const t = useT();
  const customRoot = useAttachmentConfig((s) => s.customRoot);
  const defaultRoot = useAttachmentConfig((s) => s.defaultRoot);
  const pickCustom = useAttachmentConfig((s) => s.pickCustom);
  const clearCustom = useAttachmentConfig((s) => s.clearCustom);
  const resolveDefault = useAttachmentConfig((s) => s.resolveDefault);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    void resolveDefault();
  }, [resolveDefault]);

  // Recomputed on any store change (customRoot/defaultRoot are deps of the render).
  const label = activeRootLabel();
  void customRoot;
  void defaultRoot;
  const kindText =
    label.kind === 'zotero'
      ? t('settings.attachZotero')
      : label.kind === 'custom'
        ? t('settings.attachCustom')
        : t('settings.attachDefault');

  return (
    <section className="setting-group">
      <h3>{t('settings.attachments')}</h3>
      <p className="muted small">{t('settings.attachBlurb')}</p>
      <div className="setting-row">
        <label>{kindText}</label>
        <span className="muted mono small">{label.path ?? '—'}</span>
      </div>
      {err !== null && <div className="muted small">{err}</div>}
      <div className="setting-row">
        <button
          onClick={() => {
            setErr(null);
            void pickCustom().then((e) => e !== null && setErr(e));
          }}
        >
          {t('settings.attachChoose')}
        </button>
        {customRoot !== null && <button onClick={() => clearCustom()}>{t('settings.attachReset')}</button>}
      </div>
      {label.kind === 'zotero' && <p className="muted small">{t('settings.attachZoteroNote')}</p>}
    </section>
  );
}

/// DashScope voice-dictation settings (parity — mobile voice_settings_screen):
/// the personal API key (→ OS keychain) and the recognition model. The mic
/// button in the composer is inert until a key is set here.
function VoiceSettings(): JSX.Element {
  const t = useT();
  const [key, setKey] = useState('');
  const [hasKey, setHasKey] = useState(false);
  const [model, setModel] = useState(getVoiceModel());
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    void getVoiceApiKey().then((k) => setHasKey(k !== null && k !== ''));
  }, []);

  async function save(): Promise<void> {
    await setVoiceApiKey(key);
    setVoiceModel(model);
    setHasKey(key.trim() !== '');
    setKey('');
    setSaved(true);
    setTimeout(() => setSaved(false), 1500);
  }

  return (
    <section className="setting-group">
      <h3>{t('settings.voice')}</h3>
      <p className="muted small">{t('settings.voiceBlurb')}</p>
      <div className="setting-row">
        <label>{t('settings.voiceKey')}</label>
        <PasswordInput
          value={key}
          placeholder={hasKey ? t('settings.voiceKeySet') : t('settings.voiceKeyPlaceholder')}
          onChange={(e) => setKey(e.target.value)}
        />
      </div>
      <div className="setting-row">
        <label>{t('settings.voiceModel')}</label>
        <select value={model} onChange={(e) => setModel(e.target.value)}>
          {VOICE_MODELS.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
      </div>
      <div className="setting-row">
        <button onClick={() => void save()}>{saved ? t('settings.voiceSaved') : t('admin.save')}</button>
      </div>
    </section>
  );
}

function AppearanceSettings(): JSX.Element {
  const t = useT();
  const pref = useTheme((s) => s.pref);
  const setPref = useTheme((s) => s.setPref);
  const font = useAppearance((s) => s.font);
  const fontScale = useAppearance((s) => s.fontScale);
  const setFont = useAppearance((s) => s.setFont);
  const setFontScale = useAppearance((s) => s.setFontScale);
  const lang = useLang((s) => s.lang);
  const setLang = useLang((s) => s.setLang);
  const themes: { v: ThemePref; label: string }[] = [
    { v: 'dark', label: t('theme.dark') },
    { v: 'light', label: t('theme.light') },
    { v: 'system', label: t('theme.system') },
  ];
  const langs: { v: Lang; label: string }[] = [
    { v: 'en', label: 'English' },
    { v: 'zh', label: '中文' },
  ];
  const fonts: { v: UiFontFamily; label: string }[] = [
    { v: 'inter', label: 'Inter' },
    { v: 'system', label: t('settings.fontSystem') },
    { v: 'mono', label: 'JetBrains Mono' },
  ];
  return (
    <section className="setting-group">
      <h3>{t('settings.appearance')}</h3>
      <div className="setting-row">
        <label>{t('settings.theme')}</label>
        <div className="seg">
          {themes.map((o) => (
            <button key={o.v} className={pref === o.v ? 'seg-btn active' : 'seg-btn'} onClick={() => setPref(o.v)}>
              {o.label}
            </button>
          ))}
        </div>
      </div>
      <div className="setting-row">
        <label htmlFor="settings-ui-font">{t('settings.font')}</label>
        <select
          id="settings-ui-font"
          className="settings-font-select"
          value={font}
          onChange={(event) => setFont(event.target.value as UiFontFamily)}
        >
          {fonts.map((option) => (
            <option key={option.v} value={option.v}>{option.label}</option>
          ))}
        </select>
      </div>
      <div className="setting-row">
        <label htmlFor="settings-ui-font-size">{t('settings.fontSize')}</label>
        <div className="settings-font-size-control">
          <input
            id="settings-ui-font-size"
            type="range"
            min={UI_FONT_SCALE_MIN * 100}
            max={UI_FONT_SCALE_MAX * 100}
            step={UI_FONT_SCALE_STEP * 100}
            value={fontScale * 100}
            onChange={(event) => setFontScale(Number(event.target.value) / 100)}
          />
          <output htmlFor="settings-ui-font-size">{Math.round(fontScale * 100)}%</output>
        </div>
      </div>
      <div className="setting-row">
        <label>{t('settings.language')}</label>
        <div className="seg">
          {langs.map((o) => (
            <button key={o.v} className={lang === o.v ? 'seg-btn active' : 'seg-btn'} onClick={() => setLang(o.v)}>
              {o.label}
            </button>
          ))}
        </div>
      </div>
    </section>
  );
}

/// Network — the single HTTP-proxy config plus a per-connection toggle deciding
/// which of TermiPod's outbound connections route through it (all default ON once
/// a proxy is set). The proxy is a manual override, else the system/env proxy the
/// Rust `system_proxy` command auto-detects (env vars, Windows registry, macOS
/// `scutil`). Desktop-only. See `state/proxy.ts`.
function NetworkSettings(): JSX.Element {
  const t = useT();
  const override = useProxy((s) => s.override);
  const detected = useProxy((s) => s.detected);
  const use = useProxy((s) => s.use);
  const setOverride = useProxy((s) => s.setOverride);
  const setUse = useProxy((s) => s.setUse);
  const resolveDetected = useProxy((s) => s.resolveDetected);

  useEffect(() => {
    void resolveDetected();
  }, [resolveDetected]);

  const effective = override.trim() !== '' ? override.trim() : detected;
  const connLabels: Record<ProxyConn, string> = {
    hub: t('network.connHub'),
    attachments: t('network.connAttachments'),
    workspace: t('network.connWorkspace'),
    discovery: t('network.connDiscovery'),
    update: t('network.connUpdate'),
    drawio: t('network.connDrawio'),
    webtab: t('network.connWebtab'),
  };

  return (
    <section className="setting-group">
      <h3>{t('network.title')}</h3>
      <p className="muted small">{t('network.blurb')}</p>
      <div className="setting-row">
        <label>{t('network.proxyUrl')}</label>
        <input
          className="network-proxy-input"
          type="text"
          spellCheck={false}
          placeholder={detected ?? 'http://proxy.corp:8080'}
          value={override}
          onChange={(e) => setOverride(e.target.value)}
        />
      </div>
      <p className="muted small">{detected ? `${t('network.detected')} ${detected}` : t('network.noneDetected')}</p>

      <h4 className="network-subhead">{t('network.useFor')}</h4>
      {effective === null || effective === undefined || effective === '' ? (
        <p className="muted small">{t('network.noProxyHint')}</p>
      ) : null}
      <div className="network-conns">
        {PROXY_CONNS.map((c) => (
          <label key={c} className="network-conn">
            <input type="checkbox" checked={use[c]} onChange={(e) => setUse(c, e.target.checked)} />
            <span>{connLabels[c]}</span>
          </label>
        ))}
      </div>
      {isShell() && <ClearWebtabData />}
    </section>
  );
}

/// "Clear web-tab browsing data" — wipes the persistent `persist:webtab`
/// partition (cookies/storage/cache the in-app browser accumulates). Closes the
/// privacy loop the persistent partition opens (plan §4).
function ClearWebtabData(): JSX.Element {
  const t = useT();
  const [done, setDone] = useState(false);
  return (
    <div className="setting-row network-clear-webtab">
      <div>
        <label>{t('network.clearWebtab')}</label>
        <p className="muted small">{t('network.clearWebtabHint')}</p>
      </div>
      <button
        className="import-btn"
        onClick={() => {
          setDone(false);
          void invoke('webtab_clear_data')
            .then(() => setDone(true))
            .catch(() => undefined);
        }}
      >
        {done ? t('network.clearWebtabDone') : t('network.clearWebtab')}
      </button>
    </div>
  );
}

function CacheSettings(): JSX.Element {
  const t = useT();
  const [cacheKb, setCacheKb] = useState(() => Math.round(cacheSizeBytes() / 1024));
  return (
    <section className="setting-group">
      <h3>{t('settings.cache')}</h3>
      <p className="muted small">{t('settings.cacheBlurb')}</p>
      <div className="setting-row">
        <label>{t('settings.cacheSize')}</label>
        <span className="muted">{cacheKb} KB</span>
      </div>
      <div className="setting-row">
        <button
          onClick={() => {
            clearCache();
            setCacheKb(0);
          }}
        >
          {t('settings.clearCache')}
        </button>
      </div>
    </section>
  );
}

/// Account — the current hub connection plus hub-profile management (parity with
/// the mobile hub-profile / team switcher). Add/edit a profile reuses the shell's
/// connect panel (`onConnect`); switching re-binds the client and drops the cache.
function AccountSettings({ onConnect }: { onConnect?: (edit?: HubProfile) => void }): JSX.Element {
  const t = useT();
  const { ask: confirmAsk, node: confirmNode } = useConfirm();
  const client = useSession((s) => s.client);
  const activeId = useSession((s) => s.activeProfileId);
  const switchProfile = useSession((s) => s.switchProfile);
  const disconnect = useSession((s) => s.disconnect);
  const teamId = useSession((s) => s.config.teamId);
  const baseUrl = useSession((s) => s.config.baseUrl);
  const [profiles, setProfiles] = useState<HubProfile[]>(() => listProfiles());

  async function remove(id: string): Promise<void> {
    const name = profiles.find((p) => p.id === id)?.name ?? '';
    if (!(await confirmAsk({ message: t('profile.confirmRemove').replace('{name}', name), danger: true }))) return;
    // Drop the live client too when removing the active profile (parity with the
    // titlebar switcher) — otherwise the app keeps driving a deleted hub.
    const wasActive = useSession.getState().activeProfileId === id;
    await removeProfile(id);
    setProfiles(listProfiles());
    if (wasActive) disconnect();
  }
  async function onDisconnect(): Promise<void> {
    if (await confirmAsk({ message: t('settings.confirmDisconnect') })) disconnect();
  }

  return (
    <>
      <section className="setting-group">
        <h3>{t('settings.accountCurrent')}</h3>
        {client === null ? (
          <p className="muted small">{t('settings.accountOffline')}</p>
        ) : (
          <>
            <div className="setting-row">
              <label>{t('settings.accountHub')}</label>
              <span className="muted">{baseUrl}</span>
            </div>
            <div className="setting-row">
              <label>{t('connect.team')}</label>
              <span className="muted">{teamId}</span>
            </div>
          </>
        )}
        <div className="setting-row">
          {client === null ? (
            <button className="primary" onClick={() => onConnect?.()}>
              {t('shell.connect')}
            </button>
          ) : (
            <button className="danger" onClick={() => void onDisconnect()}>
              {t('settings.disconnect')}
            </button>
          )}
        </div>
      </section>

      <section className="setting-group">
        <h3>{t('settings.accountProfiles')}</h3>
        <p className="muted small">{t('settings.accountProfilesBlurb')}</p>
        <div className="profile-list">
          {profiles.map((p) => (
            <div key={p.id} className={p.id === activeId ? 'profile-item active' : 'profile-item'}>
              <button
                className="profile-pick"
                disabled={p.id === activeId}
                onClick={() => void switchProfile(p.id)}
              >
                <span className="profile-name">{p.name}</span>
                <span className="muted small">
                  {p.teamId} · {p.baseUrl.replace(/^https?:\/\//, '')}
                </span>
              </button>
              {p.id === activeId && <span className="pill ok">{t('settings.accountActive')}</span>}
              <button className="link-btn" onClick={() => onConnect?.(p)}>
                {t('profile.edit')}
              </button>
              <button className="link-btn" onClick={() => void remove(p.id)}>
                {t('profile.remove')}
              </button>
            </div>
          ))}
          {profiles.length === 0 && <div className="muted small">{t('profile.none')}</div>}
        </div>
        <div className="setting-row">
          <button onClick={() => onConnect?.()}>+ {t('profile.add')}</button>
        </div>
      </section>
      {confirmNode}
    </>
  );
}

/// About — app identity, version, and where to send feedback / read the source.
/// Keyboard shortcuts (#460): rebind the app-level chords (command palette,
/// assistant dock, terminal dock, split panes, and — D2.1 — the annotate
/// trigger, which ships UNBOUND: its button shows "not set" until the user
/// captures a chord). Click a binding, then press the new keys — a bare
/// modifier keeps listening, Esc cancels, and an invalid combo (no modifier /
/// a ⌘<digit> job-switch chord — see `isBindable`) or one already taken by
/// another action is rejected inline. Job switching itself stays fixed
/// (position-based, like VS Code), so it is documented here but not rebindable.
function ShortcutSettings(): JSX.Element {
  const t = useT();
  const bindings = useKeybindings((s) => s.bindings);
  const setBinding = useKeybindings((s) => s.setBinding);
  const resetBindings = useKeybindings((s) => s.resetBindings);
  const [capturing, setCapturing] = useState<BindingAction | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const mac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform);

  const rows: { action: BindingAction; label: string }[] = [
    { action: 'palette', label: t('settings.scPalette') },
    { action: 'assistant', label: t('settings.scAssistant') },
    { action: 'terminal', label: t('settings.scTerminal') },
    { action: 'splitToggle', label: t('settings.scSplitToggle') },
    { action: 'splitSwap', label: t('settings.scSplitSwap') },
    { action: 'annotate', label: t('settings.scAnnotate') },
  ];

  function onCapture(action: BindingAction, e: ReactKeyboardEvent): void {
    e.preventDefault();
    e.stopPropagation(); // don't let the AppShell global handler fire on the captured chord
    if (e.key === 'Escape') {
      setCapturing(null);
      setErr(null);
      return;
    }
    const combo = comboFromEvent(e);
    if (combo === null) return; // a bare modifier press — keep listening
    if (!isBindable(combo)) {
      setErr(t('settings.scInvalid'));
      return;
    }
    const clash = rows.find((r) => r.action !== action && bindings[r.action] === combo);
    if (clash !== undefined) {
      setErr(t('settings.scConflict').replace('{name}', clash.label));
      return;
    }
    setBinding(action, combo);
    setCapturing(null);
    setErr(null);
  }

  return (
    <section className="setting-group">
      <h3>{t('settings.shortcuts')}</h3>
      <p className="muted small">{t('settings.shortcutsBlurb')}</p>
      {rows.map((r) => (
        <div className="setting-row" key={r.action}>
          <label>{r.label}</label>
          <button
            className={capturing === r.action ? 'primary' : ''}
            onClick={() => {
              setCapturing(r.action);
              setErr(null);
            }}
            onBlur={() => {
              if (capturing === r.action) setCapturing(null);
            }}
            onKeyDown={(e) => {
              if (capturing === r.action) onCapture(r.action, e);
            }}
          >
            {capturing === r.action
              ? t('settings.scRecording')
              : bindings[r.action] !== ''
                ? formatCombo(bindings[r.action], mac)
                : t('settings.scUnset')}
          </button>
        </div>
      ))}
      {err !== null && <div className="error small">{err}</div>}
      <div className="setting-row">
        <button
          onClick={() => {
            resetBindings();
            setErr(null);
          }}
        >
          {t('settings.scReset')}
        </button>
      </div>
    </section>
  );
}

function AboutSettings(): JSX.Element {
  const t = useT();
  return (
    <section className="setting-group">
      <div className="about-head">
        <div className="about-name">TermiPod</div>
        <div className="muted small">{t('settings.aboutTagline')}</div>
      </div>
      <div className="setting-row">
        <label>{t('settings.version')}</label>
        <span className="muted mono">{__APP_VERSION__}</span>
      </div>
      <p className="muted small">{t('settings.aboutBlurb')}</p>
      <div className="setting-row about-links">
        <button onClick={() => openExternal(`${REPO_URL}/issues`)}>
          {t('settings.feedback')} <Icon name="external" size={13} />
        </button>
        <button onClick={() => openExternal(REPO_URL)}>
          {t('settings.sourceCode')} <Icon name="external" size={13} />
        </button>
      </div>
    </section>
  );
}

type CatId = 'account' | 'display' | 'input' | 'data' | 'network' | 'assistant' | 'vault' | 'about';
const CAT_LS_KEY = 'termipod.settings.cat';

/// The Settings job surface (pinned to the bottom of the activity bar). Where the
/// round-1 build stacked every device preference into one scrolling modal, this
/// splits them into a left category rail + a content pane, so the surface scales
/// as more settings land instead of becoming an undifferentiated wall. Team/hub
/// *policy* still lives in the Admin cockpit, not here — this is device prefs.
export function SettingsSurface({ onConnect }: { onConnect?: (edit?: HubProfile) => void }): JSX.Element {
  const t = useT();
  const tauri = isShell();

  // Order (director spec): Account first · Display · Input · Data (local storage)
  // · Vault (sensitive credentials — keys + cross-device sync) · About last
  // (which now also carries the software-update panel on desktop).
  const cats: { id: CatId; label: string; render: () => JSX.Element }[] = [
    { id: 'account', label: t('settings.catAccount'), render: () => <AccountSettings onConnect={onConnect} /> },
    { id: 'display', label: t('settings.catDisplay'), render: () => <AppearanceSettings /> },
    // Input owns both voice and keyboard interaction. Shortcuts are available in
    // every build; voice remains native-shell only.
    {
      id: 'input',
      label: t('settings.catInput'),
      render: () => (
        <>
          {tauri && <VoiceSettings />}
          <ShortcutSettings />
        </>
      ),
    },
    {
      id: 'data',
      label: t('settings.catData'),
      render: () => (
        <>
          {tauri && <AttachmentLocation />}
          <CacheSettings />
        </>
      ),
    },
    // Assistant — the embedded kimi-code assistant's server status + its
    // customization surfaces (config/mcp/skills/agents/plugins), desktop only.
    ...(tauri
      ? [{ id: 'assistant' as const, label: t('settings.catAssistant'), render: () => <AssistantSettings /> }]
      : []),
    // Network — proxy config for every outbound connection (desktop only; the
    // browser build has no native HTTP core to route).
    ...(tauri
      ? [{ id: 'network' as const, label: t('settings.catNetwork'), render: () => <NetworkSettings /> }]
      : []),
    // Vault — the home for sensitive credentials (SSH keys, and the connection
    // secrets the zero-knowledge vault seals) plus the cross-device sync.
    ...(tauri
      ? [
          {
            id: 'vault' as const,
            label: t('settings.catVault'),
            render: () => (
              <>
                <p className="muted small settings-lead">{t('settings.vaultLead')}</p>
                <VaultManager />
                <VaultPanel />
              </>
            ),
          },
        ]
      : []),
    // About — app identity + version, and (desktop only) the software-update
    // panel folded in beneath it, so there is one "what/which version am I
    // running, and is there a newer one" home instead of two sibling tabs.
    {
      id: 'about',
      label: t('settings.catAbout'),
      render: () => (
        <>
          <AboutSettings />
          {tauri && <UpdateSection />}
        </>
      ),
    },
  ];

  const [cat, setCat] = useState<CatId>(() => {
    let saved = localStorage.getItem(CAT_LS_KEY);
    if (saved === 'sshkeys') saved = 'vault'; // migrate the renamed category
    if (saved === 'updates') saved = 'about'; // Updates merged into About
    if (saved === 'shortcuts') saved = 'input'; // Keyboard merged into Input
    if (saved === 'env-profiles') saved = 'account'; // Team environments moved to Fleet Admin
    return saved !== null && cats.some((c) => c.id === saved) ? (saved as CatId) : 'account';
  });
  function pick(id: CatId): void {
    setCat(id);
    try {
      localStorage.setItem(CAT_LS_KEY, id);
    } catch {
      /* ignore */
    }
  }

  const active = cats.find((c) => c.id === cat) ?? cats[0];

  return (
    <div className="settings-surface">
      <aside className="settings-cats">
        <div className="settings-cats-head">{t('settings.title')}</div>
        {cats.map((c) => (
          <button
            key={c.id}
            className={`settings-cat${c.id === active.id ? ' active' : ''}`}
            onClick={() => pick(c.id)}
          >
            {c.label}
          </button>
        ))}
      </aside>
      <div className={`settings-content${active.id === 'vault' ? ' wide' : ''}`}>
        <div className="settings-content-inner">
          <h2 className="settings-content-title">{active.label}</h2>
          {active.render()}
        </div>
      </div>
    </div>
  );
}
