import { useEffect, useRef, useState } from 'react';
import { useT } from '../i18n';
import { listProfiles, removeProfile, type HubProfile } from '../state/profiles';
import { useSession } from '../state/session';

/// Titlebar hub switcher (parity Phase 3a). Shows the active profile as a pill;
/// the dropdown lists saved profiles to switch between, plus add/edit/remove.
/// Switching re-binds the client and drops the query cache (session store).
export function ProfileSwitcher({
  onAdd,
  onEdit,
}: {
  onAdd: () => void;
  onEdit: (p: HubProfile) => void;
}): JSX.Element {
  const t = useT();
  const activeId = useSession((s) => s.activeProfileId);
  const teamId = useSession((s) => s.config.teamId);
  const switchProfile = useSession((s) => s.switchProfile);
  const [open, setOpen] = useState(false);
  const [profiles, setProfiles] = useState<HubProfile[]>([]);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (open) setProfiles(listProfiles());
  }, [open]);

  useEffect(() => {
    function onDoc(e: MouseEvent): void {
      if (ref.current !== null && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener('mousedown', onDoc);
    return () => document.removeEventListener('mousedown', onDoc);
  }, []);

  const active = profiles.find((p) => p.id === activeId);
  const label = active?.name ?? teamId;

  return (
    <div className="profile-switcher" ref={ref}>
      <button className="pill switcher-pill" onClick={() => setOpen((v) => !v)}>
        {label} ▾
      </button>
      {open && (
        <div className="switcher-menu">
          {profiles.map((p) => (
            <div key={p.id} className={p.id === activeId ? 'switcher-item active' : 'switcher-item'}>
              <button
                className="switcher-pick"
                onClick={() => {
                  void switchProfile(p.id);
                  setOpen(false);
                }}
              >
                <span className="switcher-name">{p.name}</span>
                <span className="muted small">
                  {p.teamId} · {p.baseUrl.replace(/^https?:\/\//, '')}
                </span>
              </button>
              <button className="link-btn" onClick={() => { setOpen(false); onEdit(p); }}>
                {t('profile.edit')}
              </button>
              <button
                className="link-btn"
                onClick={() => {
                  void removeProfile(p.id).then(() => setProfiles(listProfiles()));
                }}
              >
                {t('profile.remove')}
              </button>
            </div>
          ))}
          {profiles.length === 0 && <div className="muted small switcher-empty">{t('profile.none')}</div>}
          <button className="switcher-add" onClick={() => { setOpen(false); onAdd(); }}>
            + {t('profile.add')}
          </button>
        </div>
      )}
    </div>
  );
}
