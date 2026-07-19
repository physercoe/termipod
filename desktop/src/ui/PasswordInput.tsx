import { useState, type InputHTMLAttributes } from 'react';
import { Icon } from './Icon';
import { useT } from '../i18n';

/// A password `<input>` with an inline reveal/hide eye toggle (#320). Every
/// secret-entry field should let the user verify what they typed — a bare
/// `type="password"` with no reveal is a common source of "why is my passphrase
/// wrong" support pain. This is the lightweight sibling of VaultManager's
/// `PasswordField` (which additionally carries a generator + strength meter for
/// NEW passwords); use this wherever you just need entry + reveal.
///
/// The input's own props pass straight through (value, onChange, placeholder,
/// autoFocus, …). `wrapClassName` styles the wrapper for flex contexts; the
/// input keeps `className` so existing per-field sizing still applies.
export function PasswordInput({
  wrapClassName,
  className,
  ...rest
}: { wrapClassName?: string } & InputHTMLAttributes<HTMLInputElement>): JSX.Element {
  const t = useT();
  const [show, setShow] = useState(false);
  return (
    <span className={wrapClassName !== undefined ? `pw-field ${wrapClassName}` : 'pw-field'}>
      <input {...rest} className={className} type={show ? 'text' : 'password'} />
      <button
        type="button"
        className="pw-field-eye icon-btn"
        title={show ? t('vault.hide') : t('vault.reveal')}
        aria-label={show ? t('vault.hide') : t('vault.reveal')}
        aria-pressed={show}
        onClick={() => setShow((v) => !v)}
      >
        <Icon name={show ? 'eye-off' : 'eye'} size={15} />
      </button>
    </span>
  );
}
