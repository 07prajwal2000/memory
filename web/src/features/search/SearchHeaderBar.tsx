import { UnstyledButton, Group, Text, Kbd, Box } from '@mantine/core';
import { IconSearch } from '@tabler/icons-react';

export interface SearchHeaderBarProps {
  onClick: () => void;
}

export function SearchHeaderBar({ onClick }: SearchHeaderBarProps) {
  const isMac =
    typeof navigator !== 'undefined' && /Mac|iPod|iPhone|iPad/.test(navigator.platform);

  return (
    <UnstyledButton
      onClick={onClick}
      style={{
        display: 'flex',
        alignItems: 'center',
        padding: '5px 10px',
        backgroundColor: 'var(--mantine-color-default)',
        border: '1px solid var(--mantine-color-default-border)',
        borderRadius: '6px',
        width: '100%',
        maxWidth: '360px',
        cursor: 'pointer',
      }}
      aria-label="Open search spotlight"
    >
      <Group justify="space-between" style={{ width: '100%' }}>
        <Group gap={8}>
          <IconSearch size={16} color="var(--mantine-color-dimmed)" />
          <Text size="xs" c="dimmed">
            Search memories...
          </Text>
        </Group>
        <Box display="flex" style={{ gap: '2px' }}>
          <Kbd size="xs">{isMac ? '⌘' : 'Ctrl'}</Kbd>
          <Kbd size="xs">K</Kbd>
        </Box>
      </Group>
    </UnstyledButton>
  );
}
