// A route that skips the idle prefetch, such as the Builder, starts to load
// when a link to it shows that the user will open it: a pointer on the link,
// focus on it, or a press. It loads one time, with the route's own loader,
// and not when the user asked to save data or the role may not open it.
import { mount } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, withDirectives } from 'vue';
import { createMemoryHistory, createRouter, RouterLink } from 'vue-router';

const { builderLoads } = vi.hoisted(() => ({ builderLoads: vi.fn() }));
vi.mock('@/views/Builder.vue', () => {
  builderLoads();
  return { default: {} };
});

import { prefetchRoute, vPrefetchRoute } from '@/utils/prefetch.js';

afterEach(() => {
  vi.unstubAllGlobals();
});

const INTENTS = ['pointerenter', 'focus', 'pointerdown', 'touchstart'];

// a lazy page's loader, which counts its calls
const loader = () => vi.fn(() => Promise.resolve({ default: {} }));

// A router whose routes `builder` and `denied` skip the idle prefetch, and
// whose `hosts` does not; `denied` is for a role that may not open it.
function testRouter() {
  const loads = { builder: loader(), hosts: loader(), denied: loader() };
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: {} },
      {
        path: '/builder',
        name: 'builder',
        component: loads.builder,
        meta: { prefetch: false },
      },
      { path: '/hosts', name: 'hosts', component: loads.hosts },
      {
        path: '/denied',
        name: 'denied',
        component: loads.denied,
        meta: { prefetch: false, allowed: () => false },
      },
    ],
  });
  return { router, loads };
}

// a link to the route named `name`, with the directive
const NavLink = {
  props: ['name'],
  render() {
    const to = { name: this.name };
    return withDirectives(
      h(RouterLink, { to }, () => this.name),
      [[vPrefetchRoute, to]],
    );
  },
};

function link(name) {
  const { router, loads } = testRouter();
  const wrapper = mount(NavLink, {
    props: { name },
    global: { plugins: [router] },
  });
  return { wrapper, a: wrapper.find('a'), loads };
}

describe('prefetchRoute', () => {
  it('loads the Builder chunk of the app router one time', async () => {
    const { default: router } = await import('@/router.js');
    for (let i = 0; i < 3; i++) prefetchRoute(router, { name: 'builder' });
    await vi.waitFor(() => expect(builderLoads).toHaveBeenCalledOnce());
    prefetchRoute(router, { name: 'builder' });
    expect(builderLoads).toHaveBeenCalledOnce();
  });

  it('ignores a route the router does not have', () => {
    const { router } = testRouter();
    expect(() => prefetchRoute(router, { name: 'nothing' })).not.toThrow();
  });
});

describe('the prefetch-route directive', () => {
  it.each(INTENTS)('loads the route on %s', async (type) => {
    const { a, loads } = link('builder');
    expect(loads.builder).not.toHaveBeenCalled();
    await a.trigger(type);
    expect(loads.builder).toHaveBeenCalledOnce();
  });

  it('loads the route only one time for every intent', async () => {
    const { a, loads } = link('builder');
    for (const type of [...INTENTS, ...INTENTS]) await a.trigger(type);
    expect(loads.builder).toHaveBeenCalledOnce();
  });

  it('loads nothing when the user asked to save data', async () => {
    const { a, loads } = link('builder');
    vi.stubGlobal('navigator', { connection: { saveData: true } });
    for (const type of INTENTS) await a.trigger(type);
    expect(loads.builder).not.toHaveBeenCalled();
  });

  it('loads nothing for a route the role may not open', async () => {
    const { a, loads } = link('denied');
    for (const type of INTENTS) await a.trigger(type);
    expect(loads.denied).not.toHaveBeenCalled();
  });

  it('leaves a route that the idle prefetch loads to it', async () => {
    const { a, loads } = link('hosts');
    for (const type of INTENTS) await a.trigger(type);
    expect(loads.hosts).not.toHaveBeenCalled();
  });

  it('follows a link that changes its route', async () => {
    const { wrapper, a, loads } = link('hosts');
    await wrapper.setProps({ name: 'builder' });
    await a.trigger('pointerenter');
    expect(loads.builder).toHaveBeenCalledOnce();
  });

  it('stops listening once the link is gone', async () => {
    const { wrapper, a, loads } = link('builder');
    const el = a.element;
    wrapper.unmount();
    for (const type of INTENTS) el.dispatchEvent(new Event(type));
    expect(loads.builder).not.toHaveBeenCalled();
  });
});
