// Every page's search box looks the same, with the search icon on its left
// and the width Buefy gives it, and offers to clear the search only while
// there is one. Each page is rendered with the real Buefy components.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/utils/rbac.js', () => ({ roleAllowed: () => true }));
vi.mock('@/utils/axios.js', () => {
  // the pages' lists never arrive
  const never = () => new Promise(() => {});
  return { default: { get: never, post: never, patch: never, delete: never } };
});
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({
    token: 't',
    features: [],
    role: { name: 'Global Admin' },
    username: 'admin',
  }),
}));
vi.mock('@/utils/websocket', () => ({
  addWsHandler: () => {},
  removeWsHandler: () => {},
  onWsReconnect: () => () => {},
  sendWsMsg: () => {},
}));
vi.mock('@/utils/pageLoader.js', () => ({
  createPageLoader: () => ({ start: () => {}, stop: () => {}, load: () => {} }),
  loadingText: (what) => `Loading ${what}…`,
  stillLoading: (loaded) => !loaded,
  pageStatus: {},
}));
// its terminal needs a browser
vi.mock('@/components/MiniTerminal.vue', () => ({ default: {} }));

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
