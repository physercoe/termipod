/// Renderer-side checkpoint model + architecture classifier for the Inspect (J3)
/// model viewer (plan §5, W4 core). The main process (`ipc/checkpoint.ts`) does
/// the header parse and returns `CheckpointInfo`; this module mirrors that shape
/// (the app has no shared types across the electron/renderer boundary) and adds
/// the pure presentation logic: humanized counts, and the **architecture card**
/// classifier that turns an HF `config.json` (or gguf metadata, or — as a last
/// resort — the tensor names) into a family + block-template + component chips.
///
/// Honest labelling (plan §5): the card is the *recipe by name*, not traced
/// truth — a custom `forward()` or patched attention is invisible to it. The
/// `provenance` field says where each reading came from.

export interface TensorInfo {
  name: string;
  dtype: string;
  shape: number[];
  params: number;
}

/// One operator node of an ONNX graph — mirrors `electron/src/ipc/checkpoint.ts`
/// (metadata only: op type + input/output tensor *names* that wire the edges).
export interface OnnxGraphNode {
  name: string;
  opType: string;
  inputs: string[];
  outputs: string[];
}

/// The ONNX operator graph (nodes + external interface), for the "View as graph"
/// render. Mirror of the main-process shape.
export interface OnnxGraphData {
  nodes: OnnxGraphNode[];
  inputs: string[];
  outputs: string[];
  truncatedNodes?: number;
}

export interface CheckpointInfo {
  format: 'safetensors' | 'gguf' | 'onnx';
  path: string;
  fileSize: number;
  totalParams: number;
  tensorCount: number;
  tensors: TensorInfo[];
  metadata: Record<string, string | number>;
  dtypeHistogram: Record<string, number>;
  /// ONNX only: op_type -> node count (the graph's operator mix).
  ops?: Record<string, number>;
  /// ONNX only: the operator graph, for the "View as graph" render.
  graph?: OnnxGraphData;
  truncatedTensors?: number;
}

export function humanCount(n: number): string {
  if (n >= 1e12) return `${(n / 1e12).toFixed(2)}T`;
  if (n >= 1e9) return `${(n / 1e9).toFixed(2)}B`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`;
  return String(n);
}

export function humanBytes(n: number): string {
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${i === 0 ? v : v.toFixed(1)} ${u[i]}`;
}

export type ArchTemplate = 'dense-gqa' | 'moe' | 'mla' | 'mla-moe' | 'unknown';

export interface ArchCard {
  family: string;
  template: ArchTemplate;
  layers?: number;
  hidden?: number;
  heads?: number;
  kvHeads?: number;
  vocab?: number;
  context?: number;
  experts?: number;
  expertsPerTok?: number;
  sharedExperts?: number;
  chips: string[];
  /// Where the readings came from — the provenance badge's value.
  provenance: 'config' | 'gguf' | 'tensors';
}

function num(obj: Record<string, unknown>, ...keys: string[]): number | undefined {
  for (const k of keys) {
    const v = obj[k];
    if (typeof v === 'number' && Number.isFinite(v)) return v;
  }
  return undefined;
}

// model_type / architecture id -> display family name.
const FAMILY: Record<string, string> = {
  llama: 'Llama',
  mistral: 'Mistral',
  mixtral: 'Mixtral',
  qwen2: 'Qwen2',
  qwen2_moe: 'Qwen2-MoE',
  qwen3: 'Qwen3',
  qwen3_moe: 'Qwen3-MoE',
  deepseek: 'DeepSeek',
  deepseek_v2: 'DeepSeek-V2',
  deepseek_v3: 'DeepSeek-V3',
  kimi_k2: 'Kimi K2',
  gemma: 'Gemma',
  gemma2: 'Gemma 2',
  gemma3: 'Gemma 3',
  phi3: 'Phi-3',
  starcoder2: 'StarCoder2',
  cohere: 'Command-R',
};

function familyName(id: string): string {
  const k = id.toLowerCase();
  if (FAMILY[k]) return FAMILY[k];
  // Title-case an unknown id (`gptbigcode` -> `Gptbigcode`).
  return id ? id.charAt(0).toUpperCase() + id.slice(1) : 'Unknown';
}

// SwiGLU/GeGLU + RoPE + RMSNorm are near-universal in these decoder families;
// only claim them for a recognised family (else omit — recipe by name, honestly).
const KNOWN_DECODER = new Set(Object.keys(FAMILY));

function commonChips(id: string, out: string[]): void {
  const k = id.toLowerCase();
  if (!KNOWN_DECODER.has(k)) return;
  out.push('RoPE', 'RMSNorm');
  out.push(k.startsWith('gemma') ? 'GeGLU' : 'SwiGLU');
}

/// Analytic parameter count from an HF `config.json` alone (round-3 §5a) — for
/// the weightless case where no checkpoint/tensor data is available. Covers
/// **dense + GQA + MoE** decoder transformers (SwiGLU/GeGLU gated FFN, the shape
/// these families share). Returns **null** rather than a wrong number when the
/// architecture is one this closed form doesn't model faithfully — **MLA**
/// (DeepSeek-V2/V3: the low-rank q/kv projections need fields this omits) — or
/// when a load-bearing field is missing. A returned count is an *estimate*
/// (biases, exact norm/router details, and per-layer dense/MoE mixes are
/// approximated) and must be badged as such.
export function estimateParamsFromConfig(config: Record<string, unknown>): number | null {
  // MLA is not modelled here — its param count depends on kv_lora_rank /
  // q_lora_rank / qk_nope/rope head dims in ways a dense formula gets wrong.
  if (num(config, 'kv_lora_rank') !== undefined) return null;

  const hidden = num(config, 'hidden_size', 'n_embd');
  const layers = num(config, 'num_hidden_layers', 'n_layer');
  const heads = num(config, 'num_attention_heads');
  const vocab = num(config, 'vocab_size');
  const inter = num(config, 'intermediate_size', 'n_inner', 'ffn_dim');
  if (hidden === undefined || layers === undefined || heads === undefined || vocab === undefined || inter === undefined) return null;

  const kvHeads = num(config, 'num_key_value_heads') ?? heads;
  const headDim = num(config, 'head_dim') ?? hidden / heads;

  // Attention: q_proj + o_proj are hidden↔(heads·headDim); k_proj + v_proj are
  // hidden↔(kvHeads·headDim) (GQA shrinks the KV side).
  const attn = 2 * hidden * heads * headDim + 2 * hidden * kvHeads * headDim;

  // FFN: gated (gate + up + down) = 3 · hidden · intermediate. MoE replaces the
  // dense FFN with num_experts expert FFNs (at moe_intermediate_size) + a router,
  // plus any shared experts.
  const experts = num(config, 'num_local_experts', 'n_routed_experts', 'num_experts');
  let ffn: number;
  if (experts !== undefined && experts > 0) {
    const expInter = num(config, 'moe_intermediate_size') ?? inter;
    const shared = num(config, 'n_shared_experts', 'num_shared_experts') ?? 0;
    const sharedInter = num(config, 'shared_expert_intermediate_size') ?? expInter;
    ffn = experts * 3 * hidden * expInter + hidden * experts + shared * 3 * hidden * sharedInter;
  } else {
    ffn = 3 * hidden * inter;
  }

  const norms = 2 * hidden; // two RMSNorms per block (input + post-attention)
  const perLayer = attn + ffn + norms;

  const embed = vocab * hidden;
  const tied = config.tie_word_embeddings !== false; // HF default is tied
  const lmHead = tied ? 0 : vocab * hidden;

  return embed + lmHead + layers * perLayer + hidden; // + final norm
}

/// Whether some text is a transformers `config.json` — parses to an object
/// carrying `model_type` or `architectures` (round-3 §5a). Used to gate the
/// "View architecture" flip on a JSON code tab from any source. Deliberately
/// strict (not "any JSON") since `config.json` is a generic name; no auto-hijack.
export function parseHfConfig(text: string | undefined): Record<string, unknown> | null {
  if (text === undefined || text.trim() === '') return null;
  try {
    const o: unknown = JSON.parse(text);
    if (o !== null && typeof o === 'object' && !Array.isArray(o)) {
      const rec = o as Record<string, unknown>;
      if (typeof rec.model_type === 'string' || Array.isArray(rec.architectures)) return rec;
    }
  } catch {
    /* not JSON */
  }
  return null;
}

/// Build the architecture card. `config` is a parsed HF `config.json` (safetensors
/// sidecar); `metadata` is gguf KV; `tensorNames` corroborates or, absent a
/// config, drives the classification. Returns null when there is nothing to say.
export function classifyArch(opts: {
  config?: Record<string, unknown> | null;
  metadata?: Record<string, string | number>;
  tensorNames: string[];
}): ArchCard | null {
  const names = opts.tensorNames;
  const hasMla = names.some((n) => /(?:^|\.)(?:kv_a_proj|q_a_proj|kv_a_layernorm|q_a_layernorm)/.test(n));
  const hasExperts = names.some((n) => /\.experts?\.\d|\.mlp\.experts\.|\bexps\b|_exps\./.test(n) || /\.\d+\.ffn_gate_exps/.test(n));

  // ── config.json path (safetensors) ──────────────────────────────────────────
  const cfg = opts.config;
  if (cfg) {
    const archs = Array.isArray(cfg.architectures) ? (cfg.architectures as unknown[]) : [];
    const id = (typeof cfg.model_type === 'string' && cfg.model_type) || (typeof archs[0] === 'string' ? (archs[0] as string) : '');
    const experts = num(cfg, 'num_local_experts', 'n_routed_experts', 'num_experts');
    const mlaCfg = num(cfg, 'kv_lora_rank', 'q_lora_rank') !== undefined || hasMla;
    const moe = (experts !== undefined && experts > 0) || hasExperts;
    const heads = num(cfg, 'num_attention_heads');
    const kvHeads = num(cfg, 'num_key_value_heads');
    const chips: string[] = [];
    if (mlaCfg) chips.push('MLA');
    else if (kvHeads !== undefined && heads !== undefined && kvHeads < heads) chips.push('GQA');
    if (moe) chips.push('MoE');
    const shared = num(cfg, 'n_shared_experts', 'num_shared_experts');
    if (shared !== undefined && shared > 0) chips.push('shared-experts');
    commonChips(id, chips);
    return {
      family: familyName(id),
      template: mlaCfg && moe ? 'mla-moe' : mlaCfg ? 'mla' : moe ? 'moe' : 'dense-gqa',
      layers: num(cfg, 'num_hidden_layers', 'n_layer'),
      hidden: num(cfg, 'hidden_size', 'n_embd'),
      heads,
      kvHeads,
      vocab: num(cfg, 'vocab_size'),
      context: num(cfg, 'max_position_embeddings', 'n_positions'),
      experts: experts !== undefined && experts > 0 ? experts : undefined,
      expertsPerTok: num(cfg, 'num_experts_per_tok', 'moe_topk'),
      sharedExperts: shared,
      chips,
      provenance: 'config',
    };
  }

  // ── gguf metadata path ──────────────────────────────────────────────────────
  // Only true gguf metadata carries `general.architecture`; safetensors
  // `__metadata__` and ONNX producer stats do not, so an empty arch falls
  // through to the tensor-name inference below rather than emitting a bogus
  // "Unknown / Dense decoder" card.
  const md = opts.metadata;
  const arch0 = md && typeof md['general.architecture'] === 'string' ? (md['general.architecture'] as string) : '';
  if (md && arch0) {
    const arch = arch0;
    const g = (suffix: string): number | undefined => {
      const v = md[`${arch}.${suffix}`];
      return typeof v === 'number' ? v : undefined;
    };
    const experts = g('expert_count');
    const moe = (experts !== undefined && experts > 0) || hasExperts;
    const heads = g('attention.head_count');
    const kvHeads = g('attention.head_count_kv');
    const chips: string[] = [];
    if (hasMla) chips.push('MLA');
    else if (kvHeads !== undefined && heads !== undefined && kvHeads < heads) chips.push('GQA');
    if (moe) chips.push('MoE');
    commonChips(arch, chips);
    return {
      family: familyName(arch),
      template: hasMla && moe ? 'mla-moe' : hasMla ? 'mla' : moe ? 'moe' : 'dense-gqa',
      layers: g('block_count'),
      hidden: g('embedding_length'),
      heads,
      kvHeads,
      vocab: g('vocab_size'),
      context: g('context_length'),
      experts: experts !== undefined && experts > 0 ? experts : undefined,
      expertsPerTok: g('expert_used_count'),
      chips,
      provenance: 'gguf',
    };
  }

  // ── tensor-name-only fallback ───────────────────────────────────────────────
  if (hasMla || hasExperts) {
    const chips: string[] = [];
    if (hasMla) chips.push('MLA');
    if (hasExperts) chips.push('MoE');
    return {
      family: 'Unknown',
      template: hasMla && hasExperts ? 'mla-moe' : hasMla ? 'mla' : 'moe',
      chips,
      provenance: 'tensors',
    };
  }
  return null;
}

export const TEMPLATE_LABEL: Record<ArchTemplate, string> = {
  'dense-gqa': 'Dense decoder (GQA)',
  moe: 'Mixture-of-Experts',
  mla: 'MLA decoder',
  'mla-moe': 'MLA + MoE',
  unknown: 'Unknown',
};

// ── namespace tree (tensor names split on '.') ─────────────────────────────────
export interface TreeNode {
  key: string; // this segment
  path: string; // full dotted path to here
  params: number;
  children: TreeNode[];
  leaf?: TensorInfo;
  /// Set on a synthetic node that stands in for `count` structurally-identical
  /// numeric-indexed siblings (e.g. the 61 decoder layers). Its `params` is the
  /// AGGREGATE across all members; its children are one member's per-member
  /// structure (so the usual "parent = sum(children)" rollup does NOT hold here —
  /// the `× count` badge is what reconciles them). See [[collapseRepeats]].
  repeat?: { count: number; from: number; to: number };
}

/// Build a collapsible namespace tree from tensor names, rolling up param counts
/// into every ancestor. Children are sorted numeric-aware (`layers.2` < `layers.10`).
export function buildTree(tensors: TensorInfo[]): TreeNode {
  const root: TreeNode = { key: '', path: '', params: 0, children: [] };
  const index = new Map<string, TreeNode>([['', root]]);
  for (const t of tensors) {
    const segs = t.name.split('.');
    let parent = root;
    let prefix = '';
    root.params += t.params;
    for (let i = 0; i < segs.length; i += 1) {
      prefix = prefix === '' ? segs[i] : `${prefix}.${segs[i]}`;
      let node = index.get(prefix);
      if (node === undefined) {
        node = { key: segs[i], path: prefix, params: 0, children: [] };
        index.set(prefix, node);
        parent.children.push(node);
      }
      node.params += t.params;
      if (i === segs.length - 1) node.leaf = t;
      parent = node;
    }
  }
  const cmp = (a: TreeNode, b: TreeNode): number => a.key.localeCompare(b.key, undefined, { numeric: true });
  const sortRec = (n: TreeNode): void => {
    n.children.sort(cmp);
    n.children.forEach(sortRec);
  };
  sortRec(root);
  return root;
}

const NUMERIC = /^\d+$/;

/// A canonical string of a subtree's *shape* — its descendant key structure and
/// leaf dtype/shape, with numeric keys normalised to `#` so that two decoder
/// layers (whose expert indices differ) still match. Two nodes with the same
/// signature are structurally identical and safe to collapse.
function structureSignature(n: TreeNode): string {
  if (n.leaf) return `L:${n.leaf.dtype}:${n.leaf.shape.join('x')}`;
  const kids = n.children.map((c) => `${NUMERIC.test(c.key) ? '#' : c.key}(${structureSignature(c)})`).sort();
  return `{${kids.join(',')}}`;
}

/// Collapse runs of structurally-identical numeric-indexed siblings into a single
/// synthetic `× N` node (plan §4b) — so a 61-layer model shows `layers → [0–60]
/// ×61` instead of 61 near-identical subtrees. Recurses first, so nested repeats
/// (MoE `experts.0…N` inside a layer) collapse too. Groups by structural
/// signature, so a heterogeneous stack (e.g. a few dense layers then MoE layers)
/// splits into separate groups rather than force-merging. Pure — returns a new
/// tree; the caller keeps the raw tree for the "expand all" view.
export function collapseRepeats(node: TreeNode, minRun = 3): TreeNode {
  const processed = node.children.map((c) => collapseRepeats(c, minRun));
  const others = processed.filter((c) => !NUMERIC.test(c.key));
  const numeric = processed.filter((c) => NUMERIC.test(c.key));

  const bySig = new Map<string, TreeNode[]>();
  for (const c of numeric) {
    const sig = structureSignature(c);
    const arr = bySig.get(sig);
    if (arr) arr.push(c);
    else bySig.set(sig, [c]);
  }

  const newChildren: TreeNode[] = [...others];
  for (const group of bySig.values()) {
    if (group.length >= minRun) {
      const sorted = [...group].sort((a, b) => Number(a.key) - Number(b.key));
      const from = Number(sorted[0].key);
      const to = Number(sorted[sorted.length - 1].key);
      newChildren.push({
        key: `[${from}–${to}]`,
        path: `${node.path === '' ? '' : `${node.path}.`}<repeat:${from}-${to}>`,
        params: group.reduce((a, g) => a + g.params, 0),
        children: sorted[0].children, // one member's per-member structure (aggregate is on the header)
        repeat: { count: group.length, from, to },
      });
    } else {
      newChildren.push(...group);
    }
  }
  newChildren.sort((a, b) => a.key.localeCompare(b.key, undefined, { numeric: true }));
  return { ...node, children: newChildren };
}
