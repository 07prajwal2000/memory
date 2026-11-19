/**
 * Formatting utilities for dates, sizes, and expiry.
 */

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const kb = bytes / 1024;
  if (kb < 1024) return `${kb.toFixed(1)} KB`;
  const mb = kb / 1024;
  return `${mb.toFixed(2)} MB`;
}

export function formatDateTime(isoString: string): string {
  try {
    const d = new Date(isoString);
    if (isNaN(d.getTime())) return isoString;
    return d.toLocaleString(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    });
  } catch {
    return isoString;
  }
}

export function formatRelativeTime(isoString: string, baseTime = new Date()): string {
  try {
    const d = new Date(isoString);
    if (isNaN(d.getTime())) return isoString;

    const diffMs = baseTime.getTime() - d.getTime();
    const diffSec = Math.floor(diffMs / 1000);
    const diffMin = Math.floor(diffSec / 60);
    const diffHours = Math.floor(diffMin / 60);
    const diffDays = Math.floor(diffHours / 24);

    if (diffSec < 45) return 'just now';
    if (diffMin < 60) return `${diffMin}m ago`;
    if (diffHours < 24) return `${diffHours}h ago`;
    if (diffDays === 1) return 'yesterday';
    if (diffDays < 30) return `${diffDays}d ago`;

    return d.toLocaleDateString();
  } catch {
    return isoString;
  }
}

export function formatExpiryTime(
  expiresAt: string | null,
  baseTime = new Date(),
): {
  label: string;
  isExpiringSoon: boolean;
  isExpired: boolean;
  remainingHours: number;
} {
  if (!expiresAt) {
    return {
      label: 'permanent',
      isExpiringSoon: false,
      isExpired: false,
      remainingHours: Infinity,
    };
  }

  try {
    const expiry = new Date(expiresAt);
    if (isNaN(expiry.getTime())) {
      return {
        label: 'invalid expiry',
        isExpiringSoon: false,
        isExpired: false,
        remainingHours: 0,
      };
    }

    const diffMs = expiry.getTime() - baseTime.getTime();
    if (diffMs <= 0) {
      return {
        label: 'expired',
        isExpiringSoon: true,
        isExpired: true,
        remainingHours: 0,
      };
    }

    const totalHours = diffMs / (1000 * 60 * 60);
    const days = Math.floor(totalHours / 24);
    const hours = Math.floor(totalHours % 24);
    const minutes = Math.floor((diffMs / (1000 * 60)) % 60);

    let label: string;
    if (days > 0) {
      label = `expires in ${days}d ${hours}h`;
    } else if (hours > 0) {
      label = `expires in ${hours}h ${minutes}m`;
    } else {
      label = `expires in ${minutes}m`;
    }

    return {
      label,
      isExpiringSoon: totalHours < 24,
      isExpired: false,
      remainingHours: totalHours,
    };
  } catch {
    return {
      label: 'unknown',
      isExpiringSoon: false,
      isExpired: false,
      remainingHours: 0,
    };
  }
}
