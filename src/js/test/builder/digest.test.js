import { describe, expect, test } from 'vitest';
import { webcrypto } from 'node:crypto';
import { reactive } from 'vue';

import {
  canonicalJSON,
  contentDigest,
  contentDigestSync,
  isDigest,
  sha256Hex,
} from '@/builder/digest.js';

function hex(buffer) {
  return Array.from(new Uint8Array(buffer), (byte) =>
    byte.toString(16).padStart(2, '0'),
  ).join('');
}

describe('content digests', () => {
  test('object keys are serialized in sorted order, like Go', () => {
    expect(canonicalJSON({ b: 1, a: 2 })).toBe('{"a":2,"b":1}');
    expect(canonicalJSON({ a: { d: 1, c: [1, { f: 2, e: 3 }] } })).toBe(
      '{"a":{"c":[1,{"e":3,"f":2}],"d":1}}',
    );
  });

  test('object keys are sorted by code point, as Go sorts them', () => {
    // UTF-16 order puts an emoji (a surrogate pair) before fullwidth forms
    // and U+FE0F; Go sorts by UTF-8 bytes, which is code point order. The
    // expected digests are the ones builder.ContentDigest computes.
    expect(canonicalJSON({ '🚀': 2, ＡＰＩ: 1 })).toBe('{"ＡＰＩ":1,"🚀":2}');
    expect(contentDigestSync({ '🚀': 2, ＡＰＩ: 1 })).toBe(
      'sha256:7cf16ae08177e0263ec292b10dd7f0a97956bbdafd53b502741f71d8b1364213',
    );

    const metadata = { '🚀': 'b', '𐀀': 'd', '': 'c', ＡＰＩ: 'a' };

    expect(Object.keys(JSON.parse(canonicalJSON(metadata)))).toEqual([
      '',
      'ＡＰＩ',
      '𐀀',
      '🚀',
    ]);
    expect(contentDigestSync({ apps: [{ name: 'x', metadata }] })).toBe(
      'sha256:66195a715364924d070855cb9fa3c3d28fa60e0e2c8a529d1865e7b59e4cbad6',
    );
    expect(contentDigestSync({ a: { z: 3, '😀': 2, '️': 1 } })).toBe(
      'sha256:b5d0e07b11cc92d235cbf5eff6ceab9822de3fb5bce0559657d1d32214f3ae7b',
    );
  });

  test('HTML characters are escaped the way encoding/json escapes them', () => {
    expect(canonicalJSON({ a: '<b>&' })).toBe('{"a":"\\u003cb\\u003e\\u0026"}');
  });

  test('undefined members are omitted', () => {
    expect(canonicalJSON({ a: 1, b: undefined })).toBe('{"a":1}');
  });

  test('digests use the sha256 prefix the server requires', async () => {
    const digest = await contentDigest({ a: 1 });

    expect(digest).toMatch(/^sha256:[0-9a-f]{64}$/);
    expect(isDigest(digest)).toBe(true);
    expect(isDigest('sha256:short')).toBe(false);
    expect(isDigest('')).toBe(false);
  });

  test('empty content has no digest', async () => {
    expect(await contentDigest(null)).toBe('');
  });

  test('the digest is stable regardless of key order', async () => {
    const first = await contentDigest({ a: 1, b: 2 });
    const second = await contentDigest({ b: 2, a: 1 });

    expect(first).toBe(second);
  });

  test('the digest matches SHA-256 across padding boundaries', async () => {
    // Lengths around the 55/56 and 64 byte block boundaries, and multi-block.
    for (const length of [0, 1, 55, 56, 63, 64, 65, 119, 120, 1000]) {
      const bytes = new Uint8Array(length).map(
        (_, index) => (index * 31) % 256,
      );
      const expected = hex(await webcrypto.subtle.digest('SHA-256', bytes));

      expect(sha256Hex(bytes), `length ${length}`).toBe(expected);
    }

    expect(sha256Hex(new TextEncoder().encode('abc'))).toBe(
      'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad',
    );
  });

  test('the synchronous digest is the digest of the canonical JSON', async () => {
    const content = { apps: [{ name: 'a<b', metadata: { z: 1, y: 'é' } }] };
    const bytes = new TextEncoder().encode(canonicalJSON(content));
    const expected = `sha256:${hex(await webcrypto.subtle.digest('SHA-256', bytes))}`;

    expect(contentDigestSync(content)).toBe(expected);
    expect(await contentDigest(content)).toBe(expected);
    expect(contentDigestSync(undefined)).toBe('');
  });

  test('an unchanged content object, or its reactive proxy, is not read again', () => {
    // Validation digests the scenario on every edit, and content can be
    // megabytes; counting reads of one member shows whether it was
    // serialized again.
    let reads = 0;
    const content = { apps: [] };
    Object.defineProperty(content, 'name', {
      enumerable: true,
      get() {
        reads += 1;

        return 'cached';
      },
    });

    const digest = contentDigestSync(content);
    const readsOnce = reads;

    expect(readsOnce).toBeGreaterThan(0);
    expect(contentDigestSync(content)).toBe(digest);
    expect(contentDigestSync(reactive(content))).toBe(digest);
    expect(reads).toBe(readsOnce);

    // Equal content in a new object is digested again, to the same value.
    expect(contentDigestSync({ apps: [], name: 'cached' })).toBe(digest);
    expect(contentDigestSync({ apps: [], name: 'changed' })).not.toBe(digest);
  });
});
