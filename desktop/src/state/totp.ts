/// RFC 6238 TOTP for vault login items (#320) — the `totp` secret slot has been
/// in the data model (and syncs from mobile) but had no desktop UI. Computed in
/// the webview via WebCrypto HMAC (SHA-1/256/512), so no new Rust command is
/// needed and the seed — like every vault secret — is read from the keychain on
/// demand only. Defaults follow every authenticator app: 6 digits, 30s step.

const STEP_S = 30;
const DIGITS = 6;
const B32_ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';

/// Everything needed to mint codes: the decoded seed plus the otpauth params.
/// Non-default algorithm/digits/period are honored — silently minting codes
/// with the wrong parameters (some banks/AWS issue SHA-256/8-digit/60s URIs)
/// is worse than flagging the seed invalid (#320 review).
export interface TotpParams {
  seed: Uint8Array<ArrayBuffer>;
  algorithm: 'SHA-1' | 'SHA-256' | 'SHA-512';
  digits: number;
  period: number;
}

/// Base32 (RFC 4648) → bytes, tolerating what users actually paste: lower-case,
/// spaces, and '=' padding. Returns null on any non-alphabet character so the
/// UI can flag an invalid seed instead of minting wrong codes.
export function decodeBase32(input: string): Uint8Array<ArrayBuffer> | null {
  const clean = input.replace(/[\s=]/g, '').toUpperCase();
  if (clean === '') return null;
  const out: number[] = [];
  let bits = 0;
  let value = 0;
  for (const ch of clean) {
    const idx = B32_ALPHABET.indexOf(ch);
    if (idx < 0) return null;
    value = (value << 5) | idx;
    bits += 5;
    if (bits >= 8) {
      out.push((value >>> (bits - 8)) & 0xff);
      bits -= 8;
    }
  }
  return new Uint8Array(out);
}

const ALGORITHMS: Record<string, TotpParams['algorithm']> = {
  SHA1: 'SHA-1',
  'SHA-1': 'SHA-1',
  SHA256: 'SHA-256',
  'SHA-256': 'SHA-256',
  SHA512: 'SHA-512',
  'SHA-512': 'SHA-512',
};

/// otpauth:// query params beyond the secret, with the RFC 6238 defaults.
/// Returns null on anything we can't honor (unknown algorithm, digits outside
/// 6–8, a non-positive period) so the UI shows the invalid-seed state.
function parseParams(q: URLSearchParams): Pick<TotpParams, 'algorithm' | 'digits' | 'period'> | null {
  const algorithm = ALGORITHMS[(q.get('algorithm') ?? 'SHA1').toUpperCase()];
  if (algorithm === undefined) return null;
  const digitsRaw = q.get('digits');
  const digits = digitsRaw === null ? DIGITS : Number(digitsRaw);
  if (!Number.isInteger(digits) || digits < 6 || digits > 8) return null;
  const periodRaw = q.get('period');
  const period = periodRaw === null ? STEP_S : Number(periodRaw);
  if (!Number.isInteger(period) || period <= 0) return null;
  return { algorithm, digits, period };
}

/// Parse a stored slot value into TOTP parameters: a bare base32 seed (all
/// defaults), or an otpauth://totp/… URI (what authenticator QR exports carry).
export function parseSeed(stored: string): TotpParams | null {
  const s = stored.trim();
  if (s === '') return null;
  if (s.toLowerCase().startsWith('otpauth://')) {
    try {
      const url = new URL(s);
      const secret = url.searchParams.get('secret');
      if (secret === null) return null;
      const seed = decodeBase32(secret);
      if (seed === null) return null;
      const params = parseParams(url.searchParams);
      return params === null ? null : { seed, ...params };
    } catch {
      return null;
    }
  }
  const seed = decodeBase32(s);
  return seed === null ? null : { seed, algorithm: 'SHA-1', digits: DIGITS, period: STEP_S };
}

/** Seconds until the current code rolls over (drives the countdown ring). */
export function secondsRemaining(period: number, nowMs = Date.now()): number {
  return period - (Math.floor(nowMs / 1000) % period);
}

/** The current RFC 6238 code for the given parameters. */
export async function totpCode(params: TotpParams, nowMs = Date.now()): Promise<string> {
  const counter = Math.floor(nowMs / 1000 / params.period);
  const msg = new Uint8Array(8);
  // 64-bit big-endian counter; the high word stays 0 until well past 2106.
  new DataView(msg.buffer).setUint32(4, counter, false);
  const key = await crypto.subtle.importKey(
    'raw',
    params.seed,
    { name: 'HMAC', hash: params.algorithm },
    false,
    ['sign'],
  );
  const sig = new Uint8Array(await crypto.subtle.sign('HMAC', key, msg));
  const offset = sig[sig.length - 1] & 0x0f;
  const bin =
    ((sig[offset] & 0x7f) << 24) | (sig[offset + 1] << 16) | (sig[offset + 2] << 8) | sig[offset + 3];
  return String(bin % 10 ** params.digits).padStart(params.digits, '0');
}
