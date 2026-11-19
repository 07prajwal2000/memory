import { useState } from 'react';
import {
  Stack,
  Button,
  Group,
  Text,
  NavLink,
  Skeleton,
  Box,
  Divider,
  ScrollArea,
} from '@mantine/core';
import { IconPlus, IconFileCode } from '@tabler/icons-react';
import { useNavigate, useParams, useLocation } from 'react-router-dom';
import { Context, useContexts } from '../../api';
import { ContextNavbarItem } from './ContextNavbarItem';
import { CreateContextModal } from './CreateContextModal';
import { UpdateExpiryModal } from './UpdateExpiryModal';
import { DeleteContextModal } from './DeleteContextModal';

export interface ContextsNavListProps {
  onNavigate?: () => void;
}

export function ContextsNavList({ onNavigate }: ContextsNavListProps) {
  const { data: contexts, isLoading, isError } = useContexts();
  const navigate = useNavigate();
  const location = useLocation();
  const { slug: activeSlug } = useParams<{ slug?: string }>();

  const [createOpened, setCreateOpened] = useState(false);
  const [expiryContext, setExpiryContext] = useState<Context | null>(null);
  const [deleteContext, setDeleteContext] = useState<Context | null>(null);

  const isDefaultFileActive = location.pathname === '/default';

  const globalContext = contexts?.find((c) => c.is_global);
  const otherContexts = contexts?.filter((c) => !c.is_global) ?? [];

  const handleSelectContext = (slug: string) => {
    navigate(`/c/${slug}`);
    if (onNavigate) onNavigate();
  };

  const handleSelectDefaultFile = () => {
    navigate('/default');
    if (onNavigate) onNavigate();
  };

  return (
    <Box display="flex" style={{ flexDirection: 'column', height: '100%' }}>
      <Group justify="space-between" px="xs" pb="xs">
        <Text size="xs" fw={700} c="dimmed" tt="uppercase" lts={1}>
          Contexts
        </Text>
        <Button
          size="xs"
          variant="light"
          color="accent"
          style={{ minHeight: 36 }}
          leftSection={<IconPlus size={14} />}
          onClick={() => setCreateOpened(true)}
        >
          New
        </Button>
      </Group>

      <ScrollArea style={{ flex: 1 }} offsetScrollbars scrollbarSize={6}>
        <Stack gap={4} px={4}>
          {isLoading && (
            <Stack gap={8} p="xs">
              <Skeleton height={44} radius="sm" />
              <Skeleton height={44} radius="sm" />
              <Skeleton height={44} radius="sm" />
              <Skeleton height={44} radius="sm" />
            </Stack>
          )}

          {isError && (
            <Text size="xs" c="red" p="xs">
              Failed to load contexts
            </Text>
          )}

          {globalContext && (
            <>
              {/* Pinned Global Context */}
              <ContextNavbarItem
                context={globalContext}
                isActive={activeSlug === 'global' && !isDefaultFileActive}
                onSelect={handleSelectContext}
                onOpenExpiry={setExpiryContext}
                onOpenDelete={setDeleteContext}
              />

              {/* Pinned Default File entry: global/readme.md */}
              <NavLink
                active={isDefaultFileActive}
                onClick={handleSelectDefaultFile}
                label="Default file"
                description="global/readme.md"
                leftSection={
                  <IconFileCode size={18} stroke={1.5} color="var(--mantine-color-accent-6)" />
                }
                styles={{
                  root: {
                    borderRadius: '6px',
                    minHeight: '44px',
                    padding: '6px 10px 6px 16px',
                    fontSize: '13px',
                    display: 'flex',
                    alignItems: 'center',
                  },
                  description: {
                    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
                    fontSize: '11px',
                  },
                }}
              />

              <Divider my={4} opacity={0.4} />
            </>
          )}

          {/* Other Contexts */}
          {otherContexts.map((ctx) => (
            <ContextNavbarItem
              key={ctx.slug}
              context={ctx}
              isActive={activeSlug === ctx.slug}
              onSelect={handleSelectContext}
              onOpenExpiry={setExpiryContext}
              onOpenDelete={setDeleteContext}
            />
          ))}

          {contexts && contexts.length === 0 && (
            <Text size="xs" c="dimmed" p="xs" ta="center">
              No contexts found
            </Text>
          )}
        </Stack>
      </ScrollArea>

      <CreateContextModal opened={createOpened} onClose={() => setCreateOpened(false)} />

      <UpdateExpiryModal
        context={expiryContext}
        opened={Boolean(expiryContext)}
        onClose={() => setExpiryContext(null)}
      />

      <DeleteContextModal
        context={deleteContext}
        opened={Boolean(deleteContext)}
        onClose={() => setDeleteContext(null)}
      />
    </Box>
  );
}
