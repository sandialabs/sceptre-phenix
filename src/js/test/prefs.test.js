// Values the browser remembers in localStorage, and what happens when it
// cannot.
import { afterEach, expect, it, vi } from 'vitest';

import { readPref, removePref, writePref } from '@/utils/prefs.js';
import { blockedStorage, memoryStorage } from './helpers/storage.js';

afterEach(() => vi.unstubAllGlobals());

it('keeps values as strings in localStorage', () => {
  vi.stubGlobal('localStorage', memoryStorage());

  expect(readPref('a')).toBe(null);
  writePref('a', 7);
  expect(readPref('a')).toBe('7');
  removePref('a');
  expect(readPref('a')).toBe(null);
});

it.each([
  [
    'storage that throws',
    () => vi.stubGlobal('localStorage', blockedStorage()),
  ],
  ['no storage', () => vi.stubGlobal('localStorage', undefined)],
  [
    // when site data is blocked, even reading localStorage throws
    'a localStorage that cannot be read',
    () => {
      vi.stubGlobal('localStorage', undefined);
      Object.defineProperty(globalThis, 'localStorage', {
        configurable: true,
        get() {
          throw new Error('blocked');
        },
      });
    },
  ],
])('finds nothing and keeps nothing with %s', (_, stub) => {
  stub();
  expect(readPref('a')).toBe(null);
  expect(() => writePref('a', 1)).not.toThrow();
  expect(() => removePref('a')).not.toThrow();
});
