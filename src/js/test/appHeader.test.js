// The header: a link to each page the role may open and the menus that group
// the others, the Tunneler and Docs buttons, the button that reloads the
// page's data, the background jobs menu and Logout.
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
import NavMenu from '@/components/NavMenu.vue';
import RefreshStatus from '@/components/RefreshStatus.vue';
import JobsMenu from '@/components/jobs/JobsMenu.vue';
import { DOCS_URL } from '@/utils/docs.js';
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

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllEnvs();
});

const header = (options) => mountComponent(AppHeader, options);

// the text of each top-level link in the header, in order
const links = (wrapper) => wrapper.findAll('.navbar-start a').map(textOf);

// Each item of the header's start, in order: a link's text, or a menu as
// "Label: its links", read by opening it.
async function navigation(wrapper) {
  const items = [];
  for (const item of wrapper.findAll(
    '.navbar-start > .nav-tip, .navbar-start > .nav-menu',
  )) {
    if (!item.classes('nav-menu')) {
      items.push(textOf(item.find('a')));
      continue;
    }
    const button = item.find('.nav-menu-button');
    await button.trigger('click');
    const held = item.findAll('.nav-menu-panel a').map(textOf);
    await button.trigger('click');
    items.push(`${textOf(button)}: ${held.join(', ')}`);
  }
  return items;
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
        'Builder',
        'SCORCH',
        'SoH',
        'WebShark',
        'Resources: Configs, Disks, Hosts, VLANs',
        'Admin: Users, Logs, Settings, Console',
      ],
    ],
    [
      'leaves out the pages of features the server does not have',
      admin,
      [],
      [
        'Experiments',
        'Builder',
        'SCORCH',
        'SoH',
        'Resources: Configs, Disks, Hosts, VLANs',
        'Admin: Users, Logs, Settings, Console',
      ],
    ],
    [
      'links only the pages a role may open, as the router allows them',
      viewer,
      ['webshark'],
      ['Experiments', 'SCORCH', 'SoH'],
    ],
    [
      'links Configs and Logs for the roles those pages need',
      role('Configs', ['configs', 'list'], ['logs', 'get']),
      [],
      ['Resources: Configs', 'Admin: Logs'],
    ],
    [
      'links Disks and Hosts for the roles that may list them',
      role('Hardware', ['disks', 'list'], ['hosts', 'list']),
      [],
      ['Resources: Disks, Hosts'],
    ],
    [
      'links Settings and Console for the roles that may change or use them',
      role('Operator', ['settings', 'update'], ['miniconsole', 'post']),
      [],
      ['Admin: Settings, Console'],
    ],
    [
      'links WebShark for a role that may open captures when it is installed',
      role('Captures', ['vms/captures', 'list']),
      ['webshark'],
      ['WebShark'],
    ],
    [
      'links the Builder for a role that may use it',
      role('Builder', ['builder', 'use']),
      [],
      ['Builder'],
    ],
    [
      'links Users for a role that may list users',
      role('Users', ['users', 'list']),
      [],
      ['Admin: Users'],
    ],
    [
      'links Users for a role that may get only the signed-in user',
      role('Self', ['users', 'get', ['alice']]),
      [],
      ['Admin: Users'],
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
    expect(await navigation(await header())).toEqual(want);
  });

  it('gives each item an icon and a tooltip that names it, with a dot between items', async () => {
    Object.assign(store, { role: admin, features: ['webshark'] });
    const page = await header();
    const start = page.find('.navbar-start');

    expect(
      tooltipButtons(start).map(({ tooltip, control }) => [
        tooltip,
        textOf(control),
        control.find('[data-icon]').attributes('data-icon'),
      ]),
    ).toEqual([
      ['Experiments', 'Experiments', 'flask'],
      ['Builder', 'Builder', 'diagram-project'],
      ['SCORCH', 'SCORCH', 'fire'],
      ['State of Health', 'SoH', 'heart-pulse'],
      ['WebShark', 'WebShark', 'shark-fin'],
      ['Resources', 'Resources', 'cubes'],
      ['Admin', 'Admin', 'screwdriver-wrench'],
    ]);
    // the icons are decoration: each item is named by its text
    for (const icon of start.findAll('.icon')) {
      expect(icon.attributes('aria-hidden')).toBe('true');
    }
    const dots = start.findAll('.nav-dot');
    expect(dots).toHaveLength(6);
    for (const dot of dots) {
      expect(textOf(dot)).toBe('·');
      expect(dot.attributes('aria-hidden')).toBe('true');
    }
    // the dots sit between the items, not before the first or after the last
    const order = start
      .findAll('.navbar-start > *')
      .map((el) => (el.classes('nav-dot') ? '·' : 'item'));
    expect(order).toEqual(
      ['item', ...Array(6).fill(['·', 'item']).flat()].flat(),
    );
    expect(page.find('[data-testid="nav-builder"]').attributes('href')).toBe(
      '/builder',
    );
  });

  it('labels SoH in plain text, and spells it out in its tooltip', async () => {
    Object.assign(store, { role: admin, features: [] });
    const page = await header();
    const soh = page
      .findAll('.navbar-start a')
      .find((link) => textOf(link) === 'SoH');
    expect(soh.find('abbr').exists()).toBe(false);
    expect(soh.attributes('aria-label')).toBeUndefined();
    expect(soh.attributes('href')).toBe('/soh');
    const [tip] = tooltipButtons(page).filter(
      ({ control }) => control.element === soh.element,
    );
    expect(tip.tooltip).toBe('State of Health');
  });

  it.each([
    [
      'Resources',
      [
        ['Configs', 'file-code', '/configs/'],
        ['Disks', 'hard-drive', '/disks/'],
        ['Hosts', 'server', '/hosts'],
        ['VLANs', 'network-wired', '/vlans'],
      ],
    ],
    [
      'Admin',
      [
        ['Users', 'users', '/users'],
        ['Logs', 'file-lines', '/log'],
        ['Settings', 'gear', '/settings'],
        ['Console', 'terminal', '/console'],
      ],
    ],
  ])('gives each link in %s its icon and text', async (label, want) => {
    Object.assign(store, { role: admin, features: [] });
    const page = await header();
    const menu = page
      .findAllComponents(NavMenu)
      .find((m) => m.props('label') === label);
    await menu.find('.nav-menu-button').trigger('click');
    expect(
      menu
        .findAll('.nav-menu-panel a')
        .map((a) => [
          textOf(a),
          a.find('[data-icon]').attributes('data-icon'),
          a.attributes('href'),
        ]),
    ).toEqual(want);
  });

  it.each([
    ['/hosts', 'Resources'],
    ['/configs/', 'Resources'],
    ['/settings', 'Admin'],
    ['/log', 'Admin'],
    ['/experiments', null],
  ])('on %s, marks the %s menu as current', async (route, want) => {
    Object.assign(store, { role: admin, features: [] });
    const page = await header({ route });
    const current = page
      .findAllComponents(NavMenu)
      .filter((menu) => menu.classes('is-current'))
      .map((menu) => menu.props('label'));
    expect(current).toEqual(want ? [want] : []);
  });

  it('offers only the experiments to no one signed in', async () => {
    Object.assign(store, { auth: false, role: admin });
    const signedOut = await header();
    expect(await navigation(signedOut)).toEqual(['Experiments']);
    expect(signedOut.findComponent(NavMenu).exists()).toBe(false);
    expect(byLabel(signedOut, 'Logout').exists()).toBe(false);
  });

  it('closes the burger menu when a link in it is followed', async () => {
    Object.assign(store, { role: admin, features: [] });
    const page = await header();
    const menuOpen = () => page.find('.navbar-menu').classes('is-active');

    await page.find('.navbar-burger').trigger('click');
    expect(menuOpen()).toBe(true);
    // a click on the text, which Buefy's navbar leaves open
    await page.find('[data-testid="nav-builder"] .nav-text').trigger('click');
    await flushPromises();
    expect(menuOpen()).toBe(false);
    expect(page.router.currentRoute.value.name).toBe('builder');
  });
});

describe('a header menu', () => {
  const links = [
    { route: 'hosts', label: 'Hosts', icon: 'server' },
    { route: 'settings', label: 'Settings', icon: 'gear' },
  ];
  const menu = (options) =>
    mountComponent(NavMenu, {
      props: { label: 'Resources', icon: 'cubes', links },
      ...options,
    });

  it('is a disclosure that opens its links, without its tooltip over them', async () => {
    const wrapper = await menu();
    const trigger = wrapper.find('.nav-menu-button');
    const tooltip = wrapper.findComponent({ name: 'BTooltip' });
    expect(textOf(trigger)).toBe('Resources');
    expect(trigger.attributes('type')).toBe('button');
    expect(trigger.attributes('aria-expanded')).toBe('false');
    expect(trigger.attributes('aria-controls')).toBeUndefined();
    expect(wrapper.find('#nav-resources-panel').exists()).toBe(false);
    expect(tooltip.props('active')).toBe(true);

    await trigger.trigger('click');
    expect(trigger.attributes('aria-expanded')).toBe('true');
    expect(trigger.attributes('aria-controls')).toBe('nav-resources-panel');
    expect(wrapper.findAll('#nav-resources-panel a').map(textOf)).toEqual([
      'Hosts',
      'Settings',
    ]);
    expect(tooltip.props('active')).toBe(false);
  });

  it('shows the current page is in it', async () => {
    const isCurrent = async (route) =>
      (await menu({ route })).classes('is-current');
    expect(await isCurrent('/hosts')).toBe(true);
    expect(await isCurrent('/experiments')).toBe(false);
  });

  it('closes on Escape, back to its button, on a click outside it, and when a link is followed', async () => {
    const wrapper = await menu({ attachTo: document.body });
    const trigger = wrapper.find('.nav-menu-button');
    const isOpen = () => wrapper.find('#nav-resources-panel').exists();

    await trigger.trigger('click');
    await wrapper.find('#nav-resources-panel').trigger('click');
    expect(isOpen()).toBe(true);
    document.body.click();
    await nextTick();
    expect(isOpen()).toBe(false);
    expect(document.activeElement).not.toBe(trigger.element);

    await trigger.trigger('click');
    await wrapper.trigger('keydown', { key: 'Escape' });
    expect(isOpen()).toBe(false);
    expect(document.activeElement).toBe(trigger.element);

    await trigger.trigger('click');
    await wrapper.find('#nav-resources-panel a').trigger('click');
    await flushPromises();
    expect(isOpen()).toBe(false);
    expect(wrapper.router.currentRoute.value.name).toBe('hosts');
    wrapper.unmount();
  });

  it('closes when focus moves out of it, not within it', async () => {
    const wrapper = await menu({ attachTo: document.body });
    const outside = document.createElement('button');
    document.body.append(outside);
    const isOpen = () => wrapper.find('#nav-resources-panel').exists();

    await wrapper.find('.nav-menu-button').trigger('click');
    await wrapper.trigger('focusout', {
      relatedTarget: wrapper.find('#nav-resources-panel a').element,
    });
    expect(isOpen()).toBe(true);
    await wrapper.trigger('focusout', { relatedTarget: outside });
    expect(isOpen()).toBe(false);
    wrapper.unmount();
    outside.remove();
  });

  describe('on hover', () => {
    const hoverable = (matches) =>
      vi.stubGlobal('matchMedia', (query) => ({
        matches: matches && query.includes('hover: hover'),
      }));
    afterEach(() => {
      vi.unstubAllGlobals();
      vi.useRealTimers();
    });

    it('opens with a mouse on a wide screen, and closes a moment after the pointer leaves', async () => {
      vi.useFakeTimers();
      hoverable(true);
      const wrapper = await menu();
      const isOpen = () => wrapper.find('#nav-resources-panel').exists();

      await wrapper.trigger('mouseenter');
      expect(isOpen()).toBe(true);
      await wrapper.trigger('mouseleave');
      expect(isOpen()).toBe(true);
      await wrapper.trigger('mouseenter');
      vi.advanceTimersByTime(1000);
      await nextTick();
      expect(isOpen()).toBe(true);

      await wrapper.trigger('mouseleave');
      vi.advanceTimersByTime(1000);
      await nextTick();
      expect(isOpen()).toBe(false);
    });

    it('stays open after a click on the button, until a second click', async () => {
      vi.useFakeTimers();
      hoverable(true);
      const wrapper = await menu();
      const trigger = wrapper.find('.nav-menu-button');
      const isOpen = () => wrapper.find('#nav-resources-panel').exists();

      await wrapper.trigger('mouseenter');
      await trigger.trigger('click');
      await wrapper.trigger('mouseleave');
      vi.advanceTimersByTime(1000);
      await nextTick();
      expect(isOpen()).toBe(true);
      await trigger.trigger('click');
      expect(isOpen()).toBe(false);
    });

    it('stays open while focus is inside it after the pointer leaves, until focus leaves', async () => {
      vi.useFakeTimers();
      hoverable(true);
      const wrapper = await menu({ attachTo: document.body });
      const outside = document.createElement('button');
      document.body.append(outside);
      const isOpen = () => wrapper.find('#nav-resources-panel').exists();

      await wrapper.trigger('mouseenter');
      wrapper.find('#nav-resources-panel a').element.focus();
      await wrapper.trigger('mouseleave');
      vi.advanceTimersByTime(1000);
      await nextTick();
      expect(isOpen()).toBe(true);

      await wrapper.trigger('focusout', { relatedTarget: outside });
      expect(isOpen()).toBe(false);
      wrapper.unmount();
      outside.remove();
    });

    it('does not open on a touch screen or in the burger menu', async () => {
      hoverable(false);
      const wrapper = await menu();
      await wrapper.trigger('mouseenter');
      expect(wrapper.find('#nav-resources-panel').exists()).toBe(false);
    });
  });

  it('puts a tooltip on a link that has one, and leaves the others as they are', async () => {
    const wrapper = await mountComponent(NavMenu, {
      props: {
        label: 'Resources',
        icon: 'cubes',
        links: [
          ...links,
          {
            route: 'disks',
            label: 'Disks',
            icon: 'hdd',
            tip: 'VLAN IDs in use',
          },
        ],
      },
    });
    await wrapper.find('.nav-menu-button').trigger('click');
    const tips = wrapper.findAll('.nav-menu-panel li').map((li) => {
      const tip = li.findComponent({ name: 'BTooltip' });
      return tip.exists() ? tip.props('label') : null;
    });
    expect(tips).toEqual([null, null, 'VLAN IDs in use']);
  });
});

describe('the right end of the header', () => {
  // the items in order: buttons by name, separators as |
  const tools = (wrapper) =>
    wrapper.findAll('.nav-tools > *').map((item) => {
      if (item.classes('nav-sep')) return '|';
      if (item.classes('refresh-status')) return 'Refresh';
      if (item.classes('jobs-menu')) return 'Jobs';
      const control = item.find('a, button');
      return control.attributes('aria-label') ?? textOf(control);
    });

  it('has Tunneler, Docs, Refresh, Jobs and Logout, in groups split by lines', async () => {
    Object.assign(store, {
      role: role('Viewer', ['experiments', 'list']),
      features: ['tunneler-download'],
    });
    Object.assign(pageStatus, { refresh: () => {}, updatedAt: NOW });
    const page = await header();

    expect(tools(page)).toEqual([
      'Tunneler',
      'Docs (opens in new tab)',
      '|',
      'Refresh',
      'Jobs',
      '|',
      'Logout',
    ]);
    for (const sep of page.findAll('.nav-sep')) {
      expect(sep.attributes('aria-hidden')).toBe('true');
    }
    // one style for every icon button
    expect(
      page
        .findAll('.nav-tools a, .nav-tools button')
        .every((control) => control.classes('nav-icon-button')),
    ).toBe(true);
  });

  it('keeps only Docs for no one signed in', async () => {
    store.auth = false;
    expect(tools(await header())).toEqual(['Docs (opens in new tab)']);
  });

  it('opens the docs in a new tab from the Docs button', async () => {
    const page = await header();
    const docs = page.find('.docs-button');
    expect(docs.attributes()).toMatchObject({
      href: DOCS_URL,
      target: '_blank',
      rel: 'noopener noreferrer',
      'aria-label': 'Docs (opens in new tab)',
    });
    expect(docs.find('[data-icon="book"]').exists()).toBe(true);
    const [tip] = tooltipButtons(page.find('.nav-tools')).filter(
      ({ control }) => control.element === docs.element,
    );
    expect(tip.tooltip).toBe('Docs');
  });
});

describe('the Tunneler button', () => {
  const viewer = () =>
    Object.assign(store, {
      role: role('Viewer', ['experiments', 'list']),
      features: ['tunneler-download'],
    });
  const button = (wrapper) => wrapper.find('.navbar-end a.tunneler-button');

  it('links to the Tunneler page at the right of the header, as a filled icon', async () => {
    viewer();
    const page = await header();

    expect(links(page)).not.toContain('Tunneler');
    const link = button(page);
    expect(link.attributes('href')).toBe('/tunneler');
    expect(link.classes()).toEqual(
      expect.arrayContaining(['button', 'is-primary', 'nav-icon-button']),
    );
    expect(link.find('[data-icon="archway"]').exists()).toBe(true);
    // only the icon shows; the name is for screen readers and the tooltip
    const label = link.find('.tunneler-label');
    expect(label.classes()).toContain('is-sr-only');
    expect(textOf(link)).toBe('Tunneler');
    const [tip] = tooltipButtons(page.find('.nav-tools')).filter(
      ({ control }) => control.element === link.element,
    );
    expect(tip.tooltip).toBe('Tunneler');
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
  const ACTION = "Refresh this page's data";

  // the button, its tooltip and the color of its icon
  const refreshButton = (wrapper) => {
    const [button] = tooltipButtons(wrapper.find('.refresh-status'));
    return {
      ...button,
      tone: button.control
        .classes()
        .find((c) => /^refresh-(fresh|aging|stale|failed|idle)$/.test(c))
        ?.slice('refresh-'.length),
    };
  };

  it.each([
    [
      'says the first load is running and spins',
      { loading: true },
      { tip: 'Refreshing…', tone: 'idle', disabled: true, spins: true },
    ],
    [
      'says a reload is running, in the color of the data it reloads',
      { loading: true, updatedAt: NOW - 3 * minute },
      { tip: 'Refreshing…', tone: 'aging', disabled: true, spins: true },
    ],
    [
      'measures the age from when the server read the data',
      { updatedAt: NOW - 30 * 1000, observedAt: NOW - 3 * minute },
      {
        tip: 'Updated 3 min ago',
        tone: 'aging',
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
        tip: 'Updated 3 min ago · may be out of date: minimega was busy. Showing earlier data',
        tone: 'stale',
        disabled: false,
        spins: false,
      },
    ],
    [
      'says data older than its bound may be out of date',
      { updatedAt: NOW - 2 * minute, resource: 'hosts' },
      {
        tip: 'Updated 2 min ago · may be out of date: older than 90 s',
        tone: 'stale',
        disabled: false,
        spins: false,
      },
    ],
    [
      'keeps the red of a failure through the retry',
      { loading: true, failed: true },
      { tip: 'Refreshing…', tone: 'failed', disabled: true, spins: true },
    ],
    [
      'says the data was just updated',
      { updatedAt: NOW - 30 * 1000 },
      { tip: 'Updated just now', tone: 'fresh', disabled: false, spins: false },
    ],
    [
      'says how many minutes ago the data was updated',
      { updatedAt: NOW - 5 * minute },
      {
        tip: 'Updated 5 min ago',
        tone: 'stale',
        disabled: false,
        spins: false,
      },
    ],
    [
      'gives the time of an update over an hour ago',
      { updatedAt: NOW - 90 * minute },
      {
        tip: `Updated ${new Date(NOW - 90 * minute).toLocaleTimeString()}`,
        tone: 'stale',
        disabled: false,
        spins: false,
      },
    ],
    [
      'says a reload failed',
      { failed: true, updatedAt: NOW - 5 * minute },
      { tip: 'Refresh failed', tone: 'failed', disabled: false, spins: false },
    ],
    [
      'is plain before anything has loaded',
      {},
      { tip: ACTION, tone: 'idle', disabled: false, spins: false },
    ],
  ])('%s', async (_, status, want) => {
    store.role = role('Viewer', ['experiments', 'list']);
    Object.assign(pageStatus, { refresh: () => {}, ...status });
    const page = await header();
    const { tooltip, tone, disabled, control, label } = refreshButton(page);

    expect({
      tip: tooltip,
      tone,
      disabled,
      spins: page.find('[data-icon="sync-alt"].fa-spin').exists(),
    }).toEqual(want);
    // its name says what it does, then what the tooltip says
    expect(label).toBe(
      want.tip === ACTION
        ? ACTION
        : `${ACTION}, ${want.tip[0].toLowerCase()}${want.tip.slice(1)}`,
    );
    // an icon only, at every width, without a separate flag icon
    expect(textOf(control)).toBe('');
    expect(page.find('.refresh-status').findAll('.icon')).toHaveLength(1);
    expect(page.find('.refresh-label').exists()).toBe(false);
  });

  it.each([
    [
      'with an age limit: fresh to half of it, aging to the limit, then stale past it',
      'hosts',
      [
        [0, 'fresh'],
        [30 * 1000, 'fresh'],
        [45 * 1000, 'aging'],
        [90 * 1000, 'aging'],
        [105 * 1000, 'stale'],
      ],
    ],
    [
      'without one: fresh for a minute, aging to five, then stale',
      'experiments',
      [
        [0, 'fresh'],
        [45 * 1000, 'fresh'],
        [60 * 1000, 'aging'],
        [285 * 1000, 'aging'],
        [300 * 1000, 'stale'],
      ],
    ],
  ])('colors the icon by age %s', async (_, resource, steps) => {
    Object.assign(pageStatus, {
      refresh: () => {},
      updatedAt: NOW,
      resource,
    });
    const refresh = await mountComponent(RefreshStatus);
    const seen = [];
    let at = 0;
    for (const [ms] of steps) {
      // the clock the status redraws with, every 15 s
      vi.advanceTimersByTime(ms - at);
      at = ms;
      await nextTick();
      seen.push([ms, refreshButton(refresh).tone]);
    }
    expect(seen).toEqual(steps);
  });

  it('opens its tooltip to the left, inside the window', async () => {
    Object.assign(pageStatus, { refresh: () => {}, updatedAt: NOW });
    const refresh = await mountComponent(RefreshStatus);
    expect(refresh.findComponent({ name: 'BTooltip' }).props('position')).toBe(
      'is-left',
    );
  });

  it('says how old the data is in the name of its button', async () => {
    Object.assign(pageStatus, {
      refresh: () => {},
      updatedAt: NOW - 3 * minute,
    });
    const refresh = await mountComponent(RefreshStatus);
    expect(
      byLabel(refresh, "Refresh this page's data, updated 3 min ago").exists(),
    ).toBe(true);
  });

  it('reloads the data when pressed', async () => {
    const reload = vi.fn();
    Object.assign(pageStatus, { refresh: reload, updatedAt: NOW });
    const refresh = await mountComponent(RefreshStatus);
    await refreshButton(refresh).control.trigger('click');
    expect(reload).toHaveBeenCalledOnce();
  });

  it.each([
    ['nothing while a reload runs', { loading: true, updatedAt: NOW }, ''],
    ['nothing once data loads', { updatedAt: NOW - 5 * minute }, ''],
    ['a failed reload', { failed: true, updatedAt: NOW }, 'Refresh failed'],
    [
      'nothing on the retry of a failed reload',
      { failed: true, loading: true },
      '',
    ],
    [
      'data that may be out of date, and why',
      { updatedAt: NOW - 2 * minute, resource: 'hosts' },
      'Data may be out of date: older than 90 s',
    ],
  ])('announces %s', async (_, status, want) => {
    store.role = role('Viewer', ['experiments', 'list']);
    Object.assign(pageStatus, { refresh: () => {}, ...status });
    const page = await header();

    // the only live region is the one for problems, so polls stay quiet
    expect(page.findAll('[aria-live]')).toHaveLength(1);
    expect(page.find('.refresh-alert.is-sr-only').text()).toBe(want);
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
      expect(refreshButton(refresh).tooltip).toBe(
        `Updated just now · may be out of date: ${want}`,
      );
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
      const shown = () => {
        const { tooltip, tone } = refreshButton(refresh);
        return { tooltip, tone };
      };
      expect(shown()).toEqual({ tooltip: 'Updated just now', tone: 'fresh' });

      try {
        setWsDown(true, cause);
        await nextTick();
        expect(shown()).toEqual({
          tooltip: `Updated just now · may be out of date: ${want}`,
          tone: 'stale',
        });
        expect(refresh.find('.refresh-alert').text()).toBe(
          `Data may be out of date: ${want}`,
        );
      } finally {
        setWsDown(false);
      }
      await nextTick();
      expect(shown()).toEqual({ tooltip: 'Updated just now', tone: 'fresh' });
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
    // any age up to three hours, often near where the tooltip changes form
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
        const { tooltip } = refreshButton(refresh);
        refresh.unmount();

        const [, justNow, minutes, time] =
          /^Updated (?:(just now)|(\d+) min ago|(.+))$/.exec(tooltip);
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

  it('closes when focus moves out of it, not within it', async () => {
    const menu = await mountComponent(JobsMenu, { attachTo: document.body });
    const outside = document.createElement('button');
    document.body.append(outside);
    const isOpen = () => menu.classes('is-active');

    await menu.find('.jobs-trigger').trigger('click');
    await menu.trigger('focusout', {
      relatedTarget: menu.find('#jobs-panel').element,
    });
    expect(isOpen()).toBe(true);
    await menu.trigger('focusout', { relatedTarget: outside });
    expect(isOpen()).toBe(false);
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
    // a button, so the keyboard reaches it, that shows only its icon
    expect(logout.element.tagName).toBe('BUTTON');
    expect(logout.attributes('type')).toBe('button');
    expect(logout.attributes('aria-label')).toBe('Logout');
    expect(textOf(logout)).toBe('');
    expect(logout.find('[data-icon="right-from-bracket"]').exists()).toBe(true);
    const tip = () =>
      tooltipButtons(wrapper.find('.nav-tools')).find(
        ({ control }) => control.element === logout.element,
      ).tooltip;
    expect(tip()).toBe('Logout');
    await logout.trigger('click');
    // while it waits for the server, it says so
    expect(logout.attributes('aria-label')).toBe('Logging out…');
    expect(logout.attributes('aria-busy')).toBe('true');
    expect(tip()).toBe('Logging out…');
    await flushPromises();

    expect(axios.post).toHaveBeenCalledWith('logout');
    expect(axios.get).not.toHaveBeenCalledWith('logout');
    expect(store.logout).toHaveBeenCalled();
  });

  it('is Reauthorize behind an authenticating proxy, even before sign-in', async () => {
    vi.stubEnv('VITE_AUTH', 'proxy');
    store.auth = false;
    const wrapper = await header();
    const button = wrapper.find('.app-header__logout');
    expect(button.attributes('aria-label')).toBe('Reauthorize');
    const tip = wrapper
      .findAllComponents({ name: 'BTooltip' })
      .find((t) => t.find('.app-header__logout').exists());
    expect(tip.props('label')).toBe(
      'Reauthorize (reload your session and role)',
    );
    // the rightmost button: below it, the tooltip passes the window edge
    expect(tip.props('position')).toBe('is-left');
    expect(button.attributes('data-testid')).toBeUndefined();
    expect(button.find('[data-icon="user-shield"]').exists()).toBe(true);
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

describe('the load of a page before it opens', () => {
  const INTENTS = ['pointerenter', 'focus', 'pointerdown', 'touchstart'];

  // Makes the page `name` of the header's router skip the idle prefetch and
  // load with a loader that counts its calls; returns the loader.
  function onDemand(page, name) {
    const load = vi.fn(() => Promise.resolve({ default: {} }));
    const { path } = page.router.resolve({ name });
    page.router.addRoute({
      path,
      name,
      component: load,
      meta: { prefetch: false },
    });
    return load;
  }

  it('loads the Builder one time for a pointer, focus or press on its link', async () => {
    store.role = role('Builder', ['builder', 'use']);
    const page = await header();
    const load = onDemand(page, 'builder');
    const link = page.find('[data-testid="nav-builder"]');

    await link.trigger('pointerenter');
    expect(load).toHaveBeenCalledOnce();
    for (const type of INTENTS) await link.trigger(type);
    expect(load).toHaveBeenCalledOnce();
  });

  it('loads no Builder for a role that may not use it', async () => {
    store.role = role('Viewer', ['experiments', 'list']);
    const page = await header();
    const load = onDemand(page, 'builder');

    expect(page.find('[data-testid="nav-builder"]').exists()).toBe(false);
    for (const link of page.findAll('a')) {
      for (const type of INTENTS) await link.trigger(type);
    }
    expect(load).not.toHaveBeenCalled();
  });

  it('loads a page in a menu that skips the idle prefetch', async () => {
    store.role = role('Configs', ['configs', 'list']);
    const page = await header();
    const load = onDemand(page, 'configs');

    await page.find('.nav-menu-button').trigger('click');
    const link = page.find('.nav-menu-link');
    expect(textOf(link)).toBe('Configs');
    await link.trigger('focus');
    expect(load).toHaveBeenCalledOnce();
  });
});
