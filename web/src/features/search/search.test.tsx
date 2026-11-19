import { describe, it, expect } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { Route, Routes } from 'react-router-dom';
import { renderWithProviders } from '../../test/test-utils';
import { SearchResultsView } from './SearchResultsView';
import { mockDb } from '../../mocks/db';

describe('Search Feature', () => {
  it('renders search results matching query in global scope', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/search" element={<SearchResultsView />} />
      </Routes>,
      { initialEntries: ['/search?q=prompting'] },
    );

    expect(screen.getByText('Hybrid Search')).toBeInTheDocument();

    await waitFor(() => {
      expect(screen.getByText(/agents\/prompting-guide\.md/i)).toBeInTheDocument();
    });

    expect(screen.getByText('bm25')).toBeInTheDocument();
  });

  it('renders search results scoped to a specific context', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/search" element={<SearchResultsView />} />
      </Routes>,
      { initialEntries: ['/search?q=sqlite-vec&context=core-system'] },
    );

    await waitFor(() => {
      expect(screen.getAllByText(/storage\/sqlite-vec\.md/i).length).toBeGreaterThan(0);
    });

    expect(screen.getByText('bm25')).toBeInTheDocument();
    expect(screen.getByText('vec')).toBeInTheDocument();
  });

  it('displays degraded notice when search engine is in degraded mode', async () => {
    mockDb.setDegradedSearch(true);

    renderWithProviders(
      <Routes>
        <Route path="/search" element={<SearchResultsView />} />
      </Routes>,
      { initialEntries: ['/search?q=prompting'] },
    );

    await waitFor(() => {
      expect(screen.getByText(/search degraded mode/i)).toBeInTheDocument();
    });

    expect(screen.getByText(/embedder temporarily unavailable/i)).toBeInTheDocument();

    mockDb.setDegradedSearch(false);
  });

  it('enforces that no_global cannot be unchecked when no context is selected', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/search" element={<SearchResultsView />} />
      </Routes>,
      { initialEntries: ['/search'] },
    );

    const globalSwitch = screen.getByLabelText(/include global context/i);
    expect(globalSwitch).toBeChecked();
    expect(globalSwitch).toBeDisabled();
  });
});
