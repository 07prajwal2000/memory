import { describe, it, expect, beforeEach } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Route, Routes } from 'react-router-dom';
import { renderWithProviders } from '../../test/test-utils';
import { SearchResultsView } from './SearchResultsView';
import { mockDb } from '../../mocks/db';

describe('Search Feature', () => {
  beforeEach(() => {
    localStorage.clear();
  });
  it('renders search results matching query in global scope', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/search" element={<SearchResultsView />} />
      </Routes>,
      { initialEntries: ['/search?q=prompting'] },
    );

    expect(screen.getByText('Hybrid Search')).toBeInTheDocument();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /agents\/prompting-guide\.md/i })).toBeInTheDocument();
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
      expect(screen.getByRole('button', { name: /storage\/sqlite-vec\.md/i })).toBeInTheDocument();
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

  it('finds document with a partial word (prefix match)', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/search" element={<SearchResultsView />} />
      </Routes>,
      { initialEntries: ['/search?q=promp'] },
    );

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /agents\/prompting-guide\.md/i })).toBeInTheDocument();
    });

    expect(screen.getAllByText('fuzzy').length).toBeGreaterThan(0);
  });

  it('finds document with a typo (typo tolerance)', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/search" element={<SearchResultsView />} />
      </Routes>,
      { initialEntries: ['/search?q=tokn'] },
    );

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /guidelines\/security\.md/i })).toBeInTheDocument();
    });

    expect(screen.getAllByText('fuzzy').length).toBeGreaterThan(0);
  });

  it('switching fuzzy off removes fuzzy-only hits', async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <Routes>
        <Route path="/search" element={<SearchResultsView />} />
      </Routes>,
      { initialEntries: ['/search?q=tokn'] },
    );

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /guidelines\/security\.md/i })).toBeInTheDocument();
    });

    const fuzzySwitch = screen.getByLabelText(/^fuzzy$/i);
    expect(fuzzySwitch).toBeChecked();

    await user.click(fuzzySwitch);

    await waitFor(() => {
      expect(screen.getByText(/no results for "tokn"/i)).toBeInTheDocument();
    });
  });

  it('renders highlights on matched terms', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/search" element={<SearchResultsView />} />
      </Routes>,
      { initialEntries: ['/search?q=tokn'] },
    );

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /guidelines\/security\.md/i })).toBeInTheDocument();
    });

    // Check that <mark> elements are rendered for the matched terms
    const marks = document.querySelectorAll('mark');
    expect(marks.length).toBeGreaterThan(0);
  });
});
