import { useState, useEffect, useMemo, useCallback } from 'react';
import {
  Stack,
  Group,
  TextInput,
  TagsInput,
  Button,
  SegmentedControl,
  Paper,
  Box,
  Alert,
  Skeleton,
  Breadcrumbs,
  Anchor,
  Text,
} from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { notifications } from '@mantine/notifications';
import {
  IconDeviceFloppy,
  IconTrash,
  IconArrowLeft,
  IconAlertTriangle,
  IconFilePlus,
} from '@tabler/icons-react';
import { useParams, useNavigate } from 'react-router-dom';
import { useMemoryDetail, useUpsertMemory, useDeleteMemory, fetchMemory } from '../../api';
import { validateBodySize, validateMemoryName, isDefaultFile } from '../../lib/validation';
import { useKeyboardShortcut } from '../../hooks';
import { CodeMirrorEditor } from './CodeMirrorEditor';
import { MarkdownPreview } from './MarkdownPreview';
import { MemoryMetadataBar } from './MemoryMetadataBar';
import { ConflictDialog } from './ConflictDialog';
import { AppendMemoryModal } from './AppendMemoryModal';
import { ConfirmModal, IndexWarningBanner, ErrorState } from '../../components';

export function MemoryEditorView() {
  const { slug = 'global', id } = useParams<{ slug?: string; id?: string }>();
  const navigate = useNavigate();
  const isNew = !id;
  const isMobile = useMediaQuery('(max-width: 48em)');

  const { data: memory, isLoading, isError, refetch } = useMemoryDetail(slug, id);
  const upsertMutation = useUpsertMemory();
  const deleteMutation = useDeleteMemory();

  const [name, setName] = useState('');
  const [tags, setTags] = useState<string[]>([]);
  const [body, setBody] = useState('');
  const [initialState, setInitialState] = useState({ name: '', tags: [] as string[], body: '' });

  const [viewMode, setViewMode] = useState<'split' | 'editor' | 'preview'>('split');
  const [indexWarning, setIndexWarning] = useState<string | null>(null);

  // Modals state
  const [deleteOpened, setDeleteOpened] = useState(false);
  const [appendOpened, setAppendOpened] = useState(false);
  const [conflictState, setConflictState] = useState<{
    opened: boolean;
    serverVersion?: number;
    serverBody: string;
  }>({ opened: false, serverBody: '' });
  const [discardOpened, setDiscardOpened] = useState(false);

  // Effective view mode: on mobile, never split view (tabs Edit | Preview only)
  const effectiveViewMode = isMobile && viewMode === 'split' ? 'editor' : viewMode;

  useEffect(() => {
    if (memory && !isNew) {
      setName(memory.name || '');
      setTags(memory.tags || []);
      setBody(memory.body || '');
      setInitialState({
        name: memory.name || '',
        tags: memory.tags || [],
        body: memory.body || '',
      });
      setIndexWarning(null);
    } else if (isNew) {
      setName('');
      setTags([]);
      setBody('# Untitled\n\n');
      setInitialState({ name: '', tags: [], body: '# Untitled\n\n' });
      setIndexWarning(null);
    }
  }, [memory, isNew]);

  const isDirty = useMemo(() => {
    return (
      name !== initialState.name ||
      body !== initialState.body ||
      JSON.stringify(tags) !== JSON.stringify(initialState.tags)
    );
  }, [name, body, tags, initialState]);

  // Window unload guard
  useEffect(() => {
    const handleBeforeUnload = (e: BeforeUnloadEvent) => {
      if (isDirty) {
        e.preventDefault();
        e.returnValue = '';
      }
    };
    window.addEventListener('beforeunload', handleBeforeUnload);
    return () => window.removeEventListener('beforeunload', handleBeforeUnload);
  }, [isDirty]);

  // Size validation
  const sizeValidation = validateBodySize(body);
  const nameValidation = validateMemoryName(name);

  // Save handler
  const handleSave = useCallback(
    async (overrideVersion?: number) => {
      if (!nameValidation.valid) {
        notifications.show({ title: 'Invalid Name', message: nameValidation.error, color: 'red' });
        return;
      }
      if (!sizeValidation.valid) {
        notifications.show({ title: 'Content Too Large', message: sizeValidation.error, color: 'red' });
        return;
      }

      try {
        const payload = {
          context: slug,
          id: isNew ? undefined : id,
          name: name.trim() || undefined,
          tags,
          body,
          if_version: overrideVersion !== undefined ? overrideVersion : memory?.version,
        };

        const res = await upsertMutation.mutateAsync(payload);
        setInitialState({ name: res.name || '', tags: res.tags || [], body: res.body });
        setName(res.name || '');
        setTags(res.tags || []);
        setBody(res.body);

        if (res.index_warning) {
          setIndexWarning(res.index_warning);
        } else {
          setIndexWarning(null);
        }

        notifications.show({
          title: isNew ? 'Memory created' : 'Memory saved',
          message: `Saved ${res.name || res.id}`,
          color: 'green',
        });

        if (isNew) {
          navigate(`/c/${slug}/m/${res.id}`, { replace: true });
        }
      } catch (err: unknown) {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const error = err as any;
        if (error.status === 409 || error.kind === 'conflict') {
          let serverBody = '';
          const currentVersion = error.details?.current_version;
          try {
            if (id) {
              const latest = await fetchMemory({ context: slug, id });
              serverBody = latest.body;
            }
          } catch {
            serverBody = '(Could not load current server content)';
          }

          setConflictState({
            opened: true,
            serverVersion: currentVersion,
            serverBody,
          });
        } else {
          notifications.show({
            title: 'Save failed',
            message: error.message || 'Could not save memory',
            color: 'red',
          });
        }
      }
    },
    [nameValidation, sizeValidation, slug, isNew, id, name, tags, body, memory?.version, upsertMutation, navigate],
  );

  useKeyboardShortcut('s', () => handleSave(), { ctrlOrCmd: true, preventDefault: true });

  const handleAppend = (appendText: string) => {
    const newBody = body ? `${body}\n\n${appendText}` : appendText;
    setBody(newBody);
    notifications.show({
      title: 'Appended',
      message: 'Text added to document. Remember to save.',
      color: 'blue',
    });
  };

  const handleDelete = async () => {
    if (!id) return;
    try {
      await deleteMutation.mutateAsync({ id, context: slug });
      notifications.show({
        title: 'Memory deleted',
        message: 'The memory document was removed.',
        color: 'blue',
      });
      navigate(`/c/${slug}`);
    } catch (err: unknown) {
      notifications.show({
        title: 'Delete failed',
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        message: (err as any)?.message || 'Failed to delete memory',
        color: 'red',
      });
    }
  };

  const handleBack = () => {
    if (isDirty) {
      setDiscardOpened(true);
    } else {
      navigate(`/c/${slug}`);
    }
  };

  const isDefault = !isNew && isDefaultFile(slug, memory?.name);

  if (isLoading) {
    return (
      <Box p="md" maw={1400} mx="auto">
        <Stack gap="md">
          <Skeleton height={44} width={200} />
          <Skeleton height={56} />
          <Skeleton height={400} />
        </Stack>
      </Box>
    );
  }

  if (isError && !isNew) {
    return (
      <Box p="md" maw={1400} mx="auto">
        <ErrorState
          title="Memory not found"
          message="Could not load the requested memory document."
          onRetry={() => refetch()}
          action={
            <Button
              variant="default"
              size="md"
              style={{ minHeight: 44 }}
              onClick={() => navigate(`/c/${slug}`)}
              mt="xs"
            >
              Back to context
            </Button>
          }
        />
      </Box>
    );
  }

  return (
    <Box p={{ base: 'xs', sm: 'md' }} maw={1400} mx="auto">
      <Stack gap="sm">
        {/* Breadcrumbs and Top Toolbar */}
        <Group justify="space-between" align="center" wrap="wrap" gap="xs">
          <Breadcrumbs separator="/" mb={2}>
            <Anchor size="sm" onClick={handleBack} style={{ minHeight: 32, display: 'inline-flex', alignItems: 'center' }}>
              {slug}
            </Anchor>
            <Text size="sm" c="dimmed">
              {isNew ? 'new memory' : memory?.name || `${id}.md`}
            </Text>
          </Breadcrumbs>

          <Group gap="xs" wrap="wrap">
            <Button
              variant="default"
              size="sm"
              style={{ minHeight: 44 }}
              leftSection={<IconArrowLeft size={16} />}
              onClick={handleBack}
            >
              Back
            </Button>

            {!isNew && (
              <Button
                variant="light"
                size="sm"
                color="blue"
                style={{ minHeight: 44 }}
                leftSection={<IconFilePlus size={16} />}
                onClick={() => setAppendOpened(true)}
              >
                Append
              </Button>
            )}

            {!isNew && !isDefault && (
              <Button
                variant="subtle"
                size="sm"
                color="red"
                style={{ minHeight: 44 }}
                leftSection={<IconTrash size={16} />}
                onClick={() => setDeleteOpened(true)}
              >
                Delete
              </Button>
            )}

            <Button
              color="accent"
              size="sm"
              style={{ minHeight: 44 }}
              leftSection={<IconDeviceFloppy size={16} />}
              onClick={() => handleSave()}
              loading={upsertMutation.isPending}
              disabled={!sizeValidation.valid || !nameValidation.valid}
            >
              Save {isMobile ? '' : '(Ctrl+S)'}
            </Button>
          </Group>
        </Group>

        {/* Warning Banners */}
        {indexWarning && (
          <IndexWarningBanner warning={indexWarning} onDismiss={() => setIndexWarning(null)} />
        )}

        {!sizeValidation.valid && (
          <Alert color="red" icon={<IconAlertTriangle size={16} />} title="Size limit exceeded">
            {sizeValidation.error}
          </Alert>
        )}

        {/* Name and Tags inputs */}
        <Paper withBorder p="sm" radius="md">
          <Stack gap="xs">
            <Box
              style={{
                display: 'grid',
                gridTemplateColumns: isMobile ? '1fr' : '1fr 1fr',
                gap: '12px',
              }}
            >
              <TextInput
                label="Memory Name (relative path)"
                placeholder="e.g. guidelines/coding-standards.md"
                value={name}
                onChange={(e) => setName(e.currentTarget.value)}
                error={nameValidation.valid ? undefined : nameValidation.error}
                size="md"
                style={{ fontFamily: 'ui-monospace, monospace' }}
              />

              <TagsInput
                label="Tags"
                placeholder="Add tags..."
                value={tags}
                onChange={setTags}
                size="md"
                clearable
              />
            </Box>

            <MemoryMetadataBar
              id={id}
              version={memory?.version}
              updatedAt={memory?.updated_at}
              updatedBy={memory?.updated_by}
              bodyBytes={sizeValidation.bytes}
              isDirty={isDirty}
            />
          </Stack>
        </Paper>

        {/* View Mode Toggle: Mobile shows Edit | Preview tabs; Desktop shows Split | Editor | Preview */}
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

      <AppendMemoryModal
        opened={appendOpened}
        onClose={() => setAppendOpened(false)}
        onAppend={handleAppend}
      />

      <ConfirmModal
        opened={deleteOpened}
        onClose={() => setDeleteOpened(false)}
        onConfirm={handleDelete}
        title="Delete Memory"
        message={`Are you sure you want to delete "${name || id}"? This action cannot be undone.`}
        confirmLabel="Delete"
        color="red"
        isLoading={deleteMutation.isPending}
      />

      <ConfirmModal
        opened={discardOpened}
        onClose={() => setDiscardOpened(false)}
        onConfirm={() => {
          setDiscardOpened(false);
          navigate(`/c/${slug}`);
        }}
        title="Discard Unsaved Changes?"
        message="You have unsaved edits in this memory. Are you sure you want to navigate away and lose them?"
        confirmLabel="Discard & Leave"
        cancelLabel="Stay Here"
        color="yellow"
      />

      <ConflictDialog
        opened={conflictState.opened}
        serverVersion={conflictState.serverVersion}
        serverBody={conflictState.serverBody}
        myBody={body}
        onReloadTheirs={() => {
          setBody(conflictState.serverBody);
          setInitialState((prev) => ({ ...prev, body: conflictState.serverBody }));
          setConflictState({ opened: false, serverBody: '' });
          notifications.show({
            title: 'Reloaded',
            message: 'Loaded the current server version into editor.',
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
