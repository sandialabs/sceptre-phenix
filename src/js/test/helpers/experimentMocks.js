// Stand-ins for the modules the experiment pages reach RBAC, the server,
// error reports, the store, the websocket and their loader through, and the
// state tests set and read on them. vi.mock runs before a test file's
// imports, so a file loads this module with vi.hoisted and hands each
// module's factory to vi.mock:
//
//   const { axios, modules } = await vi.hoisted(
//     () => import('./helpers/experimentMocks.js'),
//   );
//   vi.mock('@/utils/axios.js', modules.axios);
//
// Loaded that early, it must not import a module a test mocks.
import { vi } from 'vitest';

import { idle } from './pageLoader.js';

// roleAllowed(resource, verb, name) answers with rbac.allowed
export const rbac = { allowed: () => true };

export const axios = {
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  delete: vi.fn(),
};

// useErrorNotification and showError
export const notify = { error: vi.fn(), show: vi.fn() };

// what usePhenixStore() returns
export const store = {
  token: 't',
  features: [],
  role: null,
  username: 'alice',
};

// a vi.mock factory for each module, by name
export const modules = {
  rbac: () => ({ roleAllowed: (...args) => rbac.allowed(...args) }),
  axios: () => ({ default: axios }),
  errorNotif: () => ({
    useErrorNotification: notify.error,
    showError: notify.show,
  }),
  store: () => ({ usePhenixStore: () => store }),
  websocket: () => ({
    addWsHandler: () => {},
    removeWsHandler: () => {},
    onWsReconnect: () => () => {},
    sendWsMsg: () => {},
  }),
  pageLoader: idle,
};

// Undoes what a test set: every role may do anything, the server supports
// no features, and no request or error report has been made.
export function resetMocks() {
  rbac.allowed = () => true;
  store.features = [];
  for (const fn of [...Object.values(axios), ...Object.values(notify)]) {
    fn.mockReset();
  }
}
