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
  store,
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
import { runOutcome, scorchRunTotals } from '@/utils/pageTotals.js';
import ScorchRuns from '@/views/ScorchRuns.vue';
import { flush, never } from './helpers/async.js';
import { wsMsg } from './helpers/fixtures.js';
import {
  buttons,
  mountPage,
  textOf,
  toasts,
  tooltipButtons,
  tooltipPositions,
} from './helpers/mount.js';
import { allowOnly, denyOnly } from './helpers/permissions.js';
import { componentDeclarations } from './helpers/css.js';

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
      'Download (disabled)',
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
      runs: [['Download (disabled)', 'start this run']],
    });
  });
});

describe('Cleanup', () => {
  it("runs only a run's cleanup stage, and waits while any run is busy", async () => {
    const wrapper = await open([{ hasCleanup: true }, {}]);
    expect((await pageButtons(wrapper)).runs).toEqual([
      ['Clear (disabled)', 'Cleanup', 'Download (disabled)', 'start this run'],
      // a run without cleanup components has no Cleanup
      ['Clear (disabled)', 'Download (disabled)', 'start this run'],
    ]);

    await click(runs(wrapper)[0], 'Cleanup');
    expect(sent()).toEqual([`POST ${url(0, 'cleanup')}`]);
    expect((await pageButtons(wrapper)).runs[0]).toEqual([
      'Clear (disabled)',
      'Cleanup (disabled)',
      'Download (disabled)',
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

describe("a run's Download button", () => {
  const filesURL = 'experiments/exp1/files';

  // a file as the experiment files listing gives it
  const listed = (path, size = 1536) => ({
    name: path.split('/').at(-1),
    path,
    date: '2026-10-29T14:10:00Z',
    size,
    categories: [],
    plainText: false,
    isDir: false,
  });

  // the server lists these files once asked, after the runs
  const listing = (...paths) =>
    axios.get.mockImplementation((path) =>
      path === filesURL
        ? Promise.resolve({ data: { files: paths.map((p) => listed(p)) } })
        : never(),
    );

  const filesListed = () =>
    axios.get.mock.calls.filter(([path]) => path === filesURL).length;

  // the run's Download control: its tooltip, and the archives its menu links
  const download = (wrapper, run) => {
    const where = runs(wrapper)[run];
    const [control] = tooltipButtons(where).filter(
      (t) =>
        t.control.text().includes('Download') && t.label.startsWith('Download'),
    );
    const links = where.findAll('.run-downloads-item a').map((a) => ({
      href: a.attributes('href'),
      started: a.find('time').attributes('datetime'),
      size: textOf(a.find('.run-downloads-size')),
    }));
    return { control, links };
  };

  it("links each of the run's archives, newest first, with its start time and size", async () => {
    store.token = 't';
    listing(
      'scorch-run-1_2026-10-28T09-00-00Z.tgz',
      'scorch-run-1_2026-10-29T14-03-22Z.tgz',
      'scorch-run-10_2026-10-30T00-00-00Z.tgz',
      'scorch-run-0_2026-10-30T00-00-00Z.tgz',
      'scorch/run-1/out.log',
    );
    const wrapper = await open([{}, { name: 'second' }]);

    const { control, links } = download(wrapper, 1);
    expect(control).toMatchObject({
      label: 'Download the artifacts of run second',
      disabled: false,
    });
    const file = (name) =>
      `/api/v1/experiments/exp1/files/${name}?path=${name}&token=t`;
    expect(links).toEqual([
      {
        href: file('scorch-run-1_2026-10-29T14-03-22Z.tgz'),
        started: '2026-10-29T14:03:22Z',
        size: '1.5 KiB',
      },
      {
        href: file('scorch-run-1_2026-10-28T09-00-00Z.tgz'),
        started: '2026-10-28T09:00:00Z',
        size: '1.5 KiB',
      },
    ]);
    expect(download(wrapper, 0).links.map((link) => link.started)).toEqual([
      '2026-10-30T00:00:00Z',
    ]);
    // the menu opens to the left, from the window's right edge
    expect(tooltipPositions(runs(wrapper)[1])).toContain('is-left');
    expect(control.control.attributes('aria-expanded')).toBe('false');
    await control.control.trigger('click');
    // Buefy opens the menu after the click's other handlers
    await flush();
    expect(control.control.attributes('aria-expanded')).toBe('true');
    expect(runs(wrapper)[1].find('.dropdown-menu').isVisible()).toBe(true);
  });

  it.each([
    [
      'the run has never finished',
      () =>
        listing(
          'scorch/run-0/out.log',
          'scorch-run-1_2026-10-29T14-03-22Z.tgz',
        ),
      'Cannot download: the run has no archive yet. SCORCH archives its artifacts each time it finishes',
    ],
    [
      'the files are still being listed',
      () => axios.get.mockImplementation(never),
      "Cannot download: the list of the experiment's files is still loading",
    ],
    [
      'the files could not be listed',
      () => axios.get.mockRejectedValue(new Error('502')),
      "Cannot download: the list of the experiment's files did not load",
    ],
  ])('is greyed with the reason when %s', async (_, answer, reason) => {
    answer();
    const wrapper = await open([{}]);
    const { control, links } = download(wrapper, 0);
    expect(control).toMatchObject({ tooltip: reason, disabled: true });
    expect(links).toEqual([]);
    // a failed listing is the button's to report
    expect(notify.error).not.toHaveBeenCalled();
  });

  it.each([
    [
      'may not list the files',
      denyOnly(['experiments/files', 'list']),
      "Cannot download: you do not have permission to list the experiment's files",
    ],
    ['may not download them', denyOnly(['experiments/files', 'get']), null],
    [
      'may only download them',
      allowOnly(['experiments/files'], ['experiments']),
      '',
    ],
  ])('for a role that %s', async (_, role, reason) => {
    rbac.allowed = role;
    listing('scorch-run-0_2026-10-29T14-03-22Z.tgz');
    const wrapper = await open([{}]);
    const { control, links } = download(wrapper, 0);

    if (reason === null) {
      // hidden, and the files are not asked for
      expect(control).toBeUndefined();
      expect(filesListed()).toBe(0);
    } else if (reason) {
      expect(control).toMatchObject({ tooltip: reason, disabled: true });
      expect(filesListed()).toBe(0);
    } else {
      expect(control.disabled).toBe(false);
      expect(links).toHaveLength(1);
    }
  });

  it("lists the files again once a run's done stage ends, for its new archive", async () => {
    listing();
    const wrapper = await open([{}]);
    expect(download(wrapper, 0).control.disabled).toBe(true);
    expect(filesListed()).toBe(1);

    // archiving, then archived
    const done = (status) => ({
      pipeline: [{ name: 'done', stage: '', status }],
    });
    report('pipeline-update', '0/0', done('running'));
    await flushPromises();
    expect(filesListed()).toBe(1);

    listing('scorch-run-0_2026-10-29T14-03-22Z.tgz');
    report('pipeline-update', '0/0', done('failure'));
    await flushPromises();
    expect(filesListed()).toBe(2);
    expect(download(wrapper, 0).control.disabled).toBe(false);
    expect(download(wrapper, 0).links).toHaveLength(1);
  });
});

describe("a run's pipeline box", () => {
  it('is a named region the keyboard can focus, to scroll a wide pipeline', async () => {
    const wrapper = await open([{}, { name: 'second' }]);
    const box = runs(wrapper)[1].find('.run-pipeline');
    expect(
      ['role', 'tabindex', 'aria-label'].map((a) => box.attributes(a)),
    ).toEqual(['region', '0', 'Run: second pipeline']);
  });
});

describe("a run's header", () => {
  const file = 'src/components/scorch/ScorchRun.vue';
  const style = (selector, media) =>
    componentDeclarations(file, selector, media);

  // at 1024 px Download and the play button wrapped below Clear and Cleanup
  it('keeps the name centered between the loop and the controls, and gives it a row of its own when narrow', async () => {
    const wrapper = await open([{}]);
    const header = runs(wrapper)[0].find('.run-header');
    expect(
      ['run-loop', 'run-title', 'run-status'].map((c) =>
        header.find(`.${c}`).exists(),
      ),
    ).toEqual([true, true, true]);

    expect(style('.scorch-run')).toEqual({ 'container-type': 'inline-size' });
    expect(style('.run-header')).toMatchObject({
      'grid-template-columns': '1fr auto 1fr',
      'grid-template-areas': "'loop title status'",
    });
    expect(style('.run-header', '(max-width: 80rem)')).toMatchObject({
      'grid-template-columns': 'auto 1fr',
      'grid-template-areas': "'title title' 'loop status'",
    });
    expect(
      ['.run-loop', '.run-title', '.run-status'].map(
        (s) => style(s)['grid-area'],
      ),
    ).toEqual(['loop', 'title', 'status']);
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

describe('the pipeline totals', () => {
  // the strip's stats, as "label: value"
  const totalsText = (wrapper) =>
    wrapper
      .findAll('[aria-label="Pipeline totals"] .stat')
      .map((s) => `${textOf(s.find('dt'))}: ${textOf(s.find('dd'))}`);

  const done = (status) => ({ name: 'done', stage: '', status });

  it('counts the pipelines by how their last run went, as the websocket reports it', async () => {
    const wrapper = await open(
      [
        {},
        { pipeline: [...nodes('success', 'failure'), done('success')] },
        { pipeline: [...nodes('success'), done('success')] },
        { pipeline: nodes('success', 'unknown') },
      ],
      0,
    );
    expect(totalsText(wrapper)).toEqual([
      'Pipelines: 4',
      'Running: 1',
      'Succeeded: 1',
      'Failed: 1',
    ]);

    report('success', 0);
    for (const handler of ws.handlers) {
      handler(
        wsMsg('apps/scorch', 'exp1/3/0', 'pipeline-update', {
          pipeline: [...nodes('success'), done('success')],
        }),
      );
    }
    await nextTick();
    expect(totalsText(wrapper)).toEqual([
      'Pipelines: 4',
      'Running: 0',
      'Succeeded: 2',
      'Failed: 1',
    ]);
  });

  it('shows the counts as unknown before the runs load', async () => {
    axios.get.mockImplementation(never);
    const wrapper = await mountPage(ScorchRuns, { route: '/scorch/exp1' });
    expect(totalsText(wrapper)).toEqual([
      'Pipelines: …',
      'Running: …',
      'Succeeded: …',
      'Failed: …',
    ]);
  });

  it('puts each pipeline in at most one outcome', () => {
    const status = fc.constantFrom(
      'unknown',
      'running',
      'success',
      'failure',
      'background',
    );
    const run = fc.record({
      running: fc.boolean(),
      nodes: fc.array(
        fc.record({
          name: fc.constantFrom('c0', 'c1', 'done'),
          status,
        }),
        { maxLength: 6 },
      ),
    });
    fc.assert(
      fc.property(fc.array(run, { maxLength: 12 }), (runs) => {
        const got = scorchRunTotals(runs);
        expect(got.pipelines).toBe(runs.length);
        expect(got.running).toBe(runs.filter((r) => r.running).length);
        expect(got.running + got.succeeded + got.failed).toBeLessThanOrEqual(
          got.pipelines,
        );
        for (const r of runs) {
          const outcome = runOutcome(r);
          if (r.running) expect(outcome).toBe('running');
          else if (r.nodes.some((n) => n.status === 'failure'))
            expect(outcome).toBe('failed');
        }
      }),
    );
  });
});
