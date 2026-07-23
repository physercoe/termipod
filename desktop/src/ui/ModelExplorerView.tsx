import { useEffect, useRef, useState } from 'react';
import { useT } from '../i18n';
import { invoke } from '../bridge';
import { Icon } from '../ui/Icon';
import type { CheckpointInfo } from '../state/checkpoint';
import { checkpointToGraphCollection, onnxToGraphCollection, type GraphCollection } from '../state/modelGraph';
import { createVisualizer, loadModelExplorer, type ModelExplorerElement } from '../state/modelExplorer';

function baseOf(p: string): string {
  const i = Math.max(p.lastIndexOf('/'), p.lastIndexOf('\\'));
  return i >= 0 ? p.slice(i + 1) : p;
}

/// Build the Model Explorer graph collection for a parsed checkpoint: the ONNX
/// operator graph when present (real nodes + edges), else the weight namespace
/// hierarchy (no edges — the collapsible grouping is the value).
function collectionFor(info: CheckpointInfo, label: string): GraphCollection {
  if (info.graph !== undefined && info.graph.nodes.length > 0) {
    return onnxToGraphCollection(info.graph, new Set(info.tensors.map((t) => t.name)), label);
  }
  return checkpointToGraphCollection(info.tensors, label);
}

/// The interactive Model Explorer WebGL graph view (plan §5, W4). Fed either a
/// **checkpoint path** (re-inspected header-only, like `ModelView`, → GraphCollection)
/// or a **pre-built GraphCollection** (the tracer Tier-2 `torch.export` traced graph).
/// Mounts the `<model-explorer-visualizer>` element; its heavy runtime (2.5 MB IIFE +
/// a layout web worker + WebGL) is self-hosted, loaded on first mount — never in boot.
export function ModelExplorerView({ path, collection }: { path?: string; collection?: GraphCollection }): JSX.Element {
  const t = useT();
  const host = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState<'loading' | 'ready' | 'error'>('loading');
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    let el: ModelExplorerElement | null = null;
    setStatus('loading');
    setErr(null);
    void (async () => {
      try {
        let gc: GraphCollection;
        if (collection !== undefined) {
          gc = collection;
        } else if (path !== undefined) {
          const info = await invoke<CheckpointInfo>('checkpoint_inspect', { path });
          if (cancelled) return;
          gc = collectionFor(info, baseOf(path) || 'model');
        } else {
          throw new Error('no graph to render');
        }
        await loadModelExplorer();
        if (cancelled || host.current === null) return;
        el = createVisualizer([gc]);
        el.style.width = '100%';
        el.style.height = '100%';
        host.current.appendChild(el);
        setStatus('ready');
      } catch (e) {
        if (!cancelled) {
          setErr(e instanceof Error ? e.message : String(e));
          setStatus('error');
        }
      }
    })();
    return () => {
      cancelled = true;
      if (el !== null && el.parentNode !== null) el.parentNode.removeChild(el);
    };
  }, [path, collection]);

  if (status === 'error')
    return (
      <div className="inspect-error region-pad">
        <Icon name="alert" size={16} /> {err}
      </div>
    );

  return (
    <div className="me-graphwrap">
      {status === 'loading' && <div className="me-graph-loading muted">{t('graph.rendering')}</div>}
      <div className="me-graph-host" ref={host} />
    </div>
  );
}
