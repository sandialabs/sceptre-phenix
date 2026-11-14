// The bar at the top of the window while a navigation waits for the chunk of
// a page: it shows only after 150 ms, and it goes away when the navigation
// ends, fails, is cancelled or goes somewhere else.
import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { h } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';

import PageLoadBar from '@/components/PageLoadBar.vue';

const page = { render: () => h('p', 'page') };

// a lazy page whose chunk loads when the test says so
function slowPage() {
  const chunk = Promise.withResolvers();
  return {
    load: () => chunk.promise,
    arrive: () => chunk.resolve({ default: page }),
    fail: () => chunk.reject(new TypeError('Failed to fetch')),
  };
}

let pages;
let router;

beforeEach(async () => {
  vi.useFakeTimers();
  pages = { a: slowPage(), b: slowPage(), moved: slowPage() };
  router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: page },
      { path: '/fast', component: page },
      { path: '/a', component: pages.a.load },
      { path: '/b', component: pages.b.load },
      { path: '/moved', component: pages.moved.load },
    ],
  });
  await router.push('/');
});

afterEach(() => {
  vi.useRealTimers();
});

const mountBar = () => mount(PageLoadBar, { global: { plugins: [router] } });
const bar = (wrapper) => wrapper.find('[role="progressbar"]');

// starts a navigation without waiting for it; resolves to its failure or error
const go = (path) => router.push(path).catch((err) => err);

describe('the page load bar', () => {
  it('shows after 150 ms, named "Loading page", and goes when the page loads', async () => {
    const wrapper = mountBar();
    const done = go('/a');
    await flushPromises();

    vi.advanceTimersByTime(149);
    await flushPromises();
    expect(bar(wrapper).exists()).toBe(false);

    vi.advanceTimersByTime(1);
    await flushPromises();
    expect(bar(wrapper).exists()).toBe(true);
    expect(bar(wrapper).attributes('aria-label')).toBe('Loading page');

    pages.a.arrive();
    await done;
    await flushPromises();
    expect(bar(wrapper).exists()).toBe(false);
    expect(router.currentRoute.value.path).toBe('/a');
  });

  it('never shows for a navigation that ends within 150 ms', async () => {
    const wrapper = mountBar();
    const done = go('/a');
    await flushPromises();
    vi.advanceTimersByTime(100);
    pages.a.arrive();
    await done;

    vi.advanceTimersByTime(1000);
    await flushPromises();
    expect(bar(wrapper).exists()).toBe(false);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('goes when the chunk fails to load', async () => {
    const wrapper = mountBar();
    const done = go('/a');
    await flushPromises();
    vi.advanceTimersByTime(150);
    await flushPromises();
    expect(bar(wrapper).exists()).toBe(true);

    pages.a.fail();
    expect(await done).toBeInstanceOf(TypeError);
    await flushPromises();
    expect(bar(wrapper).exists()).toBe(false);
    expect(router.currentRoute.value.path).toBe('/');
  });

  it('stays for a newer navigation when it cancels an older one', async () => {
    const wrapper = mountBar();
    const first = go('/a');
    await flushPromises();
    const second = go('/b');
    await flushPromises();
    vi.advanceTimersByTime(150);
    await flushPromises();
    expect(bar(wrapper).exists()).toBe(true);

    // the older navigation ends cancelled, and the newer one still waits
    pages.a.arrive();
    await first;
    await flushPromises();
    expect(bar(wrapper).exists()).toBe(true);

    pages.b.arrive();
    await second;
    await flushPromises();
    expect(bar(wrapper).exists()).toBe(false);
    expect(router.currentRoute.value.path).toBe('/b');
  });

  it('goes when a fast navigation cancels a slow one', async () => {
    const wrapper = mountBar();
    const first = go('/a');
    await flushPromises();
    vi.advanceTimersByTime(150);
    await flushPromises();
    expect(bar(wrapper).exists()).toBe(true);

    await go('/fast');
    await flushPromises();
    expect(bar(wrapper).exists()).toBe(false);

    pages.a.arrive();
    await first;
    await flushPromises();
    expect(bar(wrapper).exists()).toBe(false);
    expect(router.currentRoute.value.path).toBe('/fast');
  });

  it.each([
    ['is redirected', '/fast', '/fast'],
    ['is aborted', false, '/'],
  ])(
    'goes when a navigation %s after its chunk loads',
    async (_, outcome, path) => {
      router.beforeResolve((to) => (to.path === '/moved' ? outcome : true));
      const wrapper = mountBar();
      const done = go('/moved');
      await flushPromises();
      vi.advanceTimersByTime(150);
      await flushPromises();
      expect(bar(wrapper).exists()).toBe(true);

      pages.moved.arrive();
      await done;
      await flushPromises();
      expect(bar(wrapper).exists()).toBe(false);
      expect(router.currentRoute.value.path).toBe(path);
    },
  );

  it('stops watching the router once unmounted', async () => {
    const wrapper = mountBar();
    wrapper.unmount();
    go('/a');
    await flushPromises();
    expect(vi.getTimerCount()).toBe(0);
  });
});
