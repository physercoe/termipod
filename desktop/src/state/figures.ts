/// The figure-renderer registry (docs/plans/figure-renderer-registry.md, Phase A).
///
/// One abstraction for every text-to-figure tool: a `spec → SVG` function keyed
/// by a `FigureSpec` discriminator. Adding a renderer is a row in `FIGURES` — no
/// new editor, no new document kind, no `AuthorSurface` switch arm (that is the
/// whole point of the registry; editor-shaped tools like Excalidraw get their own
/// kind in Phase C, `spec → SVG` tools never do).
///
/// Every renderer is a **lazy `import()`** on first use of its spec (same policy
/// as the lazy TableEditor/MarkdownEditor), so nothing here touches app boot —
/// the heavy libraries (mermaid, graphviz-wasm, vega) are code-split into their
/// own chunks and only fetched when a figure of that spec is first rendered.

export type FigureSpec = 'mermaid' | 'graphviz' | 'vega-lite'; // grows in Phase B

/// A renderer failure surfaced inline by both the editor and the fenced-block
/// renderer — message plus, when the tool reports one, the source line — so a bad
/// spec shows the error and its source, never a blank pane.
export class FigureRenderError extends Error {
  constructor(
    message: string,
    readonly line?: number,
  ) {
    super(message);
    this.name = 'FigureRenderError';
  }
}

export interface FigureRenderer {
  spec: FigureSpec;
  labelKey: string; // i18n key (en + zh)
  ext: string; // on-disk extension for a NEW save (plan decision §1.2)
  openExts?: string[]; // extra extensions recognized on open (graphviz: `.gv`)
  fence: string[]; // fenced-block languages that map to this spec
  sample: string; // seed body for a new doc (human + agent starter)
  /// `.json` disambiguation: true when a `.json` file's content is this spec.
  sniffJson?: (body: string) => boolean;
  /// Lazy-load the library and return the uniform `src → SVG` render function.
  load: () => Promise<(src: string) => Promise<string>>;
}

/// The viewer's current theme, for renderers that support theming (mermaid).
/// Mirrors the app's light/dark resolution: an explicit `data-theme` on the root
/// wins, else the OS preference.
function isDarkTheme(): boolean {
  const attr = document.documentElement.getAttribute('data-theme');
  if (attr === 'dark') return true;
  if (attr === 'light') return false;
  return window.matchMedia('(prefers-color-scheme: dark)').matches;
}

const MERMAID_SAMPLE = `graph TD
  A[Start] --> B{Decision}
  B -->|Yes| C[Proceed]
  B -->|No| D[Revisit]
  C --> E[Done]
  D --> B`;

const GRAPHVIZ_SAMPLE = `digraph G {
  rankdir=LR;
  node [shape=box, style=rounded];
  A -> B -> C;
  A -> C [style=dashed];
}`;

const VEGA_LITE_SAMPLE = JSON.stringify(
  {
    $schema: 'https://vega.github.io/schema/vega-lite/v5.json',
    description: 'A simple bar chart.',
    data: {
      values: [
        { category: 'A', amount: 28 },
        { category: 'B', amount: 55 },
        { category: 'C', amount: 43 },
        { category: 'D', amount: 91 },
      ],
    },
    mark: 'bar',
    encoding: {
      x: { field: 'category', type: 'nominal' },
      y: { field: 'amount', type: 'quantitative' },
    },
  },
  null,
  2,
);

export const FIGURES: FigureRenderer[] = [
  {
    spec: 'mermaid',
    labelKey: 'figure.mermaid',
    ext: 'mmd',
    fence: ['mermaid'],
    sample: MERMAID_SAMPLE,
    load: async () => {
      const mermaid = (await import('mermaid')).default;
      let n = 0;
      return async (src: string) => {
        // Re-init per render so a theme toggle between renders is reflected;
        // `startOnLoad:false` keeps mermaid from scanning the DOM on import.
        mermaid.initialize({ startOnLoad: false, theme: isDarkTheme() ? 'dark' : 'default', securityLevel: 'strict' });
        n += 1;
        // A unique id per render — mermaid stamps it into the SVG and reuses a
        // measurement container keyed on it; a collision would blank the output.
        const id = `tp-mermaid-${Date.now().toString(36)}-${n}`;
        try {
          const { svg } = await mermaid.render(id, src);
          return svg;
        } catch (e) {
          throw new FigureRenderError(e instanceof Error ? e.message : String(e));
        }
      };
    },
  },
  {
    spec: 'graphviz',
    labelKey: 'figure.graphviz',
    ext: 'dot',
    openExts: ['gv'],
    fence: ['dot', 'graphviz'],
    sample: GRAPHVIZ_SAMPLE,
    load: async () => {
      const { Graphviz } = await import('@hpcc-js/wasm-graphviz');
      const gv = await Graphviz.load();
      return async (src: string) => {
        try {
          return gv.dot(src);
        } catch (e) {
          throw new FigureRenderError(e instanceof Error ? e.message : String(e));
        }
      };
    },
  },
  {
    spec: 'vega-lite',
    labelKey: 'figure.vegaLite',
    ext: 'vl.json',
    fence: ['vega-lite', 'vegalite'],
    sample: VEGA_LITE_SAMPLE,
    // A `.vl.json` opens by extension; a bare `.json` is sniffed here — a
    // Vega-Lite spec advertises the vega-lite schema or carries a top-level
    // `mark`. Arbitrary JSON matches neither, so it is never hijacked (§1.2).
    sniffJson: (body) => {
      try {
        const o = JSON.parse(body) as Record<string, unknown>;
        if (o === null || typeof o !== 'object') return false;
        const schema = typeof o.$schema === 'string' ? o.$schema : '';
        if (schema.includes('vega-lite')) return true;
        return 'mark' in o && ('encoding' in o || 'data' in o);
      } catch {
        return false;
      }
    },
    load: async () => {
      const vega = await import('vega');
      const vl = await import('vega-lite');
      return async (src: string) => {
        try {
          const spec = JSON.parse(src) as import('vega-lite').TopLevelSpec;
          const compiled = vl.compile(spec).spec;
          // `renderer:'none'` builds the view without touching the DOM; `toSVG`
          // then serializes it to a standalone SVG string (the uniform shape).
          const view = new vega.View(vega.parse(compiled), { renderer: 'none' });
          return await view.toSVG();
        } catch (e) {
          throw new FigureRenderError(e instanceof Error ? e.message : String(e));
        }
      };
    },
  },
];

/// Registry lookup by spec.
export function figureBySpec(spec: string): FigureRenderer | undefined {
  return FIGURES.find((f) => f.spec === spec);
}

/// Registry lookup by fenced-block language (`mermaid`, `dot`, `vega-lite`, …).
export function figureByFence(lang: string): FigureRenderer | undefined {
  const l = lang.toLowerCase();
  return FIGURES.find((f) => f.fence.includes(l));
}

/// The spec for a file being opened: a registry `ext` match wins; a `.json` is
/// content-sniffed against each row's `sniffJson`. Returns undefined for
/// non-figure files (the caller falls back to its markdown/table logic).
export function specForFile(ext: string, content: string): FigureSpec | undefined {
  const e = ext.toLowerCase();
  // Longest-extension-first so `.vl.json` matches vega-lite before a bare `.json`
  // reaches the sniffers. (The caller passes the last dotted segment for a simple
  // ext, but also the compound tail for multi-dot names — see kindForFile.)
  const byExt = FIGURES.find((f) => f.ext === e || f.openExts?.includes(e) === true || e.endsWith(`.${f.ext}`));
  if (byExt !== undefined) return byExt.spec;
  if (e === 'json') {
    const sniffed = FIGURES.find((f) => f.sniffJson?.(content) === true);
    if (sniffed !== undefined) return sniffed.spec;
  }
  return undefined;
}

/// A lazy-render cache: one resolved `src → SVG` function per spec, so repeated
/// renders (typing in the editor, many fences on a page) don't re-import the
/// library. The first call per spec pays the import; the rest are instant.
const renderers = new Map<FigureSpec, Promise<(src: string) => Promise<string>>>();
export function getRenderer(spec: FigureSpec): Promise<(src: string) => Promise<string>> {
  let r = renderers.get(spec);
  if (r === undefined) {
    const row = figureBySpec(spec);
    if (row === undefined) return Promise.reject(new FigureRenderError(`unknown figure spec: ${spec}`));
    r = row.load();
    renderers.set(spec, r);
    // A failed load (chunk fetch offline, WASM init) must not be cached, or the
    // spec stays broken until app reload — evict so the next render retries.
    r.catch(() => {
      if (renderers.get(spec) === r) renderers.delete(spec);
    });
  }
  return r;
}

/// Render `src` for `spec` to an SVG string, going through the cached renderer.
/// Throws `FigureRenderError` on a bad spec (message + optional source line).
export async function renderFigure(spec: FigureSpec, src: string): Promise<string> {
  const fn = await getRenderer(spec);
  return fn(src);
}
