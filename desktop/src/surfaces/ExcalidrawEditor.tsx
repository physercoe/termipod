import { useEffect, useMemo, useRef, useState } from 'react';
import { Excalidraw, exportToBlob, exportToSvg, getSceneVersion, serializeAsJSON } from '@excalidraw/excalidraw';
import '@excalidraw/excalidraw/index.css';
import { invoke } from '../bridge';
import { useT } from '../i18n';
import { isShell } from '../platform';
import { useTheme } from '../state/theme';
import { toast } from '../state/toast';
import { useDocuments, type Doc } from '../state/documents';

/// The J2 Author **Excalidraw** editor — a freeform hand-drawn sketch surface
/// (figure-plan Phase C). Unlike the `figure` kind (a `spec → SVG` function) this
/// is a stateful interactive editor, so it follows the `diagram`/`canvas`/`table`
/// kind-per-format precedent: one `DocKind` (`'excalidraw'`), body = `.excalidraw`
/// JSON (the ecosystem-standard, agent-authorable scene format).
///
/// The `<Excalidraw>` component is uncontrolled after mount — it reads
/// `initialData` once and then owns the live scene — so we mount it keyed by
/// `doc.id` (a doc switch remounts) and stream changes back out via `onChange`,
/// exactly as `DiagramEditor` does with the draw.io embed. No controlled-value
/// reconcile loop is needed or wanted.

// Excalidraw fetches its fonts at runtime from `${EXCALIDRAW_ASSET_PATH}fonts/…`,
// falling back to the esm.sh CDN when unset. We self-host them (see
// scripts/sync-excalidraw-assets.mjs) and point the loader at the local copy so
// the editor renders fully offline — no network fetch. Root-relative so it
// resolves under both the dev server and the packaged `app://` origin.
if (typeof window !== 'undefined') {
  (window as unknown as { EXCALIDRAW_ASSET_PATH?: string }).EXCALIDRAW_ASSET_PATH ??= '/excalidraw-assets/';
}

type ExcalidrawProps = React.ComponentProps<typeof Excalidraw>;
type ExcalidrawAPI = Parameters<NonNullable<ExcalidrawProps['excalidrawAPI']>>[0];
type ChangeArgs = Parameters<NonNullable<ExcalidrawProps['onChange']>>;
type SceneData = { elements: ChangeArgs[0]; appState: ChangeArgs[1]; files: ChangeArgs[2] };

/// Parse a persisted `.excalidraw` body into Excalidraw `initialData`. A blank
/// (new) doc has no body → a blank scene. `serializeAsJSON` already strips
/// runtime-only appState, so the stored `appState` is safe to restore verbatim.
function toInitialData(body: string): SceneData | null {
  if (body.trim() === '') return null;
  try {
    const d = JSON.parse(body) as { elements?: unknown; appState?: unknown; files?: unknown };
    return {
      elements: (Array.isArray(d.elements) ? d.elements : []) as ChangeArgs[0],
      appState: (d.appState ?? {}) as ChangeArgs[1],
      files: (d.files ?? undefined) as ChangeArgs[2],
    };
  } catch {
    return null;
  }
}

function initialSceneVersion(body: string): number {
  const data = toInitialData(body);
  return getSceneVersion(data?.elements ?? []);
}

async function blobToBase64(blob: Blob): Promise<string> {
  const dataUrl = await new Promise<string>((resolve, reject) => {
    const fr = new FileReader();
    fr.onload = () => resolve(String(fr.result));
    fr.onerror = () => reject(new Error('blob read failed'));
    fr.readAsDataURL(blob);
  });
  return dataUrl.split(',')[1] ?? '';
}

export function ExcalidrawEditor({ doc }: { doc: Doc }): JSX.Element {
  const t = useT();
  const pref = useTheme((s) => s.pref);
  const update = useDocuments((s) => s.update);
  const apiRef = useRef<ExcalidrawAPI | null>(null);
  const [ready, setReady] = useState(false);

  // The dark/light the shell resolves to (mirrors the app's theme; Excalidraw's
  // own theme toggle is superseded — the app owns theme).
  const dark =
    pref === 'dark' || (pref === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches);

  // Read `initialData` once per doc (the component is uncontrolled thereafter).
  const initialData = useMemo(() => toInitialData(doc.body), [doc.id]); // eslint-disable-line react-hooks/exhaustive-deps
  const baseName = (doc.title !== '' ? doc.title : 'sketch').replace(/\.[^.]+$/, '').replace(/[^\w.-]+/g, '-');

  // Persist is debounced: `onChange` fires on every pointer move while drawing,
  // and `serializeAsJSON` over a large scene per event is a main-thread stall.
  // Skip emits that don't change the scene version (Excalidraw re-emits on
  // mount + font-load reflow with the loaded scene — those must not dirty the
  // doc). The trailing write flushes on unmount so the last stroke survives.
  const lastVersion = useRef<number>(initialSceneVersion(doc.body));
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const flush = useRef<(() => void) | undefined>(undefined);

  useEffect(() => {
    return () => {
      if (timer.current !== undefined) clearTimeout(timer.current);
      flush.current?.();
    };
  }, [doc.id]);

  function onChange(...[elements, appState, files]: ChangeArgs): void {
    const version = getSceneVersion(elements);
    if (version === lastVersion.current) return;
    lastVersion.current = version;
    flush.current = () => update(doc.id, { body: serializeAsJSON(elements, appState, files, 'local') });
    if (timer.current !== undefined) clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      timer.current = undefined;
      flush.current?.();
    }, 600);
  }

  async function exportSvg(): Promise<void> {
    const api = apiRef.current;
    if (api === null || !isShell()) return;
    try {
      const svg = await exportToSvg({
        elements: api.getSceneElements(),
        appState: api.getAppState(),
        files: api.getFiles(),
        exportPadding: 12,
      });
      const path = await invoke<string | null>('doc_save', {
        content: new XMLSerializer().serializeToString(svg),
        defaultName: `${baseName}.svg`,
      });
      if (path !== null) toast.success(t('figure.exported'));
    } catch (e) {
      toast.error(`${t('figure.exportFailed')}: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  async function exportPng(): Promise<void> {
    const api = apiRef.current;
    if (api === null || !isShell()) return;
    try {
      const blob = await exportToBlob({
        elements: api.getSceneElements(),
        appState: api.getAppState(),
        files: api.getFiles(),
        mimeType: 'image/png',
        quality: 1,
        exportPadding: 12,
      });
      const path = await invoke<string | null>('save_image_as', {
        defaultName: `${baseName}.png`,
        base64: await blobToBase64(blob),
      });
      if (path !== null) toast.success(t('figure.exported'));
    } catch (e) {
      toast.error(`${t('figure.exportFailed')}: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  return (
    <div className="excalidraw-editor">
      <div className="figure-bar">
        <span className="figure-badge">
          {t('author.newExcalidraw')}
        </span>
        <span className="spacer" />
        {isShell() && (
          <>
            <button className="import-btn" disabled={!ready} onClick={() => void exportSvg()}>
              {t('figure.exportSvg')}
            </button>
            <button className="import-btn" disabled={!ready} onClick={() => void exportPng()}>
              {t('figure.exportPng')}
            </button>
          </>
        )}
      </div>
      <div className="excalidraw-host">
        <Excalidraw
          initialData={initialData}
          theme={dark ? 'dark' : 'light'}
          excalidrawAPI={(api) => {
            apiRef.current = api;
            setReady(true);
          }}
          onChange={onChange}
        />
      </div>
    </div>
  );
}
