// Which resources and resource names a role's policies allow; mirrors
// role_test.go.
import fc from 'fast-check';
import { roleAllowed, userTokenAllowed } from '@/utils/rbac.js';
import { usePhenixStore } from '@/store.js';
import { beforeEach, test, expect, vi } from 'vitest';

vi.mock('@/store.js', () => ({ usePhenixStore: vi.fn() }));

const signIn = (role) => usePhenixStore.mockReturnValue({ role });

beforeEach(() =>
  signIn({
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
      {
        resources: ['notes'],
        resourceNames: ['note!'],
        verbs: ['get'],
      },
    ],
  }),
);

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

test('only a leading ! negates a resourceName', () => {
  expect(roleAllowed('notes', 'get', 'note!')).toBe(true);
  expect(roleAllowed('notes', 'get', 'note')).toBe(false);
});

test('allows nothing when no one is signed in', () => {
  signIn(null);
  expect(roleAllowed('experiments', 'get', 'expA')).toBe(false);
  expect(roleAllowed('vms', 'watch')).toBe(false);
});

test('an empty resourceName is never allowed', () => {
  expect(roleAllowed('experiments', 'get', '')).toBe(false);
  expect(roleAllowed('experiments', 'get', '', 'expA')).toBe(true);
});

test("answers from the signed-in role's own policies after a new sign in", () => {
  const role = (resourceNames) => ({
    name: 'Same Name',
    policies: [{ resources: ['experiments'], resourceNames, verbs: ['get'] }],
  });

  signIn(role(['exp1']));
  expect(roleAllowed('experiments', 'get', 'exp1')).toBe(true);
  expect(roleAllowed('experiments', 'get', 'exp2')).toBe(false);

  // a role of the same name with other policies
  signIn(role(['exp2']));
  expect(roleAllowed('experiments', 'get', 'exp1')).toBe(false);
  expect(roleAllowed('experiments', 'get', 'exp2')).toBe(true);
});

test('a policy with null resource names allows no resource name', () => {
  signIn({
    name: 'Null Resource Names',
    policies: [
      { resources: ['vms/forwards'], resourceNames: null, verbs: ['create'] },
    ],
  });

  expect(roleAllowed('vms/forwards', 'create')).toBe(true);
  expect(roleAllowed('vms/forwards', 'create', 'expA/vm1')).toBe(false);
});

test('a token for another user also needs users/tokens create on them', () => {
  const usersPatch = {
    resources: ['users'],
    resourceNames: ['alice', 'bob', 'carol'],
    verbs: ['patch'],
  };
  const signInAsAlice = (...policies) =>
    usePhenixStore.mockReturnValue({
      username: 'alice',
      role: { name: `Tokens ${policies.length}`, policies },
    });

  signInAsAlice(usersPatch);
  expect(userTokenAllowed('alice')).toBe(true);
  expect(userTokenAllowed('bob')).toBe(false);
  expect(userTokenAllowed('dave')).toBe(false);

  signInAsAlice(usersPatch, {
    resources: ['users/tokens'],
    resourceNames: ['bob'],
    verbs: ['create'],
  });
  expect(userTokenAllowed('bob')).toBe(true);
  expect(userTokenAllowed('carol')).toBe(false);
});

// Go's filepath.Match for patterns of literals, * and ?: neither crosses a /
const goMatch = (pattern, name) =>
  new RegExp(
    `^${[...pattern]
      .map((c) =>
        c === '*'
          ? '[^/]*'
          : c === '?'
            ? '[^/]'
            : c.replace(/[.^$+()|[\]{}\\]/, '\\$&'),
      )
      .join('')}$`,
  ).test(name);

// what policy.go and role.go allow
const nameAllowed = (policy, name) => {
  const hits = policy.resourceNames.filter((n) =>
    goMatch(n.replace(/^!/, ''), name),
  );
  return hits.length > 0 && !hits.some((n) => n.startsWith('!'));
};
const goAllowed = (policies, resource, verb, names) =>
  policies.some(
    (p) =>
      p.resources.some((r) => goMatch(r, resource)) &&
      p.verbs.some((v) => v === '*' || v === verb) &&
      (names.length === 0 || names.some((n) => nameAllowed(p, n))),
  );

const segment = (chars) =>
  fc.string({ unit: fc.constantFrom(...chars), minLength: 1, maxLength: 3 });
// a resource name: an experiment, a VM, or a VM within an experiment, at
// times with a trailing slash
const nameArb = (chars = 'ab.-!') =>
  fc
    .tuple(
      fc.array(segment(chars), { minLength: 1, maxLength: 2 }),
      fc.boolean(),
    )
    .map(([s, slash]) => s.join('/') + (slash ? '/' : ''));
const patternArb = fc
  .array(segment('ab.-*?!'), { minLength: 1, maxLength: 2 })
  .map((s) => s.join('/'));
const RESOURCES = ['vms', 'vms/start', 'experiments'];
const VERBS = ['get', 'patch'];
const policyArb = fc.record({
  resources: fc.array(
    fc.constantFrom(...RESOURCES, '*', '**', 'vms/*', '*/start'),
    { minLength: 1, maxLength: 2 },
  ),
  resourceNames: fc.array(
    fc
      .tuple(fc.boolean(), patternArb)
      .map(([negate, p]) => (negate ? `!${p}` : p)),
    { maxLength: 3 },
  ),
  verbs: fc.array(fc.constantFrom(...VERBS, '*'), {
    minLength: 1,
    maxLength: 2,
  }),
});
const queryArb = (names = nameArb()) =>
  fc.record({
    resource: fc.constantFrom(...RESOURCES),
    verb: fc.constantFrom(...VERBS),
    names: fc.array(names, { maxLength: 3 }),
  });

test('allows what the server allows for any policies and request', () => {
  fc.assert(
    fc.property(
      fc.array(policyArb, { maxLength: 3 }),
      queryArb(),
      (policies, { resource, verb, names }) => {
        signIn({ name: 'Any', policies });
        expect(roleAllowed(resource, verb, ...names)).toBe(
          goAllowed(policies, resource, verb, names),
        );
      },
    ),
  );
});

test('answers a request the same whether or not it asked before', () => {
  fc.assert(
    fc.property(
      fc.array(policyArb, { maxLength: 3 }),
      fc.array(queryArb(nameArb('ab')), { minLength: 1, maxLength: 8 }),
      (policies, queries) => {
        const ask = ({ resource, verb, names }) =>
          roleAllowed(resource, verb, ...names);
        signIn({ name: 'Cached', policies });
        const answers = queries.map(ask);

        // each asked of a new sign in, whose answers are not yet cached
        const fresh = queries.map((q) => {
          signIn({ name: 'Cached', policies });
          return ask(q);
        });
        expect(answers).toEqual(fresh);
      },
    ),
  );
});

test('tells one name holding a comma from two names', () => {
  signIn({
    name: 'Comma',
    policies: [{ resources: ['vms'], resourceNames: ['a'], verbs: ['get'] }],
  });
  expect(roleAllowed('vms', 'get', 'a,b')).toBe(false);
  expect(roleAllowed('vms', 'get', 'a', 'b')).toBe(true);
});

test('matches dot and empty name segments as the server does', () => {
  signIn({
    name: 'Dots',
    policies: [
      { resources: ['vms'], resourceNames: ['*', 'exp/*'], verbs: ['get'] },
    ],
  });
  expect(roleAllowed('vms', 'get', '.')).toBe(true);
  expect(roleAllowed('vms', 'get', '..')).toBe(true);
  expect(roleAllowed('vms', 'get', 'exp/')).toBe(true);
  expect(roleAllowed('vms', 'get', 'vm1/')).toBe(false);
});
