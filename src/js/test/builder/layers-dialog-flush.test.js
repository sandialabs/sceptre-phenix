// @vitest-environment happy-dom
// Save and Done write the typed text of the layer rows first, with no wait.
// A new node matcher with one field typed stops them and gets focus.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

import LayersDialog from '@/components/builder/dialogs/LayersDialog.vue';
import LayerRows from '@/components/builder/layers/LayerRows.vue';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

enableAutoUnmount(afterEach);

const PLANT = {
  id: 'plant',
  name: 'Plant',
  description: '',
  iconKey: '',
  icon: '',
  matchConflict: 'lowest',
  layerOutlines: false,
  builtin: false,
  version: 1,
  etag: '"1"',
  layers: [
    { id: 2, label: 'Control' },
    { id: 1, label: 'Field' },
  ],
};

function editSet(set = PLANT) {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);

  store.layerSets = {
    ...store.layerSets,
    status: 'ready',
    loaded: true,
    items: [set],
  };
  store.saveLayerSet = vi.fn(async (id, content) => ({
    ...content,
    id,
    version: 2,
  }));

  const wrapper = mount(LayersDialog, {
    props: { mode: 'library', setId: set.id },
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  return { store, wrapper };
}

const rowOf = (wrapper, id) => wrapper.get(`[data-layer-id="${id}"]`);
const field = (wrapper, id, testid) =>
  rowOf(wrapper, id).get(`[data-testid="${testid}"]`);

async function addMatcher(wrapper, id) {
  await field(wrapper, id, 'layer-matcher-add').trigger('click');
  await flushPromises();

  return {
    path: field(wrapper, id, 'layer-matcher-path'),
    pattern: field(wrapper, id, 'layer-matcher-pattern'),
  };
}

async function save(wrapper) {
  await wrapper.get('[data-testid="layers-save"]').trigger('click');
  await flushPromises();
}

// The layer of that ID in the content the last Save sent.
function savedLayer(store, id) {
  const [, content] = store.saveLayerSet.mock.calls.at(-1);

  return content.layers.find((layer) => layer.id === id);
}

describe('Save in the editor of a layer set', () => {
  test('writes a node matcher typed with input events only', async () => {
    const { store, wrapper } = editSet();
    const { path, pattern } = await addMatcher(wrapper, 2);

    await path.setValue('general.hostname');
    await pattern.setValue('^plc-');
    await save(wrapper);

    expect(store.saveLayerSet).toHaveBeenCalledTimes(1);
    expect(savedLayer(store, 2).matchers).toEqual({
      'general.hostname': '^plc-',
    });
    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  test('writes a node matcher whose change fired in the same task as Save', async () => {
    const { store, wrapper } = editSet();
    const { path, pattern } = await addMatcher(wrapper, 2);

    await path.setValue('general.hostname');
    path.element.dispatchEvent(new Event('change'));
    await pattern.setValue('^plc-');
    pattern.element.dispatchEvent(new Event('change'));
    wrapper.get('[data-testid="layers-save"]').element.click();
    await flushPromises();
    // The change that waited for Tab does nothing after Save wrote it.
    await new Promise((resolve) => setTimeout(resolve));

    expect(store.saveLayerSet).toHaveBeenCalledTimes(1);
    expect(savedLayer(store, 2).matchers).toEqual({
      'general.hostname': '^plc-',
    });
    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  test.each([
    ['path', 'general.hostname', 'pattern', 'Type a regular expression'],
    ['pattern', '^plc-', 'path', 'Type a field'],
  ])(
    'stops at a new node matcher with only its %s typed, and focuses the other field',
    async (typedField, text, emptyField, message) => {
      const { store, wrapper } = editSet();
      const fields = await addMatcher(wrapper, 2);

      await fields[typedField].setValue(text);
      await save(wrapper);

      const empty = fields[emptyField];

      expect(store.saveLayerSet).not.toHaveBeenCalled();
      expect(wrapper.emitted('close')).toBeUndefined();
      expect(empty.attributes('aria-invalid')).toBe('true');
      expect(document.activeElement).toBe(empty.element);
      expect(
        field(wrapper, 2, `layer-matcher-${emptyField}-error`).text(),
      ).toContain(message);
      expect(wrapper.get('[data-testid="layers-error"]').text()).toBe(
        'Some layer fields have errors. Correct them. Then select Save.',
      );

      // The field typed now is written, and Save goes on.
      await empty.setValue(emptyField === 'path' ? 'general.hostname' : '^x');
      await save(wrapper);
      expect(store.saveLayerSet).toHaveBeenCalledTimes(1);
      expect(Object.keys(savedLayer(store, 2).matchers)).toEqual([
        'general.hostname',
      ]);
    },
  );

  test('drops a new node matcher with both fields empty', async () => {
    const { store, wrapper } = editSet();

    await addMatcher(wrapper, 2);
    await save(wrapper);

    expect(store.saveLayerSet).toHaveBeenCalledTimes(1);
    expect(savedLayer(store, 2).matchers).toBeUndefined();
    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  test('writes two new node matchers of one layer, and a changed one', async () => {
    const { store, wrapper: again } = editSet({
      ...PLANT,
      layers: [
        { id: 2, label: 'Control', matchers: { 'general.hostname': '^plc-' } },
        { id: 1, label: 'Field' },
      ],
    });

    await field(again, 2, 'layer-matcher-pattern').setValue('^rtu-');
    await field(again, 2, 'layer-matcher-add').trigger('click');
    await flushPromises();
    await field(again, 2, 'layer-matcher-add').trigger('click');
    await flushPromises();

    const paths = rowOf(again, 2).findAll('[data-testid="layer-matcher-path"]');
    const patterns = rowOf(again, 2).findAll(
      '[data-testid="layer-matcher-pattern"]',
    );

    await paths[1].setValue('general.role');
    await patterns[1].setValue('^ctl');
    await paths[2].setValue('general.site');
    await patterns[2].setValue('^a$');
    await save(again);

    expect(store.saveLayerSet).toHaveBeenCalledTimes(1);
    expect(savedLayer(store, 2).matchers).toEqual({
      'general.hostname': '^rtu-',
      'general.role': '^ctl',
      'general.site': '^a$',
    });
  });

  test('writes a new layer ID last, after the label and matcher of its row', async () => {
    const { store, wrapper } = editSet();
    const { path, pattern } = await addMatcher(wrapper, 2);

    await path.setValue('general.hostname');
    await pattern.setValue('^plc-');
    await field(wrapper, 2, 'layer-label').setValue('Plant floor');
    await field(wrapper, 2, 'layer-id').setValue('5');
    await save(wrapper);

    expect(store.saveLayerSet).toHaveBeenCalledTimes(1);
    expect(savedLayer(store, 2)).toBeUndefined();
    expect(savedLayer(store, 5)).toEqual({
      id: 5,
      label: 'Plant floor',
      matchers: { 'general.hostname': '^plc-' },
    });
  });

  test('writes a typed color, and stops at a color that is not one', async () => {
    const { store, wrapper } = editSet();

    await field(wrapper, 1, 'layer-color').setValue('#2F6FBF');
    await field(wrapper, 2, 'layer-color').setValue('#zz');
    await save(wrapper);

    const bad = field(wrapper, 2, 'layer-color');

    expect(store.saveLayerSet).not.toHaveBeenCalled();
    expect(bad.attributes('aria-invalid')).toBe('true');
    expect(document.activeElement).toBe(bad.element);

    await bad.setValue('');
    await save(wrapper);

    expect(store.saveLayerSet).toHaveBeenCalledTimes(1);
    expect(savedLayer(store, 1).color).toBe('#2f6fbf');
    expect(savedLayer(store, 2).color).toBeUndefined();
  });
});

describe('Done in the editor of a diagram', () => {
  function editDiagram() {
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

  test('writes a node matcher typed with input events only, then closes', async () => {
    const { store, wrapper } = editDiagram();
    const id = Number(
      wrapper.get('[data-testid="layer-row"]').attributes('data-layer-id'),
    );
    const { path, pattern } = await addMatcher(wrapper, id);

    await path.setValue('general.hostname');
    await pattern.setValue('^plc-');
    await wrapper.get('[data-testid="layers-done"]').trigger('click');
    await flushPromises();

    expect(store.doc.layers.find((layer) => layer.id === id).matchers).toEqual({
      'general.hostname': '^plc-',
    });
    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  test('stays open at a new node matcher with one field typed', async () => {
    const { wrapper } = editDiagram();
    const id = Number(
      wrapper.get('[data-testid="layer-row"]').attributes('data-layer-id'),
    );
    const { path, pattern } = await addMatcher(wrapper, id);

    await path.setValue('general.hostname');
    await wrapper.get('[data-testid="layers-done"]').trigger('click');
    await flushPromises();

    expect(wrapper.emitted('close')).toBeUndefined();
    expect(pattern.attributes('aria-invalid')).toBe('true');
    expect(document.activeElement).toBe(pattern.element);
  });
});

describe('flush of the layer rows', () => {
  test('a matcher that the layers refuse gets an error, which Remove clears', async () => {
    const ops = {
      add: () => null,
      renumber: () => '',
      update: () => '',
      remove: () => '',
    };
    const wrapper = mount(LayerRows, {
      props: { layers: [{ id: 1, label: 'One' }], ops },
      attachTo: document.body,
    });

    await wrapper.get('[data-testid="layer-matcher-add"]').trigger('click');
    await wrapper
      .get('[data-testid="layer-matcher-path"]')
      .setValue('general.hostname');
    await wrapper.get('[data-testid="layer-matcher-pattern"]').setValue('^a');

    expect(await wrapper.vm.flush()).toBe(true);
    expect(wrapper.get('[data-testid="layer-matcher-path-error"]').text()).toBe(
      'Layer 1 cannot take this node matcher. Change it, or remove it.',
    );

    await wrapper.get('[data-testid="layer-matcher-remove"]').trigger('click');
    await flushPromises();

    expect(await wrapper.vm.flush()).toBe(false);
    expect(wrapper.find('[data-testid="layer-matcher"]').exists()).toBe(false);
  });

  test('Remove layer clears the errors of the node matchers of that layer', async () => {
    const ops = {
      add: () => null,
      renumber: () => '',
      update: () => '',
      remove: () => 'Removed layer 1.',
    };
    const wrapper = mount(LayerRows, {
      props: { layers: [{ id: 1, label: 'One' }], ops },
      attachTo: document.body,
    });

    await wrapper.get('[data-testid="layer-matcher-add"]').trigger('click');
    await wrapper
      .get('[data-testid="layer-matcher-path"]')
      .setValue('general.hostname');

    expect(await wrapper.vm.flush()).toBe(true);
    expect(
      wrapper
        .get('[data-testid="layer-matcher-pattern"]')
        .attributes('aria-invalid'),
    ).toBe('true');

    await wrapper.get('[data-testid="layer-remove"]').trigger('click');
    await flushPromises();
    await wrapper.setProps({ layers: [] });

    expect(await wrapper.vm.flush()).toBe(false);
  });
});
