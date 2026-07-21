/// SigV4 signer validation (ADR-055 M2.5d). The signer is the risk of the S3
/// port, so it is cross-checked against `aws4` — the canonical Node SigV4
/// library, proven against live AWS — as an INDEPENDENT oracle. `aws4` signs the
/// same minimal header set (`host;x-amz-content-sha256;x-amz-date`) for an
/// `s3`-service request with no extra headers, so a matching Signature validates
/// the whole pipeline (canonical request, path/query URI-encoding, string-to-sign,
/// signing-key derivation, HMAC). For the body case we hand `aws4` the
/// pre-computed content-sha256 as a header (no body) so it too signs the minimal
/// set. Run with `node --test`. `aws4` is a test-only devDependency.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import aws4 from 'aws4';
import { signRequest, sha256Hex, uriEncode, utcStamps, civilFromDays, EMPTY_SHA256 } from './sigv4.ts';

const ACCESS = 'AKIDEXAMPLE';
const SECRET = 'wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY';
const REGION = 'us-east-1';
const AMZ = '20260721T131415Z';
const STAMP = '20260721';

const sigOf = (auth: string): string => /Signature=(\w+)/.exec(auth)![1];

function mine(method: string, urlStr: string, payloadHash: string): string {
  const url = new URL(urlStr);
  return sigOf(
    signRequest({
      method,
      url,
      query: url.search.slice(1),
      payloadHash,
      region: REGION,
      access: ACCESS,
      secret: SECRET,
      amzDate: AMZ,
      stamp: STAMP,
    }).authorization,
  );
}

function oracle(method: string, host: string, path: string, contentSha: string): string {
  const opts = aws4.sign(
    { host, path, method, service: 's3', region: REGION, headers: { 'X-Amz-Date': AMZ, 'X-Amz-Content-Sha256': contentSha } },
    { accessKeyId: ACCESS, secretAccessKey: SECRET },
  );
  return sigOf(String((opts.headers as Record<string, unknown>).Authorization));
}

const H = 'https://s3.us-east-1.amazonaws.com';

test('signRequest == aws4: GET object (body-less)', () => {
  assert.equal(mine('GET', `${H}/mybucket/a/b.txt`, EMPTY_SHA256), oracle('GET', 's3.us-east-1.amazonaws.com', '/mybucket/a/b.txt', EMPTY_SHA256));
});

test('signRequest == aws4: GET with percent-encoded (space + unicode) path', () => {
  const p = '/mybucket/my%20notes/caf%C3%A9.txt';
  assert.equal(mine('GET', `${H}${p}`, EMPTY_SHA256), oracle('GET', 's3.us-east-1.amazonaws.com', p, EMPTY_SHA256));
});

test('signRequest == aws4: GET with a sorted ListObjectsV2 query', () => {
  const p = '/mybucket?list-type=2&max-keys=1000&prefix=zotero%2F';
  assert.equal(mine('GET', `${H}${p}`, EMPTY_SHA256), oracle('GET', 's3.us-east-1.amazonaws.com', p, EMPTY_SHA256));
});

test('signRequest == aws4: PUT with a non-empty body hash', () => {
  const hash = sha256Hex(Buffer.from('hello world payload'));
  assert.equal(mine('PUT', `${H}/mybucket/c.bin`, hash), oracle('PUT', 's3.us-east-1.amazonaws.com', '/mybucket/c.bin', hash));
});

test('utcStamps: epoch + a fixed instant (vs Date.UTC oracle)', () => {
  assert.deepEqual(utcStamps(0), ['19700101T000000Z', '19700101']);
  assert.deepEqual(utcStamps(Math.floor(Date.UTC(2026, 6, 21, 13, 14, 15) / 1000)), ['20260721T131415Z', '20260721']);
});

test('civilFromDays: inverse of the epoch-day count (vs Date.UTC)', () => {
  for (const [y, m, d] of [[1970, 1, 1], [2000, 2, 29], [2026, 7, 21]]) {
    const days = Date.UTC(y, m - 1, d) / 86400_000;
    assert.deepEqual(civilFromDays(days), [y, m, d]);
  }
});

test('uriEncode: unreserved literal, space + slash rules', () => {
  assert.equal(uriEncode('a b/c', false), 'a%20b/c'); // slash preserved in a path
  assert.equal(uriEncode('a b/c', true), 'a%20b%2Fc'); // slash encoded in a query value
  assert.equal(uriEncode("-._~AZaz09", false), '-._~AZaz09'); // unreserved untouched
});
