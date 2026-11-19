import { createBrowserRouter, Navigate } from 'react-router-dom';
import { AppShell } from './AppShell';
import { RouteErrorBoundary } from './ErrorBoundary';
import { MemoryListView, MemoryEditorView } from '../features/memories';
import { DefaultFileView } from '../features/default-file';
import { SearchResultsView } from '../features/search';
import { StatusView } from '../features/status';

export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppShell />,
    errorElement: <RouteErrorBoundary />,
    children: [
      {
        index: true,
        element: <Navigate to="/c/global" replace />,
      },
      {
        path: 'c/:slug',
        element: <MemoryListView />,
      },
      {
        path: 'c/:slug/new',
        element: <MemoryEditorView />,
      },
      {
        path: 'c/:slug/m/:id',
        element: <MemoryEditorView />,
      },
      {
        path: 'default',
        element: <DefaultFileView />,
      },
      {
        path: 'search',
        element: <SearchResultsView />,
      },
      {
        path: 'status',
        element: <StatusView />,
      },
      {
        path: '*',
        element: <Navigate to="/c/global" replace />,
      },
    ],
  },
]);
