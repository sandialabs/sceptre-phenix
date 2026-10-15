// The app's router view mounts a page afresh when its path parameters change,
// so another experiment's link never leaves the first experiment's page in
// place, and keeps it when only the query changes.
import { describe, expect, it } from 'vitest';

import { viewKey } from '@/utils/viewKey.js';

const route = (name, params = {}, query = {}) => ({
  name,
  path: '/x',
  params,
  query,
});

describe('viewKey', () => {
  it('differs for the same route with other path parameters', () => {
    expect(viewKey(route('experiment', { id: 'a' }))).not.toBe(
      viewKey(route('experiment', { id: 'b' })),
    );
  });

  it('stays the same when only the query changes', () => {
    expect(viewKey(route('webshark', {}, { exp: 'a' }))).toBe(
      viewKey(route('webshark', {}, { exp: 'b' })),
    );
  });

  it('differs between routes', () => {
    expect(viewKey(route('scorchruns', { id: 'a' }))).not.toBe(
      viewKey(route('soh', { id: 'a' })),
    );
  });
});
