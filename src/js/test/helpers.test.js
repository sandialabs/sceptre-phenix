// The roles the mocked roleAllowed answers with (helpers/permissions.js),
// and the mount helpers' element lookups (helpers/mount.js).
import { mount } from '@vue/test-utils';
import fc from 'fast-check';
import { h } from 'vue';
import { describe, expect, it } from 'vitest';

import { byLabel } from './helpers/mount.js';
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

describe('byLabel', () => {
  it('finds the element with any aria-label, quotes and all', () => {
    fc.assert(
      fc.property(fc.string({ minLength: 1 }), fc.string(), (label, other) => {
        fc.pre(label !== other);
        const wrapper = mount({
          render: () =>
            h('div', [
              h('button', { 'aria-label': other }, 'other'),
              h('button', { 'aria-label': label }, 'wanted'),
            ]),
        });
        expect(byLabel(wrapper, label).text()).toBe('wanted');
        expect(byLabel(wrapper, `${label}x`).exists()).toBe(
          `${label}x` === other,
        );
        wrapper.unmount();
      }),
    );
  });
});
