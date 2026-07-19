import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { PersistQueryClientProvider } from '@tanstack/react-query-persist-client';
import { App } from './App';
import { localStoragePersister, persistMaxAge, queryClient, shouldPersistQuery } from './state/queryClient';
import '@fontsource-variable/inter/index.css';
import '@fontsource-variable/jetbrains-mono/index.css';
import 'katex/dist/katex.min.css';
import './styles/app.css';

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
