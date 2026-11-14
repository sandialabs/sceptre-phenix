// @vitest-environment happy-dom
// The Builder header's texts for screen readers: the buttons' descriptions and
// the focus mode announcements name the keys in words, also on a Mac.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { flushPromises, mount } from '@vue/test-utils';

vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({
    username: 'builder',
    role: {
      name: 'Builder',
      policies: [{ resources: ['builder'], resourceNames: [], verbs: ['use'] }],
    },
    auth: true,
  }),
}));
vi.mock('@/router', () => ({
  default: { replace: vi.fn(), push: vi.fn(), resolve: vi.fn() },
  pageTitle: (...parts) => [...parts.filter(Boolean), 'phēnix'].join(' - '),
}));
vi.mock('vue-router', () => ({
  useRoute: () => ({
    name: 'builder',
    fullPath: '/builder',
    query: {},
    meta: { title: 'Builder' },
  }),
  useRouter: () => ({ replace: vi.fn(), push: vi.fn(), resolve: vi.fn() }),
  onBeforeRouteLeave: () => {},
}));

import Builder from '@/views/Builder.vue';
import { exitFocusMode } from '@/builder/focusMode.js';
import { setPlatform } from '@/builder/keymap.js';
import { useBuilderStore } from '@/builder/store.js';

globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
};

afterEach(() => {
  exitFocusMode(null);
  setPlatform(null);
});

describe('the header on a Mac', () => {
  test('names the keys in words in what only screen readers get', async () => {
    setPlatform('mac');
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

    const wrapper = mount(Builder, {
      attachTo: document.body,
      global: { plugins: [pinia] },
    });

    await flushPromises();

    const texts = Object.fromEntries(
      wrapper
        .findAll('[id^="header-tip-"]')
        .map((tip) => [tip.attributes('id'), tip.text()]),
    );

    expect(texts).toMatchObject({
      'header-tip-settings': 'Option+Shift+S',
      'header-tip-focus':
        'Hide the navigation bar and fill the screen (Shift+Command+F)',
    });

    await wrapper.get('[data-testid="drafts-focus-mode"]').trigger('click');
    texts.announcement = store.announcement;

    expect(texts.announcement).toBe(
      'Focus mode on: the navigation bar is hidden. Exit focus mode or ' +
        'Shift+Command+F shows it again.',
    );
    for (const [name, text] of Object.entries(texts)) {
      expect(text, name).not.toMatch(/[⌘⌥⇧⌃]/);
    }
  });
});
