import { useState, useEffect, useMemo, useCallback } from 'react';
import {
  Stack,
  Group,
  Button,
  SegmentedControl,
  Paper,
  Box,
  Alert,
  Skeleton,
  Title,
  Text,
} from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { notifications } from '@mantine/notifications';
import {
  IconDeviceFloppy,
  IconInfoCircle,
  IconLock,
} from '@tabler/icons-react';
import { useDefaultMemory, useUpsertMemory } from '../../api';
import { validateBodySize } from '../../lib/validation';
import { useKeyboardShortcut } from '../../hooks';
import { CodeMirrorEditor, MarkdownPreview } from '../memories';
import { MemoryMetadataBar } from '../memories/MemoryMetadataBar';
import { ConflictDialog } from '../memories/ConflictDialog';
import { IndexWarningBanner, ErrorState } from '../../components';

export function DefaultFileView() {
  const { data: defaultMem, isLoading, isError, refetch } = useDefaultMemory();
  const upsertMutation = useUpsertMemory();
  const isMobile = useMediaQuery('(max-width: 48em)');

  const [body, setBody] = useState('');
  const [initialBody, setInitialBody] = useState('');
  const [viewMode, setViewMode] = useState<'split' | 'editor' | 'preview'>('split');
  const [indexWarning, setIndexWarning] = useState<string | null>(null);

  const effectiveViewMode = isMobile && viewMode === 'split' ? 'editor' : viewMode;

  const [conflictState, setConflictState] = useState<{
    opened: boolean;
    serverVersion?: number;
    serverBody: string;
  }>({ opened: false, serverBody: '' });

  useEffect(() => {
    if (defaultMem) {
      setBody(defaultMem.body || '');
      setInitialBody(defaultMem.body || '');
      setIndexWarning(null);
    }
  }, [defaultMem]);

  const isDirty = useMemo(() => body !== initialBody, [body, initialBody]);

  const sizeValidation = validateBodySize(body);

  const handleSave = useCallback(
    async (overrideVersion?: number) => {
      if (!sizeValidation.valid) {
        notifications.show({ title: 'Content Too Large', message: sizeValidation.error, color: 'red' });
        return;
      }

      try {
        const res = await upsertMutation.mutateAsync({
          context: 'global',
          name: 'readme.md',
          tags: defaultMem?.tags || ['system', 'instructions'],
          body,
          if_version: overrideVersion !== undefined ? overrideVersion : defaultMem?.version,
        });

        setInitialBody(res.body);
        setBody(res.body);

        if (res.index_warning) {
          setIndexWarning(res.index_warning);
        } else {
          setIndexWarning(null);
        }

        notifications.show({
          title: 'Default file updated',
          message: 'Saved global/readme.md',
          color: 'green',
        });
      } catch (err: unknown) {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const error = err as any;
        if (error.status === 409 || error.kind === 'conflict') {
          setConflictState({
            opened: true,
            serverVersion: error.details?.current_version,
            serverBody: defaultMem?.body || '',
          });
        } else {
          notifications.show({
            title: 'Save failed',
            message: error.message || 'Could not save default file',
            color: 'red',
          });
        }
      }
    },
    [sizeValidation, defaultMem, body, upsertMutation],
  );

  useKeyboardShortcut('s', () => handleSave(), { ctrlOrCmd: true, preventDefault: true });

  if (isLoading) {
    return (
      <Box p="md" maw={1400} mx="auto">
        <Stack gap="md">
          <Skeleton height={44} width={280} />
          <Skeleton height={60} />
          <Skeleton height={500} />
        </Stack>
      </Box>
    );
  }

  if (isError) {
    return (
      <Box p="md" maw={1400} mx="auto">
        <ErrorState
          title="Could not load default file"
          message="Failed to fetch global/readme.md instructions."
          onRetry={() => refetch()}
        />
      </Box>
    );
  }

  return (
    <Box p={{ base: 'xs', sm: 'md' }} maw={1400} mx="auto">
      <Stack gap="sm">
        {/* Header Title and Actions */}
        <Group justify="space-between" align="center" wrap="wrap" gap="xs">
          <Box>
            <Group gap="xs" wrap="wrap">
              <Title order={2} fw={700}>
                Default File
              </Title>
              <Text size="sm" c="dimmed" style={{ fontFamily: 'ui-monospace, monospace' }}>
                global/readme.md
              </Text>
            </Group>
          </Box>

          <Group gap="xs">
            <Button
              color="accent"
              size="sm"
              style={{ minHeight: 44 }}
              leftSection={<IconDeviceFloppy size={16} />}
              onClick={() => handleSave()}
              loading={upsertMutation.isPending}
              disabled={!sizeValidation.valid}
            >
              Save {isMobile ? '' : '(Ctrl+S)'}
            </Button>
          </Group>
        </Group>

        {/* Pinned Explainer Banner */}
        <Alert
          color="blue"
          variant="light"
          icon={<IconInfoCircle size={18} />}
          title="Agent Default Instructions"
        >
          <Text size="sm">
            AI agents load this file at the start of every session to discover conventions, project
            guidelines, and operational instructions. This file is permanently pinned in the{' '}
            <strong>global</strong> context and cannot be deleted.
          </Text>
        </Alert>

        {indexWarning && (
          <IndexWarningBanner warning={indexWarning} onDismiss={() => setIndexWarning(null)} />
        )}

        {/* Metadata info */}
        <Paper withBorder p="xs" radius="md">
          <Group justify="space-between" wrap="wrap">
            <Group gap="xs">
              <IconLock size={14} color="var(--mantine-color-dimmed)" />
              <Text size="xs" c="dimmed">
                Protected system file
              </Text>
            </Group>

            <MemoryMetadataBar
              id={defaultMem?.id}
              version={defaultMem?.version}
              updatedAt={defaultMem?.updated_at}
              updatedBy={defaultMem?.updated_by}
              bodyBytes={sizeValidation.bytes}
              isDirty={isDirty}
            />
          </Group>
        </Paper>

        {/* View Mode Toggle */}
        <Group justify={isMobile ? 'center' : 'flex-end'}>
          <SegmentedControl
            value={effectiveViewMode}
            onChange={(val) => setViewMode(val as 'split' | 'editor' | 'preview')}
            data={
              isMobile
                ? [
                    { value: 'editor', label: 'Edit' },
                    { value: 'preview', label: 'Preview' },
                  ]
                : [
                    { value: 'split', label: 'Split' },
                    { value: 'editor', label: 'Editor' },
                    { value: 'preview', label: 'Preview' },
                  ]
            }
            size="sm"
            style={{ minHeight: 38 }}
          />
        </Group>

        {/* Editor and Preview Layout */}
        <Box style={{ width: '100%', overflowX: 'hidden' }}>
          {effectiveViewMode === 'split' ? (
            <Box
              style={{
                display: 'grid',
                gridTemplateColumns: '1fr 1fr',
                gap: '12px',
                minHeight: '520px',
              }}
            >
              <CodeMirrorEditor value={body} onChange={setBody} height="520px" />
              <MarkdownPreview content={body} />
            </Box>
          ) : effectiveViewMode === 'editor' ? (
            <CodeMirrorEditor value={body} onChange={setBody} height={isMobile ? '400px' : '560px'} />
          ) : (
            <MarkdownPreview content={body} />
          )}
        </Box>
      </Stack>

      <ConflictDialog
        opened={conflictState.opened}
        serverVersion={conflictState.serverVersion}
        serverBody={conflictState.serverBody}
        myBody={body}
        onReloadTheirs={() => {
          setBody(conflictState.serverBody);
          setInitialBody(conflictState.serverBody);
          setConflictState({ opened: false, serverBody: '' });
          notifications.show({
            title: 'Reloaded',
            message: 'Loaded the latest server version into editor.',
            color: 'blue',
          });
        }}
        onOverwrite={() => {
          setConflictState({ opened: false, serverBody: '' });
          handleSave(conflictState.serverVersion);
        }}
        onClose={() => setConflictState({ opened: false, serverBody: '' })}
      />
    </Box>
  );
}
