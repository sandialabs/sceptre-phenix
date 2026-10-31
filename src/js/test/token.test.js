import { expect, test } from 'vitest';

import { tokenExpiry } from '@/utils/token.js';

// A JWT that expires at `exp`, in seconds. The tokenExpired cases are in
// token.contract.test.js.
const jwt = (exp) => `header.${btoa(JSON.stringify({ exp }))}.signature`;

test('the expiry is in milliseconds, or null without a numeric exp claim', () => {
  expect(tokenExpiry(jwt(1000))).toBe(1000000);
  expect(
    tokenExpiry(`header.${btoa(JSON.stringify({ exp: 'soon' }))}.signature`),
  ).toBeNull();
  expect(tokenExpiry(`header.${btoa('{}')}.signature`)).toBeNull();
  expect(tokenExpiry('header.not-json.signature')).toBeNull();
  expect(tokenExpiry(undefined)).toBeNull();
});
