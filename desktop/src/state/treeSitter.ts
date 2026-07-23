import type { Language, Node, Parser as ParserT } from 'web-tree-sitter';

/// Tree-sitter symbol extraction for the Inspect (J3) code outline (round-2 plan
/// §1/§2). web-tree-sitter's core + the per-language grammar WASMs are served as
/// static assets (`public/tree-sitter/…`, copied by `scripts/sync-treesitter-
/// assets.mjs`) and loaded on demand — so nothing tree-sitter lands in the boot
/// bundle, and a grammar is fetched only the first time a file of that language
/// is inspected.
///
/// web-tree-sitter is pulled via a **dynamic import** (never a top-level one) so
/// importing this module's constants (`SUPPORTED_LANGS`) stays cheap for the
/// caller's gating check — the ~heavy parser glue loads only when `extractSymbols`
/// actually runs.
///
/// Queries below are validated against the real grammars (a Node probe compiled
/// each + extracted from a sample) — an invalid query throws at construction, so
/// they must stay in lockstep with the grammar ABI the sync script ships.

export type SymbolKind = 'function' | 'class' | 'method' | 'type';

export interface CodeSymbol {
  name: string;
  kind: SymbolKind;
  /// 1-based source line — the `revealLine` target.
  line: number;
}

// A tab's coarse language id → the grammar that parses it. `c`/`c++` share the
// cpp grammar (it parses C); `jsx` rides the javascript grammar.
const GRAMMAR_FOR: Record<string, string> = {
  python: 'python',
  javascript: 'javascript',
  jsx: 'javascript',
  typescript: 'typescript',
  tsx: 'tsx',
  go: 'go',
  rust: 'rust',
  java: 'java',
  ruby: 'ruby',
  bash: 'bash',
  c: 'cpp',
  'c++': 'cpp',
  cpp: 'cpp',
  'c-sharp': 'c-sharp',
  csharp: 'c-sharp',
  php: 'php',
};

/// The languages the outline can parse — the surface gates on this before it
/// touches the parser (so an unsupported language never loads the WASM glue).
export const SUPPORTED_LANGS = new Set(Object.keys(GRAMMAR_FOR));

// grammar id → the capture query that pulls out functions / classes / methods /
// types. Capture name = the symbol kind.
const QUERY: Record<string, string> = {
  python: `
    (function_definition name: (identifier) @func)
    (class_definition name: (identifier) @class)`,
  javascript: `
    (function_declaration name: (identifier) @func)
    (generator_function_declaration name: (identifier) @func)
    (class_declaration name: (identifier) @class)
    (method_definition name: (property_identifier) @method)
    (variable_declarator name: (identifier) @func value: [(arrow_function) (function_expression)])`,
  typescript: `
    (function_declaration name: (identifier) @func)
    (class_declaration name: (type_identifier) @class)
    (abstract_class_declaration name: (type_identifier) @class)
    (method_definition name: (property_identifier) @method)
    (interface_declaration name: (type_identifier) @type)
    (type_alias_declaration name: (type_identifier) @type)
    (enum_declaration name: (identifier) @type)
    (variable_declarator name: (identifier) @func value: [(arrow_function) (function_expression)])`,
  tsx: `
    (function_declaration name: (identifier) @func)
    (class_declaration name: (type_identifier) @class)
    (method_definition name: (property_identifier) @method)
    (interface_declaration name: (type_identifier) @type)
    (type_alias_declaration name: (type_identifier) @type)
    (variable_declarator name: (identifier) @func value: [(arrow_function) (function_expression)])`,
  go: `
    (function_declaration name: (identifier) @func)
    (method_declaration name: (field_identifier) @method)
    (type_declaration (type_spec name: (type_identifier) @type))`,
  rust: `
    (function_item name: (identifier) @func)
    (struct_item name: (type_identifier) @type)
    (enum_item name: (type_identifier) @type)
    (trait_item name: (type_identifier) @type)
    (mod_item name: (identifier) @type)`,
  java: `
    (class_declaration name: (identifier) @class)
    (interface_declaration name: (identifier) @type)
    (enum_declaration name: (identifier) @type)
    (method_declaration name: (identifier) @method)
    (constructor_declaration name: (identifier) @method)`,
  ruby: `
    (method name: (identifier) @method)
    (singleton_method name: (identifier) @method)
    (class name: (constant) @class)
    (module name: (constant) @type)`,
  bash: `(function_definition name: (word) @func)`,
  cpp: `
    (function_definition declarator: (function_declarator declarator: (identifier) @func))
    (class_specifier name: (type_identifier) @class)
    (struct_specifier name: (type_identifier) @type)`,
  'c-sharp': `
    (class_declaration name: (identifier) @class)
    (interface_declaration name: (identifier) @type)
    (struct_declaration name: (identifier) @type)
    (method_declaration name: (identifier) @method)`,
  php: `
    (function_definition name: (name) @func)
    (method_declaration name: (name) @method)
    (class_declaration name: (name) @class)
    (interface_declaration name: (name) @type)`,
};

const KIND_FOR: Record<string, SymbolKind> = { func: 'function', class: 'class', method: 'method', type: 'type' };

// Parsing above this size is skipped — a giant minified file is neither a useful
// outline nor worth the parse cost.
const MAX_PARSE_BYTES = 2 * 1024 * 1024;

// Lazily-initialised singletons (the parser core loads once; grammars cache per
// id). Held as promises so concurrent callers share one in-flight load.
let parserMod: Promise<typeof import('web-tree-sitter')> | null = null;
let ready: Promise<ParserT> | null = null;
const grammars = new Map<string, Promise<Language>>();

async function getParser(): Promise<ParserT> {
  if (parserMod === null) parserMod = import('web-tree-sitter');
  const { Parser } = await parserMod;
  if (ready === null) {
    ready = Parser.init({ locateFile: (name: string) => `/tree-sitter/${name}` }).then(() => new Parser());
  }
  return ready;
}

async function getGrammar(grammar: string): Promise<Language> {
  let p = grammars.get(grammar);
  if (p === undefined) {
    p = (async () => {
      const { Language } = await (parserMod ?? (parserMod = import('web-tree-sitter')));
      return Language.load(`/tree-sitter/grammars/tree-sitter-${grammar}.wasm`);
    })();
    grammars.set(grammar, p);
  }
  try {
    return await p;
  } catch (e) {
    grammars.delete(grammar);
    throw e;
  }
}

/// Extract the outline symbols for `source` in `langId`. Returns `[]` for an
/// unsupported language, an over-size file, or any parse/load failure — the
/// outline degrades to hidden, never throws into the render.
export async function extractSymbols(langId: string | undefined, source: string): Promise<CodeSymbol[]> {
  if (langId === undefined) return [];
  const grammar = GRAMMAR_FOR[langId];
  if (grammar === undefined || source.length > MAX_PARSE_BYTES) return [];
  try {
    const [parser, { Query }] = await Promise.all([getParser(), parserMod ?? (parserMod = import('web-tree-sitter'))]);
    const lang = await getGrammar(grammar);
    parser.setLanguage(lang);
    const tree = parser.parse(source);
    if (tree === null) return [];
    const query = new Query(lang, QUERY[grammar]);
    const out: CodeSymbol[] = [];
    const seen = new Set<string>();
    for (const cap of query.captures(tree.rootNode)) {
      const kind = KIND_FOR[cap.name];
      const node = cap.node as Node;
      const name = node.text;
      if (kind === undefined || name === '') continue;
      const line = node.startPosition.row + 1;
      const key = `${line}:${name}`;
      if (seen.has(key)) continue;
      seen.add(key);
      out.push({ name, kind, line });
    }
    tree.delete();
    out.sort((a, b) => a.line - b.line);
    return out;
  } catch {
    return [];
  }
}
