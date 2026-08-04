import { useEffect, useMemo, useRef, useState } from 'react';
import {
  CaptureUpdateAction,
  Excalidraw,
  exportToBlob,
  exportToSvg,
  getSceneVersion,
  serializeAsJSON,
} from '@excalidraw/excalidraw';
import '@excalidraw/excalidraw/index.css';
import { invoke } from '../bridge';
import { useT } from '../i18n';
import { isShell } from '../platform';
import { useTheme } from '../state/theme';
import { toast } from '../state/toast';
import { useDocuments, type Doc } from '../state/documents';
import { openScene, parseExcalidrawScene } from '../state/excalidrawScene';
import { registerLiveApply } from '../state/liveApply';
import { Icon } from '../ui/Icon';

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
/// (new) doc and one we could not read both load nothing — but they are not the
/// same document, and only `openScene` tells them apart (see `unreadable`
/// below). The cast is where the structural shapes from `state/excalidrawScene`
/// meet the vendor's types; that module stays free of the Excalidraw import so
/// `node --test` can cover the parse.
function toInitialData(body: string): SceneData | null {
  const scene = parseExcalidrawScene(body);
  if (scene === null) return null;
  return {
    elements: scene.elements as ChangeArgs[0],
    // Through `unknown` because `SceneData` borrows the FULL `AppState` from
    // `onChange`, while `initialData` only ever receives a partial one — a
    // stored appState is a handful of persisted keys, never all ninety.
    appState: scene.appState as unknown as ChangeArgs[1],
    files: scene.files as ChangeArgs[2],
  };
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
  // A non-empty body that is not a scene opens READ-ONLY — the A5 rule, which
  // this editor needed just as much as the table grid did. `kindForFile` sends
  // every `.excalidraw` file here on its extension alone, so a corrupt or
  // foreign one used to open as a blank canvas and the first `onChange`
  // serialized that blank over it. Persistence is suppressed below and the
  // exports are disabled, so nothing this session can overwrite the bytes.
  const unreadable = useMemo(() => openScene(doc.body).state === 'unreadable', [doc.id]); // eslint-disable-line react-hooks/exhaustive-deps
  const baseName = (doc.title !== '' ? doc.title : 'sketch').replace(/\.[^.]+$/, '').replace(/[^\w.-]+/g, '-');

  // Persist is debounced: `onChange` fires on every pointer move while drawing,
  // and `serializeAsJSON` over a large scene per event is a main-thread stall.
  // Skip emits that don't change the scene version (Excalidraw re-emits on
  // mount + font-load reflow with the loaded scene — those must not dirty the
  // doc). The trailing write flushes on unmount so the last stroke survives.
  const lastVersion = useRef<number>(initialSceneVersion(doc.body));
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const flush = useRef<(() => void) | undefined>(undefined);

  // Consume-and-clear: a flush runs at most once, so the unmount flush only
  // fires when a debounced write is still pending — re-running an already-
  // flushed write would re-dirty a doc the user just saved (identical body).
  function runFlush(): void {
    const f = flush.current;
    flush.current = undefined;
    f?.();
  }

  useEffect(() => {
    return () => {
      if (timer.current !== undefined) clearTimeout(timer.current);
      runFlush();
    };
  }, [doc.id]); // eslint-disable-line react-hooks/exhaustive-deps

  // B3: the live-apply target. `<Excalidraw>` reads `initialData` once and owns
  // the scene from then on, so writing `doc.body` alone leaves the user looking
  // at the pre-write drawing while the store holds the agent's — the tool would
  // report a change nobody can see.
  //
  // Three rules, the same three B2 gives the canvas, in this vendor's terms:
  //
  //   1. `captureUpdate: IMMEDIATELY`, so **Cmd+Z undoes the agent**. The
  //      parameter's default is `EVENTUALLY`, which does not put the update on
  //      the undo stack as its own step — the user would be left with an
  //      agent's drawing and no keystroke back to theirs. This is also the only
  //      route back to strokes made inside the last debounce window, which the
  //      store never saw.
  //   2. Refuse a body that is not a scene. `validateAuthorBody` refuses the
  //      same class one layer up on the agent path, so this is the editor's own
  //      contract rather than that check's twin — and it is what keeps
  //      `updateScene` from being handed a null.
  //   3. Refuse while the document is unreadable. A user who is looking at a
  //      read-only notice because we could not parse their file must not have
  //      an agent write over the bytes underneath it.
  //
  // `addFiles` runs BEFORE `updateScene` on purpose: it is the call that can
  // throw on a malformed entry, and a throw after the scene changed would leave
  // the screen holding a body the store refuses — the editor showing one
  // document and `doc.body` another.
  //
  // `appState` is deliberately NOT applied. It is presentation — scroll, zoom,
  // the active tool — so pushing the agent's would move the user's camera as a
  // side effect of an edit they can see perfectly well from where they are.
  // The next `onChange` serializes the user's own appState back, so the stored
  // document keeps their view, not the agent's.
  useEffect(() => {
    return registerLiveApply(doc.id, (body) => {
      const api = apiRef.current;
      if (api === null || unreadable) return 'rejected';
      const scene = parseExcalidrawScene(body);
      if (scene === null) return 'rejected';
      const files = scene.files === undefined ? [] : Object.values(scene.files);
      if (files.length > 0) api.addFiles(files as Parameters<ExcalidrawAPI['addFiles']>[0]);
      api.updateScene({
        elements: scene.elements as Parameters<ExcalidrawAPI['updateScene']>[0]['elements'],
        captureUpdate: CaptureUpdateAction.IMMEDIATELY,
      });
      return 'applied_live';
    });
  }, [doc.id, unreadable]);

  function onChange(...[elements, appState, files]: ChangeArgs): void {
    // What is on screen is a blank canvas, not the document — writing it back
    // is the loss this flag exists to prevent.
    if (unreadable) return;
    const version = getSceneVersion(elements);
    if (version === lastVersion.current) return;
    lastVersion.current = version;
    flush.current = () => update(doc.id, { body: serializeAsJSON(elements, appState, files, 'local') });
    if (timer.current !== undefined) clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      timer.current = undefined;
      runFlush();
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
            {/* Exporting an unreadable document would write a blank SVG/PNG
                under the document's own name — a quiet lie about what the file
                contains, and the one export path that leaves the app. */}
            <button className="import-btn" disabled={!ready || unreadable} onClick={() => void exportSvg()}>
              {t('figure.exportSvg')}
            </button>
            <button className="import-btn" disabled={!ready || unreadable} onClick={() => void exportPng()}>
              {t('figure.exportPng')}
            </button>
          </>
        )}
      </div>
      {/* Say plainly that this is not the document — the wording TableEditor
          uses for the same situation. A blank canvas with no explanation reads
          as "my drawing is gone", which is what the old behaviour then made
          true on the first stroke. */}
      {unreadable && (
        <div className="doc-unreadable">
          <Icon name="alert" size={13} /> {t('excalidraw.unreadable')}
        </div>
      )}
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
