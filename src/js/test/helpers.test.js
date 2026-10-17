// The roles the mocked roleAllowed answers with (helpers/permissions.js).
import { describe, expect, it } from 'vitest';

import {
  allowAll,
  allowOnly,
  denyAll,
  denyOnly,
} from './helpers/permissions.js';

describe('permissions', () => {
  it('allows or denies everything', () => {
    expect(allowAll('vms', 'delete', 'web')).toBe(true);
    expect(denyAll('vms', 'list')).toBe(false);
  });

  it.each([
    // [permission, resource, verb, name, matches]
    [['vms'], 'vms', 'delete', 'web', true],
    [['vms'], 'disks', 'delete', 'web', false],
    [['vms', 'patch'], 'vms', 'patch', 'web', true],
    [['vms', 'patch'], 'vms', 'list', 'web', false],
    [['*', 'delete'], 'disks', 'delete', undefined, true],
    [['*', 'delete'], 'disks', 'get', undefined, false],
    [['files', 'get', 'exp1'], 'files', 'get', 'exp1', true],
    [['files', 'get', 'exp1'], 'files', 'get', 'exp2', false],
    [['files', 'get', '*'], 'files', 'get', 'exp2', true],
  ])('%j matching %s %s %s is %s', (perm, resource, verb, name, matches) => {
    expect(allowOnly(perm)(resource, verb, name)).toBe(matches);
    expect(denyOnly(perm)(resource, verb, name)).toBe(!matches);
  });

  it('allows what any one of several permissions matches', () => {
    const role = allowOnly(['vms', 'list'], ['disks']);
    expect(role('vms', 'list')).toBe(true);
    expect(role('disks', 'create')).toBe(true);
    expect(role('vms', 'delete')).toBe(false);
  });
});
