// @vitest-environment happy-dom
// The address while a draft is open, and a Builder view left while it still
// reads its lists: the link that opened it is then not followed.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { flushPromises, mount } from '@vue/test-utils';

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
const route = vi.hoisted(() => ({
  name: 'builder',
  fullPath: '/builder',
  query: {},
  meta: { title: 'Builder' },
}));

vi.mock('@/store.js', () => ({
  usePhenixStore: () => phenix,
}));
vi.mock('@/router', () => ({
  default: router,
  pageTitle: (...parts) => [...parts.filter(Boolean), 'phēnix'].join(' - '),
}));
vi.mock('vue-router', () => ({
  useRoute: () => route,
  useRouter: () => router,
  onBeforeRouteLeave: () => {},
}));

import Builder from '@/views/Builder.vue';
import BuilderDrafts from '@/components/builder/BuilderDrafts.vue';
import { useBuilderStore } from '@/builder/store.js';

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
  route.fullPath = '/builder';
  route.query = {};
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

// Mounts the Builder at /builder?topology=plant. The schema comes when the
// returned function is called. Other lists come at once and name topology
// plant, so its link opens Import.
function mountLinkedBuilder() {
  route.fullPath = '/builder?topology=plant';
  route.query = { topology: 'plant' };

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

describe('the Builder and its link', () => {
  test('follows the link once the lists arrive', async () => {
    const { listSchema } = mountLinkedBuilder();

    await flushPromises();
    listSchema();
    await flushPromises();

    expect(router.replace).toHaveBeenCalledWith({ query: {} });
  });

  test('left while it reads its lists, does not follow the link', async () => {
    const { wrapper, store, listSchema } = mountLinkedBuilder();

    await flushPromises();
    wrapper.unmount();
    listSchema();
    await flushPromises();

    expect(router.replace).not.toHaveBeenCalled();
    expect(store.error).toBeFalsy();
  });
});
