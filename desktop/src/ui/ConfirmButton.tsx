import { useEffect, useState } from 'react';
import { useT } from '../i18n';

interface Props {
  label: string;
  onConfirm: () => void;
  disabled?: boolean;
  danger?: boolean;
}

/// A destructive-action button that requires a second click to fire (plan §5 /
/// WS7 "destructive-action confirmations"). Reverts after 3s if not confirmed.
export function ConfirmButton({ label, onConfirm, disabled, danger }: Props): JSX.Element {
  const t = useT();
  const [armed, setArmed] = useState(false);

  useEffect(() => {
    if (!armed) return;
    const t = setTimeout(() => setArmed(false), 3000);
    return () => clearTimeout(t);
  }, [armed]);

  return (
    <button
      className={danger === true ? 'danger' : undefined}
      disabled={disabled}
      onClick={() => {
        if (armed) {
          setArmed(false);
          onConfirm();
        } else {
          setArmed(true);
        }
      }}
    >
      {armed ? t('confirm.confirm') : label}
    </button>
  );
}
