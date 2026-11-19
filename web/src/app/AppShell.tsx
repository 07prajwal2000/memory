import { useState, useEffect } from 'react';
import {
  AppShell as MantineAppShell,
  Burger,
  Group,
  Text,
  Box,
  UnstyledButton,
  Drawer,
  ActionIcon,
  Tooltip,
} from '@mantine/core';
import { useDisclosure, useMediaQuery } from '@mantine/hooks';
import { Outlet, useNavigate } from 'react-router-dom';
import { IconBrain, IconSearch } from '@tabler/icons-react';
import { ContextsNavList } from '../features/contexts';
import { SearchHeaderBar, SearchSpotlight, useSpotlightTrigger } from '../features/search';
import { StatusIndicator, ThemeToggle, TokenModal } from '../components';
import { setUnauthorizedListener } from '../api';

export function AppShell() {
  const [drawerOpened, { toggle: toggleDrawer, close: closeDrawer }] = useDisclosure();
  const spotlight = useSpotlightTrigger();
  const [tokenModalOpened, setTokenModalOpened] = useState(false);
  const navigate = useNavigate();
  const isMobile = useMediaQuery('(max-width: 48em)');

  useEffect(() => {
    setUnauthorizedListener(() => {
      setTokenModalOpened(true);
    });
    return () => setUnauthorizedListener(null);
  }, []);

  return (
    <MantineAppShell
      header={{ height: 56 }}
      navbar={{
        width: 280,
        breakpoint: 'sm',
        collapsed: { mobile: true },
      }}
      padding="0"
    >
      {/* Header */}
      <MantineAppShell.Header>
        <Group
          h="100%"
          px="sm"
          justify="space-between"
          wrap="nowrap"
          style={{ width: '100%', maxWidth: '100vw' }}
        >
          <Group gap={8} wrap="nowrap">
            {/* Burger for mobile: opens drawer */}
            <Box
              style={{
                minWidth: 44,
                minHeight: 44,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
              hiddenFrom="sm"
            >
              <Burger
                opened={drawerOpened}
                onClick={toggleDrawer}
                size="sm"
                aria-label="Toggle contexts navigation drawer"
              />
            </Box>

            <UnstyledButton
              onClick={() => navigate('/c/global')}
              style={{
                minHeight: 44,
                display: 'flex',
                alignItems: 'center',
              }}
            >
              <Group gap={8} wrap="nowrap">
                <Box
                  p={4}
                  style={{
                    backgroundColor: 'var(--mantine-color-accent-light)',
                    borderRadius: '6px',
                    display: 'flex',
                    alignItems: 'center',
                  }}
                >
                  <IconBrain size={20} color="var(--mantine-color-accent-6)" />
                </Box>
                <Text fw={700} size="md" style={{ fontFamily: 'ui-monospace, monospace' }}>
                  mem
                </Text>
              </Group>
            </UnstyledButton>
          </Group>

          {/* Central Spotlight Search trigger on desktop */}
          <Box
            visibleFrom="sm"
            style={{ flex: 1, maxWidth: 440, margin: '0 12px' }}
          >
            <SearchHeaderBar onClick={spotlight.open} />
          </Box>

          {/* Right Status + Search Icon on mobile + Theme */}
          <Group gap={4} wrap="nowrap">
            {/* Mobile search button */}
            <Box hiddenFrom="sm">
              <Tooltip label="Search (Ctrl+K)">
                <ActionIcon
                  variant="default"
                  size="lg"
                  style={{ minWidth: 44, minHeight: 44 }}
                  onClick={spotlight.open}
                  aria-label="Open search spotlight"
                >
                  <IconSearch size={18} />
                </ActionIcon>
              </Tooltip>
            </Box>

            <StatusIndicator />
            <ThemeToggle />
          </Group>
        </Group>
      </MantineAppShell.Header>

      {/* Desktop Navbar: static sidebar (visibleFrom="sm") */}
      <MantineAppShell.Navbar p="xs" visibleFrom="sm">
        <ContextsNavList />
      </MantineAppShell.Navbar>

      {/* Mobile Drawer (< sm): slides out on burger click */}
      <Drawer
        opened={drawerOpened}
        onClose={closeDrawer}
        size={isMobile ? '85%' : 300}
        padding="md"
        title={
          <Group gap={8}>
            <IconBrain size={20} color="var(--mantine-color-accent-6)" />
            <Text fw={700} style={{ fontFamily: 'ui-monospace, monospace' }}>
              mem contexts
            </Text>
          </Group>
        }
        hiddenFrom="sm"
      >
        <ContextsNavList onNavigate={closeDrawer} />
      </Drawer>

      {/* Main Content Area */}
      <MantineAppShell.Main
        style={{
          width: '100%',
          maxWidth: '100vw',
          overflowX: 'hidden',
        }}
      >
        <Outlet />
      </MantineAppShell.Main>

      {/* Global Spotlight Search Modal */}
      <SearchSpotlight opened={spotlight.opened} onClose={spotlight.close} />

      {/* Bearer Token Authentication Modal */}
      <TokenModal
        opened={tokenModalOpened}
        onClose={() => setTokenModalOpened(false)}
      />
    </MantineAppShell>
  );
}
