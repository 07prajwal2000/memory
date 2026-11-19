import { useState } from 'react';
import {
  Modal,
  TextInput,
  Stack,
  Group,
  Text,
  Badge,
  UnstyledButton,
  Box,
  Kbd,
} from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { IconSearch, IconFileText, IconArrowRight, IconX } from '@tabler/icons-react';
import { useNavigate } from 'react-router-dom';
import { useSearch } from '../../api';
import { useDebounce, useKeyboardShortcut } from '../../hooks';

export interface SearchSpotlightProps {
  opened: boolean;
  onClose: () => void;
}

export function SearchSpotlight({ opened, onClose }: SearchSpotlightProps) {
  const [query, setQuery] = useState('');
  const debouncedQuery = useDebounce(query, 250);
  const navigate = useNavigate();
  const isMobile = useMediaQuery('(max-width: 48em)');

  const trimmed = debouncedQuery.trim();
  const { data: searchResults, isLoading } = useSearch(
    { q: trimmed, limit: 6 },
    Boolean(trimmed),
  );

  const handleSelect = (context: string, id: string) => {
    onClose();
    navigate(`/c/${context}/m/${id}`);
  };

  const handleViewAll = () => {
    if (!trimmed) return;
    onClose();
    navigate(`/search?q=${encodeURIComponent(trimmed)}`);
  };

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      fullScreen={isMobile}
      size="lg"
      withCloseButton={isMobile}
      padding="sm"
      radius="md"
      centered
      styles={{
        body: { padding: '8px' },
      }}
    >
      <Stack gap="xs">
        <Group justify="space-between" align="center">
          <TextInput
            placeholder="Search memories or type a command..."
            leftSection={<IconSearch size={18} />}
            rightSection={isMobile ? undefined : <Kbd size="xs">Esc</Kbd>}
            value={query}
            onChange={(e) => setQuery(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                handleViewAll();
              }
            }}
            data-autofocus
            size="md"
            variant="unstyled"
            style={{
              flex: 1,
              borderBottom: '1px solid var(--mantine-color-default-border)',
              paddingBottom: '4px',
            }}
          />
          {isMobile && (
            <UnstyledButton
              onClick={onClose}
              style={{
                minWidth: 44,
                minHeight: 44,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
              aria-label="Close search"
            >
              <IconX size={20} />
            </UnstyledButton>
          )}
        </Group>

        <Box style={{ maxHeight: isMobile ? '70vh' : 380, overflowY: 'auto' }}>
          {isLoading && (
            <Text size="xs" c="dimmed" p="sm" ta="center">
              Searching...
            </Text>
          )}

          {!isLoading && trimmed && searchResults?.hits.length === 0 && (
            <Text size="sm" c="dimmed" p="md" ta="center">
              No memories found matching "{trimmed}".
            </Text>
          )}

          <Stack gap={2}>
            {searchResults?.hits.map((hit) => (
              <UnstyledButton
                key={`${hit.context}-${hit.doc_id}`}
                onClick={() => handleSelect(hit.context, hit.doc_id)}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  width: '100%',
                  minHeight: 44,
                  padding: '8px 10px',
                  borderRadius: '6px',
                  textAlign: 'left',
                }}
              >
                <Group justify="space-between" wrap="nowrap" style={{ width: '100%' }}>
                  <Group gap={8} wrap="nowrap" style={{ minWidth: 0 }}>
                    <IconFileText size={16} stroke={1.5} color="var(--mantine-color-accent-6)" />
                    <Text size="sm" fw={500} truncate style={{ fontFamily: 'ui-monospace, monospace' }}>
                      {hit.name || `${hit.doc_id}.md`}
                    </Text>
                    <Badge size="xs" variant="outline" color="gray">
                      {hit.context}
                    </Badge>
                  </Group>

                  <Badge size="xs" variant="light" color="accent">
                    {(hit.score * 100).toFixed(0)}%
                  </Badge>
                </Group>
              </UnstyledButton>
            ))}
          </Stack>
        </Box>

        {trimmed && (
          <Group
            justify="space-between"
            pt="xs"
            style={{
              borderTop: '1px solid var(--mantine-color-default-border)',
              minHeight: 44,
            }}
          >
            <Text size="xs" c="dimmed">
              Press <Kbd size="xs">Enter</Kbd> to open full results page
            </Text>
            <UnstyledButton
              onClick={handleViewAll}
              style={{ minHeight: 44, display: 'flex', alignItems: 'center' }}
            >
              <Group gap={4}>
                <Text size="xs" c="accent" fw={600}>
                  View all results
                </Text>
                <IconArrowRight size={12} color="var(--mantine-color-accent-6)" />
              </Group>
            </UnstyledButton>
          </Group>
        )}
      </Stack>
    </Modal>
  );
}

export function useSpotlightTrigger() {
  const [opened, setOpened] = useState(false);

  useKeyboardShortcut('k', () => setOpened(true), {
    ctrlOrCmd: true,
    preventDefault: true,
  });

  return {
    opened,
    open: () => setOpened(true),
    close: () => setOpened(false),
  };
}
