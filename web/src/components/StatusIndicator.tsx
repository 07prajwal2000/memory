import { Tooltip, Box, Group, Text } from '@mantine/core';
import { useNavigate } from 'react-router-dom';
import { useServerStatus } from '../api';

export function StatusIndicator() {
  const { data: status, isError } = useServerStatus();
  const navigate = useNavigate();

  let color = 'green';
  let label = 'Server connected & healthy';

  if (isError) {
    color = 'red';
    label = 'Server connection failed';
  } else if (!status) {
    color = 'gray';
    label = 'Checking server status...';
  } else if (
    status.models.embed.state === 'error' ||
    status.models.rerank.state === 'error'
  ) {
    color = 'red';
    label = 'Model service error';
  } else if (
    !status.index.in_sync ||
    status.models.embed.state === 'starting' ||
    status.models.rerank.state === 'starting'
  ) {
    color = 'yellow';
    label = 'Server syncing / models loading';
  }

  return (
    <Tooltip label={`${label} — click to view status`} withArrow>
      <Box
        component="button"
        onClick={() => navigate('/status')}
        style={{
          background: 'none',
          border: 'none',
          cursor: 'pointer',
          padding: '4px 8px',
          borderRadius: '4px',
          display: 'flex',
          alignItems: 'center',
        }}
        aria-label="Server status indicator"
      >
        <Group gap={6}>
          <Box
            w={8}
            h={8}
            style={{
              borderRadius: '50%',
              backgroundColor: `var(--mantine-color-${color}-6, #12b886)`,
              boxShadow: `0 0 6px var(--mantine-color-${color}-6, #12b886)`,
            }}
          />
          <Text size="xs" c="dimmed" visibleFrom="sm" fw={500}>
            {status ? `v${status.version.replace(/^v/, '')}` : 'connecting'}
          </Text>
        </Group>
      </Box>
    </Tooltip>
  );
}
