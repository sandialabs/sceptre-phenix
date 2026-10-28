// @vitest-environment happy-dom
// The address while a draft is open: a draft opened from its card puts its
// id in the address, so a reload reopens it.

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
    fullPath: '/builder',
    query: {},
    meta: { title: 'Builder' },
  }),
  useRouter: () => router,
  onBeforeRouteLeave: () => {},
}));

import Builder from '@/views/Builder.vue';
import BuilderDrafts from '@/components/builder/BuilderDrafts.vue';
import { useBuilderStore } from '@/builder/store.js';

enableAutoUnmount(afterEach);

globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
};

const DRAFT_ID = '968d2b25-04ec-489c-874c-2d53bfbfc8f1';

// Mounts the Builder at /builder with empty lists. Loading a draft only sets
// the fields the address follows.
async function mountBuilder() {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);

  for (const action of [
    'fetchSchema',
    'fetchSources',
    'fetchDrafts',
    'fetchDocuments',
    'fetchTemplates',
    'fetchLayerSets',
  ]) {
    vi.spyOn(store, action).mockResolvedValue();
  }
  vi.spyOn(store, 'loadDraft').mockImplementation(async (id) => {
    store.draftId = id;
    store.published = null;
    store.autosave = { flush: async () => true, dispose: () => {} };

    return store.doc;
  });

  const wrapper = mount(Builder, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  await flushPromises();
  router.replace.mockClear();

  return { wrapper, store };
}

afterEach(() => {
  router.replace.mockClear();
});

describe('the address of an open draft', () => {
  test('names a draft opened from its card', async () => {
    const { wrapper } = await mountBuilder();

    wrapper.findComponent(BuilderDrafts).vm.$emit('open', { id: DRAFT_ID });

    await vi.waitFor(() =>
      expect(router.replace).toHaveBeenCalledWith({
        query: { draft: DRAFT_ID },
      }),
    );
  });

  test('does not change when the editor closes and the address names nothing', async () => {
    const { wrapper } = await mountBuilder();

    wrapper.findComponent(BuilderDrafts).vm.$emit('open', { id: DRAFT_ID });
    await vi.waitFor(() => expect(router.replace).toHaveBeenCalled());
    router.replace.mockClear();

    wrapper.vm.editing = false;
    await flushPromises();

    expect(router.replace).not.toHaveBeenCalled();
  });
});
