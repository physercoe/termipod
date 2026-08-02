import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { PersistQueryClientProvider } from '@tanstack/react-query-persist-client';
import { armExport, importStateIfFresh } from './migration/state';
import { installNativeContextMenu } from './nativeContextMenu';
import '@fontsource-variable/inter/index.css';
import '@fontsource-variable/jetbrains-mono/index.css';
import 'katex/dist/katex.min.css';
import './styles/app.css';

// Migration boot (ADR-055 M0): on a fresh native profile restore the exported
// localStorage snapshot BEFORE the query persister (and the app) read it; a
// no-op under Tauri (keys already present) and in the browser build. Then arm
// the ongoing export so the next boot / the Electron cutover has fresh state.
//
// The app graph (App, state/queryClient) is imported dynamically, AFTER the
// restore: several state modules write termipod.* keys at module scope during
// import (e.g. documents.ts's store creation runs migrateCanvas, which sets
// termipod.canvas.migrated), and the query persister reads localStorage at
// creation. Statically importing them first made the fresh-profile check in
// importStateIfFresh always see a populated profile, so the first-boot restore
// never fired and the snapshot was then overwritten by the export (#351).
async function boot(): Promise<void> {
  await importStateIfFresh();
  const [{ App }, { localStoragePersister, persistMaxAge, queryClient, shouldPersistQuery }] = await Promise.all([
    import('./App'),
    import('./state/queryClient'),
  ]);
  const root = document.getElementById('root');
  if (root !== null) {
    createRoot(root).render(
      <StrictMode>
        <PersistQueryClientProvider
          client={queryClient}
          persistOptions={{
            persister: localStoragePersister,
            maxAge: persistMaxAge,
            // Drop the whole snapshot when the app version changes (schema drift),
            // and never serialize the heavy blob/document/transcript payloads.
            buster: __APP_VERSION__,
            dehydrateOptions: { shouldDehydrateQuery: shouldPersistQuery },
          }}
        >
          <App />
        </PersistQueryClientProvider>
      </StrictMode>,
    );
  }
  armExport();
  // Chromium/Electron has no default right-click menu; add a native
  // Cut/Copy/Paste fallback for surfaces without their own (electron-only).
  installNativeContextMenu();
  // Push the persisted browser-bridge toggle to the main process (no-op when
  // off — the default). Imported dynamically with the app graph so its store
  // reads localStorage AFTER the migration restore above; the main side owns
  // the server + discovery file. The hub-context push feeds the W2 audit
  // mirror (re-pushed automatically on profile switch via the store's
  // useSession subscription).
  const { syncBrowserBridgeToMain, pushBridgeHubContext } = await import('./state/browserBridge');
  syncBrowserBridgeToMain();
  pushBridgeHubContext();
  // Push the persisted UI-context sharing toggle (D1) — main reconciles the
  // user-level ~/.kimi-code/mcp.json entry (this doubles as the per-start
  // refresh of the stable relay copy) and opens the focus-cache push. No-op
  // when off — the default.
  const { syncUiContextToMain } = await import('./state/uiContext');
  syncUiContextToMain();
  // D6: listen for agent-pointing orders. Independent of the toggle above —
  // main only ever pushes one while sharing is on, and a listener with nothing
  // to hear costs nothing.
  const { initAgentHighlights } = await import('./state/agentHighlight');
  initAgentHighlights();
  // Coworking A2: answer main's author-bridge requests (author_read /
  // author_apply). Same posture as the highlight listener — main only pushes
  // while sharing is on, and the subscription is also the signal main checks
  // before parking on a reply, so it is registered unconditionally.
  const { initAuthorBridge } = await import('./state/authorBridgeHost');
  initAuthorBridge();
}

void boot();
