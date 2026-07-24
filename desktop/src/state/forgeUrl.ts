/// Pure forge-reference parsing for the Inspect forge sources (round-3 T3),
/// split from `forge.ts` (which pulls in the bridge/vault) so the parser is
/// unit-testable in isolation. No imports — just string logic.

export type Forge = 'github' | 'hf';

export interface ParsedForge {
  forge: Forge;
  id: string;
  ref?: string;
  subpath?: string;
}

/// Parse a repo reference into `{ forge, id, ref?, subpath? }`, or null. Accepts
/// `https://github.com/{o}/{r}`, `…/tree/{ref}[/{sub}]`, `…/blob/{ref}/{path}`,
/// the `huggingface.co` / `hf.co` equivalents, and the bare shorthand
/// `owner/repo[@ref]` (whose forge is ambiguous — `hint` disambiguates it, as the
/// add-root dialog's forge selector does).
export function parseForgeUrl(input: string, hint: Forge = 'github'): ParsedForge | null {
  const s = input.trim();
  if (s === '') return null;

  const hostMatch = s.match(/^(?:https?:\/\/)?(github\.com|huggingface\.co|hf\.co)\/(.+)$/i);
  if (hostMatch !== null) {
    const forge: Forge = hostMatch[1].toLowerCase().startsWith('github') ? 'github' : 'hf';
    const rest = hostMatch[2].replace(/[?#].*$/, '').replace(/\/+$/, '');
    const parts = rest.split('/').filter((p) => p !== '');
    const markerIdx = parts.findIndex((p) => p === 'tree' || p === 'blob');
    const idParts = markerIdx >= 0 ? parts.slice(0, markerIdx) : parts;
    const ref = markerIdx >= 0 ? parts[markerIdx + 1] : undefined;
    const subpath = markerIdx >= 0 ? parts.slice(markerIdx + 2).join('/') || undefined : undefined;
    if (forge === 'github') {
      if (idParts.length < 2) return null;
      return { forge, id: `${idParts[0]}/${stripGit(idParts[1])}`, ref, subpath };
    }
    if (idParts.length < 1) return null;
    return { forge, id: idParts.slice(0, 2).join('/'), ref, subpath };
  }

  const shorthand = s.match(/^([^/@\s]+\/[^/@\s]+)(?:@(\S+))?$/);
  if (shorthand !== null) return { forge: hint, id: stripGit(shorthand[1]), ref: shorthand[2] };
  return null;
}

export function stripGit(s: string): string {
  return s.replace(/\.git$/, '');
}

export function encForgePath(p: string): string {
  return p
    .split('/')
    .map((seg) => encodeURIComponent(seg))
    .join('/');
}
