// The VM tiles' screenshots: fetched only for the tiles on screen, a couple at
// a time, once per load of the VMs.
import { flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, capturing, loaderSpy, modules, rbac, resetMocks } =
  await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/pageLoader.js', capturing);

import loadingImg from '@/assets/imgs/loading-screenshot.svg';
import VMtilesView from '@/views/experiment/VMtilesView.vue';
import { never } from './helpers/async.js';
import { mountComponent } from './helpers/mount.js';
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
    if (!names.includes(tile.find('p.title').text())) continue;
    for (const observer of FakeObserver.all) {
      if (observer.watched.has(tile.element)) {
        observer.callback([{ target: tile.element, isIntersecting: visible }]);
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
  { experiment: 'exp1', name: 'a', running: true, screenshot: '' },
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
        tile.find('p.title').text(),
        tile.find('img').attributes('src'),
      ]),
  );

beforeEach(() => {
  resetMocks({ store: { token: 't', features: [], role: null } });
  FakeObserver.all = [];
  vi.stubGlobal('IntersectionObserver', FakeObserver);
  axios.get.mockImplementation(async (url) => ({
    data: shot(url.split('/').at(-2)),
  }));
});

afterEach(() => vi.unstubAllGlobals());

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
    ['refused', denyOnly(['vms/screenshot', 'get']), [], 'not-available'],
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

    expect(pictures(page).a).toContain('not-available');
  });
});
