// The Users page's Create token button, offered for the users the role may
// create a token for (rbac.test.js tests that rule), its totals and its
// Actions tooltips.
import fc from 'fast-check';
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
import { userTotals } from '@/utils/pageTotals.js';
import Users from '@/views/Users.vue';
import {
  mountPage,
  tableRows,
  textOf,
  tooltipPositions,
} from './helpers/mount.js';
import { allowOnly, denyOnly } from './helpers/permissions.js';

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

describe('the Users page totals', () => {
  // the strip's stats, as "label: value"
  const totalsText = (wrapper) =>
    wrapper
      .findAll('[aria-label="User totals"] .stat')
      .map((s) => `${textOf(s.find('dt'))}: ${textOf(s.find('dd'))}`);

  const withRole = (username, role) => ({
    ...user(username),
    role: { name: role },
  });

  it('counts every user and the distinct roles they hold, whatever the search', async () => {
    axios.get.mockImplementation(async (url) => ({
      data:
        url === 'users'
          ? {
              users: [
                withRole('alice', 'Global Admin'),
                withRole('bob', 'Experiment User'),
                withRole('carol', 'Experiment User'),
                withRole('dave', ''),
              ],
            }
          : { roles: [] },
    }));
    const page = await mountPage(Users, { route: '/users' });
    const want = ['Users: 4 (1 without a role)', 'Roles: 2'];
    expect(totalsText(page)).toEqual(want);

    await page.find('input[placeholder="Find a User"]').setValue('alice');
    expect(tableRows(page)).toHaveLength(1);
    expect(totalsText(page)).toEqual(want);
  });

  it('never counts more roles than users', () => {
    const role = fc.option(fc.constantFrom('A', 'B', 'C', 'D'), {
      nil: '',
    });
    fc.assert(
      fc.property(fc.array(role, { maxLength: 30 }), (roles) => {
        const got = userTotals(roles.map((r) => ({ role_name: r })));
        expect(got.users).toBe(roles.length);
        expect(got.roles).toBe(new Set(roles.filter(Boolean)).size);
        expect(got.unassigned).toBe(roles.filter((r) => !r).length);
        expect(got.roles + got.unassigned).toBeLessThanOrEqual(got.users);
      }),
    );
  });
});

// the column ends at the window's right edge
it('opens the Actions tooltips to the left, inside the window', async () => {
  rbac.allowed = denyOnly(['roles', 'list']);
  const page = await mountPage(Users, { route: '/users' });
  const rows = tableRows(page);
  expect(rows).toHaveLength(2);
  for (const cells of rows) {
    expect(tooltipPositions(cells.Actions)).toEqual([
      'is-left',
      'is-left',
      'is-left',
    ]);
  }
});
