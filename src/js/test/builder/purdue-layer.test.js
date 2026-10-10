import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

import { copySelection, pasteClipboard } from '@/builder/clipboard.js';
import {
  addNode,
  findNode,
  PURDUE_LEVEL_TITLES,
  PURDUE_LEVELS,
  purdueLevel,
  setPurdueLevel,
  updateNode,
} from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';
import {
  nodeOptionsFromTemplate,
  templateFromNode,
} from '@/builder/templates.js';
import { validateDocument } from '@/builder/validate.js';

import { sampleDocument } from './fixtures.js';

// The Purdue layer of a device or a switch: one of the levels of the
// Purdue model, kept in the diagram only.
describe('the Purdue layer of a node', () => {
  test('lists the levels from the top to the bottom, each named', () => {
    expect(PURDUE_LEVELS).toEqual(['5', '4', '3.5', '3', '2', '1', '0']);
    expect(PURDUE_LEVELS.map((level) => PURDUE_LEVEL_TITLES[level])).toEqual([
      'Level 5: Enterprise network',
      'Level 4: Site business planning and logistics',
      'Level 3.5: Industrial DMZ',
      'Level 3: Site operations',
      'Level 2: Area supervisory control',
      'Level 1: Basic control',
      'Level 0: Physical process',
    ]);
  });

  test('is set on a device and a switch, and removed again', () => {
    const { doc, alpha, sw } = sampleDocument();
    const set = setPurdueLevel(
      setPurdueLevel(doc, alpha.id, '1'),
      sw.id,
      '3.5',
    );

    expect(findNode(set, alpha.id).device.purdueLevel).toBe('1');
    expect(findNode(set, sw.id).switch.purdueLevel).toBe('3.5');
    expect(purdueLevel(findNode(set, alpha.id))).toBe('1');
    expect(
      validateDocument(set).filter((issue) => issue.level === 'error'),
    ).toEqual([]);

    // None leaves the document as it was, byte for byte.
    const cleared = setPurdueLevel(
      setPurdueLevel(set, alpha.id, ''),
      sw.id,
      '',
    );

    expect(JSON.stringify(cleared)).toBe(JSON.stringify(doc));
  });

  test('changes nothing for another kind, an unknown level or the same level', () => {
    const { doc, alpha } = sampleDocument();
    const note = addNode(doc, { kind: 'note', text: 'Read me' });
    const set = setPurdueLevel(doc, alpha.id, '2');

    expect(setPurdueLevel(note.doc, note.node.id, '2')).toBe(note.doc);
    expect(setPurdueLevel(doc, alpha.id, '6')).toBe(doc);
    expect(setPurdueLevel(doc, alpha.id, 'Level 2')).toBe(doc);
    expect(setPurdueLevel(set, alpha.id, '2')).toBe(set);
    expect(setPurdueLevel(doc, 'nonesuch', '2')).toBe(doc);
    expect(purdueLevel(note.node)).toBe('');
  });

  test('changes nothing for a device included from another topology', () => {
    const { doc, alpha } = sampleDocument();
    const included = {
      ...doc,
      nodes: doc.nodes.map((node) =>
        node.id === alpha.id
          ? { ...node, device: { ...node.device, includedFrom: 'plant' } }
          : node,
      ),
    };

    expect(setPurdueLevel(included, alpha.id, '1')).toBe(included);
  });

  test('stays when the Inspector applies other fields, and an empty one goes', () => {
    const { doc, alpha, sw } = sampleDocument();
    const set = setPurdueLevel(setPurdueLevel(doc, alpha.id, '0'), sw.id, '2');
    const applied = updateNode(
      updateNode(set, alpha.id, { device: { fillColor: '#eef4fb' } }),
      sw.id,
      { switch: { outlineColor: '#1f7a5a' } },
    );

    expect(findNode(applied, alpha.id).device.purdueLevel).toBe('0');
    expect(findNode(applied, sw.id).switch.purdueLevel).toBe('2');

    const emptied = updateNode(
      updateNode(applied, alpha.id, { device: { purdueLevel: '' } }),
      sw.id,
      { switch: { purdueLevel: '' } },
    );

    expect('purdueLevel' in findNode(emptied, alpha.id).device).toBe(false);
    expect('purdueLevel' in findNode(emptied, sw.id).switch).toBe(false);
  });

  test('a new device or switch takes one, and an unknown one is none', () => {
    const { doc } = sampleDocument();
    const device = addNode(doc, { kind: 'device', purdueLevel: '1' });
    const hub = addNode(device.doc, { kind: 'switch', purdueLevel: '3' });
    const other = addNode(hub.doc, { kind: 'device', purdueLevel: '9' });
    const otherHub = addNode(other.doc, { kind: 'switch', purdueLevel: '9' });

    expect(device.node.device.purdueLevel).toBe('1');
    expect(hub.node.switch.purdueLevel).toBe('3');
    expect('purdueLevel' in other.node.device).toBe(false);
    expect('purdueLevel' in otherHub.node.switch).toBe(false);
    expect(
      validateDocument(otherHub.doc).map((issue) => issue.code),
    ).not.toContain('switch.purdue-level.unknown');
  });

  test('a copy keeps it', () => {
    const { doc, alpha, sw } = sampleDocument();
    const set = setPurdueLevel(setPurdueLevel(doc, alpha.id, '1'), sw.id, '2');
    const pasted = pasteClipboard(
      set,
      copySelection(set, { nodes: [alpha.id, sw.id] }),
    );
    const copies = pasted.nodeIds.map((id) => findNode(pasted.doc, id));

    expect(copies.map(purdueLevel).sort()).toEqual(['1', '2']);
  });

  test('a template keeps it, and a device made from the template takes it', () => {
    const { doc, alpha } = sampleDocument();
    const set = setPurdueLevel(doc, alpha.id, '1');
    const template = templateFromNode(findNode(set, alpha.id), {
      name: 'PLC',
    });

    expect(template.device.purdueLevel).toBe('1');

    const made = addNode(doc, nodeOptionsFromTemplate(template, doc));

    expect(made.node.device.purdueLevel).toBe('1');
  });

  test('an unknown one is refused, as the server refuses it', () => {
    const { doc, alpha, sw } = sampleDocument();
    const wrong = {
      ...doc,
      nodes: doc.nodes.map((node) => {
        if (node.id === alpha.id) {
          return { ...node, device: { ...node.device, purdueLevel: '6' } };
        }

        return node.id === sw.id
          ? { ...node, switch: { ...node.switch, purdueLevel: 'DMZ' } }
          : node;
      }),
    };
    const codes = validateDocument(wrong).map((issue) => issue.code);

    expect(codes).toEqual(
      expect.arrayContaining([
        'device.purdue-level.unknown',
        'switch.purdue-level.unknown',
      ]),
    );
  });
});

describe('setting the Purdue layer in the store', () => {
  let store;
  let sample;

  beforeEach(() => {
    setActivePinia(createPinia());
    store = useBuilderStore();
    sample = sampleDocument();
    store.setDocument(sample.doc);
  });

  test('is one undo step, announced', () => {
    const before = store.doc;

    expect(store.setPurdueLevel(sample.alpha.id, '3.5')).not.toBeNull();
    expect(purdueLevel(findNode(store.doc, sample.alpha.id))).toBe('3.5');
    expect(store.announcement).toContain(
      'Changed the Purdue layer of Device alpha to Level 3.5: Industrial DMZ',
    );

    // The same level again is no step.
    expect(store.setPurdueLevel(sample.alpha.id, '3.5')).toBeNull();

    expect(store.setPurdueLevel(sample.sw.id, '2')).not.toBeNull();
    expect(store.setPurdueLevel(sample.sw.id, '')).not.toBeNull();
    expect(store.announcement).toContain('Removed the Purdue layer of Switch');

    store.undo();
    store.undo();
    store.undo();
    expect(store.doc).toEqual(before);

    store.redo();
    expect(purdueLevel(findNode(store.doc, sample.alpha.id))).toBe('3.5');
  });

  test('a read-only draft keeps the level it has', () => {
    store.readOnly = true;

    expect(store.setPurdueLevel(sample.alpha.id, '1')).toBeNull();
    expect(purdueLevel(findNode(store.doc, sample.alpha.id))).toBe('');
  });
});
