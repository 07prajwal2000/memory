import { useState, useEffect } from 'react';
import {
  Modal,
  TextInput,
  Switch,
  NumberInput,
  Button,
  Stack,
  Group,
} from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { notifications } from '@mantine/notifications';
import { useNavigate } from 'react-router-dom';
import { useCreateContext, useServerStatus } from '../../api';
import { validateSlug } from '../../lib/validation';

export interface CreateContextModalProps {
  opened: boolean;
  onClose: () => void;
}

export function CreateContextModal({ opened, onClose }: CreateContextModalProps) {
  const [slug, setSlug] = useState('');
  const [isTemp, setIsTemp] = useState(false);
  const [expiryDays, setExpiryDays] = useState<number>(7);
  const [slugError, setSlugError] = useState<string | null>(null);

  const isMobile = useMediaQuery('(max-width: 48em)');
  const { data: serverStatus } = useServerStatus();
  const createMutation = useCreateContext();
  const navigate = useNavigate();

  useEffect(() => {
    if (opened) {
      setSlug('');
      setIsTemp(false);
      setSlugError(null);
      if (serverStatus?.config?.default_temp_expiry_days) {
        setExpiryDays(serverStatus.config.default_temp_expiry_days);
      } else {
        setExpiryDays(7);
      }
    }
  }, [opened, serverStatus]);

  const handleSlugChange = (val: string) => {
    setSlug(val);
    const trimmed = val.trim();
    if (!trimmed) {
      setSlugError(null);
      return;
    }
    const res = validateSlug(trimmed);
    setSlugError(res.valid ? null : res.error || 'Invalid slug');
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = slug.trim();
    if (trimmed) {
      const res = validateSlug(trimmed);
      if (!res.valid) {
        setSlugError(res.error || 'Invalid slug');
        return;
      }
    }

    try {
      const created = await createMutation.mutateAsync({
        slug: trimmed || undefined,
        temp: isTemp || !trimmed,
        expiry_days: isTemp || !trimmed ? expiryDays : null,
      });

      notifications.show({
        title: 'Context created',
        message: `Context "${created.slug}" is ready.`,
        color: 'green',
      });

      onClose();
      navigate(`/c/${created.slug}`);
    } catch (err: unknown) {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const message = (err as any)?.message || 'Failed to create context';
      setSlugError(message);
    }
  };

  const isAutoNamed = !slug.trim();

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      fullScreen={isMobile}
      title="Create New Context"
      centered
      size="md"
    >
      <form onSubmit={handleSubmit}>
        <Stack gap="md">
          <TextInput
            label="Context Slug"
            description="Lowercase letters, numbers, and hyphens (max 32). Leave empty for an auto-generated temporary context."
            placeholder="e.g. backend-api or leave empty"
            value={slug}
            onChange={(e) => handleSlugChange(e.currentTarget.value)}
            error={slugError}
            data-autofocus
            size="md"
          />

          <Switch
            label="Temporary context (auto-expires)"
            description={
              isAutoNamed
                ? 'Auto-generated contexts are always temporary'
                : 'Automatically deletes after the specified number of days'
            }
            checked={isTemp || isAutoNamed}
            disabled={isAutoNamed}
            onChange={(e) => setIsTemp(e.currentTarget.checked)}
            size="md"
          />

          {(isTemp || isAutoNamed) && (
            <NumberInput
              label="Expiry duration (days)"
              description="Default from server config"
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
              disabled={createMutation.isPending}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              color="accent"
              size="md"
              style={{ minHeight: 44 }}
              loading={createMutation.isPending}
              disabled={Boolean(slugError)}
            >
              {isAutoNamed ? 'Create Auto Temp Context' : 'Create Context'}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
}
