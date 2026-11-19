import React from 'react';
import { render, RenderOptions } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MantineProvider } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { ModalsProvider } from '@mantine/modals';
import { MemoryRouter } from 'react-router-dom';
import { theme } from '../theme';

export function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        gcTime: Infinity,
      },
      mutations: {
        retry: false,
      },
    },
  });
}

export function createTestWrapper(initialEntries = ['/']) {
  const queryClient = createTestQueryClient();

  return function TestWrapper({ children }: { children: React.ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>
        <MantineProvider theme={theme} defaultColorScheme="dark">
          <ModalsProvider>
            <Notifications />
            <MemoryRouter initialEntries={initialEntries}>{children}</MemoryRouter>
          </ModalsProvider>
        </MantineProvider>
      </QueryClientProvider>
    );
  };
}

export function renderWithProviders(
  ui: React.ReactElement,
  options?: Omit<RenderOptions, 'wrapper'> & { initialEntries?: string[] },
) {
  const { initialEntries, ...renderOptions } = options || {};
  return render(ui, {
    wrapper: createTestWrapper(initialEntries),
    ...renderOptions,
  });
}

export * from '@testing-library/react';
