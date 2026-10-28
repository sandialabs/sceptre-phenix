// The expiry of the session token (utils/token.js).

import { describe, expect, test } from 'vitest';

import { tokenExpired, tokenExpiry } from '@/utils/token.js';

// A JWT that expires at `exp`, in seconds.
const jwt = (exp) => `header.${btoa(JSON.stringify({ exp }))}.signature`;

describe('tokenExpiry', () => {
  test('is the exp claim in milliseconds, or null for a token with none', () => {
    expect(tokenExpiry(jwt(1000))).toBe(1000000);
    expect(tokenExpiry(jwt('soon'))).toBeNull();
    expect(tokenExpiry('authorized')).toBeNull();
    expect(tokenExpiry(null)).toBeNull();
  });
});

describe('tokenExpired', () => {
  test('a token has expired when its claims say so; one that is no JWT never has', () => {
    const later = Math.ceil(Date.now() / 1000) + 60;

    expect(tokenExpired(jwt(1))).toBe(true);
    expect(tokenExpired(jwt(later))).toBe(false);
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
});
