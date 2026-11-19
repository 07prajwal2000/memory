import {
  NavLink,
  Badge,
  Group,
  Menu,
  ActionIcon,
  Tooltip,
} from '@mantine/core';
import {
  IconFolder,
  IconDotsVertical,
  IconClock,
  IconTrash,
  IconWorld,
} from '@tabler/icons-react';
import { Context } from '../../api';
import { ExpiryBadge } from './ExpiryBadge';

export interface ContextNavbarItemProps {
  context: Context;
  isActive: boolean;
  onSelect: (slug: string) => void;
  onOpenExpiry: (context: Context) => void;
  onOpenDelete: (context: Context) => void;
}

export function ContextNavbarItem({
  context,
  isActive,
  onSelect,
  onOpenExpiry,
  onOpenDelete,
}: ContextNavbarItemProps) {
  const isGlobal = context.is_global;

  return (
    <NavLink
      active={isActive}
      onClick={() => onSelect(context.slug)}
      label={context.slug}
      leftSection={
        isGlobal ? (
          <IconWorld size={20} stroke={1.5} color="var(--mantine-color-accent-6)" />
        ) : (
          <IconFolder size={20} stroke={1.5} />
        )
      }
      rightSection={
        <Group gap={6} wrap="nowrap" onClick={(e) => e.stopPropagation()}>
          {context.expires_at && <ExpiryBadge expiresAt={context.expires_at} />}
          <Badge size="xs" variant="subtle" color="gray">
            {context.memory_count}
          </Badge>

          <Menu position="bottom-end" shadow="md" width={180}>
            <Menu.Target>
              <ActionIcon
                variant="subtle"
                color="gray"
                size="lg"
                style={{ minWidth: 44, minHeight: 44 }}
                aria-label={`Options for ${context.slug}`}
              >
                <IconDotsVertical size={16} />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              {isGlobal ? (
                <>
                  <Tooltip label="Global context cannot be expired" position="left">
                    <div>
                      <Menu.Item leftSection={<IconClock size={16} />} disabled>
                        Change Expiry
                      </Menu.Item>
                    </div>
                  </Tooltip>
                  <Tooltip label="Global context cannot be deleted" position="left">
                    <div>
                      <Menu.Item leftSection={<IconTrash size={16} />} color="red" disabled>
                        Delete Context
                      </Menu.Item>
                    </div>
                  </Tooltip>
                </>
              ) : (
                <>
                  <Menu.Item
                    leftSection={<IconClock size={16} />}
                    onClick={() => onOpenExpiry(context)}
                  >
                    Change Expiry
                  </Menu.Item>
                  <Menu.Divider />
                  <Menu.Item
                    leftSection={<IconTrash size={16} />}
                    color="red"
                    onClick={() => onOpenDelete(context)}
                  >
                    Delete Context
                  </Menu.Item>
                </>
              )}
            </Menu.Dropdown>
          </Menu>
        </Group>
      }
      styles={{
        root: {
          borderRadius: '6px',
          minHeight: '44px',
          padding: '6px 10px',
          fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
          fontSize: '13px',
          display: 'flex',
          alignItems: 'center',
        },
      }}
    />
  );
}
