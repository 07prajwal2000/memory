import { useState, useMemo, useEffect } from 'react';
import {
  Stack,
  Group,
  TextInput,
  Select,
  SegmentedControl,
  Button,
  Pagination,
  Text,
  Skeleton,
  Paper,
  Box,
  Title,
  Breadcrumbs,
  Anchor,
} from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { notifications } from '@mantine/notifications';
import {
  IconSearch,
  IconPlus,
  IconList,
  IconFolder,
  IconNote,
} from '@tabler/icons-react';
import { useParams, useNavigate } from 'react-router-dom';
import { useMemoriesList, useContextDetail } from '../../api';
import { useDebounce } from '../../hooks';
import { buildMemoryTree } from './treeUtils';
import { MemoryTreeItem } from './MemoryTreeItem';
import { MemoryFlatItem } from './MemoryFlatItem';
import { EmptyState, ErrorState } from '../../components';

const PAGE_SIZE = 25;

export function MemoryListView() {
  const { slug = 'global', id: activeId } = useParams<{ slug?: string; id?: string }>();
  const navigate = useNavigate();
  const isMobile = useMediaQuery('(max-width: 48em)');

  const [rawSearch, setRawSearch] = useState('');
  const debouncedSearch = useDebounce(rawSearch, 300);
  const [sort, setSort] = useState<'name' | 'updated'>('name');
  const [viewMode, setViewMode] = useState<'tree' | 'flat'>('tree');
  const [page, setPage] = useState(1);

  // Check context exists & not expired
  const {
    data: contextData,
    isLoading: isContextLoading,
    isError: isContextError,
    error: contextError,
  } = useContextDetail(slug);

  // Handle 404 / expired context
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

  const handleSelect = (id: string) => {
    navigate(`/c/${slug}/m/${id}`);
  };

  return (
    <Box p={{ base: 'xs', sm: 'md' }} maw={1200} mx="auto" style={{ overflowX: 'hidden' }}>
      <Stack gap="md">
        {/* Header Breadcrumbs and Actions */}
        <Group justify="space-between" align="center" wrap="wrap" gap="xs">
          <Box>
            <Breadcrumbs separator="/" mb={4}>
              <Anchor size="xs" onClick={() => navigate('/c/global')}>
                contexts
              </Anchor>
              <Text size="xs" c="dimmed" fw={600}>
                {slug}
              </Text>
            </Breadcrumbs>
            <Title order={2} fw={700}>
              {slug}
            </Title>
            {contextData && (
              <Text size="sm" c="dimmed">
                {contextData.memory_count} total memory document(s)
                {contextData.expires_at ? ' • Temporary context' : ' • Permanent context'}
              </Text>
            )}
          </Box>

          <Button
            color="accent"
            size="md"
            style={{ minHeight: 44 }}
            leftSection={<IconPlus size={18} />}
            onClick={() => navigate(`/c/${slug}/new`)}
          >
            New Memory
          </Button>
        </Group>

        {/* Filter and View Controls Bar: stacks vertically on small screens */}
        <Paper p="xs" withBorder radius="md">
          <Box
            style={{
              display: 'flex',
              flexDirection: isMobile ? 'column' : 'row',
              justifyContent: 'space-between',
              alignItems: isMobile ? 'stretch' : 'center',
              gap: '10px',
            }}
          >
            <Box
              style={{
                display: 'flex',
                flexDirection: isMobile ? 'column' : 'row',
                gap: '8px',
                flex: 1,
              }}
            >
              <TextInput
                placeholder="Filter memories by name..."
                leftSection={<IconSearch size={16} />}
                value={rawSearch}
                onChange={(e) => {
                  setRawSearch(e.currentTarget.value);
                  setPage(1);
                }}
                size="md"
                style={{ flex: 1 }}
              />
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
                size="md"
                w={isMobile ? '100%' : 180}
              />
            </Box>

            <Group justify={isMobile ? 'center' : 'flex-end'}>
              <SegmentedControl
                value={viewMode}
                onChange={(val) => setViewMode(val as 'tree' | 'flat')}
                data={[
                  {
                    value: 'tree',
                    label: (
                      <Group gap={6}>
                        <IconFolder size={16} />
                        <Text size="xs">Tree</Text>
                      </Group>
                    ),
                  },
                  {
                    value: 'flat',
                    label: (
                      <Group gap={6}>
                        <IconList size={16} />
                        <Text size="xs">Flat</Text>
                      </Group>
                    ),
                  },
                ]}
                size="sm"
                style={{ minHeight: 38 }}
              />
            </Group>
          </Box>
        </Paper>

        {/* List Content */}
        {isLoading || isContextLoading ? (
          <Stack gap="xs" py="md">
            <Skeleton height={44} radius="sm" />
            <Skeleton height={44} radius="sm" />
            <Skeleton height={44} radius="sm" />
            <Skeleton height={44} radius="sm" />
            <Skeleton height={44} radius="sm" />
          </Stack>
        ) : isError ? (
          <ErrorState
            title="Failed to load memories"
            message="Could not retrieve memories for this context."
            onRetry={() => refetch()}
          />
        ) : !data || data.items.length === 0 ? (
          <EmptyState
            icon={<IconNote size={40} stroke={1.5} />}
            title={debouncedSearch ? 'No matching memories' : 'No memories in this context'}
            description={
              debouncedSearch
                ? `No memory files matched "${debouncedSearch}". Try clearing your search.`
                : 'Create your first memory to store agent notes, guidelines, or state.'
            }
            action={
              <Button
                color="accent"
                size="md"
                style={{ minHeight: 44 }}
                leftSection={<IconPlus size={18} />}
                onClick={() => navigate(`/c/${slug}/new`)}
              >
                Create Memory
              </Button>
            }
          />
        ) : (
          <Paper withBorder radius="md" p="xs" style={{ width: '100%', overflowX: 'hidden' }}>
            {viewMode === 'tree' ? (
              <Stack gap={2}>
                {treeNodes.map((node) => (
                  <MemoryTreeItem
                    key={node.path}
                    node={node}
                    selectedId={activeId}
                    onSelect={handleSelect}
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
                  flexDirection: isMobile ? 'column' : 'row',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  gap: '10px',
                  marginTop: '16px',
                  paddingTop: '12px',
                  borderTop: '1px solid var(--mantine-color-default-border)',
                }}
              >
                <Text size="xs" c="dimmed">
                  Showing {offset + 1}–{Math.min(offset + PAGE_SIZE, data.total)} of {data.total}
                </Text>
                <Pagination
                  value={page}
                  onChange={setPage}
                  total={totalPages}
                  size="md"
                  color="accent"
                />
              </Box>
            )}
          </Paper>
        )}
      </Stack>
    </Box>
  );
}
