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

// An Experiment User assigned to exp-a; assigning a role scopes every
// unscoped policy to the user's experiments.
const experimentUser = [
  {
    resources: ['experiments', 'experiments/*'],
    resourceNames: ['exp-a'],
    verbs: ['list', 'get'],
  },
  { resources: ['builder'], resourceNames: ['exp-a'], verbs: ['get', 'post'] },
  {
    resources: ['scorch'],
    resourceNames: ['exp-a'],
    verbs: ['get', 'post', 'delete'],
  },
  { resources: ['tunneler'], resourceNames: ['exp-a'], verbs: ['get'] },
];

test('experiment user controls Scorch only for assigned experiments', () => {
  useRole('Experiment User', experimentUser);

  expect(roleAllowed('builder', 'get')).toBe(true);
  expect(roleAllowed('builder', 'post')).toBe(true);
  expect(roleAllowed('builder', 'put')).toBe(false);
  expect(roleAllowed('tunneler', 'get')).toBe(true);
  expect(scorchControlAllowed('exp-a', false)).toBe(true);
  expect(scorchControlAllowed('exp-a', true)).toBe(true);
  expect(scorchControlAllowed('exp-b', false)).toBe(false);
  expect(scorchControlAllowed('exp-b', true)).toBe(false);
});

test('service checks ignore experiment scope on the policy', () => {
  useRole('Scoped Scorch Viewer', [
    { resources: ['scorch'], resourceNames: ['exp-a'], verbs: ['get'] },
  ]);

  expect(roleAllowed('scorch', 'get')).toBe(true);
  expect(roleAllowed('builder', 'get')).toBe(false);
  expect(roleAllowed('tunneler', 'get')).toBe(false);
});

test('Scorch control needs experiment read access', () => {
  useRole('Scorch Service Only', [
    { resources: ['scorch'], resourceNames: null, verbs: ['*'] },
  ]);

  expect(scorchControlAllowed('exp-a', false)).toBe(false);
  expect(scorchControlAllowed('exp-a', true)).toBe(false);
});

test('Scorch control does not need experiments/trigger', () => {
  useRole('Scorch Admin', [
    {
      resources: ['experiments', 'experiments/*'],
      resourceNames: ['*'],
      verbs: ['list', 'get'],
    },
    { resources: ['scorch'], resourceNames: null, verbs: ['*'] },
  ]);

  expect(roleAllowed('experiments/trigger', 'create', 'exp-a')).toBe(false);
  expect(scorchControlAllowed('exp-a', false)).toBe(true);
  expect(scorchControlAllowed('exp-a', true)).toBe(true);
});

test('Scorch control needs the Scorch service permission', () => {
  useRole('Trigger Only', [
    {
      resources: ['experiments', 'experiments/trigger'],
      resourceNames: ['*'],
      verbs: ['get', 'create', 'delete'],
    },
    { resources: ['scorch'], resourceNames: null, verbs: ['get'] },
  ]);

  expect(scorchControlAllowed('exp-a', false)).toBe(false);
  expect(scorchControlAllowed('exp-a', true)).toBe(false);
});

test('starting and canceling Scorch runs need matching verbs', () => {
  useRole('Scorch Starter', [
    { resources: ['experiments'], resourceNames: ['*'], verbs: ['get'] },
    { resources: ['scorch'], resourceNames: null, verbs: ['get', 'post'] },
  ]);

  expect(scorchControlAllowed('exp-a', false)).toBe(true);
  expect(scorchControlAllowed('exp-a', true)).toBe(false);
});

test('unscoped policies with null resource names match no name', () => {
  useRole('Null Resource Names', [
    { resources: ['vms/forwards'], resourceNames: null, verbs: ['create'] },
  ]);

  expect(roleAllowed('vms/forwards', 'create')).toBe(true);
  expect(roleAllowed('vms/forwards', 'create', 'exp-a/vm1')).toBe(false);
});

test('config checks use Kind/name', () => {
  useRole('Builder', [
    {
      resources: ['configs'],
      resourceNames: ['Topology/*', 'Scenario/*'],
      verbs: ['get', 'update'],
    },
  ]);

  expect(roleAllowed('configs', 'update', 'Topology/topo')).toBe(true);
  expect(roleAllowed('configs', 'get', 'User/admin')).toBe(false);
});

test('missing role allows nothing', () => {
  store.role = null;

  expect(roleAllowed('scorch', 'get')).toBe(false);
  expect(scorchControlAllowed('exp-a', false)).toBe(false);
});
