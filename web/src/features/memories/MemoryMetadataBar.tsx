import { Group, Badge, Text, ActionIcon, Tooltip, CopyButton } from '@mantine/core';
import { IconCopy, IconCheck } from '@tabler/icons-react';
import { formatBytes } from '../../lib/formatters';
import { RelativeTime } from '../../components';

export interface MemoryMetadataBarProps {
  id?: string;
  version?: number;
  updatedAt?: string;
  updatedBy?: string;
  bodyBytes: number;
  isDirty: boolean;
}

export function MemoryMetadataBar({
  id,
  version,
  updatedAt,
  updatedBy,
  bodyBytes,
  isDirty,
}: MemoryMetadataBarProps) {
  return (
    <Group justify="space-between" wrap="wrap" gap="xs" py={4}>
      <Group gap="xs" wrap="wrap">
        {id && (
          <Group gap={4}>
            <Text size="xs" c="dimmed">
              ID:
            </Text>
            <Badge
              variant="outline"
              color="gray"
              size="sm"
              style={{ fontFamily: 'ui-monospace, monospace' }}
            >
              {id}
            </Badge>
            <CopyButton value={id} timeout={2000}>
              {({ copied, copy }) => (
                <Tooltip label={copied ? 'Copied' : 'Copy ID'} withArrow>
                  <ActionIcon color={copied ? 'teal' : 'gray'} variant="subtle" size="xs" onClick={copy}>
                    {copied ? <IconCheck size={12} /> : <IconCopy size={12} />}
                  </ActionIcon>
                </Tooltip>
              )}
            </CopyButton>
          </Group>
        )}

        {version !== undefined && (
          <Badge variant="light" color="accent" size="sm">
            v{version}
          </Badge>
        )}

        {isDirty && (
          <Badge color="yellow" variant="dot" size="sm">
            Unsaved changes
          </Badge>
        )}
      </Group>

      <Group gap="md" wrap="wrap">
        <Text size="xs" c="dimmed">
          Size: {formatBytes(bodyBytes)}
        </Text>
        {updatedBy && (
          <Text size="xs" c="dimmed">
            By: <strong>{updatedBy}</strong>
          </Text>
        )}
        {updatedAt && (
          <Text size="xs" c="dimmed">
            Updated <RelativeTime isoString={updatedAt} size="xs" />
          </Text>
        )}
      </Group>
    </Group>
  );
}
