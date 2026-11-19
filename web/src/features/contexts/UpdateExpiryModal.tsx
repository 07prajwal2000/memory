import { useState, useEffect } from 'react';
import { Modal, Switch, NumberInput, Button, Stack, Group, Text } from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { notifications } from '@mantine/notifications';
import { Context, useUpdateContext } from '../../api';

export interface UpdateExpiryModalProps {
  context: Context | null;
  opened: boolean;
  onClose: () => void;
}

export function UpdateExpiryModal({ context, opened, onClose }: UpdateExpiryModalProps) {
  const [isPermanent, setIsPermanent] = useState(true);
  const [expiryDays, setExpiryDays] = useState<number>(7);
  const isMobile = useMediaQuery('(max-width: 48em)');

  const updateMutation = useUpdateContext();

  useEffect(() => {
    if (context && opened) {
      setIsPermanent(!context.expires_at);
      setExpiryDays(7);
    }
  }, [context, opened]);

  if (!context) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await updateMutation.mutateAsync({
        slug: context.slug,
        data: {
          expiry_days: isPermanent ? null : expiryDays,
        },
      });

      notifications.show({
        title: 'Expiry updated',
        message: isPermanent
          ? `Context "${context.slug}" is now permanent.`
          : `Context "${context.slug}" will expire in ${expiryDays} days.`,
        color: 'green',
      });

      onClose();
    } catch (err: unknown) {
      notifications.show({
        title: 'Failed to update expiry',
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        message: (err as any)?.message || 'Update failed',
        color: 'red',
      });
    }
  };

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      fullScreen={isMobile}
      title={`Update Expiry: ${context.slug}`}
      centered
      size="sm"
    >
      <form onSubmit={handleSubmit}>
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            Manage retention for context <code>{context.slug}</code>.
          </Text>

          <Switch
            label="Make context permanent (never auto-deletes)"
            checked={isPermanent}
            onChange={(e) => setIsPermanent(e.currentTarget.checked)}
            size="md"
          />

          {!isPermanent && (
            <NumberInput
              label="Extend/set expiry duration (days from now)"
              min={1}
              max={365}
              value={expiryDays}
              onChange={(val) => setExpiryDays(typeof val === 'number' ? val : 7)}
              size="md"
            />
          )}

          <Group justify="flex-end" mt="md">
            <Button
              variant="default"
              size="md"
              style={{ minHeight: 44 }}
              onClick={onClose}
              disabled={updateMutation.isPending}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              color="accent"
              size="md"
              style={{ minHeight: 44 }}
              loading={updateMutation.isPending}
            >
              Save Changes
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
}
