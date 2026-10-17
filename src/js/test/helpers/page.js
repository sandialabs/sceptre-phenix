// A route-level page run unmounted (see context.js), recording the router,
// Buefy and events; mock the modules it reaches the server through first.
import { vi } from 'vitest';

import { flush } from './async.js';
import { fakeBuefy } from './buefy.js';
import { makeContext } from './context.js';

// The page at the route with `id` as its id parameter, `query` and route
// `name`. `fields` are set last, so they replace any of the above.
export function pageContext(
  component,
  { id, query = {}, name, fields = {} } = {},
) {
  return makeContext(component, {
    $route: { params: id === undefined ? {} : { id }, query, name },
    $router: { push: vi.fn(), replace: vi.fn() },
    $buefy: fakeBuefy(),
    $emit: vi.fn(),
    $nextTick: (fn) => Promise.resolve().then(fn),
    ...fields,
  });
}

// the page after its created() hook, with the requests that hook made
// answered by the mocks
export async function openPage(component, options) {
  const ctx = pageContext(component, options);
  await component.created.call(ctx);
  await flush();
  return ctx;
}
