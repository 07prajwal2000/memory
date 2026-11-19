import React from 'react';
import ReactDOM from 'react-dom/client';
import { RouterProvider } from 'react-router-dom';

import '@mantine/core/styles.css';
import '@mantine/notifications/styles.css';
import '@mantine/spotlight/styles.css';

import { AppProviders, router } from './app';
import { initTokenFromUrl } from './api';

async function prepareApp() {
  // Extract and store any ?token= from the URL query params once
  initTokenFromUrl();

  // Enable MSW in mock mode (npm run build:mock / dev mock)
  const isMock =
    import.meta.env.MODE === 'mock' ||
    import.meta.env.VITE_API_MODE === 'mock';

  if (isMock) {
    const { enableMocking } = await import('./mocks/browser');
    await enableMocking();
  }
}

prepareApp()
  .catch((err) => {
    console.error('Failed to prepare application:', err);
  })
  .finally(() => {
    ReactDOM.createRoot(document.getElementById('root')!).render(
      <React.StrictMode>
        <AppProviders>
          <RouterProvider router={router} />
        </AppProviders>
      </React.StrictMode>,
    );
  });
