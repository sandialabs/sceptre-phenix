import { describe, expect, test } from 'vitest';
import { webcrypto } from 'node:crypto';

import { canonicalJSON, contentDigest, isDigest } from '@/builder/digest.js';

describe('content digests', () => {
  test('object keys are serialized in sorted order, like Go', () => {
    expect(canonicalJSON({ b: 1, a: 2 })).toBe('{"a":2,"b":1}');
    expect(canonicalJSON({ a: { d: 1, c: [1, { f: 2, e: 3 }] } })).toBe(
      '{"a":{"c":[1,{"e":3,"f":2}],"d":1}}',
    );
  });

  test('HTML characters are escaped the way encoding/json escapes them', () => {
    expect(canonicalJSON({ a: '<b>&' })).toBe('{"a":"\\u003cb\\u003e\\u0026"}');
  });

  test('undefined members are omitted', () => {
    expect(canonicalJSON({ a: 1, b: undefined })).toBe('{"a":1}');
  });

  test('digests use the sha256 prefix the server requires', async () => {
    const digest = await contentDigest({ a: 1 }, webcrypto);

    expect(digest).toMatch(/^sha256:[0-9a-f]{64}$/);
    expect(isDigest(digest)).toBe(true);
    expect(isDigest('sha256:short')).toBe(false);
    expect(isDigest('')).toBe(false);
  });

  test('empty content has no digest', async () => {
    expect(await contentDigest(null, webcrypto)).toBe('');
  });

  test('the digest is stable regardless of key order', async () => {
    const first = await contentDigest({ a: 1, b: 2 }, webcrypto);
    const second = await contentDigest({ b: 2, a: 1 }, webcrypto);

    expect(first).toBe(second);
  });
});
