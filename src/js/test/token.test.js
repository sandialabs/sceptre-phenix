import { expect, test, vi } from 'vitest';

import { tokenExpired } from '@/store.js';
import { tokenExpiry } from '@/utils/token.js';

// The real router needs a browser. Vitest hoists vi.mock above the imports.
vi.mock('@/router', () => ({ default: {} }));
vi.unmock('@/store.js');

// A JWT that expires at `exp`, in seconds.
const jwt = (exp) => `header.${btoa(JSON.stringify({ exp }))}.signature`;

test('a token has expired when its claims say so, one that is no JWT never has', () => {
  expect(tokenExpired(jwt(1))).toBe(true);
  expect(tokenExpired(jwt(Date.now() / 1000 + 60))).toBe(false);
  expect(tokenExpired('authorized')).toBe(false);
  expect(tokenExpired(null)).toBe(false);
});

test('a token whose claims encode to base64url characters still expires', () => {
  // The server signs {exp, sub} as UTF-8 JSON. A name such as chloé then
  // encodes with _, which atob refuses until it is made base64 again.
  const utf8 = String.fromCharCode(
    ...new TextEncoder().encode(
      JSON.stringify({ exp: 1000000000, sub: 'chloé' }),
    ),
  );
  const claims = btoa(utf8)
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '');
  expect(claims).toMatch(/[-_]/);
  expect(tokenExpired(`header.${claims}.signature`)).toBe(true);
});

test('the expiry is in milliseconds, or null without a numeric exp claim', () => {
  expect(tokenExpiry(jwt(1000))).toBe(1000000);
  expect(
    tokenExpiry(`header.${btoa(JSON.stringify({ exp: 'soon' }))}.signature`),
  ).toBeNull();
  expect(tokenExpiry(`header.${btoa('{}')}.signature`)).toBeNull();
  expect(tokenExpiry('header.not-json.signature')).toBeNull();
  expect(tokenExpiry(undefined)).toBeNull();
});
