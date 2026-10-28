// @vitest-environment happy-dom
// The header's counts are one Tab stop, which stays on a count when the
// count that held it goes away.

import { afterEach, describe, expect, test } from 'vitest';
import { enableAutoUnmount, mount } from '@vue/test-utils';
import { createPinia } from 'pinia';

import BuilderCounts from '@/components/builder/BuilderCounts.vue';
import { setLayers, setNodeLayer } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

enableAutoUnmount(afterEach);

describe('the counts’ Tab stop', () => {
  test('moves to the last count when the layers count goes away', async () => {
    const { doc, alpha } = sampleDocument();
    const layered = setNodeLayer(
      setLayers(doc, [{ id: 1, label: 'Field' }]),
      alpha.id,
      1,
    );
    const pinia = createPinia();
    const store = useBuilderStore(pinia);

    store.doc = layered;

    const wrapper = mount(BuilderCounts, {
      attachTo: document.body,
      global: { plugins: [pinia] },
    });
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
