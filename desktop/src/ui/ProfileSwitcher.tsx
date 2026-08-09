import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useT } from '../i18n';
import { Icon } from './Icon';
import { listProfiles, removeProfile, type HubProfile } from '../state/profiles';
import { useSession } from '../state/session';
import { useConfirm } from './ConfirmModal';

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
  const { ask: confirmAsk, node: confirmNode } = useConfirm();
  const activeId = useSession((s) => s.activeProfileId);
  const teamId = useSession((s) => s.config.teamId);
  const switchProfile = useSession((s) => s.switchProfile);
  const [open, setOpen] = useState(false);
  const [profiles, setProfiles] = useState<HubProfile[]>([]);
  const [menuPos, setMenuPos] = useState<{ left: number; bottom: number; width: number } | null>(null);
  const ref = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  function placeMenu(): void {
    const button = buttonRef.current;
    if (button === null) return;
    const rect = button.getBoundingClientRect();
    const width = Math.min(240, window.innerWidth - 16);
    setMenuPos({
      left: Math.max(8, Math.min(rect.right - width, window.innerWidth - width - 8)),
      bottom: window.innerHeight - rect.top + 4,
      width,
    });
  }

  useEffect(() => {
    if (open) setProfiles(listProfiles());
  }, [open]);

  useEffect(() => {
    function onDoc(e: MouseEvent): void {
      const target = e.target as Node;
      if (ref.current !== null && !ref.current.contains(target) && !menuRef.current?.contains(target)) setOpen(false);
    }
    function onKey(e: KeyboardEvent): void {
      if (e.key === 'Escape') setOpen(false);
    }
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onKey);
    };
  }, []);

  useEffect(() => {
    if (!open) return;
    placeMenu();
    const reposition = (): void => placeMenu();
    window.addEventListener('resize', reposition);
    window.addEventListener('scroll', reposition, true);
    return () => {
      window.removeEventListener('resize', reposition);
      window.removeEventListener('scroll', reposition, true);
    };
  }, [open]);

  const active = profiles.find((p) => p.id === activeId);
  const label = active?.name ?? teamId;

  return (
    <div className="profile-switcher" ref={ref}>
      <button
        ref={buttonRef}
        className="pill switcher-pill"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => {
          if (!open) placeMenu();
          setOpen((v) => !v);
        }}
      >
        <span className="switcher-label">{label}</span>
        <Icon name="chevron-down" size={13} />
      </button>
      {open && menuPos !== null && createPortal(
        <div ref={menuRef} className="switcher-menu switcher-menu-status" role="menu" style={menuPos}>
          {profiles.map((p) => (
            <div key={p.id} className={p.id === activeId ? 'switcher-item active' : 'switcher-item'}>
              <button
                className="switcher-pick"
                role="menuitem"
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
              <span className="switcher-item-actions">
                <button className="link-btn" onClick={() => { setOpen(false); onEdit(p); }}>
                  {t('profile.edit')}
                </button>
                <button
                  className="link-btn"
                  onClick={() => {
                    void (async () => {
                      if (!(await confirmAsk({ message: t('profile.confirmRemove').replace('{name}', p.name), danger: true })))
                        return;
                      // Tear down the live client too when the removed profile is
                      // the active one — otherwise the app keeps driving a deleted
                      // hub until relaunch.
                      const wasActive = useSession.getState().activeProfileId === p.id;
                      await removeProfile(p.id);
                      setProfiles(listProfiles());
                      if (wasActive) useSession.getState().disconnect();
                    })();
                  }}
                >
                  {t('profile.remove')}
                </button>
              </span>
            </div>
          ))}
          {profiles.length === 0 && <div className="muted small switcher-empty">{t('profile.none')}</div>}
          <button className="switcher-add" role="menuitem" onClick={() => { setOpen(false); onAdd(); }}>
            + {t('profile.add')}
          </button>
        </div>,
        document.body,
      )}
      {confirmNode}
    </div>
  );
}
