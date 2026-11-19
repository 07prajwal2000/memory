import React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MantineProvider } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { ModalsProvider } from '@mantine/modals';
import { theme } from '../theme';
import { AppErrorBoundary } from './ErrorBoundary';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (failureCount, error) => {
        // Do not retry 404, 403, 401, 409
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const status = (error as any)?.status;
        if (status === 404 || status === 403 || status === 401 || status === 409) {
          return false;
        }
        return failureCount < 2;
      },
      staleTime: 5000,
      refetchOnWindowFocus: false,
    },
  },
});

export function AppProviders({ children }: { children: React.ReactNode }) {
  return (
    <AppErrorBoundary>
      <QueryClientProvider client={queryClient}>
        <MantineProvider theme={theme} defaultColorScheme="dark">
          <ModalsProvider>
            <Notifications position="top-right" zIndex={2000} />
            {children}
          </ModalsProvider>
        </MantineProvider>
      </QueryClientProvider>
    </AppErrorBoundary>
  );
}
