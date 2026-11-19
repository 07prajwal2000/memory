import { Badge, Tooltip } from '@mantine/core';
import { formatExpiryTime, formatDateTime } from '../../lib/formatters';

export interface ExpiryBadgeProps {
  expiresAt: string | null;
  size?: 'xs' | 'sm' | 'md';
}

export function ExpiryBadge({ expiresAt, size = 'xs' }: ExpiryBadgeProps) {
  if (!expiresAt) return null;

  const { label, isExpiringSoon, isExpired } = formatExpiryTime(expiresAt);

  let color = 'gray';
  if (isExpired) {
    color = 'red';
  } else if (isExpiringSoon) {
    color = 'yellow';
  }

  return (
    <Tooltip label={`Expires: ${formatDateTime(expiresAt)}`} withArrow position="top">
      <Badge size={size} color={color} variant="light">
        {label}
      </Badge>
    </Tooltip>
  );
}
