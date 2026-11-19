import React from 'react';
import { Stack, Text, Title, Box } from '@mantine/core';
import { IconFolderOff } from '@tabler/icons-react';

export interface EmptyStateProps {
  icon?: React.ReactNode;
  title: string;
  description?: string;
  action?: React.ReactNode;
}

export function EmptyState({
  icon = <IconFolderOff size={36} stroke={1.5} />,
  title,
  description,
  action,
}: EmptyStateProps) {
  return (
    <Box py="xl" px="md">
      <Stack align="center" gap="sm" ta="center">
        <Box c="dimmed" opacity={0.6}>
          {icon}
        </Box>
        <Title order={4} fw={600}>
          {title}
        </Title>
        {description && (
          <Text size="sm" c="dimmed" maw={440}>
            {description}
          </Text>
        )}
        {action && <Box mt="xs">{action}</Box>}
      </Stack>
    </Box>
  );
}
