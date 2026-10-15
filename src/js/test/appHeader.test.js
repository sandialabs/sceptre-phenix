// The header: a link to each page the signed-in role may open, the current
// page's load state beside the button that reloads it, and the background
// jobs menu.
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const store = vi.hoisted(() => ({
  auth: true,
  role: null,
  token: 't',
  features: [],
}));
vi.mock('@/store.js', () => ({ usePhenixStore: () => store }));
vi.mock('@/utils/axios.js', () => ({ default: { get: vi.fn() } }));

import AppHeader from '@/components/AppHeader.vue';
import RefreshStatus from '@/components/RefreshStatus.vue';
import { useJobStore } from '@/utils/jobs.js';
import { pageStatus } from '@/utils/pageLoader.js';
import { makeContext } from './helpers/context.js';
import { renderSSR, textOf, tooltipButtons } from './helpers/render.js';
import { role } from './helpers/roles.js';

const NOW = Date.parse('2026-09-30T12:00:00Z');

beforeEach(() => {
  setActivePinia(createPinia());
  // the refresh status's clock, and the timer it keeps it current with
  vi.useFakeTimers({ now: NOW });
  Object.assign(store, { auth: true, role: null, features: [] });
  Object.assign(pageStatus, {
    refresh: null,
    loading: false,
    updatedAt: null,
    observedAt: null,
    failed: false,
    stale: false,
    staleReason: null,
    resource: null,
  });
});

afterEach(() => vi.useRealTimers());

// the text of each link in the header's menu, in order
function links(html) {
  const menu = html.slice(
    html.indexOf('navbar-start'),
    html.indexOf('navbar-end'),
  );
  return [...menu.matchAll(/<a\b[^>]*>([\s\S]*?)<\/a>/g)].map(([, text]) =>
    textOf(text),
  );
}

describe('the menu', () => {
  const admin = role('Global Admin', ['*', '*'], ['*/*', '*']);
  const viewer = role(
    'Viewer',
    ['experiments', 'list'],
    ['logs', 'list'],
    ['settings', 'get'],
  );

  it.each([
    [
      'links every page for a role allowed everything, with every feature',
      admin,
      ['webshark', 'tunneler-download'],
      [
        'Experiments',
        'Configs',
        'Disks',
        'Hosts',
        'Users',
        'Logs',
        'Scorch',
        'State of Health',
        'Builder',
        'Console',
        'WebShark',
        'Tunneler',
        'Settings',
      ],
    ],
    [
      'leaves out the pages of features the server does not have',
      admin,
      [],
      [
        'Experiments',
        'Configs',
        'Disks',
        'Hosts',
        'Users',
        'Logs',
        'Scorch',
        'State of Health',
        'Builder',
        'Console',
        'Settings',
      ],
    ],
    [
      'links only the pages a role may open, as the router allows them',
      viewer,
      ['webshark'],
      ['Experiments', 'Users', 'Scorch', 'State of Health'],
    ],
    [
      'links the Builder and Logs for the roles those pages need',
      role('Builder', ['configs', 'list'], ['logs', 'get']),
      [],
      ['Configs', 'Users', 'Logs', 'Builder'],
    ],
    [
      'links WebShark for a role that may open captures when it is installed',
      role('Captures', ['vms/captures', 'list']),
      ['webshark'],
      ['Users', 'WebShark'],
    ],
  ])('%s', async (_, signedIn, features, want) => {
    Object.assign(store, { role: signedIn, features });
    expect(links(await renderSSR(AppHeader))).toEqual(want);
  });

  it('offers only the experiments to no one signed in', async () => {
    Object.assign(store, { auth: false, role: admin });
    const html = await renderSSR(AppHeader);
    expect(links(html)).toEqual(['Experiments']);
    expect(textOf(html)).not.toContain('Logout');
  });
});

describe('the refresh status', () => {
  const minute = 60 * 1000;

  it.each([
    [
      'says the first load is running and spins',
      { loading: true },
      { label: 'Loading…', state: 'loading', disabled: true, spins: true },
    ],
    [
      'says a reload is running over data already shown, and its age',
      { loading: true, updatedAt: NOW - 5 * minute },
      {
        label: 'Refreshing… (data 5 min old)',
        state: 'loading',
        disabled: true,
        spins: true,
      },
    ],
    [
      'says a reload is running over data read moments ago',
      { loading: true, updatedAt: NOW - 20 * 1000 },
      { label: 'Refreshing…', state: 'loading', disabled: true, spins: true },
    ],
    [
      'measures the age from when the server read the data',
      { updatedAt: NOW - 30 * 1000, observedAt: NOW - 3 * minute },
      {
        label: 'Updated 3 min ago',
        state: 'ok',
        disabled: false,
        spins: false,
      },
    ],
    [
      'says the server served earlier data',
      {
        updatedAt: NOW,
        observedAt: NOW - 3 * minute,
        stale: true,
        staleReason: 'server',
      },
      {
        label: 'Updated 3 min ago · may be out of date',
        state: 'stale',
        disabled: false,
        spins: false,
        reason: 'minimega was busy; showing earlier data',
      },
    ],
    [
      'says data older than its bound may be out of date',
      { updatedAt: NOW - 2 * minute, resource: 'hosts' },
      {
        label: 'Updated 2 min ago · may be out of date',
        state: 'stale',
        disabled: false,
        spins: false,
        reason: 'older than 90 s',
      },
    ],
    [
      'shows a reload after a failure as loading',
      { loading: true, failed: true },
      { label: 'Loading…', state: 'loading', disabled: true, spins: true },
    ],
    [
      'says the data was just updated',
      { updatedAt: NOW - 30 * 1000 },
      { label: 'Updated just now', state: 'ok', disabled: false, spins: false },
    ],
    [
      'says how many minutes ago the data was updated',
      { updatedAt: NOW - 5 * minute },
      {
        label: 'Updated 5 min ago',
        state: 'ok',
        disabled: false,
        spins: false,
      },
    ],
    [
      'gives the time of an update over an hour ago',
      { updatedAt: NOW - 90 * minute },
      {
        label: `Updated ${new Date(NOW - 90 * minute).toLocaleTimeString()}`,
        state: 'ok',
        disabled: false,
        spins: false,
      },
    ],
    [
      'says a reload failed',
      { failed: true, updatedAt: NOW - 5 * minute },
      {
        label: 'Refresh failed',
        state: 'failed',
        disabled: false,
        spins: false,
      },
    ],
    [
      'is plain before anything has loaded',
      {},
      { label: '', state: 'idle', disabled: false, spins: false },
    ],
  ])('%s', async (_, status, want) => {
    store.role = role('Viewer', ['experiments', 'list']);
    Object.assign(pageStatus, { refresh: () => {}, ...status });
    const html = await renderSSR(AppHeader);

    const label = html.match(
      /<span[^>]*class="[^"]*refresh-label[^"]*"[^>]*>([^<]*)</,
    )[1];
    const reason = html.match(/<span class="is-sr-only"[^>]*>: ([^<]*)</)?.[1];
    const [button] = tooltipButtons(html);
    expect({
      label,
      state: button.attrs.match(
        /refresh-(loading|ok|failed|idle|stale)\b/,
      )?.[1],
      disabled: button.disabled,
      spins: /data-icon="sync-alt" class="fa-spin"/.test(html),
      ...(reason && { reason }),
    }).toEqual(want);
    expect(button).toMatchObject({
      tooltip: "Refresh this page's data",
      label: "Refresh this page's data",
    });
  });

  it.each([
    ['nothing while a reload runs', { loading: true, updatedAt: NOW }, ''],
    ['nothing once data loads', { updatedAt: NOW - 5 * minute }, ''],
    ['a failed reload', { failed: true, updatedAt: NOW }, 'Refresh failed'],
    [
      'data that may be out of date, and why',
      { updatedAt: NOW - 2 * minute, resource: 'hosts' },
      'Data may be out of date: older than 90 s',
    ],
  ])('announces %s', async (_, status, want) => {
    store.role = role('Viewer', ['experiments', 'list']);
    Object.assign(pageStatus, { refresh: () => {}, ...status });
    const html = await renderSSR(AppHeader);

    // the only live region is the one for problems, so polls stay quiet
    expect(html.match(/aria-live=/g)).toHaveLength(1);
    expect(
      html.match(/class="refresh-alert is-sr-only"[^>]*>([^<]*)</)[1],
    ).toBe(want);
  });

  it.each([
    ['a failed reload', { failed: true, updatedAt: NOW }, 'Refresh failed'],
    [
      'data that stays out of date',
      { updatedAt: NOW - 2 * minute, resource: 'hosts' },
      'Data may be out of date: older than 90 s',
    ],
  ])(
    'keeps announcing %s through each poll, so it is not read out again',
    (_, status, want) => {
      Object.assign(pageStatus, { refresh: () => {}, ...status });
      const ctx = makeContext(RefreshStatus);
      // the alert after the page status changes, as its watcher leaves it
      const after = (change) => {
        Object.assign(pageStatus, change);
        RefreshStatus.watch.problem?.handler.call(ctx, ctx.problem);
        return ctx.alert;
      };

      expect(after({})).toBe(want);
      expect(after({ loading: true })).toBe(want);
      expect(after({ loading: false })).toBe(want);
      expect(after({ loading: true })).toBe(want);
      // a reload that fixes it clears the announcement
      expect(after({ loading: false, failed: false, updatedAt: NOW })).toBe('');
    },
  );

  it('is left out on a page with nothing to reload, and when signed out', async () => {
    store.role = role('Viewer', ['experiments', 'list']);
    expect(await renderSSR(AppHeader)).not.toContain('refresh-status');

    Object.assign(pageStatus, { refresh: () => {}, updatedAt: NOW });
    store.auth = false;
    expect(await renderSSR(AppHeader)).not.toContain('refresh-status');
  });
});

describe('the jobs menu', () => {
  const viewer = role('Viewer', ['experiments', 'list']);
  const job = (id, status) => ({
    id,
    title: `Job ${id}`,
    kind: 'vms.action',
    experiment: 'exp',
    status,
    progress: { done: 0, total: 1 },
    items: [],
    created: `2026-10-14T00:00:0${id}Z`,
    revision: 1,
  });

  it('counts the running jobs in its accessible name', async () => {
    store.role = viewer;
    const jobs = useJobStore();
    jobs.put(job('1', 'running'));
    jobs.put(job('2', 'queued'));
    jobs.put(job('3', 'succeeded'));

    const html = await renderSSR(AppHeader);
    expect(html).toContain('aria-label="Background jobs (2 jobs running)"');
    expect(html).toMatch(/class="tag is-info is-rounded jobs-count"[^>]*>2</);
  });

  it('is a disclosure whose buttons and links assistive technology reaches', async () => {
    store.role = viewer;
    useJobStore().put(job('1', 'running'));

    const html = await renderSSR(AppHeader);
    // no menu, whose items' children would be presentational
    expect(html).not.toMatch(/role="menu(item)?"/);
    expect(html).toContain('aria-expanded="false"');
    expect(html).toContain('aria-controls="jobs-panel"');
    expect(html).toMatch(
      /id="jobs-panel"[^>]*role="region"|role="region"[^>]*id="jobs-panel"/,
    );
  });

  it('says when there are none, and is not shown to no one signed in', async () => {
    store.role = viewer;
    expect(await renderSSR(AppHeader)).toContain(
      'aria-label="Background jobs (0 jobs running)"',
    );

    Object.assign(store, { auth: false });
    expect(await renderSSR(AppHeader)).not.toContain('Background jobs');
  });
});
