import { useEffect, useState } from 'react';
import { useLang, useT, type Lang } from '../i18n';
import { isTauri } from '../platform';
import { cacheSizeBytes, clearCache } from '../state/queryClient';
import { useSession } from '../state/session';
import { useTheme, type ThemePref } from '../state/theme';
import { getVoiceApiKey, getVoiceModel, setVoiceApiKey, setVoiceModel, VOICE_MODELS } from '../voice/settings';
import { activeRootLabel, useAttachmentConfig } from '../state/attachments';
import { SshKeysSettings } from './SshKeys';
import { UpdateSection } from './UpdateSection';
import { VaultPanel } from './VaultPanel';

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
        <input
          type="password"
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

function ConnectionSettings(): JSX.Element {
  const t = useT();
  const disconnect = useSession((s) => s.disconnect);
  const teamId = useSession((s) => s.config.teamId);
  const baseUrl = useSession((s) => s.config.baseUrl);
  return (
    <section className="setting-group">
      <h3>{t('settings.connection')}</h3>
      <div className="setting-row">
        <label>Hub</label>
        <span className="muted">{baseUrl}</span>
      </div>
      <div className="setting-row">
        <label>{t('connect.team')}</label>
        <span className="muted">{teamId}</span>
      </div>
      <div className="setting-row">
        <button className="danger" onClick={() => disconnect()}>
          {t('settings.disconnect')}
        </button>
      </div>
    </section>
  );
}

type CatId = 'appearance' | 'updates' | 'security' | 'sshkeys' | 'storage' | 'voice' | 'connection';
const CAT_LS_KEY = 'termipod.settings.cat';

/// The Settings job surface (pinned to the bottom of the activity bar). Where the
/// round-1 build stacked every device preference into one scrolling modal, this
/// splits them into a left category rail + a content pane, so the surface scales
/// as more settings land instead of becoming an undifferentiated wall. Team/hub
/// *policy* still lives in the Admin cockpit, not here — this is device prefs.
export function SettingsSurface(): JSX.Element {
  const t = useT();
  const tauri = isTauri();

  const cats: { id: CatId; label: string; render: () => JSX.Element }[] = [
    { id: 'appearance', label: t('settings.catAppearance'), render: () => <AppearanceSettings /> },
    { id: 'updates', label: t('settings.catUpdates'), render: () => <UpdateSection /> },
    { id: 'security', label: t('settings.catSecurity'), render: () => <VaultPanel /> },
    ...(tauri
      ? [
          {
            id: 'sshkeys' as const,
            label: t('settings.catSshKeys'),
            render: () => <SshKeysSettings />,
          },
          {
            id: 'storage' as const,
            label: t('settings.catStorage'),
            render: () => (
              <>
                <AttachmentLocation />
                <CacheSettings />
              </>
            ),
          },
          { id: 'voice' as const, label: t('settings.catVoice'), render: () => <VoiceSettings /> },
        ]
      : [{ id: 'storage' as const, label: t('settings.catStorage'), render: () => <CacheSettings /> }]),
    { id: 'connection', label: t('settings.catConnection'), render: () => <ConnectionSettings /> },
  ];

  const [cat, setCat] = useState<CatId>(() => {
    const saved = localStorage.getItem(CAT_LS_KEY) as CatId | null;
    return saved !== null && cats.some((c) => c.id === saved) ? saved : 'appearance';
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
      <div className="settings-content">
        <h2 className="settings-content-title">{active.label}</h2>
        {active.render()}
      </div>
    </div>
  );
}
