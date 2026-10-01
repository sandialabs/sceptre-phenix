import { afterEach, expect, test, vi } from 'vitest';

import { followStorageKey, pageStorage } from '@/builder/storage.js';

afterEach(() => {
  vi.unstubAllGlobals();
});

test('pageStorage hands back the named storage', () => {
  const local = { getItem: () => null };
  const session = { getItem: () => null };
  vi.stubGlobal('localStorage', local);
  vi.stubGlobal('sessionStorage', session);

  expect(pageStorage()).toBe(local);
  expect(pageStorage('sessionStorage')).toBe(session);
});

test('pageStorage reads blocked site data as no storage', () => {
  const saved = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    get() {
      throw new Error('SecurityError: access denied');
    },
  });
  try {
    expect(pageStorage()).toBeNull();
  } finally {
    if (saved) {
      Object.defineProperty(globalThis, 'localStorage', saved);
    } else {
      delete globalThis.localStorage;
    }
  }
});

test('followStorageKey reloads for its key and for cleared storage, until stopped', () => {
  const target = new EventTarget();
  const reload = vi.fn();
  const otherTab = (key) =>
    target.dispatchEvent(Object.assign(new Event('storage'), { key }));
  const stop = followStorageKey('phenix.builder.shortcuts', reload, target);

  otherTab('phenix.builder.theme');
  expect(reload).not.toHaveBeenCalled();
  otherTab('phenix.builder.shortcuts');
  otherTab(null);
  expect(reload).toHaveBeenCalledTimes(2);

  stop();
  otherTab('phenix.builder.shortcuts');
  expect(reload).toHaveBeenCalledTimes(2);
});
