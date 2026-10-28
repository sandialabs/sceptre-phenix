// @vitest-environment happy-dom
// Add a connection and Move to a group, mounted with a real Builder store:
// where focus goes when a field is missing, and what a valid submit changes.

import { afterEach, describe, expect, test } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, mount } from '@vue/test-utils';

import ConnectDialog from '@/components/builder/dialogs/ConnectDialog.vue';
import RegroupDialog from '@/components/builder/dialogs/RegroupDialog.vue';
import { findNode, groupNodes } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

enableAutoUnmount(afterEach);

// Opens `component` in the page over `doc`.
function open(component, doc) {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);

  store.doc = doc;

  const wrapper = mount(component, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  return { wrapper, store, before: store.doc };
}

const optionTexts = (select) =>
  select.findAll('option').map((option) => option.text());

describe('Add a connection', () => {
  test('Connect with a field empty names it, marks it, and moves focus to it', async () => {
    const sample = sampleDocument();
    const { wrapper, store, before } = open(ConnectDialog, sample.doc);
    const device = wrapper.get('#connect-device');

    await wrapper.get('form').trigger('submit');

    expect(wrapper.get('[role="alert"]').text()).toBe(
      'Choose a device and a switch.',
    );
    expect(device.attributes('aria-invalid')).toBe('true');
    expect(device.attributes('aria-describedby')).toBe('connect-error');
    expect(document.activeElement.id).toBe('connect-device');

    // With the device chosen, the message asks only for the switch.
    await device.setValue(sample.bravo.id);
    expect(wrapper.get('[role="alert"]').text()).toBe('Choose a switch.');
    expect(device.attributes('aria-invalid')).toBeUndefined();

    await wrapper.get('form').trigger('submit');

    expect(document.activeElement.id).toBe('connect-switch');
    expect(store.doc).toBe(before);
    expect(wrapper.emitted('close')).toBeUndefined();
  });

  test('choosing a device lists its free interfaces, and clears the interface of the last one', async () => {
    const sample = sampleDocument();
    const { wrapper } = open(ConnectDialog, sample.doc);
    const device = wrapper.get('#connect-device');
    const handle = wrapper.get('#connect-interface');
    const [bravoEth0] = sample.bravo.device.interfaces;

    await device.setValue(sample.bravo.id);
    expect(optionTexts(handle)).toEqual(['Add a new interface', 'eth0']);
    await handle.setValue(bravoEth0.id);
    expect(handle.element.value).toBe(bravoEth0.id);

    // alpha's eth0 is connected already, so only a new interface is offered.
    await device.setValue(sample.alpha.id);
    expect(optionTexts(handle)).toEqual(['Add a new interface']);
    expect(handle.element.value).toBe('');

    await device.setValue(sample.bravo.id);
    expect(handle.element.value).toBe('');
  });

  test('a valid connection is made in the diagram, and the dialog closes', async () => {
    const sample = sampleDocument();
    const { wrapper, store } = open(ConnectDialog, sample.doc);
    const [bravoEth0] = sample.bravo.device.interfaces;

    await wrapper.get('#connect-device').setValue(sample.bravo.id);
    await wrapper.get('#connect-interface').setValue(bravoEth0.id);
    await wrapper.get('#connect-switch').setValue(sample.sw.id);
    await wrapper.get('[data-testid="connect-dialog-submit"]').trigger('click');

    expect(store.doc.edges).toHaveLength(sample.doc.edges.length + 1);
    expect(store.doc.edges).toContainEqual(
      expect.objectContaining({
        sourceNodeId: sample.bravo.id,
        sourceHandleId: bravoEth0.id,
        targetNodeId: sample.sw.id,
      }),
    );
    expect(wrapper.find('[data-testid="connect-error"]').exists()).toBe(false);
    expect(wrapper.emitted('close')).toHaveLength(1);
  });
});

describe('Move to a group', () => {
  // The sample diagram with alpha in a group.
  function grouped() {
    const sample = sampleDocument();
    const { doc, group } = groupNodes(sample.doc, [sample.alpha.id], {
      title: 'Plant',
    });

    return { ...sample, doc, group };
  }

  test('Move with no node says so, marks Node, and moves focus to it', async () => {
    const { doc } = grouped();
    const { wrapper, store, before } = open(RegroupDialog, doc);
    const node = wrapper.get('#regroup-node');

    await wrapper.get('form').trigger('submit');

    expect(wrapper.get('[role="alert"]').text()).toBe('Choose a node.');
    expect(node.attributes('aria-invalid')).toBe('true');
    expect(node.attributes('aria-describedby')).toBe('regroup-error');
    expect(document.activeElement.id).toBe('regroup-node');
    expect(store.doc).toBe(before);
    expect(wrapper.emitted('close')).toBeUndefined();
  });

  test('Group follows the chosen node, a move that changes nothing says so, and a valid one moves it and closes', async () => {
    const { doc, group, alpha, bravo } = grouped();
    const { wrapper, store, before } = open(RegroupDialog, doc);
    const groupField = wrapper.get('#regroup-group');

    await wrapper.get('#regroup-node').setValue(alpha.id);
    expect(groupField.element.value).toBe(group.id);

    await wrapper.get('form').trigger('submit');
    expect(wrapper.get('[role="alert"]').text()).toMatch(
      /^alpha.* is in Plant already\.$/,
    );
    expect(store.doc).toBe(before);

    await wrapper.get('#regroup-node').setValue(bravo.id);
    expect(groupField.element.value).toBe('');
    expect(wrapper.find('[data-testid="regroup-error"]').exists()).toBe(false);

    await groupField.setValue(group.id);
    await wrapper.get('[data-testid="regroup-dialog-submit"]').trigger('click');

    expect(findNode(store.doc, bravo.id).parentId).toBe(group.id);
    expect(wrapper.emitted('close')).toHaveLength(1);
  });
});
