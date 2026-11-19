import { useState, useEffect } from 'react';
import {
  Stack,
  Group,
  TextInput,
  Select,
  Switch,
  Button,
  Text,
  Paper,
  Box,
  Alert,
  Skeleton,
  Title,
  Tooltip,
} from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { useSearchParams } from 'react-router-dom';
import {
  IconSearch,
  IconAlertTriangle,
  IconClock,
  IconFolderOff,
} from '@tabler/icons-react';
import { useContexts, useSearch } from '../../api';
import { useDebounce } from '../../hooks';
import { SearchResultCard } from './SearchResultCard';
import { EmptyState, ErrorState } from '../../components';

export function SearchResultsView() {
  const [searchParams, setSearchParams] = useSearchParams();
  const isMobile = useMediaQuery('(max-width: 48em)');

  const urlQ = searchParams.get('q') || '';
  const urlContext = searchParams.get('context') || '';
  const urlNoGlobal = searchParams.get('no_global') === 'true';

  const [queryInput, setQueryInput] = useState(urlQ);
  const debouncedQuery = useDebounce(queryInput, 300);

  const [selectedContext, setSelectedContext] = useState<string | null>(
    urlContext || null,
  );
  const [includeGlobal, setIncludeGlobal] = useState<boolean>(!urlNoGlobal);

  const { data: contexts } = useContexts();

  // Sync inputs with URL
  useEffect(() => {
    setQueryInput(urlQ);
    setSelectedContext(urlContext || null);
    setIncludeGlobal(!urlNoGlobal);
  }, [urlQ, urlContext, urlNoGlobal]);

  // Update URL params when query changes
  useEffect(() => {
    const params = new URLSearchParams();
    if (debouncedQuery.trim()) {
      params.set('q', debouncedQuery.trim());
    }
    if (selectedContext) {
      params.set('context', selectedContext);
    }
    if (!includeGlobal && selectedContext) {
      params.set('no_global', 'true');
    }

    if (params.toString() !== searchParams.toString()) {
      setSearchParams(params, { replace: true });
    }
  }, [debouncedQuery, selectedContext, includeGlobal, setSearchParams, searchParams]);

  const handleContextChange = (val: string | null) => {
    setSelectedContext(val);
    if (!val) {
      setIncludeGlobal(true);
    }
  };

  const isGlobalSwitchDisabled = !selectedContext;

  const searchQuery = debouncedQuery.trim();
  const { data, isLoading, isError, error, refetch } = useSearch(
    {
      q: searchQuery,
      context: selectedContext || undefined,
      no_global: !includeGlobal && Boolean(selectedContext),
    },
    Boolean(searchQuery),
  );

  return (
    <Box p={{ base: 'xs', sm: 'md' }} maw={1000} mx="auto" style={{ overflowX: 'hidden' }}>
      <Stack gap="md">
        <Title order={2} fw={700}>
          Hybrid Search
        </Title>

        {/* Scope Controls */}
        <Paper withBorder p="md" radius="md">
          <Stack gap="sm">
            <TextInput
              placeholder="Search all memories using hybrid keyword + vector search..."
              leftSection={<IconSearch size={18} />}
              value={queryInput}
              onChange={(e) => setQueryInput(e.currentTarget.value)}
              size="md"
              data-autofocus
            />

            {/* Vertically stacked on mobile; horizontal on desktop */}
            <Box
              style={{
                display: 'flex',
                flexDirection: isMobile ? 'column' : 'row',
                justifyContent: 'space-between',
                alignItems: isMobile ? 'stretch' : 'center',
                gap: '12px',
              }}
            >
              <Box
                style={{
                  display: 'flex',
                  flexDirection: isMobile ? 'column' : 'row',
                  alignItems: isMobile ? 'stretch' : 'center',
                  gap: '12px',
                }}
              >
                <Select
                  placeholder="All contexts (global scope)"
                  clearable
                  data={
                    contexts
                      ?.filter((c) => !c.is_global)
                      .map((c) => ({
                        value: c.slug,
                        label: `Context: ${c.slug}`,
                      })) || []
                  }
                  value={selectedContext}
                  onChange={handleContextChange}
                  size="md"
                  w={isMobile ? '100%' : 240}
                />

                <Tooltip
                  label={
                    isGlobalSwitchDisabled
                      ? 'Excluding global context requires selecting a specific context'
                      : 'Include memories from the global context in search results'
                  }
                  withArrow
                >
                  <Box style={{ minHeight: 36, display: 'flex', alignItems: 'center' }}>
                    <Switch
                      label="Include global context"
                      checked={includeGlobal}
                      disabled={isGlobalSwitchDisabled}
                      onChange={(e) => setIncludeGlobal(e.currentTarget.checked)}
                      size="md"
                    />
                  </Box>
                </Tooltip>
              </Box>

              {data && (
                <Group gap={6} justify={isMobile ? 'flex-start' : 'flex-end'}>
                  <IconClock size={14} color="var(--mantine-color-dimmed)" />
                  <Text size="xs" c="dimmed">
                    {data.hits.length} result(s) in {data.took_ms} ms
                  </Text>
                </Group>
              )}
            </Box>
          </Stack>
        </Paper>

        {/* Degraded Notices Alert */}
        {data?.degraded && data.degraded.length > 0 && (
          <Alert
            color="yellow"
            variant="light"
            icon={<IconAlertTriangle size={18} />}
            title="Search Degraded Mode"
          >
            {data.degraded.map((notice, i) => (
              <Text key={i} size="sm">
                {notice}
              </Text>
            ))}
          </Alert>
        )}

        {/* Results List */}
        {!searchQuery ? (
          <EmptyState
            icon={<IconSearch size={40} stroke={1.5} />}
            title="Search your memories"
            description="Type a keyword, concept, or question above. Search combines BM25 keyword matching and dense vector embeddings."
          />
        ) : isLoading ? (
          <Stack gap="sm">
            <Skeleton height={90} radius="md" />
            <Skeleton height={90} radius="md" />
            <Skeleton height={90} radius="md" />
          </Stack>
        ) : isError ? (
          <ErrorState
            title="Search request failed"
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            message={(error as any)?.message || 'Could not complete search query.'}
            onRetry={() => refetch()}
          />
        ) : data?.hits.length === 0 ? (
          <EmptyState
            icon={<IconFolderOff size={40} stroke={1.5} />}
            title={`No results for "${searchQuery}"`}
            description="Try searching with different terms or expand your context scope."
            action={
              selectedContext && (
                <Button
                  variant="default"
                  size="md"
                  style={{ minHeight: 44 }}
                  onClick={() => {
                    setSelectedContext(null);
                    setIncludeGlobal(true);
                  }}
                >
                  Search all contexts
                </Button>
              )
            }
          />
        ) : (
          <Stack gap="sm">
            {data?.hits.map((hit) => (
              <SearchResultCard key={`${hit.context}-${hit.doc_id}-${hit.chunk}`} hit={hit} />
            ))}
          </Stack>
        )}
      </Stack>
    </Box>
  );
}
