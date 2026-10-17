// Values the browser remembers in localStorage, and what happens when it
// cannot.
import fc from 'fast-check';
import { afterEach, expect, it, vi } from 'vitest';

import { readPref, removePref, writePref } from '@/utils/prefs.js';
import { blockedStorage } from './helpers/storage.js';

afterEach(() => vi.unstubAllGlobals());

// test/setup.js gives each test an empty stand-in localStorage
it('keeps values as strings in localStorage', () => {
  expect(readPref('a')).toBe(null);
  writePref('a', 7);
  expect(readPref('a')).toBe('7');
  removePref('a');
  expect(readPref('a')).toBe(null);
});

it('reads back any value as its string, and drops one with no string', () => {
  const value = fc.anything({ withNullPrototype: true });
  fc.assert(
    fc.property(fc.string(), value, (key, v) => {
      let text = null;
      try {
        text = String(v);
      } catch {
        // an object with no prototype has no string
      }
      expect(() => writePref(key, v)).not.toThrow();
      expect(readPref(key)).toBe(text);
      removePref(key);
      expect(readPref(key)).toBe(null);
    }),
  );
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
