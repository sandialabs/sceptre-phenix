// The header: a link to each page the role may open, the page's load state
// beside the button that reloads it, and the background jobs menu.
import fc from 'fast-check';
import { nextTick } from 'vue';
import { flushPromises } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, notify, resetMocks, store } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);

import AppHeader from '@/components/AppHeader.vue';
import RefreshStatus from '@/components/RefreshStatus.vue';
import JobsMenu from '@/components/jobs/JobsMenu.vue';
import { setWsDown } from '@/utils/freshness.js';
import { useJobStore } from '@/utils/jobs.js';
import { pageStatus } from '@/utils/pageLoader.js';
import {
  byLabel,
  mountComponent,
  textOf,
  tooltipButtons,
} from './helpers/mount.js';
import { role } from './helpers/roles.js';

const NOW = Date.parse('2026-09-30T12:00:00Z');

beforeEach(() => {
  setActivePinia(createPinia());
  // the refresh status's clock, and the timer it keeps it current with
  vi.useFakeTimers({ now: NOW });
  resetMocks({
    store: {
      auth: true,
      role: null,
      token: 't',
      features: [],
      username: 'alice',
    },
  });
  Object.assign(pageStatus, {
    refresh: null,
    loading: false,
    updatedAt: null,
    observedAt: null,
    failed: false,
    staleReason: null,
    resource: null,
  });
});

afterEach(() => vi.useRealTimers());

const header = (options) => mountComponent(AppHeader, options);

// the text of each link in the header's menu, in order
const links = (wrapper) => wrapper.findAll('.navbar-start a').map(textOf);

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
        'Console',
        'WebShark',
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
        'Console',
        'Settings',
      ],
    ],
    [
      'links only the pages a role may open, as the router allows them',
      viewer,
      ['webshark'],
      ['Experiments', 'Scorch', 'State of Health'],
    ],
    [
      'links Configs and Logs for the roles those pages need',
      role('Configs', ['configs', 'list'], ['logs', 'get']),
      [],
      ['Configs', 'Logs'],
    ],
    [
      'links WebShark for a role that may open captures when it is installed',
      role('Captures', ['vms/captures', 'list']),
      ['webshark'],
      ['WebShark'],
    ],
    [
      'links Users for a role that may list users',
      role('Users', ['users', 'list']),
      [],
      ['Users'],
    ],
    [
      'links Users for a role that may get only the signed-in user',
      role('Self', ['users', 'get', ['alice']]),
      [],
      ['Users'],
    ],
    [
      'leaves out Users for a role that may get only other users',
      role('Other', ['users', 'get', ['bob']]),
      [],
      [],
    ],
    ['leaves out Users for a disabled account', role('Disabled'), [], []],
  ])('%s', async (_, signedIn, features, want) => {
    Object.assign(store, { role: signedIn, features });
    expect(links(await header())).toEqual(want);
  });

  it('offers only the experiments to no one signed in', async () => {
    Object.assign(store, { auth: false, role: admin });
    const signedOut = await header();
    expect(links(signedOut)).toEqual(['Experiments']);
    expect(textOf(signedOut)).not.toContain('Logout');
  });
});

describe('the Tunneler button', () => {
  const viewer = () =>
    Object.assign(store, {
      role: role('Viewer', ['experiments', 'list']),
      features: ['tunneler-download'],
    });
  const button = (wrapper) => wrapper.find('.navbar-end a.tunneler-button');

  it('links to the Tunneler page at the right of the header, not as a tab', async () => {
    viewer();
    const page = await header();

    expect(links(page)).not.toContain('Tunneler');
    const link = button(page);
    expect(link.attributes('href')).toBe('/tunneler');
    expect(link.classes()).toEqual(
      expect.arrayContaining(['button', 'is-primary', 'is-medium']),
    );
    expect(link.find('[data-icon="download"]').exists()).toBe(true);
    expect(textOf(link)).toBe('Tunneler');
    expect(link.attributes('aria-current')).toBeUndefined();
  });

  it('is marked as the current page on the Tunneler page', async () => {
    viewer();
    const page = await header({ route: '/tunneler' });
    expect(button(page).attributes('aria-current')).toBe('page');
  });

  it('closes the burger menu when followed', async () => {
    viewer();
    const page = await header();
    const menuOpen = () => page.find('.navbar-menu').classes('is-active');
    expect(menuOpen()).toBe(false);

    await page.find('.navbar-burger').trigger('click');
    expect(menuOpen()).toBe(true);
    // a click on the label, which Buefy's navbar leaves open
    await button(page).find('span:not(.icon)').trigger('click');
    await flushPromises();
    expect(menuOpen()).toBe(false);
    expect(page.router.currentRoute.value.name).toBe('tunneler');
  });

  it.each([
    ['the server has no tunneler downloads', true, []],
    ['no one is signed in', false, ['tunneler-download']],
  ])('is left out when %s', async (_, auth, features) => {
    Object.assign(store, {
      auth,
      role: role('Viewer', ['experiments', 'list']),
      features,
    });
    const page = await header();
    expect(page.find('.tunneler-button').exists()).toBe(false);
    expect(page.find('a[href="/tunneler"]').exists()).toBe(false);
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
        staleReason: 'server',
      },
      {
        label: 'Updated 3 min ago · may be out of date',
        state: 'stale',
        disabled: false,
        spins: false,
        reason: 'minimega was busy. Showing earlier data',
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
    const refresh = (await header()).find('.refresh-status');

    // the label's own text, before its screen-reader reason
    const label = refresh.find('.refresh-label');
    const reason = label.find('.is-sr-only');
    const [button] = tooltipButtons(refresh);
    expect({
      label: label.element.firstChild.textContent,
      state: button.control
        .classes()
        .find((c) => /^refresh-(loading|ok|failed|idle|stale)$/.test(c))
        ?.slice('refresh-'.length),
      disabled: button.disabled,
      spins: refresh.find('[data-icon="sync-alt"].fa-spin').exists(),
      ...(reason.exists() && { reason: textOf(reason).replace(/^: /, '') }),
    }).toEqual(want);
    expect(button).toMatchObject({
      tooltip: "Refresh this page's data",
      label: "Refresh this page's data",
    });
  });

  // the label is hidden from screen readers only while it repeats the alert
  it.each([
    [
      'nothing while a reload runs',
      { loading: true, updatedAt: NOW },
      '',
      false,
    ],
    ['nothing once data loads', { updatedAt: NOW - 5 * minute }, '', false],
    [
      'a failed reload',
      { failed: true, updatedAt: NOW },
      'Refresh failed',
      true,
    ],
    [
      'nothing on the retry of a failed reload',
      { failed: true, loading: true },
      '',
      false,
    ],
    [
      'data that may be out of date, and why',
      { updatedAt: NOW - 2 * minute, resource: 'hosts' },
      'Data may be out of date: older than 90 s',
      false,
    ],
  ])('announces %s', async (_, status, want, labelHidden) => {
    store.role = role('Viewer', ['experiments', 'list']);
    Object.assign(pageStatus, { refresh: () => {}, ...status });
    const page = await header();

    // the only live region is the one for problems, so polls stay quiet
    expect(page.findAll('[aria-live]')).toHaveLength(1);
    expect(page.find('.refresh-alert.is-sr-only').text()).toBe(want);
    expect(page.find('.refresh-label').attributes('aria-hidden')).toBe(
      labelHidden ? 'true' : undefined,
    );
  });

  it.each([
    [
      'the server shutting down',
      'shutdown',
      'phēnix server shutting down or restarting',
    ],
    ['falling behind', 'behind', 'live updates disconnected'],
    ['a lost connection', 'lost', 'live updates disconnected'],
  ])('says why live data stopped updating after %s', async (_, cause, want) => {
    setWsDown(true, cause);
    try {
      Object.assign(pageStatus, {
        refresh: () => {},
        updatedAt: NOW,
        resource: 'experiments',
      });
      const refresh = await mountComponent(RefreshStatus);
      expect(refresh.find('.refresh-label').attributes('title')).toBe(want);
      expect(refresh.find('.refresh-alert').text()).toBe(
        `Data may be out of date: ${want}`,
      );
    } finally {
      setWsDown(false);
    }
  });

  it.each([
    [
      'the server shutting down',
      'shutdown',
      'phēnix server shutting down or restarting',
    ],
    ['a lost connection', 'lost', 'live updates disconnected'],
  ])(
    'turns stale at once on %s, and fresh again on reconnecting',
    async (_, cause, want) => {
      Object.assign(pageStatus, {
        refresh: () => {},
        updatedAt: NOW,
        resource: 'experiments',
      });
      const refresh = await mountComponent(RefreshStatus);
      const label = () => textOf(refresh.find('.refresh-label'));
      expect(label()).toBe('Updated just now');

      try {
        setWsDown(true, cause);
        await nextTick();
        expect(label()).toBe(`Updated just now · may be out of date: ${want}`);
        expect(refresh.find('.refresh-alert').text()).toBe(
          `Data may be out of date: ${want}`,
        );
      } finally {
        setWsDown(false);
      }
      await nextTick();
      expect(label()).toBe('Updated just now');
    },
  );

  it.each([
    ['a failed reload', { failed: true, updatedAt: NOW }, 'Refresh failed'],
    [
      'data that stays out of date',
      { updatedAt: NOW - 2 * minute, resource: 'hosts' },
      'Data may be out of date: older than 90 s',
    ],
  ])(
    'keeps announcing %s through each poll, so it is not read out again',
    async (_, status, want) => {
      Object.assign(pageStatus, { refresh: () => {}, ...status });
      const refresh = await mountComponent(RefreshStatus);
      // the alert after the page status changes
      const after = async (change) => {
        Object.assign(pageStatus, change);
        await nextTick();
        return refresh.find('.refresh-alert').text();
      };

      expect(await after({})).toBe(want);
      expect(await after({ loading: true })).toBe(want);
      expect(await after({ loading: false })).toBe(want);
      expect(await after({ loading: true })).toBe(want);
      // a reload that fixes it clears the announcement
      expect(
        await after({ loading: false, failed: false, updatedAt: NOW }),
      ).toBe('');
    },
  );

  it('gives an age that reads back within half a minute of the data, wherever the server read it', async () => {
    // any age up to three hours, often near where the label changes form
    const ageMs = fc.oneof(
      fc.nat(3 * 60 * minute),
      fc
        .constantFrom(minute, 60 * minute)
        .chain((edge) => fc.integer({ min: edge - 1000, max: edge + 1000 })),
    );
    await fc.assert(
      fc.asyncProperty(ageMs, fc.boolean(), async (ms, byServer) => {
        Object.assign(pageStatus, {
          refresh: () => {},
          updatedAt: byServer ? NOW : NOW - ms,
          observedAt: byServer ? NOW - ms : null,
        });
        const refresh = await mountComponent(RefreshStatus);
        const label = textOf(refresh.find('.refresh-label'));
        refresh.unmount();

        const [, justNow, minutes, time] =
          /^Updated (?:(just now)|(\d+) min ago|(.+))$/.exec(label);
        if (justNow) expect(ms).toBeLessThan(minute);
        else if (minutes) {
          expect(Math.abs(minutes * minute - ms)).toBeLessThanOrEqual(30_500);
          expect(Number(minutes)).toBeGreaterThan(0);
          expect(Number(minutes)).toBeLessThan(60);
        } else {
          expect(time).toBe(new Date(NOW - ms).toLocaleTimeString());
          expect(ms).toBeGreaterThan(59 * minute);
        }
      }),
      { numRuns: 20 },
    );
  });

  it('is left out on a page with nothing to reload, and when signed out', async () => {
    store.role = role('Viewer', ['experiments', 'list']);
    expect((await header()).find('.refresh-status').exists()).toBe(false);

    Object.assign(pageStatus, { refresh: () => {}, updatedAt: NOW });
    store.auth = false;
    expect((await header()).find('.refresh-status').exists()).toBe(false);
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

    const page = await header();
    expect(
      byLabel(
        page,
        'Background jobs (2 jobs running; 1 succeeded job not viewed)',
      ).exists(),
    ).toBe(true);
    expect(page.find('.tag.is-info.is-rounded.jobs-count').text()).toBe('2');
  });

  it('is a disclosure whose buttons and links assistive technology reaches', async () => {
    store.role = viewer;
    useJobStore().put(job('1', 'running'));

    const page = await header();
    const trigger = page.find('.jobs-trigger');
    expect(trigger.attributes('aria-expanded')).toBe('false');
    expect(trigger.attributes('aria-controls')).toBe('jobs-panel');
    expect(page.find('#jobs-panel').attributes('role')).toBe('region');
    expect(page.find('.jobs-menu').classes()).toEqual([
      'dropdown',
      'is-right',
      'jobs-menu',
    ]);
  });

  it('closes on Escape, back to its button, and on a click outside it', async () => {
    const menu = await mountComponent(JobsMenu, { attachTo: document.body });
    const trigger = menu.find('.jobs-trigger');
    const isOpen = () => menu.classes('is-active');

    await trigger.trigger('click');
    await menu.find('#jobs-panel').trigger('click');
    expect(isOpen()).toBe(true);
    document.body.click();
    await nextTick();
    expect(isOpen()).toBe(false);
    expect(document.activeElement).not.toBe(trigger.element);

    await trigger.trigger('click');
    await menu.trigger('keydown', { key: 'Escape' });
    expect(isOpen()).toBe(false);
    expect(document.activeElement).toBe(trigger.element);

    // Escape with the panel already closed leaves focus where it is
    trigger.element.blur();
    await menu.trigger('keydown', { key: 'Escape' });
    expect(document.activeElement).not.toBe(trigger.element);
  });

  it.each([
    [
      ['failed', 'succeeded'],
      'is-unviewed-failed',
      'exclamation-circle',
      '1 failed job and 1 succeeded job not viewed',
    ],
    [
      ['partial'],
      'is-unviewed-failed',
      'exclamation-circle',
      '1 partly succeeded job not viewed',
    ],
    [
      ['succeeded', 'canceled'],
      'is-unviewed-succeeded',
      'check-circle',
      '1 succeeded job not viewed',
    ],
    [['canceled'], 'is-dark', null, null],
  ])(
    'with finished %j jobs not viewed, is %s',
    async (statuses, cls, icon, text) => {
      const jobs = useJobStore();
      statuses.forEach((status, i) => jobs.put(job(String(i + 1), status)));
      jobs.put(job('9', 'running'));

      const menu = await mountComponent(JobsMenu);
      const trigger = menu.find('.jobs-trigger');
      expect(trigger.classes()).toContain(cls);
      const label = text
        ? `Background jobs (1 job running; ${text})`
        : 'Background jobs (1 job running)';
      expect(trigger.attributes('aria-label')).toBe(label);
      expect(trigger.attributes('title')).toBe(label);
      const shown = menu.find('.jobs-unviewed-icon');
      expect(shown.exists()).toBe(icon !== null);
      if (icon) {
        expect(shown.attributes('aria-hidden')).toBe('true');
        expect(shown.find(`[data-icon="${icon}"]`).exists()).toBe(true);
      }
    },
  );

  it('counts the jobs listed as viewed once opened, and those that finish while open', async () => {
    const jobs = useJobStore();
    jobs.put(job('1', 'failed'));
    jobs.put(job('2', 'running'));

    const menu = await mountComponent(JobsMenu);
    const trigger = menu.find('.jobs-trigger');
    expect(trigger.classes()).toContain('is-unviewed-failed');

    await trigger.trigger('click');
    expect(trigger.classes()).toContain('is-dark');
    expect(JSON.parse(localStorage.getItem('phenix.jobs.viewed'))).toEqual([
      '1',
    ]);

    jobs.put({ ...job('2', 'succeeded'), revision: 2 });
    await nextTick();
    await nextTick();
    expect(jobs.unviewed).toEqual([]);

    await trigger.trigger('click');
    jobs.put(job('3', 'succeeded'));
    await nextTick();
    expect(trigger.classes()).toContain('is-unviewed-succeeded');
  });

  it('says when there are none, and is not shown to no one signed in', async () => {
    store.role = viewer;
    expect(
      byLabel(await header(), 'Background jobs (0 jobs running)').exists(),
    ).toBe(true);

    Object.assign(store, { auth: false });
    expect((await header()).html()).not.toContain('Background jobs');
  });
});

describe('Logout', () => {
  it('is a button that signs the session out with a POST, then clears it', async () => {
    Object.assign(store, {
      role: role('Viewer', ['experiments', 'list']),
      logout: vi.fn(),
    });
    axios.post.mockResolvedValue({ status: 204 });
    const wrapper = await header();

    const logout = wrapper.find('[data-testid="nav-logout"]');
    // a button, so the keyboard reaches it
    expect(logout.element.tagName).toBe('BUTTON');
    expect(logout.attributes('type')).toBe('button');
    expect(textOf(logout)).toBe('Logout');
    await logout.trigger('click');
    await flushPromises();

    expect(axios.post).toHaveBeenCalledWith('logout');
    expect(axios.get).not.toHaveBeenCalledWith('logout');
    expect(store.logout).toHaveBeenCalled();
  });

  // The component as Logout's click handler sees it, with its button shown or
  // hidden in a collapsed menu.
  function logoutHandler({ shown = true, proxyAuth = false } = {}) {
    const burger = { focus: vi.fn() };
    const button = {
      focus: vi.fn(),
      getClientRects: () => (shown ? [{}] : []),
    };
    const ctx = {
      ...AppHeader.data(),
      proxyAuth,
      $buefy: { toast: { open: vi.fn() } },
      $el: {
        querySelector: (selector) =>
          selector === '.navbar-burger' ? burger : null,
      },
    };

    return {
      ctx,
      button,
      burger,
      click: () =>
        AppHeader.methods.logout.call(ctx, { currentTarget: button }),
    };
  }

  beforeEach(() => {
    store.logout = vi.fn();
  });

  it.each([
    ['the server forgets the token', { status: 204 }],
    ['the server no longer knows an expired token', { status: 401 }],
  ])('ends the session when %s', async (_, answer) => {
    axios.post.mockImplementation(() =>
      answer.status < 400
        ? Promise.resolve(answer)
        : Promise.reject({ response: answer }),
    );
    const { ctx, click } = logoutHandler();

    click();
    expect(ctx.loggingOut).toBe(true);
    await flushPromises();

    expect(axios.post).toHaveBeenCalledWith('logout');
    expect(store.logout).toHaveBeenCalledOnce();
    expect(ctx.$buefy.toast.open).not.toHaveBeenCalled();
    expect(notify.show).not.toHaveBeenCalled();
    expect(ctx.loggingOut).toBe(false);
  });

  it('keeps the session when the logout fails, says so, and returns focus to Logout', async () => {
    axios.post.mockRejectedValue(new Error('Network Error'));
    const { ctx, button, burger, click } = logoutHandler();

    click();
    await flushPromises();

    expect(store.logout).not.toHaveBeenCalled();
    expect(notify.show).toHaveBeenCalledWith(
      'Could not log out. Check your connection and try again.',
    );
    expect(button.focus).toHaveBeenCalledOnce();
    expect(burger.focus).not.toHaveBeenCalled();
    expect(ctx.loggingOut).toBe(false);
  });

  it('focuses the menu button after a failed logout from a closed narrow-window menu', async () => {
    axios.post.mockResolvedValue({ status: 500 });
    const { burger, click } = logoutHandler({ shown: false });

    click();
    await flushPromises();

    expect(store.logout).not.toHaveBeenCalled();
    expect(burger.focus).toHaveBeenCalledOnce();
  });

  it('sends no second request for a second press while the logout waits', async () => {
    axios.post.mockResolvedValue({ status: 204 });
    const { click } = logoutHandler();

    click();
    click();
    await flushPromises();

    expect(axios.post).toHaveBeenCalledOnce();
    expect(store.logout).toHaveBeenCalledOnce();
  });

  it('says Reauthorize reauthorized the account', async () => {
    axios.post.mockResolvedValue({ status: 204 });
    const { ctx, click } = logoutHandler({ proxyAuth: true });

    click();
    await flushPromises();

    expect(store.logout).toHaveBeenCalledOnce();
    expect(ctx.$buefy.toast.open).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'is-success' }),
    );
  });
});
