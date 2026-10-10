import { describe, expect, test } from 'vitest';

import {
  decodeDocument,
  DEVICE_KEYS,
  DOCUMENT_KEYS,
  EDGE_KEYS,
  GRID_KEYS,
  GROUP_KEYS,
  HANDLE_KEYS,
  ICON_ENTRY_KEYS,
  ICON_NODE_KEYS,
  LINE_KEYS,
  METADATA_KEYS,
  NETWORK_KEYS,
  NODE_KEYS,
  NOTE_KEYS,
  parseDocument,
  parseImport,
  SHAPE_KEYS,
  SOURCE_KEYS,
  SWITCH_KEYS,
  TEMPLATE_DEVICE_KEYS,
  TEMPLATE_KEYS,
  VIEWPORT_KEYS,
} from '@/builder/decode.js';
import { addNode, SCHEMA_URI, STAMP_KEYS } from '@/builder/model.js';
import bundle from '@/builder/schema/builder-v1.schema.json';

import { sampleDocument, testId } from './fixtures.js';
import { ICON_DATA } from './png.js';

// A document that uses every presentation field a document may leave out:
// the custom icon and colors of a device, the colors of a switch, a group's
// description, border, icon key and custom icon, the line style of a network
// and of a connection, the includes that were not resolved, a template and
// the icon they use.
function decoratedDocument() {
  const sample = sampleDocument();
  const group = addNode(sample.doc, { kind: 'group', title: 'Rack' });
  const doc = JSON.parse(JSON.stringify(group.doc));
  const find = (id) => doc.nodes.find((node) => node.id === id);

  doc.icons = { plc: { data: ICON_DATA } };
  Object.assign(find(sample.alpha.id).device, {
    icon: 'plc',
    outlineColor: '#2f6fbf',
    fillColor: '#EEF4FB',
  });
  Object.assign(find(sample.sw.id).switch, {
    outlineColor: '#1f7a5a',
    fillColor: '#e8f5f0',
  });
  Object.assign(find(group.node.id).group, {
    description: 'first rack\nsecond line',
    borderStyle: 'dotted',
    iconKey: 'container',
    icon: 'plc',
  });
  doc.networks[0].lineStyle = 'dashed';
  doc.edges[0].lineStyle = 'dash-dot';
  doc.source = {
    kind: 'topology',
    includeTopologies: ['shared', 'plant'],
    unresolvedIncludes: ['plant'],
  };
  doc.templates = [
    {
      id: testId(),
      name: 'PLC',
      description: 'Programmable logic controller',
      device: {
        iconKey: 'server',
        icon: 'plc',
        outlineColor: '#2f6fbf',
        fillColor: '#EEF4FB',
        spec: {
          type: 'VirtualMachine',
          general: { hostname: 'plc', vm_type: 'kvm' },
          hardware: { os_type: 'linux', drives: [{ image: 'plc.qc2' }] },
          network: { interfaces: [{ name: 'eth0', vlan: 'EXP' }] },
        },
      },
    },
  ];

  return doc;
}

describe('strict decoding', () => {
  test('round trips a valid document', () => {
    const { doc } = sampleDocument();
    const decoded = parseDocument(JSON.parse(JSON.stringify(doc)));

    expect(decoded).toEqual(doc);
  });

  test('unknown document fields are rejected, never dropped', () => {
    const { doc } = sampleDocument();

    expect(() => decodeDocument({ ...doc, extra: true })).toThrowError(
      /unknown field/i,
    );
  });

  test('unknown node fields are rejected', () => {
    const { doc } = sampleDocument();
    const payload = JSON.parse(JSON.stringify(doc));

    payload.nodes[0].vlan = 'EXP';

    expect(() => decodeDocument(payload)).toThrowError(/unknown field/i);
  });

  test('accepts complete server source provenance', () => {
    const { doc } = sampleDocument();
    const payload = {
      ...doc,
      source: {
        kind: 'topology',
        name: 'generated',
        apiVersion: 'phenix.sandia.gov/v1',
        topology: 'base',
        importedAt: '2026-08-28T20:00:00Z',
        digest: `sha256:${'a'.repeat(64)}`,
        updatedAt: '2026-08-28T19:00:00Z',
        warnings: ['Generated layout'],
      },
    };

    expect(() => decodeDocument(payload)).not.toThrow();
  });

  test('the schema URI and revision are enforced', () => {
    const { doc } = sampleDocument();

    expect(() => decodeDocument({ ...doc, $schema: 'x' })).toThrowError(
      /schema/i,
    );
    expect(() => decodeDocument({ ...doc, revision: 2 })).toThrowError(
      /revision/i,
    );
    expect(SCHEMA_URI).toContain('/schemas/builder/v1');
  });

  test('keeps shapes, icons and lines, and refuses unknown fields in them', () => {
    const { doc } = sampleDocument();
    const shape = addNode(doc, {
      kind: 'shape',
      shape: 'circle',
      label: 'DMZ',
      fillColor: '#eef4fb',
      borderStyle: 'dotted',
    });
    const icon = addNode(shape.doc, {
      kind: 'icon',
      iconKey: 'external',
      label: 'Internet',
    });
    const line = addNode(icon.doc, {
      kind: 'line',
      points: [
        { x: 0, y: 0 },
        { x: 160, y: 40 },
      ],
      color: '#c0392b',
      lineStyle: 'dashed',
      endArrow: true,
    });
    const payload = JSON.parse(JSON.stringify(line.doc));

    expect(parseDocument(payload)).toEqual(line.doc);

    const at = (id) => payload.nodes.findIndex((node) => node.id === id);

    payload.nodes[at(line.node.id)].line.points[1].z = 1;

    expect(() => decodeDocument(payload)).toThrowError(
      /line\.points\[1\]: unknown field "z"/,
    );

    delete payload.nodes[at(line.node.id)].line.points[1].z;
    payload.nodes[at(icon.node.id)].icon.color = '#000000';

    expect(() => decodeDocument(payload)).toThrowError(
      /\.icon: unknown field "color"/,
    );

    delete payload.nodes[at(icon.node.id)].icon.color;
    payload.nodes[at(shape.node.id)].shape.text = 'DMZ';

    expect(() => decodeDocument(payload)).toThrowError(
      /\.shape: unknown field "text"/,
    );
  });

  test('a node must carry exactly one payload', () => {
    const { doc } = sampleDocument();
    const payload = JSON.parse(JSON.stringify(doc));

    payload.nodes[0].note = { text: 'x' };

    expect(() => decodeDocument(payload)).toThrowError(/exactly one/i);
  });

  test('keeps the draft layout and edge routes, and reads null as none', () => {
    const { doc } = sampleDocument();
    const route = [
      { x: 0, y: 0 },
      { x: 40, y: 0 },
      { x: 40, y: 30 },
    ];
    const payload = JSON.parse(JSON.stringify(doc));

    payload.layout = 'cards';
    payload.edges[0].route = route;

    const decoded = parseDocument(payload);

    expect(decoded.layout).toBe('cards');
    expect(decoded.edges[0].route).toEqual(route);

    payload.layout = null;
    payload.edges[0].route = null;

    const cleared = parseDocument(payload);

    expect('layout' in cleared).toBe(false);
    expect('route' in cleared.edges[0]).toBe(false);

    payload.edges[0].route = [{ x: 0, y: 0, z: 1 }, route[1]];

    expect(() => decodeDocument(payload)).toThrowError(
      /edges\[0\]\.route\[0\]: unknown field "z"/,
    );
  });

  // The server refuses a key its Document struct lacks and the editor one
  // DOCUMENT_KEYS lacks, so a key added on one side alone makes the other
  // refuse every document that has it. The bundle is generated from the
  // server's schema.
  test('the document keys are the root properties of the schema bundle', () => {
    expect([...DOCUMENT_KEYS].sort()).toEqual(
      Object.keys(bundle.properties).sort(),
    );
  });

  // The same holds for every object in a document: a test on the server
  // holds each definition of the bundle to the fields of its Go type.
  test.each([
    ['metadata', METADATA_KEYS],
    ['node', NODE_KEYS],
    ['device', DEVICE_KEYS],
    ['interfaceHandle', HANDLE_KEYS],
    ['switch', SWITCH_KEYS],
    ['note', NOTE_KEYS],
    ['group', GROUP_KEYS],
    ['shape', SHAPE_KEYS],
    ['iconNode', ICON_NODE_KEYS],
    ['line', LINE_KEYS],
    ['network', NETWORK_KEYS],
    ['edge', EDGE_KEYS],
    ['viewport', VIEWPORT_KEYS],
    ['grid', GRID_KEYS],
    ['source', SOURCE_KEYS],
    ['template', TEMPLATE_KEYS],
    ['templateDevice', TEMPLATE_DEVICE_KEYS],
    ['icon', ICON_ENTRY_KEYS],
  ])('the %s keys are the properties of its definition', (name, keys) => {
    expect([...keys].sort()).toEqual(
      Object.keys(bundle.$defs[name].properties).sort(),
    );
  });

  // The server decodes scenarios as a list of text: anything else is no
  // document, and a null entry is an empty name, which validation refuses.
  test('scenarios are a list of names', () => {
    const { doc } = sampleDocument();

    for (const scenarios of ['plant-ntp', { name: 'plant-ntp' }, [1], [{}]]) {
      expect(() => decodeDocument({ ...doc, scenarios })).toThrowError(
        /"scenarios" must be an array of scenario names/,
      );
    }

    expect(
      decodeDocument({ ...doc, scenarios: ['plant-ntp'] }).scenarios,
    ).toEqual(['plant-ntp']);
    expect(() => parseDocument({ ...doc, scenarios: [null] })).toThrowError(
      /scenario name is required/,
    );
  });

  // A template fills in a device, so a key added to devices is one of a
  // template's device too, but for what a device has of its own.
  test('a template device has the keys of a device, less its own', () => {
    expect([...TEMPLATE_DEVICE_KEYS]).toEqual([
      'iconKey',
      'icon',
      'outlineColor',
      'fillColor',
      'spec',
    ]);
    expect(
      [...DEVICE_KEYS].filter((key) => !TEMPLATE_DEVICE_KEYS.has(key)),
    ).toEqual(['hostname', 'interfaces', 'includedFrom']);
  });

  test('keeps every presentation field', () => {
    const doc = decoratedDocument();
    const decoded = parseDocument(JSON.parse(JSON.stringify(doc)));

    expect(decoded).toEqual(doc);
    expect(JSON.stringify(decoded)).toBe(JSON.stringify(doc));
  });

  // A document that uses none of them gains no key, so it is saved as it
  // was, with the digest it had.
  test('adds no presentation field to a document without one', () => {
    const { doc } = sampleDocument();
    const text = JSON.stringify(parseDocument(JSON.parse(JSON.stringify(doc))));

    expect(text).toBe(JSON.stringify(doc));

    for (const key of [
      'templates',
      'icons',
      'icon',
      'outlineColor',
      'fillColor',
      'borderStyle',
      'lineStyle',
      'unresolvedIncludes',
    ]) {
      expect(text, key).not.toContain(`"${key}"`);
    }
  });

  test('reads null templates and icons as none, and keeps empty ones', () => {
    const { doc } = sampleDocument();
    const none = parseDocument({ ...doc, templates: null, icons: null });

    expect('templates' in none).toBe(false);
    expect('icons' in none).toBe(false);

    const empty = parseDocument({ ...doc, templates: [], icons: {} });

    expect(empty.templates).toEqual([]);
    expect(empty.icons).toEqual({});
  });

  test('templates and icons of the wrong shape are refused', () => {
    const doc = decoratedDocument();
    const [template] = doc.templates;
    const refused = (change) => () => decodeDocument({ ...doc, ...change });

    expect(refused({ templates: { 0: template } })).toThrowError(
      'document: "templates" must be an array',
    );
    expect(refused({ templates: ['PLC'] })).toThrowError(
      'templates[0]: expected an object',
    );
    expect(refused({ templates: [{ ...template, hint: 'x' }] })).toThrowError(
      'templates[0]: unknown field "hint"',
    );
    expect(
      refused({ templates: [{ ...template, device: 'server' }] }),
    ).toThrowError('templates[0].device: expected an object');

    // A template fills in a device, and has none of a device's own fields.
    for (const key of ['hostname', 'interfaces', 'includedFrom', 'label']) {
      expect(
        refused({
          templates: [
            template,
            { ...template, device: { ...template.device, [key]: 'x' } },
          ],
        }),
        key,
      ).toThrowError(`templates[1].device: unknown field "${key}"`);
    }

    expect(refused({ icons: [] })).toThrowError(
      'document: "icons" must be an object',
    );
    expect(refused({ icons: 'x' })).toThrowError(
      'document: "icons" must be an object',
    );
    expect(refused({ icons: { plc: ICON_DATA } })).toThrowError(
      'icons: expected an object',
    );
    expect(
      refused({
        icons: { plc: { data: ICON_DATA, type: 'image/svg+xml' } },
      }),
    ).toThrowError('icons: unknown field "type"');
    // An icon's name is its key: an entry holds its data alone.
    expect(
      refused({ icons: { plc: { name: 'plc', data: ICON_DATA } } }),
    ).toThrowError('icons: unknown field "name"');
  });

  test('unknown fields beside the presentation fields are refused', () => {
    const doc = decoratedDocument();
    const withKey = (pick) => {
      const payload = JSON.parse(JSON.stringify(doc));

      pick(payload).color2 = '#fff';

      return () => decodeDocument(payload);
    };

    expect(
      withKey((d) => d.nodes.find((n) => n.kind === 'switch').switch),
    ).toThrowError(/switch: unknown field "color2"/);
    expect(
      withKey((d) => d.nodes.find((n) => n.kind === 'group').group),
    ).toThrowError(/group: unknown field "color2"/);
    expect(withKey((d) => d.networks[0])).toThrowError(
      'networks[0]: unknown field "color2"',
    );
    expect(withKey((d) => d.edges[0])).toThrowError(
      'edges[0]: unknown field "color2"',
    );
    expect(withKey((d) => d.source)).toThrowError(
      'source: unknown field "color2"',
    );
  });

  test('accepts who made and last saved the document, and reads null as none', () => {
    const { doc } = sampleDocument();
    const stamp = {
      createdBy: 'alice',
      createdAt: '2026-10-01T15:04:05Z',
      updatedBy: 'bob@example.com',
      updatedAt: '2026-10-01T16:10:00Z',
    };
    const withMetadata = (fields) => ({
      ...doc,
      metadata: { ...doc.metadata, ...fields },
    });
    const decoded = parseDocument(withMetadata(stamp));

    expect(decoded.metadata).toMatchObject(stamp);

    const none = parseDocument(
      withMetadata({
        createdBy: null,
        createdAt: null,
        updatedBy: null,
        updatedAt: null,
        notes: null,
      }),
    );

    expect(STAMP_KEYS.some((key) => key in none.metadata)).toBe(false);
    expect('notes' in none.metadata).toBe(false);
    // A key near one of them is still unknown.
    expect(() =>
      decodeDocument(withMetadata({ updatedby: 'bob' })),
    ).toThrowError(/metadata: unknown field "updatedby"/);
    expect(() => decodeDocument({ ...doc, owner: 'bob' })).toThrowError(
      /document: unknown field "owner"/,
    );
  });

  // The fields a document says of itself are in its metadata only: a
  // document of the shape before it had metadata is refused.
  test('the metadata fields are refused at the root, and the author by its old name', () => {
    const { doc } = sampleDocument();

    for (const key of [
      'id',
      'name',
      'description',
      'author',
      'createdAt',
      'updatedBy',
      'updatedAt',
      'notes',
    ]) {
      expect(() => decodeDocument({ ...doc, [key]: 'x' }), key).toThrowError(
        `document: unknown field "${key}"`,
      );
    }

    expect(() =>
      decodeDocument({ ...doc, metadata: { ...doc.metadata, author: 'x' } }),
    ).toThrowError('metadata: unknown field "author"');
  });

  test('metadata and notes of the wrong shape are refused', () => {
    const { doc } = sampleDocument();

    expect(() => decodeDocument({ ...doc, metadata: 'x' })).toThrowError(
      'document: "metadata" must be an object',
    );
    expect(() => decodeDocument({ ...doc, metadata: [] })).toThrowError(
      'document: "metadata" must be an object',
    );
    expect(() =>
      decodeDocument({ ...doc, metadata: { ...doc.metadata, notes: 'x' } }),
    ).toThrowError('document: "metadata.notes" must be an array');

    // Left out, the metadata is empty, as Go decodes it, and the document
    // is refused for the id it lacks.
    const { metadata, ...bare } = doc;

    expect(metadata.id).toBeTruthy();
    expect(decodeDocument(bare).metadata).toEqual({});
    expect(() => parseDocument(bare)).toThrowError(
      'metadata.id: document ID is required',
    );
  });
});

describe('parseImport', () => {
  test('accepts JSON and YAML builder documents', () => {
    const { doc } = sampleDocument();
    const json = parseImport(JSON.stringify(doc));

    expect(json.ok).toBe(true);
    expect(json.document.metadata.id).toBe(doc.metadata.id);

    const yaml = parseImport(
      `$schema: ${doc.$schema}\nrevision: 1\nmetadata: {id: ${doc.metadata.id}}\nnodes: []\nnetworks: []\nedges: []\nviewport: {x: 0, y: 0, zoom: 1}\ngrid: {enabled: true, size: 16, snap: true}\n`,
    );

    expect(yaml.ok).toBe(true);
  });

  test('refuses phenix Topology and Experiment configs with guidance', () => {
    const result = parseImport(
      JSON.stringify({ kind: 'Topology', metadata: { name: 'x' } }),
    );

    expect(result.ok).toBe(false);
    expect(result.code).toBe('unsupported-kind');
    // Import on the drafts page is what converts them.
    expect(result.error).toMatch(/\bUse Import on the drafts page\b/);
    expect(result.error).toMatch(/^A Topology config is not/);

    // The article follows the kind.
    expect(
      parseImport(JSON.stringify({ kind: 'Experiment', metadata: {} })).error,
    ).toMatch(/^An Experiment config is not a Builder document\./);
    expect(
      parseImport('kind: Topology\n', { as: 'raw', expectedKind: 'Experiment' })
        .error,
    ).toBe('Expected an Experiment config, not Topology.');
  });

  test('reports invalid documents instead of repairing them', () => {
    const { doc } = sampleDocument();
    const payload = JSON.parse(JSON.stringify(doc));

    payload.edges[0].networkId = 'missing';

    const result = parseImport(JSON.stringify(payload));

    expect(result.ok).toBe(false);
    expect(result.issues.length).toBeGreaterThan(0);
  });

  test('empty and unparsable input is named', () => {
    expect(parseImport('  ').code).toBe('empty');
    expect(parseImport('{"a": ').code).toBe('parse');
  });

  test('raw mode returns the parsed value for scenario uploads', () => {
    const result = parseImport(
      'apiVersion: phenix.sandia.gov/v1\nkind: Scenario\n',
      {
        as: 'raw',
      },
    );

    expect(result.ok).toBe(true);
    expect(result.value.kind).toBe('Scenario');
  });

  test('YAML imports stay JSON-compatible', () => {
    const result = parseImport('created: 2026-08-28T12:00:00Z\n', {
      as: 'raw',
    });

    expect(result.ok).toBe(true);
    expect(result.value.created).toBe('2026-08-28T12:00:00Z');
  });

  // An alias is its anchor's value itself, which each copy of the document
  // expands once per alias: nine levels of ten aliases, in under 1 KB, are a
  // billion values. An asterisk that is not an alias is kept.
  test('YAML aliases are refused before they expand', () => {
    const bomb = [
      'l0: &l0 x',
      ...Array.from(
        { length: 9 },
        (_, i) => `l${i + 1}: &l${i + 1} [${Array(10).fill(`*l${i}`).join()}]`,
      ),
    ].join('\n');

    expect(bomb.length).toBeLessThan(1024);
    expect(parseImport(bomb, { as: 'raw' })).toMatchObject({
      ok: false,
      code: 'aliases',
      error: expect.stringMatching(
        /^YAML aliases \(\*name\) are not supported\./,
      ),
    });
    expect(parseImport('a: &a 1\nb: *a\n', { as: 'raw' }).code).toBe('aliases');
    expect(
      parseImport("a: &a [1]\nb: 'a *b'\nc: run *nix\nd: |\n  *e\n# *f\n", {
        as: 'raw',
      }),
    ).toEqual({
      ok: true,
      value: { a: [1], b: 'a *b', c: 'run *nix', d: '*e\n' },
    });
  });

  test('imports enforce size and expected config kind', () => {
    expect(parseImport('12345', { as: 'raw', maxBytes: 4 }).code).toBe(
      'too-large',
    );
    expect(
      parseImport('kind: Topology\n', {
        as: 'raw',
        expectedKind: 'Scenario',
      }).code,
    ).toBe('unsupported-kind');
  });
});
