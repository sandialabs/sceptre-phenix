// @vitest-environment happy-dom
// A drop on the canvas adds the palette entry it carries, and ignores a
// kind the palette does not send.

import { afterEach, beforeEach, describe, expect, test } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

import BuilderCanvas from '@/components/builder/BuilderCanvas.vue';
import { PALETTE_MIME } from '@/components/builder/paletteDnd.js';
import { useBuilderStore } from '@/builder/store.js';

enableAutoUnmount(afterEach);

globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
};

let pinia;
let store;

beforeEach(() => {
  pinia = createPinia();
  store = useBuilderStore(pinia);
  store.newDocument({ name: 'Drops' });
});

async function drop(kind) {
  const wrapper = mount(BuilderCanvas, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  await flushPromises();
  await wrapper.get('[data-testid="builder-canvas"]').trigger('drop', {
    clientX: 200,
    clientY: 150,
    dataTransfer: {
      getData: (type) => (type === PALETTE_MIME ? kind : ''),
    },
  });
}

describe('a drop on the canvas', () => {
  test('adds the palette entry it carries', async () => {
    await drop('circle');

    expect(store.doc.nodes.map((node) => node.kind)).toEqual(['shape']);
    expect(store.doc.nodes[0].shape.shape).toBe('circle');
  });

  test('ignores a kind the palette does not send', async () => {
    await drop('router');

    expect(store.doc.nodes ?? []).toEqual([]);
  });
});
