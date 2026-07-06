import { useEffect, useState } from 'react';
import { useLang, useT, type Lang } from '../i18n';
import { isTauri } from '../platform';
import { cacheSizeBytes, clearCache } from '../state/queryClient';
import { useSession } from '../state/session';
import { useTheme, type ThemePref } from '../state/theme';
import { getVoiceApiKey, getVoiceModel, setVoiceApiKey, setVoiceModel, VOICE_MODELS } from '../voice/settings';
import { UpdateSection } from './UpdateSection';
import { VaultPanel } from './VaultPanel';

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

  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="settings" onMouseDown={(e) => e.stopPropagation()}>
        <div className="admin-tabs">
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
