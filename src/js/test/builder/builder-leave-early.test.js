// @vitest-environment happy-dom
// The Builder view left while it still reads its lists: the link that
// opened it is not followed once the lists arrive.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

const phenix = vi.hoisted(() => ({
  username: 'builder',
  role: {
    name: 'Builder',
    policies: [{ resources: ['builder'], resourceNames: [], verbs: ['use'] }],
  },
  auth: true,
}));
const router = vi.hoisted(() => ({
  replace: vi.fn(),
  push: vi.fn(),
  resolve: vi.fn(),
}));

vi.mock('@/store.js', () => ({
  usePhenixStore: () => phenix,
}));
vi.mock('@/router', () => ({
  default: router,
  pageTitle: (...parts) => [...parts.filter(Boolean), 'phēnix'].join(' - '),
}));
vi.mock('vue-router', () => ({
  useRoute: () => ({
    name: 'builder',
    fullPath: '/builder?topology=plant',
    query: { topology: 'plant' },
    meta: { title: 'Builder' },
  }),
  useRouter: () => router,
  onBeforeRouteLeave: () => {},
}));

import Builder from '@/views/Builder.vue';
import { useBuilderStore } from '@/builder/store.js';

enableAutoUnmount(afterEach);

globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
};

// Mounts the Builder. The schema comes when the returned function is called.
// Other lists come at once and name topology plant, so its link opens Import.
function mountBuilder() {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);
  let listSchema;

  vi.spyOn(store, 'fetchSchema').mockImplementation(
    () =>
      new Promise((resolve) => {
        listSchema = resolve;
      }),
  );
  vi.spyOn(store, 'fetchSources').mockImplementation(async () => {
    store.sources = { ...store.sources, topologies: ['plant'] };
  });
  for (const action of [
    'fetchDrafts',
    'fetchDocuments',
    'fetchTemplates',
    'fetchLayerSets',
  ]) {
    vi.spyOn(store, action).mockResolvedValue();
  }

  const wrapper = mount(Builder, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  return { wrapper, store, listSchema: () => listSchema() };
}

afterEach(() => {
  router.replace.mockClear();
});

describe('the Builder and its link', () => {
  test('follows the link once the lists arrive', async () => {
    const { listSchema } = mountBuilder();

    await flushPromises();
    listSchema();
    await flushPromises();

    expect(router.replace).toHaveBeenCalledWith({ query: {} });
  });

  test('left while it reads its lists, does not follow the link', async () => {
    const { wrapper, store, listSchema } = mountBuilder();

    await flushPromises();
    wrapper.unmount();
    listSchema();
    await flushPromises();

    expect(router.replace).not.toHaveBeenCalled();
    expect(store.error).toBeFalsy();
  });
});
