// The remembered preflight checks where the browser blocks site data: reading
// localStorage throws, and the Checks panel still works.

import { afterEach, beforeEach, describe, expect, test } from 'vitest';

import {
  loadPreflightChecks,
  savePreflightChecks,
} from '@/builder/preflight.js';

let saved;

beforeEach(() => {
  saved = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    get() {
      throw new DOMException('Site data is blocked.', 'SecurityError');
    },
  });
});

afterEach(() => {
  if (saved) {
    Object.defineProperty(globalThis, 'localStorage', saved);
  } else {
    delete globalThis.localStorage;
  }
});

describe('preflight checks with blocked site data', () => {
  test('load as no checks', () => {
    expect(loadPreflightChecks()).toEqual([]);
  });

  test('save without an error', () => {
    expect(() => savePreflightChecks(['capacity', 'disks'])).not.toThrow();
  });
});
