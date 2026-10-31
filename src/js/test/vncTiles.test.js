// The running experiment's VNC tab on its own: a tile per VM with a console,
// and its zoom slider.
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);

import VncTiles from '@/components/experiment/VncTiles.vue';
import { vm } from './helpers/fixtures.js';
import { denyOnly } from './helpers/permissions.js';
import { mountComponent, textOf } from './helpers/mount.js';

beforeEach(() => resetMocks());

const render = (props = {}) =>
  mountComponent(VncTiles, {
    route: '/experiment/demo',
    props: { experiment: 'demo', zoom: 4, width: 200, ...props },
  });

const tileNames = (wrapper) => wrapper.findAll('.vnc-tile-name').map(textOf);

describe('the tiles', () => {
  it('shows the VMs with a console, each opening it', async () => {
    const wrapper = await render({
      vms: [
        vm(),
        vm({ name: 'hil', external: true }),
        vm({ name: 'off', dnb: true }),
      ],
    });
    expect(tileNames(wrapper)).toEqual(['web']);
    const img = wrapper.find('img');
    expect(img.attributes('width')).toBe('200');
    expect(img.attributes('alt')).toBe(
      'Open the console of VM web: Loading the screenshot of VM web',
    );
    expect(wrapper.find('a[href]').attributes('href')).toBe(
      '/api/v1/experiments/demo/vms/web/vnc?token=t',
    );
  });

  it.each([
    ['loading until the experiment has', '', 'Loading VMs…'],
    ['empty when it has none', 'demo', 'No VMs with a VNC console'],
  ])('says it is %s', async (_, experiment, text) => {
    expect(textOf(await render({ experiment, vms: [] }))).toContain(text);
  });

  it("asks for a VM's details by its name, if the role may get it", async () => {
    const wrapper = await render({ vms: [vm()] });
    await wrapper.find('button.vnc-tile-name').trigger('click');
    expect(wrapper.emitted('info')).toEqual([[vm()]]);

    rbac.allowed = denyOnly(['vms', 'get']);
    const denied = await render({ vms: [vm()] });
    expect(denied.find('button.vnc-tile-name').exists()).toBe(false);
    expect(tileNames(denied)).toEqual(['web']);
  });
});

it('passes on the zoom picked', async () => {
  const wrapper = await render({ vms: [] });
  wrapper.findComponent({ name: 'BSlider' }).vm.$emit('update:modelValue', 8);
  expect(wrapper.emitted('update:zoom')).toEqual([[8]]);
});
