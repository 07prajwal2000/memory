import { describe, it, expect, vi, beforeEach } from 'vitest';
import { screen, waitFor, fireEvent, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Route, Routes } from 'react-router-dom';
import { renderWithProviders } from '../../test/test-utils';
import { MemoryWorkspaceLayout } from './MemoryWorkspaceLayout';
import { MemoryEmptyDetailView } from './MemoryEmptyDetailView';
import { MemoryEditorView } from './MemoryEditorView';
import { MemoryListView } from './MemoryListView';
import { DefaultFileView } from '../default-file';
import { NotFoundView } from '../../app/NotFoundView';
import { mockDb } from '../../mocks/db';

function renderWorkspaceRoutes(initialEntry = '/c/core-system') {
  return renderWithProviders(
    <Routes>
      <Route path="/c/:slug" element={<MemoryWorkspaceLayout />}>
        <Route index element={<MemoryEmptyDetailView />} />
        <Route path="m/:id" element={<MemoryEditorView />} />
        <Route path="new" element={<MemoryEditorView />} />
      </Route>
    </Routes>,
    { initialEntries: [initialEntry] },
  );
}

describe('Memories Feature', () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
  });

  describe('MemoryPanel & ViewModeToggle', () => {
    it('renders memory list and allows toggling tree and flat views with accessible names', async () => {
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

      // Accessible toggle radio buttons
      const treeRadio = screen.getByRole('radio', { name: /tree view/i });
      const flatRadio = screen.getByRole('radio', { name: /flat list/i });
      expect(treeRadio).toBeInTheDocument();
      expect(flatRadio).toBeInTheDocument();
      expect(treeRadio).toBeChecked();

      // Switch to flat view
      fireEvent.click(flatRadio);

      await waitFor(() => {
        expect(screen.getByText('storage/engine.md')).toBeInTheDocument();
      });

      expect(localStorage.getItem('mem_view_mode')).toBe('flat');
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

  describe('Desktop Persistent Two-Pane Workspace', () => {
    it('renders panel visible in editor route on desktop and shows memory content', async () => {
      renderWorkspaceRoutes('/c/core-system/m/mem_core_000');

      // Left panel is visible
      await waitFor(() => {
        expect(screen.getByRole('heading', { level: 2, name: 'core-system' })).toBeInTheDocument();
        expect(screen.getByText('storage/')).toBeInTheDocument();
      });

      // Right editor pane is visible
      await waitFor(() => {
        expect(screen.getByDisplayValue(/storage\/engine\.md/i)).toBeInTheDocument();
      });
    });

    it('selecting another memory navigates and keeps panel mounted', async () => {
      renderWorkspaceRoutes('/c/core-system/m/mem_core_000');

      await waitFor(() => {
        expect(screen.getByDisplayValue(/storage\/engine\.md/i)).toBeInTheDocument();
      });

      // Find another file in the tree
      const nextFile = screen.getByRole('button', { name: /sqlite-vec\.md/i });
      fireEvent.click(nextFile);

      await waitFor(() => {
        expect(screen.getByDisplayValue(/storage\/sqlite-vec\.md/i)).toBeInTheDocument();
      });

      // Panel remains mounted
      expect(screen.getByRole('heading', { level: 2, name: 'core-system' })).toBeInTheDocument();
    });

    it('highlights selected item and expands ancestors for a nested name', async () => {
      // Add a nested memory to mockDb
      const created = mockDb.upsertMemory({
        context: 'core-system',
        name: 'deep/nested/item.md',
        body: '# Deep nested documentation',
      });

      renderWorkspaceRoutes(`/c/core-system/m/${created.id}`);

      await waitFor(() => {
        expect(screen.getByDisplayValue(/deep\/nested\/item\.md/i)).toBeInTheDocument();
      });

      // Both ancestor folders 'deep/' and 'nested/' should be expanded automatically
      await waitFor(() => {
        expect(screen.getByText('deep/')).toBeInTheDocument();
        expect(screen.getByText('nested/')).toBeInTheDocument();
        const itemBtn = screen.getByRole('button', { name: /item\.md/i });
        expect(itemBtn).toBeInTheDocument();
        expect(itemBtn).toHaveAttribute('aria-selected', 'true');
      });
    });

    it('dirty guard on switching: shows confirmation dialog, cancels without changing, confirms to navigate', async () => {
      const user = userEvent.setup();
      renderWorkspaceRoutes('/c/core-system/m/mem_core_000');

      await waitFor(() => {
        expect(screen.getByDisplayValue(/storage\/engine\.md/i)).toBeInTheDocument();
      });

      // Make editor dirty by typing in the name input
      const nameInput = screen.getByDisplayValue(/storage\/engine\.md/i);
      await user.type(nameInput, '-dirty-edit');

      // Attempt to select another memory from the tree
      const otherFile = screen.getByRole('button', { name: /sqlite-vec\.md/i });
      fireEvent.click(otherFile);

      // Discard confirmation dialog appears
      await waitFor(() => {
        expect(screen.getByText(/discard unsaved changes\?/i)).toBeInTheDocument();
      });

      // 1. Cancel discard: click "Stay Here"
      const stayBtn = screen.getByRole('button', { name: /stay here/i });
      fireEvent.click(stayBtn);

      await waitFor(() => {
        expect(screen.queryByText(/discard unsaved changes\?/i)).not.toBeInTheDocument();
      });

      // Still on the original memory with dirty changes intact
      expect(screen.getByDisplayValue(/storage\/engine\.md-dirty-edit/i)).toBeInTheDocument();

      // 2. Click another memory again and this time confirm "Discard & Leave"
      fireEvent.click(otherFile);
      await waitFor(() => {
        expect(screen.getByText(/discard unsaved changes\?/i)).toBeInTheDocument();
      });

      const discardBtn = screen.getByRole('button', { name: /discard & leave/i });
      fireEvent.click(discardBtn);

      await waitFor(() => {
        expect(screen.queryByText(/discard unsaved changes\?/i)).not.toBeInTheDocument();
        expect(screen.getByDisplayValue(/storage\/sqlite-vec\.md/i)).toBeInTheDocument();
      });
    });

    it('resizing via keyboard persists width in localStorage', async () => {
      renderWorkspaceRoutes('/c/core-system');

      await waitFor(() => {
        expect(screen.getByRole('separator', { name: /resize memories panel/i })).toBeInTheDocument();
      });

      const separator = screen.getByRole('separator', { name: /resize memories panel/i });
      expect(separator).toHaveAttribute('aria-valuenow', '320');

      // Press ArrowRight to expand by 10
      fireEvent.keyDown(separator, { key: 'ArrowRight' });

      await waitFor(() => {
        expect(separator).toHaveAttribute('aria-valuenow', '330');
      });
      expect(localStorage.getItem('mem_panel_width')).toBe('330');

      // Press ArrowLeft to shrink by 10
      fireEvent.keyDown(separator, { key: 'ArrowLeft' });
      await waitFor(() => {
        expect(separator).toHaveAttribute('aria-valuenow', '320');
      });
      expect(localStorage.getItem('mem_panel_width')).toBe('320');
    });

    it('collapse and expand persists in localStorage and toggles via button or keyboard', async () => {
      renderWorkspaceRoutes('/c/core-system');

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /collapse sidebar/i })).toBeInTheDocument();
      });

      // Collapse
      const collapseBtn = screen.getByRole('button', { name: /collapse sidebar/i });
      fireEvent.click(collapseBtn);

      await waitFor(() => {
        expect(localStorage.getItem('mem_panel_collapsed')).toBe('true');
        expect(screen.getByRole('button', { name: /expand sidebar/i })).toBeInTheDocument();
      });

      // Expand
      const expandBtn = screen.getByRole('button', { name: /expand sidebar/i });
      fireEvent.click(expandBtn);

      await waitFor(() => {
        expect(localStorage.getItem('mem_panel_collapsed')).toBe('false');
        expect(screen.getByRole('button', { name: /collapse sidebar/i })).toBeInTheDocument();
      });
    });

    it('storage failures do not crash the application', async () => {
      const originalGetItem = localStorage.getItem;
      const originalSetItem = localStorage.setItem;

      vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
        throw new Error('Storage disabled / quota exceeded');
      });
      vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
        throw new Error('Storage write failed');
      });

      expect(() => {
        renderWorkspaceRoutes('/c/core-system');
      }).not.toThrow();

      await waitFor(() => {
        expect(screen.getByRole('heading', { level: 2, name: 'core-system' })).toBeInTheDocument();
      });

      Storage.prototype.getItem = originalGetItem;
      Storage.prototype.setItem = originalSetItem;
    });
  });

  describe('Mobile Layout & Files Drawer', () => {
    it('mobile editor renders Files button which opens drawer containing the memory panel', async () => {
      const origMatchMedia = window.matchMedia;
      window.matchMedia = vi.fn().mockImplementation((query: string) => ({
        matches: query.includes('max-width: 48em'),
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }));

      renderWorkspaceRoutes('/c/core-system/m/mem_core_000');

      await waitFor(() => {
        expect(screen.getByDisplayValue(/storage\/engine\.md/i)).toBeInTheDocument();
      });

      // In mobile editor, "Files" button is rendered
      const filesBtn = screen.getByRole('button', { name: /files/i });
      expect(filesBtn).toBeInTheDocument();

      // Click Files to open drawer
      fireEvent.click(filesBtn);

      await waitFor(() => {
        // Drawer opens and contains memory list
        const drawer = screen.getByRole('dialog', { name: /files/i });
        expect(drawer).toBeInTheDocument();
        expect(within(drawer).getByText('storage/')).toBeInTheDocument();
        expect(within(drawer).getByText('engine.md')).toBeInTheDocument();
      });

      window.matchMedia = origMatchMedia;
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

  describe('Deep Link & Hard Refresh Direct Mount Regression Tests', () => {
    function renderAppRoutes(initialEntry: string) {
      return renderWithProviders(
        <Routes>
          <Route path="/c/:slug" element={<MemoryWorkspaceLayout />}>
            <Route index element={<MemoryEmptyDetailView />} />
            <Route path="m/:id" element={<MemoryEditorView />} />
            <Route path="new" element={<MemoryEditorView />} />
          </Route>
          <Route path="/default" element={<DefaultFileView />} />
          <Route path="/default-file" element={<DefaultFileView />} />
          <Route path="*" element={<NotFoundView />} />
        </Routes>,
        { initialEntries: [initialEntry] },
      );
    }

    it('mounts directly at /c/core-system/m/mem_core_000 and renders document without list preloading', async () => {
      renderAppRoutes('/c/core-system/m/mem_core_000');

      await waitFor(() => {
        expect(screen.getByDisplayValue(/storage\/engine\.md/i)).toBeInTheDocument();
      });
      expect(screen.getByText('storage/')).toBeInTheDocument();
    });

    it('mounts directly at /c/global/m/mem_glob_pm7looc and renders document on fresh mount', async () => {
      renderAppRoutes('/c/global/m/mem_glob_pm7looc');

      await waitFor(() => {
        expect(screen.getByDisplayValue(/agents\/error-conventions\.md/i)).toBeInTheDocument();
      });
    });

    it('mounts directly at /c/global/m/mem_core_000 (mismatched context) and presents clear not-found state', async () => {
      renderAppRoutes('/c/global/m/mem_core_000');

      await waitFor(() => {
        expect(screen.getByRole('heading', { level: 4, name: /memory not found/i })).toBeInTheDocument();
        expect(screen.getByText(/does not belong to context "global"/i)).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /back to context/i })).toBeInTheDocument();
      });
    });

    it('mounts directly at /c/core-system/new and renders editor for new document', async () => {
      renderAppRoutes('/c/core-system/new');

      await waitFor(() => {
        expect(screen.getByText(/new memory/i)).toBeInTheDocument();
        expect(screen.getByPlaceholderText(/e\.g\. guidelines\/coding-standards\.md/i)).toBeInTheDocument();
      });
    });

    it('mounts directly at /default-file and renders default file', async () => {
      renderAppRoutes('/default-file');

      await waitFor(() => {
        expect(screen.getByRole('heading', { level: 2, name: /default file/i })).toBeInTheDocument();
        expect(screen.getByText('global/readme.md')).toBeInTheDocument();
      });
    });

    it('mounts directly at an unknown route and renders 404 page', async () => {
      renderAppRoutes('/unknown-route-123');

      await waitFor(() => {
        expect(screen.getByRole('heading', { level: 4, name: /page not found \(404\)/i })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /back to global context/i })).toBeInTheDocument();
      });
    });

    it('mounts directly at an unknown context and redirects to global with notification', async () => {
      renderAppRoutes('/c/unknown-context-xyz');

      await waitFor(() => {
        expect(screen.getByRole('heading', { level: 2, name: 'global' })).toBeInTheDocument();
      });
    });
  });
});
