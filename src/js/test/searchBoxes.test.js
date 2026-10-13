// Every page's search box looks the same, with the search icon on its left
// and the width Buefy gives it, and offers to clear the search only while
// there is one. Each page is rendered with the real Buefy components.
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
import { renderSSR, stubBrowser } from './helpers/render.js';

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

// the search box's control: its icons, the input and its placeholder
const searchBox = (html) =>
  html.match(
    /<div class="control[^"]*"[^>]*>(?:(?!<\/div>).)*?placeholder="(?:Find|Search)[^"]*"(?:(?!<\/div>).)*<\/div>/s,
  )?.[0];

const clearButton = (html) =>
  html.match(/<button[^>]*aria-label="Clear [^"]*"[^>]*>/)?.[0];

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
  stubBrowser();
  warn = vi.spyOn(console, 'warn');
});
afterEach(() => {
  vi.unstubAllGlobals();
  warn.mockRestore();
});

describe.each(pages)('%s', (_, page, route, searches) => {
  it('renders a search box with the search icon and no width of its own', async () => {
    const box = searchBox(await renderSSR(page, {}, { route }));
    expect(box).toContain('class="icon is-left"><i data-icon="search"');
    expect(box).not.toMatch(/style="[^"]*width/);
    // and the page renders without complaint
    expect(warn).not.toHaveBeenCalled();
  });

  it('offers to clear the search only while there is one', async () => {
    expect(clearButton(await renderSSR(page, {}, { route }))).toBeUndefined();
    for (const data of searches) {
      const html = await renderSSR(page, {}, { route, data });
      expect(clearButton(html), JSON.stringify(data)).toBeDefined();
    }
  });
});
