import { useState } from 'react';
import {
  Stack,
  Group,
  Button,
  Paper,
  Box,
  Title,
  Text,
  Badge,
  Grid,
  Skeleton,
  Alert,
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import {
  IconRefresh,
  IconServer,
  IconCpu,
  IconDatabase,
  IconCheck,
} from '@tabler/icons-react';
import { useServerStatus, useSyncIndex, SyncResponse } from '../../api';
import { ErrorState } from '../../components';

function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const sec = seconds % 60;

  const parts: string[] = [];
  if (days > 0) parts.push(`${days}d`);
  if (hours > 0) parts.push(`${hours}h`);
  if (minutes > 0) parts.push(`${minutes}m`);
  parts.push(`${sec}s`);

  return parts.join(' ');
}

export function StatusView() {
  const { data: status, isLoading, isError, refetch } = useServerStatus();
  const syncMutation = useSyncIndex();
  const [syncResult, setSyncResult] = useState<SyncResponse | null>(null);

  const handleSync = async () => {
    try {
      const res = await syncMutation.mutateAsync();
      setSyncResult(res);
      notifications.show({
        title: 'Index Synchronized',
        message: `Indexed ${res.indexed} docs, removed ${res.removed}.`,
        color: 'green',
      });
    } catch {
      notifications.show({
        title: 'Sync Failed',
        message: 'Could not synchronize index with files.',
        color: 'red',
      });
    }
  };

  if (isLoading) {
    return (
      <Box p="md" maw={1000} mx="auto">
        <Stack gap="md">
          <Skeleton height={40} width={240} />
          <Skeleton height={140} radius="md" />
          <Skeleton height={140} radius="md" />
          <Skeleton height={140} radius="md" />
        </Stack>
      </Box>
    );
  }

  if (isError || !status) {
    return (
      <Box p="md" maw={1000} mx="auto">
        <ErrorState
          title="Server unreachable"
          message="Could not connect to the mem backend server or retrieve status."
          onRetry={() => refetch()}
        />
      </Box>
    );
  }

  const embedState = status.models.embed.state;
  const rerankState = status.models.rerank.enabled
    ? status.models.rerank.state
    : 'disabled';

  return (
    <Box p="md" maw={1000} mx="auto">
      <Stack gap="lg">
        {/* Header */}
        <Group justify="space-between" align="center" wrap="wrap">
          <Box>
            <Title order={2} fw={700}>
              System Status
            </Title>
            <Text size="sm" c="dimmed">
              Instance health, local model sidecars, and SQLite vector index.
            </Text>
          </Box>

          <Button
            color="accent"
            size="md"
            style={{ minHeight: 44 }}
            leftSection={<IconRefresh size={16} />}
            onClick={handleSync}
            loading={syncMutation.isPending}
          >
            Sync Now
          </Button>
        </Group>

        {syncResult && (
          <Alert
            color="green"
            icon={<IconCheck size={18} />}
            title="Index Synchronization Complete"
            withCloseButton
            onClose={() => setSyncResult(null)}
          >
            <Text size="sm">
              Re-indexed <strong>{syncResult.indexed}</strong> documents from disk, cleaned{' '}
              <strong>{syncResult.removed}</strong> stale entries.
              {syncResult.errors.length > 0 && ` (${syncResult.errors.length} warnings)`}
            </Text>
          </Alert>
        )}

        <Grid gap="md">
          {/* Server Info Card */}
          <Grid.Col span={{ base: 12, md: 6 }}>
            <Paper withBorder p="md" radius="md" h="100%">
              <Stack gap="sm">
                <Group gap="xs">
                  <IconServer size={20} color="var(--mantine-color-accent-6)" />
                  <Title order={4} fw={600}>
                    Server Instance
                  </Title>
                </Group>

                <Stack gap={6}>
                  <Group justify="space-between">
                    <Text size="xs" c="dimmed">
                      Binary Version
                    </Text>
                    <Badge variant="outline" color="gray">
                      {status.version}
                    </Badge>
                  </Group>

                  <Group justify="space-between">
                    <Text size="xs" c="dimmed">
                      Uptime
                    </Text>
                    <Text size="xs" fw={500} style={{ fontFamily: 'ui-monospace, monospace' }}>
                      {formatUptime(status.uptime_s)}
                    </Text>
                  </Group>

                  <Group justify="space-between">
                    <Text size="xs" c="dimmed">
                      Data Directory
                    </Text>
                    <Text size="xs" fw={500} style={{ fontFamily: 'ui-monospace, monospace' }}>
                      {status.data_dir}
                    </Text>
                  </Group>

                  <Group justify="space-between">
                    <Text size="xs" c="dimmed">
                      Default Temp Expiry
                    </Text>
                    <Text size="xs" fw={500}>
                      {status.config.default_temp_expiry_days} days
                    </Text>
                  </Group>
                </Stack>
              </Stack>
            </Paper>
          </Grid.Col>

          {/* Index Health Card */}
          <Grid.Col span={{ base: 12, md: 6 }}>
            <Paper withBorder p="md" radius="md" h="100%">
              <Stack gap="sm">
                <Group justify="space-between">
                  <Group gap="xs">
                    <IconDatabase size={20} color="var(--mantine-color-accent-6)" />
                    <Title order={4} fw={600}>
                      Storage & Vector Index
                    </Title>
                  </Group>
                  <Badge
                    color={status.index.in_sync ? 'green' : 'yellow'}
                    variant="light"
                  >
                    {status.index.in_sync ? 'In Sync' : 'Sync Pending'}
                  </Badge>
                </Group>

                <Stack gap={6}>
                  <Group justify="space-between">
                    <Text size="xs" c="dimmed">
                      Indexed Documents
                    </Text>
                    <Text size="xs" fw={600} style={{ fontFamily: 'ui-monospace, monospace' }}>
                      {status.index.docs}
                    </Text>
                  </Group>

                  <Group justify="space-between">
                    <Text size="xs" c="dimmed">
                      Vector Chunks (384-d int8)
                    </Text>
                    <Text size="xs" fw={600} style={{ fontFamily: 'ui-monospace, monospace' }}>
                      {status.index.chunks}
                    </Text>
                  </Group>

                  <Group justify="space-between">
                    <Text size="xs" c="dimmed">
                      Pending Changes
                    </Text>
                    <Text size="xs" fw={600} style={{ fontFamily: 'ui-monospace, monospace' }}>
                      {status.index.pending}
                    </Text>
                  </Group>
                </Stack>
              </Stack>
            </Paper>
          </Grid.Col>

          {/* Local Models Card */}
          <Grid.Col span={12}>
            <Paper withBorder p="md" radius="md">
              <Stack gap="md">
                <Group gap="xs">
                  <IconCpu size={20} color="var(--mantine-color-accent-6)" />
                  <Title order={4} fw={600}>
                    Local Models (llama-server sidecar)
                  </Title>
                </Group>

                <Grid gap="sm">
                  <Grid.Col span={{ base: 12, md: 6 }}>
                    <Paper withBorder p="sm" radius="sm" bg="var(--mantine-color-body)">
                      <Stack gap={4}>
                        <Group justify="space-between">
                          <Text size="xs" fw={700} tt="uppercase" c="dimmed">
                            Embedding Model
                          </Text>
                          <Badge
                            color={embedState === 'ready' ? 'green' : embedState === 'error' ? 'red' : 'yellow'}
                            size="sm"
                          >
                            {embedState}
                          </Badge>
                        </Group>
                        <Text size="xs" style={{ fontFamily: 'ui-monospace, monospace' }}>
                          {status.models.embed.detail || 'bge-small-en-v1.5 Q8_0 GGUF'}
                        </Text>
                      </Stack>
                    </Paper>
                  </Grid.Col>

                  <Grid.Col span={{ base: 12, md: 6 }}>
                    <Paper withBorder p="sm" radius="sm" bg="var(--mantine-color-body)">
                      <Stack gap={4}>
                        <Group justify="space-between">
                          <Text size="xs" fw={700} tt="uppercase" c="dimmed">
                            Reranker Model
                          </Text>
                          <Badge
                            color={
                              rerankState === 'disabled'
                                ? 'gray'
                                : rerankState === 'ready'
                                  ? 'green'
                                  : 'yellow'
                            }
                            size="sm"
                          >
                            {rerankState === 'disabled'
                              ? 'disabled (off by default)'
                              : rerankState}
                          </Badge>
                        </Group>
                        <Text size="xs" style={{ fontFamily: 'ui-monospace, monospace' }}>
                          {status.models.rerank.detail || 'jina-reranker-v1-tiny-en (optional)'}
                        </Text>
                      </Stack>
                    </Paper>
                  </Grid.Col>
                </Grid>

                {!status.models.rerank.enabled && (
                  <Text size="xs" c="dimmed">
                    Note: Cross-encoder reranking is off by default to minimize memory footprint. Hybrid BM25 + sqlite-vec vector search is actively running.
                  </Text>
                )}
              </Stack>
            </Paper>
          </Grid.Col>
        </Grid>
      </Stack>
    </Box>
  );
}
