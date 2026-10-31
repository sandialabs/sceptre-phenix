// @vitest-environment happy-dom
// The layers editor says what an edit of the diagram's layers did in its
// own status region, and records it, so the page's live region does not say
// it again after the dialog closes. The saves to the library say the store's
// sentence first, then their warnings.

import { afterEach, describe, expect, test } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

import LayersDialog from '@/components/builder/dialogs/LayersDialog.vue';
import { unsaidPart } from '@/builder/announce.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

enableAutoUnmount(afterEach);

function mountDialog() {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);
  const { doc } = sampleDocument();

  store.doc = doc;
  store.history.reset(doc, 'Draft loaded');
  store.fetchLayerSets = async () => {};

  const wrapper = mount(LayersDialog, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  return { store, wrapper };
}

// The store's last announcement, as the page's live region holds it.
function lastAnnouncement(store) {
  return { text: store.announcement, seq: store.announcementSeq };
}

describe('the layers editor', () => {
  test('records what it says of an edit that the store announced', async () => {
    const pinia = createPinia();
    const store = useBuilderStore(pinia);
    const { doc } = sampleDocument();

    store.doc = doc;
    store.history.reset(doc, 'Draft loaded');
    store.fetchLayerSets = async () => {};

    const wrapper = mount(LayersDialog, {
      attachTo: document.body,
      global: { plugins: [pinia] },
    });

    await flushPromises();
    await wrapper.get('[data-testid="layer-add"]').trigger('click');
    await flushPromises();

    const said = wrapper.get('[data-testid="layers-status"]').text();
    const announced = {
      text: store.announcement,
      seq: store.announcementSeq,
    };

    expect(said).toMatch(/^Added /);
    expect(store.dialogSaid[0]).toEqual({ text: said, seq: announced.seq });
    expect(unsaidPart(announced, store.dialogSaid)).toBe('');
  });

  test('says all of what Save as new set announced, with its warnings', async () => {
    const { store, wrapper } = mountDialog();

    store.createLayerSet = async (content) => ({
      set: { ...content, id: 'mine', version: 1 },
      warnings: ['Icon ops was not added.'],
    });

    await flushPromises();
    await wrapper.get('[data-testid="layers-save-new"]').trigger('click');
    await flushPromises();
    await wrapper.get('[data-testid="layers-new-name"]').setValue('Mine');
    await wrapper.get('[data-testid="layers-new-set"]').trigger('submit');
    await flushPromises();

    const said = wrapper.get('[data-testid="layers-status"]').text();
    const announced = lastAnnouncement(store);

    expect(said).toBe(
      'Added layer set Mine, and linked the diagram to it. Icon ops was not added.',
    );
    expect(unsaidPart(announced, store.dialogSaid)).toBe('');
  });

  test('says all of what Save to layer set announced, with its warnings', async () => {
    const { store, wrapper } = mountDialog();
    const saved = { id: 'plant', name: 'Plant', version: 3 };

    store.saveToLayerSet = async () => ({
      entry: store.linkLayerSet(saved),
      set: saved,
      warnings: ['Icon ops was not added.'],
    });
    // The set's layers differ from the diagram's, so the editor offers Save
    // to layer set.
    store.layerSets = {
      ...store.layerSets,
      loaded: true,
      items: [
        {
          id: 'plant',
          name: 'Plant',
          version: 2,
          layers: [{ id: 1, label: 'Control' }],
        },
      ],
    };
    store.commit(
      { ...store.doc, layerSet: { source: { id: 'plant', version: 2 } } },
      '',
    );

    await flushPromises();
    await wrapper.get('[data-testid="layers-save-to-set"]').trigger('click');
    await flushPromises();

    const said = wrapper.get('[data-testid="layers-status"]').text();
    const announced = lastAnnouncement(store);

    expect(announced.text).toBe(
      'Saved the layers to layer set Plant, version 3',
    );
    expect(said).toBe(
      'Saved the layers to layer set Plant, version 3. Icon ops was not added.',
    );
    expect(unsaidPart(announced, store.dialogSaid)).toBe('');
  });
});
