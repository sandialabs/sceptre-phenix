// An experiment's SCORCH runs page: starting every run one after another,
// stopping them, clearing and cleaning up runs, and the run buttons that spin
// until the server reports over the websocket that a run started or stopped.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const rbac = vi.hoisted(() => ({ allowed: () => true }));
vi.mock('@/utils/rbac.js', () => ({
  roleAllowed: (...args) => rbac.allowed(...args),
}));
const axios = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  delete: vi.fn(),
}));
vi.mock('@/utils/axios.js', () => ({ default: axios }));
const notify = vi.hoisted(() => ({ error: vi.fn(), show: vi.fn() }));
vi.mock('@/utils/errorNotif', () => ({
  useErrorNotification: notify.error,
  showError: notify.show,
}));
vi.mock('@/utils/websocket', () => ({
  addWsHandler: () => {},
  removeWsHandler: () => {},
}));
vi.mock('@/store.js', () => ({ usePhenixStore: () => ({}) }));
// its terminal needs a browser
vi.mock('@/components/MiniTerminal.vue', () => ({ default: {} }));
const loader = await vi.hoisted(() => import('./helpers/pageLoader.js'));
vi.mock('@/utils/pageLoader.js', loader.switchable);

import { clearPageCache } from '@/utils/pageCache.js';
import ScorchRuns from '@/views/ScorchRuns.vue';
import { flush } from './helpers/async.js';
import { makeContext } from './helpers/context.js';
import { buttons, renderSSR } from './helpers/render.js';

const url = (run, action = '') =>
  `experiments/exp1/scorch/pipelines/${run}${action && `/${action}`}`;

// a run's pipeline, its components in the given statuses
const nodes = (...statuses) =>
  statuses.map((status, i) => ({ name: `c${i}`, stage: 'start', status }));

// the page on exp1 once the server has sent it these runs
async function open(runs, running = -1) {
  axios.get.mockResolvedValueOnce({
    data: {
      experiment: { status: 'started' },
      running,
      pipelines: runs.map((run, i) => ({
        name: `run${i}`,
        pipeline: nodes('unknown'),
        ...run,
      })),
    },
  });
  const ctx = makeContext(ScorchRuns, {
    $route: { params: { id: 'exp1' } },
    $buefy: { toast: { open: vi.fn() } },
  });
  ScorchRuns.created.call(ctx);
  await flush();
  expect(axios.get).toHaveBeenCalledWith(
    'experiments/exp1/scorch/pipelines',
    expect.objectContaining({ headers: { Accept: 'application/json' } }),
  );
  return ctx;
}

// what the server reports over the websocket about a run
const report = (ctx, action, run, result) =>
  ctx.handle({
    resource: { type: 'apps/scorch', name: `exp1/${run}`, action },
    result,
  });

// the requests sent to start, stop, clear and clean up runs, in order
const sent = () =>
  [
    ...axios.post.mock.calls.map(([path], i) => [
      axios.post.mock.invocationCallOrder[i],
      `POST ${path}`,
    ]),
    ...axios.delete.mock.calls.map(([path], i) => [
      axios.delete.mock.invocationCallOrder[i],
      `DELETE ${path}`,
    ]),
  ]
    .sort(([a], [b]) => a - b)
    .map(([, request]) => request);

// the page's buttons as its state has it, the header's and then each run's
async function pageButtons(ctx) {
  const html = await loader.withoutLoading(() =>
    renderSSR(
      ScorchRuns,
      {},
      {
        route: '/scorch/exp1',
        data: { runs: ctx.runs, expStatus: ctx.expStatus },
      },
    ),
  );
  const [header, ...runs] = html.split('<hr').slice(0, -1);
  return { header: buttons(header), runs: runs.map(buttons) };
}

beforeEach(() => {
  clearPageCache();
  rbac.allowed = () => true;
  for (const fn of [
    axios.get,
    axios.post,
    axios.delete,
    notify.error,
    notify.show,
  ]) {
    fn.mockReset();
  }
  axios.post.mockReturnValue(new Promise(() => {}));
  axios.delete.mockReturnValue(new Promise(() => {}));
});

afterEach(() => vi.useRealTimers());

describe('Start all', () => {
  it('starts the runs one after another, each once the one before ends', async () => {
    const ctx = await open([{}, {}, {}]);
    expect((await pageButtons(ctx)).header).toEqual([
      'Start all',
      'Stop all (disabled)',
      'Clear all (disabled)',
    ]);

    ctx.startAll();
    expect(sent()).toEqual([`POST ${url(0)}`]);
    const waiting = await pageButtons(ctx);
    expect(waiting.header).toEqual([
      'Start all (disabled)',
      'Stop all',
      'Clear all (disabled)',
    ]);
    // the runs waiting their turn spin too
    expect(waiting.runs.map((run) => run.at(-1))).toEqual([
      'start this run (spinning) (disabled)',
      'start this run (spinning) (disabled)',
      'start this run (spinning) (disabled)',
    ]);

    report(ctx, 'start', 0);
    expect(sent()).toHaveLength(1);
    report(ctx, 'success', 0);
    expect(sent()).toEqual([`POST ${url(0)}`, `POST ${url(1)}`]);

    // a failed run is reported, and the next still starts
    report(ctx, 'start', 1);
    report(ctx, 'error', 1, { error: 'component cc failed' });
    expect(notify.show).toHaveBeenCalledWith('component cc failed');
    expect(sent()).toEqual([
      `POST ${url(0)}`,
      `POST ${url(1)}`,
      `POST ${url(2)}`,
    ]);

    report(ctx, 'start', 2);
    report(ctx, 'success', 2);
    expect(sent()).toHaveLength(3);
    expect((await pageButtons(ctx)).header[0]).toBe('Start all');
  });

  it('lets a run already running finish first', async () => {
    const ctx = await open([{}, {}, {}], 1);

    ctx.startAll();
    expect(sent()).toEqual([]);

    report(ctx, 'success', 1);
    expect(sent()).toEqual([`POST ${url(0)}`]);
    report(ctx, 'start', 0);
    report(ctx, 'success', 0);
    expect(sent()).toEqual([`POST ${url(0)}`, `POST ${url(2)}`]);
  });

  it('moves on to the next run when no report of a start comes', async () => {
    const ctx = await open([{}, {}]);
    vi.useFakeTimers();

    ctx.startAll();
    vi.advanceTimersByTime(15000);
    expect(sent()).toEqual([`POST ${url(0)}`, `POST ${url(1)}`]);
  });
});

describe('Stop all', () => {
  it('stops the running run and drops the runs waiting to start', async () => {
    const ctx = await open([{}, {}, {}], 1);
    ctx.startAll();

    ctx.stopAll();
    expect(sent()).toEqual([`DELETE ${url(1)}`]);
    report(ctx, 'success', 1);
    expect(sent()).toHaveLength(1);
    expect((await pageButtons(ctx)).header).toEqual([
      'Start all',
      'Stop all (disabled)',
      'Clear all (disabled)',
    ]);
  });
});

describe("a run's start and stop button", () => {
  it('spins until the server reports the run started, then stopped', async () => {
    const ctx = await open([{}]);
    const runButton = async () => (await pageButtons(ctx)).runs[0].at(-1);
    expect(await runButton()).toBe('start this run');

    ctx.scorchControl('exp1', 0);
    // pressing it again while it spins sends nothing more
    ctx.scorchControl('exp1', 0);
    expect(sent()).toEqual([`POST ${url(0)}`]);
    expect(await runButton()).toBe('start this run (spinning) (disabled)');

    report(ctx, 'start', 0);
    expect(await runButton()).toBe('stop this run');

    ctx.scorchControl('exp1', 0);
    expect(sent()).toEqual([`POST ${url(0)}`, `DELETE ${url(0)}`]);
    expect(await runButton()).toBe('stop this run (spinning) (disabled)');

    report(ctx, 'success', 0);
    expect(await runButton()).toBe('start this run');
  });

  it('stops spinning after 15 seconds without a report', async () => {
    const ctx = await open([{}]);
    vi.useFakeTimers();

    ctx.scorchControl('exp1', 0);
    vi.advanceTimersByTime(14999);
    expect((await pageButtons(ctx)).runs[0].at(-1)).toBe(
      'start this run (spinning) (disabled)',
    );
    vi.advanceTimersByTime(1);
    vi.useRealTimers();
    expect((await pageButtons(ctx)).runs[0].at(-1)).toBe('start this run');
  });

  it('stops spinning and reports a request that fails', async () => {
    const err = new Error('forbidden');
    axios.post.mockRejectedValue(err);
    const ctx = await open([{}]);

    ctx.scorchControl('exp1', 0);
    await flush();
    expect(notify.error).toHaveBeenCalledWith(err);
    expect((await pageButtons(ctx)).runs[0].at(-1)).toBe('start this run');
  });
});

describe('Clear', () => {
  it("forgets an idle run's statuses, back on its first loop", async () => {
    const ctx = await open([
      { pipeline: nodes('success', 'failure') },
      {},
      { pipeline: nodes('running') },
    ]);
    report(ctx, 'start', 2);
    expect((await pageButtons(ctx)).runs.map((run) => run[0])).toEqual([
      'Clear',
      // nothing to clear
      'Clear (disabled)',
      // still running
      'Clear (disabled)',
    ]);

    // viewing the run's second loop
    axios.get.mockResolvedValue({ data: { pipeline: nodes('success') } });
    ctx.componentDetail({ name: 'loop', exp: 'exp1', run: 0, loop: 0 });
    await flush();
    expect(axios.get).toHaveBeenLastCalledWith(url(0, '1'), {
      headers: { Accept: 'application/json' },
    });

    const cleared = Promise.withResolvers();
    axios.post.mockReturnValue(cleared.promise);
    ctx.clearRun('exp1', 0);
    expect(sent()).toEqual([`POST ${url(0, 'clear')}`]);
    expect((await pageButtons(ctx)).runs[0]).toEqual([
      'Return to the previous loop',
      'Clear (disabled)',
      'start this run',
    ]);

    axios.get.mockResolvedValue({ data: { pipeline: nodes('unknown') } });
    cleared.resolve({});
    await flush();
    expect(axios.get).toHaveBeenLastCalledWith(url(0, '0'), {
      headers: { Accept: 'application/json' },
    });
  });

  it('all clears every run that can be cleared', async () => {
    axios.post.mockResolvedValue({});
    const ctx = await open([
      { pipeline: nodes('success') },
      {},
      { pipeline: nodes('failure') },
    ]);

    ctx.clearAll();
    expect(sent()).toEqual([
      `POST ${url(0, 'clear')}`,
      `POST ${url(2, 'clear')}`,
    ]);
  });

  it('reports a clear the server refuses', async () => {
    const err = new Error('forbidden');
    axios.post.mockRejectedValue(err);
    const ctx = await open([{ pipeline: nodes('success') }]);

    await ctx.clearRun('exp1', 0);
    expect(notify.error).toHaveBeenCalledWith(err);
    expect((await pageButtons(ctx)).runs[0][0]).toBe('Clear');
  });

  it('is offered only to roles that may start runs', async () => {
    rbac.allowed = (resource, verb) =>
      !(resource === 'experiments/trigger' && verb === 'create');
    const ctx = await open([{ pipeline: nodes('success') }]);

    expect(await pageButtons(ctx)).toEqual({
      header: ['Stop all (disabled)'],
      runs: [['start this run']],
    });
  });
});

describe('Cleanup', () => {
  it("runs only a run's cleanup stage, and waits while any run is busy", async () => {
    const ctx = await open([{ hasCleanup: true }, {}]);
    expect((await pageButtons(ctx)).runs).toEqual([
      ['Clear (disabled)', 'Cleanup', 'start this run'],
      // a run without cleanup components has no Cleanup
      ['Clear (disabled)', 'start this run'],
    ]);

    ctx.cleanupRun('exp1', 0);
    expect(sent()).toEqual([`POST ${url(0, 'cleanup')}`]);
    expect((await pageButtons(ctx)).runs[0]).toEqual([
      'Clear (disabled)',
      'Cleanup (disabled)',
      'start this run (spinning) (disabled)',
    ]);

    report(ctx, 'start', 0);
    expect((await pageButtons(ctx)).runs[0][1]).toBe('Cleanup (disabled)');
    report(ctx, 'success', 0);
    expect((await pageButtons(ctx)).runs[0][1]).toBe('Cleanup');

    // another run's start holds it too
    ctx.scorchControl('exp1', 1);
    expect((await pageButtons(ctx)).runs[0][1]).toBe('Cleanup (disabled)');
  });
});
