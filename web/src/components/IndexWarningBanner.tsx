import { Alert, Button, Group, Text } from '@mantine/core';
import { IconAlertTriangle, IconRefresh } from '@tabler/icons-react';
import { useSyncIndex } from '../api';
import { notifications } from '@mantine/notifications';

export interface IndexWarningBannerProps {
  warning: string;
  onDismiss?: () => void;
}

export function IndexWarningBanner({ warning, onDismiss }: IndexWarningBannerProps) {
  const syncMutation = useSyncIndex();

  const handleSync = async () => {
    try {
      const res = await syncMutation.mutateAsync();
      notifications.show({
        title: 'Sync complete',
        message: `Indexed ${res.indexed} docs. Index is now healthy.`,
        color: 'green',
      });
      if (onDismiss) onDismiss();
    } catch {
      notifications.show({
        title: 'Sync failed',
        message: 'Could not complete index synchronization.',
        color: 'red',
      });
    }
  };

  return (
    <Alert
      color="yellow"
      variant="light"
      icon={<IconAlertTriangle size={18} />}
      withCloseButton={Boolean(onDismiss)}
      onClose={onDismiss}
      mb="md"
    >
      <Group justify="space-between" align="center" gap="sm">
        <Text size="sm">{warning}</Text>
        <Button
          size="xs"
          variant="light"
          color="yellow"
          leftSection={<IconRefresh size={14} />}
          loading={syncMutation.isPending}
          onClick={handleSync}
        >
          Sync now
        </Button>
      </Group>
    </Alert>
  );
}
