import { useState, useMemo, useEffect, useCallback } from 'react';
import {
  Stack,
  Group,
  TextInput,
  Select,
  Button,
  Pagination,
  Text,
  Skeleton,
  Paper,
  Box,
  Title,
  Breadcrumbs,
  Anchor,
  ActionIcon,
  Tooltip,
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import {
  IconSearch,
  IconPlus,
  IconNote,
  IconLayoutSidebarLeftCollapse,
} from '@tabler/icons-react';
import { useNavigate } from 'react-router-dom';
import { useMemoriesList, useContextDetail } from '../../api';
import { useDebounce } from '../../hooks';
import {
  buildMemoryTree,
  getAncestorFolderPaths,
  getAllFolderPaths,
} from './treeUtils';
import { MemoryTreeItem } from './MemoryTreeItem';
import { MemoryFlatItem } from './MemoryFlatItem';
import { ViewModeToggle, ViewMode } from './ViewModeToggle';
import {
  useMemoryWorkspace,
  safeGetLocalStorage,
  safeSetLocalStorage,
  safeGetSessionStorage,
  safeSetSessionStorage,
} from './MemoryWorkspaceContext';
import { EmptyState, ErrorState } from '../../components';

const PAGE_SIZE = 25;

export interface MemoryPanelProps {
  slug: string;
  activeId?: string;
  onSelectMemory?: (id: string) => void;
  onNewMemory?: () => void;
  onCollapse?: () => void;
  showHeaderCollapse?: boolean;
}

export function MemoryPanel({
  slug,
  activeId,
  onSelectMemory,
  onNewMemory,
  onCollapse,
  showHeaderCollapse = false,
}: MemoryPanelProps) {
  const navigate = useNavigate();
  const workspace = useMemoryWorkspace();

  const [rawSearch, setRawSearch] = useState('');
  const debouncedSearch = useDebounce(rawSearch, 300);
  const [sort, setSort] = useState<'name' | 'updated'>('name');
  const [viewMode, setViewMode] = useState<ViewMode>(() => {
    const saved = safeGetLocalStorage('mem_view_mode');
    return saved === 'flat' ? 'flat' : 'tree';
  });
  const [page, setPage] = useState(1);

  const [expandedFolders, setExpandedFolders] = useState<Set<string>>(() => {
    const saved = safeGetSessionStorage(`mem_tree_expanded_${slug}`);
    if (saved) {
      try {
        return new Set<string>(JSON.parse(saved));
      } catch {
        // fallback
      }
    }
    return new Set<string>();
  });

  const {
    data: contextData,
    isLoading: isContextLoading,
    isError: isContextError,
    error: contextError,
  } = useContextDetail(slug);

  useEffect(() => {
    if (isContextError) {
      notifications.show({
        title: 'Context unavailable',
        message:
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          (contextError as any)?.message ||
          `Context "${slug}" was not found or has expired. Redirected to global.`,
        color: 'red',
      });
      navigate('/c/global', { replace: true });
    }
  }, [isContextError, contextError, slug, navigate]);

  const offset = (page - 1) * PAGE_SIZE;

  const { data, isLoading, isError, refetch } = useMemoriesList(slug, {
    q: debouncedSearch || undefined,
    sort,
    limit: PAGE_SIZE,
    offset,
  });

  const totalPages = data ? Math.max(1, Math.ceil(data.total / PAGE_SIZE)) : 1;

  const treeNodes = useMemo(() => {
    if (!data?.items) return [];
    return buildMemoryTree(data.items);
  }, [data?.items]);

  useEffect(() => {
    if (!data?.items) return;

    const saved = safeGetSessionStorage(`mem_tree_expanded_${slug}`);
    let currentSet: Set<string>;
    if (saved) {
      try {
        currentSet = new Set<string>(JSON.parse(saved));
      } catch {
        currentSet = new Set<string>(getAllFolderPaths(treeNodes));
      }
    } else {
      currentSet = new Set<string>(getAllFolderPaths(treeNodes));
    }

    if (activeId) {
      const activeMemory = data.items.find((m) => m.id === activeId);
      if (activeMemory) {
        const ancestors = getAncestorFolderPaths(activeMemory.name || `${activeMemory.id}.md`);
        for (const anc of ancestors) {
          currentSet.add(anc);
        }
      }
    }

    setExpandedFolders(currentSet);
    safeSetSessionStorage(`mem_tree_expanded_${slug}`, JSON.stringify(Array.from(currentSet)));
  }, [slug, activeId, data?.items, treeNodes]);

  const handleToggleFolder = useCallback(
    (folderPath: string) => {
      setExpandedFolders((prev) => {
        const next = new Set(prev);
        if (next.has(folderPath)) {
          next.delete(folderPath);
        } else {
          next.add(folderPath);
        }
        safeSetSessionStorage(`mem_tree_expanded_${slug}`, JSON.stringify(Array.from(next)));
        return next;
      });
    },
    [slug],
  );

  const handleViewModeChange = (mode: ViewMode) => {
    setViewMode(mode);
    safeSetLocalStorage('mem_view_mode', mode);
  };

  const handleSelect = (id: string) => {
    if (onSelectMemory) {
      onSelectMemory(id);
    } else {
      workspace.requestNavigate(`/c/${slug}/m/${id}`);
    }
  };

  const handleNew = () => {
    if (onNewMemory) {
      onNewMemory();
    } else {
      workspace.requestNavigate(`/c/${slug}/new`);
    }
  };

  return (
    <Box
      p="xs"
      style={{
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        minWidth: 0,
        overflow: 'hidden',
      }}
    >
      <Stack gap="xs" style={{ flexShrink: 0 }}>
        {/* Header Breadcrumbs, Title & Actions */}
        <Group justify="space-between" align="center" wrap="nowrap" gap="xs">
          <Box style={{ minWidth: 0, flex: 1 }}>
            <Breadcrumbs separator="/" mb={2}>
              <Anchor size="xs" onClick={() => workspace.requestNavigate('/c/global')}>
                contexts
              </Anchor>
              <Text size="xs" c="dimmed" fw={600} truncate="end">
                {slug}
              </Text>
            </Breadcrumbs>
            <Tooltip label={slug} openDelay={600} disabled={slug.length < 16}>
              <Title
                order={2}
                fw={700}
                style={{
                  fontSize: '1.25rem',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                }}
              >
                {slug}
              </Title>
            </Tooltip>
            {contextData && (
              <Text size="xs" c="dimmed" truncate="end">
                {contextData.memory_count} memories
                {contextData.expires_at ? ' • temp' : ''}
              </Text>
            )}
          </Box>

          <Group gap={6} wrap="nowrap">
            <Button
              color="accent"
              size="xs"
              style={{ minHeight: 32 }}
              leftSection={<IconPlus size={14} />}
              onClick={handleNew}
            >
              New
            </Button>
            {showHeaderCollapse && onCollapse && (
              <Tooltip label="Collapse sidebar (Ctrl+B)">
                <ActionIcon
                  variant="subtle"
                  color="gray"
                  size="md"
                  onClick={onCollapse}
                  aria-label="Collapse sidebar"
                >
                  <IconLayoutSidebarLeftCollapse size={18} />
                </ActionIcon>
              </Tooltip>
            )}
          </Group>
        </Group>

        {/* Filter and View Controls Bar */}
        <Paper p="xs" withBorder radius="md">
          <Stack gap="xs">
            <TextInput
              placeholder="Filter memories by name..."
              leftSection={<IconSearch size={15} />}
              value={rawSearch}
              onChange={(e) => {
                setRawSearch(e.currentTarget.value);
                setPage(1);
              }}
              size="sm"
            />
            <Group justify="space-between" gap="xs" wrap="wrap">
              <Select
                value={sort}
                onChange={(val) => {
                  setSort((val as 'name' | 'updated') || 'name');
                  setPage(1);
                }}
                data={[
                  { value: 'name', label: 'Sort: Name' },
                  { value: 'updated', label: 'Sort: Last Updated' },
                ]}
                size="sm"
                style={{ flex: 1, minWidth: 110 }}
              />
              <ViewModeToggle value={viewMode} onChange={handleViewModeChange} />
            </Group>
          </Stack>
        </Paper>
      </Stack>

      {/* Scrollable Memories Content */}
      <Box
        mt="xs"
        style={{
          flex: 1,
          overflowY: 'auto',
          minHeight: 0,
        }}
      >
        {isLoading || isContextLoading ? (
          <Stack gap="xs" py="xs">
            <Skeleton height={36} radius="sm" />
            <Skeleton height={36} radius="sm" />
            <Skeleton height={36} radius="sm" />
            <Skeleton height={36} radius="sm" />
            <Skeleton height={36} radius="sm" />
          </Stack>
        ) : isError ? (
          <ErrorState
            title="Failed to load memories"
            message="Could not retrieve memories."
            onRetry={() => refetch()}
          />
        ) : !data || data.items.length === 0 ? (
          <EmptyState
            icon={<IconNote size={32} stroke={1.5} />}
            title={debouncedSearch ? 'No matching memories' : 'No memories'}
            description={
              debouncedSearch
                ? `No memories matched "${debouncedSearch}".`
                : 'Create your first memory in this context.'
            }
            action={
              <Button
                color="accent"
                size="xs"
                leftSection={<IconPlus size={14} />}
                onClick={handleNew}
              >
                New Memory
              </Button>
            }
          />
        ) : (
          <Paper withBorder radius="md" p="xs" style={{ width: '100%' }}>
            {viewMode === 'tree' ? (
              <Stack gap={2}>
                {treeNodes.map((node) => (
                  <MemoryTreeItem
                    key={node.path}
                    node={node}
                    selectedId={activeId}
                    onSelect={handleSelect}
                    expandedFolders={expandedFolders}
                    onToggleFolder={handleToggleFolder}
                  />
                ))}
              </Stack>
            ) : (
              <Stack gap={0}>
                {data.items.map((mem) => (
                  <MemoryFlatItem
                    key={mem.id}
                    memory={mem}
                    isSelected={activeId === mem.id}
                    onSelect={handleSelect}
                  />
                ))}
              </Stack>
            )}

            {/* Pagination */}
            {data.total > PAGE_SIZE && (
              <Box
                style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  gap: '8px',
                  marginTop: '12px',
                  paddingTop: '8px',
                  borderTop: '1px solid var(--mantine-color-default-border)',
                }}
              >
                <Text size="xs" c="dimmed">
                  {offset + 1}–{Math.min(offset + PAGE_SIZE, data.total)} of {data.total}
                </Text>
                <Pagination
                  value={page}
                  onChange={setPage}
                  total={totalPages}
                  size="xs"
                  color="accent"
                />
              </Box>
            )}
          </Paper>
        )}
      </Box>
    </Box>
  );
}
