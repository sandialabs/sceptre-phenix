import { describe, expect, test, vi } from 'vitest';

import {
  CHOICE_LIMIT,
  NODE_SEARCH,
  createChoiceCache,
  detailParts,
  paletteResults,
  parseQuery,
  selectionSummary,
} from '@/builder/commandSearch.js';
import {
  READ_ONLY,
  VIEW_API,
  createCommandContext,
  getCommand,
  nodeChoices,
} from '@/builder/commands.js';

import { sampleDocument, withTemplates } from './fixtures.js';

function context({ store = {}, view = {} } = {}) {
  const { doc } = sampleDocument();
  const fullView = {
    editing: true,
    dialog: '',
    showMinimap: true,
    canZoomIn: true,
    canZoomOut: true,
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
      drafts: { mine: [], shared: [] },
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

test('parseQuery splits off @, # and ?, and ignores >', () => {
  expect(parseQuery('@plc')).toEqual({ prefix: '@', text: 'plc' });
  expect(parseQuery('#101')).toEqual({ prefix: '#', text: '101' });
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
        'Show Shared Drafts',
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

  // Each download format is a command, and all of them answer to the word
  // Download had before.
  test('finds the download commands by "export", and Share by "send"', () => {
    const exported = paletteResults(context(), { query: 'export' });

    expect(labels(exported)).toEqual(['Draft']);
    expect(titles(exported.groups[0])).toEqual([
      'Download…',
      'Download Builder JSON',
      'Download Builder YAML',
      'Download Topology YAML',
      'Download PNG',
      'Download SVG',
      'Download Gephi (GEXF)',
    ]);
    expect(exported.total).toBe(7);
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
    ]);
    expect(exported.groups[0].items[6].detail).toBe(
      'The network as a graph to analyze in Gephi',
    );

    // A format narrows it: the format's command first, Download… last.
    for (const [query, first] of [
      ['export gexf', 'Download Gephi (GEXF)'],
      ['export png', 'Download PNG'],
      ['gexf', 'Download Gephi (GEXF)'],
      ['gephi', 'Download Gephi (GEXF)'],
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

    // Share…, for its owner; it comes before a command matched by letters.
    const owner = context({
      store: { canShare: true },
      view: { panes: { hidden: { start: true }, stacked: false } },
    });
    const sent = paletteResults(owner, { query: 'send' });

    expect(sent.groups[0].items[0].title).toBe('Share…');
    expect(sent.groups[0].items[0].ranges).toEqual([]);
    expect(sent.groups.flatMap(titles)).toContain('Show Add nodes and Outline');

    // The drafts page has no draft to download or share.
    const landing = context({ view: { editing: false } });

    for (const query of ['export', 'send']) {
      expect(
        paletteResults(landing, { query }).groups.flatMap((group) =>
          group.items.filter((item) => item.kind === 'command'),
        ),
        query,
      ).toEqual([]);
    }
  });

  // Open experiment and Combine included nodes are offered only by a draft
  // that has an experiment or included nodes. They join the searches for
  // their own words and no other.
  test('a draft with an experiment and included nodes lists as many downloads and group commands', () => {
    const full = context({
      store: {
        experimentName: 'exp-1',
        canShare: true,
        summary: { included: 2 },
      },
    });
    const found = (query) =>
      paletteResults(full, { query }).groups.flatMap((group) =>
        group.items
          .filter((item) => item.kind === 'command')
          .map((item) => item.title),
      );

    expect(found('export')).toHaveLength(7);
    expect(paletteResults(full, { query: 'export' }).total).toBe(7);
    expect(found('download')).toHaveLength(7);
    // Move to a group… is one of them.
    expect(found('grp')).toHaveLength(7);
    expect(found('grp')).toContain('Move to a group…');
    expect(found('send')).toEqual(['Share…']);

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

    // "draft" is further into Show My Drafts and Show Shared Drafts than
    // into Open draft, which has no draft to open.
    const landing = context({ view: { editing: false } });
    const [drafts] = paletteResults(landing, { query: 'draft' }).groups;
    expect(titles(drafts).slice(0, 3)).toEqual([
      'Show My Drafts',
      'Show Shared Drafts',
      'Open draft',
    ]);
    expect(drafts.items[2].disabled).not.toBe('');
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
      tip: 'Commands and node names were searched.',
    });
    expect(results.groups[0].items.map((item) => item.query)).toEqual([
      '@vlan 4095',
      '#vlan 4095',
    ]);
  });
});

describe('@ nodes', () => {
  // alpha is labelled 'Web front end': its label is the name shown.
  test.each([
    ['label', 'front', null],
    ['hostname', 'alpha', 'Hostname'],
    ['image', 'ubuntu', 'Image'],
    ['network', 'exp', 'Network'],
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
      ranges: [[0, 6]],
    });
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
  expect(detailParts(node)).toEqual([
    { text: 'IP address ', match: false },
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
    '> Commands',
    'Keyboard shortcuts',
    'Builder help',
  ]);
  expect(
    paletteResults(context({ view: { editing: false } }), { query: '?' })
      .groups[0].items.length,
  ).toBe(1);
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

test('on the landing, drafts are found by name', () => {
  const ctx = context({
    view: { editing: false },
    store: {
      drafts: {
        mine: [{ id: 'd1', owner: 'me', name: 'Substation lab' }],
        shared: [],
      },
    },
  });
  const results = paletteResults(ctx, { query: 'substation' });
  const named = results.groups.find((group) => group.label === 'Open draft');

  expect(titles(named)).toEqual(['Substation lab']);
  expect(named.items[0].command.id).toBe('drafts.open');
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

// Typing must stay quick in thousands of nodes. These count what is read,
// not how long it takes: a document read once per node grows with the
// square of its size.
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
