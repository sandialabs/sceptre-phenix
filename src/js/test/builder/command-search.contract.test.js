import { describe, expect, test, vi } from 'vitest';

import {
  CHOICE_LIMIT,
  NODE_SEARCH,
  TEMPLATE_SEARCH,
  createChoiceCache,
  detailParts,
  paletteResults,
  parseQuery,
  selectionSummary,
} from '@/builder/commandSearch.js';
import {
  NODE_FIELD_RANKS,
  READ_ONLY,
  VIEW_API,
  createCommandContext,
  getCommand,
  nodeChoices,
  runCommand,
} from '@/builder/commands.js';

import { templateFields } from '@/builder/librarySearch.js';
import { setLayers } from '@/builder/model.js';

import { libraryOf, sampleDocument, withTemplates } from './fixtures.js';

function context({ store = {}, view = {} } = {}) {
  const { doc } = sampleDocument();
  const fullView = {
    editing: true,
    showMinimap: true,
    canZoomIn: true,
    canZoomOut: true,
    icons: [],
    ...view,
  };

  for (const name of VIEW_API) {
    fullView[name] ??= vi.fn();
  }

  return createCommandContext({
    store: withTemplates({
      doc,
      readOnly: false,
      selection: { nodes: [], edges: [] },
      clipboard: null,
      canUndo: false,
      canRedo: false,
      canRestoreLayout: false,
      theme: 'system',
      history: { undoLabel: () => 'Added device' },
      drafts: { all: [] },
      documents: [],
      ...store,
    }),
    view: fullView,
  });
}

// The interfaces of alpha in sampleDocument, given addresses.
function addressed() {
  const sample = sampleDocument();
  const alpha = sample.doc.nodes.find((node) => node.id === sample.alpha.id);

  alpha.label = 'Web front end';
  alpha.device.spec.network.interfaces = [
    {
      name: 'eth0',
      vlan: 'EXP',
      address: '10.1.2.3',
      mac: '00:16:3E:0A:01:05',
    },
  ];

  return sample;
}

const titles = (group) => group.items.map((item) => item.title);
const labels = (results) => results.groups.map((group) => group.label);

test('parseQuery splits off @, #, t:, c:, i: and ?, and ignores >', () => {
  expect(parseQuery('@plc')).toEqual({ prefix: '@', text: 'plc' });
  expect(parseQuery('#101')).toEqual({ prefix: '#', text: '101' });
  expect(parseQuery('t:pump')).toEqual({ prefix: 't:', text: 'pump' });
  expect(parseQuery('C:pumps')).toEqual({ prefix: 'c:', text: 'pumps' });
  expect(parseQuery('i:')).toEqual({ prefix: 'i:', text: '' });
  expect(parseQuery('tc:')).toEqual({ prefix: '', text: 'tc:' });
  expect(parseQuery('?')).toEqual({ prefix: '?', text: '' });
  expect(parseQuery('>group')).toEqual({ prefix: '', text: 'group' });
  expect(parseQuery('group')).toEqual({ prefix: '', text: 'group' });
});

describe('with nothing typed', () => {
  test('the selection’s commands, then recent ones, then what can run', () => {
    const { doc, alpha } = sampleDocument();
    const ctx = context({
      store: { doc, selection: { nodes: [alpha.id], edges: [] } },
    });
    const results = paletteResults(ctx, {
      recent: [
        { id: 'add.device', choices: ['builtin:router'] },
        // Gone from the diagram, so gone from the list.
        { id: 'structure.connect', choices: ['no-such-node', 'x'] },
        { id: 'structure.layout', choices: [] },
      ],
    });
    const [selected, recent, ...rest] = results.groups;

    expect(selected.label).toBe('Selected: alpha');
    expect(titles(selected)).toEqual([
      'Rename alpha',
      'Connect alpha to a switch',
      'Duplicate',
      'Copy',
      'Group selection',
      'Delete selection',
    ]);
    // Connect starts at its second step, alpha chosen.
    expect(selected.items[1].picked.map((choice) => choice.value)).toEqual([
      alpha.id,
    ]);
    expect(titles(recent)).toEqual(['Add device › Router', 'Auto layout']);
    // Commands that cannot run are left out, and none is listed twice.
    const listed = rest.flatMap(titles);
    expect(listed).not.toContain('Undo');
    expect(listed).not.toContain('Ungroup');
    expect(listed).not.toContain('Duplicate');
    expect(listed).toContain('Connect device to a switch');
    expect(results.total).toBe(
      results.groups.reduce((sum, group) => sum + group.items.length, 0),
    );
  });

  test('on the drafts landing, the landing’s commands, drafts first', () => {
    const results = paletteResults(context({ view: { editing: false } }));
    const all = results.groups.flatMap(titles);

    expect(labels(results)[0]).toBe('Drafts');
    expect(all).toEqual(
      expect.arrayContaining([
        'Blank diagram',
        'Import…',
        'Upload…',
        'Builder help',
        'Show Drafts',
      ]),
    );
    expect(all).not.toContain('Go to node');
    expect(all).not.toContain('Undo');
    // Upload is one of the ways to start, listed with them.
    expect(titles(results.groups[0])).toContain('Upload…');
    expect(labels(results)).not.toContain('Draft');
  });
});

describe('a command search', () => {
  test('lists every match, grouped by best match, with reasons', () => {
    const results = paletteResults(context(), { query: 'group' });
    const [structure, add] = results.groups;

    expect(labels(results).slice(0, 2)).toEqual(['Structure', 'Add']);
    expect(titles(structure)).toEqual([
      'Group selection',
      'Auto-group by network',
      'Auto-group by name',
      'Auto-group by name pattern…',
      'Move to a group…',
      'Ungroup',
    ]);
    expect(structure.items[0].ranges).toEqual([[0, 5]]);
    expect(structure.items[0].disabled).toBe(
      'Select at least one node to group.',
    );
    // Move to a group opens its dialog with nothing selected.
    expect(structure.items[4].disabled).toBe('');
    expect(structure.items[5].disabled).toBe('Select a group first.');
    expect(titles(add)).toEqual(['Add group']);
  });

  test('matches letters in order, and keywords without marking them', () => {
    const [first] = paletteResults(context(), { query: 'grp' }).groups;
    expect(titles(first)).toEqual([
      'Group selection',
      'Ungroup',
      'Auto-group by network',
      'Auto-group by name',
      'Auto-group by name pattern…',
      'Move to a group…',
    ]);

    // The pattern rule is found by what a pattern is, too.
    for (const query of ['regex', 'regexp', 'regular expression']) {
      expect(
        titles(paletteResults(context(), { query }).groups[0]),
        query,
      ).toEqual(['Auto-group by name pattern…']);
    }

    const [layout] = paletteResults(context(), { query: 'arrange' }).groups;
    expect(layout.items[0].title).toBe('Auto layout');
    expect(layout.items[0].ranges).toEqual([]);
  });

  // A command's icon says what it does where its group's icon would not.
  test('Rename, Open Node Editor, Settings and Edit layers have their own icons', () => {
    const iconOf = (query, title) =>
      paletteResults(context(), { query })
        .groups.flatMap((group) => group.items)
        .find((item) => item.title === title)?.icon;

    expect(iconOf('rename', 'Rename')).toBe('pencil');
    expect(iconOf('node editor', 'Open Node Editor')).toBe('pencil');
    expect(iconOf('settings', 'Settings…')).toBe('settings');
    expect(iconOf('edit layers', 'Edit layers…')).toBe('layer-group');
  });

  // Each download format is a command, and all of them answer to the word
  // Download had before.
  test('finds the download commands by "export"', () => {
    const exported = paletteResults(context(), { query: 'export' });

    expect(labels(exported)).toEqual(['Draft']);
    expect(titles(exported.groups[0])).toEqual([
      'Download…',
      'Download Builder JSON',
      'Download Builder YAML',
      'Download Topology YAML',
      'Download PNG',
      'Download SVG',
      'Download draw.io',
      'Download Gephi (GEXF)',
    ]);
    expect(exported.total).toBe(8);
    // The alias is not in the title, so nothing is marked.
    expect(exported.groups[0].items.every((item) => !item.ranges.length)).toBe(
      true,
    );
    expect(exported.groups[0].items.map((item) => item.icon)).toEqual([
      'download',
      'download',
      'download',
      'download',
      'image',
      'image',
      'download',
      'download',
    ]);
    expect(exported.groups[0].items[6].detail).toBe(
      'The diagram as a drawing to edit in draw.io',
    );
    expect(exported.groups[0].items[7].detail).toBe(
      'The network as a graph to analyze in Gephi',
    );

    // A format narrows it: the format's command first, Download… last.
    for (const [query, first] of [
      ['export gexf', 'Download Gephi (GEXF)'],
      ['export png', 'Download PNG'],
      ['gexf', 'Download Gephi (GEXF)'],
      ['gephi', 'Download Gephi (GEXF)'],
      ['drawio', 'Download draw.io'],
      ['export draw.io', 'Download draw.io'],
      ['diagrams.net', 'Download draw.io'],
      ['download topology', 'Download Topology YAML'],
    ]) {
      const [draft] = paletteResults(context(), { query }).groups;

      expect(titles(draft)[0], query).toBe(first);
    }
    expect(
      titles(paletteResults(context(), { query: 'export gexf' }).groups[0]),
    ).toEqual(['Download Gephi (GEXF)', 'Download…']);
    // Words of the title, the alias and the keywords together.
    expect(
      titles(paletteResults(context(), { query: 'save png' }).groups[0]),
    ).toEqual(['Download…', 'Download PNG']);
    expect(
      titles(paletteResults(context(), { query: 'export image' }).groups[0]),
    ).toEqual(['Download…', 'Download PNG', 'Download SVG']);

    // The drafts page has no draft to download.
    const landing = context({ view: { editing: false } });

    for (const query of ['export']) {
      expect(
        paletteResults(landing, { query }).groups.flatMap((group) =>
          group.items.filter((item) => item.kind === 'command'),
        ),
        query,
      ).toEqual([]);
    }
  });

  // Open experiment and Combine included nodes are offered only by a draft
  // with an experiment or included nodes, and only their own words find them.
  test('a draft with an experiment and included nodes lists as many downloads and group commands', () => {
    const full = context({
      store: {
        experimentName: 'exp-1',
        summary: { included: 2 },
      },
    });
    const found = (query) =>
      paletteResults(full, { query }).groups.flatMap((group) =>
        group.items
          .filter((item) => item.kind === 'command')
          .map((item) => item.title),
      );

    expect(found('export')).toHaveLength(8);
    expect(paletteResults(full, { query: 'export' }).total).toBe(8);
    expect(found('download')).toHaveLength(8);
    // Move to a group… is one of the 7 group commands. "Go to the Drafts
    // page" also holds g, r and p in order, so it comes last.
    expect(found('grp')).toHaveLength(8);
    expect(found('grp').slice(0, 7)).toContain('Move to a group…');
    expect(found('grp').at(-1)).toBe('Go to the Drafts page');

    // "exp" starts Open experiment, and the alias of each download.
    expect(found('exp').slice(0, 2)).toEqual(['Open experiment', 'Download…']);
    expect(found('experiment')).toEqual(['Open experiment', 'Publish…']);
    expect(found('combine')).toEqual([
      'Combine included nodes into a new draft',
    ]);
    expect(found('unlock')).toEqual([
      'Combine included nodes into a new draft',
    ]);

    // Without either, neither is listed.
    const plain = paletteResults(context(), { query: 'exp' });

    expect(plain.groups.flatMap(titles)).not.toContain('Open experiment');
    expect(paletteResults(context(), { query: 'combine' }).total).toBe(0);
  });

  // Upload converts a legacy Builder diagram, and Import a stored topology
  // that has one.
  test('finds the conversion of a legacy Builder diagram by "legacy"', () => {
    const [draft] = paletteResults(context(), { query: 'legacy' }).groups;

    expect(titles(draft)).toEqual(['Upload…']);
    expect(draft.items[0].command.id).toBe('draft.upload');
    expect(draft.items[0].ranges).toEqual([]);

    const landing = context({ view: { editing: false } });
    const [drafts] = paletteResults(landing, { query: 'legacy' }).groups;

    expect(drafts.items.map((item) => item.command.id)).toEqual([
      'drafts.import',
      'drafts.upload',
    ]);
  });

  test('finds Disconnect by the words for removing a connection', () => {
    for (const query of ['delete connection', 'remove connection', 'unlink']) {
      const [first] = paletteResults(context(), { query }).groups;

      expect(first.items[0].command.id, query).toBe('structure.disconnect');
    }
  });

  test('ties go to recent use, then to the registry order', () => {
    const plain = paletteResults(context(), { query: 'add' });
    expect(titles(plain.groups[0])[0]).toBe('Add device');

    const recent = paletteResults(context(), {
      query: 'add',
      recent: [{ id: 'add.note', choices: [] }],
    });
    expect(titles(recent.groups[0])[0]).toBe('Add note');
  });

  test('of matches alike but for where their words are, one that can run comes first', () => {
    const ctx = (canRestoreLayout) =>
      context({ store: { canRestoreLayout }, view: { fitRestores: true } });
    const first = (results) => results.groups[0].items[0];

    // Only the view can be restored: Enter runs it, not the layout's reason.
    const view = first(paletteResults(ctx(false), { query: 'restore prev' }));
    expect(view.title).toBe('Restore previous view');
    expect(view.disabled).toBe('');

    // Both can: the registry's order again.
    const layout = first(paletteResults(ctx(true), { query: 'restore prev' }));
    expect(layout.title).toBe('Restore previous layout');

    // "draft" is further into Show Drafts than into Open draft, which has
    // no draft to open.
    const landing = context({ view: { editing: false } });
    const [drafts] = paletteResults(landing, { query: 'draft' }).groups;
    expect(titles(drafts).slice(0, 2)).toEqual(['Show Drafts', 'Open draft']);
    expect(drafts.items[1].disabled).not.toBe('');
  });

  test('adds up to three nodes whose names match', () => {
    const results = paletteResults(context(), { query: 'a' });
    const named = results.groups.find((group) => group.label === 'Go to node');

    expect(named.items.length).toBeLessThanOrEqual(3);
    expect(titles(named)).toContain('alpha');
    expect(named.items[0].hint).toBe('@');
  });

  test('a read-only draft offers the editing commands with the reason', () => {
    const [paste] = paletteResults(context({ store: { readOnly: true } }), {
      query: 'paste',
    }).groups;

    expect(paste.items[0].disabled).toBe(READ_ONLY);
  });

  test('nothing found says what was searched and what to try', () => {
    const results = paletteResults(context(), { query: 'vlan 4095' });

    expect(results.total).toBe(0);
    expect(results.empty).toEqual({
      title: 'Nothing matches “vlan 4095”.',
      tip: 'The search included commands, node names, templates, collections and icons.',
    });
    expect(results.groups[0].items.map((item) => item.query)).toEqual([
      '@vlan 4095',
      '#vlan 4095',
      't:vlan 4095',
      'c:vlan 4095',
      'i:vlan 4095',
    ]);
  });
});

describe('@ nodes', () => {
  // alpha is labelled 'Web front end': its label is the name shown.
  test.each([
    ['label', 'front', null],
    ['hostname', 'alpha', 'Hostname'],
    ['image', 'ubuntu', 'Image'],
    ['the VLAN of an interface', 'exp', 'VLAN'],
    ['VLAN', 'vlan 100', 'VLAN'],
    ['IP address', '10.1.2', 'IP address'],
    ['MAC address', '0a:01', 'MAC address'],
  ])('are found by %s, saying which field matched', (_, text, field) => {
    const { doc, alpha } = addressed();
    const results = paletteResults(context({ store: { doc } }), {
      query: `@${text}`,
    });
    const found = results.groups[0].items.find(
      (item) => item.choice.value === alpha.id,
    );

    expect(results.mode).toBe('@');
    expect(results.noun).toBe('node');
    expect(found).toBeTruthy();
    expect(found.field?.label ?? null).toBe(field);
  });

  test('the title shows the name, the field what matched in it', () => {
    const { doc } = addressed();
    const [item] = paletteResults(context({ store: { doc } }), {
      query: '@10.1.2',
    }).groups[0].items;

    expect(item.title).toBe('Web front end');
    expect(item.field).toEqual({
      label: 'IP address',
      value: '10.1.2.3',
      prefix: 'eth0 IP address',
      rank: 1,
      ranges: [[0, 6]],
    });
  });

  // Devices of sampleDocument's first device, each with its own name and
  // spec fields, added in the order given.
  function withDevices(entries) {
    const { doc } = sampleDocument();
    const model = doc.nodes.find((node) => node.kind === 'device');

    for (const [id, label, spec] of entries) {
      doc.nodes.push({
        ...model,
        id,
        label,
        device: {
          ...model.device,
          hostname: label,
          spec: {
            ...model.device.spec,
            hardware: { drives: [{ image: 'base.qc2' }] },
            network: { interfaces: [] },
            ...spec,
          },
        },
      });
    }

    return doc;
  }

  const found = (doc, query) =>
    paletteResults(context({ store: { doc } }), { query }).groups[0].items;

  test('list a match in the name first, then by the field that matched', () => {
    // Added last field first, so the order is not the document's.
    const doc = withDevices([
      ['by-image', 'img', { hardware: { drives: [{ image: 'plc.qc2' }] } }],
      ['by-description', 'dsc', { general: { description: 'Main plc' } }],
      ['by-label', 'lbl', { labels: { role: 'plc' } }],
      ['by-annotation', 'ann', { annotations: { kind: 'plc' } }],
      [
        'by-vlan',
        'vln',
        { network: { interfaces: [{ name: 'eth1', vlan: 'plc' }] } },
      ],
      ['by-name', 'plc-01', {}],
    ]);
    const items = found(doc, '@plc');

    expect(items.map((item) => item.choice.value)).toEqual([
      'by-name',
      'by-vlan',
      'by-annotation',
      'by-label',
      'by-description',
      'by-image',
    ]);
    expect(items.map((item) => item.field?.label ?? null)).toEqual([
      null,
      'VLAN',
      'Annotation',
      'Label',
      'Description',
      'Image',
    ]);
  });

  test('a name match comes before an IP address, an IP before a MAC address', () => {
    const iface = (address, mac) => ({
      network: { interfaces: [{ name: 'eth0', address, mac }] },
    });
    const doc = withDevices([
      ['by-mac', 'm', iface('', '10:16:3e:00:00:01')],
      ['by-annotation', 'a', { annotations: { rack: 10 } }],
      // Within a field, the score keeps its order: a word at the start of
      // the value goes first.
      ['by-ip-late', 'i2', iface('192.168.10.1', '')],
      ['by-ip', 'i1', iface('10.0.0.5', '')],
      ['by-name', 'rack-10', {}],
    ]);

    // alpha and the switch of sampleDocument are found by VLAN 100.
    const ours = found(doc, '@10')
      .map((item) => item.choice.value)
      .filter((id) => id.startsWith('by-'));

    expect(ours).toEqual([
      'by-name',
      'by-ip',
      'by-ip-late',
      'by-mac',
      'by-annotation',
    ]);
  });

  test('a node found by its name letter by letter comes before the fields', () => {
    const doc = withDevices([
      ['by-label', 'x', { labels: { role: 'plc' } }],
      ['by-letters', 'p-l-c-gateway', {}],
      // Its label matches too, but its name ranks it.
      ['by-both', 'pump-l-c', { labels: { role: 'plc' } }],
      ['by-name', 'plc-1', {}],
    ]);
    const items = found(doc, '@plc');

    expect(items.map((item) => item.choice.value)).toEqual([
      'by-name',
      'by-letters',
      'by-both',
      'by-label',
    ]);
    // A name match marks the letters of the name, and shows no field.
    expect(items[1].ranges).toEqual([
      [0, 1],
      [2, 3],
      [4, 5],
    ]);
    expect(items[2].field).toBeNull();
    expect(items[3].field.label).toBe('Label');
  });

  test('a name ranks first, and the words of a query can spread over the name and the fields', () => {
    const iface = (address) => ({
      network: { interfaces: [{ name: 'eth0', address }] },
    });
    const doc = withDevices([
      // "pump" is in its name and "10.9" in its address only.
      ['by-keyword', 'pump-a', iface('10.9.0.1')],
      ['by-field', 'x', { general: { description: 'pump on 10.9' } }],
      ['by-name', 'pump 10.9', {}],
    ]);
    const items = found(doc, '@pump 10.9');

    expect(items.map((item) => item.choice.value)).toEqual([
      'by-name',
      'by-field',
      'by-keyword',
    ]);
    expect(items[2].field).toBeNull();
    expect(items[2].ranges).toEqual([]);
  });

  test('outside Go to node, a field match comes before a name letter by letter', () => {
    const command = {
      id: 'test.pick',
      title: 'Pick',
      group: 'Edit',
      steps: ['Thing'],
      choices: () => [
        { id: 'a', title: 'r-o-u-t-e-r', value: 'a' },
        {
          id: 'b',
          title: 'gw',
          fields: [{ label: 'Image', value: 'router.qc2' }],
          value: 'b',
        },
      ],
      run: () => {},
    };
    const { items } = paletteResults(context(), {
      query: 'router',
      command,
    }).groups[0];

    expect(items.map((item) => item.choice.value)).toEqual(['b', 'a']);
  });

  test('a command with nameFirst lists a name letter by letter before a field match', () => {
    const command = {
      id: 'test.pick',
      title: 'Pick',
      group: 'Edit',
      steps: ['Thing'],
      nameFirst: true,
      choices: () => [
        {
          id: 'b',
          title: 'gw',
          fields: [{ label: 'Image', value: 'router.qc2' }],
          value: 'b',
        },
        { id: 'a', title: 'r-o-u-t-e-r', value: 'a' },
      ],
      run: () => {},
    };
    const { items } = paletteResults(context(), {
      query: 'router',
      command,
    }).groups[0];

    expect(items.map((item) => item.choice.value)).toEqual(['a', 'b']);
    expect(getCommand('goto.node').nameFirst).toBe(true);
  });

  test('the second line shows what matched, after its interface or field', () => {
    const doc = withDevices([
      [
        'n1',
        'n1',
        {
          network: {
            interfaces: [
              {
                name: 'eth3',
                vlan: 'ot',
                address: '10.9.9.9',
                mac: '00:16:3e:0a:0b:0c',
              },
            ],
          },
        },
      ],
      ['n2', 'n2', { labels: { role: 'plc' } }],
      ['n3', 'n3', { annotations: { site: { name: 'north' } } }],
    ]);
    const text = (query) =>
      detailParts(found(doc, query)[0])
        .map((part) => (part.match ? `[${part.text}]` : part.text))
        .join('');

    expect(text('@10.9')).toBe(
      'eth3 IP address [10.9].9.9 · Device · base.qc2',
    );
    expect(text('@0a:0b')).toBe(
      'eth3 MAC address 00:16:3e:[0a:0b]:0c · Device · base.qc2',
    );
    expect(text('@ot')).toBe('eth3 VLAN [ot] · Device · base.qc2');
    expect(text('@role=plc')).toBe('label [role=plc] · Device · base.qc2');
    expect(text('@plc')).toBe('label role=[plc] · Device · base.qc2');
    expect(text('@north')).toBe(
      'annotation site={"name":"[north]"} · Device · base.qc2',
    );
  });

  test('a long description shows the part around the match', () => {
    const long = `${'a'.repeat(100)} pump ${'b'.repeat(100)}`;
    const doc = withDevices([['n1', 'n1', { general: { description: long } }]]);
    const parts = detailParts(found(doc, '@pump')[0]);
    const shown = parts.map((part) => part.text).join('');

    expect(parts[0]).toEqual({ text: 'description ', match: false });
    expect(parts.filter((part) => part.match)).toEqual([
      { text: 'pump', match: true },
    ]);
    expect(shown).toMatch(/^description …a+ pump b+… · Device · base\.qc2$/);
    expect(shown.length).toBeLessThan(long.length);
  });

  test('a long list is cut, and says how many there are', () => {
    const { doc } = sampleDocument();
    const device = doc.nodes.find((node) => node.kind === 'device');

    for (let i = 0; i < CHOICE_LIMIT + 10; i += 1) {
      doc.nodes.push({
        ...device,
        id: `copy-${i}`,
        label: `host-${i}`,
      });
    }

    const results = paletteResults(context({ store: { doc } }), {
      query: '@host',
    });

    expect(results.groups[0].items).toHaveLength(CHOICE_LIMIT);
    expect(results.total).toBe(CHOICE_LIMIT + 10);
    expect(results.more).toBe(10);
  });

  test('nothing found, or no diagram to search, is said', () => {
    expect(paletteResults(context(), { query: '@zzz' }).empty).toEqual({
      title: 'Nothing matches “zzz”.',
      tip: NODE_SEARCH,
    });
    expect(
      paletteResults(context({ view: { editing: false } }), { query: '@' })
        .empty.title,
    ).toBe('Open a draft first.');
  });
});

test('# finds networks by name or VLAN alias', () => {
  const byAlias = paletteResults(context(), { query: '#100' });
  const [network] = byAlias.groups[0].items;

  expect(byAlias.groups[0].label).toBe('Networks');
  expect(network.title).toBe('EXP');
  expect(network.field).toEqual({
    label: 'VLAN',
    value: 'VLAN 100',
    ranges: [[5, 8]],
  });
  expect(
    paletteResults(context(), { query: '#ex' }).groups[0].items[0],
  ).toEqual(expect.objectContaining({ title: 'EXP', ranges: [[0, 2]] }));
});

test('the second line shows a matched field, in place or named first', () => {
  const [network] = paletteResults(context(), { query: '#100' }).groups[0]
    .items;
  // "VLAN 100" is in the line already, and names itself.
  expect(detailParts(network)).toEqual([
    { text: 'VLAN ', match: false },
    { text: '100', match: true },
    { text: ' · 1 switch', match: false },
  ]);

  const { doc } = addressed();
  const [node] = paletteResults(context({ store: { doc } }), {
    query: '@10.1',
  }).groups[0].items;
  // An address shows after its interface and the name of its field.
  expect(detailParts(node)).toEqual([
    { text: 'eth0 IP address ', match: false },
    { text: '10.1', match: true },
    { text: '.2.3', match: false },
    { text: ' · Device · ubuntu.qc2 · EXP', match: false },
  ]);

  const [image] = paletteResults(context({ store: { doc } }), {
    query: '@ubuntu',
  }).groups[0].items;
  expect(detailParts(image)).toEqual([
    { text: 'Device · ', match: false },
    { text: 'ubuntu', match: true },
    { text: '.qc2 · EXP', match: false },
  ]);
  expect(detailParts({ detail: 'Choose a template' })).toEqual([
    { text: 'Choose a template', match: false },
  ]);
});

test('? lists the prefixes and the help', () => {
  const results = paletteResults(context(), { query: '?' });

  expect(results.groups.flatMap(titles)).toEqual([
    '@ Go to a node',
    '# Go to a network',
    't: Find a template',
    'c: Find a collection',
    'i: Find an icon',
    '> Commands',
    'Keyboard shortcuts',
    'Builder help',
  ]);
  expect(
    titles(
      paletteResults(context({ view: { editing: false } }), { query: '?' })
        .groups[0],
    ),
  ).toEqual([
    't: Find a template',
    'c: Find a collection',
    'i: Find an icon',
    '> Commands',
  ]);
});

test('the help of @ names every field that finds a node', () => {
  const short = (query) =>
    paletteResults(context(), { query })
      .groups.flatMap((group) => group.items)
      .find((item) => item.query?.startsWith('@')).detail;

  for (const label of Object.keys(NODE_FIELD_RANKS)) {
    expect(NODE_SEARCH.toLowerCase()).toContain(label.toLowerCase());
  }

  // The short help of ? and of Try instead names them in fewer words.
  for (const detail of [short('?'), short('zzqqxx')]) {
    expect(detail).toMatch(
      /name, hostname, IP or MAC address, VLAN, network, annotation, label or description$/,
    );
  }
});

describe('a step', () => {
  test('lists the choices of the command, searchable', () => {
    const command = getCommand('add.device');
    const all = paletteResults(context(), { command });

    expect(all.mode).toBe('step');
    expect(all.groups[0].label).toBe('Choose a template');
    expect(titles(all.groups[0])).toContain('Router');

    const [router] = paletteResults(context(), {
      command,
      query: 'rout',
    }).groups[0].items;
    expect(router).toEqual(
      expect.objectContaining({ kind: 'choice', title: 'Router', next: '' }),
    );
  });

  test('then the next step, after the first choice', () => {
    const { doc, alpha } = sampleDocument();
    const ctx = context({ store: { doc } });
    const command = getCommand('structure.connect');
    const [device] = nodeChoices(doc).filter(
      (choice) => choice.value === alpha.id,
    );
    const first = paletteResults(ctx, { command });
    const second = paletteResults(ctx, { command, picked: [device] });

    expect(first.groups[0].label).toBe('Choose a device');
    expect(first.groups[0].items[0].next).toBe('step');
    expect(second.groups[0].label).toBe('Choose a switch');
    expect(titles(second.groups[0])).toEqual(['EXP']);
  });
});

test('a layer step keeps each layer, to show its icon and color', () => {
  const sample = sampleDocument();
  const doc = setLayers(sample.doc, [
    { id: 3, label: 'Control', iconKey: 'plc', color: '#15803d' },
    { id: 1, label: 'Field' },
  ]);
  const ctx = context({
    store: { doc, selection: { nodes: [sample.alpha.id], edges: [] } },
  });
  const results = paletteResults(ctx, {
    command: getCommand('structure.setLayer'),
    picked: [],
  });
  const [step] = results.groups;

  expect(step.label).toBe('Choose a layer');
  expect(titles(step)).toEqual(['Control', 'Field', 'No layer']);
  expect(step.items[0].layer).toEqual(
    expect.objectContaining({ id: 3, iconKey: 'plc', color: '#15803d' }),
  );
  expect(step.items[2].layer).toBeNull();

  // The search finds a layer by its label.
  const found = paletteResults(ctx, {
    command: getCommand('structure.setLayer'),
    picked: [],
    query: 'cont',
  });

  expect(titles(found.groups[0])).toEqual(['Control']);

  // Both layer commands come up for "layer".
  const search = paletteResults(ctx, { query: 'layer' });
  const commands = search.groups.flatMap((group) => group.items);

  expect(commands.map((item) => item.title)).toEqual(
    expect.arrayContaining(['Set layer of selection', 'Select nodes on layer']),
  );
});

test('on the landing, drafts are found by name', () => {
  const ctx = context({
    view: { editing: false },
    store: {
      drafts: {
        all: [{ id: 'd1', createdBy: 'me', name: 'Substation lab' }],
      },
    },
  });
  const results = paletteResults(ctx, { query: 'substation' });
  const named = results.groups.find((group) => group.label === 'Open draft');

  expect(titles(named)).toEqual(['Substation lab']);
  expect(named.items[0].command.id).toBe('drafts.open');
});

describe('templates, collections and icons', () => {
  // The library: a template in the collection Pumps, a template in the
  // collection Panels, and a collection of the server.
  const PUMP = {
    id: 'pump',
    name: 'Pump controller',
    description: 'Runs the pumps',
    createdBy: 'alice',
    updatedBy: 'bob',
    device: {
      iconKey: 'plc',
      icon: 'pump-icon',
      layer: 1,
      spec: {
        type: 'VirtualMachine',
        general: { hostname: 'pump-ctl' },
        hardware: { drives: [{ image: 'plc.qc2' }] },
      },
    },
  };

  function library() {
    return libraryOf({
      templates: [
        PUMP,
        {
          id: 'hmi',
          name: 'HMI panel',
          createdBy: 'carol',
          device: { iconKey: 'desktop', spec: {} },
        },
        {
          id: 'srv',
          name: 'Historian',
          source: 'preloaded',
          device: { spec: {} },
        },
      ],
      collections: [
        {
          id: 'pumps',
          name: 'Pumps',
          templateIds: ['pump'],
          createdBy: 'alice',
          updatedBy: 'dave',
        },
        {
          id: 'panels',
          name: 'Panels',
          templateIds: ['hmi'],
        },
        {
          id: 'server-lab',
          name: 'Lab',
          source: 'preloaded',
          templateIds: ['srv'],
        },
      ],
    });
  }

  const ICONS = [
    {
      name: 'valve',
      owner: 'dave',
      aliases: ['valve-old'],
      width: 24,
      height: 24,
      bytes: 300,
      createdBy: 'dave',
      updatedBy: 'erin',
    },
  ];

  function libraryContext({ store = {}, view = {} } = {}) {
    return context({
      store: {
        templates: library(),
        ...store,
      },
      view: { icons: ICONS, ...view },
    });
  }

  const landing = (options = {}) =>
    libraryContext({ ...options, view: { editing: false, ...options.view } });

  test('t: in the editor finds templates by their fields, and names the field', () => {
    const ctx = libraryContext();

    for (const [text, label, value] of [
      ['runs the', 'Description', 'Runs the pumps'],
      // The icon's label, not its key.
      ['plc', 'Icon', 'PLC'],
      ['pump-icon', 'Custom icon', 'pump-icon'],
      ['layer 1', 'Layer', 'Layer 1'],
      ['virtualmachine', 'Type', 'VirtualMachine'],
      ['pump-ctl', 'Hostname', 'pump-ctl'],
      ['plc.qc2', 'Image', 'plc.qc2'],
      ['pumps', 'In collection', 'Pumps'],
      ['bob', 'Updated by', 'bob'],
    ]) {
      const results = paletteResults(ctx, { query: `t:${text}` });
      const found = results.groups[0]?.items.find(
        (item) => item.title === 'Pump controller',
      );

      expect(results.mode, text).toBe('t:');
      expect(results.groups[0].label).toBe('Templates');
      expect(found?.field, text).toEqual(
        expect.objectContaining({ label, value }),
      );
    }

    const [bob] = paletteResults(ctx, { query: 't:bob' }).groups[0].items;
    expect(detailParts(bob)).toEqual([
      { text: 'Updated by: ', match: false },
      { text: 'bob', match: true },
      { text: ' · Pumps · plc.qc2', match: false },
    ]);

    // Who created a template finds it, and its line names the field.
    const [hmi] = paletteResults(ctx, { query: 't:carol' }).groups[0].items;
    expect(hmi.title).toBe('HMI panel');
    expect(detailParts(hmi)).toEqual([
      { text: 'Created by: ', match: false },
      { text: 'carol', match: true },
      { text: ' · Panels · no image', match: false },
    ]);
  });

  test('t: finds a template by the label the open diagram gives its layer', () => {
    const { doc } = sampleDocument();
    const ctx = libraryContext({
      store: { doc: { ...doc, layers: [{ id: 1, label: 'Basic control' }] } },
    });
    const results = paletteResults(ctx, { query: 't:basic control' });
    const found = results.groups[0]?.items.find(
      (item) => item.title === 'Pump controller',
    );

    expect(found?.field).toEqual(
      expect.objectContaining({ label: 'Layer', value: 'Basic control' }),
    );
  });

  test('a template with no layer of its own is found by the layer its fields match', () => {
    const template = {
      id: 'plc',
      name: 'PLC',
      device: { spec: { general: { hostname: 'plc-01' } } },
    };
    const layers = [
      { id: 1, label: 'Control', matchers: { 'general.hostname': '^plc-' } },
      { id: 3, label: 'Operations', matchers: { 'general.hostname': '-01$' } },
    ];
    const layerField = (options) =>
      templateFields(template, options)
        .filter((field) => field.label === 'Layer')
        .map((field) => field.value);

    expect(layerField({ layers })).toEqual(['Layer 1', 'Control']);
    expect(layerField({ layers, matchConflict: 'highest' })).toEqual([
      'Layer 3',
      'Operations',
    ]);
    expect(layerField({ layers: [] })).toEqual([]);
  });

  test('t: finds a template by the key of its icon, also when the label differs', () => {
    const ctx = context({
      store: {
        templates: libraryOf({
          templates: [
            { id: 'rh', name: 'RHEL host', device: { iconKey: 'redhat' } },
          ],
        }),
      },
    });

    for (const [text, value] of [
      ['red hat', 'Red Hat'],
      ['redhat', 'redhat'],
    ]) {
      const [found] = paletteResults(ctx, { query: `t:${text}` }).groups[0]
        .items;

      expect(found?.title, text).toBe('RHEL host');
      expect(found.field, text).toEqual(
        expect.objectContaining({ label: 'Icon', value }),
      );
    }
  });

  test('a template chosen in the editor adds a device made from it', () => {
    const addNode = vi.fn((options) => ({ id: 'new', ...options }));
    const ctx = libraryContext({ store: { addNode } });
    const [pump] = paletteResults(ctx, { query: 't:pump controller' }).groups[0]
      .items;

    expect(pump.choice.value).toBe('library:pump');
    runCommand(pump.command, ctx, pump.choice, [pump.choice]);

    expect(addNode).toHaveBeenCalledWith(
      expect.objectContaining({ kind: 'device' }),
    );
    expect(addNode.mock.calls[0][0].hostname).toBe('pump-ctl');
  });

  test('a read-only draft adds no device from a template', () => {
    expect(
      paletteResults(libraryContext({ store: { readOnly: true } }), {
        query: 't:',
      }).empty,
    ).toEqual({ title: READ_ONLY, tip: '' });
  });

  test('on the drafts page, t: opens a template in the template editor', () => {
    const open = (ctx, query) => {
      const [item] = paletteResults(ctx, { query }).groups[0].items;

      runCommand(item.command, ctx, item.choice, [item.choice]);

      return ctx.view.openDialog.mock.calls.at(-1);
    };

    expect(open(landing(), 't:pump')).toEqual([
      'template',
      { mode: 'library-edit', id: 'pump' },
    ]);
    // A read-only template is viewed.
    expect(open(landing(), 't:historian')).toEqual([
      'template',
      { mode: 'view', id: 'srv', source: 'preloaded' },
    ]);
    expect(open(landing(), 't:firewall')).toEqual([
      'template',
      { mode: 'view', id: 'firewall', source: 'builtin' },
    ]);

    // The drafts page lists the library's templates, the server's, then
    // the built-in ones.
    const results = paletteResults(landing(), { query: 't:' });
    expect(titles(results.groups[0]).slice(0, 4)).toEqual([
      'Pump controller',
      'HMI panel',
      'Historian',
      'Server',
    ]);
    expect(
      results.groups[0].items.slice(0, 4).map((item) => item.detail),
    ).toEqual([
      'Library · plc.qc2',
      'Library · no image',
      'Server · no image',
      'Built-in · ubuntu.qc2',
    ]);
  });

  test('c: finds collections by template name and who changed them, and shows one', () => {
    const ctx = libraryContext();
    const [pumps] = paletteResults(ctx, { query: 'c:pump controller' })
      .groups[0].items;

    expect(pumps.title).toBe('Pumps');
    expect(pumps.field).toEqual(
      expect.objectContaining({ label: 'Template', value: 'Pump controller' }),
    );
    expect(pumps.detail).toBe('Library collections · 1 template');
    expect(
      paletteResults(ctx, { query: 'c:dave' }).groups[0].items[0].field,
    ).toEqual(expect.objectContaining({ label: 'Updated by', value: 'dave' }));

    runCommand(pumps.command, ctx, pumps.choice, [pumps.choice]);
    expect(ctx.view.openTemplateLibrary).toHaveBeenLastCalledWith('pumps');

    // A collection of the library is shown by its id, the server's by its
    // key.
    const values = paletteResults(ctx, { query: 'c:' }).groups[0].items.map(
      (item) => item.choice.value,
    );
    expect(values).toEqual(['pumps', 'panels', 'preloaded:server-lab']);
  });

  test('i: finds icons by other name, size and who changed them, and shows one', () => {
    const ctx = landing();

    for (const [text, label] of [
      ['valve-old', 'Also named'],
      ['24 × 24', 'Size'],
      ['erin', 'Updated by'],
    ]) {
      const [valve] = paletteResults(ctx, { query: `i:${text}` }).groups[0]
        .items;

      expect(valve.title, text).toBe('valve');
      expect(valve.field?.label, text).toBe(label);
    }

    const [valve] = paletteResults(ctx, { query: 'i:valve' }).groups[0].items;
    expect(valve.detail).toBe('Uploaded by dave · 24 × 24 pixels, 0.3 KiB');

    runCommand(valve.command, ctx, valve.choice, [valve.choice]);
    expect(ctx.view.openIcons).toHaveBeenLastCalledWith('valve');
  });

  test('a command search adds a few of each, found by words, with their prefix', () => {
    const results = paletteResults(libraryContext(), { query: 'pump' });
    const templates = results.groups.find((g) => g.label === 'Templates');
    const collections = results.groups.find((g) => g.label === 'Collections');

    expect(titles(templates)).toEqual(['Pump controller']);
    expect(templates.items[0].hint).toBe('t:');
    expect(titles(collections)).toEqual(['Pumps']);
    expect(collections.items[0].hint).toBe('c:');

    const icons = paletteResults(landing(), { query: 'erin' }).groups.find(
      (group) => group.label === 'Icons',
    );
    expect(icons.items[0].field).toEqual(
      expect.objectContaining({ label: 'Updated by', value: 'erin' }),
    );

    // Letters in order are not enough here, but are for t:.
    expect(
      labels(paletteResults(libraryContext(), { query: 'pmpctl' })),
    ).not.toContain('Templates');
    expect(
      titles(paletteResults(libraryContext(), { query: 't:pmpctl' }).groups[0]),
    ).toEqual(['Pump controller']);
  });

  test('says why there is nothing to find', () => {
    const ctx = context({ view: { editing: false } });

    expect(paletteResults(ctx, { query: 'c:' }).empty.title).toBe(
      'There are no collections.',
    );
    expect(paletteResults(ctx, { query: 'i:' }).empty.title).toBe(
      'There are no custom icons.',
    );
    // The built-in templates are always there to find.
    expect(paletteResults(ctx, { query: 't:' }).empty).toBeFalsy();
    expect(paletteResults(libraryContext(), { query: 't:zzz' }).empty).toEqual({
      title: 'Nothing matches “zzz”.',
      tip: TEMPLATE_SEARCH,
    });
  });
});

test('the selection is summed up in a sentence', () => {
  const { doc, alpha, bravo, edge } = sampleDocument();
  const summary = (nodes, edges = []) =>
    selectionSummary({ doc, selection: { nodes, edges } });

  expect(summary([])).toBe('Nothing is selected.');
  expect(summary([alpha.id, bravo.id])).toBe('Selected alpha and bravo.');
  expect(summary([alpha.id], [edge.id])).toBe(
    'Selected alpha and 1 connection.',
  );
  expect(summary(['a', 'b', 'c', 'd'])).toBe('Selected 4 nodes.');
});

// Typing stays quick in large diagrams. These count reads, not time: a
// document read once per node grows with the square of its size.
describe('a large diagram', () => {
  // 300 devices, each on one of 10 switches and its network. Every read of
  // a node, a connection and a device's interfaces is counted.
  function large(size = 300) {
    const reads = { nodes: 0, edges: 0, interfaces: 0 };
    const counted = (list, name) =>
      new Proxy(list, {
        get(target, key, receiver) {
          reads[name] += /^\d+$/.test(String(key)) ? 1 : 0;

          return Reflect.get(target, key, receiver);
        },
      });
    const networks = Array.from({ length: 10 }, (_, i) => ({
      id: `net-${i}`,
      name: `net-${i}`,
      alias: 100 + i,
    }));
    const switches = networks.map((network) => ({
      id: `sw-${network.id}`,
      kind: 'switch',
      switch: { networkId: network.id },
    }));
    const devices = Array.from({ length: size }, (_, i) => {
      const spec = {
        hardware: { drives: [{ image: 'ubuntu.qc2' }] },
        network: {
          interfaces: [{ name: 'eth0', vlan: `net-${i % 10}` }],
        },
      };

      return {
        id: `d-${i}`,
        kind: 'device',
        label: `host-${i}`,
        device: {
          hostname: `host-${i}`,
          spec: new Proxy(spec, {
            get(target, key) {
              reads.interfaces += key === 'network' ? 1 : 0;

              return target[key];
            },
          }),
        },
      };
    });
    const edges = devices.map((device, i) => ({
      id: `e-${i}`,
      sourceNodeId: device.id,
      targetNodeId: switches[i % 10].id,
      networkId: networks[i % 10].id,
    }));
    const doc = {
      nodes: counted([...devices, ...switches], 'nodes'),
      networks,
      edges: counted(edges, 'edges'),
    };

    return { doc, reads, size };
  }

  test('# counts each network’s switches reading each node once', () => {
    const { doc, reads, size } = large();
    const results = paletteResults(context({ store: { doc } }), {
      query: '#net-7',
    });

    expect(results.groups[0].items[0].detail).toBe('VLAN 107 · 1 switch');
    expect(reads.nodes).toBe(size + 10);
  });

  test('its nodes are listed reading each connection once', () => {
    const { doc, reads, size } = large();
    const choices = nodeChoices(doc);

    expect(choices).toHaveLength(size + 10);
    expect(choices[7].detail).toBe('Device · ubuntu.qc2 · net-7');
    expect(choices[size + 7].detail).toBe('Switch · net-7');
    expect(reads.edges).toBe(size);
  });

  test('a command search makes choices of the nodes it shows only', () => {
    const { doc, reads, size } = large();
    const results = paletteResults(context({ store: { doc } }), {
      query: 'host-12',
    });
    const named = results.groups.find((group) => group.label === 'Go to node');

    expect(titles(named)).toEqual(['host-12', 'host-120', 'host-121']);
    expect(named.items[0].detail).toBe('Device · ubuntu.qc2 · net-2');
    // The three shown, not every node.
    expect(reads.interfaces).toBeLessThanOrEqual(3 * 2);
    expect(reads.edges).toBe(size);
  });

  test('@ ranks 600 nodes by field, listing them once for every query', () => {
    const { doc, reads, size } = large(600);
    const ctx = context({ store: { doc } });
    const cache = createChoiceCache();

    for (const query of ['@n', '@ne', '@net', '@net-', '@net-7']) {
      paletteResults(ctx, { query, cache });
    }

    const results = paletteResults(ctx, { query: '@net-7', cache });
    const [first] = results.groups[0].items;

    // The switch of net-7 matches by network and its devices by VLAN. Both rank
    // alike, so the switch, after the devices, is not in the first 50.
    expect(results.total).toBe(size / 10 + 1);
    expect(results.more).toBe(results.total - CHOICE_LIMIT);
    expect(first.field).toEqual(
      expect.objectContaining({ label: 'VLAN', prefix: 'eth0 VLAN' }),
    );
    expect(reads.edges).toBe(size);
  });

  test('the palette lists the nodes once per document', () => {
    const { doc, reads, size } = large();
    const ctx = context({ store: { doc } });
    const cache = createChoiceCache();

    for (const query of ['@', '@h', '@ho', '@host', '@host-1']) {
      paletteResults(ctx, { query, cache });
    }
    expect(
      titles(paletteResults(ctx, { query: '@host-12', cache }).groups[0]).slice(
        0,
        3,
      ),
    ).toEqual(['host-12', 'host-120', 'host-121']);
    expect(reads.edges).toBe(size);

    // An edit replaces the document, so its nodes are listed again.
    ctx.store.doc = { ...doc, nodes: doc.nodes.slice(1) };
    expect(paletteResults(ctx, { query: '@', cache }).total).toBe(size + 9);
    expect(reads.edges).toBe(size * 2);
  });
});

test('Open Node Editor asks for a node only without one node selected', () => {
  const { doc, alpha, bravo } = sampleDocument();
  const item = (selection) =>
    paletteResults(
      context({ store: { doc, selection: { edges: [], ...selection } } }),
      { query: 'Open Node Editor' },
    )
      .groups.flatMap((group) => group.items)
      .find((entry) => entry.command?.id === 'node.edit');

  expect(item({ nodes: [alpha.id] })).toMatchObject({
    detail: 'alpha',
    next: '',
  });
  expect(item({ nodes: [alpha.id, bravo.id] })).toMatchObject({
    detail: 'Choose a node',
    next: 'step',
  });
  expect(item({ nodes: [] }).next).toBe('step');
});
