import { describe, expect, test } from 'vitest';

import {
  applyTheme,
  DEFAULT_THEME,
  isValidTheme,
  nextTheme,
  prefersDark,
  prefersReducedMotion,
  readStoredTheme,
  resolveTheme,
  storeTheme,
  THEME_STORAGE_KEY,
  watchSystemTheme,
} from '@/builder/theme.js';

function fakeStorage(initial = {}) {
  const data = { ...initial };

  return {
    data,
    getItem: (key) => (key in data ? data[key] : null),
    setItem: (key, value) => {
      data[key] = String(value);
    },
  };
}

function fakeMatchMedia(matches = {}) {
  return (query) => ({ matches: Boolean(matches[query]) });
}

const DARK = '(prefers-color-scheme: dark)';
const REDUCED = '(prefers-reduced-motion: reduce)';

describe('builder theme', () => {
  test('uses the documented storage key', () => {
    expect(THEME_STORAGE_KEY).toBe('phenix.builder.theme');
    expect(DEFAULT_THEME).toBe('system');
  });

  test('reads and validates the stored preference', () => {
    expect(readStoredTheme(fakeStorage({ [THEME_STORAGE_KEY]: 'dark' }))).toBe(
      'dark',
    );
    expect(readStoredTheme(fakeStorage({ [THEME_STORAGE_KEY]: 'neon' }))).toBe(
      'system',
    );
    expect(readStoredTheme(undefined)).toBe('system');
  });

  test('persists only valid preferences', () => {
    const storage = fakeStorage();

    expect(storeTheme('light', storage)).toBe('light');
    expect(storage.data[THEME_STORAGE_KEY]).toBe('light');
    expect(storeTheme('nope', storage)).toBe('system');
    expect(storage.data[THEME_STORAGE_KEY]).toBe('system');
  });

  test('storage failures are not fatal', () => {
    const broken = {
      getItem() {
        throw new Error('denied');
      },
      setItem() {
        throw new Error('denied');
      },
    };

    expect(readStoredTheme(broken)).toBe('system');
    expect(storeTheme('dark', broken)).toBe('dark');
  });

  test('system resolves through matchMedia', () => {
    expect(resolveTheme('system', fakeMatchMedia({ [DARK]: true }))).toBe(
      'dark',
    );
    expect(resolveTheme('system', fakeMatchMedia())).toBe('light');
    expect(resolveTheme('light', fakeMatchMedia({ [DARK]: true }))).toBe(
      'light',
    );
    expect(resolveTheme('system', undefined)).toBe('light');
  });

  test('media queries are reported', () => {
    expect(prefersDark(fakeMatchMedia({ [DARK]: true }))).toBe(true);
    expect(prefersReducedMotion(fakeMatchMedia({ [REDUCED]: true }))).toBe(
      true,
    );
    expect(prefersReducedMotion(fakeMatchMedia())).toBe(false);
  });

  test('applying a theme sets both resolved and preference attributes', () => {
    const attrs = {};
    const element = {
      setAttribute: (name, value) => {
        attrs[name] = value;
      },
    };

    const resolved = applyTheme(
      element,
      'system',
      fakeMatchMedia({ [DARK]: true }),
    );

    expect(resolved).toBe('dark');
    expect(attrs['data-builder-theme']).toBe('dark');
    expect(attrs['data-builder-theme-preference']).toBe('system');
    expect(applyTheme(null, 'light')).toBe('light');
  });

  test('system theme changes can be watched and unsubscribed', () => {
    let listener;
    let removed;
    const onChange = () => {};
    const query = {
      addEventListener: (name, callback) => {
        expect(name).toBe('change');
        listener = callback;
      },
      removeEventListener: (name, callback) => {
        expect(name).toBe('change');
        removed = callback;
      },
    };

    const stop = watchSystemTheme(() => query, onChange);

    expect(listener).toBe(onChange);
    stop();
    expect(removed).toBe(onChange);
  });

  test('the toggle cycles system, light, dark', () => {
    expect(nextTheme('system')).toBe('light');
    expect(nextTheme('light')).toBe('dark');
    expect(nextTheme('dark')).toBe('system');
    expect(nextTheme('bogus')).toBe('system');
    expect(isValidTheme('dark')).toBe(true);
    expect(isValidTheme('')).toBe(false);
  });
});
