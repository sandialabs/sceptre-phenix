// Every page's search box, drawn with real Buefy: the same look (icon left,
// Buefy's width), and a clear button only while there is a search.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/pageLoader.js', modules.pageLoader);
// its terminal needs a browser
vi.mock('@/components/MiniTerminal.vue', modules.stub);

import ConfigsList from '@/components/configs/ConfigsList.vue';
import Disks from '@/views/Disks.vue';
import Experiments from '@/views/Experiments.vue';
import RunningExperiment from '@/views/experiment/RunningExperiment.vue';
import StoppedExperiment from '@/views/experiment/StoppedExperiment.vue';
import VMtilesView from '@/views/experiment/VMtilesView.vue';
import Logs from '@/views/Logs.vue';
import Scorch from '@/views/Scorch.vue';
import SohOverview from '@/views/SohOverview.vue';
import Users from '@/views/Users.vue';
import { never } from './helpers/async.js';
import { mountPage } from './helpers/mount.js';

// each page, where it is, and the searches it can clear
const pages = [
  [
    'ConfigsList',
    ConfigsList,
    '/configs/',
    [{ searchQuery: 'dmz' }, { filterKind: 'Topology' }],
  ],
  ['Disks', Disks, '/disks/', [{ filterString: 'dmz' }]],
  ['Experiments', Experiments, '/experiments', [{ searchName: 'dmz' }]],
  ['Logs', Logs, '/log', [{ searchInput: 'dmz' }]],
  ['Scorch', Scorch, '/scorch', [{ searchName: 'dmz' }]],
  ['SohOverview', SohOverview, '/soh', [{ searchName: 'dmz' }]],
  ['Users', Users, '/users', [{ searchText: 'dmz' }]],
  [
    'RunningExperiment',
    RunningExperiment,
    '/experiment/exp1',
    [{ search: { filter: 'dmz' } }, { fileCategory: 'Unknown' }],
  ],
  [
    'StoppedExperiment',
    StoppedExperiment,
    '/experiment/exp1',
    [{ searchName: 'dmz' }, { fileCategory: 'Unknown' }],
  ],
  ['VMtilesView', VMtilesView, '/vmtiles', [{ searchName: 'dmz' }]],
];

// the control holding the search input: its icons and the input
const searchBox = (wrapper) =>
  wrapper
    .findAll('.control')
    .find((control) =>
      [...control.element.children].some((el) =>
        /^(Find|Search)/.test(el.placeholder),
      ),
    );

const clearButton = (wrapper) => wrapper.find('button[aria-label^="Clear "]');

let warn;
beforeEach(() => {
  resetMocks({
    store: {
      token: 't',
      features: [],
      role: { name: 'Global Admin' },
      username: 'admin',
    },
  });
  // the pages' lists never arrive
  for (const request of Object.values(axios)) request.mockImplementation(never);
  warn = vi.spyOn(console, 'warn');
});
afterEach(() => {
  warn.mockRestore();
});

describe.each(pages)('%s', (_, page, route, searches) => {
  it('draws a search box with the search icon and no width of its own', async () => {
    const box = searchBox(await mountPage(page, { route }));
    expect(box.find('.icon.is-left [data-icon="search"]').exists()).toBe(true);
    for (const el of [box, ...box.findAll('*')]) {
      expect(el.attributes('style') ?? '').not.toMatch(/width/);
    }
    // and the page draws without complaint
    expect(warn).not.toHaveBeenCalled();
  });

  it('offers to clear the search only while there is one', async () => {
    expect(clearButton(await mountPage(page, { route })).exists()).toBe(false);
    for (const data of searches) {
      const wrapper = await mountPage(page, { route, data });
      expect(clearButton(wrapper).exists(), JSON.stringify(data)).toBe(true);
    }
  });
});
