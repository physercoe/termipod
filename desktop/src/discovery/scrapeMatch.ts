export interface WorkIdentity {
  title?: string;
  year?: number;
  authors?: string[];
}

function words(value: string | undefined): string[] {
  return (value ?? '')
    .normalize('NFKD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLowerCase()
    .replace(/&/g, ' and ')
    .replace(/[^\p{L}\p{N}]+/gu, ' ')
    .trim()
    .split(/\s+/)
    .filter(Boolean);
}

function familyName(author: string | undefined): string | undefined {
  const tokens = words(author);
  return tokens.at(-1);
}

/// OpenAlex title search is fuzzy and returns a best guess even when it is the
/// wrong paper. Identifier lookups are trusted; this guard is for the title-only
/// fallback and intentionally prefers "not found" over attaching another work's
/// citation graph to the selected library item.
export function isLikelySameWork(seed: WorkIdentity, candidate: WorkIdentity): boolean {
  const wanted = words(seed.title);
  const found = words(candidate.title);
  if (wanted.length === 0 || found.length === 0) return false;
  if (seed.year !== undefined && candidate.year !== undefined && Math.abs(seed.year - candidate.year) > 1) return false;

  const wantedSet = new Set(wanted);
  const foundSet = new Set(found);
  let overlap = 0;
  for (const token of wantedSet) if (foundSet.has(token)) overlap += 1;
  const coverage = overlap / Math.max(wantedSet.size, foundSet.size);
  const titleMatches = wanted.join(' ') === found.join(' ') || (Math.max(wanted.length, found.length) >= 4 && coverage >= 0.85);
  if (!titleMatches) return false;

  const wantedAuthor = familyName(seed.authors?.[0]);
  const foundAuthor = familyName(candidate.authors?.[0]);
  return wantedAuthor === undefined || foundAuthor === undefined || wantedAuthor === foundAuthor;
}
