// The State of Health overview: how it judges each experiment's health, the
// line and filters above its table, and the page itself.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const allowed = vi.hoisted(() => ({ fn: () => true }));
vi.mock('@/utils/rbac.js', () => ({
  roleAllowed: (...args) => allowed.fn(...args),
}));

const axios = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock('@/utils/axios.js', () => ({ default: axios }));

const notify = vi.hoisted(() => ({ showError: vi.fn(), useError: vi.fn() }));
vi.mock('@/utils/errorNotif.js', () => ({
  showError: notify.showError,
  useErrorNotification: notify.useError,
}));

vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ role: { name: 'Global Admin' } }),
}));

vi.mock('@/utils/websocket', () => ({
  addWsHandler: () => {},
  removeWsHandler: () => {},
}));

import {
  filterCounts,
  overview,
  problemCount,
  problems,
  sparkPoints,
  trend,
  verdict,
  verdictLabel,
  vmSegments,
  vmsDown,
} from '@/utils/sohOverview.js';
import { createLiveRows } from '@/utils/liveRows.js';
import { cachePage, clearPageCache } from '@/utils/pageCache.js';
import { pageStatus } from '@/utils/pageLoader.js';
import SohOverview, {
  PROBLEMS_SHOWN,
  RELOAD_DELAY_MS,
} from '@/views/SohOverview.vue';
import { makeContext } from './helpers/context.js';
import {
  renderSSR,
  tableRows,
  textOf,
  tooltipButtons,
} from './helpers/render.js';

// every test starts with a user who may do anything
beforeEach(() => {
  allowed.fn = () => true;
  axios.get.mockReset();
  axios.post.mockReset();
  notify.showError.mockReset();
  notify.useError.mockReset();
});

// a running experiment with soh results and every check passing
function summary(overrides = {}) {
  return {
    name: 'ot-substation',
    scenario: 'soh-baseline',
    status: 'started',
    running: true,
    configured: true,
    started: true,
    soh_initialized: true,
    soh_running: false,
    vms: {
      known: true,
      total: 24,
      running: 24,
      failing: 0,
      notrunning: 0,
      notboot: 0,
      notdeploy: 0,
      external: 0,
      delayed: 0,
    },
    down_vms: [],
    hosts: 24,
    hosts_with_errors: 0,
    checks: { total: 212, passing: 212, failing: 0 },
    reachability: { total: 64, passing: 64, failing: 0 },
    last_run: '2026-09-28T14:02:11Z',
    history: [],
    failing_checks: [],
    ...overrides,
  };
}

describe('verdict', () => {
  it('is healthy when every check passes and every VM is up', () => {
    expect(verdict(summary())).toBe('healthy');
  });

  it('is checks failing for a few failing hosts', () => {
    const s = summary({
      hosts: 40,
      hosts_with_errors: 2,
      checks: { total: 320, passing: 318, failing: 2 },
    });
    expect(verdict(s)).toBe('failing');
    expect(verdictLabel(s)).toBe('checks failing');
  });

  it('is degraded when a VM that should run is down', () => {
    const vms = { ...summary().vms, running: 23, notrunning: 1 };
    expect(verdict(summary({ vms }))).toBe('degraded');

    const flushed = { ...summary().vms, running: 23, notdeploy: 1 };
    expect(verdict(summary({ vms: flushed }))).toBe('degraded');
  });

  it('does not count VMs left unbooted, external or delayed as down', () => {
    const vms = {
      ...summary().vms,
      running: 20,
      notboot: 2,
      external: 1,
      delayed: 1,
    };
    expect(vmsDown(summary({ vms }))).toBe(0);
    expect(verdict(summary({ vms }))).toBe('healthy');
  });

  it('is degraded when a fifth of the hosts fail checks', () => {
    const at = (failingHosts) =>
      verdict(
        summary({
          hosts: 10,
          hosts_with_errors: failingHosts,
          checks: { total: 100, passing: 90, failing: 10 },
        }),
      );
    expect(at(1)).toBe('failing');
    expect(at(2)).toBe('degraded');
  });

  it('is degraded when under 90% of reachability pairs pass', () => {
    const at = (passing) =>
      verdict(
        summary({
          hosts_with_errors: 1,
          checks: { total: 212, passing: 212 - (100 - passing), failing: 1 },
          reachability: { total: 100, passing, failing: 100 - passing },
        }),
      );
    expect(at(90)).toBe('failing');
    expect(at(89)).toBe('degraded');
  });

  it('ignores VM states it could not read', () => {
    const vms = { known: false, total: 24 };
    expect(verdict(summary({ vms }))).toBe('healthy');
    expect(vmSegments(summary({ vms }))).toEqual([]);
  });

  it('waits for a configured experiment that is not up yet', () => {
    const stopped = summary({ running: false, status: 'stopped' });
    expect(verdict(stopped)).toBe('waiting');
    expect(verdictLabel(stopped)).toBe('waiting for start');

    const starting = summary({ running: false, status: 'starting' });
    expect(verdict(starting)).toBe('waiting');
  });

  it('waits for the first run of a running experiment', () => {
    const s = summary({
      soh_initialized: false,
      checks: { total: 0, passing: 0, failing: 0 },
    });
    expect(verdict(s)).toBe('waiting');
    expect(verdictLabel(s)).toBe('waiting for first run');

    // partial results from a first run in progress are judged
    const partial = summary({
      soh_initialized: false,
      soh_running: true,
      checks: { total: 10, passing: 10, failing: 0 },
    });
    expect(verdict(partial)).toBe('healthy');
  });

  it('is not configured without the soh app, whatever else', () => {
    expect(verdict(summary({ configured: false }))).toBe('unconfigured');
    expect(verdict(summary({ configured: false, running: false }))).toBe(
      'unconfigured',
    );
  });
});

describe('summaries', () => {
  const degraded = summary({
    name: 'ics-water-plant',
    vms: {
      ...summary().vms,
      total: 31,
      running: 27,
      failing: 6,
      notrunning: 3,
      external: 1,
    },
    down_vms: ['plc-07', 'rtu-02', 'rtu-04'],
    hosts: 30,
    hosts_with_errors: 6,
    checks: { total: 203, passing: 188, failing: 15 },
    failing_checks: [
      {
        host: 'scada-srv',
        check: 'reachability',
        target: 'rtu-03 (10.20.2.13)',
        error: 'no ICMP reply',
        time: '2026-09-28T14:03:40Z',
      },
    ],
  });
  const all = [
    degraded,
    summary(),
    summary({ name: 'red-team-range', running: false, status: 'starting' }),
    summary({ name: 'dns-lab', configured: false, running: false }),
  ];

  it('counts experiments per filter', () => {
    expect(filterCounts(all)).toEqual({
      all: 4,
      attention: 1,
      healthy: 1,
      nodata: 2,
    });
  });

  it('sums the line above the table over running experiments', () => {
    expect(overview(all)).toEqual({
      active: 2,
      total: 4,
      hostsFailing: 6,
      vmsDown: 3,
    });
  });

  it('splits the VM bar into the non-empty states', () => {
    expect(vmSegments(degraded).map(({ key, count }) => [key, count])).toEqual([
      ['ok', 21],
      ['failing', 6],
      ['down', 3],
      ['external', 1],
    ]);
  });

  it('lists failing checks, then down VMs', () => {
    const list = problems(degraded);
    expect(list.map((p) => [p.host, p.check])).toEqual([
      ['scada-srv', 'reachability'],
      ['plc-07', 'vm'],
      ['rtu-02', 'vm'],
      ['rtu-04', 'vm'],
    ]);
    // counts the failing checks the server left out of its list
    expect(problemCount(degraded)).toBe(18);
    expect(problemCount(summary({ running: false }))).toBe(0);
  });

  it('takes the trend from the newest runs', () => {
    const history = Array.from({ length: 20 }, (_, i) => ({ failing: i }));
    expect(trend(summary({ history }), 3)).toEqual([17, 18, 19]);
    expect(trend(summary({ history: undefined }))).toEqual([]);
  });

  it('draws the sparkline inside its box', () => {
    expect(sparkPoints([0, 5, 10], 100, 20)).toEqual([
      [0, 18],
      [50, 10],
      [100, 2],
    ]);
    // a flat line of zeros sits at the bottom
    expect(sparkPoints([0, 0], 100, 20).map(([, y]) => y)).toEqual([18, 18]);
    expect(sparkPoints([], 100, 20)).toEqual([]);
  });
});

describe('SohOverview view', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  const view = (experiments) =>
    makeContext(SohOverview, {
      experiments,
      liveRows: createLiveRows(),
      loader: { load: vi.fn(), loading: false },
    });

  it('filters rows by verdict and name', () => {
    const ctx = view([
      summary({ name: 'a', configured: false }),
      summary({ name: 'b' }),
      summary({
        name: 'c',
        checks: { total: 2, passing: 1, failing: 1 },
        hosts_with_errors: 1,
      }),
    ]);

    ctx.filter = 'attention';
    expect(ctx.rows.map((r) => r.name)).toEqual(['c']);

    ctx.filter = 'nodata';
    expect(ctx.rows.map((r) => r.name)).toEqual(['a']);

    ctx.filter = 'all';
    ctx.searchName = 'B';
    expect(ctx.rows.map((r) => r.name)).toEqual(['b']);
  });

  it('says why the table is empty', () => {
    const ctx = view([]);
    ctx.loaded = true;
    // an empty cached copy still reads as loading until this visit's load
    expect(ctx.emptyText).toBe('Loading state of health…');
    pageStatus.fresh = true;
    expect(ctx.emptyText).toBe('No experiments found');
    pageStatus.fresh = false;

    ctx.experiments = [summary()];
    ctx.filter = 'attention';
    expect(ctx.emptyText).toBe('No experiments in Needs attention');
  });

  it('sorts the SoH column worst first', () => {
    const ctx = view([]);
    const rows = [
      summary({ name: 'healthy' }),
      summary({ name: 'none', configured: false }),
      summary({ name: 'down', vms: { ...summary().vms, notrunning: 1 } }),
    ];
    rows.sort((a, b) => ctx.sortByVerdict(a, b, true));
    expect(rows.map((r) => r.name)).toEqual(['down', 'healthy', 'none']);
  });

  it('runs SOH only when it can and says why not', () => {
    const ctx = view([]);
    const row = summary();
    axios.post.mockResolvedValue({});

    expect(ctx.runBlocked(row)).toBe('');
    ctx.runSoh(row);
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/ot-substation/trigger',
      null,
      { params: { apps: 'soh' } },
    );
    expect(row.pending).toBe(true);
    expect(ctx.runBlocked(row)).toBe('SOH is running');
    ctx.settle(row);

    expect(ctx.runBlocked(summary({ configured: false }))).toMatch(
      /not in the scenario/,
    );
    expect(ctx.runBlocked(summary({ running: false }))).toMatch(/not running/);
    expect(ctx.runBlocked(summary({ soh_initialized: false }))).toMatch(
      /first checks/,
    );

    allowed.fn = (resource) => resource !== 'experiments/trigger';
    expect(ctx.runBlocked(summary())).toMatch(/may not run SOH/);
    axios.post.mockClear();
    ctx.runSoh(summary());
    expect(axios.post).not.toHaveBeenCalled();
  });

  it('marks a row running and reloads when its soh run finishes', () => {
    vi.useFakeTimers();
    const row = summary();
    const ctx = view([row]);

    ctx.handleWs({
      resource: { type: 'apps/soh', name: 'ot-substation', action: 'start' },
    });
    expect(row.soh_running).toBe(true);

    ctx.handleWs({
      resource: { type: 'apps/soh', name: 'ot-substation', action: 'error' },
      result: { error: 'boom' },
    });
    expect(notify.showError).toHaveBeenCalledWith(
      'State of health run failed for ot-substation',
      'boom',
    );

    // several updates, one reload
    ctx.handleWs({
      resource: { type: 'apps/soh', name: 'ot-substation', action: 'success' },
    });
    vi.advanceTimersByTime(RELOAD_DELAY_MS);
    expect(ctx.loader.load).toHaveBeenCalledTimes(1);
  });

  it('reloads for VM state changes of running experiments only', () => {
    vi.useFakeTimers();
    const ctx = view([
      summary({ name: 'up' }),
      summary({ name: 'down', running: false, status: 'stopped' }),
    ]);
    const vm = (name, action) => ({
      resource: { type: 'experiment/vm', name, action },
    });

    ctx.handleWs(vm('down/vm1', 'start'));
    ctx.handleWs(vm('up/vm1', 'cdrom-inserted'));
    vi.advanceTimersByTime(RELOAD_DELAY_MS);
    expect(ctx.loader.load).not.toHaveBeenCalled();

    ctx.handleWs(vm('up/vm1', 'stop'));
    vi.advanceTimersByTime(RELOAD_DELAY_MS);
    expect(ctx.loader.load).toHaveBeenCalledTimes(1);
  });

  it.each([
    ['polls a running SOH run while the page is in view', false, true, 1],
    ['skips a poll while its tab is hidden', true, true, 0],
    ['skips a poll while its window is out of focus', false, false, 0],
  ])('%s', (_, hidden, focused, loads) => {
    vi.stubGlobal('document', { hidden, hasFocus: () => focused });
    const ctx = view([summary({ soh_running: true })]);

    ctx.pollRunning();
    expect(ctx.loader.load).toHaveBeenCalledTimes(loads);
  });

  it.each([
    ['skips a poll with no run going', {}, false, 0],
    ['polls a run just asked for', { pending: true }, false, 1],
    ['skips a poll while a load is in flight', { soh_running: true }, true, 0],
  ])('%s', (_, run, loading, loads) => {
    vi.stubGlobal('document', { hidden: false, hasFocus: () => true });
    const ctx = view([summary(run)]);
    ctx.loader.loading = loading;

    ctx.pollRunning();
    expect(ctx.loader.load).toHaveBeenCalledTimes(loads);
  });

  it('shows experiment start progress without reloading', () => {
    const row = summary({ running: false, status: 'stopped' });
    const ctx = view([row]);
    const exp = (action, result) => ({
      resource: { type: 'experiment', name: 'ot-substation', action },
      result,
    });

    ctx.handleWs(exp('starting'));
    expect(row.status).toBe('starting');
    ctx.handleWs(exp('progress', { percent: 0.4 }));
    expect(row.percent).toBe(0.4);
    expect(ctx.loader.load).not.toHaveBeenCalled();
  });

  it('lists the first problems until asked for all', () => {
    const failing_checks = Array.from({ length: 8 }, (_, i) => ({
      host: `h${i}`,
      check: 'process',
      error: 'missing',
      time: '2026-09-28T14:00:00Z',
    }));
    const row = summary({
      failing_checks,
      checks: { total: 20, passing: 12, failing: 8 },
    });
    const ctx = view([row]);

    expect(ctx.shownProblems(row)).toHaveLength(PROBLEMS_SHOWN);
    expect(ctx.hiddenProblems(row)).toBe(8 - PROBLEMS_SHOWN);
    expect(ctx.truncated(row)).toBe(false);

    ctx.showAll[row.name] = true;
    expect(ctx.shownProblems(row)).toHaveLength(8);
    expect(ctx.hiddenProblems(row)).toBe(0);

    row.checks.failing = 60;
    expect(ctx.truncated(row)).toBe(true);
  });

  it('describes the VM bar for rows without VM states', () => {
    const ctx = view([]);
    expect(
      ctx.healthCaption(summary({ running: false, status: 'stopped' })),
    ).toBe('24 VMs, not running');
    expect(
      ctx.healthCaption(summary({ vms: { known: false, total: 1 } })),
    ).toBe('1 VM, states unavailable');
  });
});

// The page rendered with the real Buefy components, showing its cached copy
// while the fresh load never answers.
describe('rendering', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    axios.get.mockReturnValue(new Promise(() => {}));
  });
  afterEach(() => {
    vi.useRealTimers();
    clearPageCache();
  });

  const experiments = () => [
    summary({
      name: 'ics-water-plant',
      vms: {
        ...summary().vms,
        total: 4,
        running: 3,
        failing: 1,
        notrunning: 1,
      },
      down_vms: ['vm-4'],
      hosts: 3,
      hosts_with_errors: 1,
      checks: { total: 10, passing: 9, failing: 1 },
      reachability: { total: 4, passing: 4, failing: 0 },
      history: [{ failing: 0 }, { failing: 1 }],
    }),
    summary(),
    { name: 'dns-lab', configured: false, running: false, vms: { total: 6 } },
  ];

  const render = (options) =>
    renderSSR(SohOverview, {}, { route: '/soh', ...options });

  it('shows the cached summary line and filter counts at once', async () => {
    cachePage('soh', experiments());

    const html = await render();
    const text = textOf(html);

    expect(text).toContain(
      'SOH active on 2 of 3 experiments · 1 host failing checks · 1 VM down',
    );
    expect(text).toMatch(/All 3 Needs attention 1 Healthy 1 No SOH data 1/);
    expect(html).toContain('class="attn count"');
    expect(html).toContain('placeholder="Find an Experiment"');
    expect(text).toContain('External / HIL');
  });

  it('shows no summary line before the first load', async () => {
    const html = await render();
    expect(html).not.toContain('SOH active on');
    expect(html).not.toContain('class="legend"');
  });

  it('draws a row per experiment with its health', async () => {
    cachePage('soh', experiments());

    const html = await render({ tables: true });
    const rows = tableRows(html).map((cells) => {
      const [run] = tooltipButtons(cells.Actions);
      return {
        link: cells.name.match(/href="([^"]*)"/)[1],
        name: textOf(cells.name),
        verdict: textOf(cells.verdict),
        checks: textOf(cells.checks),
        run: run.disabled ? 'blocked' : run.label,
      };
    });

    expect(rows).toEqual([
      {
        link: '/experiment/ics-water-plant',
        name: 'ics-water-plant soh-baseline · 4 VMs',
        verdict: 'degraded',
        checks: '9 / 10 1 failing',
        run: 'Run SOH on ics-water-plant',
      },
      {
        link: '/experiment/ot-substation',
        name: 'ot-substation soh-baseline · 24 VMs',
        verdict: 'healthy',
        checks: '212 / 212 all passing',
        run: 'Run SOH on ot-substation',
      },
      {
        link: '/experiment/dns-lab',
        name: 'dns-lab no scenario · 6 VMs',
        verdict: 'not configured',
        checks: '—',
        run: 'blocked',
      },
    ]);
  });
});
