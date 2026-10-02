// An experiment's page shows its running or its stopped view. It knows which
// from the link that opened it or from data other pages already loaded, and
// only asks the server when it has neither; the view it shows corrects a
// stale guess once its own data arrives.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const axios = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock('@/utils/axios.js', () => ({ default: axios }));
const notify = vi.hoisted(() => vi.fn());
vi.mock('@/utils/errorNotif.js', () => ({ useErrorNotification: notify }));
const store = vi.hoisted(() => ({ role: null }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => store }));

// Stand-ins for the views, each drawing its name. When views.found is set,
// the view shown finds the experiment running or not, as the real views do
// once their data arrives, and says so.
const views = vi.hoisted(() => ({
  found: undefined,
  standIn: (name) => ({
    // what the page's import() resolves to, as for the real views
    __esModule: true,
    default: {
      emits: ['running'],
      created() {
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
import { flush } from './helpers/async.js';
import { renderSSR, textOf } from './helpers/render.js';

// The page on exp1, opened by a link with this history state: what it
// shows, and its state, to see what it shows once its view changes.
async function open(historyState = null) {
  vi.stubGlobal('window', { history: { state: historyState } });
  let page;
  const html = await renderSSR(
    {
      ...Base,
      setup(props, ctx) {
        page = Base.setup(props, ctx);
        return page;
      },
    },
    {},
    { route: '/experiment/exp1' },
  );
  return { text: textOf(html), page };
}

// what the page shows now, as its state has it
const shownNow = async (page) =>
  textOf(await renderSSR({ ...Base, setup: () => page }));

beforeEach(() => {
  clearPageCache();
  axios.get.mockReset();
  notify.mockReset();
  store.role = { name: 'Experiment Admin' };
  views.found = undefined;
});

afterEach(() => vi.unstubAllGlobals());

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

      expect((await open(historyState)).text).toBe(shown);
      expect(axios.get).not.toHaveBeenCalled();
    },
  );

  it('shows a VM viewer the VM tiles of a running experiment', async () => {
    store.role = { name: 'VM Viewer' };
    expect((await open({ running: true })).text).toBe('VM tiles');
    expect((await open({ running: false })).text).toBe('stopped view');
  });

  it('asks the server when nothing says whether it runs, for the running view to reuse', async () => {
    const answer = Promise.withResolvers();
    axios.get.mockReturnValue(answer.promise);
    cachePage('experiments', [{ name: 'exp2', running: true }]);

    const { text, page } = await open();
    expect(text).toBe('Loading experiment…');
    expect(axios.get).toHaveBeenCalledWith('experiments/exp1', {
      signal: expect.any(AbortSignal),
      params: { vms: false },
    });

    answer.resolve({ data: { name: 'exp1', running: true } });
    await flush();
    expect(await shownNow(page)).toBe('running view');
    // kept as the running view's data, without the VMs it lists itself
    expect(cachedPage('experiment/exp1').data).toEqual({
      name: 'exp1',
      running: true,
      vms: [],
    });
    expect((await open()).text).toBe('running view');
    expect(axios.get).toHaveBeenCalledTimes(1);
  });

  it('reports an experiment it could not load', async () => {
    const err = new Error('not found');
    axios.get.mockRejectedValue(err);

    const { page } = await open();
    await flush();
    expect(notify).toHaveBeenCalledWith(err);
    expect(await shownNow(page)).toBe('Could not load the experiment');
  });

  it.each([
    [{ running: false }, true, 'stopped view', 'running view'],
    [{ running: true }, false, 'running view', 'stopped view'],
  ])(
    'switches views when the one shown finds the link wrong (link: %o)',
    async (historyState, running, guessed, shown) => {
      views.found = running;
      const { text, page } = await open(historyState);
      expect(text).toBe(guessed);
      expect(await shownNow(page)).toBe(shown);
    },
  );
});
