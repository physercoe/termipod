/// Stable, synchronous content fingerprint for renderer-side file watching.
/// Two independent 32-bit streams plus length make accidental collisions
/// vanishingly unlikely without duplicating a document body in localStorage.
export function fingerprintBody(body: string): string {
  let a = 0x811c9dc5;
  let b = 0x9e3779b9;
  for (let i = 0; i < body.length; i += 1) {
    const c = body.charCodeAt(i);
    a = Math.imul(a ^ c, 0x01000193);
    b = Math.imul(b ^ (c + i), 0x85ebca6b);
  }
  return `${body.length.toString(36)}:${(a >>> 0).toString(36)}:${(b >>> 0).toString(36)}`;
}
