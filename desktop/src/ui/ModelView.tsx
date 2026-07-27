import { lazy, Suspense, useEffect, useMemo, useState } from 'react';
import { Virtuoso } from 'react-virtuoso';
import { useT } from '../i18n';
import { invoke } from '../bridge';
import { Icon } from '../ui/Icon';
import {
  buildTree,
  classifyArch,
  collapseRepeats,
  estimateParamsFromConfig,
  normalizeModelConfig,
  humanBytes,
  humanCount,
  TEMPLATE_LABEL,
  type ArchCard,
  type CheckpointInfo,
  type TensorInfo,
  type TreeNode,
} from '../state/checkpoint';
import { DTYPE_BYTES, defaultServingDtype, deriveVramInputs, estimateVram, type Optimizer, type VramMode } from '../state/vram';
import {
  DEFAULT_MFU,
  GPU_PRESETS,
  effectiveDeviceFlops,
  estimateFlops,
  humanDuration,
  humanFlops,
  peakTflops,
  type ComputePrecision,
} from '../state/flops';
import { graphCollectionToDot, onnxToGraphCollection } from '../state/modelGraph';
import { buildArchSchematic } from '../state/archSchematic';
import { useInspect, type InspectTab } from '../state/inspect';
import { readRef } from '../state/inspectSources';

// React Flow (schematic) and CodeMirror (source) are heavy — keep them on their
// own lazy chunks so switching panes pulls them on demand, never at boot.
const ArchSchematicView = lazy(() => import('./ArchSchematicView').then((m) => ({ default: m.ArchSchematicView })));
const CodeView = lazy(() => import('./CodeView').then((m) => ({ default: m.CodeView })));

function dirOf(p: string): string {
  const i = Math.max(p.lastIndexOf('/'), p.lastIndexOf('\\'));
  return i >= 0 ? p.slice(0, i) : '';
}
function baseOf(p: string): string {
  const i = Math.max(p.lastIndexOf('/'), p.lastIndexOf('\\'));
  return i >= 0 ? p.slice(i + 1) : p;
}
function join(dir: string, name: string): string {
  if (dir === '') return name;
  const sep = dir.includes('\\') && !dir.includes('/') ? '\\' : '/';
  return `${dir.replace(/[\\/]+$/, '')}${sep}${name}`;
}

// dtype histogram as proportional chips (params carried in each precision).
function DtypeBar({ hist, total }: { hist: Record<string, number>; total: number }): JSX.Element {
  const entries = Object.entries(hist).sort((a, b) => b[1] - a[1]);
  return (
    <div className="modelview-dtypes">
      {entries.map(([dt, p]) => (
        <span key={dt} className="modelview-dtype" title={`${humanCount(p)} params`}>
          {dt} <span className="muted">{total > 0 ? `${Math.round((p / total) * 100)}%` : ''}</span>
        </span>
      ))}
    </div>
  );
}

// ONNX operator mix: op_type -> node count, as proportional chips (most-used first).
function OpsBar({ ops }: { ops: Record<string, number> }): JSX.Element {
  const t = useT();
  const entries = Object.entries(ops).sort((a, b) => b[1] - a[1]);
  const nodes = entries.reduce((a, [, c]) => a + c, 0);
  return (
    <div className="modelview-ops">
      <span className="small muted">
        {nodes.toLocaleString()} {t('model.operators')}
      </span>
      {entries.slice(0, 24).map(([op, c]) => (
        <span key={op} className="modelview-op" title={`${c.toLocaleString()} ×`}>
          {op} <span className="muted">{c.toLocaleString()}</span>
        </span>
      ))}
      {entries.length > 24 && <span className="small muted">+{entries.length - 24}</span>}
    </div>
  );
}

function Field({ label, value }: { label: string; value: string | number | undefined }): JSX.Element | null {
  if (value === undefined || value === '') return null;
  return (
    <div className="modelview-field">
      <span className="modelview-field-l small muted">{label}</span>
      <span className="modelview-field-v">{value}</span>
    </div>
  );
}

function ArchCardView({ card }: { card: ArchCard }): JSX.Element {
  const t = useT();
  const prov =
    card.provenance === 'config' ? t('model.provConfig') : card.provenance === 'gguf' ? t('model.provGguf') : t('model.provTensors');
  return (
    <div className="modelview-card">
      <div className="modelview-card-head">
        <span className="modelview-family">{card.family}</span>
        <span className="modelview-template">{TEMPLATE_LABEL[card.template]}</span>
        <span className="spacer" />
        <span className={`modelview-prov ${card.provenance}`} title={t('model.provNote')}>
          {prov}
        </span>
      </div>
      <div className="modelview-fields">
        <Field label={t('model.layers')} value={card.layers} />
        <Field label={t('model.hidden')} value={card.hidden} />
        <Field label={t('model.heads')} value={card.heads} />
        <Field label={t('model.kvHeads')} value={card.kvHeads} />
        <Field label={t('model.vocab')} value={card.vocab !== undefined ? humanCount(card.vocab) : undefined} />
        <Field label={t('model.context')} value={card.context !== undefined ? card.context.toLocaleString() : undefined} />
        <Field label={t('model.experts')} value={card.experts} />
        <Field label={t('model.expertsPerTok')} value={card.expertsPerTok} />
        <Field label={t('model.sharedExperts')} value={card.sharedExperts} />
      </div>
      {card.chips.length > 0 && (
        <div className="modelview-chips">
          {card.chips.map((c) => (
            <span key={c} className="modelview-chip">
              {c}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

/// Config-only architecture view (round-3 §5a): an HF release is fully
/// describable **without its weights** — `config.json` carries the architecture.
/// This renders the same `ArchCardView` from a parsed config alone (no
/// `CheckpointInfo`, no tensor tree/table), reachable from **every** source
/// (local / workspace / remote / hub / github / hf) since it only needs the text
/// the tab already read. When a sibling `model.safetensors.index.json` is
/// readable from the same source, its tensor-name map corroborates MoE/MLA and
/// its `total_size` gives the weights figure — still without reading a weight.
type ConfigPane = 'params' | 'schema' | 'source';

export function ConfigArchView({ tab, config }: { tab: InspectTab; config: Record<string, unknown> }): JSX.Element {
  const t = useT();
  const [tensorNames, setTensorNames] = useState<string[]>([]);
  const [totalSize, setTotalSize] = useState<number | null>(null);
  const [pane, setPane] = useState<ConfigPane>('params');
  // The raw config text drives the Source pane (falls back to a re-stringify when
  // the store hasn't cached the body — e.g. a freshly derived config).
  const rawContent = useInspect((s) => s.content[tab.id]);
  const raw = rawContent ?? JSON.stringify(config, null, 2);

  // Best-effort sibling index.json corroboration from the same source.
  useEffect(() => {
    if (tab.path === undefined) return;
    let cancelled = false;
    const idxPath = join(dirOf(tab.path), 'model.safetensors.index.json');
    void readRef({ source: tab.source, title: 'index', path: idxPath, hostId: tab.hostId, projectId: tab.projectId, repo: tab.repo }, `insp-${tab.id}-idx`)
      .then((txt) => {
        if (cancelled) return;
        const j = JSON.parse(txt) as { weight_map?: Record<string, string>; metadata?: { total_size?: number } };
        setTensorNames(Object.keys(j.weight_map ?? {}));
        const size = j.metadata?.total_size;
        if (typeof size === 'number') setTotalSize(size);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab.id]);

  const card = useMemo<ArchCard | null>(() => classifyArch({ config, tensorNames }), [config, tensorNames]);
  // Analytic param count from the config (dense/GQA/MoE); null for MLA or when a
  // field is missing. Approximate — feeds the VRAM estimator, badged as such.
  const params = useMemo(() => estimateParamsFromConfig(config), [config]);
  // The paper-style architecture schematic — offered only when the config is
  // stackable (a hidden size + a positive layer count).
  const schematic = useMemo(() => (card !== null ? buildArchSchematic(card, config) : null), [card, config]);

  // Schema is only offered when the config is a stackable transformer; if the
  // user is on it when that stops being true (edited config), fall back to params.
  const schemaPane = pane === 'schema' && schematic !== null;

  return (
    <div className="modelview">
      <div className="modelview-summary">
        <span className="modelview-fmt">config</span>
        {params !== null && (
          <span className="modelview-stat" title={t('model.paramsApproxNote')}>
            <span className="modelview-stat-v">≈{humanCount(params)}</span> <span className="small muted">{t('model.paramsApprox')}</span>
          </span>
        )}
        {totalSize !== null && (
          <span className="modelview-stat">
            <span className="modelview-stat-v">{humanBytes(totalSize)}</span> <span className="small muted">{t('model.weightsFromIndex')}</span>
          </span>
        )}
        <span className="spacer" />
        {/* Three in-place panes — no more spawning a fresh schematic tab per click. */}
        <div className="modelview-panes" role="tablist">
          <button className={`modelview-pane-btn${pane === 'params' ? ' on' : ''}`} role="tab" aria-selected={pane === 'params'} onClick={() => setPane('params')}>
            <Icon name="sliders" size={13} /> {t('model.paneParams')}
          </button>
          <button
            className={`modelview-pane-btn${schemaPane ? ' on' : ''}`}
            role="tab"
            aria-selected={schemaPane}
            disabled={schematic === null}
            title={schematic === null ? t('archgraph.cannotDerive') : undefined}
            onClick={() => setPane('schema')}
          >
            <Icon name="sitemap" size={13} /> {t('model.paneSchema')}
          </button>
          <button className={`modelview-pane-btn${pane === 'source' ? ' on' : ''}`} role="tab" aria-selected={pane === 'source'} onClick={() => setPane('source')}>
            <Icon name="code" size={13} /> {t('model.paneSource')}
          </button>
        </div>
      </div>

      {pane === 'params' && (
        <>
          {card !== null ? <ArchCardView card={card} /> : <div className="muted region-pad">{t('model.notAConfig')}</div>}
          {params !== null && <VramCard totalParams={params} dtypeHist={{}} card={card} config={config} />}
          {params !== null && <FlopsCard totalParams={params} card={card} config={config} />}
          <div className="modelview-confignote small muted">
            <Icon name="alert" size={13} /> {t('model.configOnlyNote')}
          </div>
        </>
      )}

      {schemaPane && (
        <div className="modelview-pane-body">
          <Suspense fallback={<div className="muted region-pad">{t('graph.rendering')}</div>}>
            <ArchSchematicView schematic={schematic} config={config} card={card} />
          </Suspense>
        </div>
      )}

      {pane === 'source' && (
        <div className="modelview-pane-body">
          <Suspense fallback={<div className="muted region-pad">{t('inspect.loading')}</div>}>
            <CodeView value={raw} filename="config.json" />
          </Suspense>
        </div>
      )}
    </div>
  );
}

// Precision options as bytes-per-weight (the only thing that matters for the
// weights/gradient term); fp16/bf16 collapse to one 2-byte button. fp8/int8
// share 1 B and fp4/int4 share 0.5 B — same memory cost, distinct schemes, so
// selection tracks the label, not the byte value.
const PRECISIONS: Array<{ label: string; bytes: number }> = [
  { label: 'fp32', bytes: 4 },
  { label: '16-bit', bytes: 2 },
  { label: 'fp8', bytes: 1 },
  { label: 'int8', bytes: 1 },
  { label: 'fp4', bytes: 0.5 },
  { label: 'int4', bytes: 0.5 },
];
const BATCHES = [1, 2, 4, 8, 16, 32];
const CONTEXTS = [2048, 4096, 8192, 16384, 32768, 131072, 262144, 1048576];
const ctxLabel = (n: number): string => (n >= 1048576 ? `${n / 1048576}M` : n >= 1024 ? `${n / 1024}K` : String(n));
const OPTIMIZERS: Array<{ id: Optimizer; label: string }> = [
  { id: 'adamw', label: 'AdamW' },
  { id: 'adam8bit', label: '8-bit Adam' },
  { id: 'sgd', label: 'SGD' },
];

// Map the checkpoint's own default dtype to a precision-button label.
function defaultPrecLabel(hist: Record<string, number>): string {
  const b = DTYPE_BYTES[defaultServingDtype(hist)];
  return PRECISIONS.find((p) => p.bytes === b)?.label ?? '16-bit';
}

/// VRAM estimator (plan §4b): **inference** (weights, exact from params × serving
/// precision, + KV cache (GQA or the compressed MLA latent) + a transient
/// activation term) or **training** (weights + gradients + optimizer states +
/// the full backward activation stash), live on batch/context/precision and, in
/// training, the optimizer + gradient-checkpointing. An approximation — real
/// runtimes add framework overhead and fragmentation on top.
function VramCard({
  totalParams,
  dtypeHist,
  metadata,
  card,
  config,
}: {
  totalParams: number;
  dtypeHist: Record<string, number>;
  metadata?: Record<string, string | number>;
  card: ArchCard | null;
  config: Record<string, unknown> | null;
}): JSX.Element {
  const t = useT();
  const [precLabel, setPrecLabel] = useState<string>(() => defaultPrecLabel(dtypeHist));
  const [batch, setBatch] = useState(1);
  const [context, setContext] = useState(8192);
  const [mode, setMode] = useState<VramMode>('inference');
  const [optimizer, setOptimizer] = useState<Optimizer>('adamw');
  const [gradCkpt, setGradCkpt] = useState(true);
  const bytes = PRECISIONS.find((p) => p.label === precLabel)?.bytes ?? 2;

  const est = useMemo(() => {
    const inputs = deriveVramInputs({
      totalParams,
      weightBytes: bytes,
      template: card?.template ?? 'unknown',
      card,
      config,
      metadata,
    });
    return estimateVram(inputs, { batch, context, kvBytes: 2, mode, optimizer, gradCheckpoint: gradCkpt });
  }, [totalParams, metadata, card, config, bytes, batch, context, mode, optimizer, gradCkpt]);

  const total = est.totalBytes;
  const seg = (v: number): string => (total > 0 ? `${(v / total) * 100}%` : '0%');
  const training = mode === 'training';

  // Segments differ by mode: inference = weights + KV cache + activations;
  // training = weights + gradients + optimizer + activations. The `always` set
  // is param-based (trustworthy without dims); the `derived` set needs the model
  // dims, and degrades to an honest note when they're missing.
  const segs: Array<{ key: string; label: string; v: number }> = training
    ? [
        { key: 'weights', label: t('vram.weights'), v: est.weightsBytes },
        { key: 'grad', label: t('vram.gradients'), v: est.gradientBytes },
        { key: 'opt', label: t('vram.optimizer'), v: est.optimizerBytes },
        { key: 'act', label: t('vram.activation'), v: est.activationBytes },
      ]
    : [
        { key: 'weights', label: t('vram.weights'), v: est.weightsBytes },
        { key: 'kv', label: t('vram.kvCache'), v: est.kvBytes },
        { key: 'act', label: t('vram.activation'), v: est.activationBytes },
      ];
  const alwaysKeys = training ? ['weights', 'grad', 'opt'] : ['weights'];
  const derivedNote = training ? t('vram.actUnknown') : t('vram.kvUnknown');
  const legendItem = (s: { key: string; label: string; v: number }): JSX.Element => (
    <span key={s.key}>
      <span className={`modelview-vram-dot ${s.key}`} /> {s.label} {humanBytes(s.v)}
    </span>
  );

  return (
    <div className="modelview-vram">
      <div className="modelview-vram-head">
        <span className="modelview-vram-title small muted">{t('vram.title')}</span>
        <span className="modelview-vram-approx small muted" title={t('vram.approxNote')}>
          {t('vram.approximate')}
        </span>
        <span className="spacer" />
        <span className="modelview-vram-total">{humanBytes(total)}</span>
      </div>
      <div className="modelview-vram-bar" role="img" aria-label={t('vram.total')}>
        {segs.map((s) => (
          <span key={s.key} className={`modelview-vram-seg ${s.key}`} style={{ width: seg(s.v) }} title={`${s.label} ${humanBytes(s.v)}`} />
        ))}
      </div>
      <div className="modelview-vram-legend small muted">
        {segs.filter((s) => alwaysKeys.includes(s.key)).map(legendItem)}
        {est.kvComputable ? segs.filter((s) => !alwaysKeys.includes(s.key)).map(legendItem) : <span>{derivedNote}</span>}
      </div>
      <div className="modelview-vram-ctrls">
        <span className="modelview-vram-ctrl">
          <span className="small muted">{t('vram.mode')}</span>
          <button className={`modelview-vram-btn${!training ? ' on' : ''}`} onClick={() => setMode('inference')}>
            {t('vram.inference')}
          </button>
          <button className={`modelview-vram-btn${training ? ' on' : ''}`} onClick={() => setMode('training')}>
            {t('vram.training')}
          </button>
        </span>
        <span className="modelview-vram-ctrl">
          <span className="small muted">{t('vram.precision')}</span>
          {PRECISIONS.map((p) => (
            <button key={p.label} className={`modelview-vram-btn${precLabel === p.label ? ' on' : ''}`} onClick={() => setPrecLabel(p.label)}>
              {p.label}
            </button>
          ))}
        </span>
      </div>
      <div className="modelview-vram-ctrls">
        <span className="modelview-vram-ctrl">
          <span className="small muted">{t('vram.batch')}</span>
          {BATCHES.map((b) => (
            <button key={b} className={`modelview-vram-btn${batch === b ? ' on' : ''}`} onClick={() => setBatch(b)}>
              {b}
            </button>
          ))}
        </span>
        <span className="modelview-vram-ctrl">
          <span className="small muted">{t('vram.context')}</span>
          {CONTEXTS.map((c) => (
            <button key={c} className={`modelview-vram-btn${context === c ? ' on' : ''}`} onClick={() => setContext(c)}>
              {ctxLabel(c)}
            </button>
          ))}
        </span>
      </div>
      {training && (
        <div className="modelview-vram-ctrls">
          <span className="modelview-vram-ctrl">
            <span className="small muted">{t('vram.optimizerLabel')}</span>
            {OPTIMIZERS.map((o) => (
              <button key={o.id} className={`modelview-vram-btn${optimizer === o.id ? ' on' : ''}`} onClick={() => setOptimizer(o.id)}>
                {o.label}
              </button>
            ))}
          </span>
          <span className="modelview-vram-ctrl">
            <button
              className={`modelview-vram-btn${gradCkpt ? ' on' : ''}`}
              title={t('vram.gradCheckpointHint')}
              onClick={() => setGradCkpt((v) => !v)}
            >
              {t('vram.gradCheckpoint')}
            </button>
          </span>
        </div>
      )}
    </div>
  );
}

const MFUS = [0.2, 0.3, 0.4, 0.5, 0.6];
const GPU_COUNTS = [1, 2, 4, 8, 16, 32, 64];
const COMPUTE_PRECISIONS: ComputePrecision[] = ['bf16', 'fp8', 'fp4'];

// Round a tokens/sec rate for display (integers above 1, else 2 decimals).
function fmtRate(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '—';
  return n >= 1 ? Math.round(n).toLocaleString() : n.toFixed(2);
}

/// FLOPS / throughput estimator (companion to the VRAM card). Given a GPU spec
/// (preset or custom TFLOP/s), compute precision, MFU and device count, estimate
/// how fast the model runs: **inference** = prefill time + decode latency;
/// **training** = one forward+backward step time + throughput. Uses the active
/// parameter count (MoE fires only its top-k experts) and the causal-attention
/// (∝ context²) term. Order-of-magnitude, like the VRAM card.
function FlopsCard({
  totalParams,
  metadata,
  card,
  config,
}: {
  totalParams: number;
  metadata?: Record<string, string | number>;
  card: ArchCard | null;
  config: Record<string, unknown> | null;
}): JSX.Element {
  const t = useT();
  const [gpuId, setGpuId] = useState('h100');
  const [customTf, setCustomTf] = useState('');
  const [precision, setPrecision] = useState<ComputePrecision>('bf16');
  const [mfu, setMfu] = useState(DEFAULT_MFU);
  const [numGpus, setNumGpus] = useState(1);
  const [mode, setMode] = useState<VramMode>('inference');
  const [batch, setBatch] = useState(1);
  const [context, setContext] = useState(8192);

  // Active params = exact total × the config's active/total fraction (MoE fires
  // only top-k experts). Dense or config-less → the whole param count works.
  const active = useMemo(() => {
    if (config === null) return totalParams;
    const total = estimateParamsFromConfig(config);
    const act = estimateParamsFromConfig(config, { activeOnly: true });
    return total !== null && act !== null && total > 0 ? totalParams * (act / total) : totalParams;
  }, [config, totalParams]);
  const isMoe = card?.template === 'moe' || card?.template === 'mla-moe';

  // Reuse the VRAM input derivation for layers + attention width (heads×headDim).
  const dims = useMemo(
    () => deriveVramInputs({ totalParams, weightBytes: 1, template: card?.template ?? 'unknown', card, config, metadata }),
    [totalParams, metadata, card, config],
  );
  const attnDim =
    dims.heads !== undefined && dims.heads > 0
      ? dims.heads * (dims.headDim ?? (dims.hidden !== undefined ? dims.hidden / dims.heads : 0))
      : undefined;

  const gpu = GPU_PRESETS.find((g) => g.id === gpuId) ?? GPU_PRESETS[0];
  const custom = customTf.trim() !== '' && Number.isFinite(Number(customTf)) && Number(customTf) > 0 ? Number(customTf) : null;
  const presetPeak = peakTflops(gpu, precision);
  const peak = custom ?? presetPeak;
  const deviceFlops = peak !== undefined && peak > 0 ? effectiveDeviceFlops(peak, numGpus, mfu) : 0;

  const est = useMemo(
    () => estimateFlops({ activeParams: active, layers: dims.layers, attnDim }, { mode, context, batch, deviceFlops }),
    [active, dims.layers, attnDim, mode, context, batch, deviceFlops],
  );
  const training = mode === 'training';
  const supported = peak !== undefined && peak > 0;

  const readout = (label: string, value: string, sub?: string): JSX.Element => (
    <div className="modelview-flops-out" key={label}>
      <span className="small muted">{label}</span>
      <span className="modelview-flops-val">{value}</span>
      {sub !== undefined && <span className="small muted">{sub}</span>}
    </div>
  );

  return (
    <div className="modelview-vram">
      <div className="modelview-vram-head">
        <span className="modelview-vram-title small muted">{t('flops.title')}</span>
        <span className="modelview-vram-approx small muted" title={t('flops.approxNote')}>
          {t('vram.approximate')}
        </span>
        <span className="spacer" />
        <span className="small muted">
          {t('flops.activeParams')} {humanCount(active)}
          {isMoe ? ` / ${humanCount(totalParams)}` : ''}
        </span>
      </div>

      <div className="modelview-flops-outs">
        {!supported ? (
          <span className="small muted">{t('flops.unsupportedPrec')}</span>
        ) : training ? (
          <>
            {readout(t('flops.step'), humanDuration(est.stepSeconds), `${batch}×${ctxLabel(context)}`)}
            {readout(t('flops.throughput'), `${fmtRate(est.tokensPerSecond)} ${t('flops.tokPerSecUnit')}`)}
            {readout(t('flops.perDay'), `${humanCount(est.tokensPerSecond * 86400)} tok`)}
          </>
        ) : (
          <>
            {readout(t('flops.prefill'), humanDuration(est.stepSeconds), `${ctxLabel(context)} · ${fmtRate(est.tokensPerSecond)} ${t('flops.tokPerSecUnit')}`)}
            {readout(
              t('flops.decode'),
              `${(est.decodeMsPerToken ?? 0).toFixed(2)} ${t('flops.msPerTokUnit')}`,
              est.decodeMsPerToken !== undefined && est.decodeMsPerToken > 0 ? `${fmtRate(1000 / est.decodeMsPerToken)} ${t('flops.tokPerSecUnit')}` : undefined,
            )}
            {readout(t('flops.perToken'), humanFlops(est.linearFlopsPerToken + est.attnFlopsPerToken))}
          </>
        )}
      </div>
      {supported && !est.attnComputable && <span className="small muted">{t('flops.attnUnknown')}</span>}

      <div className="modelview-vram-ctrls">
        <span className="modelview-vram-ctrl">
          <span className="small muted">{t('vram.mode')}</span>
          <button className={`modelview-vram-btn${!training ? ' on' : ''}`} onClick={() => setMode('inference')}>
            {t('vram.inference')}
          </button>
          <button className={`modelview-vram-btn${training ? ' on' : ''}`} onClick={() => setMode('training')}>
            {t('vram.training')}
          </button>
        </span>
        <span className="modelview-vram-ctrl">
          <span className="small muted">{t('flops.gpu')}</span>
          {GPU_PRESETS.map((g) => (
            <button
              key={g.id}
              className={`modelview-vram-btn${custom === null && gpuId === g.id ? ' on' : ''}`}
              title={`${g.memGb} GB`}
              onClick={() => {
                setGpuId(g.id);
                setCustomTf('');
              }}
            >
              {g.label}
            </button>
          ))}
          <input
            className="modelview-flops-num"
            value={customTf}
            inputMode="decimal"
            placeholder={t('flops.customTflops')}
            onChange={(e) => setCustomTf(e.target.value)}
          />
        </span>
      </div>

      <div className="modelview-vram-ctrls">
        <span className="modelview-vram-ctrl">
          <span className="small muted">{t('flops.compute')}</span>
          {COMPUTE_PRECISIONS.map((p) => {
            const ok = custom !== null || peakTflops(gpu, p) !== undefined;
            return (
              <button
                key={p}
                className={`modelview-vram-btn${precision === p ? ' on' : ''}`}
                disabled={!ok}
                title={ok ? undefined : t('flops.unsupportedPrec')}
                onClick={() => setPrecision(p)}
              >
                {p}
              </button>
            );
          })}
        </span>
        <span className="modelview-vram-ctrl">
          <span className="small muted" title={t('flops.mfuHint')}>
            {t('flops.mfu')}
          </span>
          {MFUS.map((m) => (
            <button key={m} className={`modelview-vram-btn${mfu === m ? ' on' : ''}`} onClick={() => setMfu(m)}>
              {m}
            </button>
          ))}
        </span>
        <span className="modelview-vram-ctrl">
          <span className="small muted">{t('flops.devices')}</span>
          {GPU_COUNTS.map((n) => (
            <button key={n} className={`modelview-vram-btn${numGpus === n ? ' on' : ''}`} onClick={() => setNumGpus(n)}>
              {n}
            </button>
          ))}
        </span>
      </div>

      <div className="modelview-vram-ctrls">
        <span className="modelview-vram-ctrl">
          <span className="small muted">{t('vram.batch')}</span>
          {BATCHES.map((b) => (
            <button key={b} className={`modelview-vram-btn${batch === b ? ' on' : ''}`} onClick={() => setBatch(b)}>
              {b}
            </button>
          ))}
        </span>
        <span className="modelview-vram-ctrl">
          <span className="small muted">{t('vram.context')}</span>
          {CONTEXTS.map((c) => (
            <button key={c} className={`modelview-vram-btn${context === c ? ' on' : ''}`} onClick={() => setContext(c)}>
              {ctxLabel(c)}
            </button>
          ))}
        </span>
      </div>
    </div>
  );
}

function TensorRow({ tensor }: { tensor: TensorInfo }): JSX.Element {
  return (
    <div className="modelview-trow">
      <span className="modelview-tname mono" title={tensor.name}>
        {tensor.name}
      </span>
      <span className="modelview-tdtype">{tensor.dtype}</span>
      <span className="modelview-tshape mono">{tensor.shape.length > 0 ? tensor.shape.join('×') : '—'}</span>
      <span className="modelview-tparams">{humanCount(tensor.params)}</span>
    </div>
  );
}

function TensorTable({ tensors }: { tensors: TensorInfo[] }): JSX.Element {
  const t = useT();
  const [filter, setFilter] = useState('');
  const shown = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return q === '' ? tensors : tensors.filter((x) => x.name.toLowerCase().includes(q));
  }, [tensors, filter]);
  return (
    <div className="modelview-table">
      <div className="modelview-table-bar">
        <Icon name="search" size={13} />
        <input className="modelview-filter" value={filter} placeholder={t('model.filterTensors')} onChange={(e) => setFilter(e.target.value)} />
        <span className="small muted">
          {shown.length.toLocaleString()}
          {shown.length !== tensors.length ? ` / ${tensors.length.toLocaleString()}` : ''} {t('model.tensors')}
        </span>
      </div>
      <div className="modelview-thead small muted">
        <span className="modelview-tname">{t('model.name')}</span>
        <span className="modelview-tdtype">{t('model.dtype')}</span>
        <span className="modelview-tshape">{t('model.shape')}</span>
        <span className="modelview-tparams">{t('model.params')}</span>
      </div>
      <div className="modelview-tbody">
        <Virtuoso totalCount={shown.length} itemContent={(i) => <TensorRow tensor={shown[i]} />} />
      </div>
    </div>
  );
}

function TreeRows({
  node,
  depth,
  expanded,
  toggle,
}: {
  node: TreeNode;
  depth: number;
  expanded: Set<string>;
  toggle: (p: string) => void;
}): JSX.Element {
  const open = expanded.has(node.path);
  const hasKids = node.children.length > 0;
  return (
    <>
      <div
        className={`modelview-tnode${node.leaf ? ' leaf' : ''}`}
        style={{ paddingLeft: `${depth * 12 + 4}px` }}
        onClick={() => hasKids && toggle(node.path)}
        role={hasKids ? 'button' : undefined}
      >
        {hasKids ? <Icon name={open ? 'chevron-down' : 'chevron-right'} size={12} /> : <span className="modelview-tnode-dot" />}
        <span className="modelview-tnode-key mono">{node.key}</span>
        {node.repeat && <span className="modelview-tnode-x">×{node.repeat.count}</span>}
        {node.leaf && <span className="modelview-tnode-dtype small muted">{node.leaf.dtype}</span>}
        <span className="spacer" />
        <span className="modelview-tnode-params small muted">{humanCount(node.params)}</span>
      </div>
      {open && hasKids && node.children.map((c) => <TreeRows key={c.path} node={c} depth={depth + 1} expanded={expanded} toggle={toggle} />)}
    </>
  );
}

/// Checkpoint inspector (plan §5, W4 core): a summary strip (size, params, dtype
/// histogram), an HF-config/gguf **architecture card** (family + block template +
/// component chips + provenance), a collapsible **namespace tree** of tensor
/// names with per-subtree param rollups, and a virtualized **tensor table**.
/// Parsing happens in the main process (`checkpoint_inspect`) — header only, the
/// bytes never leave disk.
export function ModelView({ path }: { path: string }): JSX.Element {
  const t = useT();
  const [info, setInfo] = useState<CheckpointInfo | null>(null);
  const [config, setConfig] = useState<Record<string, unknown> | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setInfo(null);
    setErr(null);
    setConfig(null);
    void (async () => {
      try {
        const ck = await invoke<CheckpointInfo>('checkpoint_inspect', { path });
        if (cancelled) return;
        setInfo(ck);
        // HF layout: a config.json beside the checkpoint feeds the architecture
        // card (safetensors only — gguf carries the same fields in its metadata).
        if (ck.format !== 'gguf') {
          try {
            const r = await invoke<{ content: string }>('doc_read', { path: join(dirOf(path), 'config.json') });
            // Flatten any multimodal wrapper (text_config/…) so the arch/VRAM/
            // FLOPS readers see a single LM config — same normalization the
            // paste/tab path gets via parseHfConfig.
            if (!cancelled) setConfig(normalizeModelConfig(JSON.parse(r.content) as Record<string, unknown>));
          } catch {
            /* no sidecar — the card falls back to tensor-name inference */
          }
        }
      } catch (e) {
        if (!cancelled) setErr(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [path]);

  const rawTree = useMemo(() => (info ? buildTree(info.tensors) : null), [info]);
  const [collapseReps, setCollapseReps] = useState(true);
  // ×N repeat-collapse (plan §4b): fold identical indexed layers into one group.
  const tree = useMemo(() => (rawTree ? (collapseReps ? collapseRepeats(rawTree) : rawTree) : null), [rawTree, collapseReps]);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  // Default-expand the top level so the layer namespace is one click away.
  useEffect(() => {
    if (tree === null) return;
    setExpanded(new Set(['', ...tree.children.map((c) => c.path)]));
  }, [tree]);
  const toggle = (p: string): void =>
    setExpanded((s) => {
      const n = new Set(s);
      if (n.has(p)) n.delete(p);
      else n.add(p);
      return n;
    });

  const card = useMemo<ArchCard | null>(
    () => (info ? classifyArch({ config, metadata: info.metadata, tensorNames: info.tensors.map((x) => x.name) }) : null),
    [info, config],
  );

  if (err !== null)
    return (
      <div className="inspect-error region-pad">
        <Icon name="alert" size={16} /> {err}
      </div>
    );
  if (info === null || tree === null) return <div className="muted region-pad">{t('inspect.loading')}</div>;

  return (
    <div className="modelview">
      <div className="modelview-summary">
        <span className="modelview-fmt">{info.format}</span>
        <span className="modelview-stat">
          <span className="modelview-stat-v">{humanCount(info.totalParams)}</span> <span className="small muted">{t('model.paramsTotal')}</span>
        </span>
        <span className="modelview-stat">
          <span className="modelview-stat-v">{humanBytes(info.fileSize)}</span>
        </span>
        <span className="modelview-stat">
          <span className="modelview-stat-v">{info.tensorCount.toLocaleString()}</span> <span className="small muted">{t('model.tensors')}</span>
        </span>
        <DtypeBar hist={info.dtypeHistogram} total={info.totalParams} />
        {info.truncatedTensors !== undefined && <span className="small muted">(+{info.truncatedTensors} {t('model.truncated')})</span>}
        <span className="spacer" />
        {info.graph !== undefined && info.graph.nodes.length > 0 && (
          <button
            className="import-btn modelview-graph-btn"
            onClick={() => {
              const gc = onnxToGraphCollection(info.graph!, new Set(info.tensors.map((x) => x.name)), baseOf(path) || 'onnx');
              useInspect.getState().open({ kind: 'graph', source: 'paste', title: `graph: ${baseOf(path)}` }, graphCollectionToDot(gc));
            }}
          >
            <Icon name="diagram" size={13} /> {t('model.viewGraph')}
            {info.graph.truncatedNodes !== undefined && <span className="small muted"> (+{info.graph.truncatedNodes})</span>}
          </button>
        )}
        <button
          className="import-btn modelview-graph-btn"
          title={t('model.interactiveGraphHint')}
          onClick={() => useInspect.getState().open({ kind: 'megraph', source: 'local', path, title: `graph: ${baseOf(path)}` })}
        >
          <Icon name="canvas" size={13} /> {t('model.interactiveGraph')}
        </button>
      </div>
      {info.ops !== undefined && <OpsBar ops={info.ops} />}
      {card !== null && <ArchCardView card={card} />}
      <VramCard totalParams={info.totalParams} dtypeHist={info.dtypeHistogram} metadata={info.metadata} card={card} config={config} />
      <FlopsCard totalParams={info.totalParams} metadata={info.metadata} card={card} config={config} />
      <div className="modelview-split">
        <div className="modelview-tree">
          <div className="modelview-pane-head small muted">
            {t('model.namespace')}
            <span className="spacer" />
            <label className="modelview-collapse-toggle" title={t('model.collapseRepeatsHint')}>
              <input type="checkbox" checked={collapseReps} onChange={(e) => setCollapseReps(e.target.checked)} />
              {t('model.collapseRepeats')}
            </label>
          </div>
          <div className="modelview-tree-body">
            {tree.children.map((c) => (
              <TreeRows key={c.path} node={c} depth={0} expanded={expanded} toggle={toggle} />
            ))}
          </div>
        </div>
        <TensorTable tensors={info.tensors} />
      </div>
    </div>
  );
}
