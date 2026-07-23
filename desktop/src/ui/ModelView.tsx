import { useEffect, useMemo, useState } from 'react';
import { Virtuoso } from 'react-virtuoso';
import { useT } from '../i18n';
import { invoke } from '../bridge';
import { Icon } from '../ui/Icon';
import {
  buildTree,
  classifyArch,
  humanBytes,
  humanCount,
  TEMPLATE_LABEL,
  type ArchCard,
  type CheckpointInfo,
  type TensorInfo,
  type TreeNode,
} from '../state/checkpoint';
import { DTYPE_BYTES, defaultServingDtype, deriveVramInputs, estimateVram } from '../state/vram';

function dirOf(p: string): string {
  const i = Math.max(p.lastIndexOf('/'), p.lastIndexOf('\\'));
  return i >= 0 ? p.slice(0, i) : '';
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

// Precision options as bytes-per-weight (the only thing that matters for the
// weights term); fp16/bf16 collapse to one 2-byte button.
const PRECISIONS: Array<{ label: string; bytes: number }> = [
  { label: '16-bit', bytes: 2 },
  { label: 'fp8', bytes: 1 },
  { label: 'int4', bytes: 0.5 },
  { label: 'fp32', bytes: 4 },
];
const BATCHES = [1, 2, 4, 8, 16, 32];
const CONTEXTS = [2048, 4096, 8192, 16384, 32768, 131072];
const ctxLabel = (n: number): string => (n >= 1024 ? `${n / 1024}K` : String(n));

/// VRAM estimator (plan §4b): weights (exact from params × serving precision) +
/// KV cache (GQA or the compressed MLA latent) + a rough activation term, live on
/// batch/context/precision. An approximation — real runtimes add framework
/// overhead and fragmentation on top.
function VramCard({ info, card, config }: { info: CheckpointInfo; card: ArchCard | null; config: Record<string, unknown> | null }): JSX.Element {
  const t = useT();
  const [bytes, setBytes] = useState<number>(() => DTYPE_BYTES[defaultServingDtype(info.dtypeHistogram)]);
  const [batch, setBatch] = useState(1);
  const [context, setContext] = useState(8192);

  const est = useMemo(() => {
    const inputs = deriveVramInputs({
      totalParams: info.totalParams,
      weightBytes: bytes,
      template: card?.template ?? 'unknown',
      card,
      config,
      metadata: info.metadata,
    });
    return estimateVram(inputs, { batch, context, kvBytes: 2 });
  }, [info, card, config, bytes, batch, context]);

  const total = est.totalBytes;
  const seg = (v: number): string => (total > 0 ? `${(v / total) * 100}%` : '0%');

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
        <span className="modelview-vram-seg weights" style={{ width: seg(est.weightsBytes) }} title={`${t('vram.weights')} ${humanBytes(est.weightsBytes)}`} />
        <span className="modelview-vram-seg kv" style={{ width: seg(est.kvBytes) }} title={`${t('vram.kvCache')} ${humanBytes(est.kvBytes)}`} />
        <span className="modelview-vram-seg act" style={{ width: seg(est.activationBytes) }} title={`${t('vram.activation')} ${humanBytes(est.activationBytes)}`} />
      </div>
      <div className="modelview-vram-legend small muted">
        <span><span className="modelview-vram-dot weights" /> {t('vram.weights')} {humanBytes(est.weightsBytes)}</span>
        {est.kvComputable ? (
          <>
            <span><span className="modelview-vram-dot kv" /> {t('vram.kvCache')} {humanBytes(est.kvBytes)}</span>
            <span><span className="modelview-vram-dot act" /> {t('vram.activation')} {humanBytes(est.activationBytes)}</span>
          </>
        ) : (
          <span>{t('vram.kvUnknown')}</span>
        )}
      </div>
      <div className="modelview-vram-ctrls">
        <span className="modelview-vram-ctrl">
          <span className="small muted">{t('vram.precision')}</span>
          {PRECISIONS.map((p) => (
            <button key={p.label} className={`modelview-vram-btn${bytes === p.bytes ? ' on' : ''}`} onClick={() => setBytes(p.bytes)}>
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
            if (!cancelled) setConfig(JSON.parse(r.content) as Record<string, unknown>);
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

  const tree = useMemo(() => (info ? buildTree(info.tensors) : null), [info]);
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
      </div>
      {info.ops !== undefined && <OpsBar ops={info.ops} />}
      {card !== null && <ArchCardView card={card} />}
      <VramCard info={info} card={card} config={config} />
      <div className="modelview-split">
        <div className="modelview-tree">
          <div className="modelview-pane-head small muted">{t('model.namespace')}</div>
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
