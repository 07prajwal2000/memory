import { useState } from 'react';
import { Modal, Textarea, Button, Group, Stack, Text } from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { IconFilePlus } from '@tabler/icons-react';

export interface AppendMemoryModalProps {
  opened: boolean;
  onClose: () => void;
  onAppend: (textToAppend: string) => void;
  isLoading?: boolean;
}

export function AppendMemoryModal({
  opened,
  onClose,
  onAppend,
  isLoading = false,
}: AppendMemoryModalProps) {
  const [appendText, setAppendText] = useState('');
  const isMobile = useMediaQuery('(max-width: 48em)');

  const handleConfirm = () => {
    if (!appendText.trim()) return;
    onAppend(appendText);
    setAppendText('');
    onClose();
  };

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      fullScreen={isMobile}
      title={
        <Group gap="xs">
          <IconFilePlus size={18} />
          <Text fw={600}>Append to Memory</Text>
        </Group>
      }
      centered
      size="md"
    >
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          Text entered below will be appended to the end of the memory body with a leading newline.
        </Text>

        <Textarea
          placeholder="Enter content to append..."
          value={appendText}
          onChange={(e) => setAppendText(e.currentTarget.value)}
          rows={6}
          data-autofocus
          size="md"
        />

        <Group justify="flex-end" gap="xs">
          <Button
            variant="default"
            size="md"
            style={{ minHeight: 44 }}
            onClick={onClose}
            disabled={isLoading}
          >
            Cancel
          </Button>
          <Button
            color="accent"
            size="md"
            style={{ minHeight: 44 }}
            onClick={handleConfirm}
            loading={isLoading}
            disabled={!appendText.trim()}
          >
            Append Text
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
