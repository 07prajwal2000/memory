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

  // Enable MSW only in explicit mock mode (npm run build:mock / dev mock)
  if (import.meta.env.MODE === 'mock') {
    const { enableMocking } = await import('./mocks/browser');
    await enableMocking();
  }
}

prepareApp().then(() => {
  ReactDOM.createRoot(document.getElementById('root')!).render(
    <React.StrictMode>
      <AppProviders>
        <RouterProvider router={router} />
      </AppProviders>
    </React.StrictMode>,
  );
});
