import { describe, it, expect } from 'vitest';
import { screen, waitFor, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Route, Routes } from 'react-router-dom';
import { renderWithProviders } from '../../test/test-utils';
import { MemoryListView } from './MemoryListView';
import { MemoryEditorView } from './MemoryEditorView';
import { DefaultFileView } from '../default-file';
import { mockDb } from '../../mocks/db';

describe('Memories Feature', () => {
  describe('MemoryListView', () => {
    it('renders memory list and allows toggling tree and flat views', async () => {
      renderWithProviders(
        <Routes>
          <Route path="/c/:slug" element={<MemoryListView />} />
        </Routes>,
        { initialEntries: ['/c/core-system'] },
      );

      await waitFor(() => {
        expect(screen.getByRole('heading', { level: 2, name: 'core-system' })).toBeInTheDocument();
        expect(screen.getByText('storage/')).toBeInTheDocument();
      });

      // Switch to flat view
      const flatTab = screen.getByText('Flat');
      fireEvent.click(flatTab);

      await waitFor(() => {
        expect(screen.getByText('storage/engine.md')).toBeInTheDocument();
      });
    });

    it('filters memories by name query', async () => {
      renderWithProviders(
        <Routes>
          <Route path="/c/:slug" element={<MemoryListView />} />
        </Routes>,
        { initialEntries: ['/c/core-system'] },
      );

      await waitFor(() => {
        expect(screen.getByRole('heading', { level: 2, name: 'core-system' })).toBeInTheDocument();
      });

      const searchInput = screen.getByPlaceholderText(/filter memories by name/i);
      fireEvent.change(searchInput, { target: { value: 'sqlite-vec' } });

      await waitFor(() => {
        expect(screen.getByText('sqlite-vec.md')).toBeInTheDocument();
      });
    });
  });

  describe('MemoryEditorView & Conflict Flow End-to-End', () => {
    it('saves edits to an existing memory', async () => {
      const user = userEvent.setup();
      renderWithProviders(
        <Routes>
          <Route path="/c/:slug/m/:id" element={<MemoryEditorView />} />
        </Routes>,
        { initialEntries: ['/c/core-system/m/mem_core_000'] },
      );

      await waitFor(() => {
        expect(screen.getByDisplayValue(/storage\/engine\.md/i)).toBeInTheDocument();
      });

      const saveBtn = screen.getByRole('button', { name: /save/i });
      await user.click(saveBtn);

      await waitFor(() => {
        expect(screen.getByText('Memory saved')).toBeInTheDocument();
      });
    });

    it('detects version conflict (409) and presents resolution dialog', async () => {
      const user = userEvent.setup();
      renderWithProviders(
        <Routes>
          <Route path="/c/:slug/m/:id" element={<MemoryEditorView />} />
        </Routes>,
        { initialEntries: ['/c/core-system/m/mem_core_001'] },
      );

      await waitFor(() => {
        expect(screen.getByDisplayValue(/storage\/sqlite-vec\.md/i)).toBeInTheDocument();
      });

      // Simulate a concurrent edit by another agent incrementing the server version
      const current = mockDb.getMemory({ context: 'core-system', id: 'mem_core_001' });
      mockDb.upsertMemory({
        context: 'core-system',
        id: 'mem_core_001',
        name: 'storage/sqlite-vec.md',
        body: '# Concurrent edit from another agent',
        if_version: current.version,
      });

      // Attempt to save from this editor (which holds stale version)
      const saveBtn = screen.getByRole('button', { name: /save/i });
      await user.click(saveBtn);

      // Verify conflict dialog opens
      await waitFor(() => {
        expect(screen.getByText(/version conflict detected/i)).toBeInTheDocument();
      });

      expect(screen.getByText(/another client or agent has modified this memory/i)).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /reload theirs/i })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /overwrite with mine/i })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /copy my text/i })).toBeInTheDocument();

      // Test Reload theirs action
      const reloadBtn = screen.getByRole('button', { name: /reload theirs/i });
      await user.click(reloadBtn);

      await waitFor(() => {
        expect(screen.queryByText(/version conflict detected/i)).not.toBeInTheDocument();
      });
    });
  });

  describe('Default file protection', () => {
    it('renders default file and does not show delete action', async () => {
      renderWithProviders(<DefaultFileView />);

      await waitFor(() => {
        expect(screen.getByText('Default File')).toBeInTheDocument();
      });

      expect(screen.getByText('global/readme.md')).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: /^delete$/i })).not.toBeInTheDocument();
    });
  });
});
