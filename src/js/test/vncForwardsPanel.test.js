// The Port forwards panel of the console top bar: the list of the VM's port
// forwards, Remove, Add port forward, the Tunneler link, and the greyed-out
// rule for what the role or the VM state rules out.
import { flushPromises } from '@vue/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, notify, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/errorNotif', modules.errorNotif);

import VncForwardsPanel from '@/vnc/VncForwardsPanel.vue';
import { vm } from './helpers/fixtures.js';
import {
  mountComponent,
  textOf,
  toasts,
  tooltipButtons,
} from './helpers/mount.js';
import { allowAll, denyOnly } from './helpers/permissions.js';

const URL = 'experiments/demo/vms/web/forwards';

const forward = (srcPort, dstPort, owner = 'alice') => ({
  srcPort,
  dstHost: '127.0.0.1',
  dstPort,
  owner,
});

let listeners;

function mountPanel(props = {}) {
  return mountComponent(VncForwardsPanel, {
    props: { experiment: 'demo', vm: 'web', vmRow: vm(), open: true, ...props },
    attachTo: document.body,
  });
}

// each row as its host port, destination and owner cells
const cells = (wrapper) =>
  wrapper
    .findAll('.vnc-forward')
    .map((row) =>
      [
        '.vnc-forward-port',
        '.vnc-forward-destination',
        '.vnc-forward-owner',
      ].map((part) => textOf(row.find(part))),
    );

const rows = (wrapper) =>
  cells(wrapper).map(([port, dst, owner]) => `${port} → ${dst} ${owner}`);

const control = (wrapper, label) =>
  tooltipButtons(wrapper).find(
    (t) => t.label === label || textOf(t.control) === label,
  );

beforeEach(() => {
  resetMocks();
  listeners = [forward(40001, 22), forward(40002, 80, 'bob')];
  axios.get.mockImplementation(async (url) =>
    url === URL ? { data: { listeners } } : Promise.reject(new Error(url)),
  );
  axios.delete.mockImplementation(async () => {
    listeners = listeners.slice(1);
    return { data: {} };
  });
});

describe('the list', () => {
  it('lists the port forwards of the VM when it opens', async () => {
    const wrapper = await mountPanel({ open: false });
    await flushPromises();
    expect(axios.get).not.toHaveBeenCalled();
    await wrapper.setProps({ open: true });
    await flushPromises();
    expect(axios.get).toHaveBeenCalledExactlyOnceWith(URL);
    expect(rows(wrapper)).toEqual([
      '40001 → 127.0.0.1:22 alice',
      '40002 → 127.0.0.1:80 bob',
    ]);
  });

  it('shows the host port, destination, owner and Remove of each forward in their own columns', async () => {
    const wrapper = await mountPanel();
    await flushPromises();
    expect(
      wrapper.findAll('.vnc-forward-table th').map((th) => textOf(th)),
    ).toEqual(['Host port', 'Destination', 'Owner', 'Remove']);
    expect(cells(wrapper)).toEqual([
      ['40001', '127.0.0.1:22', 'alice'],
      ['40002', '127.0.0.1:80', 'bob'],
    ]);
    for (const row of wrapper.findAll('.vnc-forward')) {
      expect(row.findAll('td')).toHaveLength(4);
      expect(row.find('td:last-child .vnc-forward-remove').exists()).toBe(true);
    }
  });

  it('says when the VM has none', async () => {
    listeners = [];
    const wrapper = await mountPanel();
    await flushPromises();
    expect(textOf(wrapper.find('.vnc-panel-note'))).toBe(
      'The VM has no port forwards.',
    );
  });

  it('reads the list again when refresh changes', async () => {
    const wrapper = await mountPanel();
    await flushPromises();
    listeners = [...listeners, forward(40003, 443)];
    await wrapper.setProps({ refresh: 1 });
    await flushPromises();
    expect(axios.get).toHaveBeenCalledTimes(2);
    expect(rows(wrapper)).toHaveLength(3);
  });

  it('names the permission when the role may not list them, and asks nothing', async () => {
    rbac.allowed = denyOnly(['vms/forwards', 'list']);
    const wrapper = await mountPanel();
    await flushPromises();
    expect(axios.get).not.toHaveBeenCalled();
    expect(textOf(wrapper.find('.vnc-panel-note'))).toBe(
      'Your role does not permit this action (vms/forwards list)',
    );
  });

  it('says when it cannot read the list', async () => {
    axios.get.mockRejectedValue(new Error('down'));
    const wrapper = await mountPanel();
    await flushPromises();
    expect(notify.error).toHaveBeenCalledOnce();
    expect(textOf(wrapper.find('.vnc-panel-note'))).toBe(
      'Could not list the port forwards.',
    );
  });
});

describe('Remove', () => {
  const remove = (wrapper, index) =>
    tooltipButtons(wrapper).filter((t) =>
      t.control.classes('vnc-forward-remove'),
    )[index];

  it('removes an own port forward, then lists them again', async () => {
    const wrapper = await mountPanel();
    await flushPromises();
    await remove(wrapper, 0).control.trigger('click');
    await flushPromises();
    expect(axios.delete).toHaveBeenCalledExactlyOnceWith(URL, {
      params: { host: '127.0.0.1', dst: 22 },
    });
    expect(axios.get).toHaveBeenCalledTimes(2);
    expect(rows(wrapper)).toEqual(['40002 → 127.0.0.1:80 bob']);
    expect(toasts().map((t) => t.text)).toContain(
      'Removed the port forward 40001 → 127.0.0.1:22 of the web VM',
    );
  });

  it.each([
    [allowAll, 1, 'Only bob, who made this port forward, can remove it'],
    [
      denyOnly(['vms/forwards', 'delete']),
      0,
      'Your role does not permit this action (vms/forwards delete)',
    ],
  ])(
    'greys out Remove with the reason (%#)',
    async (allowed, index, reason) => {
      rbac.allowed = allowed;
      const wrapper = await mountPanel();
      await flushPromises();
      const button = remove(wrapper, index);
      expect(button).toMatchObject({ tooltip: reason, disabled: true });
      await button.control.trigger('click');
      expect(axios.delete).not.toHaveBeenCalled();
    },
  );
});

describe('Add port forward', () => {
  it('asks the page for the add dialog', async () => {
    const wrapper = await mountPanel();
    await flushPromises();
    const add = control(wrapper, 'Add port forward');
    expect(add.disabled).toBe(false);
    await add.control.trigger('click');
    expect(wrapper.emitted('add')).toEqual([[]]);
  });

  it.each([
    [
      allowAll,
      vm({ ccActive: false }),
      'Cannot forward a port: it needs an active miniccc agent in the VM',
    ],
    [
      allowAll,
      vm({ state: 'QUIT', running: false }),
      'Cannot forward a port: the VM is not running',
    ],
    [
      denyOnly(['vms/forwards', 'create']),
      vm(),
      'Your role does not permit this action (vms/forwards create)',
    ],
  ])('is greyed out with the reason (%#)', async (allowed, row, reason) => {
    rbac.allowed = allowed;
    const wrapper = await mountPanel({ vmRow: row });
    await flushPromises();
    const add = control(wrapper, 'Add port forward');
    expect(add).toMatchObject({ tooltip: reason, disabled: true });
    await add.control.trigger('click');
    expect(wrapper.emitted('add')).toBeUndefined();
  });
});

describe('Tunneler', () => {
  it('links to the Tunneler page in a new tab', async () => {
    const wrapper = await mountPanel();
    await flushPromises();
    const link = wrapper.find('.vnc-forward-tunneler');
    expect(link.attributes('href')).toBe('/tunneler');
    expect(link.attributes('target')).toBe('_blank');
    expect(textOf(link)).toBe('Tunneler');
  });
});
