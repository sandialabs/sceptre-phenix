// Custom icons in the store: the icon shelf, and the commit that makes
// every recorded document carry exactly the icons it uses.

import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { toRaw } from 'vue';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

import { iconId, MAX_DOCUMENT_ICONS } from '@/builder/icons.js';
import { findNode, updateNode } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';
import { validateDocument } from '@/builder/validate.js';

import { sampleDocument } from './fixtures.js';
import { base64Of, ICON_DATA, ICON_KEY, png } from './png.js';

// An icon of its own bytes.
function iconOf(index) {
  const bytes = png(2, 2, [index, 40, 80, 255]);
  const data = base64Of(bytes);

  return { id: iconId(bytes), entry: { name: `icon ${index}`, data } };
}

const PLC = { name: 'plc', data: ICON_DATA };

const errorsOf = (doc) =>
  validateDocument(doc).filter((issue) => issue.level === 'error');

describe('custom icons in the store', () => {
  let store;
  let sample;
  // Every message announced, in order: two made in one edit are both read.
  let announced;

  beforeEach(() => {
    setActivePinia(createPinia());
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

  test('a commit copies an icon the document comes to name from the shelf', () => {
    store.shelveIcons({ [ICON_KEY]: PLC });

    const entry = use(sample.alpha.id, ICON_KEY);

    expect(store.doc.icons).toEqual({ [ICON_KEY]: PLC });
    expect(iconOfNode(sample.alpha.id)).toBe(ICON_KEY);
    // What is recorded, and so saved, is the settled document.
    expect(entry.snapshot).toBe(toRaw(store.doc));
    expect(store.history.current()).toBe(toRaw(store.doc));
    expect(errorsOf(store.doc)).toEqual([]);
    expect(announced).toEqual(['Changed the custom icon']);
  });

  test('a second node takes the icon the document already carries', () => {
    store.shelveIcons({ [ICON_KEY]: PLC });
    use(sample.alpha.id, ICON_KEY);

    const carried = store.doc.icons;

    // Nothing is on the shelf now: the document's own copy is enough.
    store.iconShelf.clear();
    use(sample.bravo.id, ICON_KEY);

    expect(toRaw(store.doc.icons)).toBe(toRaw(carried));
    expect(iconOfNode(sample.bravo.id)).toBe(ICON_KEY);
  });

  test('an icon no node names any more leaves the document, and Undo brings it back', () => {
    store.shelveIcons(new Map([[ICON_KEY, PLC]]));
    use(sample.alpha.id, ICON_KEY);
    use(sample.alpha.id, '', 'Removed the custom icon');

    expect('icons' in store.doc).toBe(false);
    expect('icon' in findNode(store.doc, sample.alpha.id).device).toBe(false);

    // The history holds whole documents, icons included: the shelf is not
    // asked again.
    store.iconShelf.clear();
    store.undo();

    expect(store.doc.icons).toEqual({ [ICON_KEY]: PLC });
    expect(iconOfNode(sample.alpha.id)).toBe(ICON_KEY);

    store.undo();
    expect('icons' in store.doc).toBe(false);
    store.redo();
    expect(store.doc.icons).toEqual({ [ICON_KEY]: PLC });
  });

  test('deleting the last node that uses an icon removes the icon', () => {
    store.shelveIcons({ [ICON_KEY]: PLC });
    use(sample.alpha.id, ICON_KEY);

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

  // The shelf never puts anything into a document that the server would
  // refuse the document for.
  test('an icon the shelf does not have, or has wrong, is left out, and said', () => {
    use(sample.alpha.id, ICON_KEY);

    expect('icon' in findNode(store.doc, sample.alpha.id).device).toBe(false);
    expect('icons' in store.doc).toBe(false);
    expect(announced).toEqual([
      'Changed the custom icon',
      '1 custom icon was left out: a diagram holds at most 32.',
    ]);

    const other = iconOf(1);

    store.shelveIcons({ [ICON_KEY]: other.entry });
    use(sample.alpha.id, ICON_KEY);

    expect('icons' in store.doc).toBe(false);
    expect(errorsOf(store.doc)).toEqual([]);
  });

  test('a diagram takes 32 icons; one more is left out and announced', () => {
    const icons = Array.from({ length: MAX_DOCUMENT_ICONS + 2 }, (_, index) =>
      iconOf(index),
    );

    store.shelveIcons(new Map(icons.map((icon) => [icon.id, icon.entry])));

    let doc = store.doc;
    const ids = [];

    icons.forEach((icon, index) => {
      const added = store.addNode({
        kind: 'device',
        hostname: `plc-${index}`,
        look: { icon: icon.id },
      });

      ids.push(added.id);
      doc = store.doc;
    });

    expect(Object.keys(doc.icons)).toHaveLength(MAX_DOCUMENT_ICONS);
    expect(
      ids.map((id) => findNode(doc, id).device.icon).filter(Boolean),
    ).toHaveLength(MAX_DOCUMENT_ICONS);
    expect(errorsOf(doc)).toEqual([]);
    expect(announced.slice(-4)).toEqual([
      'Added device',
      '1 custom icon was left out: a diagram holds at most 32.',
      'Added device',
      '1 custom icon was left out: a diagram holds at most 32.',
    ]);
  });

  test('a commit that is refused changes nothing', () => {
    store.shelveIcons({ [ICON_KEY]: PLC });
    store.readOnly = true;

    expect(use(sample.alpha.id, ICON_KEY)).toBeNull();
    expect('icons' in store.doc).toBe(false);
  });

  test('a copy carries its icons into another diagram', () => {
    store.shelveIcons({ [ICON_KEY]: PLC });
    use(sample.alpha.id, ICON_KEY);
    store.select({ nodes: [sample.alpha.id], edges: [] });
    store.copy();

    expect(store.clipboard.icons).toEqual({ [ICON_KEY]: PLC });

    // Another diagram, in the same session: the clipboard stays.
    store.iconShelf.clear();
    store.setDocument(sampleDocument().doc);
    expect('icons' in store.doc).toBe(false);

    const [pasted] = store.paste();

    expect(iconOfNode(pasted)).toBe(ICON_KEY);
    expect(store.doc.icons).toEqual({ [ICON_KEY]: PLC });
    expect(errorsOf(store.doc)).toEqual([]);
    // The paste made the icon known, for an edit that follows.
    expect(store.iconShelf.get(ICON_KEY)).toEqual(PLC);
  });

  test('a paste into a full diagram leaves its icon out, and says so', () => {
    const icons = Array.from({ length: MAX_DOCUMENT_ICONS }, (_, index) =>
      iconOf(index + 100),
    );

    store.shelveIcons({ [ICON_KEY]: PLC });
    use(sample.alpha.id, ICON_KEY);
    store.select({ nodes: [sample.alpha.id], edges: [] });
    store.copy();
    use(sample.alpha.id, '', 'Removed the custom icon');

    store.shelveIcons(new Map(icons.map((icon) => [icon.id, icon.entry])));
    icons.forEach((icon, index) => {
      store.addNode({
        kind: 'device',
        hostname: `plc-${index}`,
        look: { icon: icon.id },
      });
    });
    announced.length = 0;

    const [pasted] = store.paste();

    expect('icon' in findNode(store.doc, pasted).device).toBe(false);
    expect(Object.keys(store.doc.icons)).toHaveLength(MAX_DOCUMENT_ICONS);
    expect(announced).toEqual([
      'Pasted 1 node',
      '1 custom icon was left out: a diagram holds at most 32.',
    ]);
  });

  test('the shelf takes icons by id, a map or an object, and keeps the last put there', () => {
    store.shelveIcons(null);
    store.shelveIcons({
      [ICON_KEY]: { name: '', data: ICON_DATA, width: 1, owner: 'bob' },
      server: PLC,
      'sha256:short': PLC,
    });
    store.shelveIcons(new Map([[iconOf(1).id, { name: 'one' }]]));

    // Only an icon id is a key, and only a name and data are kept.
    expect([...store.iconShelf]).toEqual([[ICON_KEY, { data: ICON_DATA }]]);

    const many = Array.from({ length: 130 }, (_, index) => iconOf(index));

    store.shelveIcons(new Map(many.map((icon) => [icon.id, icon.entry])));

    expect(store.iconShelf.size).toBe(128);
    expect(store.iconShelf.has(ICON_KEY)).toBe(false);
    expect(store.iconShelf.has(many[0].id)).toBe(false);
    expect(store.iconShelf.has(many[2].id)).toBe(true);
    expect(store.iconShelf.has(many.at(-1).id)).toBe(true);

    // One put there again is among the last.
    store.shelveIcons({ [many[2].id]: many[2].entry, [ICON_KEY]: PLC });
    expect([...store.iconShelf.keys()].slice(-2)).toEqual([
      many[2].id,
      ICON_KEY,
    ]);
    expect(store.iconShelf.size).toBe(128);
  });

  // The library is the user's: nothing of it is kept for the next one.
  test('the shelf and the clipboard go when the session ends', () => {
    store.shelveIcons({ [ICON_KEY]: PLC });
    use(sample.alpha.id, ICON_KEY);
    store.select({ nodes: [sample.alpha.id], edges: [] });
    store.copy();

    store.endSession();

    expect(store.iconShelf.size).toBe(0);
    expect(store.clipboard).toBeNull();
    expect('icons' in store.doc).toBe(false);
  });
});
