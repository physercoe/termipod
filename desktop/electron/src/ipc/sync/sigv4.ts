/// AWS Signature V4 signing (ADR-055 M2.5d) — the pure, testable heart of the S3
/// backend, ported from `s3.rs`'s hand-rolled SigV4 (`send_signed` + primitives).
/// No HTTP: given a request's parts it returns the `Authorization` header value.
/// This is the RISK of the S3 port, so it is isolated here and validated in
/// `sigv4.test.ts` against `aws4` (the canonical Node SigV4 library, proven
/// against live AWS) as an INDEPENDENT oracle — not a doc hex reproduced from
/// memory, and not a second in-test reimplementation (which would share any
/// spec-misreading; see the equivalence-test blind spot).
///
/// Minimal signed header set: `host;x-amz-content-sha256;x-amz-date` — path-style
/// addressing, no extra headers — which is exactly what `aws4` signs for an
/// `s3`-service request with no additional headers, so the two are comparable.
import { createHash, createHmac } from 'node:crypto';

/// sha256("") — the payload hash for a body-less request.
export const EMPTY_SHA256 = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855';

export function sha256Hex(b: Uint8Array): string {
  return createHash('sha256').update(b).digest('hex');
}

function hmac(key: Uint8Array, data: string): Buffer {
  return createHmac('sha256', key).update(data, 'utf8').digest();
}

/// RFC-3986 percent-encoding as AWS canonicalisation requires: only the unreserved
/// set is literal; `/` is preserved in a path (`encodeSlash=false`) but encoded in
/// a query value. Mirrors s3.rs `uri_encode`.
export function uriEncode(s: string, encodeSlash: boolean): string {
  let out = '';
  for (const byte of Buffer.from(s, 'utf8')) {
    const c = String.fromCharCode(byte);
    if (
      (byte >= 0x41 && byte <= 0x5a) || // A-Z
      (byte >= 0x61 && byte <= 0x7a) || // a-z
      (byte >= 0x30 && byte <= 0x39) || // 0-9
      c === '-' || c === '.' || c === '_' || c === '~'
    ) {
      out += c;
    } else if (c === '/' && !encodeSlash) {
      out += '/';
    } else {
      out += `%${byte.toString(16).toUpperCase().padStart(2, '0')}`;
    }
  }
  return out;
}

/// Inverse of `days_from_civil` (Howard Hinnant): epoch days → [year, month, day],
/// for formatting the `x-amz-date` header. Mirrors s3.rs `civil_from_days`.
export function civilFromDays(z0: number): [number, number, number] {
  const z = z0 + 719468;
  const era = Math.floor((z >= 0 ? z : z - 146096) / 146097);
  const doe = z - era * 146097;
  const yoe = Math.floor((doe - Math.floor(doe / 1460) + Math.floor(doe / 36524) - Math.floor(doe / 146096)) / 365);
  const y = yoe + era * 400;
  const doy = doe - (365 * yoe + Math.floor(yoe / 4) - Math.floor(yoe / 100));
  const mp = Math.floor((5 * doy + 2) / 153);
  const d = doy - Math.floor((153 * mp + 2) / 5) + 1;
  const m = mp < 10 ? mp + 3 : mp - 9;
  return [m <= 2 ? y + 1 : y, m, d];
}

const p2 = (n: number): string => String(n).padStart(2, '0');
const p4 = (n: number): string => String(n).padStart(4, '0');

/// Format a Unix-seconds instant as [`amz` (`YYYYMMDDTHHMMSSZ`), `stamp`
/// (`YYYYMMDD`)]. Mirrors s3.rs `now_utc` (parameterised on the instant so it is
/// testable). Callers pass `Date.now()/1000` in production.
export function utcStamps(secs: number): [string, string] {
  const days = Math.floor(secs / 86400);
  const [y, m, d] = civilFromDays(days);
  const tod = secs - days * 86400;
  const hh = Math.floor(tod / 3600);
  const mm = Math.floor((tod % 3600) / 60);
  const ss = tod % 60;
  return [`${p4(y)}${p2(m)}${p2(d)}T${p2(hh)}${p2(mm)}${p2(ss)}Z`, `${p4(y)}${p2(m)}${p2(d)}`];
}

export interface SignInput {
  method: string;
  /// Full object/list URL. `pathname` is the canonical URI; `host` (incl. port)
  /// goes in the signed `host` header.
  url: URL;
  /// The exact canonical query string already on the URL (params sorted, each
  /// URI-encoded); empty for object PUT/GET.
  query: string;
  /// sha256 hex of the body, or EMPTY_SHA256 for a body-less request.
  payloadHash: string;
  region: string;
  access: string;
  secret: string;
  amzDate: string; // YYYYMMDDTHHMMSSZ
  stamp: string; // YYYYMMDD
}

/// Compute the SigV4 `Authorization` header value (+ echoes the amz date and
/// payload hash the caller must also send). Mirrors s3.rs `send_signed`'s signing.
export function signRequest(inp: SignInput): { authorization: string; amzDate: string; payloadHash: string } {
  const host = inp.url.host; // host[:port]
  const canonicalUri = inp.url.pathname;
  const signedHeaders = 'host;x-amz-content-sha256;x-amz-date';
  const canonicalHeaders = `host:${host}\nx-amz-content-sha256:${inp.payloadHash}\nx-amz-date:${inp.amzDate}\n`;
  const canonicalRequest = [inp.method, canonicalUri, inp.query, canonicalHeaders, signedHeaders, inp.payloadHash].join('\n');
  const scope = `${inp.stamp}/${inp.region}/s3/aws4_request`;
  const stringToSign = `AWS4-HMAC-SHA256\n${inp.amzDate}\n${scope}\n${sha256Hex(Buffer.from(canonicalRequest, 'utf8'))}`;
  const kDate = hmac(Buffer.from(`AWS4${inp.secret}`, 'utf8'), inp.stamp);
  const kRegion = hmac(kDate, inp.region);
  const kService = hmac(kRegion, 's3');
  const kSigning = hmac(kService, 'aws4_request');
  const signature = hmac(kSigning, stringToSign).toString('hex');
  const authorization = `AWS4-HMAC-SHA256 Credential=${inp.access}/${scope}, SignedHeaders=${signedHeaders}, Signature=${signature}`;
  return { authorization, amzDate: inp.amzDate, payloadHash: inp.payloadHash };
}
