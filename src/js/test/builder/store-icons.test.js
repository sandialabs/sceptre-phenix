// Custom icons in the store: nodes name icons, and the commit drops the
// copies a recorded document need not carry.

import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { toRaw } from 'vue';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

import { iconLibrary } from '@/builder/iconLibrary.js';
import { indexIcons, MAX_DOCUMENT_ICONS } from '@/builder/icons.js';
import { findNode, updateNode } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';
import { validateDocument } from '@/builder/validate.js';

import { sampleDocument } from './fixtures.js';
import { base64Of, ICON_DATA, png } from './png.js';

// An icon of its own bytes.
function iconOf(index) {
  const bytes = png(2, 2, [index, 40, 80, 255]);

  return { name: `icon-${index}`, entry: { data: base64Of(bytes) } };
}

const PLC = { data: ICON_DATA };
const OTHER = iconOf(1).entry;

const errorsOf = (doc) =>
  validateDocument(doc).filter((issue) => issue.level === 'error');

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

  test('a node names an icon, and the document carries no copy of it', () => {
    serverHas([{ name: 'plc', data: ICON_DATA }]);

    const entry = use(sample.alpha.id, 'plc');

    expect(iconOfNode(sample.alpha.id)).toBe('plc');
    expect('icons' in store.doc).toBe(false);
    // What is recorded, and so saved, is the settled document.
    expect(entry.snapshot).toBe(toRaw(store.doc));
    expect(store.history.current()).toBe(toRaw(store.doc));
    expect(errorsOf(store.doc)).toEqual([]);
    expect(announced).toEqual(['Changed the custom icon']);
  });

  // A name nothing resolves is the node's still: the canvas draws its
  // built-in icon, and the name resolves once the server has the icon.
  test('a node keeps a name the icon library does not have', () => {
    use(sample.alpha.id, 'not-uploaded');

    expect(iconOfNode(sample.alpha.id)).toBe('not-uploaded');
    expect(errorsOf(store.doc)).toEqual([]);
  });

  test('a commit drops a copy the server holds under its name with the same bytes', () => {
    carryPlc();
    serverHas([{ name: 'PLC', data: ICON_DATA }]);

    use(sample.bravo.id, 'plc');

    expect('icons' in store.doc).toBe(false);
    expect(iconOfNode(sample.alpha.id)).toBe('plc');
    expect(iconOfNode(sample.bravo.id)).toBe('plc');
  });

  test('a commit keeps a copy the server lacks, or holds with other bytes', () => {
    const doc = carryPlc();

    use(sample.bravo.id, 'plc');
    expect(store.doc.icons).toEqual({ plc: PLC });

    serverHas([{ name: 'plc', ...OTHER }]);
    use(sample.bravo.id, '', 'Removed the custom icon');
    expect(toRaw(store.doc.icons)).toEqual(doc.icons);
  });

  test('a copy no node names any more leaves the document, and Undo brings it back', () => {
    carryPlc();
    use(sample.alpha.id, '', 'Removed the custom icon');

    expect('icons' in store.doc).toBe(false);
    expect('icon' in findNode(store.doc, sample.alpha.id).device).toBe(false);

    // The history holds whole documents, copies included.
    store.undo();

    expect(store.doc.icons).toEqual({ plc: PLC });
    expect(iconOfNode(sample.alpha.id)).toBe('plc');

    store.redo();
    expect('icons' in store.doc).toBe(false);
  });

  test('deleting the last node that uses a copy removes it', () => {
    carryPlc();
    store.remove({ nodes: [sample.alpha.id], edges: [] });

    expect('icons' in store.doc).toBe(false);
    expect(errorsOf(store.doc)).toEqual([]);
  });

  test('an edit that names no icon leaves the document as it was given', () => {
    const next = updateNode(store.doc, sample.alpha.id, {
      position: { x: 8, y: 8 },
    });

    store.commit(next, 'Moved alpha');

    expect(toRaw(store.doc)).toBe(next);
    expect('icons' in store.doc).toBe(false);
  });

  test('a commit that is refused changes nothing', () => {
    store.readOnly = true;

    expect(use(sample.alpha.id, 'plc')).toBeNull();
    expect('icon' in findNode(store.doc, sample.alpha.id).device).toBe(false);
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

  test('a paste of a copy the server holds as it is carries none', () => {
    carryPlc();
    store.select({ nodes: [sample.alpha.id], edges: [] });
    store.copy();
    store.setDocument(sampleDocument().doc);
    serverHas([{ name: 'plc', data: ICON_DATA }]);

    const [pasted] = store.paste();

    expect(iconOfNode(pasted)).toBe('plc');
    expect('icons' in store.doc).toBe(false);
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
