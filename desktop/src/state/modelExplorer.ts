/// Loader for the Model Explorer visualizer custom element (plan §5, W4 — the
/// interactive WebGL model-graph view). The element ships as a self-registering IIFE
/// (`main_browser.js`) plus a same-origin web worker and font-texture static files,
/// all self-hosted under `/model-explorer/` by `scripts/sync-model-explorer-assets.mjs`
/// (the tree-sitter/excalidraw precedent). We inject the script once, point the
/// visualizer's globals at our self-hosted worker + assets, and resolve when the
/// custom element is defined. Rendering itself is WebGL in a real renderer — this is
/// the on-device slice; the loader is the headlessly-shippable plumbing.
import type { GraphCollection } from './modelGraph';

const SCRIPT_PATH = '/model-explorer/main_browser.js';
const ASSET_BASE = '/model-explorer/static_files';
const WORKER_PATH = '/model-explorer/worker.js';
export const ME_TAG = 'model-explorer-visualizer';

/// The subset of the visualizer element we drive (properties are set via JS, not
/// attributes). See `ai-edge-model-explorer-visualizer`'s `custom_element/index.d.ts`.
export interface ModelExplorerElement extends HTMLElement {
  graphCollections?: GraphCollection[];
}

interface ModelExplorerGlobal {
  assetFilesBaseUrl?: string;
  workerScriptPath?: string;
}

declare global {
  interface Window {
    modelExplorer?: ModelExplorerGlobal;
  }
}

let loadPromise: Promise<void> | null = null;

/// Idempotently load + register `<model-explorer-visualizer>` and configure its
/// self-hosted worker/asset paths. Safe to call from every view mount; the script is
/// injected once. Rejects if the script fails to load (missing synced assets).
export function loadModelExplorer(): Promise<void> {
  if (loadPromise !== null) return loadPromise;
  loadPromise = new Promise<void>((resolve, reject) => {
    if (typeof window === 'undefined' || typeof document === 'undefined') {
      reject(new Error('Model Explorer requires a DOM'));
      return;
    }
    if (customElements.get(ME_TAG) !== undefined) {
      configure();
      resolve();
      return;
    }
    const s = document.createElement('script');
    s.src = SCRIPT_PATH;
    s.async = true;
    s.onload = () => {
      // The IIFE sets `window.modelExplorer = {}` as its last act, so our paths must
      // be written *after* it runs (here), before the element mounts and reads them.
      configure();
      customElements.whenDefined(ME_TAG).then(() => resolve(), reject);
    };
    s.onerror = () => reject(new Error(`failed to load ${SCRIPT_PATH} — are the Model Explorer assets synced?`));
    document.head.appendChild(s);
  });
  return loadPromise;
}

function configure(): void {
  const g: ModelExplorerGlobal = window.modelExplorer ?? {};
  g.assetFilesBaseUrl = ASSET_BASE;
  g.workerScriptPath = WORKER_PATH;
  window.modelExplorer = g;
}

/// Create a configured visualizer element bound to a graph collection. The caller
/// appends it to a container and removes it on unmount.
export function createVisualizer(graphs: GraphCollection[]): ModelExplorerElement {
  const el = document.createElement(ME_TAG) as ModelExplorerElement;
  el.graphCollections = graphs;
  return el;
}
