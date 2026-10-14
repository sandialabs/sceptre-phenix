// Stand-ins for the modules pages reach RBAC, the server, error reports, the
// store, the websocket, file downloads and their loader through, and the
// state tests set and read on them. vi.mock runs before a test file's
// imports, so a file loads this module with vi.hoisted and hands each
// module's factory to vi.mock:
//
//   const { axios, modules, resetMocks } = await vi.hoisted(
//     () => import('./helpers/mocks.js'),
//   );
//   vi.mock('@/utils/axios.js', modules.axios);
//
//   beforeEach(() => resetMocks());
//
// Loaded that early, it must not import a module a test mocks: it imports
// only vitest and helpers that do the same.
import { vi } from 'vitest';

import { idle, resetLoader } from './pageLoader.js';
import { allowAll } from './permissions.js';

// the other page loaders, for a file to mock pageLoader.js with instead of
// modules.pageLoader
export { capturing, fetching, loaderSpy, switchable } from './pageLoader.js';

// roleAllowed(resource, verb, name) answers with rbac.allowed; set it to a
// predicate from permissions.js
export const rbac = { allowed: allowAll };

export const axios = {
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  patch: vi.fn(),
  delete: vi.fn(),
  request: vi.fn(),
};

// useErrorNotification and showError
export const notify = { error: vi.fn(), show: vi.fn() };

// the lasting notifications opened outside a component (utils/notify.js),
// such as the outcome of an operation that did not fully succeed
export const notices = { open: vi.fn() };

// what usePhenixStore() returns after resetMocks()
export const STORE_DEFAULTS = {
  token: 't',
  features: [],
  role: null,
  username: 'alice',
};

// what usePhenixStore() returns
export const store = structuredClone(STORE_DEFAULTS);

// The websocket traffic: the messages pages sent, the handlers they added
// and have not removed, and the reconnect callbacks still registered.
export const ws = { sent: [], handlers: [], reconnect: [] };

// file-saver's saveAs, which downloads a file the page built
export const saver = { saveAs: vi.fn() };

const remove = (list, item) => {
  const i = list.indexOf(item);
  if (i >= 0) list.splice(i, 1);
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
    addWsHandler: (handler) => ws.handlers.push(handler),
    removeWsHandler: (handler) => remove(ws.handlers, handler),
    onWsReconnect: (f) => {
      ws.reconnect.push(f);
      return () => remove(ws.reconnect, f);
    },
    sendWsMsg: (msg) => ws.sent.push(msg),
  }),
  fileSaver: () => ({ default: saver }),
  notify: () => ({
    openNotification: (...args) => notices.open(...args),
    setNotificationApp: () => {},
  }),
  // a component that cannot run outside a browser, such as MiniTerminal.vue
  stub: () => ({ default: {} }),
  // every page gets a loader that loads nothing
  pageLoader: idle,
};

// Undoes what a test set: every role may do anything, no request, error
// report, download or websocket traffic has been made, no page has made a
// loader, and the store holds exactly `store` (STORE_DEFAULTS unless given;
// {} for an empty store), so a key a test added is gone.
export function resetMocks({ store: fields = STORE_DEFAULTS } = {}) {
  rbac.allowed = allowAll;
  for (const fn of [
    ...Object.values(axios),
    ...Object.values(notify),
    notices.open,
    saver.saveAs,
  ]) {
    fn.mockReset();
  }
  for (const list of Object.values(ws)) list.length = 0;
  for (const key of Object.keys(store)) delete store[key];
  Object.assign(
    store,
    fields === STORE_DEFAULTS ? structuredClone(STORE_DEFAULTS) : fields,
  );
  resetLoader();
}
