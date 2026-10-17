// The State of Health overview: how it judges each experiment's health, the
// line and filters above its table, and the page itself.
import { flushPromises } from '@vue/test-utils';
import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, capturing, loaderSpy, modules, notify, rbac, resetMocks, ws } =
  await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
// the page shows the summaries it is given; its loads are recorded
vi.mock('@/utils/pageLoader.js', capturing);

import {
  FILTERS,
  VERDICTS,
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
import { pageStatus } from '@/utils/pageLoader.js';
import SohOverview, {
  PROBLEMS_SHOWN,
  RELOAD_DELAY_MS,
} from '@/views/SohOverview.vue';
import { wsMsg } from './helpers/fixtures.js';
import {
  mountComponent,
  tableRows,
  textOf,
  tooltipButtons,
} from './helpers/mount.js';
import { denyOnly } from './helpers/permissions.js';

// every test starts with a user who may do anything
beforeEach(() => resetMocks({ store: { role: { name: 'Global Admin' } } }));

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

  it('is checks failing for a few failing hosts, degraded from a fifth of them', () => {
    const at = (failingHosts) =>
      summary({
        hosts: 10,
        hosts_with_errors: failingHosts,
        checks: { total: 100, passing: 90, failing: 10 },
      });
    expect(verdict(at(1))).toBe('failing');
    expect(verdictLabel(at(1))).toBe('checks failing');
    expect(verdict(at(2))).toBe('degraded');
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

// usually `value`, now and then one of `other`
const often = (value, other) =>
  fc.oneof(
    { weight: 4, arbitrary: fc.constant(value) },
    { weight: 1, arbitrary: other },
  );
const countsArb = fc
  .record({ total: fc.nat(300), failing: often(0, fc.nat(300)) })
  .map(({ total, failing }) => {
    const bad = Math.min(failing, total);
    return { total, passing: total - bad, failing: bad };
  });
const vmsArb = fc
  .record({
    known: often(true, fc.constant(false)),
    running: fc.nat(30),
    failing: fc.nat(30),
    notrunning: often(0, fc.nat(3)),
    notdeploy: often(0, fc.nat(3)),
    notboot: fc.nat(3),
    external: fc.nat(3),
    delayed: fc.nat(3),
  })
  .map((v) => ({
    ...v,
    failing: Math.min(v.failing, v.running),
    total:
      v.running +
      v.notrunning +
      v.notdeploy +
      v.notboot +
      v.external +
      v.delayed,
  }));
// a summary as GET /soh gives it, mostly of a running experiment with results
const summaryArb = fc
  .record({
    configured: often(true, fc.constant(false)),
    up: often(
      ['started', true],
      fc.constantFrom(['stopped', false], ['starting', false]),
    ),
    soh_initialized: often(true, fc.constant(false)),
    soh_running: fc.boolean(),
    vms: vmsArb,
    hosts: fc.nat(30),
    hosts_with_errors: often(0, fc.nat(30)),
    checks: countsArb,
    reachability: countsArb,
  })
  .map(({ up: [status, running], hosts, hosts_with_errors, ...rest }) =>
    summary({
      ...rest,
      status,
      running,
      hosts,
      hosts_with_errors: Math.min(hosts, hosts_with_errors),
    }),
  );
const rank = (s) => VERDICTS[verdict(s)].rank;

describe('any summary', () => {
  it('never reads better for a VM down, a host failing or a check failing', () => {
    const worse = [
      (s) => ({ ...s, vms: { ...s.vms, notrunning: s.vms.notrunning + 1 } }),
      (s) => ({
        ...s,
        hosts: s.hosts + 1,
        hosts_with_errors: s.hosts_with_errors + 1,
      }),
      (s) => ({
        ...s,
        checks: {
          ...s.checks,
          total: s.checks.total + 1,
          failing: s.checks.failing + 1,
        },
      }),
      (s) => ({
        ...s,
        reachability: {
          ...s.reachability,
          total: s.reachability.total + 1,
          failing: s.reachability.failing + 1,
        },
      }),
    ];
    fc.assert(
      fc.property(summaryArb, fc.constantFrom(...worse), (s, worsen) => {
        expect(rank(worsen(s))).toBeLessThanOrEqual(rank(s));
        if (verdict(s) === 'healthy') {
          expect(s.checks.failing + vmsDown(s)).toBe(0);
        }
      }),
    );
  });

  it('puts every experiment in one filter, and sums the line over running ones', () => {
    fc.assert(
      fc.property(fc.array(summaryArb, { maxLength: 8 }), (list) => {
        const counts = filterCounts(list);
        expect(counts.all).toBe(list.length);
        expect(counts.attention + counts.healthy + counts.nodata).toBe(
          list.length,
        );
        const line = overview(list);
        const running = list.filter((s) => s.running);
        expect(line.total).toBe(list.length);
        expect(line.active).toBeLessThanOrEqual(running.length);
        // the problems the rows count are the failing checks and down VMs
        const failing = running.reduce((n, s) => n + s.checks.failing, 0);
        expect(line.vmsDown + failing).toBe(
          list.reduce((n, s) => n + problemCount(s), 0),
        );
        expect(line.hostsFailing).toBeLessThanOrEqual(
          running.reduce((n, s) => n + s.hosts, 0),
        );
      }),
    );
  });

  it('splits the VM bar into positive segments that add up to the VMs counted', () => {
    const order = ['ok', 'failing', 'down', 'delayed', 'notboot', 'external'];
    fc.assert(
      fc.property(summaryArb, (s) => {
        const segments = vmSegments(s);
        if (!s.running || !s.vms.known) {
          expect(segments).toEqual([]);
          return;
        }
        expect(segments.every((g) => g.count > 0)).toBe(true);
        const keys = segments.map((g) => g.key);
        expect(keys).toEqual(order.filter((k) => keys.includes(k)));
        const { total, notboot, external, delayed } = s.vms;
        expect(segments.reduce((n, g) => n + g.count, 0)).toBe(total);
        expect(total - notboot - external - delayed).toBe(
          s.vms.running + vmsDown(s),
        );
      }),
    );
  });

  it('draws any sparkline inside its box, higher for more failures', () => {
    const box = fc.record({
      width: fc.integer({ min: 10, max: 400 }),
      height: fc.integer({ min: 10, max: 100 }),
    });
    fc.assert(
      fc.property(
        fc.array(fc.nat(500), { minLength: 1 }),
        box,
        (values, { width, height }) => {
          const points = sparkPoints(values, width, height);
          expect(points).toHaveLength(values.length);
          points.forEach(([x, y], i) => {
            expect(x).toBeGreaterThanOrEqual(0);
            expect(x).toBeLessThanOrEqual(width);
            expect(y).toBeGreaterThanOrEqual(2);
            expect(y).toBeLessThanOrEqual(height - 2);
            if (i > 0) expect(x).toBeGreaterThan(points[i - 1][0]);
            for (let j = 0; j < i; j++) {
              if (values[j] < values[i])
                expect(y).toBeLessThanOrEqual(points[j][1]);
            }
          });
          if (values.length > 1) expect(points.at(-1)[0]).toBe(width);
        },
      ),
    );
  });

  it('takes the trend from the newest runs, oldest first', () => {
    fc.assert(
      fc.property(
        fc.array(fc.nat(50)),
        fc.integer({ min: 1, max: 30 }),
        (failing, count) => {
          const history = failing.map((n) => ({ failing: n }));
          const out = trend(summary({ history }), count);
          expect(out).toHaveLength(Math.min(count, failing.length));
          if (out.length)
            expect(failing.slice(failing.length - out.length)).toEqual(out);
        },
      ),
    );
  });
});

// the page showing these summaries, as loaded unless `data` says otherwise
const page = (experiments, data) =>
  mountComponent(SohOverview, {
    route: '/soh',
    data: { experiments, loaded: true, ...data },
  });

// the experiment each row names, in order
const names = (wrapper) =>
  tableRows(wrapper).map((cells) => textOf(cells.Experiment.find('a')));

const filterButton = (wrapper, label) =>
  wrapper.findAll('.filters button').find((b) => textOf(b).startsWith(label));

const publish = (msg) => {
  for (const handler of ws.handlers) handler(msg);
  return flushPromises();
};

// the Run SOH button of each row: its label, and whether it is disabled or spins
const runButtons = (wrapper) =>
  tableRows(wrapper).map((cells) => {
    const [run] = tooltipButtons(cells.Actions);
    return { ...run, spinning: run.control.classes('is-loading') };
  });

describe('SohOverview view', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    pageStatus.fresh = false;
  });

  it('filters rows by verdict and name', async () => {
    const wrapper = await page([
      summary({ name: 'a', configured: false }),
      summary({ name: 'b' }),
      summary({
        name: 'c',
        checks: { total: 2, passing: 1, failing: 1 },
        hosts_with_errors: 1,
      }),
    ]);

    await filterButton(wrapper, 'Needs attention').trigger('click');
    expect(names(wrapper)).toEqual(['c']);

    await filterButton(wrapper, 'No SOH data').trigger('click');
    expect(names(wrapper)).toEqual(['a']);

    await filterButton(wrapper, 'All').trigger('click');
    await wrapper.find('input[placeholder="Find an Experiment"]').setValue('B');
    expect(names(wrapper)).toEqual(['b']);
  });

  it('sorts any rows worst first and shows each filter its share', async () => {
    const byName = (rows, name) => rows.find((r) => r.name === name);
    await fc.assert(
      fc.asyncProperty(fc.array(summaryArb, { maxLength: 6 }), async (list) => {
        const rows = list.map((s, i) => ({ ...s, name: `exp-${i}` }));
        const wrapper = await page(rows);
        try {
          const shown = names(wrapper);
          expect([...shown].sort()).toEqual(rows.map((r) => r.name));
          const ranks = shown.map((name) => rank(byName(rows, name)));
          expect(ranks).toEqual([...ranks].sort((a, b) => a - b));

          const counts = filterCounts(rows);
          for (const filter of FILTERS) {
            await filterButton(wrapper, filter.label).trigger('click');
            const kept = names(wrapper);
            expect(kept).toHaveLength(counts[filter.key]);
            for (const name of kept) {
              const key = verdict(byName(rows, name));
              expect(filter.verdicts ?? [key]).toContain(key);
            }
          }
        } finally {
          wrapper.unmount();
        }
      }),
      { numRuns: 15 },
    );
  });

  it('says why the table is empty', async () => {
    const empty = await page([]);
    const emptyText = (wrapper) => textOf(wrapper.find('tbody'));
    // an empty cached copy still reads as loading until this visit's load
    expect(emptyText(empty)).toBe('Loading state of health…');
    pageStatus.fresh = true;
    await flushPromises();
    expect(emptyText(empty)).toBe('No experiments found');
    pageStatus.fresh = false;

    const healthy = await page([summary()]);
    await filterButton(healthy, 'Needs attention').trigger('click');
    expect(emptyText(healthy)).toBe('No experiments in Needs attention');
  });

  it('sorts the SoH column worst first', async () => {
    const wrapper = await page([
      summary({ name: 'healthy' }),
      summary({ name: 'none', configured: false }),
      summary({ name: 'down', vms: { ...summary().vms, notrunning: 1 } }),
    ]);
    expect(names(wrapper)).toEqual(['down', 'healthy', 'none']);
  });

  it('runs SOH only when it can and says why not', async () => {
    axios.post.mockResolvedValue({});
    const wrapper = await page([summary()]);
    const [run] = runButtons(wrapper);

    expect(run).toMatchObject({
      label: 'Run SOH on ot-substation',
      disabled: false,
    });
    await run.control.trigger('click');
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/ot-substation/trigger',
      null,
      { params: { apps: 'soh' } },
    );
    expect(runButtons(wrapper)).toMatchObject([
      { label: 'SOH is running', disabled: true, spinning: true },
    ]);

    const blocked = await page([
      summary({ name: 'a', configured: false }),
      summary({ name: 'b', running: false }),
      summary({ name: 'c', soh_initialized: false }),
    ]);
    // sorted waiting first, then not configured
    expect(runButtons(blocked).map((b) => [b.label, b.disabled])).toEqual([
      ['The experiment is not running', true],
      ['SOH is still running its first checks', true],
      ['The soh app is not in the scenario', true],
    ]);

    rbac.allowed = denyOnly(['experiments/trigger']);
    axios.post.mockClear();
    const denied = await page([summary()]);
    const [refused] = runButtons(denied);
    expect(refused).toMatchObject({ disabled: true });
    expect(refused.label).toMatch(/may not run SOH/);
    // the click handler refuses too, were the button enabled
    refused.control.element.removeAttribute('disabled');
    await refused.control.trigger('click');
    expect(axios.post).not.toHaveBeenCalled();
  });

  it('marks a row running and reloads when its soh run finishes', async () => {
    vi.useFakeTimers();
    const wrapper = await page([summary()]);
    const lastCheck = () => textOf(tableRows(wrapper)[0]['Last Check']);

    await publish(wsMsg('apps/soh', 'ot-substation', 'start'));
    expect(lastCheck()).toContain('running now…');

    await publish(
      wsMsg('apps/soh', 'ot-substation', 'error', { error: 'boom' }),
    );
    expect(notify.show).toHaveBeenCalledWith(
      'State of health run failed for ot-substation',
      'boom',
    );

    // several updates, one reload
    await publish(wsMsg('apps/soh', 'ot-substation', 'success'));
    vi.advanceTimersByTime(RELOAD_DELAY_MS);
    expect(loaderSpy.load).toHaveBeenCalledTimes(1);
  });

  it('reloads for VM state changes of running experiments only', async () => {
    vi.useFakeTimers();
    await page([
      summary({ name: 'up' }),
      summary({ name: 'down', running: false, status: 'stopped' }),
    ]);
    const vm = (name, action) => wsMsg('experiment/vm', name, action);

    await publish(vm('down/vm1', 'start'));
    await publish(vm('up/vm1', 'cdrom-inserted'));
    vi.advanceTimersByTime(RELOAD_DELAY_MS);
    expect(loaderSpy.load).not.toHaveBeenCalled();

    await publish(vm('up/vm1', 'stop'));
    vi.advanceTimersByTime(RELOAD_DELAY_MS);
    expect(loaderSpy.load).toHaveBeenCalledTimes(1);
  });

  // RUNNING_POLL_MS in SohOverview.vue
  const POLL_MS = 15000;

  // how often the page reloads over one poll interval
  async function polls(
    run,
    { hidden = false, focused = true, loading = false } = {},
  ) {
    vi.useFakeTimers();
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(hidden);
    vi.spyOn(document, 'hasFocus').mockReturnValue(focused);
    await page([summary(run)]);
    loaderSpy.loading = loading;

    vi.advanceTimersByTime(POLL_MS);
    return loaderSpy.load.mock.calls.length;
  }

  it.each([
    ['polls a running SOH run while the page is in view', {}, 1],
    ['skips a poll while its tab is hidden', { hidden: true }, 0],
    ['skips a poll while its window is out of focus', { focused: false }, 0],
  ])('%s', async (_, view, loads) => {
    expect(await polls({ soh_running: true }, view)).toBe(loads);
  });

  it.each([
    ['skips a poll with no run going', {}, false, 0],
    ['polls a run just asked for', { pending: true }, false, 1],
    ['skips a poll while a load is in flight', { soh_running: true }, true, 0],
  ])('%s', async (_, run, loading, loads) => {
    expect(await polls(run, { loading })).toBe(loads);
  });

  it('shows experiment start progress without reloading', async () => {
    const wrapper = await page([
      summary({ running: false, status: 'stopped' }),
    ]);
    const status = () => tableRows(wrapper)[0]['Experiment Status'];
    const exp = (action, result) =>
      wsMsg('experiment', 'ot-substation', action, result);

    await publish(exp('starting'));
    expect(status().find('progress').exists()).toBe(true);
    await publish(exp('progress', { percent: 0.4 }));
    expect(status().find('progress').element.value).toBe(40);
    expect(loaderSpy.load).not.toHaveBeenCalled();
  });

  it('lists the first problems until asked for all', async () => {
    const failing_checks = Array.from({ length: 8 }, (_, i) => ({
      host: `h${i}`,
      check: 'process',
      error: 'missing',
      time: '2026-09-28T14:00:00Z',
    }));
    const row = (name, failing) =>
      summary({
        name,
        failing_checks,
        checks: { total: 20, passing: 12, failing },
      });
    const wrapper = await page([row('a', 8), row('b', 60)], {
      opened: ['a', 'b'],
    });
    const detail = (i) => wrapper.findAll('tr.detail').at(i);
    const shown = (i) => detail(i).findAll('.problems tbody tr');
    const hint = (i) => textOf(detail(i).find('.hint'));

    expect(shown(0)).toHaveLength(PROBLEMS_SHOWN);
    expect(hint(0)).toMatch(/^Show all 8 problems ·/);
    expect(hint(0)).not.toContain('this list stops');

    await detail(0).find('.hint a').trigger('click');
    expect(shown(0)).toHaveLength(8);
    expect(hint(0)).not.toContain('Show all');

    expect(hint(1)).toContain('this list stops at the newest 8 failing checks');
  });

  it('describes the VM bar for rows without VM states', async () => {
    const wrapper = await page([
      summary({ name: 'a', running: false, status: 'stopped' }),
      summary({ name: 'b', vms: { known: false, total: 1 } }),
    ]);
    expect(
      tableRows(wrapper).map((cells) => textOf(cells['VM Health'])),
    ).toEqual(['24 VMs, not running', '1 VM, states unavailable']);
  });
});

describe('rendering', () => {
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

  it('shows the summary line and filter counts', async () => {
    const wrapper = await page(experiments());
    const text = textOf(wrapper);

    expect(text).toContain(
      'SOH active on 2 of 3 experiments · 1 host failing checks · 1 VM down',
    );
    expect(wrapper.findAll('.filters button').map(textOf)).toEqual([
      'All 3',
      'Needs attention 1',
      'Healthy 1',
      'No SOH data 1',
    ]);
    expect(wrapper.find('.count.attn').exists()).toBe(true);
    expect(
      wrapper.find('input[placeholder="Find an Experiment"]').exists(),
    ).toBe(true);
    expect(textOf(wrapper.find('.legend'))).toContain('External / HIL');
  });

  it('says when VM states may be out of date', async () => {
    const wrapper = await page([
      summary({ vms_stale: true, vms_observed_at: '2026-10-14T10:00:00Z' }),
    ]);
    const stale = wrapper.find('.vms-stale');
    expect(textOf(stale)).toBe('states may be out of date');
    expect(stale.attributes('title')).toMatch(
      /^minimega was busy; showing the VM states/,
    );
  });

  it('shows each load, keeping rows changed since the server read it', async () => {
    const wrapper = await page([], { loaded: false });
    const lastCheck = () =>
      tableRows(wrapper).map((cells) => textOf(cells['Last Check']));

    loaderSpy.options.apply(experiments(), { seq: 1 });
    await flushPromises();
    expect(textOf(wrapper)).toContain(
      'SOH active on 2 of 3 experiments · 1 host failing checks · 1 VM down',
    );
    expect(names(wrapper)).toEqual([
      'ics-water-plant',
      'ot-substation',
      'dns-lab',
    ]);

    await publish(wsMsg('apps/soh', 'ot-substation', 'start', undefined, 5));
    loaderSpy.options.apply(experiments(), { seq: 3 });
    await flushPromises();
    expect(lastCheck()[1]).toContain('running now…');
    expect(lastCheck()[0]).not.toContain('running now…');
  });

  it('shows no summary line before the first load', async () => {
    const wrapper = await page([], { loaded: false });
    expect(textOf(wrapper)).not.toContain('SOH active on');
    expect(wrapper.find('.legend').exists()).toBe(false);
  });

  it('draws a row per experiment with its health', async () => {
    const wrapper = await page(experiments());
    const rows = tableRows(wrapper).map((cells) => {
      const [run] = tooltipButtons(cells.Actions);
      return {
        link: cells.Experiment.find('a').attributes('href'),
        name: cells.Experiment.findAll('a, .sub').map(textOf).join(' '),
        verdict: textOf(cells.SoH),
        checks: cells.Checks.findAll('.big, .sub, .dash').map(textOf).join(' '),
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
