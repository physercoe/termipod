import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { PersistQueryClientProvider } from '@tanstack/react-query-persist-client';
import { App } from './App';
import { localStoragePersister, persistMaxAge, queryClient } from './state/queryClient';
import './styles/app.css';

const root = document.getElementById('root');
if (root !== null) {
  createRoot(root).render(
    <StrictMode>
      <PersistQueryClientProvider
        client={queryClient}
        persistOptions={{ persister: localStoragePersister, maxAge: persistMaxAge }}
      >
        <App />
      </PersistQueryClientProvider>
    </StrictMode>,
  );
}
