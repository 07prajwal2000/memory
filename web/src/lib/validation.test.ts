import { describe, it, expect } from 'vitest';
import {
  validateSlug,
  validateMemoryName,
  validateBodySize,
  isReservedContext,
  isDefaultFile,
  normalizeMemoryName,
} from './validation';

describe('validation', () => {
  describe('validateSlug', () => {
    it('accepts valid slugs', () => {
      expect(validateSlug('my-project').valid).toBe(true);
      expect(validateSlug('project123').valid).toBe(true);
      expect(validateSlug('a').valid).toBe(true);
      expect(validateSlug('a-b-c-d').valid).toBe(true);
    });

    it('rejects empty or whitespace slug', () => {
      expect(validateSlug('').valid).toBe(false);
      expect(validateSlug('   ').valid).toBe(false);
    });

    it('rejects uppercase or invalid characters', () => {
      expect(validateSlug('MyProject').valid).toBe(false);
      expect(validateSlug('my_project').valid).toBe(false);
      expect(validateSlug('my.project').valid).toBe(false);
      expect(validateSlug('-project').valid).toBe(false);
      expect(validateSlug('project-').valid).toBe(false);
      expect(validateSlug('my--project').valid).toBe(false);
    });

    it('rejects slugs longer than 32 characters', () => {
      const longSlug = 'a'.repeat(33);
      expect(validateSlug(longSlug).valid).toBe(false);
    });

    it('rejects reserved slug "global"', () => {
      const res = validateSlug('global');
      expect(res.valid).toBe(false);
      expect(res.error).toContain('reserved');
    });
  });

  describe('isReservedContext', () => {
    it('identifies global context as reserved regardless of case', () => {
      expect(isReservedContext('global')).toBe(true);
      expect(isReservedContext('GLOBAL')).toBe(true);
      expect(isReservedContext('other')).toBe(false);
    });
  });

  describe('normalizeMemoryName', () => {
    it('appends .md if not present', () => {
      expect(normalizeMemoryName('notes/todo')).toBe('notes/todo.md');
      expect(normalizeMemoryName('readme')).toBe('readme.md');
    });

    it('preserves existing .md extension', () => {
      expect(normalizeMemoryName('notes/todo.md')).toBe('notes/todo.md');
    });

    it('cleans redundant slashes', () => {
      expect(normalizeMemoryName('/notes//todo/')).toBe('notes/todo.md');
    });
  });

  describe('validateMemoryName', () => {
    it('allows empty name (as name is optional per contract)', () => {
      expect(validateMemoryName('').valid).toBe(true);
      expect(validateMemoryName('  ').valid).toBe(true);
    });

    it('accepts valid paths with slashes and extensions', () => {
      expect(validateMemoryName('guidelines/coding.md').valid).toBe(true);
      expect(validateMemoryName('a/b/c/d/file.md').valid).toBe(true);
      expect(validateMemoryName('readme.md').valid).toBe(true);
      expect(validateMemoryName('user_auth.spec-1.md').valid).toBe(true);
    });

    it('rejects consecutive or leading/trailing slashes', () => {
      expect(validateMemoryName('/leading.md').valid).toBe(false);
      expect(validateMemoryName('trailing.md/').valid).toBe(false);
      expect(validateMemoryName('a//b.md').valid).toBe(false);
    });

    it('rejects names exceeding 200 characters', () => {
      const longName = 'a/'.repeat(101) + 'file.md';
      expect(validateMemoryName(longName).valid).toBe(false);
    });
  });

  describe('validateBodySize', () => {
    it('accepts content within 1 MiB limit', () => {
      const small = 'hello world';
      const res = validateBodySize(small);
      expect(res.valid).toBe(true);
      expect(res.bytes).toBe(11);
    });

    it('rejects content exceeding 1 MiB', () => {
      // 1 MiB = 1048576 bytes
      const large = 'a'.repeat(1024 * 1024 + 10);
      const res = validateBodySize(large);
      expect(res.valid).toBe(false);
      expect(res.error).toContain('exceeds');
    });
  });

  describe('isDefaultFile', () => {
    it('returns true only for global/readme.md', () => {
      expect(isDefaultFile('global', 'readme.md')).toBe(true);
      expect(isDefaultFile('global', '/readme.md')).toBe(true);
      expect(isDefaultFile('global', 'other.md')).toBe(false);
      expect(isDefaultFile('work', 'readme.md')).toBe(false);
    });
  });
});
