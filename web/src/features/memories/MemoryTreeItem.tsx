import { useState } from 'react';
import { Box, Group, Text, Badge, Collapse, UnstyledButton } from '@mantine/core';
import { IconChevronRight, IconChevronDown, IconFolder, IconFileText } from '@tabler/icons-react';
import { TreeNode } from './treeUtils';
import { RelativeTime } from '../../components';

export interface MemoryTreeItemProps {
  node: TreeNode;
  selectedId?: string;
  onSelect: (id: string) => void;
  depth?: number;
}

export function MemoryTreeItem({
  node,
  selectedId,
  onSelect,
  depth = 0,
}: MemoryTreeItemProps) {
  const [opened, setOpened] = useState(true);

  if (node.type === 'folder') {
    return (
      <Box>
        <UnstyledButton
          onClick={() => setOpened((o) => !o)}
          style={{
            display: 'block',
            width: '100%',
            padding: '5px 8px',
            paddingLeft: `${depth * 16 + 8}px`,
            borderRadius: '4px',
          }}
          aria-expanded={opened}
        >
          <Group gap={6} wrap="nowrap">
            {opened ? <IconChevronDown size={14} /> : <IconChevronRight size={14} />}
            <IconFolder size={16} stroke={1.5} color="var(--mantine-color-accent-5)" />
            <Text size="sm" fw={500} style={{ fontFamily: 'ui-monospace, monospace' }}>
              {node.name}/
            </Text>
            <Badge size="xs" variant="light" color="gray">
              {node.children.length}
            </Badge>
          </Group>
        </UnstyledButton>

        <Collapse expanded={opened}>
          {node.children.map((child) => (
            <MemoryTreeItem
              key={child.path}
              node={child}
              selectedId={selectedId}
              onSelect={onSelect}
              depth={depth + 1}
            />
          ))}
        </Collapse>
      </Box>
    );
  }

  const memory = node.memory;
  const isSelected = selectedId === memory.id;

  return (
    <UnstyledButton
      onClick={() => onSelect(memory.id)}
      style={{
        display: 'block',
        width: '100%',
        padding: '6px 8px',
        paddingLeft: `${depth * 16 + 12}px`,
        borderRadius: '4px',
        backgroundColor: isSelected ? 'var(--mantine-color-accent-light)' : undefined,
      }}
    >
      <Group justify="space-between" wrap="nowrap">
        <Group gap={8} wrap="nowrap" style={{ minWidth: 0, overflow: 'hidden' }}>
          <IconFileText size={16} stroke={1.5} style={{ flexShrink: 0 }} />
          <Text
            size="sm"
            truncate
            fw={isSelected ? 600 : 400}
            style={{ fontFamily: 'ui-monospace, monospace' }}
          >
            {node.name}
          </Text>
          <Badge size="xs" variant="outline" color="gray" style={{ flexShrink: 0 }}>
            v{memory.version}
          </Badge>
        </Group>

        <Group gap={6} wrap="nowrap" style={{ flexShrink: 0 }}>
          {memory.tags.slice(0, 2).map((tag) => (
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
