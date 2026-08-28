import { describe, expect, test } from 'vitest';
import {
  normalizeDefaultTheme,
  normalizeLocalTheme,
  resolveTheme,
} from '@/utils/theme.js';

describe('theme resolution', () => {
  test('uses a local preference before the global default', () => {
    expect(resolveTheme('dark', 'light', false)).toBe('dark');
    expect(resolveTheme('light', 'dark', true)).toBe('light');
  });

  test('resolves a system default from the browser preference', () => {
    expect(resolveTheme(null, 'system', true)).toBe('dark');
    expect(resolveTheme(null, 'system', false)).toBe('light');
  });

  test('uses an explicit global default without consulting the system', () => {
    expect(resolveTheme(null, 'dark', false)).toBe('dark');
    expect(resolveTheme(null, 'light', true)).toBe('light');
  });

  test('rejects invalid values safely', () => {
    expect(normalizeLocalTheme('system')).toBeNull();
    expect(normalizeLocalTheme('invalid')).toBeNull();
    expect(normalizeDefaultTheme('invalid')).toBe('system');
  });
});
