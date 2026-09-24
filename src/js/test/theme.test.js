import { describe, expect, test } from 'vitest';
import {
  createThemeManager,
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

function fakeBrowser({
  stored = null,
  prefersDark = false,
  defaultTheme,
} = {}) {
  const storage = new Map();
  if (stored !== null) storage.set('phenix.theme', stored);
  const listeners = {};
  return {
    __PHENIX_DEFAULT_THEME__: defaultTheme,
    storage,
    listeners,
    matchMedia: () => ({
      matches: prefersDark,
      addEventListener: (_, handler) => {
        listeners.media = handler;
      },
    }),
    localStorage: {
      getItem: (key) => (storage.has(key) ? storage.get(key) : null),
      setItem: (key, value) => storage.set(key, value),
      removeItem: (key) => storage.delete(key),
    },
    addEventListener: (type, handler) => {
      listeners[type] = handler;
    },
  };
}

function fakeRoot() {
  return { dataset: {}, style: {} };
}

describe('theme manager', () => {
  test('applies the resolved theme to the document root', () => {
    const root = fakeRoot();
    const manager = createThemeManager(
      fakeBrowser({ defaultTheme: 'system', prefersDark: true }),
      root,
    );
    expect(manager.activeTheme.value).toBe('dark');
    expect(root.dataset.theme).toBe('dark');
    expect(root.style.colorScheme).toBe('dark');
  });

  test('drops an invalid stored preference', () => {
    const browser = fakeBrowser({ stored: 'sepia', defaultTheme: 'light' });
    const manager = createThemeManager(browser, fakeRoot());
    expect(manager.localTheme.value).toBeNull();
    expect(browser.storage.has('phenix.theme')).toBe(false);
    expect(manager.activeTheme.value).toBe('light');
  });

  test('toggling stores the new local preference', () => {
    const browser = fakeBrowser({ defaultTheme: 'light' });
    const root = fakeRoot();
    const manager = createThemeManager(browser, root);
    manager.toggleTheme();
    expect(manager.activeTheme.value).toBe('dark');
    expect(browser.storage.get('phenix.theme')).toBe('dark');
    expect(root.dataset.theme).toBe('dark');
  });

  test('reports a storage failure after applying the theme', () => {
    const browser = fakeBrowser({ defaultTheme: 'light' });
    browser.localStorage.setItem = () => {
      throw new Error('quota');
    };
    const root = fakeRoot();
    const manager = createThemeManager(browser, root);
    expect(() => manager.setLocalTheme('dark')).toThrow(/could not be saved/);
    expect(root.dataset.theme).toBe('dark');
  });

  test('follows system changes only without a local choice', () => {
    const browser = fakeBrowser({ defaultTheme: 'system', prefersDark: false });
    const root = fakeRoot();
    const manager = createThemeManager(browser, root);
    expect(root.dataset.theme).toBe('light');

    browser.listeners.media({ matches: true });
    expect(root.dataset.theme).toBe('dark');

    manager.setLocalTheme('light');
    browser.listeners.media({ matches: false });
    browser.listeners.media({ matches: true });
    expect(root.dataset.theme).toBe('light');
  });

  test('syncs a preference changed in another tab', () => {
    const browser = fakeBrowser({ defaultTheme: 'light' });
    const root = fakeRoot();
    createThemeManager(browser, root);
    browser.listeners.storage({ key: 'phenix.theme', newValue: 'dark' });
    expect(root.dataset.theme).toBe('dark');
    browser.listeners.storage({ key: 'phenix.theme', newValue: null });
    expect(root.dataset.theme).toBe('light');
  });

  test('setDefaultTheme re-resolves when no local choice exists', () => {
    const browser = fakeBrowser({ defaultTheme: 'light' });
    const root = fakeRoot();
    const manager = createThemeManager(browser, root);
    manager.setDefaultTheme('dark');
    expect(root.dataset.theme).toBe('dark');
    manager.setDefaultTheme('bogus');
    expect(manager.defaultTheme.value).toBe('system');
  });
});
