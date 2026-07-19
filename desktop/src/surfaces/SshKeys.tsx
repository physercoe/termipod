import { useEffect, useRef, useState } from 'react';
import { useT } from '../i18n';
import { deleteKey, importKey, listKeys, type SshKeyMeta } from '../state/keys';
import { ConfirmButton } from '../ui/ConfirmButton';
import { PasswordInput } from '../ui/PasswordInput';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/// Dedicated SSH-key management (Settings → SSH Keys). Import a private key by
/// pasting the PEM *or* choosing a local key file (`~/.ssh/id_ed25519`, a `.pem`,
/// …) — the file is read in the web layer via the File API (no extra Tauri
/// plugin) and validated/introspected by the Rust core (`ssh_parse_key`). Secrets
/// live in the OS keychain, never in localStorage. Saved keys are pickable in the
/// terminal's SSH connect form.
export function SshKeysSettings(): JSX.Element {
  const t = useT();
  const [keys, setKeys] = useState<SshKeyMeta[]>([]);
  const [name, setName] = useState('');
  const [pem, setPem] = useState('');
  const [passphrase, setPassphrase] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    setKeys(listKeys());
  }, []);

  // Read a chosen key file into the PEM field (File.text() works in WebView2
  // without any filesystem plugin); default the name to the file's basename.
  async function onFile(e: React.ChangeEvent<HTMLInputElement>): Promise<void> {
    const file = e.target.files?.[0];
    e.target.value = ''; // allow re-picking the same file
    if (file === undefined) return;
    setErr(null);
    try {
      const text = await file.text();
      setPem(text);
      if (name.trim() === '') setName(file.name.replace(/\.(pem|key|txt)$/i, ''));
    } catch (ex) {
      setErr(msg(ex));
    }
  }

  async function doImport(): Promise<void> {
    setBusy(true);
    setErr(null);
    try {
      await importKey({ name: name.trim() || 'key', pem, passphrase });
      setName('');
      setPem('');
      setPassphrase('');
      setKeys(listKeys());
    } catch (ex) {
      setErr(msg(ex));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="setting-group">
      <h3>{t('term.keys')}</h3>
      <p className="muted small">{t('settings.sshKeysBlurb')}</p>

      <div className="sshkey-import">
        <div className="sshkey-import-row">
          <input
            className="sshkey-name"
            placeholder={t('term.keyName')}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <button onClick={() => fileRef.current?.click()}>{t('term.importKeyFile')}</button>
          {/* No `accept` filter — private keys are commonly extensionless
              (`~/.ssh/id_ed25519`, `id_rsa`), which an extension filter hides. */}
          <input ref={fileRef} type="file" hidden onChange={(e) => void onFile(e)} />
        </div>
        <textarea
          className="sshkey-pem"
          placeholder={t('term.keyPlaceholder')}
          spellCheck={false}
          value={pem}
          onChange={(e) => setPem(e.target.value)}
        />
        <div className="sshkey-import-row">
          <PasswordInput
            wrapClassName="sshkey-pass"
            placeholder={t('term.passphrase')}
            value={passphrase}
            onChange={(e) => setPassphrase(e.target.value)}
          />
          <button className="primary" disabled={busy || pem.trim() === ''} onClick={() => void doImport()}>
            {t('term.importKey')}
          </button>
        </div>
        {err !== null && <div className="error">{err}</div>}
      </div>

      <div className="sshkey-list">
        {keys.length === 0 && <div className="muted small">{t('term.noKeys')}</div>}
        {keys.map((k) => (
          <div key={k.id} className="key-item">
            <span className="key-name">{k.name}</span>
            <span className="muted small">{k.type}</span>
            {k.source === 'imported' && <span className="muted small">· {t('term.keyImported')}</span>}
            <span className="spacer" />
            <ConfirmButton
              label={t('term.delete')}
              danger
              onConfirm={() => void deleteKey(k.id).then(() => setKeys(listKeys()))}
            />
          </div>
        ))}
      </div>
    </section>
  );
}
