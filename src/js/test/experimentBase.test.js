// An experiment's page picks its running or stopped view from the link or
// cached data, asks only if neither says, and fixes a stale guess on load.
import { flushPromises } from '@vue/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, notify, resetMocks, store } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);

// Stand-ins for the views, each drawing its name and noting it in views.shown;
// with views.found set, it reports the experiment running or not.
const views = vi.hoisted(() => ({
  found: undefined,
  shown: [],
  standIn: (name) => ({
    // what the page's import() resolves to, as for the real views
    __esModule: true,
    default: {
      emits: ['running'],
      created() {
        views.shown.push(name);
        if (views.found !== undefined) this.$emit('running', views.found);
      },
      render: () => name,
    },
  }),
}));
vi.mock('@/views/experiment/RunningExperiment.vue', () =>
  views.standIn('running view'),
);
vi.mock('@/views/experiment/StoppedExperiment.vue', () =>
  views.standIn('stopped view'),
);
vi.mock('@/views/experiment/VMtilesView.vue', () => views.standIn('VM tiles'));

import { cachedPage, cachePage, clearPageCache } from '@/utils/pageCache.js';
import Base from '@/views/experiment/Base.vue';
import { mountPage } from './helpers/mount.js';

// the page on exp1, opened by a link with this history state, once the view
// it picked has loaded
function open(historyState = null) {
  window.history.replaceState(historyState, '');
  return mountPage(Base, { route: '/experiment/exp1' });
}

beforeEach(() => {
  clearPageCache();
  resetMocks({ store: { role: { name: 'Experiment Admin' } } });
  views.found = undefined;
  views.shown = [];
});

describe("an experiment's page", () => {
  it.each([
    [
      'the link that opened it says it runs',
      { running: true },
      {},
      'running view',
    ],
    [
      'the link says it is stopped, whatever other pages loaded',
      { running: false },
      { 'experiment/exp1': { running: true } },
      'stopped view',
    ],
    [
      'the running page loaded it running',
      null,
      { 'experiment/exp1': { running: true } },
      'running view',
    ],
    [
      'the running page loaded it stopped',
      null,
      { 'experiment/exp1': { running: false } },
      'stopped view',
    ],
    [
      'the stopped page loaded it',
      null,
      { 'experiment/exp1/stopped': { name: 'exp1' } },
      'stopped view',
    ],
    [
      'the experiment list shows it running',
      {},
      {
        experiments: [
          { name: 'exp2', running: false },
          { name: 'exp1', running: true },
        ],
      },
      'running view',
    ],
  ])(
    'opens its view at once when %s',
    async (_, historyState, cached, shown) => {
      for (const [key, data] of Object.entries(cached)) cachePage(key, data);

      expect((await open(historyState)).text()).toBe(shown);
      expect(axios.get).not.toHaveBeenCalled();
    },
  );

  it('shows a VM viewer the VM tiles of a running experiment', async () => {
    store.role = { name: 'VM Viewer' };
    expect((await open({ running: true })).text()).toBe('VM tiles');
    expect((await open({ running: false })).text()).toBe('stopped view');
  });

  it('asks the server when nothing says whether it runs, for the running view to reuse', async () => {
    const answer = Promise.withResolvers();
    axios.get.mockReturnValue(answer.promise);
    cachePage('experiments', [{ name: 'exp2', running: true }]);

    const page = await open();
    expect(page.text()).toBe('Loading experiment…');
    expect(axios.get).toHaveBeenCalledWith('experiments/exp1', {
      signal: expect.any(AbortSignal),
      // reports the response's freshness into the cache entry
      meta: expect.any(Object),
      params: { vms: false },
    });

    answer.resolve({ data: { name: 'exp1', running: true } });
    await flushPromises();
    expect(page.text()).toBe('running view');
    // kept as the running view's data, without the VMs it lists itself
    expect(cachedPage('experiment/exp1').data).toEqual({
      name: 'exp1',
      running: true,
      vms: [],
    });
    expect((await open()).text()).toBe('running view');
    expect(axios.get).toHaveBeenCalledTimes(1);
  });

  it('reports an experiment it could not load', async () => {
    const err = new Error('not found');
    axios.get.mockRejectedValue(err);

    const page = await open();
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(page.text()).toBe('Could not load the experiment');
  });

  it.each([
    [{ running: false }, true, 'stopped view', 'running view'],
    [{ running: true }, false, 'running view', 'stopped view'],
  ])(
    'switches views when the one shown finds the link wrong (link: %o)',
    async (historyState, running, guessed, shown) => {
      views.found = running;
      const page = await open(historyState);
      expect(views.shown).toEqual([guessed, shown]);
      expect(page.text()).toBe(shown);
    },
  );
});
