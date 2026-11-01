// The router view remounts a page when its path parameters change, so another
// experiment's link never keeps the old page, and keeps it on query changes.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import { viewKey } from '@/utils/viewKey.js';

// route names with the path parameters their routes declare, in path order
const PARAMS = {
  experiment: ['id'],
  scorchruns: ['id'],
  soh: ['id'],
  file: ['id', 'name'],
  webshark: [],
};
// values that often repeat, now and then with JSON's own characters
const value = fc.oneof(
  fc.constantFrom('a', 'b', 'a:b', '"', '{}'),
  fc.string(),
);
const query = fc.dictionary(fc.string(), fc.string());
const routeArb = fc.constantFrom(...Object.keys(PARAMS)).chain((name) =>
  fc.record({
    name: fc.constant(name),
    path: fc.constant('/x'),
    params: fc.record(
      Object.fromEntries(PARAMS[name].map((param) => [param, value])),
    ),
    query,
  }),
);

describe('viewKey', () => {
  it('stays the same when only the query changes', () => {
    fc.assert(
      fc.property(routeArb, query, (route, other) => {
        expect(viewKey({ ...route, query: other })).toBe(viewKey(route));
      }),
    );
  });

  it('differs between routes, and for the same route with other path parameters', () => {
    fc.assert(
      fc.property(routeArb, routeArb, (a, b) => {
        const same =
          a.name === b.name &&
          PARAMS[a.name].every((param) => a.params[param] === b.params[param]);
        expect(viewKey(a) === viewKey(b)).toBe(same);
      }),
    );
  });
});
