import { useState, useEffect } from 'react';
import { Modal, Text, Button, Group, TextInput, Stack } from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';

export interface ConfirmModalProps {
  opened: boolean;
  onClose: () => void;
  onConfirm: () => void;
  title: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  color?: string;
  isLoading?: boolean;
  /**
   * If provided, user must type this exact string to confirm
   */
  requireMatchString?: string;
  requireMatchPlaceholder?: string;
}

export function ConfirmModal({
  opened,
  onClose,
  onConfirm,
  title,
  message,
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  color = 'red',
  isLoading = false,
  requireMatchString,
  requireMatchPlaceholder = 'Type to confirm...',
}: ConfirmModalProps) {
  const [typedInput, setTypedInput] = useState('');
  const isMobile = useMediaQuery('(max-width: 48em)');

  useEffect(() => {
    if (opened) {
      setTypedInput('');
    }
  }, [opened]);

  const isMatchValid = !requireMatchString || typedInput === requireMatchString;

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={title}
      centered
      fullScreen={isMobile}
      size="sm"
    >
      <Stack gap="md">
        <Text size="sm">{message}</Text>

        {requireMatchString && (
          <TextInput
            label={`Please type "${requireMatchString}" to confirm:`}
            placeholder={requireMatchPlaceholder}
            value={typedInput}
            onChange={(e) => setTypedInput(e.currentTarget.value)}
            data-autofocus
            size="md"
          />
        )}

        <Group justify="flex-end" mt="md" gap="xs">
          <Button
            variant="default"
            size="md"
            style={{ minHeight: 44 }}
            onClick={onClose}
            disabled={isLoading}
          >
            {cancelLabel}
          </Button>
          <Button
            color={color}
            size="md"
            style={{ minHeight: 44 }}
            onClick={onConfirm}
            loading={isLoading}
            disabled={!isMatchValid}
          >
            {confirmLabel}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
