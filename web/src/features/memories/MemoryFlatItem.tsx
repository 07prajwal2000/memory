import { useRef, useEffect } from 'react';
import { Group, Text, Badge, UnstyledButton, Tooltip } from '@mantine/core';
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
  const itemRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (isSelected && itemRef.current) {
      try {
        itemRef.current.scrollIntoView?.({ block: 'nearest' });
      } catch {
        // safe fallback
      }
    }
  }, [isSelected]);

  const displayName = memory.name || `${memory.id}.md`;

  return (
    <UnstyledButton
      ref={itemRef}
      onClick={() => onSelect(memory.id)}
      style={{
        display: 'block',
        width: '100%',
        padding: '6px 8px',
        borderRadius: '4px',
        backgroundColor: isSelected ? 'var(--mantine-color-accent-light)' : undefined,
        color: isSelected ? 'var(--mantine-color-accent-light-color)' : undefined,
        borderBottom: '1px solid var(--mantine-color-default-border)',
        transition: 'background-color 150ms ease',
      }}
      aria-selected={isSelected}
    >
      <Group justify="space-between" wrap="nowrap">
        <Group gap={6} wrap="nowrap" style={{ minWidth: 0, overflow: 'hidden' }}>
          <IconFileText size={16} stroke={1.5} style={{ flexShrink: 0 }} />
          <Tooltip label={displayName} openDelay={600} disabled={displayName.length < 20}>
            <Text
              size="sm"
              truncate
              fw={isSelected ? 600 : 400}
              style={{ fontFamily: 'ui-monospace, monospace' }}
            >
              {displayName}
            </Text>
          </Tooltip>
          <Badge size="xs" variant="outline" color="gray" style={{ flexShrink: 0 }}>
            v{memory.version}
          </Badge>
          <Text size="xs" c="dimmed" style={{ flexShrink: 0 }}>
            {formatBytes(memory.size)}
          </Text>
        </Group>

        <Group gap={4} wrap="nowrap" style={{ flexShrink: 0 }}>
          {memory.tags.slice(0, 1).map((tag) => (
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
