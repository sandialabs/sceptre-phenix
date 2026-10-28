// @vitest-environment happy-dom
// The header's connections count while the connections are hidden: it is
// struck through, says how to show them, and selects nothing.

import { afterEach, beforeEach, describe, expect, test } from 'vitest';
import { enableAutoUnmount, mount } from '@vue/test-utils';
import { createPinia } from 'pinia';

import BuilderCounts from '@/components/builder/BuilderCounts.vue';
import { connectionsHiddenText } from '@/builder/commands.js';
import { resetSettings, setSetting } from '@/builder/settings.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

enableAutoUnmount(afterEach);

function mountCounts() {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);

  store.doc = sampleDocument().doc;

  const wrapper = mount(BuilderCounts, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  return {
    wrapper,
    store,
    links: () => wrapper.get('[data-testid="count-links"]'),
  };
}

beforeEach(() => {
  resetSettings(null);
});

afterEach(() => {
  resetSettings(null);
});

describe('the connections count', () => {
  test('selects every connection while they are shown', async () => {
    const { store, links } = mountCounts();

    await links().trigger('click');

    expect(store.selection.edges).toEqual(
      store.doc.edges.map((edge) => edge.id),
    );
    expect(links().classes()).not.toContain('builder-counts__button--hidden');
    expect(links().text()).toBe('1 connection');
  });

  test('selects nothing while they are hidden, and says how to show them', async () => {
    const { wrapper, store, links } = mountCounts();
    const before = store.selection;

    setSetting('showConnections', false, null);
    await wrapper.vm.$nextTick();
    await links().trigger('click');

    expect(store.selection).toBe(before);
    expect(store.selection.edges).toEqual([]);
    expect(store.announcement).toBe(connectionsHiddenText());
    expect(links().classes()).toContain('builder-counts__button--hidden');
    expect(links().text()).toBe('1 connection, hidden');
    expect(
      document
        .getElementById(links().attributes('aria-describedby'))
        .textContent.trim(),
    ).toBe(connectionsHiddenText());
  });
});
