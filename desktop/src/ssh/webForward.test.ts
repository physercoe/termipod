import assert from 'node:assert/strict';
import test from 'node:test';
import { forwardedWebUrl, normalizeWebPath, parseRemotePort } from './webForward.ts';

test('parseRemotePort accepts only whole TCP ports in range', () => {
  assert.equal(parseRemotePort('8080'), 8080);
  assert.equal(parseRemotePort(' 443 '), 443);
  for (const value of ['', '0', '65536', '1.5', '80abc', '-1']) {
    assert.equal(parseRemotePort(value), null, value);
  }
});

test('normalizeWebPath keeps the browser inside the forwarded origin', () => {
  assert.equal(normalizeWebPath(''), '/');
  assert.equal(normalizeWebPath('ui'), '/ui');
  assert.equal(normalizeWebPath('/ui?mode=full#top'), '/ui?mode=full#top');
});

test('forwardedWebUrl always uses the local loopback listener', () => {
  assert.equal(forwardedWebUrl('http', 49152, ''), 'http://127.0.0.1:49152/');
  assert.equal(forwardedWebUrl('https', 49153, 'admin'), 'https://127.0.0.1:49153/admin');
});
