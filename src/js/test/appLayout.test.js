// How App.vue lays out the page for a route: the centered container, the
// window width of a wide route, or the full bleed of the Builder.
import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { h } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';

vi.mock('@/utils/websocket', () => ({
  addWsHandler: () => {},
  removeWsHandler: () => {},
  connectWebsocket: () => {},
  disconnectWebsocket: () => {},
  isWsConnected: () => false,
  onWsReconnect: () => () => {},
}));
vi.mock('@/router', () => ({
  default: { resolve: () => ({ href: '/features' }) },
}));
vi.mock('@/utils/signIn.js', () => ({ followSignIn: () => () => {} }));
vi.mock('@/utils/timeout.js', () => ({
  TimeoutTool: class {
    resetTimer() {}
  },
}));
vi.mock('@/utils/pageData.js', () => ({ schedulePagePreload: () => {} }));
vi.mock('@/utils/jobs.js', () => ({
  useJobStore: () => ({
    apply: () => {},
    load: async () => {},
    clear: () => {},
  }),
}));

import App from '@/App.vue';

const page = { render: () => h('p', 'page') };

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({ json: async () => ({ features: [] }) })),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

// the app opened at path: the classes of its main area, and whether it has a footer
async function opened(path) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/experiments', component: page },
      { path: '/soh/:id', component: page, meta: { wide: true } },
      { path: '/builder', component: page, meta: { fullBleed: true } },
    ],
  });
  await router.push(path);
  const app = mount(App, {
    global: {
      plugins: [router],
      stubs: { 'app-header': true, 'app-footer': true },
    },
  });
  await flushPromises();
  return {
    main: app.find('#main').classes(),
    footer: app.find('app-footer-stub').exists(),
  };
}

describe('App layout', () => {
  it.each([
    [
      'centers a page in the container',
      '/experiments',
      ['row', 'container', 'is-fullhd', 'px-4'],
      true,
    ],
    [
      'gives a wide route the window width, with its gutter and footer',
      '/soh/demo',
      ['row', 'px-4', 'main--wide'],
      true,
    ],
    [
      'fills the window for the Builder, with no footer',
      '/builder',
      ['main--full-bleed'],
      false,
    ],
  ])('%s', async (_, path, main, footer) => {
    expect(await opened(path)).toEqual({ main, footer });
  });
});
