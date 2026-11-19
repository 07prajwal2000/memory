import { Modal, Text, Button, Group, Stack, Paper, SimpleGrid } from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { notifications } from '@mantine/notifications';
import { IconCopy, IconRefresh, IconAlertTriangle, IconReplace } from '@tabler/icons-react';

export interface ConflictDialogProps {
  opened: boolean;
  serverVersion?: number;
  serverBody: string;
  myBody: string;
  onReloadTheirs: () => void;
  onOverwrite: () => void;
  onClose: () => void;
  isOverwriting?: boolean;
}

export function ConflictDialog({
  opened,
  serverVersion,
  serverBody,
  myBody,
  onReloadTheirs,
  onOverwrite,
  onClose,
  isOverwriting = false,
}: ConflictDialogProps) {
  const isMobile = useMediaQuery('(max-width: 48em)');

  const handleCopyMyText = async () => {
    try {
      await navigator.clipboard.writeText(myBody);
      notifications.show({
        title: 'Copied',
        message: 'Your edited text has been copied to clipboard.',
        color: 'green',
      });
    } catch {
      notifications.show({
        title: 'Copy failed',
        message: 'Could not access clipboard.',
        color: 'red',
      });
    }
  };

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      fullScreen={isMobile}
      title={
        <Group gap="xs">
          <IconAlertTriangle color="var(--mantine-color-yellow-6)" size={20} />
          <Text fw={600}>Version Conflict Detected</Text>
        </Group>
      }
      size="xl"
      centered
      closeOnClickOutside={false}
    >
      <Stack gap="md">
        <Text size="sm">
          Another client or agent has modified this memory while you were editing (current server version is{' '}
          <strong>v{serverVersion ?? 'unknown'}</strong>). Review the versions below:
        </Text>

        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
          <Stack gap={4}>
            <Text size="xs" fw={700} c="dimmed" tt="uppercase">
              Server Version (v{serverVersion})
            </Text>
            <Paper
              withBorder
              p="xs"
              bg="var(--mantine-color-body)"
              style={{
                maxHeight: isMobile ? 180 : 280,
                overflowY: 'auto',
                fontFamily: 'ui-monospace, monospace',
                fontSize: '12px',
                whiteSpace: 'pre-wrap',
              }}
            >
              {serverBody || <Text c="dimmed">Empty</Text>}
            </Paper>
          </Stack>

          <Stack gap={4}>
            <Text size="xs" fw={700} c="accent" tt="uppercase">
              Your Edited Version
            </Text>
            <Paper
              withBorder
              p="xs"
              bg="var(--mantine-color-body)"
              style={{
                maxHeight: isMobile ? 180 : 280,
                overflowY: 'auto',
                fontFamily: 'ui-monospace, monospace',
                fontSize: '12px',
                whiteSpace: 'pre-wrap',
              }}
            >
              {myBody || <Text c="dimmed">Empty</Text>}
            </Paper>
          </Stack>
        </SimpleGrid>

        <Group justify="space-between" mt="md" wrap="wrap" gap="xs">
          <Button
            variant="default"
            size="sm"
            leftSection={<IconCopy size={16} />}
            onClick={handleCopyMyText}
          >
            Copy my text
          </Button>

          <Group gap="xs">
            <Button
              variant="default"
              size="sm"
              leftSection={<IconRefresh size={16} />}
              onClick={onReloadTheirs}
            >
              Reload theirs
            </Button>
            <Button
              color="yellow"
              size="sm"
              leftSection={<IconReplace size={16} />}
              onClick={onOverwrite}
              loading={isOverwriting}
            >
              Overwrite with mine
            </Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  );
}
