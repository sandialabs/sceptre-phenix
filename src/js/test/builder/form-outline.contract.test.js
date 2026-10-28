// The Inspector form outline (formOutline.js), the collapsed state of each
// Inspector variant (inspector/collapse.js), and capitalize (text.js).

import { describe, expect, test } from 'vitest';
import fc from 'fast-check';
import { effectScope, ref } from 'vue';

import {
  resetCollapsed,
  useCollapseState,
} from '@/components/builder/inspector/collapse.js';
import {
  activeEntry,
  afterInsert,
  afterMove,
  afterRemove,
  entryTitle,
  hasErrorUnder,
  itemSummary,
  outlineTree,
  pathContains,
  specFieldPath,
} from '@/builder/formOutline.js';
import { capitalize } from '@/builder/text.js';

describe('list entry names', () => {
  test('the first field of the summary keys that is set names an entry', () => {
    expect(itemSummary({ image: 'ubuntu.qc2', cache_mode: 'none' })).toBe(
      'ubuntu.qc2',
    );
    expect(itemSummary({ name: '  ', image: 'win.qc2' })).toBe('win.qc2');
    expect(itemSummary({ cache_mode: 'none' })).toBe('');
    expect(itemSummary('exec df -h')).toBe('exec df -h');
    expect(itemSummary(0)).toBe('0');
    expect(itemSummary(undefined)).toBe('');
    expect(itemSummary(null)).toBe('');
  });

  test('long text is cut to 40 characters with an ellipsis', () => {
    const summary = itemSummary({ description: 'x'.repeat(60) });

    expect(summary).toHaveLength(40);
    expect(summary.endsWith('…')).toBe(true);
  });

  test('an entry header is its name and its summary', () => {
    expect(entryTitle('Interface 1', { name: 'eth0' })).toBe(
      'Interface 1: eth0',
    );
    expect(entryTitle('Drive 2', {})).toBe('Drive 2');
    expect(entryTitle('miniccc command 1', 'exec ls')).toBe(
      'miniccc command 1: exec ls',
    );
  });
});

describe('names that start a sentence', () => {
  test('take a capital, except tools written in lowercase', () => {
    expect(capitalize('drive 1')).toBe('Drive 1');
    expect(capitalize('firewall ruleset 2')).toBe('Firewall ruleset 2');
    expect(capitalize('miniccc command 1')).toBe('miniccc command 1');
    expect(capitalize('minimega')).toBe('minimega');
    expect(capitalize('minicccs')).toBe('Minicccs');
    expect(capitalize('')).toBe('');
  });
});

describe('a field’s path in the spec', () => {
  test('is its data path without spec, with indexes between dots', () => {
    expect(specFieldPath('spec.general.hostname')).toBe('general.hostname');
    expect(specFieldPath('spec.network.interfaces.0.address')).toBe(
      'network.interfaces.0.address',
    );
    expect(specFieldPath('spec.commands.2')).toBe('commands.2');
  });

  test('fields that are not part of the spec have none', () => {
    expect(specFieldPath('hostname')).toBe('');
    expect(specFieldPath('fillColor')).toBe('');
    expect(specFieldPath('spec')).toBe('');
    expect(specFieldPath('specs.x')).toBe('');
    expect(specFieldPath(undefined)).toBe('');
  });
});

describe('errors in a section', () => {
  test('a section holds itself and the fields under it', () => {
    expect(pathContains('spec.network', 'spec.network')).toBe(true);
    expect(pathContains('spec.network', 'spec.network.interfaces.0.mac')).toBe(
      true,
    );
    expect(pathContains('spec.network', 'spec.networks')).toBe(false);
    expect(
      pathContains('spec.network.interfaces.1', 'spec.network.interfaces.10'),
    ).toBe(false);
  });

  test('an error under any of the paths counts', () => {
    const errors = ['spec.network.interfaces.1.mac'];

    expect(hasErrorUnder('spec.network', errors)).toBe(true);
    expect(hasErrorUnder('spec.network.interfaces.0', errors)).toBe(false);
    expect(hasErrorUnder(['spec.commands', 'spec.network'], errors)).toBe(true);
    expect(hasErrorUnder('spec.network', [])).toBe(false);
    expect(hasErrorUnder('spec.network', undefined)).toBe(false);
  });
});

describe('collapsed list entries follow their entries', () => {
  const list = 'spec.network.interfaces';
  const collapsed = [
    'spec.general',
    `${list}.0`,
    `${list}.2`,
    `${list}.2.ospf`,
    'spec.network.interfacesX.1',
  ];

  test('an entry added moves the ones after it down', () => {
    expect([...afterInsert(collapsed, list, 1)]).toEqual([
      'spec.general',
      `${list}.0`,
      `${list}.3`,
      `${list}.3.ospf`,
      'spec.network.interfacesX.1',
    ]);
  });

  test('an entry removed takes its state, and the ones after it move up', () => {
    expect([...afterRemove(collapsed, list, 0)]).toEqual([
      'spec.general',
      `${list}.1`,
      `${list}.1.ospf`,
      'spec.network.interfacesX.1',
    ]);
    expect([...afterRemove(collapsed, list, 2)]).toEqual([
      'spec.general',
      `${list}.0`,
      'spec.network.interfacesX.1',
    ]);
  });

  test('two entries that change places keep their own state', () => {
    expect([...afterMove(collapsed, list, 2, 1)]).toEqual([
      'spec.general',
      `${list}.0`,
      `${list}.1`,
      `${list}.1.ospf`,
      'spec.network.interfacesX.1',
    ]);
    expect([...afterMove(collapsed, list, 0, 1)]).toContain(`${list}.1`);
    expect([...afterMove(collapsed, list, 0, 1)]).not.toContain(`${list}.0`);
  });
});

describe('the table of contents', () => {
  const entries = [
    { key: '#checks', depth: 0 },
    { key: 'spec.general', depth: 0 },
    { key: 'spec.general.hostname', depth: 1 },
    { key: 'spec.network', depth: 0 },
    { key: 'spec.network.interfaces', depth: 1 },
    { key: 'spec.network.interfaces.0', depth: 2 },
    { key: 'spec.network.interfaces.0.ospf', depth: 3 },
    { key: 'spec.network.interfaces.1', depth: 2 },
    { key: 'spec.network.routes', depth: 1 },
    { key: '#position', depth: 0 },
  ];
  const keys = (nodes) =>
    nodes.map((node) =>
      node.children.length ? [node.key, keys(node.children)] : node.key,
    );

  test('nests each entry under the one before it a level up', () => {
    expect(keys(outlineTree(entries))).toEqual([
      '#checks',
      ['spec.general', ['spec.general.hostname']],
      [
        'spec.network',
        [
          [
            'spec.network.interfaces',
            ['spec.network.interfaces.0', 'spec.network.interfaces.1'],
          ],
          'spec.network.routes',
        ],
      ],
      '#position',
    ]);
  });

  test('leaves out entries deeper than the levels it shows', () => {
    expect(keys(outlineTree(entries, 0))).toEqual([
      '#checks',
      'spec.general',
      'spec.network',
      '#position',
    ]);
  });

  test('marks the innermost entry in view', () => {
    const order = entries.map((entry) => entry.key);

    expect(
      activeEntry(
        order,
        new Set([
          'spec.network',
          'spec.network.interfaces',
          'spec.network.interfaces.1',
        ]),
      ),
    ).toBe('spec.network.interfaces.1');
    expect(activeEntry(order, new Set())).toBe('');
  });
});

describe('the collapsed state of an Inspector', () => {
  function state(variant, errors = []) {
    const scope = effectScope();
    const made = scope.run(() => useCollapseState(variant, ref(errors)));

    return { ...made, stop: () => scope.stop() };
  }

  test('is kept for each variant, and Reset view expands every one', () => {
    const editor = state('test-editor');
    const canvas = state('test-canvas');

    editor.setCollapsed('spec.general', true);
    expect(editor.isCollapsed('spec.general')).toBe(true);
    expect(canvas.isCollapsed('spec.general')).toBe(false);
    // The next node opened shows it the same.
    expect(state('test-editor').isCollapsed('spec.general')).toBe(true);

    resetCollapsed();
    expect(editor.isCollapsed('spec.general')).toBe(false);
    editor.stop();
    canvas.stop();
  });

  test('going to a field expands what holds it', () => {
    const editor = state('test-reveal');
    const list = 'spec.network.interfaces';

    editor.collapseAll(['spec.network', list, `${list}.0`, 'spec.general']);
    expect(editor.reveal(`${list}.0.mac`)).toBe(true);
    expect(editor.isCollapsed('spec.network')).toBe(false);
    expect(editor.isCollapsed(list)).toBe(false);
    expect(editor.isCollapsed(`${list}.0`)).toBe(false);
    expect(editor.isCollapsed('spec.general')).toBe(true);
    // Nothing more to expand.
    expect(editor.reveal(`${list}.0.mac`)).toBe(false);
    expect(editor.reveal(undefined)).toBe(false);

    editor.expandAll();
    expect(editor.isCollapsed('spec.general')).toBe(false);
    editor.stop();
  });

  test('list entries keep their state as they move', () => {
    const editor = state('test-moves');
    const list = 'spec.hardware.drives';

    editor.setCollapsed(`${list}.1`, true);
    editor.moved(list, 1, 0);
    expect(editor.isCollapsed(`${list}.0`)).toBe(true);
    editor.inserted(list, 0);
    expect(editor.isCollapsed(`${list}.1`)).toBe(true);
    editor.removed(list, 1);
    expect(editor.isCollapsed(`${list}.1`)).toBe(false);
    expect(editor.isCollapsed(`${list}.0`)).toBe(false);
    editor.stop();
  });

  test('says whether a field with an error is in a section', () => {
    const editor = state('test-errors', ['spec.hardware.drives.0.image']);

    expect(editor.hasErrors('spec.hardware')).toBe(true);
    expect(editor.hasErrors('spec.hardware.drives.1')).toBe(false);
    editor.stop();
  });
});

describe('collapsed paths through list edits, for any paths', () => {
  const LIST = 'spec.a';
  // Paths in the list, in another list, in one whose name starts the same,
  // and under a key that is no index.
  const path = fc
    .tuple(
      fc.constantFrom(LIST, 'spec.b', 'spec.ab'),
      fc.oneof(fc.nat(6), fc.constant('x')),
      fc.constantFrom('', 'x', 'x.y'),
    )
    .map(([list, index, rest]) =>
      [list, index, rest].filter((part) => part !== '').join('.'),
    );
  const paths = fc.array(path, { maxLength: 8 });
  const sorted = (set) => [...set].sort();

  test('removing an entry just added leaves the paths as they were', () => {
    fc.assert(
      fc.property(paths, fc.nat(7), (collapsed, index) => {
        const inserted = afterInsert(collapsed, LIST, index);

        expect(sorted(afterRemove(inserted, LIST, index))).toEqual(
          sorted(new Set(collapsed)),
        );
      }),
      { numRuns: 300 },
    );
  });

  test('moving two entries twice leaves the paths as they were', () => {
    fc.assert(
      fc.property(paths, fc.nat(7), fc.nat(7), (collapsed, from, to) => {
        const moved = afterMove(collapsed, LIST, from, to);

        expect(sorted(afterMove(moved, LIST, from, to))).toEqual(
          sorted(new Set(collapsed)),
        );
      }),
      { numRuns: 300 },
    );
  });

  test('removing an entry drops its paths and only them', () => {
    fc.assert(
      fc.property(paths, fc.nat(7), (collapsed, index) => {
        const before = new Set(collapsed);
        const own = `${LIST}.${index}`;
        const gone = [...before].filter(
          (entry) => entry === own || entry.startsWith(`${own}.`),
        );
        const after = afterRemove(collapsed, LIST, index);

        expect(after.size).toBe(before.size - gone.length);
        for (const entry of before) {
          if (!gone.includes(entry) && !entry.startsWith(`${LIST}.`)) {
            expect(after.has(entry)).toBe(true);
          }
        }
      }),
      { numRuns: 300 },
    );
  });
});
