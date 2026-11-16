// Custom icons in the store: nodes name icons, and the commit drops the
// copies a recorded document need not carry.

import { beforeEach, describe, expect, test } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { toRaw } from 'vue';

import { iconLibrary } from '@/builder/iconLibrary.js';
import { indexIcons, MAX_DOCUMENT_ICONS } from '@/builder/icons.js';
import { findNode, updateNode } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';

import { errorsOf, sampleDocument } from './fixtures.js';
import { base64Of, ICON_DATA, png } from './png.js';

// An icon of its own bytes.
function iconOf(index) {
  const bytes = png(2, 2, [index, 40, 80, 255]);

  return { name: `icon-${index}`, entry: { data: base64Of(bytes) } };
}

const PLC = { data: ICON_DATA };

// Makes the Builder's icon library hold these icons, as a read of it would.
function serverHas(icons) {
  iconLibrary.state.index = indexIcons(icons);
}

describe('custom icons in the store', () => {
  let store;
  let sample;
  // Every message announced, in order.
  let announced;

  beforeEach(() => {
    setActivePinia(createPinia());
    serverHas([]);
    store = useBuilderStore();
    sample = sampleDocument();
    store.setDocument(sample.doc);
    announced = [];
    store.$onAction(({ name, args }) => {
      if (name === 'announce') {
        announced.push(args[0]);
      }
    });
  });

  const iconOfNode = (id) => findNode(store.doc, id).device.icon;
  const use = (nodeId, icon, label = 'Changed the custom icon') =>
    store.commit(updateNode(store.doc, nodeId, { device: { icon } }), label);

  // A copy of the plc icon, which the alpha device names.
  function carryPlc() {
    const doc = {
      ...updateNode(store.doc, sample.alpha.id, { device: { icon: 'plc' } }),
      icons: { plc: PLC },
    };

    store.setDocument(doc);

    return doc;
  }

  test('a commit drops a copy the server holds under its name with the same bytes', () => {
    carryPlc();
    serverHas([{ name: 'PLC', data: ICON_DATA }]);

    const entry = use(sample.bravo.id, 'plc');

    expect('icons' in store.doc).toBe(false);
    expect(iconOfNode(sample.alpha.id)).toBe('plc');
    expect(iconOfNode(sample.bravo.id)).toBe('plc');
    // What is recorded, and so saved, is the settled document.
    expect(entry.snapshot).toBe(toRaw(store.doc));
    expect(announced.at(-1)).toBe('Changed the custom icon');
  });

  test('a copy carries its copies of icons into another diagram', () => {
    carryPlc();
    store.select({ nodes: [sample.alpha.id], edges: [] });
    store.copy();

    expect(store.clipboard.icons).toEqual({ plc: PLC });

    // Another diagram, in the same session: the clipboard stays.
    store.setDocument(sampleDocument().doc);
    expect('icons' in store.doc).toBe(false);

    const [pasted] = store.paste();

    expect(iconOfNode(pasted)).toBe('plc');
    expect(store.doc.icons).toEqual({ plc: PLC });
    expect(errorsOf(store.doc)).toEqual([]);
  });

  test('a paste into a diagram that carries 50 copies adds none, and the node keeps its icon', () => {
    carryPlc();
    store.select({ nodes: [sample.alpha.id], edges: [] });
    store.copy();

    const icons = Array.from({ length: MAX_DOCUMENT_ICONS }, (_, index) =>
      iconOf(index + 100),
    );
    // The copies are named by templates, so the document keeps them.
    const doc = {
      ...sampleDocument().doc,
      icons: Object.fromEntries(icons.map((icon) => [icon.name, icon.entry])),
      templates: icons.map((icon, index) => ({
        id: `bbbbbbbb-0000-4000-8000-${String(index).padStart(12, '0')}`,
        name: `T${index}`,
        device: {
          icon: icon.name,
          spec: { general: { hostname: `t-${index}` } },
        },
      })),
    };
    store.setDocument(doc);

    const [pasted] = store.paste();

    expect(iconOfNode(pasted)).toBe('plc');
    expect(Object.keys(store.doc.icons)).toHaveLength(MAX_DOCUMENT_ICONS);
    expect(Object.hasOwn(store.doc.icons, 'plc')).toBe(false);
  });

  test('the clipboard goes when the session ends', () => {
    carryPlc();
    store.select({ nodes: [sample.alpha.id], edges: [] });
    store.copy();

    store.endSession();

    expect(store.clipboard).toBeNull();
  });
});
