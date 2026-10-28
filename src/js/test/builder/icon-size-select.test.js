// @vitest-environment happy-dom
// The Icon size selects, mounted and chosen from: the diagram's, in the
// Diagram section, and a device's, in its form. A size chosen with the
// pointer applies at once, one stepped to with keys once the choice is made.

import { afterEach, describe, expect, test } from 'vitest';
import { enableAutoUnmount, flushPromises } from '@vue/test-utils';

import InspectorDiagram from '@/components/builder/inspector/InspectorDiagram.vue';

import { findNode, setIconSize } from '@/builder/model.js';

import { sampleDocument } from './fixtures.js';
import { actionsShown, fieldAt, mountInspector } from './inspector-mount.js';

enableAutoUnmount(afterEach);

// The Diagram section on `doc`, and its Icon size select.
async function openDiagram(doc) {
  const { wrapper, store } = await mountInspector({
    doc,
    component: InspectorDiagram,
  });

  return {
    wrapper,
    store,
    select: () => wrapper.get('[data-testid="inspector-icon-size-select"]'),
  };
}

describe('the Inspector’s diagram Icon size', () => {
  test('chosen with the pointer applies at once, and stepped to with keys once the choice is made', async () => {
    const { doc } = sampleDocument();
    const { store, select } = await openDiagram(doc);
    const steps = store.history.entries.length;
    const label = () => store.history.undoLabel();

    // A choice from the list with the pointer is one step at once.
    await select().trigger('pointerdown');
    await select().setValue('large');
    expect(store.doc.iconSize).toBe('large');
    expect(store.history.entries).toHaveLength(steps + 1);
    expect(label()).toBe("Changed the diagram's icon size to Large");

    // Each size the arrow keys step to is held. A modifier alone keeps it
    // held, and Enter applies the last, as one step.
    await select().trigger('keydown', { key: 'ArrowUp' });
    await select().setValue('medium');
    expect(store.doc.iconSize).toBe('large');
    await select().trigger('keydown', { key: 'ArrowUp' });
    await select().setValue('small');
    await select().trigger('keydown', { key: 'Shift' });
    expect(select().element.value).toBe('small');
    expect(store.doc.iconSize).toBe('large');
    expect(store.history.entries).toHaveLength(steps + 1);

    await select().trigger('keydown', { key: 'Enter' });
    expect(store.doc).not.toHaveProperty('iconSize');
    expect(store.history.entries).toHaveLength(steps + 2);
    expect(label()).toBe("Changed the diagram's icon size to Small");

    // Enter again has nothing held to apply.
    await select().trigger('keydown', { key: 'Enter' });
    expect(store.history.entries).toHaveLength(steps + 2);

    // Focus leaving the select applies the size held.
    await select().trigger('keydown', { key: 'ArrowDown' });
    await select().setValue('medium');
    expect(store.doc).not.toHaveProperty('iconSize');
    await select().trigger('blur');
    expect(store.doc.iconSize).toBe('medium');
    expect(store.history.entries).toHaveLength(steps + 3);
    expect(label()).toBe("Changed the diagram's icon size to Medium");

    // Undo puts the size before back.
    store.undo();
    expect(store.doc).not.toHaveProperty('iconSize');
  });

  test('a size chosen with the pointer after keys applies at once', async () => {
    const { doc } = sampleDocument();
    const { store, select } = await openDiagram(doc);

    await select().trigger('keydown', { key: 'ArrowDown' });
    await select().trigger('pointerdown');
    await select().setValue('large');
    expect(store.doc.iconSize).toBe('large');
    expect(store.history.undoLabel()).toBe(
      "Changed the diagram's icon size to Large",
    );
  });

  // The select's binding does not change when the document's size does
  // not, so the select is given that size again.
  test('a size the store refuses leaves the select showing the diagram’s size', async () => {
    const { doc } = sampleDocument();
    const { store, select } = await openDiagram(setIconSize(doc, 'medium'));
    const steps = store.history.entries.length;

    // While a conflict is resolved, a choice with the pointer.
    store.resolvingConflict = true;
    await select().trigger('pointerdown');
    await select().setValue('large');
    expect(store.doc.iconSize).toBe('medium');
    expect(select().element.value).toBe('medium');

    // A size stepped to with the keys shows while it is held. The draft
    // turns read only, and focus leaves before the select goes.
    store.resolvingConflict = false;
    await select().trigger('keydown', { key: 'ArrowDown' });
    await select().setValue('large');
    expect(select().element.value).toBe('large');
    expect(store.doc.iconSize).toBe('medium');

    const held = select().element;

    store.readOnly = true;
    held.dispatchEvent(new FocusEvent('blur'));
    expect(held.value).toBe('medium');
    expect(store.doc.iconSize).toBe('medium');
    expect(store.error).toBe('This draft is read only.');
    expect(store.history.entries).toHaveLength(steps);

    // A size taken leaves the select as it is: its binding follows.
    store.readOnly = false;
    await flushPromises();
    await select().trigger('pointerdown');
    await select().setValue('small');
    expect(select().element.value).toBe('small');
    expect(store.doc).not.toHaveProperty('iconSize');
  });
});

describe('a node’s Icon size in the Inspector', () => {
  test('of a device stepped to with keys is applied once the choice is made, Diagram default too', async () => {
    const { doc, alpha } = sampleDocument();
    const { wrapper, store } = await mountInspector({ doc, element: alpha });
    const select = () => fieldAt(wrapper, 'iconSize');
    const key = (name) => select().trigger('keydown', { key: name });
    const device = () => findNode(store.doc, alpha.id).device;
    const steps = store.history.entries.length;
    // Diagram default is the choice without a size.
    const [unset] = select()
      .findAll('option')
      .map((option) => option.element.value);

    await key('ArrowDown');
    await select().setValue('medium');
    await key('ArrowDown');
    await select().setValue('large');

    // Held: the device has no size of its own yet, and nothing waits for
    // Apply.
    expect('iconSize' in device()).toBe(false);
    expect(store.history.entries).toHaveLength(steps);
    expect(actionsShown(wrapper)).toBe(false);

    // Enter applies the last size, as one step.
    await key('Enter');
    expect(device().iconSize).toBe('large');
    expect(store.history.entries).toHaveLength(steps + 1);
    expect(store.history.undoLabel()).toBe(
      'Changed the icon size of Device alpha to Large',
    );

    // Diagram default, stepped to, is held as well, and Tab applies it: the
    // device draws the diagram's size again.
    await key('ArrowUp');
    await select().setValue(unset);
    expect(device().iconSize).toBe('large');
    expect(store.history.entries).toHaveLength(steps + 1);

    await key('Tab');
    expect('iconSize' in device()).toBe(false);
    expect(store.history.entries).toHaveLength(steps + 2);
    expect(store.history.undoLabel()).toBe(
      'Changed the icon size of Device alpha to the diagram default',
    );

    // A size chosen with no key before it is applied at once.
    await select().setValue('small');
    expect(device().iconSize).toBe('small');
    expect(store.history.entries).toHaveLength(steps + 3);
  });
});
