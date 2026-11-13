// The VLANs page: its three views drawn from a cluster with every issue kind,
// the filters in the query string, the keyboard on the ID grid, the details
// panel, each fix's requests and the role that hides it, and live updates.
import { flushPromises } from '@vue/test-utils';
import { nextTick } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const {
  axios,
  capturing,
  loaderSpy,
  modules,
  notify,
  rbac,
  resetMocks,
  saver,
  ws,
} = await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('file-saver', modules.fileSaver);
// a loader that records its options and loads nothing
vi.mock('@/utils/pageLoader.js', capturing);

import { CHECK_DELAY_MS } from '@/utils/vlanActions.js';
import { RUNNING_TIP, normalizeVlans } from '@/utils/vlans.js';
import Vlans from '@/views/Vlans.vue';
import { httpError, wsMsg } from './helpers/fixtures.js';
import {
  buttons,
  confirmDialog,
  dialogs,
  mountPage,
  textOf,
  toasts,
  tooltipButtons,
} from './helpers/mount.js';
import { denyOnly } from './helpers/permissions.js';
import { vlansFixture } from './helpers/vlansFixture.js';

beforeEach(() => {
  resetMocks({ store: { token: 't', features: [], role: null } });
  axios.get.mockResolvedValue({ data: {} });
  axios.post.mockResolvedValue({ data: { issues: [] } });
  axios.put.mockResolvedValue({ data: {} });
  axios.delete.mockResolvedValue({ data: {} });
});

afterEach(() => {
  vi.useRealTimers();
});

// the page at route, showing the fixture changed by edit
function page(route = '/vlans', edit = (body) => body) {
  return mountPage(Vlans, {
    route,
    attachTo: document.body,
    data: { data: normalizeVlans(edit(vlansFixture())), loaded: true },
  });
}

const card = (wrapper, key) => wrapper.find(`[data-card="${key}"]`);
const expCard = (wrapper, name) => card(wrapper, `experiment:${name}`);
const cardNames = (wrapper) =>
  wrapper.findAll('.vlan-card-select').map((b) => textOf(b));
const query = (wrapper) => wrapper.router.currentRoute.value.query;

// clicks the button with this text, or this label for an icon button, in node
async function click(node, text) {
  const button = node
    .findAll('button, a.button')
    .find((b) => (textOf(b) || b.attributes('aria-label')) === text);
  if (!button) throw new Error(`no button ${text}: ${buttons(node)}`);
  await button.trigger('click');
  await flushPromises();
}

// the newest dialog's text
const dialogText = () => textOf(dialogs().at(-1));

describe('the By experiment view', () => {
  it('shows a card for each experiment and the unowned namespace, issues first', async () => {
    const wrapper = await page();
    expect(cardNames(wrapper)).toEqual([
      'lab',
      'old-demo',
      'scratch',
      'substation-a',
      'red-team-2',
      'ics-training',
    ]);
    // the blacklist card has its own view
    expect(card(wrapper, 'blacklist').exists()).toBe(false);
    expect(textOf(wrapper.find('.vlans-banner'))).toContain(
      '7 issues: 3 conflicts, 2 differences, 2 warnings.',
    );
  });

  it('marks each alias with its sources, its worst issue and its message', async () => {
    const wrapper = await page();
    const aliases = expCard(wrapper, 'substation-a')
      .findAll('.vlan-alias')
      .map((li) => [
        textOf(li.find('.vlan-alias-name')),
        textOf(li.find('.vlan-alias-ids')),
        li.findAll('.vlan-source.is-set').length,
        li.find('.vlan-issue-tag').exists()
          ? textOf(li.find('.vlan-issue-tag'))
          : '',
        li.classes().find((c) => c.startsWith('is-')) ?? '',
      ]);
    expect(aliases).toEqual([
      ['CORP', '104 → 110', 2, 'Differs', 'is-difference'],
      ['FIELD', '103', 2, '', ''],
      ['MGMT', '101', 2, '', ''],
      ['SCADA', '102', 2, 'Conflict', 'is-conflict'],
    ]);
    const corp = expCard(wrapper, 'substation-a').findAll('.vlan-alias')[0];
    expect(textOf(corp.find('.vlan-alias-message'))).toBe(
      'phēnix pins 104. minimega allocated 110.',
    );
    const tap = expCard(wrapper, 'red-team-2').findAll('.vlan-alias')[2];
    expect(textOf(tap.find('.vlan-issue-tag'))).toBe('minimega only');
    // the phēnix chip is dashed, with words for a screen reader
    expect(tap.findAll('.vlan-source').map((s) => textOf(s))).toEqual([
      'phēnix does not pin it',
      'minimega allocated 330',
    ]);
    expect(textOf(expCard(wrapper, 'ics-training'))).toContain(
      '150–199 overlaps substation-a.',
    );
    expect(textOf(expCard(wrapper, 'lab'))).toContain('Blacklisted');
    expect(textOf(card(wrapper, 'namespace:old-demo'))).toContain(
      'No experiment has the namespace old-demo.',
    );
  });

  it('sets the pinned aliases apart from the others by edge, ground and a thumbtack', async () => {
    const wrapper = await page();
    const rows = (name) =>
      expCard(wrapper, name)
        .findAll('.vlan-alias')
        .map((li) => [
          textOf(li.find('.vlan-alias-name')),
          li.classes('vlan-alias-pinned')
            ? 'pinned'
            : li.classes('vlan-alias-unpinned')
              ? 'unpinned'
              : '',
          li.find('.vlan-pin-icon').exists(),
        ]);
    expect(rows('red-team-2')).toEqual([
      ['RT_C2', 'pinned', true],
      ['RT_TARGET', 'pinned', true],
      ['TAP_TEMP', 'unpinned', false],
    ]);
    // minimega alone holds the IDs of a namespace with no experiment
    expect(
      card(wrapper, 'namespace:old-demo')
        .findAll('.vlan-alias')
        .every((li) => li.classes('vlan-alias-unpinned')),
    ).toBe(true);
  });

  it('offers the fixes each card needs', async () => {
    const wrapper = await page();
    expect(
      buttons(expCard(wrapper, 'scratch').find('.vlan-card-actions')),
    ).toEqual(['Change EXP to 1…', 'Edit IDs…', 'Set range…']);
    expect(
      buttons(expCard(wrapper, 'ics-training').find('.vlan-card-actions')),
    ).toEqual(['Edit IDs…', 'Suggest a range…']);
    expect(
      buttons(card(wrapper, 'namespace:old-demo').find('.vlan-card-actions')),
    ).toEqual(['Release 4 IDs…']);
    const blacklist = await page('/vlans?view=blacklist');
    expect(
      buttons(card(blacklist, 'blacklist').find('.vlan-card-actions')),
    ).toEqual(['Add an ID…']);
  });

  it('greys out the pins and range of a running experiment, but not Pin live IDs', async () => {
    const wrapper = await page();
    const sub = expCard(wrapper, 'substation-a');
    expect(buttons(sub.find('.vlan-card-actions'))).toEqual([
      'Edit IDs… (disabled)',
      // ics-training's range overlaps its own
      'Suggest a range… (disabled)',
      'Pin live IDs…',
    ]);
    const tips = tooltipButtons(sub).filter((t) => t.disabled);
    expect(tips.map((t) => t.tooltip)).toEqual([
      `Edit IDs: ${RUNNING_TIP}`,
      `Suggest a range: ${RUNNING_TIP}`,
    ]);
  });

  it('draws the ID, range and pin fixes and the experiment link as icons with tooltips', async () => {
    const wrapper = await page();
    const tips = tooltipButtons(expCard(wrapper, 'substation-a'));
    expect(tips.map((t) => [t.label, t.tooltip])).toEqual([
      ['Edit IDs…', `Edit IDs: ${RUNNING_TIP}`],
      ['Suggest a range…', `Suggest a range: ${RUNNING_TIP}`],
      [
        'Pin live IDs…',
        'Pin live IDs: copy the VLAN IDs that minimega allocated to this ' +
          'running experiment into its spec, so that the next start gives ' +
          'the same IDs',
      ],
      ['Open the experiment substation-a', 'Open the experiment'],
    ]);
    // each shows its icon only; the words are its tooltip and its name
    expect(tips.map((t) => textOf(t.control))).toEqual(['', '', '', '']);
    expect(
      tips.map((t) => t.control.findComponent({ name: 'BIcon' }).props('icon')),
    ).toEqual(['pencil', 'arrows-left-right', 'thumbtack', 'flask']);
    expect(tips.at(-1).control.attributes('href')).toContain('substation-a');
    const ranged = tooltipButtons(expCard(wrapper, 'scratch'));
    expect(ranged.map((t) => t.tooltip)).toEqual([
      'Edit IDs',
      'Set range',
      'Open the experiment',
    ]);
  });

  it.each([
    [
      'pins and ranges',
      '/vlans',
      denyOnly(['experiments', 'patch']),
      (w) => buttons(expCard(w, 'scratch')),
      ['Edit IDs…', 'Set range…', 'Change EXP to 1…'],
    ],
    [
      'blacklist additions',
      '/vlans?view=blacklist',
      denyOnly(['vlans/blacklist', 'create']),
      (w) => buttons(card(w, 'blacklist')),
      ['Add an ID…'],
    ],
    [
      'blacklist removals',
      '/vlans?view=blacklist',
      denyOnly(['vlans/blacklist', 'delete']),
      (w) => buttons(card(w, 'blacklist')),
      ['Remove 100 from the blacklist'],
    ],
    [
      'releases',
      '/vlans',
      denyOnly(['vlans/allocations', 'delete']),
      (w) => buttons(card(w, 'namespace:old-demo')),
      ['Release 4 IDs…'],
    ],
  ])(
    'hides %s from a role that may not make them',
    async (_, route, role, read, hidden) => {
      const allowed = await page(route);
      for (const label of hidden) expect(read(allowed)).toContain(label);
      allowed.unmount();

      rbac.allowed = role;
      const refused = await page(route);
      for (const label of hidden) expect(read(refused)).not.toContain(label);
    },
  );
});

describe('the filters and the query string', () => {
  it('shows only the cards a chip matches, and keeps the chip in the query', async () => {
    const wrapper = await page();
    const chips = wrapper.findAll('.vlans-chip').map((c) => textOf(c));
    expect(chips).toEqual([
      'All 6',
      'Issues 6',
      'Running 2',
      'Stopped 3',
      'Unowned 1',
    ]);
    await click(wrapper, 'Running 2');
    expect(query(wrapper)).toEqual({ filter: 'running' });
    expect(cardNames(wrapper)).toEqual(['substation-a', 'red-team-2']);
  });

  it('turns the issues filter on and off from the banner', async () => {
    const wrapper = await page();
    await click(wrapper.find('.vlans-banner'), 'Show issues only');
    expect(query(wrapper)).toEqual({ filter: 'issues' });
    expect(cardNames(wrapper)).not.toContain('Blacklist');
    await click(wrapper.find('.vlans-banner'), 'Show issues only');
    expect(query(wrapper)).toEqual({});
  });

  it('searches by ID, alias or experiment', async () => {
    const wrapper = await page();
    await wrapper.find('.vlans-search input').setValue('302');
    await flushPromises();
    expect(query(wrapper)).toEqual({ q: '302' });
    expect(cardNames(wrapper)).toEqual(['red-team-2']);
  });

  it('opens on the experiment and filter a link names', async () => {
    const wrapper = await page('/vlans?experiment=scratch&filter=issues');
    expect(cardNames(wrapper)).toEqual(['scratch']);
    expect(expCard(wrapper, 'scratch').classes()).toContain('is-selected');
    const chip = wrapper.find('.vlans-exp-chip');
    expect(textOf(chip)).toBe('Experiment: scratch');
    await chip.find('button').trigger('click');
    await flushPromises();
    expect(query(wrapper)).toEqual({ filter: 'issues' });
  });

  it('keeps the ID space view in the query', async () => {
    const wrapper = await page();
    await click(wrapper.find('.vlans-views'), 'ID space');
    expect(query(wrapper)).toEqual({ view: 'grid' });
    expect(wrapper.find('.vlan-grid').exists()).toBe(true);
    await click(wrapper.find('.vlans-views'), 'By experiment');
    expect(query(wrapper)).toEqual({});
  });

  it('keeps the Blacklist view in the query, with only the blacklist card and no chips', async () => {
    const wrapper = await page();
    const views = wrapper.find('.vlans-views');
    const pressed = () =>
      views.findAll('button[aria-pressed="true"]').map((b) => textOf(b));
    expect(buttons(views)).toEqual(['By experiment', 'ID space', 'Blacklist']);
    await click(views, 'Blacklist');
    expect(query(wrapper)).toEqual({ view: 'blacklist' });
    expect(pressed()).toEqual(['Blacklist']);
    expect(cardNames(wrapper)).toEqual(['Blacklist']);
    expect(
      card(wrapper, 'blacklist').element.parentElement.classList,
    ).toContain('vlans-blacklist');
    expect(wrapper.find('.vlans-chips').exists()).toBe(false);
    await click(views, 'By experiment');
    expect(query(wrapper)).toEqual({});
    expect(pressed()).toEqual(['By experiment']);
    expect(card(wrapper, 'blacklist').exists()).toBe(false);
    expect(wrapper.find('.vlans-chips').exists()).toBe(true);
  });

  it('opens the Blacklist view from the link of the old Blacklist chip', async () => {
    const wrapper = await page('/vlans?filter=blacklist');
    expect(cardNames(wrapper)).toEqual(['Blacklist']);
  });

  it('has no Blacklist view for a role that cannot see the blacklist', async () => {
    const wrapper = await page('/vlans', (body) => {
      delete body.blacklist;
      return body;
    });
    expect(buttons(wrapper.find('.vlans-views'))).toEqual([
      'By experiment',
      'ID space',
    ]);
  });

  it('adds a history entry for each chip and view, but not for each search key', async () => {
    const wrapper = await page();
    const push = vi.spyOn(wrapper.router, 'push');
    const replace = vi.spyOn(wrapper.router, 'replace');
    await click(wrapper, 'Running 2');
    await click(wrapper.find('.vlans-views'), 'ID space');
    expect(push).toHaveBeenCalledTimes(2);
    await wrapper.find('.vlans-search input').setValue('30');
    await flushPromises();
    expect(push).toHaveBeenCalledTimes(2);
    expect(replace).toHaveBeenCalledTimes(1);
  });

  it('says when no card matches', async () => {
    const wrapper = await page('/vlans?q=nothing');
    expect(textOf(wrapper.find('.vlans-empty'))).toBe(
      'No cards match the filter or the search.',
    );
  });
});

describe('the ID space view', () => {
  const cursor = (wrapper) => wrapper.find('[data-testid="vlan-grid-cursor"]');
  const at = (wrapper) => cursor(wrapper).attributes('aria-label');

  it('has a name, a summary and a legend', async () => {
    const wrapper = await page('/vlans?view=grid');
    const grid = wrapper.find('.vlan-grid');
    expect(grid.attributes('role')).toBe('group');
    expect(grid.attributes('aria-label')).toBe(
      'VLAN ID space: IDs 0 to 4095 in 64 rows of 64',
    );
    const summary = document.getElementById(
      grid.attributes('aria-describedby'),
    );
    expect(textOf(summary)).toBe(
      '4076 free, 4 allocated and pinned, 2 allocated and not pinned, ' +
        '5 pinned only, 1 blacklisted, 6 with a conflict or unowned.',
    );
    const legend = textOf(wrapper.find('.vlan-grid-legend'));
    expect(legend).toContain('Allocated, not pinned (2)');
    expect(legend).toContain('Allocated and pinned (4)');
    expect(legend).toContain('Pinned only (5)');
    expect(legend).toContain('Conflict or unowned');
  });

  it('moves between IDs with the arrow, page and home keys, within 1 to 4094', async () => {
    const wrapper = await page('/vlans?view=grid');
    expect(at(wrapper)).toBe('ID 1: Free.');
    const press = async (key, opts = {}) => {
      await cursor(wrapper).trigger('keydown', { key, ...opts });
      return at(wrapper).split(':')[0];
    };
    expect(await press('ArrowLeft')).toBe('ID 1');
    expect(await press('ArrowUp')).toBe('ID 1');
    expect(await press('ArrowRight')).toBe('ID 2');
    expect(await press('ArrowDown')).toBe('ID 66');
    expect(await press('PageDown')).toBe('ID 578');
    expect(await press('End')).toBe('ID 639');
    expect(await press('Home')).toBe('ID 576');
    expect(await press('PageUp')).toBe('ID 64');
    expect(await press('End', { ctrlKey: true })).toBe('ID 4094');
    expect(await press('ArrowRight')).toBe('ID 4094');
    expect(await press('Home', { ctrlKey: true })).toBe('ID 1');
  });

  it('leaves the Alt and Meta keys to the browser', async () => {
    const wrapper = await page('/vlans?view=grid&vlan=102');
    for (const key of ['altKey', 'metaKey']) {
      const event = new KeyboardEvent('keydown', {
        key: 'ArrowLeft',
        [key]: true,
        cancelable: true,
      });
      cursor(wrapper).element.dispatchEvent(event);
      await nextTick();
      expect(event.defaultPrevented).toBe(false);
      expect(at(wrapper)).toMatch(/^ID 102:/);
    }
  });

  it('opens the details of the ID the cursor is on', async () => {
    const wrapper = await page('/vlans?view=grid&vlan=102');
    expect(at(wrapper)).toMatch(/^ID 102: Conflict or unowned\./);
    await cursor(wrapper).trigger('keydown', { key: 'ArrowRight' });
    await cursor(wrapper).trigger('click');
    await flushPromises();
    expect(query(wrapper)).toEqual({ view: 'grid', vlan: '103' });
    expect(textOf(wrapper.find('.vlan-details-title'))).toBe('VLAN 103');
  });

  it('outlines the selected card in the legend', async () => {
    const wrapper = await page('/vlans?experiment=substation-a&view=grid');
    expect(textOf(wrapper.find('.vlan-grid-legend'))).toContain(
      'Range of substation-a',
    );
    expect(textOf(wrapper.find('.vlan-grid-legend'))).toContain(
      'IDs of substation-a',
    );
  });
});

describe('a range of IDs in the ID space view', () => {
  const canvas = (wrapper) => wrapper.find('.vlan-grid-canvas');
  const cursor = (wrapper) => wrapper.find('[data-testid="vlan-grid-cursor"]');
  // the middle of the cell of an ID; the grid has no layout here, so its
  // canvas starts at 0, 0
  const at = (id) => ({
    clientX: (id % 64) * 9 + 4,
    clientY: Math.floor(id / 64) * 9 + 4,
    button: 0,
    pointerId: 1,
  });

  async function drag(wrapper, from, to) {
    await canvas(wrapper).trigger('pointerdown', at(from));
    await canvas(wrapper).trigger('pointermove', at(to));
    await canvas(wrapper).trigger('pointerup', at(to));
    await flushPromises();
  }

  const panel = (wrapper) => wrapper.find('.vlan-range-details');
  const entries = (wrapper) =>
    panel(wrapper)
      .findAll('.vlan-range-id')
      .map((li) => [
        textOf(li.find('.vlan-id')),
        textOf(li.find('.tag')),
        li.findAll('.vlan-range-lines li').map((l) => textOf(l)),
      ]);

  it('shows the details of each ID in use that a drag crosses, in either direction', async () => {
    const wrapper = await page('/vlans?view=grid');
    await canvas(wrapper).trigger('pointerdown', at(104));
    await canvas(wrapper).trigger('pointermove', at(100));
    expect(textOf(wrapper.find('.vlan-grid-status'))).toBe(
      'IDs 100 to 104: release to show their details.',
    );
    await canvas(wrapper).trigger('pointerup', at(100));
    await flushPromises();

    expect(query(wrapper)).toEqual({ view: 'grid', vlans: '100-104' });
    expect(textOf(panel(wrapper).find('.vlan-details-title'))).toBe(
      'VLANs 100–104',
    );
    expect(textOf(panel(wrapper).find('.vlan-range-summary'))).toBe(
      '5 IDs: 1 pinned only; 2 allocated and pinned; 2 conflict or unowned.',
    );
    expect(entries(wrapper)).toEqual([
      [
        'VLAN 100',
        'Conflict or unowned',
        ['phēnix: lab pins MGMT', 'Blacklist: minimega gives it to no alias'],
      ],
      [
        'VLAN 101',
        'Allocated and pinned',
        ['phēnix: substation-a pins MGMT', 'minimega: substation-a: MGMT'],
      ],
      [
        'VLAN 102',
        'Conflict or unowned',
        [
          'phēnix: scratch pins EXP',
          'phēnix: substation-a pins SCADA',
          'minimega: substation-a: SCADA',
        ],
      ],
      [
        'VLAN 103',
        'Allocated and pinned',
        ['phēnix: substation-a pins FIELD', 'minimega: substation-a: FIELD'],
      ],
      ['VLAN 104', 'Pinned only', ['phēnix: substation-a pins CORP']],
    ]);
    expect(textOf(panel(wrapper))).toContain('substation-a 100–199');
    expect(textOf(panel(wrapper))).toContain(
      'substation-a SCADA: Also pinned by scratch (EXP).',
    );
    // the range is outlined, with its name in the legend
    expect(textOf(wrapper.find('.vlan-grid-legend'))).toContain('Selected IDs');
    // the panel reads nothing from the server
    expect(axios.get).not.toHaveBeenCalled();
  });

  it('opens the details of one ID for a press and release on it', async () => {
    const wrapper = await page('/vlans?view=grid');
    await drag(wrapper, 102, 102);
    expect(query(wrapper)).toEqual({ view: 'grid', vlan: '102' });
    expect(textOf(wrapper.find('.vlan-details-title'))).toBe('VLAN 102');
  });

  it('selects from the last ID opened to the one clicked with Shift', async () => {
    const wrapper = await page('/vlans?view=grid');
    await drag(wrapper, 300, 300);
    await canvas(wrapper).trigger('pointerdown', at(330));
    await canvas(wrapper).trigger('pointerup', { ...at(330), shiftKey: true });
    await flushPromises();
    expect(query(wrapper)).toEqual({ view: 'grid', vlans: '300-330' });
    expect(entries(wrapper).map(([id, tag]) => [id, tag])).toEqual([
      ['VLAN 301', 'Allocated and pinned'],
      ['VLAN 302', 'Allocated and pinned'],
      ['VLAN 330', 'Allocated, not pinned'],
    ]);
    // each allocated state has its own tag color
    const tags = panel(wrapper).findAll('.vlan-range-id .tag');
    expect(tags.map((tag) => tag.classes().filter((c) => c !== 'tag'))).toEqual(
      [['is-success'], ['is-success'], ['is-info']],
    );
    expect(textOf(panel(wrapper).find('.vlan-range-summary'))).toBe(
      '31 IDs: 28 free, in a range; 1 allocated, not pinned; ' +
        '2 allocated and pinned.',
    );
  });

  it('selects from the last ID opened to the cursor with Shift+Enter', async () => {
    const wrapper = await page('/vlans?view=grid');
    await cursor(wrapper).trigger('keydown', { key: 'End', ctrlKey: true });
    await cursor(wrapper).trigger('click');
    await flushPromises();
    expect(query(wrapper)).toEqual({ view: 'grid', vlan: '4094' });
    for (let i = 0; i < 3; i++) {
      await cursor(wrapper).trigger('keydown', { key: 'ArrowLeft' });
    }
    const event = new KeyboardEvent('keydown', {
      key: 'Enter',
      shiftKey: true,
      cancelable: true,
    });
    cursor(wrapper).element.dispatchEvent(event);
    await flushPromises();
    expect(event.defaultPrevented).toBe(true);
    expect(query(wrapper)).toEqual({ view: 'grid', vlans: '4091-4094' });
    expect(textOf(panel(wrapper))).toContain(
      'No experiment or minimega namespace uses these IDs.',
    );
  });

  it('leaves out the IDs that are not valid, and the other buttons', async () => {
    const wrapper = await page('/vlans?view=grid');
    await drag(wrapper, 0, 0);
    await canvas(wrapper).trigger('pointerdown', { ...at(5), button: 2 });
    await canvas(wrapper).trigger('pointerup', at(5));
    await flushPromises();
    expect(query(wrapper)).toEqual({ view: 'grid' });
  });

  it("opens an ID's own details from the range, and gives focus back as it closes", async () => {
    const wrapper = await page('/vlans?view=grid');
    cursor(wrapper).element.focus();
    await drag(wrapper, 100, 110);
    expect(document.activeElement).toBe(
      panel(wrapper).find('.vlan-details-title').element,
    );
    await panel(wrapper).find('[aria-label="Show VLAN 110"]').trigger('click');
    await flushPromises();
    expect(query(wrapper)).toEqual({ view: 'grid', vlan: '110' });
    expect(panel(wrapper).exists()).toBe(false);
    await wrapper.find('.vlan-details-close').trigger('click');
    await flushPromises();
    expect(query(wrapper)).toEqual({ view: 'grid' });
    expect(document.activeElement).toBe(cursor(wrapper).element);
  });

  it('opens on the range a link names', async () => {
    const wrapper = await page('/vlans?view=grid&vlans=398-403');
    expect(textOf(panel(wrapper).find('.vlan-range-summary'))).toBe(
      '6 IDs: 2 free, in a range; 4 conflict or unowned.',
    );
    expect(entries(wrapper).map(([id, state]) => `${id} ${state}`)).toEqual([
      'VLAN 400 Conflict or unowned',
      'VLAN 401 Conflict or unowned',
      'VLAN 402 Conflict or unowned',
      'VLAN 403 Conflict or unowned',
    ]);
  });
});

describe('the details panel', () => {
  it("shows an ID's owners, sources, range, VMs and issues", async () => {
    axios.get.mockResolvedValue({
      data: {
        id: 102,
        vlans: [],
        vms: [
          {
            experiment: 'substation-a',
            vm: 'hmi',
            host: 'compute1',
            interface: 0,
          },
        ],
        issues: [],
      },
    });
    const wrapper = await page('/vlans?vlan=102');
    expect(axios.get).toHaveBeenCalledWith('vlans/102', expect.anything());
    const panel = wrapper.find('.vlan-details');
    const text = textOf(panel);
    expect(text).toContain('substation-a pins SCADA = 102');
    expect(text).toContain('scratch pins EXP = 102');
    expect(text).toContain('substation-a: SCADA = 102');
    expect(text).toContain('substation-a 100–199');
    expect(text).toContain('hmi (substation-a, 0, compute1)');
    // each issue names its own experiment and alias
    expect(text).toContain('substation-a SCADA: Also pinned by scratch (EXP).');
    expect(text).toContain('scratch EXP: Also pinned by substation-a (SCADA).');
    expect(buttons(panel.find('.vlan-details-actions'))).toEqual([
      'Change scratch EXP…',
      'Change substation-a SCADA… (disabled)',
      'Add to the blacklist…',
    ]);
    const links = panel
      .findAll('.vlan-details-links a')
      .map((a) => [textOf(a), a.attributes('href')]);
    expect(links).toEqual([
      ['Show VMs', '/experiment/scratch?q=EXP'],
      ['SoH group', '/soh/scratch'],
      ['Show VMs', '/experiment/substation-a?q=%22scada+(102)%22'],
      ['SoH group', '/soh/substation-a'],
      ['Netflow', '/experiment/substation-a?tab=netflow&q=102'],
    ]);
  });

  it('reads the details again when the page data changes, and keeps the old ones until then', async () => {
    const vm = (name) => ({ experiment: 'scratch', vm: name, interface: 0 });
    let answer;
    axios.get
      .mockResolvedValueOnce({
        data: { id: 102, vms: [vm('old')], issues: [] },
      })
      .mockReturnValueOnce(new Promise((resolve) => (answer = resolve)));
    const wrapper = await page('/vlans?vlan=102');
    const panel = () => textOf(wrapper.find('.vlan-details'));
    expect(panel()).toContain('old (scratch, 0)');
    wrapper.vm.data = normalizeVlans(vlansFixture());
    await flushPromises();
    expect(axios.get).toHaveBeenCalledTimes(2);
    expect(panel()).toContain('old (scratch, 0)');
    answer({ data: { id: 102, vms: [vm('new')], issues: [] } });
    await flushPromises();
    expect(panel()).toContain('new (scratch, 0)');
    expect(panel()).not.toContain('old (scratch, 0)');
  });

  it('takes focus as it opens, and gives it back to the opener as it closes', async () => {
    const wrapper = await page();
    const opener = expCard(wrapper, 'scratch').find(
      '[aria-label="Show VLAN 102"]',
    );
    opener.element.focus();
    await opener.trigger('click');
    await flushPromises();
    expect(document.activeElement).toBe(
      wrapper.find('.vlan-details-title').element,
    );
    await wrapper.find('.vlan-details-close').trigger('click');
    await flushPromises();
    expect(document.activeElement).toBe(opener.element);
  });

  it('links to the owner card and closes', async () => {
    const wrapper = await page('/vlans?view=grid&vlan=150');
    await click(wrapper.find('.vlan-details'), 'Show the card');
    expect(query(wrapper)).toEqual({ vlan: '150' });
    expect(expCard(wrapper, 'ics-training').classes()).toContain('is-selected');
    await wrapper.find('.vlan-details-close').trigger('click');
    await flushPromises();
    expect(query(wrapper)).toEqual({});
    expect(wrapper.find('.vlan-details').exists()).toBe(false);
  });

  it('starts captures on the VLAN after a confirm', async () => {
    axios.post.mockResolvedValue({ data: { captures: [{}, {}] } });
    const wrapper = await page('/vlans?vlan=301');
    await click(wrapper.find('.vlan-details'), 'Capture…');
    expect(dialogText()).toContain('RT_C2');
    await confirmDialog();
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/red-team-2/captureVlan',
      { vlan: 'RT_C2' },
    );
    expect(toasts().at(-1).text).toBe(
      'Started 2 packet captures on the RT_C2 VLAN.',
    );
  });

  it('hides Capture from a role that may not capture', async () => {
    rbac.allowed = denyOnly(['vms/captures', 'create']);
    const wrapper = await page('/vlans?vlan=301');
    expect(buttons(wrapper.find('.vlan-details'))).not.toContain('Capture…');
  });
});

describe('the dialogs', () => {
  const modal = (wrapper) => wrapper.find('.modal.is-active');

  it('changes a VLAN ID after checking it in the cluster', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    const wrapper = await page();
    await click(expCard(wrapper, 'scratch'), 'Change EXP to 1…');
    const dialog = modal(wrapper);
    expect(textOf(dialog.find('.modal-card-title'))).toBe(
      'Change the VLAN ID of EXP',
    );
    await dialog.find('input').setValue('105');
    await vi.advanceTimersByTimeAsync(CHECK_DELAY_MS);
    await flushPromises();
    expect(axios.post).toHaveBeenLastCalledWith('vlans/check', {
      experiment: 'scratch',
      aliases: { EXP: 105 },
      min: 0,
      max: 0,
    });
    expect(textOf(dialog.find('.vlan-dialog-check'))).toBe(
      '105 is free in the cluster.',
    );
    expect(textOf(dialog.find('.vlan-dialog-help'))).toBe('Was 102.');

    await dialog.find('form').trigger('submit');
    await flushPromises();
    expect(axios.put).toHaveBeenCalledWith(
      'experiments/scratch/vlans/aliases/EXP',
      { id: 105 },
      undefined,
    );
    expect(loaderSpy.load).toHaveBeenCalledWith({ force: true });
  });

  it('shows the issues a new ID would have', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    axios.post.mockResolvedValue({
      data: {
        issues: [
          { kind: 'pin-blacklisted', message: 'minimega blacklists 999.' },
        ],
      },
    });
    const wrapper = await page();
    await click(expCard(wrapper, 'scratch'), 'Edit IDs…');
    const dialog = modal(wrapper);
    expect(textOf(dialog.find('.modal-card-title'))).toBe(
      'Edit the VLAN IDs of scratch',
    );
    await dialog.find('input').setValue('999');
    await vi.advanceTimersByTimeAsync(CHECK_DELAY_MS);
    await flushPromises();
    expect(textOf(dialog.find('.vlan-dialog-check'))).toBe(
      'minimega blacklists 999.',
    );
  });

  it('clears a pin with 0', async () => {
    const wrapper = await page();
    await click(expCard(wrapper, 'scratch'), 'Edit IDs…');
    await modal(wrapper).find('input').setValue('0');
    await modal(wrapper).find('form').trigger('submit');
    await flushPromises();
    expect(axios.delete).toHaveBeenCalledWith(
      'experiments/scratch/vlans/aliases/EXP',
    );
  });

  const conflict = httpError(409, {
    issues: [
      {
        kind: 'duplicate-pin',
        experiments: ['scratch', 'substation-a'],
        message: 'Also pinned by substation-a (SCADA).',
      },
    ],
  });

  it('saves over a conflict with force only after a confirm', async () => {
    axios.put.mockRejectedValueOnce(conflict).mockResolvedValue({ data: {} });
    const wrapper = await page();
    await click(expCard(wrapper, 'scratch'), 'Edit IDs…');
    await modal(wrapper).find('input').setValue('101');
    await modal(wrapper).find('form').trigger('submit');
    await flushPromises();
    expect(dialogText()).toContain('Also pinned by substation-a (SCADA).');
    expect(
      textOf(dialogs().at(-1).querySelector('footer .button:last-child')),
    ).toBe('Save anyway');
    await confirmDialog();
    expect(axios.put.mock.calls.map((c) => c[2])).toEqual([
      undefined,
      { params: { force: true } },
    ]);
  });

  it('stays open when Escape cancels the conflict question', async () => {
    axios.put.mockRejectedValue(conflict);
    const wrapper = await page();
    await click(expCard(wrapper, 'scratch'), 'Edit IDs…');
    await modal(wrapper).find('input').setValue('101');
    await modal(wrapper).find('form').trigger('submit');
    await flushPromises();
    expect(dialogText()).toContain('Save anyway');
    document.dispatchEvent(new KeyboardEvent('keyup', { key: 'Escape' }));
    await flushPromises();
    expect(dialogs()).toEqual([]);
    expect(modal(wrapper).exists()).toBe(true);
    expect(axios.put).toHaveBeenCalledTimes(1);
  });

  it('stays open with the IDs that did not save', async () => {
    axios.put
      .mockResolvedValueOnce({ data: {} })
      .mockRejectedValueOnce(httpError(500));
    const wrapper = await page();
    await click(expCard(wrapper, 'ics-training'), 'Edit IDs…');
    const [dmz, eng] = modal(wrapper).findAll('input');
    await dmz.setValue('160');
    await eng.setValue('161');
    await modal(wrapper).find('form').trigger('submit');
    await flushPromises();
    expect(axios.put).toHaveBeenCalledTimes(2);
    expect(notify.error).toHaveBeenCalledTimes(1);
    expect(toasts().at(-1).text).toBe(
      'Saved the VLAN IDs of 1 alias of ics-training',
    );
    expect(modal(wrapper).exists()).toBe(true);
    const help = modal(wrapper)
      .findAll('.vlan-dialog-help')
      .map((h) => textOf(h));
    expect(help.slice(0, 2)).toEqual(['', 'Was 150.']);
    expect(loaderSpy.load).not.toHaveBeenCalled();
  });

  it('shows names in the toasts as text, not HTML', async () => {
    const name = '<img src=x onerror=alert(1)>';
    const wrapper = await page('/vlans', (body) => {
      for (const row of body.vlans) {
        if (row.namespace === 'old-demo') row.namespace = name;
      }
      for (const issue of body.issues) {
        if (issue.namespace === 'old-demo') issue.namespace = name;
      }
      return body;
    });
    await click(card(wrapper, `namespace:${name}`), 'Release 4 IDs…');
    await confirmDialog();
    expect(toasts().at(-1).text).toBe(`Released the VLAN IDs of ${name}`);
    expect(document.querySelector('.toast img')).toBe(null);
  });

  it('does not offer force to a role that cannot see the other experiment', async () => {
    rbac.allowed = (resource, verb, name) =>
      !(resource === 'vlans' && verb === 'list' && name === 'substation-a');
    axios.put.mockRejectedValue(conflict);
    const wrapper = await page();
    await click(expCard(wrapper, 'scratch'), 'Edit IDs…');
    await modal(wrapper).find('input').setValue('101');
    await modal(wrapper).find('form').trigger('submit');
    await flushPromises();
    expect(dialogs()).toEqual([]);
    expect(axios.put).toHaveBeenCalledTimes(1);
    expect(notify.error).toHaveBeenCalledWith(conflict);
  });

  it('suggests a free range for the alias count and saves it', async () => {
    axios.get.mockResolvedValue({ data: { start: 400, end: 403 } });
    const wrapper = await page();
    await click(expCard(wrapper, 'ics-training'), 'Suggest a range…');
    expect(axios.get).toHaveBeenCalledWith('vlans/free', {
      params: { count: 4, outside_ranges: true },
    });
    const dialog = modal(wrapper);
    expect(textOf(dialog.find('.modal-card-title'))).toBe('Set the VLAN range');
    expect(dialog.findAll('input').map((i) => i.element.value)).toEqual([
      '400',
      '403',
    ]);
    expect(textOf(dialog.find('.vlan-dialog-check'))).toContain(
      'Suggested a free range: 400–403.',
    );
    await dialog.find('form').trigger('submit');
    await flushPromises();
    expect(axios.put).toHaveBeenCalledWith(
      'experiments/ics-training/vlans/range',
      { min: 400, max: 403 },
      undefined,
    );
  });

  it('refuses a minimum above the maximum, and clears a range', async () => {
    const wrapper = await page();
    await click(expCard(wrapper, 'scratch'), 'Set range…');
    const dialog = modal(wrapper);
    const [min, max] = dialog.findAll('input');
    await min.setValue('300');
    await max.setValue('200');
    expect(textOf(dialog)).toContain(
      'The minimum must not be more than the maximum.',
    );
    expect(buttons(dialog)).toContain('Save (submits) (disabled)');
    wrapper.unmount();

    const ics = await page();
    await click(expCard(ics, 'ics-training'), 'Edit IDs…');
    await click(expCard(ics, 'ics-training'), 'Suggest a range…');
    await click(modal(ics), 'Clear the range');
    expect(axios.delete).toHaveBeenCalledWith(
      'experiments/ics-training/vlans/range',
    );
  });

  // a request that stays open until finish() resolves it
  const held = () => {
    let finish;
    const promise = new Promise((resolve) => {
      finish = () => resolve({ data: {} });
    });
    return { promise, finish: () => finish() };
  };
  const busyText = (wrapper) =>
    textOf(card(wrapper, 'blacklist').find('[role="status"]'));

  it('spins an ID until the blacklist removes it', async () => {
    const request = held();
    axios.delete.mockReturnValue(request.promise);
    const wrapper = await page('/vlans?view=blacklist');
    await click(card(wrapper, 'blacklist'), 'Remove 100 from the blacklist');
    await confirmDialog();
    expect(buttons(card(wrapper, 'blacklist'))).toContain(
      'Remove 100 from the blacklist (spinning)',
    );
    expect(busyText(wrapper)).toBe('Removing 100 from the blacklist…');
    request.finish();
    await flushPromises();
    expect(buttons(card(wrapper, 'blacklist'))).not.toContain(
      'Remove 100 from the blacklist (spinning)',
    );
    expect(busyText(wrapper)).toBe('');
    expect(toasts().at(-1).text).toBe('Removed 100 from the blacklist');
  });

  it('spins the Add button until the blacklist adds the ID', async () => {
    const request = held();
    axios.put.mockReturnValue(request.promise);
    const wrapper = await page('/vlans?view=blacklist');
    await click(card(wrapper, 'blacklist'), 'Add an ID…');
    await confirmDialog('42');
    expect(axios.put).toHaveBeenCalledWith('vlans/blacklist/42');
    expect(
      buttons(card(wrapper, 'blacklist').find('.vlan-card-actions')),
    ).toEqual(['Add an ID… (spinning)']);
    expect(busyText(wrapper)).toBe('Adding 42 to the blacklist…');
    request.finish();
    await flushPromises();
    expect(
      buttons(card(wrapper, 'blacklist').find('.vlan-card-actions')),
    ).toEqual(['Add an ID…']);
    expect(busyText(wrapper)).toBe('');
  });

  it('pins the live IDs of a running experiment after a confirm', async () => {
    axios.post.mockResolvedValue({ data: {} });
    const wrapper = await page();
    await click(expCard(wrapper, 'substation-a'), 'Pin live IDs…');
    expect(dialogText()).toContain('CORP: 104 → 110');
    await confirmDialog();
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/substation-a/vlans/pin-live',
    );
    expect(toasts().at(-1).text).toBe(
      'Pinned the live VLAN IDs of substation-a',
    );
  });

  it('releases the IDs of an unowned namespace after a danger confirm', async () => {
    const wrapper = await page();
    await click(card(wrapper, 'namespace:old-demo'), 'Release 4 IDs…');
    const dialog = dialogs().at(-1);
    expect(textOf(dialog)).toContain(
      'This clears 4 aliases of the minimega namespace old-demo: 400, 401, 402, and 403.',
    );
    expect(
      dialog.querySelector('footer .button:last-child').classList,
    ).toContain('is-danger');
    await confirmDialog();
    expect(axios.delete).toHaveBeenCalledWith('vlans/allocations/old-demo');
  });

  it('adds a typed ID to the blacklist', async () => {
    const wrapper = await page('/vlans?view=blacklist');
    await click(card(wrapper, 'blacklist'), 'Add an ID…');
    await confirmDialog('77');
    expect(axios.put).toHaveBeenCalledWith('vlans/blacklist/77');
  });

  it('refuses a typed ID out of range', async () => {
    const wrapper = await page('/vlans?view=blacklist');
    await click(card(wrapper, 'blacklist'), 'Add an ID…');
    await confirmDialog('5000');
    expect(axios.put).not.toHaveBeenCalled();
    // the prompt stays open on the number it refuses
    expect(dialogs()).toHaveLength(1);
  });

  it('adds the ID of the details panel to the blacklist', async () => {
    const wrapper = await page('/vlans?vlan=150');
    await click(wrapper.find('.vlan-details'), 'Add to the blacklist…');
    await confirmDialog();
    expect(axios.put).toHaveBeenCalledWith('vlans/blacklist/150');
  });

  it('removes an ID from the blacklist after a confirm', async () => {
    const wrapper = await page('/vlans?view=blacklist');
    await card(wrapper, 'blacklist')
      .find('[aria-label="Remove 999 from the blacklist"]')
      .trigger('click');
    await flushPromises();
    await confirmDialog();
    expect(axios.delete).toHaveBeenCalledWith('vlans/blacklist/999');
  });

  it('greys out blacklist removal when minimega cannot remove IDs', async () => {
    const wrapper = await page('/vlans?view=blacklist', (body) => {
      body.minimega.blacklistRemoval = false;
      return body;
    });
    const tips = tooltipButtons(card(wrapper, 'blacklist'));
    expect(tips.map((t) => [t.label, t.disabled, t.tooltip])).toEqual([
      [
        'Remove 100 from the blacklist',
        true,
        'minimega cannot remove IDs from its blacklist',
      ],
      [
        'Remove 999 from the blacklist',
        true,
        'minimega cannot remove IDs from its blacklist',
      ],
    ]);
    await tips[0].control.trigger('click');
    await flushPromises();
    expect(dialogs()).toEqual([]);
  });

  it('lists a blacklisted number that is not a VLAN ID, with no actions', async () => {
    const wrapper = await page('/vlans?view=blacklist', (body) => {
      body.blacklist = [0, ...body.blacklist];
      return body;
    });
    const blacklist = card(wrapper, 'blacklist');
    expect(textOf(blacklist.find('.vlan-blacklist-invalid'))).toBe(
      '0 (not a VLAN ID)',
    );
    expect(blacklist.find('[aria-label="Show VLAN 0"]').exists()).toBe(false);
    expect(
      blacklist.find('[aria-label="Remove 0 from the blacklist"]').exists(),
    ).toBe(false);
    // the header counts what the card lists
    expect(textOf(wrapper.find('.vlans-head'))).toMatch(/Blacklisted\s*3/i);
  });

  it('reports a failed change', async () => {
    const failure = httpError(501, { message: 'not supported' });
    axios.delete.mockRejectedValue(failure);
    const wrapper = await page('/vlans?view=blacklist');
    await card(wrapper, 'blacklist')
      .find('[aria-label="Remove 999 from the blacklist"]')
      .trigger('click');
    await flushPromises();
    await confirmDialog();
    expect(notify.error).toHaveBeenCalledWith(failure);
  });
});

describe('the header', () => {
  it('finds a free block and outlines it in the ID space view', async () => {
    axios.get.mockResolvedValue({ data: { start: 111, end: 126 } });
    const wrapper = await page();
    await click(wrapper.find('.vlans-tools'), 'Find free IDs');
    await wrapper.find('.vlan-finder form').trigger('submit');
    await flushPromises();
    expect(axios.get).toHaveBeenCalledWith('vlans/free', {
      params: { count: 16 },
    });
    expect(query(wrapper)).toEqual({ view: 'grid' });
    expect(textOf(wrapper.find('.vlan-finder-result'))).toContain(
      'Free block found: IDs 111–126. The block is inside the range of substation-a.',
    );
    expect(textOf(wrapper.find('.vlan-grid-legend'))).toContain(
      'Free block found',
    );

    await click(
      wrapper.find('.vlan-finder-result'),
      'Set as the range of ics-training…',
    );
    const inputs = wrapper.find('.modal.is-active').findAll('input');
    expect(inputs.map((i) => i.element.value)).toEqual(['111', '126']);
  });

  it('says when no block is free', async () => {
    axios.get.mockRejectedValue(httpError(404, {}));
    const wrapper = await page();
    await click(wrapper.find('.vlans-tools'), 'Find free IDs');
    await wrapper.find('.vlan-finder form').trigger('submit');
    await flushPromises();
    expect(textOf(wrapper.find('.vlan-finder-result'))).toBe(
      'No block of 16 free IDs is free.',
    );
  });

  // exports in format from the page at route, and reads the file saved
  async function exported(route, format) {
    const wrapper = await page(route);
    const item = wrapper
      .findAll('.dropdown-item')
      .find((i) => textOf(i) === format.toUpperCase());
    await item.trigger('click');
    await flushPromises();
    const [blob, name] = saver.saveAs.mock.calls.at(-1);
    expect(name).toBe(`vlans.${format}`);
    return blob.text();
  }

  it('exports the rows of the cards it shows as CSV, with no request', async () => {
    const csv = await exported('/vlans?filter=unowned', 'csv');
    expect(axios.get).not.toHaveBeenCalledWith('vlans', expect.anything());
    expect(csv.trim().split('\n')).toEqual([
      'id,alias,experiment,namespace,running,states,phenix,minimega,vms,hosts,issues',
      '400,LAN,,old-demo,false,allocated,,400,0,,unowned',
      '401,WAN,,old-demo,false,allocated,,401,0,,unowned',
      '402,DMZ,,old-demo,false,allocated,,402,0,,unowned',
      '403,OOB,,old-demo,false,allocated,,403,0,,unowned',
    ]);
  });

  it('exports what the chip and the search show as JSON', async () => {
    const body = JSON.parse(
      await exported('/vlans?filter=running&q=CORP', 'json'),
    );
    expect(new Set(body.vlans.map((row) => row.experiment))).toEqual(
      new Set(['substation-a']),
    );
    expect(body.experiments.map((exp) => exp.name)).toEqual(['substation-a']);
    expect(body.issues.every((i) => i.experiments[0] === 'substation-a')).toBe(
      true,
    );
    expect(body.blacklist).toBeUndefined();
  });

  it('says when minimega could not be read, and shows phēnix data only', async () => {
    const wrapper = await page('/vlans', (body) => ({
      ...body,
      minimega: { ok: false, error: 'minimega is busy' },
    }));
    expect(textOf(wrapper.find('.vlans-notice'))).toBe(
      'Could not read minimega: minimega is busy. The page shows the phēnix experiment specs only, with no differences.',
    );
  });

  it('does not say minimega did not allocate an ID when it could not be read', async () => {
    const wrapper = await page('/vlans', (body) => ({
      ...body,
      minimega: { ok: false, error: 'minimega is busy' },
    }));
    const lab = expCard(wrapper, 'lab').find('.vlan-alias');
    expect(textOf(lab.findAll('.vlan-source').at(-1))).toBe(
      'minimega ID unknown',
    );
  });

  it('counts the IDs in each state, and filters to the issues from the count', async () => {
    const wrapper = await page();
    const stats = wrapper
      .findAll('.stat-strip .stat')
      .map((stat) => `${textOf(stat.find('dt'))} ${textOf(stat.find('dd'))}`);
    expect(stats).toEqual([
      'Allocated 11',
      'Pinned 11',
      'Blacklisted 2',
      'Issues 7',
      'Free 4,076',
    ]);
    await wrapper.find('.stat-issues button').trigger('click');
    await flushPromises();
    expect(query(wrapper)).toEqual({ filter: 'issues' });
  });
});

describe('live updates', () => {
  it.each([
    ['vlans', 'update', true],
    ['experiment', 'start', true],
    ['experiment', 'stop', true],
    ['experiment', 'delete', true],
    ['experiment', 'progress', false],
    ['disks', 'update', false],
  ])('a %s %s message reloads: %s', async (type, action, reloads) => {
    await page();
    for (const handler of ws.handlers) handler(wsMsg(type, 'x', action));
    if (reloads) expect(loaderSpy.load).toHaveBeenCalledWith({ force: true });
    else expect(loaderSpy.load).not.toHaveBeenCalled();
  });

  it('reads minimega again on the header refresh', async () => {
    await page();
    await loaderSpy.options.refresh();
    expect(loaderSpy.load).toHaveBeenCalledWith({ force: true });
    expect(loaderSpy.options.key).toBe('vlans');
  });

  it('stops following messages when it closes', async () => {
    const wrapper = await page();
    expect(ws.handlers).toHaveLength(1);
    wrapper.unmount();
    expect(ws.handlers).toHaveLength(0);
  });
});
