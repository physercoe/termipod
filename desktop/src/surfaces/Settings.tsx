import { useEffect, useState } from 'react';
import { useLang, useT, type Lang } from '../i18n';
import { isTauri } from '../platform';
import { cacheSizeBytes, clearCache } from '../state/queryClient';
import { useSession } from '../state/session';
import { useTheme, type ThemePref } from '../state/theme';
import { getVoiceApiKey, getVoiceModel, setVoiceApiKey, setVoiceModel, VOICE_MODELS } from '../voice/settings';
import { activeRootLabel, useAttachmentConfig } from '../state/attachments';
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
        {customRoot !== null && (
          <button onClick={() => clearCustom()}>{t('settings.attachReset')}</button>
        )}
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

/// Device settings (mirrors the mobile Settings surface's device-prefs role):
/// appearance (theme + language) and the current connection. Team/hub policy
/// lives in the Admin cockpit, not here.
export function Settings({ onClose }: { onClose: () => void }): JSX.Element {
  const t = useT();
  const pref = useTheme((s) => s.pref);
  const setPref = useTheme((s) => s.setPref);
  const lang = useLang((s) => s.lang);
  const setLang = useLang((s) => s.setLang);
  const disconnect = useSession((s) => s.disconnect);
  const teamId = useSession((s) => s.config.teamId);
  const baseUrl = useSession((s) => s.config.baseUrl);

  const themes: { v: ThemePref; label: string }[] = [
    { v: 'dark', label: t('theme.dark') },
    { v: 'light', label: t('theme.light') },
    { v: 'system', label: t('theme.system') },
  ];
  const langs: { v: Lang; label: string }[] = [
    { v: 'en', label: 'English' },
    { v: 'zh', label: '中文' },
  ];

  const [cacheKb, setCacheKb] = useState(() => Math.round(cacheSizeBytes() / 1024));

  // Draggable dialog: the modal is centred by default; grabbing its header offsets
  // it so it can be repositioned (director: "the window cannot move"). Track via
  // window listeners — WebView2's setPointerCapture is unreliable ([[memory]]).
  const [drag, setDrag] = useState({ x: 0, y: 0 });
  function startDrag(e: React.MouseEvent): void {
    // Don't start a drag from the close button.
    if ((e.target as HTMLElement).closest('button') !== null) return;
    e.preventDefault();
    const startX = e.clientX;
    const startY = e.clientY;
    const base = { ...drag };
    const move = (ev: MouseEvent): void => {
      setDrag({ x: base.x + (ev.clientX - startX), y: base.y + (ev.clientY - startY) });
    };
    const up = (): void => {
      window.removeEventListener('mousemove', move);
      window.removeEventListener('mouseup', up);
    };
    window.addEventListener('mousemove', move);
    window.addEventListener('mouseup', up);
  }

  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div
        className="settings"
        onMouseDown={(e) => e.stopPropagation()}
        style={drag.x !== 0 || drag.y !== 0 ? { transform: `translate(${drag.x}px, ${drag.y}px)` } : undefined}
      >
        <div className="admin-tabs admin-tabs-drag" onMouseDown={startDrag}>
          <strong>{t('settings.title')}</strong>
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="admin-body">
          <section className="setting-group">
            <h3>{t('settings.appearance')}</h3>
            <div className="setting-row">
              <label>{t('settings.theme')}</label>
              <div className="seg">
                {themes.map((o) => (
                  <button
                    key={o.v}
                    className={pref === o.v ? 'seg-btn active' : 'seg-btn'}
                    onClick={() => setPref(o.v)}
                  >
                    {o.label}
                  </button>
                ))}
              </div>
            </div>
            <div className="setting-row">
              <label>{t('settings.language')}</label>
              <div className="seg">
                {langs.map((o) => (
                  <button
                    key={o.v}
                    className={lang === o.v ? 'seg-btn active' : 'seg-btn'}
                    onClick={() => setLang(o.v)}
                  >
                    {o.label}
                  </button>
                ))}
              </div>
            </div>
          </section>

          <UpdateSection />

          <VaultPanel />

          {isTauri() && <AttachmentLocation />}

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

          {isTauri() && <VoiceSettings />}

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
              <button
                className="danger"
                onClick={() => {
                  onClose();
                  disconnect();
                }}
              >
                {t('settings.disconnect')}
              </button>
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}
