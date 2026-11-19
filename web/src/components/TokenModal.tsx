import { useState } from 'react';
import { Modal, Text, TextInput, Button, Stack, Group, Alert } from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { IconKey, IconLock } from '@tabler/icons-react';
import { getStoredToken, setStoredToken } from '../api';

export interface TokenModalProps {
  opened: boolean;
  onClose: () => void;
  onSuccess?: () => void;
}

export function TokenModal({ opened, onClose, onSuccess }: TokenModalProps) {
  const [tokenInput, setTokenInput] = useState(getStoredToken() || '');
  const [error, setError] = useState<string | null>(null);
  const isMobile = useMediaQuery('(max-width: 48em)');

  const handleSave = () => {
    const trimmed = tokenInput.trim();
    if (!trimmed) {
      setError('Token cannot be empty');
      return;
    }
    setStoredToken(trimmed);
    setError(null);
    onClose();
    if (onSuccess) onSuccess();
    window.location.reload();
  };

  const handleClear = () => {
    setStoredToken(null);
    setTokenInput('');
    setError(null);
  };

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      fullScreen={isMobile}
      title={
        <Group gap="xs">
          <IconLock size={18} />
          <Text fw={600}>Bearer Token Required</Text>
        </Group>
      }
      centered
      closeOnClickOutside={false}
    >
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          This server requires authentication. Enter your bearer token below to access the memory dashboard.
        </Text>

        {error && (
          <Alert color="red">
            {error}
          </Alert>
        )}

        <TextInput
          label="Bearer Token"
          placeholder="Paste server bearer token..."
          value={tokenInput}
          onChange={(e) => {
            setTokenInput(e.currentTarget.value);
            setError(null);
          }}
          leftSection={<IconKey size={16} />}
          data-autofocus
          size="md"
        />

        <Group justify="space-between" mt="sm">
          <Button variant="subtle" color="gray" size="md" style={{ minHeight: 44 }} onClick={handleClear}>
            Clear
          </Button>
          <Group gap="xs">
            <Button variant="default" size="md" style={{ minHeight: 44 }} onClick={onClose}>
              Cancel
            </Button>
            <Button size="md" color="accent" style={{ minHeight: 44 }} onClick={handleSave}>
              Save & Reconnect
            </Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  );
}
