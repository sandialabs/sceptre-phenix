// The real store's login: what it keeps on this device, and the page it opens.
import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { router, session } = vi.hoisted(() => ({
  router: { replace: vi.fn() },
  session: { startBuilderSession: vi.fn(), endBuilderSession: vi.fn() },
}));
vi.unmock('@/store.js');
vi.mock('@/router', () => ({ default: router }));
vi.mock('@/builder/session.js', () => session);

import { usePhenixStore } from '@/store.js';

const KEYS = ['phenix.user', 'phenix.token', 'phenix.role', 'phenix.auth'];

// a login answer for alice with this role
const answer = (name) => ({
  token: 't0k',
  user: { username: 'alice', role: { name } },
});

// the phenix keys of one storage, by key
const kept = (storage) =>
  Object.fromEntries(KEYS.map((key) => [key, storage.getItem(key)]));

beforeEach(() => {
  setActivePinia(createPinia());
  sessionStorage.clear();
  vi.clearAllMocks();
});

describe('login', () => {
  const saved = {
    'phenix.user': 'alice',
    'phenix.token': 't0k',
    'phenix.role': '{"name":"Global Admin"}',
    'phenix.auth': 'true',
  };
  const none = Object.fromEntries(KEYS.map((key) => [key, null]));

  it.each([
    [true, 'keeps the session after the browser closes', saved],
    [false, 'keeps the session only for this tab', none],
  ])('with remember %s, %s', (remember, _, local) => {
    const store = usePhenixStore();
    store.login(answer('Global Admin'), remember, false);

    expect(session.startBuilderSession).toHaveBeenCalledExactlyOnceWith(
      'alice',
    );
    expect(store).toMatchObject({
      username: 'alice',
      token: 't0k',
      role: { name: 'Global Admin' },
      auth: true,
    });
    expect(kept(localStorage)).toEqual(local);
    expect(kept(sessionStorage)).toEqual(saved);
    expect(router.replace).not.toHaveBeenCalled();
  });

  // Each row: a role, the page that was asked for, and the page that opens.
  const vms = { name: 'vms', params: { id: 'exp' } };
  it.each([
    ['VM Viewer', vms, { name: 'vmtiles' }],
    ['Disabled', vms, { name: 'disabled' }],
    ['Global Admin', vms, vms],
    ['Global Admin', { name: 'signin' }, { name: 'home' }],
    ['Global Admin', null, { name: 'home' }],
  ])('a %s that asked for %o opens %o', (role, next, page) => {
    const store = usePhenixStore();
    store.next = next;
    store.login(answer(role), false);

    expect(router.replace).toHaveBeenCalledExactlyOnceWith(page);
  });

  it('forgets the page it asked for once it opens it', () => {
    const store = usePhenixStore();
    store.next = { name: 'vms' };
    store.login(answer('Global Admin'), false);
    expect(store.next).toBeNull();

    // A role that may not open it keeps it for a later login.
    store.next = { name: 'vms' };
    store.login(answer('VM Viewer'), false);
    expect(store.next).toEqual({ name: 'vms' });
  });
});
