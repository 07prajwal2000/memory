import { describe, it, expect } from 'vitest';
import {
  formatBytes,
  formatRelativeTime,
  formatExpiryTime,
} from './formatters';

describe('formatters', () => {
  describe('formatBytes', () => {
    it('formats bytes, kilobytes, and megabytes', () => {
      expect(formatBytes(500)).toBe('500 B');
      expect(formatBytes(1024)).toBe('1.0 KB');
      expect(formatBytes(2048)).toBe('2.0 KB');
      expect(formatBytes(1024 * 1024)).toBe('1.00 MB');
    });
  });

  describe('formatRelativeTime', () => {
    it('formats relative times correctly', () => {
      const now = new Date('2026-11-18T12:00:00Z');
      const justNow = new Date('2026-11-18T11:59:30Z').toISOString();
      const minsAgo = new Date('2026-11-18T11:50:00Z').toISOString();
      const hoursAgo = new Date('2026-11-18T09:00:00Z').toISOString();
      const yesterday = new Date('2026-11-17T10:00:00Z').toISOString();
      const daysAgo = new Date('2026-11-14T12:00:00Z').toISOString();

      expect(formatRelativeTime(justNow, now)).toBe('just now');
      expect(formatRelativeTime(minsAgo, now)).toBe('10m ago');
      expect(formatRelativeTime(hoursAgo, now)).toBe('3h ago');
      expect(formatRelativeTime(yesterday, now)).toBe('yesterday');
      expect(formatRelativeTime(daysAgo, now)).toBe('4d ago');
    });
  });

  describe('formatExpiryTime', () => {
    it('returns permanent for null expiry', () => {
      const res = formatExpiryTime(null);
      expect(res.label).toBe('permanent');
      expect(res.isExpired).toBe(false);
      expect(res.isExpiringSoon).toBe(false);
    });

    it('identifies expired contexts', () => {
      const now = new Date('2026-11-18T12:00:00Z');
      const past = new Date('2026-11-18T11:00:00Z').toISOString();
      const res = formatExpiryTime(past, now);
      expect(res.isExpired).toBe(true);
      expect(res.label).toBe('expired');
    });

    it('identifies contexts expiring in less than 24 hours (warning state)', () => {
      const now = new Date('2026-11-18T12:00:00Z');
      const soon = new Date('2026-11-18T18:30:00Z').toISOString(); // 6h 30m
      const res = formatExpiryTime(soon, now);
      expect(res.isExpired).toBe(false);
      expect(res.isExpiringSoon).toBe(true);
      expect(res.label).toBe('expires in 6h 30m');
    });

    it('formats multi-day remaining duration', () => {
      const now = new Date('2026-11-18T12:00:00Z');
      const multiDay = new Date('2026-11-20T16:00:00Z').toISOString(); // 2d 4h
      const res = formatExpiryTime(multiDay, now);
      expect(res.isExpired).toBe(false);
      expect(res.isExpiringSoon).toBe(false);
      expect(res.label).toBe('expires in 2d 4h');
    });
  });
});
