// The Users page's Create token button, offered for the users the role may
// create a token for (rbac.test.js tests that rule).
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket.js', modules.websocket);

import { clearPageCache } from '@/utils/pageCache.js';
import Users from '@/views/Users.vue';
import { mountPage } from './helpers/mount.js';
import { allowOnly } from './helpers/permissions.js';

const user = (username) => ({
  username,
  first_name: '',
  last_name: '',
  role: { name: 'Experiment Admin' },
});

beforeEach(() => {
  clearPageCache();
  resetMocks();
  axios.get.mockImplementation(async (url) => ({
    data: url === 'users' ? { users: [user('alice'), user('bob')] } : {},
  }));
});

describe('the Users page token button', () => {
  it('is offered for the signed-in user to a role that may patch them', async () => {
    rbac.allowed = allowOnly(['users', 'patch']);
    const page = await mountPage(Users, { route: '/users' });

    expect(
      page
        .findAll('[aria-label^="Create token for "]')
        .map((b) => b.attributes('aria-label')),
    ).toEqual(['Create token for alice']);
  });
});
