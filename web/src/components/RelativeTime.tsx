import { Tooltip, Text } from '@mantine/core';
import { formatDateTime, formatRelativeTime } from '../lib/formatters';

export interface RelativeTimeProps {
  isoString: string;
  size?: 'xs' | 'sm' | 'md' | 'lg';
  c?: string;
}

export function RelativeTime({ isoString, size = 'xs', c = 'dimmed' }: RelativeTimeProps) {
  const relative = formatRelativeTime(isoString);
  const full = formatDateTime(isoString);

  return (
    <Tooltip label={full} withArrow position="top">
      <Text component="span" size={size} c={c}>
        {relative}
      </Text>
    </Tooltip>
  );
}
