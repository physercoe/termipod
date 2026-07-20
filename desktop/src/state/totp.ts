/// RFC 6238 TOTP for vault login items (#320) — the `totp` secret slot has been
/// in the data model (and syncs from mobile) but had no desktop UI. Computed in
/// the webview via WebCrypto HMAC-SHA-1, so no new Rust command is needed and
/// the seed — like every vault secret — is read from the keychain on demand
/// only. 6 digits, 30s step: the parameters every authenticator app defaults to.

const STEP_S = 30;
const DIGITS = 6;
const B32_ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';

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

/// Extract the seed from a stored slot value: a bare base32 seed, or an
/// otpauth://totp/…?secret=… URI (what authenticator QR exports carry).
export function parseSeed(stored: string): Uint8Array<ArrayBuffer> | null {
  const s = stored.trim();
  if (s === '') return null;
  if (s.toLowerCase().startsWith('otpauth://')) {
    try {
      const secret = new URL(s).searchParams.get('secret');
      return secret === null ? null : decodeBase32(secret);
    } catch {
      return null;
    }
  }
  return decodeBase32(s);
}

/** Seconds until the current code rolls over (drives the countdown ring). */
export function secondsRemaining(nowMs = Date.now()): number {
  return STEP_S - (Math.floor(nowMs / 1000) % STEP_S);
}

/** The current 6-digit RFC 6238 code for a decoded seed. */
export async function totpCode(seed: Uint8Array<ArrayBuffer>, nowMs = Date.now()): Promise<string> {
  const counter = Math.floor(nowMs / 1000 / STEP_S);
  const msg = new Uint8Array(8);
  // 64-bit big-endian counter; the high word stays 0 until well past 2106.
  new DataView(msg.buffer).setUint32(4, counter, false);
  const key = await crypto.subtle.importKey('raw', seed, { name: 'HMAC', hash: 'SHA-1' }, false, ['sign']);
  const sig = new Uint8Array(await crypto.subtle.sign('HMAC', key, msg));
  const offset = sig[sig.length - 1] & 0x0f;
  const bin =
    ((sig[offset] & 0x7f) << 24) | (sig[offset + 1] << 16) | (sig[offset + 2] << 8) | sig[offset + 3];
  return String(bin % 10 ** DIGITS).padStart(DIGITS, '0');
}
