/**
 * Validation helpers mirroring .prompts/_api.md contract rules.
 */

export const SLUG_REGEX = /^[a-z0-9]+(-[a-z0-9]+)*$/;
export const MEMORY_NAME_REGEX = /^[a-z0-9._/-]+$/;
export const MAX_BODY_BYTES = 1024 * 1024; // 1 MiB

export function isReservedContext(slug: string): boolean {
  return slug.trim().toLowerCase() === 'global';
}

export function validateSlug(rawSlug: string): { valid: boolean; error?: string } {
  const slug = rawSlug.trim();
  if (!slug) {
    return { valid: false, error: 'Context slug cannot be empty' };
  }
  if (slug.length > 32) {
    return { valid: false, error: 'Context slug must be 1–32 characters' };
  }
  if (isReservedContext(slug)) {
    return { valid: false, error: '"global" is a reserved context slug' };
  }
  if (!SLUG_REGEX.test(slug)) {
    return {
      valid: false,
      error: 'Slug must contain only lowercase letters, numbers, and single hyphens (e.g. "my-project")',
    };
  }
  return { valid: true };
}

export function normalizeMemoryName(name: string): string {
  const trimmed = name.trim().toLowerCase();
  if (!trimmed) return '';
  // Ensure no leading or trailing slashes, and no double slashes
  const cleaned = trimmed.replace(/\/+/g, '/').replace(/^\/|\/$/g, '');
  if (!cleaned) return '';
  return cleaned.endsWith('.md') ? cleaned : `${cleaned}.md`;
}

export function validateMemoryName(rawName: string): { valid: boolean; error?: string } {
  const name = rawName.trim().toLowerCase();
  if (!name) {
    // Memory name is optional per contract
    return { valid: true };
  }
  if (name.length > 200) {
    return { valid: false, error: 'Memory name must not exceed 200 characters' };
  }
  if (!MEMORY_NAME_REGEX.test(name)) {
    return {
      valid: false,
      error: 'Name must only contain lowercase letters, numbers, dots, underscores, hyphens, and slashes',
    };
  }
  if (name.includes('//')) {
    return { valid: false, error: 'Name cannot contain consecutive slashes' };
  }
  if (name.startsWith('/') || name.endsWith('/')) {
    return { valid: false, error: 'Name cannot start or end with a slash' };
  }
  return { valid: true };
}

export function getUtf8Bytes(str: string): number {
  return new TextEncoder().encode(str).length;
}

export function validateBodySize(body: string): { valid: boolean; error?: string; bytes: number } {
  const bytes = getUtf8Bytes(body);
  if (bytes > MAX_BODY_BYTES) {
    const mb = (bytes / (1024 * 1024)).toFixed(2);
    return {
      valid: false,
      error: `Content size (${mb} MiB) exceeds the maximum allowed 1.00 MiB limit`,
      bytes,
    };
  }
  return { valid: true, bytes };
}

export function isDefaultFile(context: string, name?: string): boolean {
  if (context !== 'global') return false;
  if (!name) return false;
  const normalized = name.toLowerCase().replace(/^\//, '');
  return normalized === 'readme.md';
}
