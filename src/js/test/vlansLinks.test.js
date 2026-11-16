// The ways into the VLANs page from other pages (the Resources menu, the VLAN
// IDs menu, the experiment summary and the State of Health groups), and the
// VLAN conflict check before the VLAN IDs menu or the config editor saves.
import { flushPromises } from '@vue/test-utils';
import * as d3 from 'd3';
import { createPinia, setActivePinia } from 'pinia';
import { nextTick } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, capturing, loaderSpy, modules, rbac, resetMocks } =
  await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
// a loader that records its options, so a test can apply the SoH data
vi.mock('@/utils/pageLoader.js', capturing);
vi.mock('file-saver', modules.fileSaver);

import AppHeader from '@/components/AppHeader.vue';
import ConfigsEditor from '@/components/configs/ConfigsEditor.vue';
import ExperimentSummary from '@/components/experiment/ExperimentSummary.vue';
import VlanAliasMenu from '@/components/experiment/VlanAliasMenu.vue';
import { specOfConfig } from '@/utils/vlanActions.js';
import StateOfHealth from '@/views/StateOfHealth.vue';
import { httpError } from './helpers/fixtures.js';
import {
  confirmDialog,
  dialogs,
  mountComponent,
  textOf,
  tooltipButtons,
} from './helpers/mount.js';
import { allowAll, denyOnly } from './helpers/permissions.js';

const noVlans = denyOnly(['vlans']);

beforeEach(() => {
  setActivePinia(createPinia());
  resetMocks({
    store: { auth: true, token: 't', features: [], role: { name: 'x' } },
  });
  axios.get.mockResolvedValue({ data: {} });
  axios.post.mockResolvedValue({ data: { issues: [] } });
  axios.put.mockResolvedValue({ data: {} });
  axios.patch.mockResolvedValue({ status: 204 });
});

describe('the Resources menu', () => {
  // the links of the Resources menu, opened, with each one's tooltip
  async function resources() {
    const header = await mountComponent(AppHeader);
    const menu = header
      .findAll('.nav-menu')
      .find((m) => textOf(m.find('.nav-menu-button')) === 'Resources');
    await menu.find('.nav-menu-button').trigger('click');
    return menu;
  }

  it.each([
    ['links VLANs after Hosts', allowAll, true],
    ['leaves VLANs out for a role that may not list them', noVlans, false],
  ])('%s', async (_, role, shown) => {
    rbac.allowed = role;
    const menu = await resources();
    const links = menu.findAll('.nav-menu-panel a').map((a) => textOf(a));
    expect(links).toEqual(
      shown
        ? ['Configs', 'Disks', 'Hosts', 'VLANs']
        : ['Configs', 'Disks', 'Hosts'],
    );
    if (!shown) return;
    const link = menu.findAll('.nav-menu-panel a').at(-1);
    expect(link.attributes('href')).toBe('/vlans');
    const tip = tooltipButtons(menu).find(
      (t) => t.control.element === link.element,
    );
    expect(tip.tooltip).toBe(
      'VLAN IDs in use, aliases and ranges for all experiments',
    );
  });
});

describe('the VLAN IDs menu', () => {
  const vlans = [
    { alias: 'EXP', vlan: 101 },
    { alias: 'MGMT', vlan: 0 },
  ];
  const menu = () =>
    mountComponent(VlanAliasMenu, {
      props: { experiment: 'exp1', vlans, min: 100, max: 199 },
      attachTo: document.body,
    });
  // changes EXP to id and saves
  async function save(wrapper, id = '102') {
    await wrapper.find('input').setValue(id);
    await wrapper.find('form').trigger('submit');
    await flushPromises();
  }

  it.each([
    [
      'links the VLANs page of the experiment',
      allowAll,
      '/vlans?experiment=exp1',
    ],
    [
      'leaves the link out for a role that may not list VLANs',
      noVlans,
      undefined,
    ],
    [
      'leaves the link out for a role that may list only other experiments',
      denyOnly(['vlans', 'list', 'exp1']),
      undefined,
    ],
  ])('%s', async (_, role, href) => {
    rbac.allowed = role;
    const wrapper = await menu();
    const link = wrapper.find('.vlan-menu-cluster');
    expect(link.exists() ? link.attributes('href') : undefined).toBe(href);
    if (href) expect(textOf(link)).toBe('See all VLANs in the cluster');
  });

  it('checks the IDs in the cluster, then saves them with PATCH', async () => {
    const wrapper = await menu();
    await save(wrapper);
    expect(axios.post).toHaveBeenCalledWith('vlans/check', {
      experiment: 'exp1',
      aliases: { EXP: 102 },
      min: 100,
      max: 199,
    });
    expect(dialogs()).toEqual([]);
    expect(axios.patch).toHaveBeenCalledWith('experiments/exp1', '{"EXP":102}');
  });

  const issues = [
    { kind: 'duplicate-pin', message: 'Also pinned by other (EXP).' },
  ];

  it.each([
    ['saves anyway when asked to', true],
    ['keeps the old IDs when the save is canceled', false],
  ])('shows the conflicts first and %s', async (_, sure) => {
    axios.post.mockResolvedValue({ data: { issues } });
    const wrapper = await menu();
    await save(wrapper);
    const dialog = dialogs().at(-1);
    expect(textOf(dialog)).toContain('Also pinned by other (EXP).');
    expect(textOf(dialog.querySelector('footer .button:last-child'))).toBe(
      'Save anyway',
    );
    if (sure) {
      await confirmDialog();
    } else {
      dialog.querySelector('footer .button').click();
      await flushPromises();
    }
    expect(axios.patch.mock.calls.length).toBe(sure ? 1 : 0);
  });

  it.each([
    [
      'the check fails',
      allowAll,
      () => axios.post.mockRejectedValue(httpError(500)),
    ],
    ['the role may not list VLANs', noVlans, () => {}],
  ])('saves at once when %s', async (_, role, setup) => {
    rbac.allowed = role;
    setup();
    const wrapper = await menu();
    await save(wrapper);
    expect(dialogs()).toEqual([]);
    expect(axios.patch).toHaveBeenCalledTimes(1);
  });
});

describe('the experiment summary', () => {
  it.each([
    [
      'links its VLAN tile to the VLANs page',
      allowAll,
      '/vlans?experiment=exp1',
    ],
    ['has no link for a role that may not list VLANs', noVlans, undefined],
    [
      'has no link for a role that may list only other experiments',
      denyOnly(['vlans', 'list', 'exp1']),
      undefined,
    ],
  ])('%s', async (_, role, href) => {
    rbac.allowed = role;
    const wrapper = await mountComponent(ExperimentSummary, {
      props: { name: 'exp1', experiment: { vlans: [{ alias: 'A', vlan: 1 }] } },
    });
    const link = wrapper.find('[data-testid="summary-vlans"]');
    expect(link.exists() ? link.attributes('href') : undefined).toBe(href);
    expect(textOf(wrapper.find('.exp-fact-vlans dd'))).toBe('1');
    if (href) {
      expect(link.attributes('aria-label')).toBe(
        'See the VLANs of exp1 in the cluster',
      );
    }
  });
});

describe('the State of Health VLAN groups', () => {
  const open = () =>
    mountComponent(StateOfHealth, {
      route: '/soh/demo',
      data: { loaded: true },
    });

  it('links a group heading to its VLAN ID on the VLANs page', async () => {
    axios.get.mockResolvedValue({
      data: {
        experiment: {
          name: 'demo',
          aliases: [
            { alias: 'DMZ', pinned: 0, live: 149 },
            { alias: 'CORP', pinned: 104, live: 0 },
            { alias: 'NEW', pinned: 0, live: 0 },
          ],
        },
      },
    });
    const page = await open();
    await page.vm.loadVlanIds();
    expect(axios.get).toHaveBeenCalledWith('experiments/demo/vlans', undefined);
    expect(page.vm.vlanGroupRoute('dmz')).toEqual({
      name: 'vlans',
      query: { vlan: '149' },
    });
    expect(page.vm.vlanGroupRoute('CORP').query.vlan).toBe('104');
    expect(page.vm.vlanGroupRoute('NEW')).toBe(null);
    // read once
    await page.vm.loadVlanIds();
    expect(axios.get).toHaveBeenCalledTimes(1);
  });

  it('draws the graph before the IDs come, then links the headings', async () => {
    let answer;
    axios.get.mockReturnValue(new Promise((resolve) => (answer = resolve)));
    const page = await open();
    const draw = vi
      .spyOn(page.vm, 'generateGraph')
      .mockImplementation(() => {});
    vi.spyOn(page.vm, 'fitGraphView').mockImplementation(() => {});
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    const groups = d3
      .select(svg)
      .selectAll('g')
      .data([{ label: 'DMZ' }, { label: '' }])
      .join('g');
    page.vm.drawGroupHeadings(groups);
    svg.querySelector('text').setAttribute('x', '5');
    page.vm.graph = { svg: d3.select(svg), groups };

    loaderSpy.options.apply({ started: true, nodes: [], edges: [] });
    await nextTick();
    expect(draw).toHaveBeenCalledTimes(1);
    expect(axios.get).toHaveBeenCalledWith('experiments/demo/vlans', undefined);
    expect(svg.querySelector('a')).toBe(null);

    answer({
      data: { experiment: { aliases: [{ alias: 'DMZ', live: 149 }] } },
    });
    await flushPromises();
    const link = svg.querySelector('a.soh-group-link');
    expect(link.getAttribute('href')).toBe('/vlans?vlan=149');
    expect(link.querySelector('text').textContent).toBe('DMZ (149)');
    expect(link.querySelector('text').getAttribute('x')).toBe('5');
    expect(svg.querySelectorAll('text')).toHaveLength(1);
    expect(draw).toHaveBeenCalledTimes(1);
  });

  it('leaves the headings as text for a role that may not get VLANs', async () => {
    rbac.allowed = noVlans;
    const page = await open();
    await page.vm.loadVlanIds();
    expect(axios.get).not.toHaveBeenCalled();
    expect(page.vm.vlanGroupRoute('dmz')).toBe(null);
  });
});

describe('the config editor', () => {
  const experiment = {
    apiVersion: 'phenix.sandia.gov/v1',
    kind: 'Experiment',
    metadata: { name: 'exp1' },
    spec: { vlans: { aliases: { EXP: 102, NEW: 0 }, min: 100, max: 199 } },
  };

  it('reads the VLAN spec of an Experiment config', () => {
    expect(specOfConfig(experiment)).toEqual({
      experiment: 'exp1',
      aliases: { EXP: 102 },
      min: 100,
      max: 199,
    });
    expect(specOfConfig({ metadata: { name: 'x' } })).toEqual({
      experiment: 'x',
      aliases: {},
      min: 0,
      max: 0,
    });
  });

  // the editor on the config, saved, with the conflicts the check finds
  async function saved(config, found) {
    axios.get.mockResolvedValue({ data: structuredClone(config) });
    axios.post.mockResolvedValue({ data: { issues: found } });
    const wrapper = await mountComponent(ConfigsEditor, {
      props: { mode: 'edit', editorConfig: structuredClone(config) },
      stubs: { AceEditor: true },
      attachTo: document.body,
    });
    await flushPromises();
    await wrapper.find('.editor-buttons .is-success').trigger('click');
    await flushPromises();
    return wrapper;
  }

  it('shows the conflicts of an Experiment before it asks to save', async () => {
    await saved(experiment, [
      { kind: 'duplicate-pin', message: 'Also pinned by other (EXP).' },
    ]);
    expect(axios.post).toHaveBeenCalledWith(
      'vlans/check',
      specOfConfig(experiment),
    );
    expect(textOf(dialogs().at(-1))).toContain('Also pinned by other (EXP).');
    await confirmDialog();
    // then the usual question
    expect(textOf(dialogs().at(-1))).toContain('This will overwrite');
    await confirmDialog();
    expect(axios.put).toHaveBeenCalledTimes(1);
  });

  it('asks only the usual question when there are no conflicts', async () => {
    await saved(experiment, []);
    expect(dialogs()).toHaveLength(1);
    expect(textOf(dialogs()[0])).toContain('This will overwrite');
  });

  it('checks and asks once when Save is clicked twice during the check', async () => {
    let answer;
    axios.get.mockResolvedValue({ data: structuredClone(experiment) });
    axios.post.mockReturnValue(new Promise((resolve) => (answer = resolve)));
    const wrapper = await mountComponent(ConfigsEditor, {
      props: { mode: 'edit', editorConfig: structuredClone(experiment) },
      stubs: { AceEditor: true },
      attachTo: document.body,
    });
    await flushPromises();
    const save = wrapper.find('.editor-buttons .is-success');
    await save.trigger('click');
    await save.trigger('click');
    answer({ data: { issues: [] } });
    await flushPromises();
    expect(axios.post).toHaveBeenCalledTimes(1);
    expect(dialogs()).toHaveLength(1);
  });

  it('checks no other kind of config', async () => {
    await saved({ ...experiment, kind: 'Topology' }, []);
    expect(axios.post).not.toHaveBeenCalled();
  });
});
