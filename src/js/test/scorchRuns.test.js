// The SCORCH runs page: Start/Stop all, Clear, Cleanup and the run buttons
// that spin until the websocket reports a run started or stopped.
import { flushPromises } from '@vue/test-utils';
import fc from 'fast-check';
import { nextTick } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const {
  axios,
  modules,
  lastNotice,
  notices,
  noticeText,
  notify,
  rbac,
  resetMocks,
  ws,
} = await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/notify.js', modules.notify);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/store.js', modules.store);
// its terminal needs a browser
vi.mock('@/components/MiniTerminal.vue', modules.stub);

import ScorchRun from '@/components/scorch/ScorchRun.vue';
import { forgetServer, setWsDown } from '@/utils/freshness.js';
import { clearPageCache } from '@/utils/pageCache.js';
import ScorchRuns from '@/views/ScorchRuns.vue';
import { never } from './helpers/async.js';
import { wsMsg } from './helpers/fixtures.js';
import { buttons, mountPage, textOf, toasts } from './helpers/mount.js';
import { denyOnly } from './helpers/permissions.js';

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
  const wrapper = await mountPage(ScorchRuns, { route: '/scorch/exp1' });
  expect(axios.get).toHaveBeenCalledWith(
    'experiments/exp1/scorch/pipelines',
    expect.objectContaining({ headers: { Accept: 'application/json' } }),
  );
  return wrapper;
}

// what the server reports over the websocket about a run
const report = (action, run, result) => {
  for (const handler of ws.handlers) {
    handler(wsMsg('apps/scorch', `exp1/${run}`, action, result));
  }
};

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

const runs = (wrapper) => wrapper.findAllComponents(ScorchRun);

// the page's buttons once it has drawn its state, the header's and then
// each run's
async function pageButtons(wrapper) {
  await nextTick();
  return {
    header: buttons(wrapper.find('.runs-header')),
    runs: runs(wrapper).map(buttons),
  };
}

// clicks the button with this text or aria-label, in the header or a run
async function click(where, label) {
  await where
    .findAll('button')
    .find((b) => textOf(b) === label || b.attributes('aria-label') === label)
    .trigger('click');
}

// clicks a run's start or stop button
const runButton = (wrapper, run) =>
  runs(wrapper)[run].find('.run-status button[aria-label$=" this run"]');

beforeEach(() => {
  clearPageCache();
  resetMocks({ store: {} });
  axios.post.mockImplementation(never);
  axios.delete.mockImplementation(never);
});

afterEach(() => vi.useRealTimers());

describe('Start all', () => {
  it('starts the runs one after another, each once the one before ends', async () => {
    const wrapper = await open([{}, {}, {}]);
    expect((await pageButtons(wrapper)).header).toEqual([
      'Start all',
      'Stop all (disabled)',
      'Clear all (disabled)',
    ]);

    await click(wrapper, 'Start all');
    expect(sent()).toEqual([`POST ${url(0)}`]);
    const waiting = await pageButtons(wrapper);
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

    report('start', 0);
    expect(sent()).toHaveLength(1);
    report('success', 0);
    expect(sent()).toEqual([`POST ${url(0)}`, `POST ${url(1)}`]);

    // a failed run is reported, and the next still starts
    report('start', 1);
    report('error', 1, { error: 'component cc failed' });
    expect(notify.show).toHaveBeenCalledWith('component cc failed');
    expect(sent()).toEqual([
      `POST ${url(0)}`,
      `POST ${url(1)}`,
      `POST ${url(2)}`,
    ]);

    report('start', 2);
    report('success', 2);
    expect(sent()).toHaveLength(3);
    expect((await pageButtons(wrapper)).header[0]).toBe('Start all');
  });

  it('starts each run not running once, in order, whatever each run reports', async () => {
    // each run's reports once started: maybe a start, then how it ended, or
    // the server refusing to start it
    const reports = fc.array(
      fc.tuple(fc.boolean(), fc.constantFrom('success', 'error', 'refused')),
      { minLength: 1, maxLength: 5 },
    );
    const setup = reports.chain((ends) =>
      fc.tuple(
        fc.constant(ends),
        fc.integer({ min: -1, max: ends.length - 1 }),
      ),
    );
    await fc.assert(
      fc.asyncProperty(setup, async ([ends, running]) => {
        resetMocks({ store: {} });
        clearPageCache();
        axios.post.mockImplementation((path) =>
          ends.some(([, end], i) => end === 'refused' && path === url(i))
            ? Promise.reject(new Error('forbidden'))
            : never(),
        );
        const wrapper = await open(
          ends.map(() => ({})),
          running,
        );

        await click(wrapper, 'Start all');
        if (running >= 0) report('success', running);
        for (const [i, [started, end]] of ends.entries()) {
          await flushPromises();
          if (i === running || end === 'refused') continue;
          if (started) report('start', i);
          report(end, i);
        }
        await flushPromises();

        expect(sent()).toEqual(
          ends.map((_, i) => `POST ${url(i)}`).filter((_, i) => i !== running),
        );
        expect((await pageButtons(wrapper)).header).toEqual([
          'Start all',
          'Stop all (disabled)',
          'Clear all (disabled)',
        ]);
        wrapper.unmount();
      }),
      { numRuns: 20 },
    );
  });

  it('moves on to the next run when the server refuses a start', async () => {
    const err = new Error('forbidden');
    axios.post.mockRejectedValueOnce(err);
    const wrapper = await open([{}, {}]);

    await click(wrapper, 'Start all');
    await flushPromises();
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(sent()).toEqual([`POST ${url(0)}`, `POST ${url(1)}`]);
    expect((await pageButtons(wrapper)).runs.map((run) => run.at(-1))).toEqual([
      'start this run',
      'start this run (spinning) (disabled)',
    ]);
  });

  it.each([
    ['refuses a start as it shuts down', { response: { status: 503 } }],
    ['does not answer', { code: 'ERR_NETWORK', request: {} }],
  ])('drops the runs waiting to start when the server %s', async (_, err) => {
    axios.post.mockRejectedValueOnce(err);
    const wrapper = await open([{}, {}, {}]);

    await click(wrapper, 'Start all');
    await flushPromises();
    expect(sent()).toEqual([`POST ${url(0)}`]);
    const after = await pageButtons(wrapper);
    expect(after.header[0]).toBe('Start all');
    expect(after.runs.map((run) => run.at(-1))).toEqual([
      'start this run',
      'start this run',
      'start this run',
    ]);
  });

  it('drops the runs waiting to start when the server shuts down before a start is reported', async () => {
    const wrapper = await open([{}, {}]);
    vi.useFakeTimers();
    try {
      await click(wrapper, 'Start all');
      setWsDown(true, 'shutdown');
      vi.advanceTimersByTime(15000);
      expect(sent()).toEqual([`POST ${url(0)}`]);
    } finally {
      forgetServer();
    }
  });

  it('sends the first start of Start all while the server seems to be shutting down, so its answer is shown', async () => {
    const err = { response: { status: 503 } };
    axios.post.mockRejectedValueOnce(err);
    const wrapper = await open([{}, {}]);
    setWsDown(true, 'shutdown');
    try {
      await click(wrapper, 'Start all');
      await flushPromises();
      expect(sent()).toEqual([`POST ${url(0)}`]);
      expect(notify.error).toHaveBeenCalledWith(err);
      const after = await pageButtons(wrapper);
      expect(after.runs.map((run) => run.at(-1))).toEqual([
        'start this run',
        'start this run',
      ]);
    } finally {
      forgetServer();
    }
  });

  it('lets a run already running finish first', async () => {
    const wrapper = await open([{}, {}, {}], 1);

    await click(wrapper, 'Start all');
    expect(sent()).toEqual([]);

    report('success', 1);
    expect(sent()).toEqual([`POST ${url(0)}`]);
    report('start', 0);
    report('success', 0);
    expect(sent()).toEqual([`POST ${url(0)}`, `POST ${url(2)}`]);
  });

  it('moves on to the next run when no report of a start comes', async () => {
    const wrapper = await open([{}, {}]);
    vi.useFakeTimers();

    await click(wrapper, 'Start all');
    vi.advanceTimersByTime(15000);
    expect(sent()).toEqual([`POST ${url(0)}`, `POST ${url(1)}`]);
  });
});

describe('Stop all', () => {
  it('stops the running run and drops the runs waiting to start', async () => {
    const wrapper = await open([{}, {}, {}], 1);
    await click(wrapper, 'Start all');

    await click(wrapper, 'Stop all');
    expect(sent()).toEqual([`DELETE ${url(1)}`]);
    report('success', 1);
    expect(sent()).toHaveLength(1);
    expect((await pageButtons(wrapper)).header).toEqual([
      'Start all',
      'Stop all (disabled)',
      'Clear all (disabled)',
    ]);
  });
});

describe("a run's start and stop button", () => {
  it('spins until the server reports the run started, then stopped', async () => {
    const wrapper = await open([{}]);
    const shown = async () => (await pageButtons(wrapper)).runs[0].at(-1);
    expect(await shown()).toBe('start this run');

    await runButton(wrapper, 0).trigger('click');
    expect(await shown()).toBe('start this run (spinning) (disabled)');
    // a second press does nothing, even past the disabled button
    runButton(wrapper, 0).element.disabled = false;
    await runButton(wrapper, 0).trigger('click');
    expect(sent()).toEqual([`POST ${url(0)}`]);

    report('start', 0);
    expect(await shown()).toBe('stop this run');

    await runButton(wrapper, 0).trigger('click');
    expect(sent()).toEqual([`POST ${url(0)}`, `DELETE ${url(0)}`]);
    expect(await shown()).toBe('stop this run (spinning) (disabled)');

    report('success', 0);
    expect(await shown()).toBe('start this run');
  });

  it('stops spinning after 15 seconds without a report', async () => {
    const wrapper = await open([{}]);
    vi.useFakeTimers();

    await runButton(wrapper, 0).trigger('click');
    vi.advanceTimersByTime(14999);
    expect((await pageButtons(wrapper)).runs[0].at(-1)).toBe(
      'start this run (spinning) (disabled)',
    );
    vi.advanceTimersByTime(1);
    vi.useRealTimers();
    expect((await pageButtons(wrapper)).runs[0].at(-1)).toBe('start this run');
  });

  it('stops spinning and reports a request that fails', async () => {
    const err = new Error('forbidden');
    axios.post.mockRejectedValue(err);
    const wrapper = await open([{}]);

    await runButton(wrapper, 0).trigger('click');
    await flushPromises();
    expect(notify.error).toHaveBeenCalledWith(err);
    expect((await pageButtons(wrapper)).runs[0].at(-1)).toBe('start this run');
  });
});

describe('Clear', () => {
  it("forgets an idle run's statuses, back on its first loop", async () => {
    const wrapper = await open([
      {
        pipeline: [
          ...nodes('success', 'failure'),
          { name: 'loop', exp: 'exp1', run: 0, loop: 0, status: 'success' },
        ],
      },
      {},
      { pipeline: nodes('running') },
    ]);
    report('start', 2);
    expect((await pageButtons(wrapper)).runs.map((run) => run[0])).toEqual([
      'Clear',
      // nothing to clear
      'Clear (disabled)',
      // still running
      'Clear (disabled)',
    ]);

    // viewing the run's second loop
    axios.get.mockResolvedValue({ data: { pipeline: nodes('success') } });
    await runs(wrapper)[0]
      .findAll('.pipeline-node-label')
      .find((label) => label.text() === 'loop')
      .trigger('click');
    await flushPromises();
    expect(axios.get).toHaveBeenLastCalledWith(url(0, '1'), {
      headers: { Accept: 'application/json' },
    });

    const cleared = Promise.withResolvers();
    axios.post.mockReturnValue(cleared.promise);
    await click(runs(wrapper)[0], 'Clear');
    expect(sent()).toEqual([`POST ${url(0, 'clear')}`]);
    expect((await pageButtons(wrapper)).runs[0]).toEqual([
      'Return to the previous loop',
      'Clear (disabled)',
      'start this run',
    ]);

    axios.get.mockResolvedValue({ data: { pipeline: nodes('unknown') } });
    cleared.resolve({});
    await flushPromises();
    expect(axios.get).toHaveBeenLastCalledWith(url(0, '0'), {
      headers: { Accept: 'application/json' },
    });
  });

  it('all clears every run that can be cleared', async () => {
    axios.post.mockResolvedValue({});
    const wrapper = await open([
      { pipeline: nodes('success') },
      {},
      { pipeline: nodes('failure') },
    ]);

    await click(wrapper, 'Clear all');
    await flushPromises();
    expect(sent()).toEqual([
      `POST ${url(0, 'clear')}`,
      `POST ${url(2, 'clear')}`,
    ]);
    expect(toasts().map((toast) => toast.text)).toEqual(['Cleared 2 runs']);
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
    const wrapper = await open([
      { pipeline: nodes('success') },
      { pipeline: nodes('failure') },
    ]);

    await click(wrapper, 'Clear all');
    await flushPromises();
    expect(notify.error).not.toHaveBeenCalled();
    expect(notices.open).toHaveBeenCalledTimes(1);
    const text = noticeText();
    expect(text).toContain('Cleared 0 of 2 runs: 2 failed');
    expect(text).toContain('0: busy');
    expect(text).toContain('1: forbidden');

    axios.post.mockResolvedValue({});
    await lastNotice()
      .message.children.find((child) => child.type === 'button')
      .props.onClick();
    expect(sent().at(-1)).toBe(`POST ${url(0, 'clear')}`);
  });

  it('reports a clear the server refuses', async () => {
    const err = new Error('forbidden');
    axios.post.mockRejectedValue(err);
    const wrapper = await open([{ pipeline: nodes('success') }]);

    await click(runs(wrapper)[0], 'Clear');
    await flushPromises();
    expect(notify.error).toHaveBeenCalledWith(err);
    expect((await pageButtons(wrapper)).runs[0][0]).toBe('Clear');
  });

  it('is offered only to roles that may start runs', async () => {
    rbac.allowed = denyOnly(['experiments/trigger', 'create']);
    const wrapper = await open([{ pipeline: nodes('success') }]);

    expect(await pageButtons(wrapper)).toEqual({
      header: ['Stop all (disabled)'],
      runs: [['start this run']],
    });
  });
});

describe('Cleanup', () => {
  it("runs only a run's cleanup stage, and waits while any run is busy", async () => {
    const wrapper = await open([{ hasCleanup: true }, {}]);
    expect((await pageButtons(wrapper)).runs).toEqual([
      ['Clear (disabled)', 'Cleanup', 'start this run'],
      // a run without cleanup components has no Cleanup
      ['Clear (disabled)', 'start this run'],
    ]);

    await click(runs(wrapper)[0], 'Cleanup');
    expect(sent()).toEqual([`POST ${url(0, 'cleanup')}`]);
    expect((await pageButtons(wrapper)).runs[0]).toEqual([
      'Clear (disabled)',
      'Cleanup (disabled)',
      'start this run (spinning) (disabled)',
    ]);

    report('start', 0);
    expect((await pageButtons(wrapper)).runs[0][1]).toBe('Cleanup (disabled)');
    report('success', 0);
    expect((await pageButtons(wrapper)).runs[0][1]).toBe('Cleanup');

    // another run's start holds it too
    await runButton(wrapper, 1).trigger('click');
    expect((await pageButtons(wrapper)).runs[0][1]).toBe('Cleanup (disabled)');
  });
});

describe("a component's streamed output", () => {
  class FakeWebSocket {
    static instances = [];
    constructor(url) {
      this.url = url;
      FakeWebSocket.instances.push(this);
    }
    close() {}
  }

  let freshness;
  beforeEach(async () => {
    FakeWebSocket.instances = [];
    vi.stubGlobal('WebSocket', FakeWebSocket);
    freshness = await import('@/utils/freshness.js');
  });

  afterEach(() => {
    freshness.setWsDown(false);
    vi.unstubAllGlobals();
  });

  // opens the output of run 0's first component, which streams it
  async function stream() {
    const wrapper = await open([{}]);
    axios.get.mockResolvedValueOnce({ data: { stream: '/api/v1/out/ws' } });
    runs(wrapper)[0].props('viewer')({
      exp: 'exp1',
      run: 0,
      loop: 0,
      stage: 'start',
      name: 'c0',
      status: 'running',
    });
    await flushPromises();
    const socket = FakeWebSocket.instances.at(-1);
    socket.onmessage({ data: 'line 1' });
    return { wrapper, socket };
  }

  const output = (wrapper) => wrapper.find('textarea').element.value;

  it.each([
    [
      'the server shutting down',
      { code: 1001, reason: '' },
      'line 1\n[output stream ended: phēnix server shutting down]',
    ],
    [
      'a lost connection',
      { code: 1006, reason: '' },
      'line 1\n[lost the connection to the phēnix server. Close and reopen to retry]',
    ],
    ['the stream ending', { code: 1000, reason: '' }, 'line 1'],
  ])('says it stopped after %s', async (_, event, want) => {
    const { wrapper, socket } = await stream();
    socket.onclose(event);
    await nextTick();
    expect(output(wrapper)).toBe(want);
  });
});
