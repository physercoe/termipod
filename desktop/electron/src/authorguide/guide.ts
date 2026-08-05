/// `author_guide {kind, topic?, filter?}` — the lookup half of the co-working
/// surface (coworking C2 + C3, ADR-064 D2).
///
/// The other three `author_*` verbs are about a document that exists. This one
/// is about the FORMAT, and answers with no reference to the user's screen at
/// all: it is the same text for every caller, and it discloses nothing. That
/// property is what makes it worth having — the write verbs refuse a malformed
/// body rather than repairing it, and a refusal is only a fair trade if the
/// rules are readable somewhere cheaper than a failed call.
///
/// **Lazy by topic.** A call returns ONE topic, so the cost of a lookup is
/// bounded by the largest topic and not by everything we know. That is why
/// taking all 30 vendored shape libraries costs nothing on a call that asks
/// about tables — and why `filter` exists for the four large ones, where a
/// whole library really is thousands of tokens.
import { KIND_GUIDES, type GuideKind, type GuideTopic } from './guides.ts';
import { SHAPE_LIBRARIES, SHAPE_LIBRARIES_WITHOUT_A_FILE, SHAPE_LIBRARY_SOURCE, type ShapeLibrary } from './shapes.generated.ts';

export type GuideAnswer = { ok: true; text: string } | { ok: false; code: string; message: string };

/// The shape-library index. A `diagram` topic like any other, so an agent that
/// does not yet know a library exists finds it by asking for this one.
const SHAPES_TOPIC = 'shapes';

export const GUIDE_KINDS: readonly GuideKind[] = KIND_GUIDES.map((g) => g.kind);

function kindGuide(kind: string): (typeof KIND_GUIDES)[number] | undefined {
  return KIND_GUIDES.find((g) => g.kind === kind);
}

function shapeLibrary(name: string): ShapeLibrary | undefined {
  return SHAPE_LIBRARIES.find((l) => l.name === name);
}

/// Every topic name valid for a kind, in the order the index lists them. The
/// shape libraries are appended to `diagram` here rather than in `guides.ts`
/// so that the vendored data and the written prose stay separable — one is
/// regenerated, the other is edited.
export function topicNames(kind: GuideKind): readonly string[] {
  const guide = kindGuide(kind);
  if (guide === undefined) return [];
  const names = guide.topics.map((t) => t.name);
  if (kind !== 'diagram') return names;
  return [...names, SHAPES_TOPIC, ...SHAPE_LIBRARIES.map((l) => l.name)];
}

// ── the index (no topic) ─────────────────────────────────────────────────────

function kindIndex(kind: GuideKind): string {
  const guide = kindGuide(kind);
  if (guide === undefined) return '';
  const lines = [`# ${guide.headline}`, '', guide.overview, '', '## Topics', ''];
  for (const t of guide.topics) lines.push(`- \`${t.name}\` — ${t.summary}`);
  if (kind === 'diagram') {
    lines.push(
      `- \`${SHAPES_TOPIC}\` — the ${String(SHAPE_LIBRARIES.length)} draw.io shape libraries (AWS, Azure, GCP, Cisco, Kubernetes, BPMN, …) and how to name a shape from one.`,
    );
    lines.push('');
    lines.push(
      `Each library is also a topic of its own — \`author_guide {kind:'diagram', topic:'aws4'}\`. Ask for \`${SHAPES_TOPIC}\` first if you do not know which one you want.`,
    );
  }
  lines.push('');
  lines.push(`Call \`author_guide {kind:'${kind}', topic:'<name>'}\` for any of these.`);
  return lines.join('\n');
}

// ── the shape-library index ──────────────────────────────────────────────────

/// One row per vendored library, grouped by upstream's own categories.
///
/// The counts are the honest pair: how many names we can actually give you,
/// and how many draw.io holds. Where they differ the row says so, because an
/// agent that reads a partial list as complete concludes a shape does not
/// exist and invents one instead of saying it could not find it.
function shapesIndex(): string {
  const lines = [
    '# draw.io shape libraries',
    '',
    'A shape from a library is named in the cell `style`, and the form depends on how the library ships:',
    '',
    '```xml',
    '<!-- mxgraph vector shapes -->',
    '<mxCell value="EC2" style="shape=mxgraph.aws4.ec2;verticalLabelPosition=bottom;verticalAlign=top;align=center;" vertex="1" parent="1">',
    '  <mxGeometry x="40" y="40" width="60" height="60" as="geometry" />',
    '</mxCell>',
    '',
    '<!-- bundled SVG images (azure2, sap, atlassian) -->',
    '<mxCell value="VM" style="image;aspect=fixed;image=img/lib/azure2/compute/Virtual_Machine.svg;verticalLabelPosition=bottom;verticalAlign=top;align=center;" vertex="1" parent="1">',
    '  <mxGeometry x="40" y="40" width="60" height="60" as="geometry" />',
    '</mxCell>',
    '```',
    '',
    'Look the name up before you use it. A style naming a shape that does not exist renders as a blank box, and nothing in the apply path can catch that — the XML is perfectly valid.',
    '',
  ];
  const categories = [...new Set(SHAPE_LIBRARIES.map((l) => l.category))];
  for (const category of categories) {
    lines.push(`## ${category}`, '');
    for (const l of SHAPE_LIBRARIES.filter((x) => x.category === category)) {
      const count =
        l.claimed !== null && l.claimed > l.listed
          ? `${String(l.listed)} of ${String(l.claimed)} names`
          : `${String(l.listed)} names`;
      lines.push(`- \`${l.name}\` — ${l.description} · \`${l.prefix}\` · ${count}`);
    }
    lines.push('');
  }
  if (SHAPE_LIBRARIES_WITHOUT_A_FILE.length > 0) {
    lines.push(
      `draw.io also ships ${SHAPE_LIBRARIES_WITHOUT_A_FILE.map((n) => `\`${n}\``).join(', ')}, which we have no name list for — there is no topic for them and guessing a name from one is how you get a blank box.`,
      '',
    );
  }
  lines.push(
    `Ask for one by name: \`author_guide {kind:'diagram', topic:'aws4'}\`. For a large library add \`filter\` — \`{topic:'aws4', filter:'lambda'}\` returns only the matching names.`,
    '',
    `Vendored from ${SHAPE_LIBRARY_SOURCE.repo} @${SHAPE_LIBRARY_SOURCE.commit.slice(0, 7)} (${SHAPE_LIBRARY_SOURCE.license}).`,
  );
  return lines.join('\n');
}

// ── filtering a library ──────────────────────────────────────────────────────

/// A bullet naming a shape — the same shape the generator recognises.
const BULLET = /^- `([^`]+)`/;

/// Narrow a library to the names containing `needle`, keeping each match under
/// the heading it sat below.
///
/// The heading is not decoration: `azure2` and `material_design` put the
/// category in the PATH (`azure2/compute/Virtual_Machine.svg`), so a flat list
/// of matching names would be unusable for exactly the two libraries where a
/// filter is most worth having.
function filterLibrary(lib: ShapeLibrary, needle: string): GuideAnswer {
  const lower = needle.toLowerCase();
  const groups = new Map<string, string[]>();
  let section = '';
  let total = 0;
  let usage = '';
  let inUsage = false;
  for (const line of lib.body.split('\n')) {
    // Carry the library's own Usage block through — it is the sentence that
    // says how to compose the style, and a filtered answer that omits it hands
    // back names the caller cannot use.
    if (line.startsWith('```')) {
      if (inUsage) {
        inUsage = false;
      } else if (usage === '') {
        inUsage = true;
      }
      continue;
    }
    if (inUsage) {
      usage = usage === '' ? line : `${usage}\n${line}`;
      continue;
    }
    if (line.startsWith('#')) {
      section = line.replace(/^#+\s*/, '');
      continue;
    }
    const m = BULLET.exec(line);
    if (m === null) continue;
    total += 1;
    if (!m[1].toLowerCase().includes(lower)) continue;
    const bucket = groups.get(section);
    if (bucket === undefined) groups.set(section, [line]);
    else bucket.push(line);
  }
  const matched = [...groups.values()].reduce((n, g) => n + g.length, 0);
  if (matched === 0) {
    return {
      ok: false,
      code: 'NO_MATCH',
      message: `no shape in '${lib.name}' has '${needle}' in its name (${String(total)} searched) — try a shorter substring, or drop filter for the whole list`,
    };
  }
  const lines = [
    `# ${lib.name} — names containing "${needle}"`,
    '',
    `${String(matched)} of ${String(total)} listed names.`,
    '',
  ];
  if (usage !== '') lines.push('```xml', usage, '```', '');
  for (const [heading, bullets] of groups) {
    // Print the heading unless it is the file's own list header. `## Shapes
    // (1032)` is upstream's way of opening a flat list and says nothing; every
    // other heading is a CATEGORY, and for `azure2` and `material_design` the
    // category is part of the path (`azure2/ai_machine_learning/X.svg`), so
    // dropping it would hand back names that cannot be composed into a style.
    // Suppressing on group COUNT instead would have been wrong precisely when
    // a filter matched inside a single category — the common case.
    if (heading !== '' && !heading.startsWith('Shapes')) lines.push(`## ${heading}`, '');
    lines.push(...bullets, '');
  }
  lines.push(`Drop \`filter\` for the whole library. ${lib.claimed !== null && lib.claimed > lib.listed ? `Only ${String(lib.listed)} of this library's ${String(lib.claimed)} shapes are listed here, so a miss is not proof the shape is absent.` : ''}`.trim());
  return { ok: true, text: lines.join('\n') };
}

// ── the answer ───────────────────────────────────────────────────────────────

/// Resolve one `author_guide` call.
///
/// Every refusal names what WOULD have worked. A guide is the thing an agent
/// reaches for when it is already unsure, so "unknown topic" with no list is
/// the least useful answer available — and the one most likely to end with the
/// agent giving up and guessing at the format instead.
export function guideAnswer(kind: string, topic: string | null, filter: string | null): GuideAnswer {
  const guide = kindGuide(kind);
  if (guide === undefined) {
    return {
      ok: false,
      code: 'UNKNOWN_KIND',
      message: `no guide for kind '${kind}' — kinds are ${GUIDE_KINDS.map((k) => `'${k}'`).join(', ')}`,
    };
  }
  const kindName = guide.kind;
  if (topic === null) {
    if (filter !== null) {
      return { ok: false, code: 'INVALID_PARAMS', message: 'filter needs a topic — it narrows a shape library, and there is nothing to narrow in an index' };
    }
    return { ok: true, text: kindIndex(kindName) };
  }

  const written: GuideTopic | undefined = guide.topics.find((t) => t.name === topic);
  if (written !== undefined) {
    if (filter !== null) {
      return {
        ok: false,
        code: 'INVALID_PARAMS',
        message: `filter applies to a shape-library topic, not to '${topic}' — call it without filter`,
      };
    }
    return { ok: true, text: written.body };
  }

  if (kindName === 'diagram') {
    if (topic === SHAPES_TOPIC) {
      if (filter !== null) {
        return { ok: false, code: 'INVALID_PARAMS', message: `filter narrows one library, not the index — pick a library first, then filter it` };
      }
      return { ok: true, text: shapesIndex() };
    }
    const lib = shapeLibrary(topic);
    if (lib !== undefined) return filter !== null ? filterLibrary(lib, filter) : { ok: true, text: lib.body };
    if (SHAPE_LIBRARIES_WITHOUT_A_FILE.includes(topic)) {
      return {
        ok: false,
        code: 'UNKNOWN_TOPIC',
        message: `draw.io ships a '${topic}' library but we have no name list for it — use topic '${SHAPES_TOPIC}' to see the ${String(SHAPE_LIBRARIES.length)} that are covered`,
      };
    }
  }

  return {
    ok: false,
    code: 'UNKNOWN_TOPIC',
    message: `no '${kindName}' topic named '${topic}' — topics are ${topicNames(kindName)
      .map((n) => `'${n}'`)
      .join(', ')}`,
  };
}
