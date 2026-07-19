/// Password generation + strength estimate for the vault (#320). Deliberately
/// dependency-free: a small character-class generator with guaranteed coverage,
/// and a log2-entropy-based strength score (the zxcvbn-lite idea — bits of
/// entropy from the pool size and length, not a dictionary check).

const LOWER = 'abcdefghijkmnpqrstuvwxyz'; // no l/o — ambiguous
const UPPER = 'ABCDEFGHJKLMNPQRSTUVWXYZ'; // no I/O
const DIGITS = '23456789'; // no 0/1
const SYMBOLS = '!@#$%^&*()-_=+[]{};:,.?';

export interface GenOpts {
  length: number;
  symbols: boolean;
  digits: boolean;
  upper: boolean;
}

export const DEFAULT_GEN: GenOpts = { length: 20, symbols: true, digits: true, upper: true };

/// Generate a random password with at least one char from each enabled class.
/// Uses crypto.getRandomValues (available under the tauri:// scheme and in the
/// browser build) so the output isn't predictable.
export function generatePassword(opts: GenOpts): string {
  const classes: string[] = [LOWER];
  if (opts.upper) classes.push(UPPER);
  if (opts.digits) classes.push(DIGITS);
  if (opts.symbols) classes.push(SYMBOLS);
  const pool = classes.join('');
  const len = Math.max(8, Math.min(128, Math.floor(opts.length)));

  const rand = (n: number): number => {
    const buf = new Uint32Array(1);
    crypto.getRandomValues(buf);
    return buf[0] % n;
  };

  const out: string[] = [];
  // Guarantee one char from each enabled class...
  for (const cls of classes) out.push(cls[rand(cls.length)]);
  // ...then fill the rest from the full pool.
  while (out.length < len) out.push(pool[rand(pool.length)]);
  // Fisher-Yates shuffle so the guaranteed chars aren't always at the front.
  for (let i = out.length - 1; i > 0; i -= 1) {
    const j = rand(i + 1);
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out.join('');
}

export type StrengthLabel = 'weak' | 'fair' | 'good' | 'strong';

export interface Strength {
  bits: number;
  label: StrengthLabel;
  /** 0-1, for a meter fill. */
  fraction: number;
}

/// Rough entropy estimate: bits = length × log2(pool size the password draws on).
/// Not a dictionary check — a fast, offline signal for the meter.
export function passwordStrength(pw: string): Strength {
  if (pw === '') return { bits: 0, label: 'weak', fraction: 0 };
  let pool = 0;
  if (/[a-z]/.test(pw)) pool += 26;
  if (/[A-Z]/.test(pw)) pool += 26;
  if (/[0-9]/.test(pw)) pool += 10;
  if (/[^a-zA-Z0-9]/.test(pw)) pool += 32;
  const bits = pw.length * Math.log2(Math.max(1, pool));
  const label: StrengthLabel = bits < 40 ? 'weak' : bits < 60 ? 'fair' : bits < 90 ? 'good' : 'strong';
  return { bits: Math.round(bits), label, fraction: Math.min(1, bits / 120) };
}
