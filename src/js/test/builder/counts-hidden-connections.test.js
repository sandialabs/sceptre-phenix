// @vitest-environment happy-dom
// The header's counts: one Tab stop that stays when its count goes away, and
// a struck-through connections count while the connections are hidden.

import { afterEach, beforeEach, describe, expect, test } from 'vitest';
import { mount } from '@vue/test-utils';
import { createPinia } from 'pinia';

import BuilderCounts from '@/components/builder/BuilderCounts.vue';
import { setPlatform } from '@/builder/keymap.js';
import { setLayers, setNodeLayer } from '@/builder/model.js';
import { resetSettings, setSetting } from '@/builder/settings.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

function mountCounts(doc = sampleDocument().doc) {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);

  store.doc = doc;

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
  setPlatform(null);
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

  // Screen readers get the keys in words, also on a Mac.
  test('selects nothing while they are hidden, and says how to show them', async () => {
    setPlatform('mac');
    const { wrapper, store, links } = mountCounts();
    const before = store.selection;
    const spoken = 'Connections are hidden. Press Option+Shift+C to show them.';

    setSetting('showConnections', false, null);
    await wrapper.vm.$nextTick();
    await links().trigger('click');

    expect(store.selection).toBe(before);
    expect(store.selection.edges).toEqual([]);
    expect(store.announcement).toBe(spoken);
    expect(links().classes()).toContain('builder-counts__button--hidden');
    expect(links().text()).toBe('1 connection, hidden');
    expect(
      document
        .getElementById(links().attributes('aria-describedby'))
        .textContent.trim(),
    ).toBe(spoken);
  });
});

describe('the counts’ Tab stop', () => {
  test('moves to the last count when the layers count goes away', async () => {
    const { doc, alpha } = sampleDocument();
    const layered = setNodeLayer(
      setLayers(doc, [{ id: 1, label: 'Field' }]),
      alpha.id,
      1,
    );
    const { wrapper, store } = mountCounts(layered);
    const stops = () =>
      wrapper
        .findAll('.builder-counts__button')
        .filter((button) => button.attributes('tabindex') === '0')
        .map((button) => button.attributes('data-testid'));

    await wrapper.get('[data-testid="count-layers"]').trigger('focus');
    expect(stops()).toEqual(['count-layers']);

    store.doc = setNodeLayer(layered, alpha.id, null);
    await wrapper.vm.$nextTick();

    expect(wrapper.find('[data-testid="count-layers"]').exists()).toBe(false);
    expect(stops()).toEqual(['count-notes']);
  });
});
