// An experiment's SCORCH runs page: starting every run one after another,
// stopping them, clearing and cleaning up runs, and the run buttons that spin
// until the server reports over the websocket that a run started or stopped.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, notices, notify, rbac, resetMocks, switchable } =
  await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/notify.js', modules.notify);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/store.js', modules.store);
// its terminal needs a browser
vi.mock('@/components/MiniTerminal.vue', modules.stub);
vi.mock('@/utils/pageLoader.js', switchable);

import { clearPageCache } from '@/utils/pageCache.js';
import ScorchRuns from '@/views/ScorchRuns.vue';
import { flush, never } from './helpers/async.js';
import { wsMsg } from './helpers/fixtures.js';
import { openPage } from './helpers/page.js';
import { withoutLoading } from './helpers/pageLoader.js';
import { denyOnly } from './helpers/permissions.js';
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
  const ctx = await openPage(ScorchRuns, { id: 'exp1' });
  expect(axios.get).toHaveBeenCalledWith(
    'experiments/exp1/scorch/pipelines',
    expect.objectContaining({ headers: { Accept: 'application/json' } }),
  );
  return ctx;
}

// what the server reports over the websocket about a run
const report = (ctx, action, run, result) =>
  ctx.handle(wsMsg('apps/scorch', `exp1/${run}`, action, result));

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
  const html = await withoutLoading(() =>
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
  resetMocks({ store: {} });
  axios.post.mockImplementation(never);
  axios.delete.mockImplementation(never);
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

    await ctx.clearAll();
    expect(sent()).toEqual([
      `POST ${url(0, 'clear')}`,
      `POST ${url(2, 'clear')}`,
    ]);
    expect(ctx.$buefy.toast.open).toHaveBeenCalledWith(
      expect.objectContaining({ message: 'Cleared 2 runs' }),
    );
  });

  it('all says in one summary which runs it could not clear, and retries them', async () => {
    axios.post
      .mockRejectedValueOnce(
        Object.assign(new Error('503'), {
          response: { status: 503, data: 'busy' },
        }),
      )
      .mockRejectedValueOnce(
        Object.assign(new Error('403'), {
          response: { status: 403, data: 'forbidden' },
        }),
      );
    const ctx = await open([
      { pipeline: nodes('success') },
      { pipeline: nodes('failure') },
    ]);

    await ctx.clearAll();
    expect(notify.error).not.toHaveBeenCalled();
    expect(notices.open).toHaveBeenCalledTimes(1);
    const { message } = notices.open.mock.lastCall[0];
    const text = JSON.stringify(message.children, (_, v) =>
      typeof v === 'function' ? undefined : v,
    );
    expect(text).toContain('Cleared 0 of 2 runs; 2 failed');
    expect(text).toContain('0: busy');
    expect(text).toContain('1: forbidden');

    axios.post.mockResolvedValue({});
    await message.children
      .find((child) => child.type === 'button')
      .props.onClick();
    expect(sent().at(-1)).toBe(`POST ${url(0, 'clear')}`);
  });

  it('reports a clear the server refuses', async () => {
    const err = new Error('forbidden');
    axios.post.mockRejectedValue(err);
    const ctx = await open([{ pipeline: nodes('success') }]);

    await ctx.clearRun('exp1', 0);
    expect(notify.error).toHaveBeenCalledWith(err);
    expect((await pageButtons(ctx)).runs[0][0]).toBe('Clear');
  });
});

describe('Permissions', () => {
  // starting and cleaning up need scorch post, stopping and clearing scorch
  // delete, each with experiments get; experiments/trigger plays no part
  it.each([
    [
      'scorch post',
      ['scorch', 'post'],
      { header: ['Stop all (disabled)', 'Clear all'], runs: [['Clear']] },
    ],
    [
      'scorch delete',
      ['scorch', 'delete'],
      { header: ['Start all'], runs: [['start this run']] },
    ],
    ['experiments get', ['experiments', 'get'], { header: [], runs: [[]] }],
    [
      'no experiments/trigger',
      ['experiments/trigger'],
      {
        header: ['Start all', 'Stop all (disabled)', 'Clear all'],
        runs: [['Clear', 'start this run']],
      },
    ],
  ])(
    'offers the run controls a role without %s may use',
    async (_, perm, want) => {
      const ctx = await open([{ pipeline: nodes('success') }]);
      expect(await pageButtons(ctx)).toEqual({
        header: ['Start all', 'Stop all (disabled)', 'Clear all'],
        runs: [['Clear', 'start this run']],
      });

      rbac.allowed = denyOnly(perm);
      expect(await pageButtons(ctx)).toEqual(want);
    },
  );

  it('offers Cleanup only to roles that may post scorch', async () => {
    const ctx = await open([{ hasCleanup: true }]);
    expect((await pageButtons(ctx)).runs[0]).toContain('Cleanup');

    rbac.allowed = denyOnly(['scorch', 'post']);
    expect((await pageButtons(ctx)).runs[0]).not.toContain('Cleanup');
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
