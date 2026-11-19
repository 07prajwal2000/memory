import { SegmentedControl, Tooltip, Center, VisuallyHidden } from '@mantine/core';
import { IconFolderOpen, IconList } from '@tabler/icons-react';

export type ViewMode = 'tree' | 'flat';

export interface ViewModeToggleProps {
  value: ViewMode;
  onChange: (value: ViewMode) => void;
}

export function ViewModeToggle({ value, onChange }: ViewModeToggleProps) {
  return (
    <SegmentedControl
      value={value}
      onChange={(val) => onChange(val as ViewMode)}
      size="sm"
      aria-label="View mode"
      data={[
        {
          value: 'tree',
          label: (
            <Tooltip label="Tree view" openDelay={400}>
              <Center aria-label="Tree view" style={{ height: '100%', minWidth: 28, padding: '0 4px' }}>
                <IconFolderOpen size={16} stroke={1.5} />
                <VisuallyHidden>Tree view</VisuallyHidden>
              </Center>
            </Tooltip>
          ),
        },
        {
          value: 'flat',
          label: (
            <Tooltip label="Flat list" openDelay={400}>
              <Center aria-label="Flat list" style={{ height: '100%', minWidth: 28, padding: '0 4px' }}>
                <IconList size={16} stroke={1.5} />
                <VisuallyHidden>Flat list</VisuallyHidden>
              </Center>
            </Tooltip>
          ),
        },
      ]}
      style={{
        flexShrink: 0,
      }}
    />
  );
}
