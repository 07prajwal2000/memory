import { useRef, useEffect } from 'react';
import { Box, Group, Text, Badge, Collapse, UnstyledButton, Tooltip } from '@mantine/core';
import { IconChevronRight, IconChevronDown, IconFolder, IconFileText } from '@tabler/icons-react';
import { TreeNode } from './treeUtils';
import { RelativeTime } from '../../components';

export interface MemoryTreeItemProps {
  node: TreeNode;
  selectedId?: string;
  onSelect: (id: string) => void;
  expandedFolders: Set<string>;
  onToggleFolder: (path: string) => void;
  depth?: number;
}

export function MemoryTreeItem({
  node,
  selectedId,
  onSelect,
  expandedFolders,
  onToggleFolder,
  depth = 0,
}: MemoryTreeItemProps) {
  const fileRef = useRef<HTMLButtonElement>(null);

  const isFolder = node.type === 'folder';
  const isExpanded = isFolder ? expandedFolders.has(node.path) : false;

  useEffect(() => {
    if (!isFolder && selectedId && node.memory.id === selectedId && fileRef.current) {
      try {
        fileRef.current.scrollIntoView?.({ block: 'nearest' });
      } catch {
        // safe fallback
      }
    }
  }, [isFolder, selectedId, node]);

  if (node.type === 'folder') {
    return (
      <Box>
        <UnstyledButton
          onClick={() => onToggleFolder(node.path)}
          style={{
            display: 'block',
            width: '100%',
            padding: '5px 8px',
            paddingLeft: `${depth * 14 + 6}px`,
            borderRadius: '4px',
            transition: 'background-color 150ms ease',
          }}
          aria-expanded={isExpanded}
        >
          <Group gap={6} wrap="nowrap" justify="space-between">
            <Group gap={6} wrap="nowrap" style={{ minWidth: 0, overflow: 'hidden' }}>
              {isExpanded ? <IconChevronDown size={14} /> : <IconChevronRight size={14} />}
              <IconFolder size={16} stroke={1.5} color="var(--mantine-color-accent-5)" style={{ flexShrink: 0 }} />
              <Tooltip label={node.name} openDelay={600} disabled={node.name.length < 18}>
                <Text size="sm" fw={500} truncate style={{ fontFamily: 'ui-monospace, monospace' }}>
                  {node.name}/
                </Text>
              </Tooltip>
            </Group>
            <Badge size="xs" variant="light" color="gray" style={{ flexShrink: 0 }}>
              {node.children.length}
            </Badge>
          </Group>
        </UnstyledButton>

        <Collapse expanded={isExpanded}>
          {node.children.map((child) => (
            <MemoryTreeItem
              key={child.path}
              node={child}
              selectedId={selectedId}
              onSelect={onSelect}
              expandedFolders={expandedFolders}
              onToggleFolder={onToggleFolder}
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
      ref={fileRef}
      onClick={() => onSelect(memory.id)}
      style={{
        display: 'block',
        width: '100%',
        padding: '5px 8px',
        paddingLeft: `${depth * 14 + 10}px`,
        borderRadius: '4px',
        backgroundColor: isSelected ? 'var(--mantine-color-accent-light)' : undefined,
        color: isSelected ? 'var(--mantine-color-accent-light-color)' : undefined,
        transition: 'background-color 150ms ease',
      }}
      aria-selected={isSelected}
    >
      <Group justify="space-between" wrap="nowrap">
        <Group gap={6} wrap="nowrap" style={{ minWidth: 0, overflow: 'hidden' }}>
          <IconFileText size={15} stroke={1.5} style={{ flexShrink: 0 }} />
          <Tooltip label={node.name} openDelay={600} disabled={node.name.length < 18}>
            <Text
              size="sm"
              truncate
              fw={isSelected ? 600 : 400}
              style={{ fontFamily: 'ui-monospace, monospace' }}
            >
              {node.name}
            </Text>
          </Tooltip>
          <Badge size="xs" variant="outline" color="gray" style={{ flexShrink: 0 }}>
            v{memory.version}
          </Badge>
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
