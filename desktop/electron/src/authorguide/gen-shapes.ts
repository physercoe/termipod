// Regenerate `shapes.generated.ts` from the vendored files in `shapes/`.
//
//   node src/authorguide/gen-shapes.ts
//
// `shapes/` is a BYTE-EXACT copy of next-ai-draw-io's `docs/shape-libraries/`
// at commit 6e65394 (Apache-2.0 — see the repo NOTICE). Nothing in it is
// hand-edited, so a reviewer can diff the directory against upstream and
// expect an empty result. Every correction we make lives HERE instead, where
// it is one readable rule rather than 30 silent edits, and `shapes.test.ts`
// fails when the generated module drifts from what these rules produce.
//
// Three corrections, each because upstream's data is wrong in a way an agent
// would act on:
//
//   1. **The self-referential bullet.** 18 files list their own prefix as if
//      it were a shape (`- \`mxgraph.aws4\``), which composes to
//      `shape=mxgraph.aws4.mxgraph.aws4` and renders as a blank box. Dropped,
//      and the removal is stated in the footer rather than done silently.
//   2. **The index is generated, not copied.** Upstream's README names three
//      libraries that have no file (`arista`, `digitalocean`, `eip`) and omits
//      one that does (`material_design`). Serving it verbatim would send an
//      agent to three topics that 404 and hide a 300-icon library from it. So
//      the index is built from the files that exist, keeping upstream's
//      category, prefix and description for every row it does have.
//   3. **A stated count of what was actually listed.** Upstream's per-library
//      counts describe the real draw.io library, and several files enumerate
//      only part of it (`azure2` lists 14 of the 149 shapes in its `other`
//      category; `electrical`, `mscae`, `pid` and `rack` are explicitly
//      partial). An agent reading "648 shapes" over a list of 354 concludes a
//      missing name does not exist. The footer states both numbers and says
//      outright when the list is partial.
//
// Upstream's own claimed counts are never rewritten — they are the size of the
// library in draw.io, which we have no way to re-measure and no reason to
// doubt. Only OUR count of what this file enumerates is computed here.
import { readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const SHAPES_DIR = join(HERE, 'shapes');

/// The exact upstream state this directory was taken from. A reviewer checking
/// provenance needs the commit, not just the repo — upstream's README has
/// already drifted from its own files once.
export const SOURCE = {
  repo: 'https://github.com/DayuanJiang/next-ai-draw-io',
  commit: '6e653942b0a9912123093179c6f2db2cf7c1d42f',
  path: 'docs/shape-libraries/',
  license: 'Apache-2.0',
};

/// `material_design.md` exists upstream but appears in no README row, so there
/// is no vendored index entry to carry over. This is the only hand-authored
/// row in the index; everything else is upstream's own words.
///
/// `prefix` here is only a FALLBACK — the file's own `**URL Pattern:**` line
/// wins, and does for this library. It is kept because a future orphan might
/// declare nothing, and an index row with no way to compose a style is a row
/// that cannot be acted on. `claimed: null` because nobody ever stated this
/// library's real size, and inventing one would be the exact failure the
/// generated counts exist to avoid.
const ORPHAN_ROWS: Record<string, IndexRow | undefined> = {
  material_design: {
    category: 'UI/Mockups',
    prefix: 'https://fonts.gstatic.com/s/i/materialicons/',
    description: 'Google Material icons, by remote URL — action, navigation, content, social, etc.',
    claimed: null,
  },
};

/// One row of the served index: where this library sits, what a style pastes,
/// and how many shapes draw.io holds (`null` when upstream never said).
interface IndexRow {
  category: string;
  prefix: string;
  description: string;
  claimed: number | null;
}

/// What one vendored file yields: the served body plus the facts computed from
/// it. `removed` is the self-referential bullets dropped — kept as a list so
/// the footer can name what went rather than just admitting a number changed.
interface ParsedLibrary {
  prefix: string;
  listed: number;
  removed: string[];
  body: string;
}

/// One bullet naming a shape. Upstream writes a few with a trailing note
/// (`- \`shadedCube\` (needs \`isoAngle=15;\`)`), so the name is the first
/// backticked token and the rest of the line is kept as written.
const BULLET = /^- `([^`]+)`/;

function parseLibrary(text: string): ParsedLibrary {
  const lines = text.split('\n');
  // The declared prefix, which is also what a self-referential bullet says.
  // `**Path:**` (bundled SVGs) and `**URL Pattern:**` (material_design) are
  // the same field under two other names — an agent needs whichever one this
  // library pastes into a style.
  const prefix =
    /^\*\*Prefix:\*\* `([^`]+)`/m.exec(text)?.[1] ??
    /^\*\*Path:\*\* `([^`]+)`/m.exec(text)?.[1] ??
    /^\*\*URL Pattern:\*\* `([^`]+)`/m.exec(text)?.[1] ??
    '';
  // A `## Shapes` section is what makes a bullet a shape LIST rather than
  // prose. `pid.md` documents sibling prefixes as bullets under "Other
  // Prefixes"; without this check those would read as artifacts and vanish.
  const hasShapeList = /^## Shapes/m.test(text);

  const kept: string[] = [];
  const removed: string[] = [];
  for (const line of lines) {
    const m = BULLET.exec(line);
    if (m !== null && hasShapeList && prefix !== '' && m[1] === prefix) {
      removed.push(m[1]);
      continue;
    }
    kept.push(line);
  }
  const listed = kept.filter((l) => BULLET.test(l)).length;
  return { prefix, listed, removed, body: kept.join('\n') };
}

/// Upstream's README is a set of category tables. Parsed rather than copied,
/// because we keep the rows whose file exists and drop the rows that lie.
function parseIndex(readme: string): Map<string, IndexRow> {
  const rows = new Map<string, IndexRow>();
  let category = '';
  for (const line of readme.split('\n')) {
    const head = /^## (.+)$/.exec(line);
    if (head !== null) {
      category = head[1].trim();
      continue;
    }
    // `| lib | 1031 | `mxgraph.aws4` | Description | [lib.md](./lib.md) |`
    const cells = /^\| *([a-z0-9_]+) *\| *([0-9]+) *\| *`([^`]*)` *\| *([^|]*)\|/.exec(line);
    if (cells === null) continue;
    rows.set(cells[1], {
      category,
      claimed: Number(cells[2]),
      prefix: cells[3].trim(),
      description: cells[4].trim(),
    });
  }
  return rows;
}

/// The footer appended to every served library. Says where the text came from,
/// what we removed, and — the load-bearing sentence — whether the list is the
/// whole library. An agent that believes a partial list is complete stops
/// looking for a shape that exists.
function footer(name: string, lib: ParsedLibrary, claimed: number | null): string {
  const parts = [`Vendored from next-ai-draw-io \`${SOURCE.path}${name}.md\` @${SOURCE.commit.slice(0, 7)} (${SOURCE.license}).`];
  parts.push(`${String(lib.listed)} shape ${lib.listed === 1 ? 'name is' : 'names are'} listed above.`);
  if (lib.removed.length > 0) {
    parts.push(
      `${String(lib.removed.length)} entry naming the library's own prefix (\`${lib.removed[0]}\`) was removed — it is a scraping artifact that would compose to \`${lib.removed[0]}.${lib.removed[0]}\`.`,
    );
  }
  if (claimed !== null && claimed > lib.listed) {
    parts.push(
      `draw.io's own library holds ${String(claimed)}, so THIS LIST IS PARTIAL: a name missing here may still exist. Say so rather than inventing one.`,
    );
  }
  return `\n\n---\n\n${parts.join(' ')}\n`;
}

const ts = (v: unknown): string => JSON.stringify(v);

/// Build the module text. Exported rather than executed at import so
/// `shapes.test.ts` can assert the checked-in file matches without writing
/// anything — a generator that can only write is a generator whose output
/// nobody can check.
export function generate(): string {
  const files = readdirSync(SHAPES_DIR)
    .filter((f) => f.endsWith('.md'))
    .sort();
  const indexRows = parseIndex(readFileSync(join(SHAPES_DIR, 'README.md'), 'utf8'));

  const libraries: (IndexRow & { name: string; listed: number; body: string })[] = [];
  for (const file of files) {
    if (file === 'README.md') continue;
    const name = file.slice(0, -3);
    const lib = parseLibrary(readFileSync(join(SHAPES_DIR, file), 'utf8'));
    const row = indexRows.get(name) ?? ORPHAN_ROWS[name];
    if (row === undefined) throw new Error(`${file}: no index row and no hand-authored fallback — add one to ORPHAN_ROWS`);
    const claimed = row.claimed ?? null;
    libraries.push({
      name,
      category: row.category,
      prefix: lib.prefix !== '' ? lib.prefix : row.prefix,
      description: row.description,
      listed: lib.listed,
      claimed,
      body: lib.body.replace(/\s+$/, '') + footer(name, lib, claimed),
    });
  }

  // A README row whose file we do not have would send an agent to a topic that
  // does not resolve. Recorded so the index can say the library exists in
  // draw.io without offering a topic for it.
  const missing = [...indexRows.keys()].filter((n) => !libraries.some((l) => l.name === n)).sort();

  return `/// GENERATED by \`gen-shapes.mjs\` from \`shapes/\` — do not edit.
///
/// Run \`node src/authorguide/gen-shapes.mjs\` after changing anything under
/// \`shapes/\`; \`shapes.test.ts\` fails when this file is stale. The rules that
/// turn the vendored markdown into what we serve are documented in the
/// generator, not here.
///
/// Vendored from ${SOURCE.repo}
/// @ ${SOURCE.commit} (${SOURCE.license}) — see the repo NOTICE.

/// One draw.io shape library, as \`author_guide {kind:'diagram', topic}\`
/// serves it.
export interface ShapeLibrary {
  /// The topic name — also the upstream filename stem.
  name: string;
  /// Upstream's grouping ("Cloud Providers", "Networking & Infrastructure"…).
  category: string;
  /// What a style pastes: an mxgraph prefix, a bundled-image path, or a URL.
  prefix: string;
  /// Upstream's one-line description, verbatim.
  description: string;
  /// How many shape names this body actually enumerates. Ours, computed.
  listed: number;
  /// How many the library holds in draw.io, per upstream's index. \`null\` when
  /// upstream never indexed it. Never recomputed — we cannot measure it.
  claimed: number | null;
  /// The served markdown: upstream's file, minus the self-referential bullet,
  /// plus a provenance footer.
  body: string;
}

export const SHAPE_LIBRARY_SOURCE = ${ts(SOURCE)} as const;

/// Libraries upstream's index names but ships no file for. Kept so the index
/// can admit they exist in draw.io without offering a topic that cannot
/// resolve — the alternative is an agent asking for a topic we then refuse.
export const SHAPE_LIBRARIES_WITHOUT_A_FILE: readonly string[] = ${ts(missing)};

export const SHAPE_LIBRARIES: readonly ShapeLibrary[] = [
${libraries
  .map(
    (l) => `  {
    name: ${ts(l.name)},
    category: ${ts(l.category)},
    prefix: ${ts(l.prefix)},
    description: ${ts(l.description)},
    listed: ${String(l.listed)},
    claimed: ${l.claimed === null ? 'null' : String(l.claimed)},
    body: ${ts(l.body)},
  },`,
  )
  .join('\n')}
];
`;
}

export const GENERATED_PATH = join(HERE, 'shapes.generated.ts');

// Only writes when run as a script; importing it is side-effect free.
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const out = generate();
  writeFileSync(GENERATED_PATH, out);
  console.log(`shapes.generated.ts: ${String(out.length)} bytes`);
}
