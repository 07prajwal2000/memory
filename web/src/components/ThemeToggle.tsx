import { ActionIcon, useMantineColorScheme, useComputedColorScheme, Tooltip } from '@mantine/core';
import { IconSun, IconMoon } from '@tabler/icons-react';

export function ThemeToggle() {
  const { setColorScheme } = useMantineColorScheme();
  const computedColorScheme = useComputedColorScheme('dark', {
    getInitialValueInEffect: true,
  });

  const toggle = () => {
    try {
      setColorScheme(computedColorScheme === 'dark' ? 'light' : 'dark');
    } catch {
      // Safe fallback if localStorage fails
    }
  };

  const isDark = computedColorScheme === 'dark';

  return (
    <Tooltip label={`Switch to ${isDark ? 'light' : 'dark'} mode`} withArrow>
      <ActionIcon
        variant="default"
        size="md"
        onClick={toggle}
        aria-label="Toggle color scheme"
      >
        {isDark ? <IconSun size={18} stroke={1.5} /> : <IconMoon size={18} stroke={1.5} />}
      </ActionIcon>
    </Tooltip>
  );
}
