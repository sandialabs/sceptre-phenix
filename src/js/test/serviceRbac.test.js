// Service-level RBAC for Builder, Scorch, and Tunneler; mirrors
// web/middleware RequirePermission and the Scorch pipeline route checks.
import { roleAllowed, scorchControlAllowed } from '@/utils/rbac.js';
import { expect, test, vi } from 'vitest';

const store = vi.hoisted(() => ({ role: null }));

vi.mock('@/store.js', () => ({
  usePhenixStore: () => store,
}));

// roleAllowed caches by role name, so every test uses a distinct name.
function useRole(name, policies) {
  store.role = { name, policies };
}

const experimentUser = [
  {
    resources: ['experiments', 'experiments/*'],
    resourceNames: ['exp-a'],
    verbs: ['list', 'get'],
  },
  { resources: ['builder'], resourceNames: null, verbs: ['get', 'post'] },
  { resources: ['scorch'], resourceNames: null, verbs: ['get'] },
  { resources: ['tunneler'], resourceNames: null, verbs: ['get'] },
];

test('experiment user can use services but not control Scorch', () => {
  useRole('Experiment User', experimentUser);

  expect(roleAllowed('builder', 'get')).toBe(true);
  expect(roleAllowed('builder', 'post')).toBe(true);
  expect(roleAllowed('builder', 'put')).toBe(false);
  expect(roleAllowed('scorch', 'get')).toBe(true);
  expect(roleAllowed('tunneler', 'get')).toBe(true);
  expect(scorchControlAllowed('exp-a', false)).toBe(false);
  expect(scorchControlAllowed('exp-a', true)).toBe(false);
});

test('service checks ignore experiment scope on the policy', () => {
  useRole('Scoped Scorch Viewer', [
    { resources: ['scorch'], resourceNames: ['exp-a'], verbs: ['get'] },
  ]);

  expect(roleAllowed('scorch', 'get')).toBe(true);
  expect(roleAllowed('builder', 'get')).toBe(false);
  expect(roleAllowed('tunneler', 'get')).toBe(false);
});

test('Scorch operator controls runs only for scoped experiments', () => {
  useRole('Scorch Operator', [
    {
      resources: ['experiments/trigger'],
      resourceNames: ['exp-a'],
      verbs: ['create', 'delete'],
    },
    { resources: ['scorch'], resourceNames: null, verbs: ['*'] },
  ]);

  expect(scorchControlAllowed('exp-a', false)).toBe(true);
  expect(scorchControlAllowed('exp-a', true)).toBe(true);
  expect(scorchControlAllowed('exp-b', false)).toBe(false);
  expect(scorchControlAllowed('exp-b', true)).toBe(false);
});

test('Scorch control needs the trigger permission', () => {
  useRole('Scorch Service Only', [
    { resources: ['scorch'], resourceNames: null, verbs: ['*'] },
  ]);

  expect(scorchControlAllowed('exp-a', false)).toBe(false);
  expect(scorchControlAllowed('exp-a', true)).toBe(false);
});

test('Scorch control needs the Scorch service permission', () => {
  useRole('Trigger Only', [
    {
      resources: ['experiments/trigger'],
      resourceNames: ['*'],
      verbs: ['create', 'delete'],
    },
    { resources: ['scorch'], resourceNames: null, verbs: ['get'] },
  ]);

  expect(scorchControlAllowed('exp-a', false)).toBe(false);
  expect(scorchControlAllowed('exp-a', true)).toBe(false);
});

test('starting and canceling Scorch runs need matching verbs', () => {
  useRole('Scorch Starter', [
    {
      resources: ['experiments/trigger'],
      resourceNames: ['*'],
      verbs: ['create'],
    },
    { resources: ['scorch'], resourceNames: null, verbs: ['get', 'post'] },
  ]);

  expect(scorchControlAllowed('exp-a', false)).toBe(true);
  expect(scorchControlAllowed('exp-a', true)).toBe(false);
});

test('missing role allows nothing', () => {
  store.role = null;

  expect(roleAllowed('scorch', 'get')).toBe(false);
  expect(scorchControlAllowed('exp-a', false)).toBe(false);
});
