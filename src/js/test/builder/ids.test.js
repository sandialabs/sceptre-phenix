import { afterEach, describe, expect, test, vi } from 'vitest';
import { webcrypto } from 'node:crypto';

import { contentDigest } from '@/builder/digest.js';
import { newId, uniqueName } from '@/builder/ids.js';
import { createDocument } from '@/builder/model.js';

const UUID_V4 =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

afterEach(() => {
  vi.unstubAllGlobals();
});

// A page served over plain HTTP from another host is not a secure context:
// the browser leaves crypto.randomUUID and crypto.subtle out.
function insecureContext() {
  vi.stubGlobal('crypto', {
    getRandomValues: (array) => webcrypto.getRandomValues(array),
  });
}

describe('newId', () => {
  test('uses crypto.randomUUID where the browser offers it', () => {
    vi.stubGlobal('crypto', {
      randomUUID: () => '00000000-0000-4000-8000-000000000000',
      getRandomValues: () => {
        throw new Error('not used');
      },
    });

    expect(newId()).toBe('00000000-0000-4000-8000-000000000000');
  });

  test('builds v4 UUIDs from getRandomValues outside a secure context', () => {
    insecureContext();
    expect(globalThis.crypto.randomUUID).toBeUndefined();
    expect(globalThis.crypto.subtle).toBeUndefined();

    const ids = Array.from({ length: 500 }, () => newId());

    for (const id of ids) {
      expect(id).toMatch(UUID_V4);
    }
    expect(new Set(ids).size).toBe(ids.length);
  });

  test('documents are created and scenario digests computed outside a secure context', async () => {
    insecureContext();

    const doc = createDocument({ name: 'Plain HTTP' });

    expect(doc.id).toMatch(UUID_V4);
    // The digest the server computes for {"a":1}.
    await expect(contentDigest({ a: 1 })).resolves.toBe(
      'sha256:015abd7f5cc57a2dd94b7590f04ad8084273905ee33ec5cebeae62276a97f862',
    );
  });

  test('says so when the browser has no random source at all', () => {
    vi.stubGlobal('crypto', undefined);

    expect(() => newId()).toThrow(
      'This browser cannot generate random identifiers.',
    );
  });
});

describe('uniqueName', () => {
  test('numbers a taken name, with the separator given', () => {
    const taken = ['Untitled topology', 'untitled topology 2'];

    expect(uniqueName('eth0', ['eth0'])).toBe('eth0-2');
    expect(
      uniqueName('Untitled topology', taken, (name) => name.toLowerCase(), ' '),
    ).toBe('Untitled topology 3');
    expect(uniqueName('Untitled topology', [], undefined, ' ')).toBe(
      'Untitled topology',
    );
  });
});
