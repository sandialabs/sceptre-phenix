// The header: a link to each page the signed-in role may open, and the
// current page's load state beside the button that reloads it.
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
import { pageStatus } from '@/utils/pageLoader.js';
import { renderSSR, textOf, tooltipButtons } from './helpers/render.js';
import { role } from './helpers/roles.js';

const NOW = Date.parse('2026-09-30T12:00:00Z');

beforeEach(() => {
  // the refresh status's clock, and the timer it keeps it current with
  vi.useFakeTimers({ now: NOW });
  Object.assign(store, { auth: true, role: null, features: [] });
  Object.assign(pageStatus, {
    refresh: null,
    loading: false,
    updatedAt: null,
    failed: false,
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
      'says a reload is running over data already shown',
      { loading: true, updatedAt: NOW - 5 * minute },
      { label: 'Refreshing…', state: 'loading', disabled: true, spins: true },
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

    const label = html.match(/<span class="refresh-label"[^>]*>([^<]*)</)[1];
    const [button] = tooltipButtons(html);
    expect({
      label,
      state: button.attrs.match(/refresh-(loading|ok|failed|idle)\b/)?.[1],
      disabled: button.disabled,
      spins: /data-icon="sync-alt" class="fa-spin"/.test(html),
    }).toEqual(want);
    expect(button).toMatchObject({
      tooltip: "Refresh this page's data",
      label: "Refresh this page's data",
    });
  });

  it('is left out on a page with nothing to reload, and when signed out', async () => {
    store.role = role('Viewer', ['experiments', 'list']);
    expect(await renderSSR(AppHeader)).not.toContain('refresh-status');

    Object.assign(pageStatus, { refresh: () => {}, updatedAt: NOW });
    store.auth = false;
    expect(await renderSSR(AppHeader)).not.toContain('refresh-status');
  });
});
