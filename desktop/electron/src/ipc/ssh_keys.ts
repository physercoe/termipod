/// SSH key store (ADR-055 M2.2b) — `ssh_parse_key` + `ssh_generate_key`, the
/// Electron port of ssh.rs's key crypto (the #320 key store). Same return shapes
/// (`{ algorithm, public_openssh, fingerprint }`, plus `pem` for generate), so
/// `src/state/keys.ts` drives them unchanged.
///
/// Engine: `sshpk` — it parses private keys (encrypted, with passphrase, and
/// RSA/ed25519 alike), generates ed25519, and exports the OpenSSH private-key PEM
/// with passphrase encryption. Verified locally before adopting it:
///   • `generatePrivateKey('ed25519').toString('openssh', { passphrase })`
///     produces an ENCRYPTED key that `ssh2.utils.parseKey` (the connect flow)
///     rejects without the passphrase and parses with it — so a generated key is
///     byte-compatible with M2.2a's transport.
///   • sshpk's `SHA256:` fingerprint equals OpenSSH's
///     (`base64(sha256(publicSSH))`, no padding) — matching ssh.rs's output.
///   • a wrong passphrase throws (mapped to the same "key parse" error).
///
/// `sshpk` is pure JS (no mandatory native), kept external like the other
/// engine modules.
import type { Handler } from './dispatch';

// sshpk is a CommonJS `export =` module; dynamic import wraps it under `default`.
type SshpkModule = typeof import('sshpk');
let sshpkP: Promise<SshpkModule> | null = null;
function loadSshpk(): Promise<SshpkModule> {
  if (sshpkP === null) sshpkP = import('sshpk').then((m) => m.default);
  return sshpkP;
}

interface KeyInfo {
  algorithm: string;
  public_openssh: string;
  fingerprint: string;
}

export const sshKeyHandlers: Record<string, Handler> = {
  // Validate + introspect a pasted private key (with its passphrase if
  // encrypted): return its algorithm, OpenSSH public key, and SHA-256
  // fingerprint. A bad key / wrong passphrase throws with the parse error.
  ssh_parse_key: async (args): Promise<KeyInfo> => {
    const pem = String(args.pem ?? '');
    const passphrase = typeof args.passphrase === 'string' && args.passphrase !== '' ? args.passphrase : undefined;
    const sshpk = await loadSshpk();
    let key;
    try {
      key = sshpk.parsePrivateKey(pem, 'auto', passphrase !== undefined ? { passphrase } : undefined);
    } catch (e) {
      throw new Error(`key parse: ${(e as Error).message}`);
    }
    return {
      algorithm: key.type,
      public_openssh: key.toPublic().toString('ssh'),
      fingerprint: key.fingerprint('sha256').toString(),
    };
  },

  // Generate an ed25519 keypair in-app (matching ssh.rs's fixed Ed25519). The
  // private PEM is passphrase-encrypted when one is given, so the connect flow's
  // parser handles it unchanged; the public key + fingerprint mirror parse.
  ssh_generate_key: async (args): Promise<KeyInfo & { pem: string }> => {
    const passphrase = typeof args.passphrase === 'string' && args.passphrase !== '' ? args.passphrase : undefined;
    const sshpk = await loadSshpk();
    const key = sshpk.generatePrivateKey('ed25519');
    const pem = passphrase !== undefined ? key.toString('openssh', { passphrase }) : key.toString('openssh');
    return {
      algorithm: key.type,
      public_openssh: key.toPublic().toString('ssh'),
      fingerprint: key.fingerprint('sha256').toString(),
      pem,
    };
  },
};
