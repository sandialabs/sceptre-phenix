// @vitest-environment happy-dom
// What the Inspector's buttons do, mounted and pressed: Cancel, a connection
// point's Disconnect and Remove, Collapse all and Expand all, and the notes
// of the Diagram section (Add note, a note's box, Delete).

import { afterEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises } from '@vue/test-utils';

import InspectorDiagram from '@/components/builder/inspector/InspectorDiagram.vue';
import { closeSections } from '@/components/builder/inspector/InspectorSectionRenderer.vue';

import { findNode, metadataOf } from '@/builder/model.js';

import { sampleDocument } from './fixtures.js';
import {
  actionsShown,
  fieldAt,
  mountInspector,
  type,
} from './inspector-mount.js';

enableAutoUnmount(afterEach);
afterEach(closeSections);

// The Inspector on the sample's alpha device.
async function openAlpha() {
  const sample = sampleDocument();
  const opened = await mountInspector({
    doc: sample.doc,
    element: sample.alpha,
  });

  return { ...opened, ...sample };
}

// Focuses `button` and clicks it, as a press does.
async function press(button) {
  button.element.focus();
  await button.trigger('click');
  await flushPromises();
}

describe('Cancel', () => {
  test('discards the working copy, puts the fields back, and focus goes to the field edited last', async () => {
    const { wrapper, store, alpha } = await openAlpha();
    const announce = vi.spyOn(store, 'announce');
    const description = () => fieldAt(wrapper, 'spec.general.description');
    const hostname = () => fieldAt(wrapper, 'hostname');
    const before = store.doc;

    description().element.focus();
    await description().setValue('Edited');
    hostname().element.focus();
    await hostname().setValue('alpha-2');
    expect(actionsShown(wrapper)).toBe(true);

    await press(wrapper.get('[data-testid="inspector-cancel"]'));

    expect(description().element.value).toBe('');
    expect(hostname().element.value).toBe('alpha');
    expect(actionsShown(wrapper)).toBe(false);
    expect(announce).toHaveBeenCalledWith('Discarded unapplied changes.');
    expect(document.activeElement).toBe(hostname().element);
    expect(store.doc).toBe(before);
    expect(findNode(store.doc, alpha.id).device.hostname).toBe('alpha');
  });

  test('keeps a look, which was applied at once', async () => {
    const { wrapper, store, alpha } = await openAlpha();
    const device = () => findNode(store.doc, alpha.id).device;

    await fieldAt(wrapper, 'fillColor').setValue('#2f6fbf');

    const kept = device().spec.general?.description;

    await fieldAt(wrapper, 'spec.general.description').setValue('Edited');
    await press(wrapper.get('[data-testid="inspector-cancel"]'));

    expect(device().fillColor).toBe('#2f6fbf');
    expect(fieldAt(wrapper, 'fillColor').element.value).toBe('#2f6fbf');
    expect(device().spec.general?.description).toBe(kept);
    expect(kept).not.toBe('Edited');
    expect(store.history.undoLabel()).toBe(
      'Changed the fill color of Device alpha to #2f6fbf',
    );
  });

  test('Apply held by focus with nothing changed says there is nothing to discard', async () => {
    const { wrapper, store } = await openAlpha();
    const announce = vi.spyOn(store, 'announce');
    const memory = fieldAt(wrapper, 'spec.hardware.memory');

    // Memory's default with a space: Apply shows, and the change is none.
    memory.element.focus();
    await type(memory, `${memory.element.value} `);
    await press(wrapper.get('[data-testid="inspector-cancel"]'));

    expect(announce).toHaveBeenCalledWith('No changes to discard.');
    expect(actionsShown(wrapper)).toBe(false);
  });
});

describe('the connection points', () => {
  const removeButton = (wrapper, name) =>
    wrapper.get(`[aria-label="Remove connection point ${name}"]`);
  const names = (wrapper) =>
    wrapper.findAll('.builder-inspector__iface-name').map((row) => row.text());

  test('Disconnect removes the connection, keeps the connection point, and focus goes to its Remove', async () => {
    const { wrapper, store, alpha } = await openAlpha();
    const handle = alpha.device.interfaces[0];

    expect(names(wrapper)).toEqual(['eth0 — network EXP']);
    await press(wrapper.get('[aria-label="Disconnect eth0 from network EXP"]'));

    expect(
      store.doc.edges.some(
        (edge) =>
          edge.sourceHandleId === handle.id ||
          edge.targetHandleId === handle.id,
      ),
    ).toBe(false);
    expect(findNode(store.doc, alpha.id).device.interfaces).toEqual([handle]);
    expect(names(wrapper)).toEqual(['eth0 — not connected']);
    expect(wrapper.find('[data-testid="inspector-disconnect"]').exists()).toBe(
      false,
    );
    expect(document.activeElement).toBe(removeButton(wrapper, 'eth0').element);
  });

  test('Remove takes the connection point away, and focus goes to the next, or to Add connection point', async () => {
    const { wrapper, store, alpha } = await openAlpha();
    const add = wrapper.get('[data-testid="inspector-add-interface"]');
    const interfaces = () =>
      findNode(store.doc, alpha.id).device.interfaces.map(
        (handle) => handle.name,
      );

    await press(add);
    expect(interfaces()).toEqual(['eth0', 'eth1']);

    await press(removeButton(wrapper, 'eth0'));
    expect(interfaces()).toEqual(['eth1']);
    expect(store.doc.edges).toEqual([]);
    expect(document.activeElement).toBe(removeButton(wrapper, 'eth1').element);

    await press(removeButton(wrapper, 'eth1'));
    expect(interfaces()).toEqual([]);
    expect(wrapper.text()).toContain('No connection points.');
    expect(document.activeElement).toBe(add.element);
  });

  test('the last row removed leaves focus on the row before it', async () => {
    const { wrapper, alpha, store } = await openAlpha();

    await press(wrapper.get('[data-testid="inspector-add-interface"]'));
    await press(removeButton(wrapper, 'eth1'));

    expect(
      findNode(store.doc, alpha.id).device.interfaces.map((h) => h.name),
    ).toEqual(['eth0']);
    expect(document.activeElement).toBe(removeButton(wrapper, 'eth0').element);
  });
});

describe('Collapse all and Expand all', () => {
  test('collapse every section, list and entry, and expand them again', async () => {
    const { wrapper } = await openAlpha();
    const states = () =>
      wrapper
        .findAll('[data-collapse-path]')
        .map((entry) =>
          entry
            .get('[data-testid="inspector-disclosure"]')
            .attributes('aria-expanded'),
        );
    const opened = () =>
      wrapper.findAll('details').map((section) => section.element.open);

    expect(states().length).toBeGreaterThan(3);
    expect(new Set(states())).toEqual(new Set(['true']));
    // More settings starts closed: it is opened first.
    await wrapper
      .get('[data-testid="inspector-section"] > summary')
      .trigger('click');
    expect(opened()).toEqual([true]);

    wrapper.vm.collapseAll();
    await flushPromises();
    expect(new Set(states())).toEqual(new Set(['false']));
    expect(opened()).toEqual([false]);
    expect(wrapper.vm.collapsedState().paths.length).toBe(states().length);

    wrapper.vm.expandAll();
    await flushPromises();
    expect(new Set(states())).toEqual(new Set(['true']));
    expect(opened()).toEqual([true]);
  });
});

describe('the Diagram section’s notes', () => {
  async function openNotes(notes) {
    const { doc } = sampleDocument();
    const { wrapper, store } = await mountInspector({
      doc: notes ? { ...doc, metadata: { ...doc.metadata, notes } } : doc,
      component: InspectorDiagram,
    });

    return {
      wrapper,
      store,
      box: (n) => wrapper.get(`[data-testid="inspector-note-${n}"]`),
      add: () => wrapper.get('[data-testid="inspector-note-add"]'),
      notes: () => metadataOf(store.doc).notes,
    };
  }

  test('Add note puts an empty box at the end with focus, and its text is written as it changes', async () => {
    const { wrapper, store, box, add, notes } = await openNotes();
    const steps = store.history.entries.length;

    expect(wrapper.get('[data-testid="inspector-notes"]').text()).toContain(
      'No notes.',
    );
    await press(add());

    expect(box(1).attributes('aria-label')).toBe('Note 1');
    expect(box(1).element.value).toBe('');
    expect(document.activeElement).toBe(box(1).element);
    // Not written until it holds text.
    expect(notes()).toBeUndefined();

    await box(1).setValue('Reset the PLC first.');
    expect(notes()).toEqual(['Reset the PLC first.']);
    expect(store.history.entries).toHaveLength(steps + 1);
    expect(store.history.undoLabel()).toBe('Updated diagram notes');
  });

  test('Delete takes a note away as one step, and focus goes to the box now in its place', async () => {
    const { wrapper, store, box, notes } = await openNotes(['First', 'Second']);
    const steps = store.history.entries.length;
    const remove = wrapper.get('[data-testid="inspector-note-delete-1"]');

    expect(remove.text()).toMatch(/^Delete\s+note 1$/);
    await press(remove);

    expect(notes()).toEqual(['Second']);
    expect(store.history.entries).toHaveLength(steps + 1);
    expect(box(1).element.value).toBe('Second');
    expect(document.activeElement).toBe(box(1).element);
  });

  test('a box left empty takes its row, and focus goes to Add note when none is left', async () => {
    const { wrapper, store, box, add, notes } = await openNotes();
    const steps = store.history.entries.length;

    await press(add());
    // Focus has moved to the row's Delete button as the box changes.
    wrapper.get('[data-testid="inspector-note-delete-1"]').element.focus();
    await box(1).trigger('change');
    await flushPromises();

    expect(wrapper.find('[data-testid="inspector-note-1"]').exists()).toBe(
      false,
    );
    expect(notes()).toBeUndefined();
    expect(store.history.entries).toHaveLength(steps);
    expect(document.activeElement).toBe(add().element);
  });

  // A diagram from a file can hold an empty list of notes, which the first
  // write of the notes drops.
  test('a box emptied takes its row also in a diagram whose notes are an empty list', async () => {
    const { wrapper, box, add, notes } = await openNotes([]);

    // Typed, then emptied, so the box changes.
    await press(add());
    await type(box(1), 'x');
    await box(1).setValue('');

    expect(wrapper.find('[data-testid="inspector-note-1"]').exists()).toBe(
      false,
    );
    expect(notes()).toBeUndefined();
  });

  test('text the server would refuse shows an error, and the diagram keeps the note as saved', async () => {
    const { wrapper, box, notes } = await openNotes(['Saved']);

    await box(1).setValue('Bad \u0001 text');

    const error = wrapper.get('[data-testid="inspector-note-error-1"]');

    expect(error.text()).toBe(
      'A note cannot hold control characters other than newline and tab. Builder does not save it until you remove them.',
    );
    expect(error.attributes('role')).toBe('alert');
    expect(box(1).attributes('aria-invalid')).toBe('true');
    expect(box(1).attributes('aria-describedby')).toBe(error.attributes('id'));
    expect(box(1).element.value).toBe('Bad \u0001 text');
    expect(notes()).toEqual(['Saved']);

    await box(1).setValue('Fixed');
    expect(
      wrapper.find('[data-testid="inspector-note-error-1"]').exists(),
    ).toBe(false);
    expect(box(1).attributes('aria-invalid')).toBeUndefined();
    expect(notes()).toEqual(['Fixed']);
  });
});
