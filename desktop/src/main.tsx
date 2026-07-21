import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { PersistQueryClientProvider } from '@tanstack/react-query-persist-client';
import { App } from './App';
import { armExport, importStateIfFresh } from './migration/state';
import { localStoragePersister, persistMaxAge, queryClient, shouldPersistQuery } from './state/queryClient';
import '@fontsource-variable/inter/index.css';
import '@fontsource-variable/jetbrains-mono/index.css';
import 'katex/dist/katex.min.css';
import './styles/app.css';

// Migration boot (ADR-055 M0): on a fresh native profile restore the exported
// localStorage snapshot BEFORE the query persister (and the app) read it; a
// no-op under Tauri (keys already present) and in the browser build. Then arm
// the ongoing export so the next boot / the Electron cutover has fresh state.
async function boot(): Promise<void> {
  await importStateIfFresh();
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
}

void boot();
