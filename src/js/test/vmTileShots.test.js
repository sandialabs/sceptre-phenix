// The VM tiles' screenshots: fetched only for the tiles on screen that draw
// no live screen, a couple at a time, once per load of the VMs. The live
// tiles of the page (M5a).
import { flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, capturing, loaderSpy, modules, rbac, resetMocks } =
  await vi.hoisted(() => import('./helpers/mocks.js'));
const { FakeRfb, rfbs, resetRfbs } = await vi.hoisted(
  () => import('./helpers/fakeRfb.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/pageLoader.js', capturing);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/vnc/loadRfb.js', () => ({ loadRfb: async () => FakeRfb }));

import loadingImg from '@/assets/imgs/loading-screenshot.svg';
import VMtilesView from '@/views/experiment/VMtilesView.vue';
import { sharedLiveTilePool } from '@/vnc/liveTilePool.js';
import { never } from './helpers/async.js';
import { mountComponent, textOf } from './helpers/mount.js';
import { allowAll, denyOnly } from './helpers/permissions.js';

// An IntersectionObserver whose reports the test makes (see show).
class FakeObserver {
  static all = [];

  constructor(callback) {
    this.callback = callback;
    this.watched = new Set();
    FakeObserver.all.push(this);
  }

  observe(el) {
    this.watched.add(el);
  }

  unobserve(el) {
    this.watched.delete(el);
  }

  disconnect() {
    this.watched.clear();
  }
}

// tells the page these tiles scrolled on (or off) screen
async function show(page, names, visible = true) {
  for (const tile of page.findAll('.tile.is-child')) {
    if (!names.includes(tile.find('.title').text())) continue;
    for (const observer of FakeObserver.all) {
      if (observer.watched.has(tile.element)) {
        observer.callback([{ target: tile.element, isIntersecting: visible }]);
      }
    }
  }
  await flushPromises();
}

// tells the live tile pool these tiles are on screen
async function showLive(page, names) {
  for (const tile of page.findAll('.tile.is-child')) {
    if (!names.includes(tile.find('.title').text())) continue;
    const el = tile.find('.vnc-tile').element;
    for (const observer of FakeObserver.all) {
      if (observer.watched.has(el)) {
        observer.callback([
          { target: el, intersectionRatio: 1, isIntersecting: true },
        ]);
      }
    }
  }
  await flushPromises();
}

const shot = (name) => `data:image/png;base64,${btoa(name)}`;

// the screenshot URLs asked for, by VM name
const asked = () =>
  axios.get.mock.calls
    .map(([url]) => url.match(/vms\/([^/]+)\/screenshot\.png$/)?.[1])
    .filter(Boolean)
    .map(decodeURIComponent);

const vms = [
  {
    experiment: 'exp1',
    name: 'a',
    running: true,
    state: 'RUNNING',
    screenshot: '',
  },
  { experiment: 'exp1', name: 'b', running: true, screenshot: '' },
  { experiment: 'exp1', name: 'c', running: true, screenshot: '' },
  { experiment: 'exp1', name: 'd', running: true, screenshot: '' },
  { experiment: 'exp1', name: 'off', running: false, screenshot: '' },
];

// the page on exp1, its VMs loaded
async function tiles() {
  const page = await mountComponent(VMtilesView, { route: '/experiment/exp1' });
  loaderSpy.options.apply(vms);
  await flushPromises();
  return page;
}

const pictures = (page) =>
  Object.fromEntries(
    page
      .findAll('.tile.is-child')
      .map((tile) => [
        tile.find('.title').text(),
        tile.find('img').attributes('src'),
      ]),
  );

beforeEach(() => {
  resetMocks({ store: { token: 't', features: [], role: null } });
  FakeObserver.all = [];
  vi.stubGlobal('IntersectionObserver', FakeObserver);
  resetRfbs();
  axios.get.mockImplementation(async (url) => {
    const vnc = /^experiments\/([^/]+)\/vms\/([^/]+)\/vnc$/.exec(url);
    if (vnc) return { data: session(vnc[1], vnc[2]) };
    return { data: shot(url.split('/').at(-2)) };
  });
});

afterEach(() => {
  sharedLiveTilePool().dispose();
  vi.unstubAllGlobals();
});

const session = (exp, name) => ({
  experiment: exp,
  vm: name,
  websocket: `experiments/${exp}/vms/${name}/vnc/ws`,
  banners: {
    top: { lines: [], backgroundColor: 'white', textColor: 'black' },
    bottom: { lines: [], backgroundColor: 'white', textColor: 'black' },
  },
  display: { vga: 'std', remoteResize: false },
  clipboard: { mode: 'pasteAsKeys' },
});

describe('VM tile screenshots', () => {
  it('are fetched only for the running VMs on screen, at a shared width', async () => {
    const page = await tiles();
    expect(asked()).toEqual([]);

    await show(page, ['a', 'off']);

    expect(asked()).toEqual(['a']);
    expect(axios.get).toHaveBeenCalledWith(
      'experiments/exp1/vms/a/screenshot.png',
      expect.objectContaining({
        params: { size: 600, base64: true },
        responseType: 'text',
      }),
    );
    expect(pictures(page).a).toBe(shot('a'));
    expect(pictures(page).b).not.toBe(shot('b'));
  });

  // the API sends running VMs with an empty screenshot field
  it('show the loading picture until one arrives', async () => {
    axios.get.mockImplementation(never);
    const page = await tiles();

    await show(page, ['a']);

    expect(pictures(page).a).toBe(loadingImg);
    expect(pictures(page).b).toBe(loadingImg);
  });

  it('are fetched again only after the VMs load again', async () => {
    const page = await tiles();
    await show(page, ['a']);
    await show(page, ['a'], false);
    await show(page, ['a']);
    expect(asked()).toEqual(['a']);

    loaderSpy.options.apply(vms);
    await flushPromises();
    expect(asked()).toEqual(['a', 'a']);
  });

  it('are fetched two at a time', async () => {
    axios.get.mockImplementation(never);
    const page = await tiles();

    await show(page, ['a', 'b', 'c', 'd']);

    expect(asked()).toEqual(['a', 'b']);
  });

  it.each([
    ['allowed', allowAll, ['a'], shot('a')],
    ['refused', denyOnly(['vms/screenshot', 'get']), [], 'NO%20SCREENSHOT'],
  ])('follow the role: %s', async (_, role, wantAsked, wantPicture) => {
    rbac.allowed = role;
    const page = await tiles();

    await show(page, ['a']);

    expect(asked()).toEqual(wantAsked);
    expect(pictures(page).a).toContain(wantPicture);
  });

  it('show that none is available when one cannot be taken', async () => {
    axios.get.mockRejectedValue(new Error('400'));
    const page = await tiles();

    await show(page, ['a']);

    expect(pictures(page).a).toContain('NO%20SCREENSHOT');
  });
});

describe('VM tiles', () => {
  it('are live and view only on screen, each with its state and a console link', async () => {
    const page = await tiles();
    await showLive(page, ['a', 'off']);

    expect(rfbs.map((rfb) => rfb.url)).toEqual([
      'ws://phenix.test/api/v1/experiments/exp1/vms/a/vnc/ws?token=t',
    ]);
    expect(rfbs[0].viewOnly).toBe(true);
    const [a] = page.findAll('.tile.is-child');
    expect(textOf(a.get('.vm-tile-head .tag'))).toBe('Running');
    expect(a.get('a.vnc-tile-link').attributes('href')).toBe('/vnc?vm=exp1/a');
    expect(textOf(a.get('a.vnc-tile-link'))).toBe('Open the console of VM a');

    rfbs[0].connect();
    await flushPromises();
    expect(textOf(a.get('.vnc-tile-tag'))).toBe('LIVE');
    // the page has the Live / Snapshots switch and the live tile limit
    expect(textOf(page.get('.vnc-tile-stats'))).toBe(
      '1 of 4 live · 3 off screen',
    );
    expect(page.get('select').element.value).toBe('16');
  });

  it('fetch no screenshot for a live tile, and one when it stops', async () => {
    const page = await tiles();
    await showLive(page, ['a']);
    rfbs[0].connect();
    await flushPromises();

    loaderSpy.options.apply(vms);
    await flushPromises();
    await show(page, ['a', 'b']);
    expect(asked()).toEqual(['b']);

    sharedLiveTilePool().setLiveMode(false);
    await flushPromises();
    expect(asked()).toEqual(['b', 'a']);
    expect(pictures(page).a).toBe(shot('a'));
  });
});

describe('a live VM tile that the search hides', () => {
  it('is fetched a screenshot once the search shows it again', async () => {
    const page = await tiles();
    await showLive(page, ['a']);
    rfbs[0].connect();
    await flushPromises();
    await show(page, ['a']);
    expect(asked()).toEqual([]);

    await page.setData({ searchName: 'b' });
    await flushPromises();
    await page.setData({ searchName: '' });
    await flushPromises();
    await show(page, ['a']);
    expect(asked()).toEqual(['a']);
  });

  it('links only the VMs with a screen to the console', async () => {
    const page = await tiles();
    const links = Object.fromEntries(
      page
        .findAll('.tile.is-child')
        .map((tile) => [
          tile.find('.title').text(),
          tile.find('a.vnc-tile-link').exists(),
        ]),
    );
    expect(links).toEqual({ a: true, b: true, c: true, d: true, off: false });
  });
});
