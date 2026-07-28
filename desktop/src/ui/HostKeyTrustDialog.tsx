import { useT } from '../i18n';
import { Modal } from './Modal';
import type { HostKeyInfo } from '../vault/envSecrets';

/// Shared host-key trust dialog (ADR-056 D-2). Shows the target host's env-key
/// fingerprint short code for the operator to compare against the host's console
/// banner before secrets are sealed to it, and offers the deliberate re-trust
/// step when a pinned key has changed (a legitimate host `--rekey` vs a
/// substitution — only the operator can tell, by checking the NEW code).
///
/// Used by two flows: agent spawn (AgentSpawn) and session teleport re-seal
/// (SessionsPanel). Everything but the confirm-button wording is surface-neutral
/// and reuses the `spawn.trust*` / `spawn.retrust*` strings; the confirm label
/// names the action that follows ("… & spawn" vs "… & teleport"), so the caller
/// passes it in.
export function HostKeyTrustDialog(props: {
  info: HostKeyInfo;
  sealing: boolean;
  confirmLabel: string;
  retrustConfirmLabel: string;
  onCancel: () => void;
  onConfirm: () => void;
}): JSX.Element {
  const t = useT();
  const { info, sealing } = props;
  const changed = info.changedFrom !== undefined;
  return (
    <Modal
      onClose={props.onCancel}
      className="task-detail"
      ariaLabel={changed ? t('spawn.retrustTitle') : t('spawn.trustTitle')}
    >
      <div className="admin-tabs">
        <strong>{changed ? t('spawn.retrustTitle') : t('spawn.trustTitle')}</strong>
        <span className="spacer" />
      </div>
      <div className="task-form">
        {changed ? (
          // A pinned key changed: show BOTH codes. A deliberate host --rekey is
          // legitimate; only the operator can tell it from a substitution, by
          // checking the NEW code against the host console.
          <>
            <div className="wide">{t('spawn.retrustBody')}</div>
            <div className="wide muted small">{t('spawn.retrustOld')}</div>
            <div className="wide spawn-fingerprint muted">{info.changedFrom}</div>
            <div className="wide muted small">{t('spawn.retrustNew')}</div>
          </>
        ) : (
          <div className="wide">{t('spawn.trustBody')}</div>
        )}
        <div className="wide spawn-fingerprint">{info.fingerprint}</div>
        <div className="wide muted small">{t('spawn.trustHint')}</div>
        <div className="wide task-form-actions">
          <button onClick={props.onCancel}>{t('admin.close')}</button>
          <span className="spacer" />
          <button className={changed ? 'danger' : 'primary'} disabled={sealing} onClick={props.onConfirm}>
            {changed ? props.retrustConfirmLabel : props.confirmLabel}
          </button>
        </div>
      </div>
    </Modal>
  );
}
