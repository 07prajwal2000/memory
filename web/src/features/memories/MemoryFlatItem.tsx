import { Group, Text, Badge, UnstyledButton } from '@mantine/core';
import { IconFileText } from '@tabler/icons-react';
import { MemorySummary } from '../../api';
import { formatBytes } from '../../lib/formatters';
import { RelativeTime } from '../../components';

export interface MemoryFlatItemProps {
  memory: MemorySummary;
  isSelected: boolean;
  onSelect: (id: string) => void;
}

export function MemoryFlatItem({ memory, isSelected, onSelect }: MemoryFlatItemProps) {
  return (
    <UnstyledButton
      onClick={() => onSelect(memory.id)}
      style={{
        display: 'block',
        width: '100%',
        padding: '8px 12px',
        borderRadius: '6px',
        backgroundColor: isSelected ? 'var(--mantine-color-accent-light)' : undefined,
        borderBottom: '1px solid var(--mantine-color-default-border)',
      }}
    >
      <Group justify="space-between" wrap="nowrap">
        <Group gap={8} wrap="nowrap" style={{ minWidth: 0, overflow: 'hidden' }}>
          <IconFileText size={18} stroke={1.5} style={{ flexShrink: 0 }} />
          <Text
            size="sm"
            truncate
            fw={isSelected ? 600 : 400}
            style={{ fontFamily: 'ui-monospace, monospace' }}
          >
            {memory.name || `${memory.id}.md`}
          </Text>
          <Badge size="xs" variant="outline" color="gray" style={{ flexShrink: 0 }}>
            v{memory.version}
          </Badge>
          <Text size="xs" c="dimmed" style={{ flexShrink: 0 }}>
            {formatBytes(memory.size)}
          </Text>
        </Group>

        <Group gap={6} wrap="nowrap" style={{ flexShrink: 0 }}>
          {memory.tags.map((tag) => (
            <Badge key={tag} size="xs" variant="subtle" color="accent">
              {tag}
            </Badge>
          ))}
          <RelativeTime isoString={memory.updated_at} size="xs" />
        </Group>
      </Group>
    </UnstyledButton>
  );
}
