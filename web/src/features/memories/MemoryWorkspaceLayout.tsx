import { useState, useCallback, useMemo } from 'react';
import { Box, Drawer, ActionIcon, Tooltip } from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { IconLayoutSidebarLeftExpand } from '@tabler/icons-react';
import { useParams, useNavigate, useLocation, Outlet } from 'react-router-dom';
import { useKeyboardShortcut } from '../../hooks';
import { ConfirmModal } from '../../components';
import { ResizablePane } from './ResizablePane';
import { MemoryPanel } from './MemoryPanel';
import {
  MemoryWorkspaceContext,
  safeGetLocalStorage,
  safeSetLocalStorage,
} from './MemoryWorkspaceContext';

export function MemoryWorkspaceLayout() {
  const { slug = 'global', id } = useParams<{ slug?: string; id?: string }>();
  const navigate = useNavigate();
  const location = useLocation();
  const isMobile = useMediaQuery('(max-width: 48em)');

  // Width persistence
  const [panelWidth, setPanelWidth] = useState<number>(() => {
    const saved = safeGetLocalStorage('mem_panel_width');
    const parsed = saved ? parseInt(saved, 10) : 320;
    return isNaN(parsed) ? 320 : Math.max(220, Math.min(parsed, 600));
  });

  // Collapsed persistence
  const [isCollapsed, setIsCollapsed] = useState<boolean>(() => {
    const saved = safeGetLocalStorage('mem_panel_collapsed');
    return saved === 'true';
  });

  // Mobile drawer state
  const [filesDrawerOpened, setFilesDrawerOpened] = useState(false);

  // Unsaved changes state
  const [isDirty, setIsDirty] = useState(false);
  const [pendingNav, setPendingNav] = useState<string | null>(null);
  const [discardOpened, setDiscardOpened] = useState(false);

  const toggleCollapse = useCallback(() => {
    setIsCollapsed((prev) => {
      const next = !prev;
      safeSetLocalStorage('mem_panel_collapsed', String(next));
      return next;
    });
  }, []);

  useKeyboardShortcut('b', toggleCollapse, { ctrlOrCmd: true, preventDefault: true });

  const requestNavigate = useCallback(
    (to: string) => {
      if (isDirty) {
        setPendingNav(to);
        setDiscardOpened(true);
      } else {
        navigate(to);
        setFilesDrawerOpened(false);
      }
    },
    [isDirty, navigate],
  );

  const handleConfirmDiscard = () => {
    setIsDirty(false);
    setDiscardOpened(false);
    if (pendingNav) {
      navigate(pendingNav);
      setPendingNav(null);
    }
    setFilesDrawerOpened(false);
  };

  const handleCancelDiscard = () => {
    setPendingNav(null);
    setDiscardOpened(false);
  };

  const contextValue = useMemo(
    () => ({
      isDirty,
      setIsDirty,
      requestNavigate,
      isFilesDrawerOpen: filesDrawerOpened,
      openFilesDrawer: () => setFilesDrawerOpened(true),
      closeFilesDrawer: () => setFilesDrawerOpened(false),
      isCollapsed,
      toggleCollapse,
      panelWidth,
      setPanelWidth,
    }),
    [isDirty, requestNavigate, filesDrawerOpened, isCollapsed, toggleCollapse, panelWidth],
  );

  const isEditorRoute = location.pathname.includes('/m/') || location.pathname.endsWith('/new');

  // Mobile rendering
  if (isMobile) {
    return (
      <MemoryWorkspaceContext.Provider value={contextValue}>
        <Box style={{ width: '100%', minHeight: 'calc(100vh - 56px)' }}>
          {isEditorRoute ? (
            <>
              <Outlet />
              <Drawer
                opened={filesDrawerOpened}
                onClose={() => setFilesDrawerOpened(false)}
                size="85%"
                padding="xs"
                title="Files"
              >
                <MemoryPanel
                  slug={slug}
                  activeId={id}
                  onSelectMemory={(selectedId) => {
                    requestNavigate(`/c/${slug}/m/${selectedId}`);
                  }}
                  onNewMemory={() => {
                    requestNavigate(`/c/${slug}/new`);
                  }}
                />
              </Drawer>
            </>
          ) : (
            <MemoryPanel
              slug={slug}
              activeId={id}
              onSelectMemory={(selectedId) => {
                requestNavigate(`/c/${slug}/m/${selectedId}`);
              }}
              onNewMemory={() => {
                requestNavigate(`/c/${slug}/new`);
              }}
            />
          )}

          <ConfirmModal
            opened={discardOpened}
            onClose={handleCancelDiscard}
            onConfirm={handleConfirmDiscard}
            title="Discard Unsaved Changes?"
            message="You have unsaved edits in this memory. Are you sure you want to navigate away and lose them?"
            confirmLabel="Discard & Leave"
            cancelLabel="Stay Here"
            color="yellow"
          />
        </Box>
      </MemoryWorkspaceContext.Provider>
    );
  }

  // Desktop/Tablet (>= 48em) two-pane persistent layout
  return (
    <MemoryWorkspaceContext.Provider value={contextValue}>
      <Box
        style={{
          display: 'flex',
          flexDirection: 'row',
          width: '100%',
          height: 'calc(100vh - 56px)',
          overflow: 'hidden',
          position: 'relative',
        }}
      >
        <ResizablePane
          width={panelWidth}
          isCollapsed={isCollapsed}
          onWidthChange={setPanelWidth}
          minWidth={220}
          maxWidth={Math.max(220, Math.floor(window.innerWidth * 0.5))}
        >
          <MemoryPanel
            slug={slug}
            activeId={id}
            showHeaderCollapse
            onCollapse={toggleCollapse}
            onSelectMemory={(selectedId) => {
              requestNavigate(`/c/${slug}/m/${selectedId}`);
            }}
            onNewMemory={() => {
              requestNavigate(`/c/${slug}/new`);
            }}
          />
        </ResizablePane>

        {/* Right pane: editor or empty state */}
        <Box
          style={{
            flex: 1,
            height: '100%',
            overflowY: 'auto',
            minWidth: 0,
            position: 'relative',
          }}
        >
          {isCollapsed && (
            <Box style={{ position: 'absolute', top: 10, left: 10, zIndex: 10 }}>
              <Tooltip label="Expand sidebar (Ctrl+B)">
                <ActionIcon
                  variant="default"
                  size="md"
                  onClick={toggleCollapse}
                  aria-label="Expand sidebar"
                >
                  <IconLayoutSidebarLeftExpand size={18} />
                </ActionIcon>
              </Tooltip>
            </Box>
          )}

          <Outlet />
        </Box>

        <ConfirmModal
          opened={discardOpened}
          onClose={handleCancelDiscard}
          onConfirm={handleConfirmDiscard}
          title="Discard Unsaved Changes?"
          message="You have unsaved edits in this memory. Are you sure you want to navigate away and lose them?"
          confirmLabel="Discard & Leave"
          cancelLabel="Stay Here"
          color="yellow"
        />
      </Box>
    </MemoryWorkspaceContext.Provider>
  );
}
