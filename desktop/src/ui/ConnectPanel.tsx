import { useState } from 'react';
import { HubClient } from '../hub/client';
import { configComplete, type HubConfig } from '../hub/config';
import { useSession } from '../state/session';

/// First-run connect form: enter hub URL + team + token, probe `/v1/_info`,
/// then connect. The token is held in memory only (browser build).
export function ConnectPanel(): JSX.Element {
  const persisted = useSession((s) => s.config);
  const connect = useSession((s) => s.connect);
  const [form, setForm] = useState<HubConfig>(persisted);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const set = (k: keyof HubConfig) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm((f) => ({ ...f, [k]: e.target.value }));

  async function submit(e: React.FormEvent): Promise<void> {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await new HubClient(form).probe(); // validate URL/token before committing
      connect(form);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="connect" onSubmit={submit}>
      <h2>Connect to a hub</h2>
      <label>
        Hub URL
        <input value={form.baseUrl} onChange={set('baseUrl')} placeholder="https://hub.example.com" />
      </label>
      <label>
        Team
        <input value={form.teamId} onChange={set('teamId')} placeholder="team id" />
      </label>
      <label>
        Token
        <input value={form.token} onChange={set('token')} type="password" placeholder="bearer token" />
      </label>
      {error !== null && <div className="error">{error}</div>}
      <button className="primary" type="submit" disabled={busy || !configComplete(form)}>
        {busy ? 'Connecting…' : 'Connect'}
      </button>
    </form>
  );
}
