// The running experiment's VM table on its own: what it draws for each VM and
// what it asks the page for.
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);

import loadingImg from '@/assets/imgs/loading-screenshot.svg';
import notAvailableImg from '@/assets/imgs/not-available.png';
import VmRowActions from '@/components/experiment/VmRowActions.vue';
import VmTable from '@/components/experiment/VmTable.vue';
import { useTable } from '@/utils/useTable.js';
import { vm } from './helpers/fixtures.js';
import { denyOnly } from './helpers/permissions.js';
import { componentDeclarations, contrast } from './helpers/css.js';
import {
  mountComponent,
  tableHeader,
  tableRows,
  textOf,
  tooltipPositions,
} from './helpers/mount.js';

beforeEach(() => resetMocks());

const COLUMNS = {
  screenshot: true,
  host: true,
  ipv4: true,
  network: true,
  taps: true,
  uptime: true,
  labels: true,
  actions: true,
};

// web, capturing on its second interface, and db, busy
const web = vm({ captures: [{ interface: 1 }], host: 'node1' });
const db = vm({ name: 'db', busy: true, percent: 40 });

// the table on the demo experiment showing `vms`, with these props
const render = (vms = [web, db], props = {}) =>
  mountComponent(VmTable, {
    route: '/experiment/demo',
    props: {
      vms,
      experiment: 'demo',
      table: useTable().table,
      columns: COLUMNS,
      ...props,
    },
  });

const rowOf = (wrapper, name) =>
  tableRows(wrapper).find((cells) => textOf(cells.Node.find('.tag')) === name);

describe('the rows', () => {
  it('names each VM in a tag colored by its state, with a busy one progressing', async () => {
    const wrapper = await render();
    expect(
      tableRows(wrapper).map((cells) => [
        textOf(cells.Node.find('.tag')),
        cells.Node.find('.tag').classes('is-success'),
        cells.Node.find('progress').exists(),
      ]),
    ).toEqual([
      ['web', true, false],
      ['db', false, true],
    ]);
  });

  it('says why it has no rows', async () => {
    const wrapper = await render([], { emptyText: 'Loading VMs…' });
    expect(textOf(wrapper.find('tbody'))).toBe('Loading VMs…');
  });

  it('leaves out the columns the page hides', async () => {
    const wrapper = await render([web], {
      columns: { ...COLUMNS, host: false, labels: false },
    });
    const columns = Object.keys(tableRows(wrapper)[0]);
    expect(columns).not.toContain('Host');
    expect(columns).not.toContain('Labels');
    expect(columns).toContain('Taps');
  });

  it('shows the Delay column only while a VM waits to start', async () => {
    const delayed = vm({
      name: 'late',
      state: 'BUILDING',
      delayed_start: '5m',
    });
    const columns = async (vms) => Object.keys(tableRows(await render(vms))[0]);
    expect(await columns([web])).not.toContain('Delay');
    expect(await columns([web, delayed])).toContain('Delay');
  });

  it('marks the captured interfaces and the ones waiting on the server', async () => {
    const wrapper = await render([web], { capturePending: { 'web/0': true } });
    const { IP } = rowOf(wrapper, 'web');
    const ifaces = IP.findAll('.field > div');
    expect(ifaces.map((d) => d.classes('is-success'))).toEqual([false, true]);
    expect(ifaces.map((d) => d.attributes('aria-busy'))).toEqual([
      'true',
      'false',
    ]);
    expect(IP.findAll('.capture-spinner')).toHaveLength(1);
  });

  it('pictures a VM waiting for its screenshot until they are due', async () => {
    const shot = async (screenshotsDue) =>
      rowOf(await render([web], { screenshotsDue }), 'web')
        .Screenshot.find('img')
        .attributes('src');
    expect(await shot(false)).toBe(loadingImg);
    expect(await shot(true)).toBe(notAvailableImg);
  });

  it('links a running VM to its console', async () => {
    const link = rowOf(await render([web]), 'web').Screenshot.find('a');
    expect(link.attributes('href')).toBe(
      '/api/v1/experiments/demo/vms/web/vnc?token=t',
    );
  });

  it('counts labels and notes beside the tag icon, and shows no count for none', async () => {
    const vms = [
      vm({ name: 'bare', tags: {} }),
      vm({ name: 'untagged' }),
      vm({ name: 'hidden', tags: { __internal: 'x' } }),
      vm({ name: 'tagged', tags: { role: 'web', __notes_1: 'n', __x: '' } }),
    ];
    const wrapper = await render(vms);
    const count = (name) => {
      const cell = rowOf(wrapper, name).Labels;
      expect(cell.find('[data-icon="tag"]').exists()).toBe(true);
      const badge = cell.find('.vm-label-count-badge');
      return badge.exists() ? textOf(badge) : null;
    };
    expect(vms.map((v) => count(v.name))).toEqual([null, null, null, '2']);

    // a neutral gray, not red, with readable text
    const badge = componentDeclarations(
      'src/components/experiment/VmLabelCount.vue',
      '.vm-label-count-badge',
    );
    expect(badge['background-color']).toBe('#686868');
    expect(
      contrast(badge.color, badge['background-color']),
    ).toBeGreaterThanOrEqual(4.5);
  });

  it('shows how long a VM has run, a muted dash for one not running, and unknown for an external node', async () => {
    const wrapper = await render([
      vm({ name: 'up', uptime: 3725 }),
      vm({ name: 'down', running: false, state: 'QUIT', uptime: 0 }),
      vm({ name: 'ext', external: true, running: false }),
    ]);
    const uptime = (name) => rowOf(wrapper, name).Uptime;
    expect(textOf(uptime('up'))).toBe('01:02:05');
    expect(textOf(uptime('ext'))).toBe('unknown');

    const dash = uptime('down').find('.vm-uptime-none');
    expect([textOf(dash), dash.attributes('aria-hidden')]).toEqual([
      '—',
      'true',
    ]);
    expect(textOf(uptime('down').find('.is-sr-only'))).toBe('not running');
    const { color } = componentDeclarations(
      'src/components/experiment/VmTable.vue',
      '.vm-uptime-none',
    );
    // on the table's $background-bis
    expect(contrast(color, '#484848')).toBeGreaterThanOrEqual(4.5);
  });

  // the column ends at the window's right edge
  it('opens the Actions tooltips to the left, inside the window', async () => {
    const positions = tooltipPositions(
      rowOf(await render([web]), 'web').Actions,
    );
    expect(positions.length).toBeGreaterThan(2);
    expect(new Set(positions)).toEqual(new Set(['is-left']));
  });

  it('hides the Actions column from roles that can take none of its actions', async () => {
    rbac.allowed = (resource) => !resource.startsWith('vms/');
    expect(Object.keys(tableRows(await render([web]))[0])).not.toContain(
      'Actions',
    );
  });
});

describe('what it asks the page for', () => {
  it.each([
    ['info', 'Node', '.tag .field > div', [web]],
    ['capture', 'Taps', '.field > div:nth-child(2)', [web, 1]],
    ['change-vlan', 'Network', '.field > div:nth-child(2)', [web, 1]],
    ['edit-labels', 'Labels', '.is-clickable', [web]],
  ])('emits %s from the %s cell', async (event, column, selector, args) => {
    const wrapper = await render([web]);
    await rowOf(wrapper, 'web')[column].find(selector).trigger('click');
    expect(wrapper.emitted(event)).toEqual([args]);
  });

  it('opens the details alone of a VM the role may not get', async () => {
    rbac.allowed = denyOnly(['vms', 'get']);
    const wrapper = await render([web]);
    await rowOf(wrapper, 'web').Node.find('.field > div').trigger('click');
    expect(wrapper.emitted('details')).toEqual([[web]]);
    expect(wrapper.emitted('info')).toBeUndefined();
  });

  it.each([
    ['vms/captures', 'create', 'IP'],
    ['vms', 'patch', 'Network'],
  ])(
    'lists the %s cell as text for a role without %s',
    async (resource, verb, column) => {
      rbac.allowed = denyOnly([resource, verb]);
      const cell = rowOf(await render([web]), 'web')[column];
      expect(cell.find('.field').exists()).toBe(false);
      expect(textOf(cell)).toMatch(/, /);
    },
  );

  it("passes on its Actions column's actions with their VM", async () => {
    const wrapper = await render([web]);
    wrapper.findComponent(VmRowActions).vm.$emit('action', 'kill');
    expect(wrapper.emitted('action')).toEqual([['kill', web]]);
  });

  it('spins the capture actions of a VM starting or stopping them all', async () => {
    const wrapper = await render([web], { captureAllPending: { web: true } });
    expect(wrapper.findComponent(VmRowActions).props('pending')).toEqual([
      'captureAll',
      'stopCaptures',
    ]);
  });

  it.each([
    ['shutdown', false, 'start'],
    ['start', true, 'shutdown'],
  ])(
    'keeps spinning a pending %s after the VM swaps its button',
    async (action, running, shown) => {
      const wrapper = await render([vm({ running })], {
        actionPending: { web: [action] },
      });
      const button = wrapper.find(`.vm-row-${shown}`);
      expect(button.classes()).toContain('is-loading');
      expect(button.attributes('disabled')).toBeDefined();
    },
  );

  it('selects VMs and all of them through its checkboxes', async () => {
    const wrapper = await render([web, vm({ name: 'ext', external: true })], {
      selected: ['web'],
    });
    const box = (label) =>
      wrapper
        .findAll('label.b-checkbox')
        .find((b) => textOf(b) === label)
        .find('input');

    expect(box('Select VM web').element.checked).toBe(true);
    expect(box('Select VM ext').attributes('disabled')).toBeDefined();
    await box('Select VM web').setValue(false);
    expect(wrapper.emitted('update:selected')).toEqual([[[]]]);
    await box('Select all VMs').setValue(true);
    expect(wrapper.emitted('update:checkAll')).toEqual([[true]]);
  });

  it.each([
    ['Labels', 'labels'],
    ['Network', 'network'],
    ['Taps', 'taps'],
  ])('asks to sort by the %s column, both ways', async (header, field) => {
    const wrapper = await render();
    await tableHeader(wrapper, header).trigger('click');
    await tableHeader(wrapper, header).trigger('click');
    expect(wrapper.emitted('sort')).toEqual([
      [field, 'asc'],
      [field, 'desc'],
    ]);
  });

  it('gives the rows it shows', async () => {
    const wrapper = await render();
    expect(wrapper.vm.visibleData().map((row) => row.name)).toEqual([
      'web',
      'db',
    ]);
  });
});
