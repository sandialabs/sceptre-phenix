// mirrors role_test.go
import { roleAllowed } from '@/utils/rbac.js';
import { test, expect, vi } from 'vitest';

vi.mock('@/store.js', () => {
  return {
    usePhenixStore: vi.fn().mockReturnValue({
      role: {
        name: 'Test Role',
        policies: [
          {
            resources: ['experiments'],
            resourceNames: ['*', '*/*'],
            verbs: ['get'],
          },
          {
            resources: ['experiments/start'],
            resourceNames: ['*', '*/*'],
            verbs: ['update'],
          },
          {
            resources: ['experiments'],
            resourceNames: ['exp1'],
            verbs: ['delete'],
          },
          {
            resources: ['*'],
            resourceNames: ['vm1'],
            verbs: ['patch'],
          },
          {
            resources: ['vms'],
            resourceNames: ['*'],
            verbs: ['delete'],
          },
          {
            resources: ['vms'],
            resourceNames: ['expA/*'],
            verbs: ['update'],
          },
          {
            resources: ['vms'],
            resourceNames: ['*/vm1'],
            verbs: ['create'],
          },
          {
            resources: ['vms'],
            resourceNames: ['*/*'],
            verbs: ['get'],
          },
          {
            resources: ['vms'],
            resourceNames: ['**'],
            verbs: ['list'],
          },
          {
            resources: ['vms'],
            resourceNames: ['{expA,expB}/*'],
            verbs: ['post'],
          },
          {
            resources: ['vms/start'],
            resourceNames: ['expA/*', '!expA/secret'],
            verbs: ['update'],
          },
          {
            resources: ['**'],
            resourceNames: ['*'],
            verbs: ['watch'],
          },
          {
            resources: ['things'],
            resourceNames: ['*', '!thing1'],
            verbs: ['*'],
          },
          {
            resources: ['items'],
            resourceNames: ['item*'],
            verbs: ['*'],
          },
        ],
      },
    }),
  };
});

test('get any experiment', () => {
  expect(roleAllowed('experiments', 'get', 'expA')).toBe(true);
  expect(roleAllowed('experiments', 'get', 'expB')).toBe(true);
});

test('update only experiments/start', () => {
  expect(roleAllowed('experiments/start', 'update')).toBe(true);
  expect(roleAllowed('experiments', 'update')).toBe(false);
  expect(roleAllowed('experiments/stop', 'update')).toBe(false);
  expect(roleAllowed('experiments/start', 'update', 'expA')).toBe(true);
});

test('only delete exp1', () => {
  expect(roleAllowed('experiments', 'delete', 'exp1')).toBe(true);
  expect(roleAllowed('experiments', 'delete', 'expB')).toBe(false);
  expect(roleAllowed('experiments/stop', 'delete', 'exp1')).toBe(false);
});

test("resource single wildcard doesn't apply", () => {
  expect(roleAllowed('vms', 'patch', 'vm1')).toBe(true);
  expect(roleAllowed('vms/start', 'patch', 'vm1')).toBe(false);
});

test('resource name restriction', () => {
  expect(roleAllowed('vms', 'patch', 'vm1')).toBe(true);
  expect(roleAllowed('vms', 'patch', 'vmB')).toBe(false);
  expect(roleAllowed('experiments', 'patch', 'expA')).toBe(false);
});

test('resourceName single wildcard does not cross namespace boundaries', () => {
  expect(roleAllowed('vms', 'delete', 'vm1')).toBe(true);
  expect(roleAllowed('vms', 'delete', 'expA/vm1')).toBe(false);
});

test('resourceName can allow one namespace', () => {
  expect(roleAllowed('vms', 'update', 'expA/vm1')).toBe(true);
  expect(roleAllowed('vms', 'update', 'expB/vm1')).toBe(false);
});

test('resourceName can allow one name across namespaces', () => {
  expect(roleAllowed('vms', 'create', 'expA/vm1')).toBe(true);
  expect(roleAllowed('vms', 'create', 'expB/vm1')).toBe(true);
  expect(roleAllowed('vms', 'create', 'expA/vm2')).toBe(false);
});

test('resourceName namespace wildcard requires a namespace', () => {
  expect(roleAllowed('vms', 'get', 'expA/vm1')).toBe(true);
  expect(roleAllowed('vms', 'get', 'vm1')).toBe(false);
});

test('resourceName globstar does not cross namespace boundaries', () => {
  expect(roleAllowed('vms', 'list', 'vm1')).toBe(true);
  expect(roleAllowed('vms', 'list', 'expA/vm1')).toBe(false);
});

test('resourceName braces are not expanded', () => {
  expect(roleAllowed('vms', 'post', 'expA/vm1')).toBe(false);
  expect(roleAllowed('vms', 'post', '{expA,expB}/vm1')).toBe(true);
});

test('resourceName negation within a namespace', () => {
  expect(roleAllowed('vms/start', 'update', 'expA/vm1')).toBe(true);
  expect(roleAllowed('vms/start', 'update', 'expA/secret')).toBe(false);
  expect(roleAllowed('vms/start', 'update', 'expB/vm1')).toBe(false);
});

test('any allowed resourceName grants access', () => {
  expect(roleAllowed('vms', 'update', 'expB/vm1', 'expA/vm1')).toBe(true);
  expect(roleAllowed('vms', 'update', 'expB/vm1', 'expC/vm1')).toBe(false);
});

test('resource globstar does not cross subresource boundaries', () => {
  expect(roleAllowed('vms', 'watch', 'vm1')).toBe(true);
  expect(roleAllowed('vms/start', 'watch', 'vm1')).toBe(false);
});

test('resourceName wildcard matches leading dots', () => {
  expect(roleAllowed('things', 'get', '.thing')).toBe(true);
});

test('resourceName negation', () => {
  expect(roleAllowed('things', 'delete', 'thing')).toBe(true);
  expect(roleAllowed('things', 'delete', 'thing1')).toBe(false);
  expect(roleAllowed('things', 'delete', 'thing2')).toBe(true);
});

test('resourceName mid-wildcard', () => {
  expect(roleAllowed('items', 'delete', 'item')).toBe(true);
  expect(roleAllowed('items', 'delete', 'item1')).toBe(true);
  expect(roleAllowed('items', 'delete', 'thing')).toBe(false);
});
