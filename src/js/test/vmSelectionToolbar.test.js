// The running experiment's selection toolbar on its own: the actions it offers
// on the selected VMs, and what it asks the page for.
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);

import VmSelectionToolbar from '@/components/experiment/VmSelectionToolbar.vue';
import { denyOnly } from './helpers/permissions.js';
import { byLabel, mountComponent } from './helpers/mount.js';

beforeEach(() => resetMocks());

const render = (props = {}) =>
  mountComponent(VmSelectionToolbar, {
    props: { selected: ['web', 'db'], experiment: 'demo', ...props },
  });

const labels = (wrapper) =>
  wrapper.findAll('button[aria-label]').map((b) => b.attributes('aria-label'));

describe('the actions it offers', () => {
  it('offers the main actions, or the modify state actions in their place', async () => {
    expect(labels(await render())).toEqual([
      'Start selected VMs',
      'Pause selected VMs',
      'Create memory snapshots of selected VMs',
      'Create backing images for selected VMs',
      'Create snapshots of selected VMs',
      'Modify state of selected VMs',
    ]);
    expect(labels(await render({ modifyOpen: true }))).toEqual([
      'Redeploy selected VMs',
      'Reset disk state of selected VMs',
      'Restart selected VMs',
      'Shut down selected VMs',
      'Kill selected VMs',
      'Close state actions',
    ]);
  });

  it.each([
    ['Start selected VMs', ['vms/start', 'update', 'demo/db'], false],
    ['Kill selected VMs', ['vms', 'delete', 'demo/db'], true],
  ])(
    'hides %s from a role without its permission on one VM',
    async (label, perm, modifyOpen) => {
      expect(labels(await render({ modifyOpen }))).toContain(label);
      rbac.allowed = denyOnly(perm);
      const wrapper = await render({ modifyOpen });
      expect(labels(wrapper)).not.toContain(label);
      // the rest stay, and the modify state button whatever the role
      expect(labels(wrapper)).toHaveLength(5);
    },
  );
});

describe('what it asks the page for', () => {
  it.each([
    ['Start selected VMs', false, 'start'],
    ['Create backing images for selected VMs', false, 'commit'],
    ['Reset disk state of selected VMs', true, 'resetDisk'],
    ['Kill selected VMs', true, 'kill'],
  ])('takes "%s" as the %s action', async (label, modifyOpen, action) => {
    const wrapper = await render({ modifyOpen });
    await byLabel(wrapper, label).trigger('click');
    expect(wrapper.emitted('act')).toEqual([[action]]);
  });

  it('opens and closes the modify state actions', async () => {
    const wrapper = await render();
    await byLabel(wrapper, 'Modify state of selected VMs').trigger('click');
    await wrapper.setProps({ modifyOpen: true });
    await byLabel(wrapper, 'Close state actions').trigger('click');
    expect(wrapper.emitted('update:modifyOpen')).toEqual([[true], [false]]);
    expect(wrapper.emitted('act')).toBeUndefined();
  });
});
