/// Unified-diff / git-patch splitting for the Inspect (J3) diff viewer (W2).
///
/// A `.patch`/`.diff` file (or a pasted patch) is one text blob that may hold
/// many files. `@git-diff-view/react`'s `DiffView` renders **one** file at a
/// time (its `data.hunks` is a single file's diff block — verified: the block
/// must keep its `diff --git`/`---`/`+++`/`@@` headers for the addition/deletion
/// counts to compute), so the surface splits the patch here and renders one
/// `<DiffView>` per file. Pure + framework-free so it is unit-testable and the
/// viewer stays a thin view.

export type PatchStatus = 'modify' | 'add' | 'delete' | 'rename' | 'binary';

export interface PatchFile {
  /// The pre-image path (`/dev/null` for an added file), prefixes stripped.
  oldPath: string;
  /// The post-image path (`/dev/null` for a deleted file), prefixes stripped.
  newPath: string;
  /// The path to show in the file header — the new path, or the old path when
  /// the file was deleted.
  path: string;
  status: PatchStatus;
  additions: number;
  deletions: number;
  /// The full per-file diff block (headers + hunks) — passed verbatim as the
  /// single element of `DiffView`'s `data.hunks`.
  diff: string;
  /// A coarse language id for syntax highlighting, from the file extension.
  lang?: string;
}

// Strip a `a/`, `b/`, `i/`, `w/`, `c/`, `o/` prefix and a trailing tab-timestamp
// (the `--- path\tYYYY-MM-DD …` form some tools emit), and quotes.
function cleanPath(raw: string): string {
  let p = raw.trim();
  const tab = p.indexOf('\t');
  if (tab >= 0) p = p.slice(0, tab);
  if (p.startsWith('"') && p.endsWith('"') && p.length >= 2) p = p.slice(1, -1);
  if (/^[abciwo]\//.test(p)) p = p.slice(2);
  return p;
}

function extLang(path: string): string | undefined {
  const b = path.slice(Math.max(path.lastIndexOf('/'), path.lastIndexOf('\\')) + 1);
  const i = b.lastIndexOf('.');
  if (i < 0) return undefined;
  const e = b.slice(i + 1).toLowerCase();
  const map: Record<string, string> = {
    py: 'python', sh: 'bash', bash: 'bash', zsh: 'bash', js: 'javascript', mjs: 'javascript', cjs: 'javascript', jsx: 'javascript',
    ts: 'typescript', tsx: 'tsx', go: 'go', rs: 'rust', java: 'java', rb: 'ruby', php: 'php',
    json: 'json', yaml: 'yaml', yml: 'yaml', md: 'markdown', c: 'c', h: 'c', cpp: 'cpp', cc: 'cpp', hpp: 'cpp',
    cs: 'csharp', css: 'css', scss: 'scss', html: 'html', xml: 'xml', sql: 'sql', toml: 'toml', kt: 'kotlin', swift: 'swift',
  };
  return map[e];
}

// Split the whole patch into per-file blocks. Prefers git's `diff --git`
// boundaries; falls back to `Index:` (svn/cvs) and then to bare `--- /+++`
// unified-diff pairs.
function sectionize(text: string): string[] {
  const lines = text.split('\n');
  const isGit = lines.some((l) => l.startsWith('diff --git '));
  const isIndex = !isGit && lines.some((l) => l.startsWith('Index: '));
  const starts: number[] = [];
  for (let i = 0; i < lines.length; i++) {
    const l = lines[i];
    if (isGit) {
      if (l.startsWith('diff --git ')) starts.push(i);
    } else if (isIndex) {
      if (l.startsWith('Index: ')) starts.push(i);
    } else {
      // A bare unified diff: a file begins at a `--- ` line whose successor is a
      // `+++ ` line (so `---`/`+++` inside prose or hunks don't false-trigger).
      if (l.startsWith('--- ') && i + 1 < lines.length && lines[i + 1].startsWith('+++ ')) starts.push(i);
    }
  }
  if (starts.length === 0) return text.trim() === '' ? [] : [text];
  const blocks: string[] = [];
  // Keep any preamble before the first section attached to nothing (dropped).
  for (let s = 0; s < starts.length; s++) {
    const from = starts[s];
    const to = s + 1 < starts.length ? starts[s + 1] : lines.length;
    blocks.push(lines.slice(from, to).join('\n'));
  }
  return blocks;
}

function parseSection(block: string): PatchFile {
  const lines = block.split('\n');
  let oldPath = '';
  let newPath = '';
  let status: PatchStatus = 'modify';
  let additions = 0;
  let deletions = 0;
  let inHunk = false;

  for (const l of lines) {
    if (l.startsWith('diff --git ')) {
      const m = /^diff --git (.+?) (.+)$/.exec(l);
      if (m !== null) {
        oldPath = cleanPath(m[1]);
        newPath = cleanPath(m[2]);
      }
      continue;
    }
    if (l.startsWith('new file mode')) status = 'add';
    else if (l.startsWith('deleted file mode')) status = 'delete';
    else if (l.startsWith('rename from ')) {
      status = 'rename';
      oldPath = cleanPath(l.slice('rename from '.length));
    } else if (l.startsWith('rename to ')) {
      status = 'rename';
      newPath = cleanPath(l.slice('rename to '.length));
    } else if (l.startsWith('Binary files ') || l.startsWith('GIT binary patch')) {
      status = 'binary';
    } else if (l.startsWith('Index: ')) {
      if (newPath === '') newPath = cleanPath(l.slice('Index: '.length));
    } else if (l.startsWith('--- ')) {
      const p = l.slice(4);
      if (p.trim() === '/dev/null') status = status === 'binary' ? status : 'add';
      else oldPath = cleanPath(p);
    } else if (l.startsWith('+++ ')) {
      const p = l.slice(4);
      if (p.trim() === '/dev/null') status = status === 'binary' ? status : 'delete';
      else newPath = cleanPath(p);
    } else if (l.startsWith('@@')) {
      inHunk = true;
    } else if (inHunk) {
      if (l.startsWith('+')) additions++;
      else if (l.startsWith('-')) deletions++;
    }
  }

  const path = status === 'delete' ? oldPath || newPath : newPath || oldPath;
  return { oldPath, newPath, path, status, additions, deletions, diff: block, lang: extLang(path) };
}

/// Split a multi-file patch into its per-file diffs. Returns `[]` for an empty
/// or non-diff input.
export function splitPatch(text: string): PatchFile[] {
  return sectionize(text).map(parseSection);
}

/// A quick "does this text look like a unified diff / patch?" check — used to
/// decide whether an otherwise-`code` tab should offer the diff renderer.
export function looksLikePatch(text: string): boolean {
  const head = text.slice(0, 4096);
  return /^(diff --git |Index: |--- \S)/m.test(head) && /^@@ /m.test(head);
}
