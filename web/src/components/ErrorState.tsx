import React from 'react';
import { Stack, Text, Title, Button, Box, Alert } from '@mantine/core';
import { IconAlertTriangle, IconRefresh } from '@tabler/icons-react';

export interface ErrorStateProps {
  title?: string;
  message?: string;
  onRetry?: () => void;
  action?: React.ReactNode;
}

export function ErrorState({
  title = 'Something went wrong',
  message,
  onRetry,
  action,
}: ErrorStateProps) {
  return (
    <Box py="xl" px="md">
      <Stack align="center" gap="sm" ta="center">
        <Box c="red" opacity={0.8}>
          <IconAlertTriangle size={36} stroke={1.5} />
        </Box>
        <Title order={4} fw={600}>
          {title}
        </Title>
        {message && (
          <Alert color="red" variant="light" maw={520} ta="left">
            <Text size="sm">{message}</Text>
          </Alert>
        )}
        <Box mt="xs">
          {onRetry && (
            <Button
              variant="default"
              size="sm"
              leftSection={<IconRefresh size={16} />}
              onClick={onRetry}
            >
              Try again
            </Button>
          )}
          {action}
        </Box>
      </Stack>
    </Box>
  );
}
