import { readFileSync } from 'node:fs';

import Ajv2020 from 'ajv/dist/2020.js';
import { describe, expect, test } from 'vitest';

import { parseDocument } from '@/builder/decode.js';
import { MAX_DOCUMENT_ICONS, MAX_ICON_NAME_BYTES } from '@/builder/icons.js';
import {
  addNetwork,
  addNode,
  BORDER_STYLES,
  connect,
  createDocument,
  HEX_COLOR,
  LINE_STYLES,
  MAX_LAYER_ID,
  MAX_LAYER_LABEL_BYTES,
  MAX_LAYERS,
  PLACEHOLDER_LAYERS,
} from '@/builder/model.js';
import bundle from '@/builder/schema/builder-v1.schema.json';
import CODES from '@/builder/schema/codes.json';
import {
  deviceFieldWarnings,
  iconNameProblem,
  isTime,
  MAX_ANNOTATION_BYTES,
  MAX_ANNOTATIONS,
  MAX_DIAGRAM_NOTE_BYTES,
  MAX_DIAGRAM_NOTES,
  MAX_NAME_BYTES,
  MAX_SCENARIO_NAME_BYTES,
  MAX_SCENARIOS,
  MAX_TEMPLATE_DESCRIPTION_BYTES,
  MAX_TEMPLATE_DEVICE_BYTES,
  MAX_TEMPLATE_NAME_BYTES,
  MAX_TEMPLATES,
  MAX_USER_BYTES,
  MAX_VLAN_ALIAS,
  scenarioNameProblem,
  templateIssues,
  validateDocument,
  validateIcons,
} from '@/builder/validate.js';

import { errorsOf, sampleDocument, testId } from './fixtures.js';
import { base64Of, ICON_DATA, ICON_KEY, png } from './png.js';

function paths(doc) {
  return errorsOf(doc).map((issue) => issue.path);
}

// A new document whose metadata holds the given fields too.
function withMetadata(fields) {
  const doc = createDocument();

  return { ...doc, metadata: { ...doc.metadata, ...fields } };
}

describe('document validation', () => {
  test('a well formed document has no errors', () => {
    const { doc } = sampleDocument();

    expect(errorsOf(doc)).toEqual([]);
  });

  test('the schema URI and revision are checked', () => {
    const doc = { ...createDocument(), $schema: 'other', revision: 2 };

    expect(paths(doc)).toEqual(expect.arrayContaining(['$schema', 'revision']));
  });

  // The schema bundle is generated from the server's limits, so a limit
  // changed there fails here rather than going unnoticed.
  test('the limits are the ones the schema bundle carries', () => {
    expect(MAX_VLAN_ALIAS).toBe(bundle.$defs.network.properties.alias.maximum);
    expect(MAX_ANNOTATIONS).toBe(
      bundle.$defs.source.properties.annotations.maxProperties,
    );
    // The schema's maxLength counts characters, where the server counts
    // bytes (MaxNameBytes), so it is only equal, not the same bound.
    const metadata = bundle.$defs.metadata.properties;

    expect(MAX_NAME_BYTES).toBe(metadata.name.maxLength);
    expect(MAX_USER_BYTES).toBe(metadata.createdBy.maxLength);
    expect(MAX_USER_BYTES).toBe(metadata.updatedBy.maxLength);
    expect(MAX_DIAGRAM_NOTES).toBe(metadata.notes.maxItems);
    expect(MAX_DIAGRAM_NOTE_BYTES).toBe(metadata.notes.items.maxLength);
  });

  // The bundle's note pattern, read by ajv, takes a one-character note exactly
  // when validation does, for blank, zero width and control characters.
  test('the schema takes the notes validation takes', () => {
    const accepts = new Ajv2020({ strict: false }).compile(
      bundle.$defs.metadata.properties.notes.items,
    );
    const spans = [
      [0, 0xff],
      [0x1680, 0x1680],
      [0x180e, 0x180e],
      [0x2000, 0x2010],
      [0x2028, 0x2030],
      [0x205f, 0x2060],
      [0x3000, 0x3001],
      [0xfeff, 0xfeff],
    ];
    const differ = [];

    for (const [from, to] of spans) {
      for (let code = from; code <= to; code += 1) {
        const note = String.fromCodePoint(code);
        const valid = errorsOf(withMetadata({ notes: [note] })).length === 0;

        if (accepts(note) !== valid) {
          differ.push(`U+${code.toString(16).toUpperCase().padStart(4, '0')}`);
        }
      }
    }

    expect(differ).toEqual([]);
  });

  test('the name is bounded the way the server bounds a draft title', () => {
    const named = (name) => withMetadata({ name });

    expect(paths(named('n'.repeat(MAX_NAME_BYTES)))).toEqual([]);
    // Bytes, not characters: 257 two-byte characters are 514 bytes.
    expect(paths(named('é'.repeat(MAX_NAME_BYTES / 2 + 1)))).toEqual([
      'metadata.name',
    ]);
    expect(paths(named('my\ttopology'))).toEqual(['metadata.name']);
  });

  test('the metadata is where a document says what it is', () => {
    const { metadata, ...rest } = createDocument();

    // Left out, it is empty, as Go decodes it, and has no id.
    expect(errorsOf(rest).map(({ path, message }) => [path, message])).toEqual([
      ['metadata.id', 'a document needs an ID'],
    ]);
    expect(paths({ ...rest, metadata: { ...metadata, id: 'doc-1' } })).toEqual([
      'metadata.id',
    ]);
  });

  // validateUser and validateTime in validate.go, message for message.
  test('the creator and the last editor are bounded text', () => {
    const errors = (key, value) =>
      errorsOf(withMetadata({ [key]: value })).map(({ path, message }) => [
        path,
        message,
      ]);

    for (const key of ['createdBy', 'updatedBy']) {
      const field = `metadata.${key}`;

      expect(errors(key, 'alice')).toEqual([]);
      expect(errors(key, 'a'.repeat(MAX_USER_BYTES))).toEqual([]);
      // None, as Go decodes them.
      expect(errors(key, '')).toEqual([]);
      expect(errors(key, null)).toEqual([]);
      expect(errors(key, undefined)).toEqual([]);
      expect(errors(key, 'a'.repeat(MAX_USER_BYTES + 1))).toEqual([
        [field, `${field} must be at most 256 bytes`],
      ]);
      // Bytes, not characters: 86 three-byte characters are 258 bytes.
      expect(errors(key, '€'.repeat(86))).toEqual([
        [field, `${field} must be at most 256 bytes`],
      ]);
      expect(errors(key, 'alice\tsmith')).toEqual([
        [field, `${field} must not contain control characters`],
      ]);
      expect(errors(key, 'bob\u007f')).toEqual([
        [field, `${field} must not contain control characters`],
      ]);
      // Too long is said once, not with the control character too.
      expect(errors(key, `${'a'.repeat(MAX_USER_BYTES)}\n`)).toEqual([
        [field, `${field} must be at most 256 bytes`],
      ]);
      expect(errors(key, 7)).toEqual([[field, `${field} must be a string`]]);
      expect(errors(key, { user: 'bob' })).toEqual([
        [field, `${field} must be a string`],
      ]);
    }
  });

  test('the creation time and the last edit time have one form', () => {
    const errors = (key, value) =>
      errorsOf(withMetadata({ [key]: value })).map(({ path, message }) => [
        path,
        message,
      ]);

    for (const key of ['createdAt', 'updatedAt']) {
      const field = `metadata.${key}`;
      const refused = [
        [field, `${field} must be a UTC time in the form YYYY-MM-DDTHH:MM:SSZ`],
      ];

      expect(errors(key, '2026-10-01T15:04:05Z')).toEqual([]);
      expect(errors(key, '')).toEqual([]);
      expect(errors(key, null)).toEqual([]);
      expect(errors(key, '2026-10-01T15:04:05+00:00')).toEqual(refused);
      expect(errors(key, '2026-10-01T15:04:05.000Z')).toEqual(refused);
      expect(errors(key, '2026-02-30T00:00:00Z')).toEqual(refused);
      expect(errors(key, 1790866800)).toEqual([
        [field, `${field} must be a string`],
      ]);
      expect(errors(key, true)).toEqual([[field, `${field} must be a string`]]);
    }

    // There is no rule between the four: an editor without a time, and a
    // creation after the last edit, are valid.
    expect(
      errorsOf(
        withMetadata({
          updatedBy: 'bob',
          createdAt: '2026-10-02T00:00:00Z',
          updatedAt: '2024-02-29T23:59:59Z',
        }),
      ),
    ).toEqual([]);
  });

  // What Go's time.Parse and Format accept and give back with the layout
  // 2006-01-02T15:04:05Z (IsTime in validate.go), case for case.
  test.each([
    ['2026-10-01T15:04:05Z', true],
    ['0000-01-01T00:00:00Z', true],
    ['0000-02-29T00:00:00Z', true],
    ['9999-12-31T23:59:59Z', true],
    ['2000-02-29T00:00:00Z', true],
    ['2024-02-29T23:59:59Z', true],
    ['1900-02-29T00:00:00Z', false],
    ['2026-02-29T00:00:00Z', false],
    ['2026-00-01T00:00:00Z', false],
    ['2026-13-01T00:00:00Z', false],
    ['2026-01-00T00:00:00Z', false],
    ['2026-04-31T00:00:00Z', false],
    ['2026-10-01T24:00:00Z', false],
    ['2026-10-01T23:60:00Z', false],
    ['2026-10-01T23:59:60Z', false],
    ['2026-10-01T15:04:05Z\n', false],
    [' 2026-10-01T15:04:05Z', false],
    ['２０２６-10-01T15:04:05Z', false],
    ['+026-10-01T15:04:05Z', false],
    ['2026-10-01T15:04:05,5Z', false],
    ['2026-1-01T15:04:05Z', false],
    ['2026-10-01T5:04:05Z', false],
    ['2026-10-01T15:04:05z', false],
    ['2026-10-01 15:04:05Z', false],
    ['2026-10-01T15:04:05', false],
    ['2026-10-01', false],
    ['', false],
  ])('the time %j is valid: %s', (text, valid) => {
    expect(isTime(text)).toBe(valid);
  });

  test('source annotations are bounded the way the server bounds them', () => {
    const annotated = (annotations) => ({
      ...createDocument(),
      source: { kind: 'topology', name: 'core', annotations },
    });
    const messages = (annotations) =>
      errorsOf(annotated(annotations)).map((issue) => issue.message);
    const many = Object.fromEntries(
      Array.from({ length: MAX_ANNOTATIONS + 1 }, (_, i) => [`n${i}`, '']),
    );
    const key = 'k'.repeat(MAX_NAME_BYTES);

    // Right at the bounds, counted in bytes: é is two.
    expect(
      messages({
        [key]: 'é'.repeat((MAX_ANNOTATION_BYTES - MAX_NAME_BYTES) / 2),
      }),
    ).toEqual([]);
    expect(messages({ [`${key}k`]: '' })).toEqual([
      `annotation key "${'k'.repeat(64)}..." must be at most ${MAX_NAME_BYTES} bytes`,
    ]);
    expect(messages({ a: 'x'.repeat(MAX_ANNOTATION_BYTES) })).toEqual([
      `annotations must take at most ${MAX_ANNOTATION_BYTES} bytes in all, not ${MAX_ANNOTATION_BYTES + 1}`,
    ]);
    expect(messages(many)).toEqual([
      `the source can have at most ${MAX_ANNOTATIONS} annotations, not ${MAX_ANNOTATIONS + 1}`,
    ]);
    // Quoted as the server quotes them (Go's %q).
    expect(messages({ '\t': 'tab', 'a\u0007b': 'bell' })).toEqual([
      'annotation key "\\t" must not be blank',
      'annotation key "a\\ab" must not contain control characters',
    ]);
    expect(paths(annotated(null))).toEqual([]);
  });

  test('issues are sorted by path', () => {
    const doc = { ...withMetadata({ id: '' }), $schema: '', revision: 0 };
    const sorted = [...paths(doc)].sort();

    expect(paths(doc)).toEqual(sorted);
  });

  test('network names must be unique and whitespace free', () => {
    const doc = {
      ...createDocument(),
      networks: [
        { id: 'n1', name: 'EXP' },
        { id: 'n2', name: 'EXP' },
        // minimega VLAN names are case sensitive.
        { id: 'n3', name: 'exp' },
        { id: 'n4', name: 'has space' },
      ],
    };

    expect(paths(doc)).toEqual(
      expect.arrayContaining(['networks[1].name', 'networks[3].name']),
    );
    expect(paths(doc)).not.toContain('networks[2].name');
  });

  test('vlan aliases are bounded', () => {
    const doc = {
      ...createDocument(),
      networks: [
        { id: 'n1', name: 'A', alias: 0 },
        { id: 'n2', name: 'B', alias: MAX_VLAN_ALIAS + 1 },
      ],
    };

    expect(paths(doc)).toEqual(
      expect.arrayContaining(['networks[0].alias', 'networks[1].alias']),
    );
  });

  test('an edge must join a device to a switch', () => {
    const { doc, alpha, bravo, network } = sampleDocument();
    const broken = {
      ...doc,
      edges: [
        {
          id: 'e-bad',
          sourceNodeId: alpha.id,
          targetNodeId: bravo.id,
          networkId: network.id,
        },
      ],
    };

    expect(
      errorsOf(broken)
        .map((issue) => issue.message)
        .join(' '),
    ).toMatch(/switch/i);
  });

  test('an edge network must match the switch network', () => {
    const { doc, edge } = sampleDocument();
    const broken = {
      ...doc,
      networks: [...doc.networks, { id: 'other', name: 'OTHER' }],
      edges: [{ ...edge, networkId: 'other' }],
    };

    expect(paths(broken)).toEqual(
      expect.arrayContaining(['edges[0].networkId']),
    );
  });

  test('a handle may only be used by one edge', () => {
    const { doc, edge } = sampleDocument();
    const broken = {
      ...doc,
      edges: [edge, { ...edge, id: 'e-dup' }],
    };

    expect(errorsOf(broken).length).toBeGreaterThan(0);
  });

  // A spec is free-form, and a file or a legacy diagram edited by hand may
  // hold anything in it. The check reports what it finds. It never throws.
  test('interfaces of a spec that are no list of objects are checked without a throw', () => {
    const { doc, alpha } = sampleDocument();
    const withInterfaces = (interfaces, handles = alpha.device.interfaces) => ({
      ...doc,
      edges: handles.length ? doc.edges : [],
      nodes: doc.nodes.map((node) =>
        node.id === alpha.id
          ? {
              ...node,
              device: {
                ...node.device,
                interfaces: handles,
                spec: { ...node.device.spec, network: { interfaces } },
              },
            }
          : node,
      ),
    });

    for (const interfaces of ['x', 7, true, { eth0: {} }, [null], ['x', 5]]) {
      // A device without connection points has nothing to match.
      expect(errorsOf(withInterfaces(interfaces, []))).toEqual([]);
      expect(() => parseDocument(withInterfaces(interfaces, []))).not.toThrow();
      // One with a connection point is told that the list lacks its entry.
      expect(
        errorsOf(withInterfaces(interfaces)).map((issue) => issue.message),
      ).toEqual([
        'interface "eth0" has no matching entry in the node\'s Interfaces',
      ]);
    }
  });

  test('unknown icon keys are rejected', () => {
    const { doc, alpha } = sampleDocument();
    const broken = {
      ...doc,
      nodes: doc.nodes.map((node) =>
        node.id === alpha.id
          ? { ...node, device: { ...node.device, iconKey: 'nope' } }
          : node,
      ),
    };

    expect(
      errorsOf(broken)
        .map((issue) => issue.message)
        .join(' '),
    ).toMatch(/icon/i);
  });

  // Named as the Inspector names the fields: its node's Interfaces list and
  // Node hostname, not the phenix spec they are part of.
  test('a device whose node disagrees with it names the Inspector fields', () => {
    const { doc, alpha } = sampleDocument();
    const broken = {
      ...doc,
      nodes: doc.nodes.map((node) =>
        node.id === alpha.id
          ? {
              ...node,
              device: {
                ...node.device,
                spec: {
                  ...node.device.spec,
                  general: { hostname: 'other' },
                  network: { interfaces: [] },
                },
              },
            }
          : node,
      ),
    };

    expect(
      errorsOf(broken).map(({ path, message }) => ({ path, message })),
    ).toEqual([
      {
        path: 'nodes[1].device.interfaces[0].name',
        message:
          'interface "eth0" has no matching entry in the node\'s Interfaces',
      },
      {
        path: 'nodes[1].device.spec.general.hostname',
        message: 'Node hostname must match the device Hostname',
      },
    ]);
  });

  test('editing warnings do not block saving', () => {
    const { doc, bravo } = sampleDocument();
    const issues = validateDocument(doc);
    const warnings = issues.filter((issue) => issue.level === 'warning');

    expect(bravo).toBeTruthy();
    expect(warnings.length).toBeGreaterThan(0);
    expect(errorsOf(doc)).toEqual([]);
  });
});

describe('interface VLANs and drive images', () => {
  // The sample with bravo's spec changed by `edit`.
  function withBravo(edit) {
    const sample = sampleDocument();
    const doc = {
      ...sample.doc,
      nodes: sample.doc.nodes.map((node) => {
        if (node.id !== sample.bravo.id) {
          return node;
        }

        const copy = JSON.parse(JSON.stringify(node));

        edit(copy.device.spec, copy);

        return copy;
      }),
    };

    return { ...sample, doc };
  }

  function warningsOf(doc, options) {
    return validateDocument(doc, options).filter(
      (issue) =>
        issue.level === 'warning' && /\.(vlan|image)$/.test(issue.path),
    );
  }

  test('a VLAN that names no network of the diagram is a warning', () => {
    const { doc, bravo } = withBravo((spec) => {
      spec.network.interfaces[0].vlan = 'GHOST';
    });

    expect(warningsOf(doc)).toEqual([
      {
        code: 'interface.vlan.unknown',
        path: 'nodes[2].device.spec.network.interfaces[0].vlan',
        message:
          'interface "eth0" of "bravo" uses VLAN "GHOST", which is not a network in this diagram',
        level: 'warning',
        severity: 'warning',
        field: 'spec.network.interfaces.0.vlan',
        nodeId: bravo.id,
      },
    ]);
    expect(errorsOf(doc)).toEqual([]);
  });

  // Typing a VLAN in another case connects the interface to that network
  // (connectByVLAN), so the field has no warning.
  test('a VLAN naming a network in another case, or none, is no warning', () => {
    const named = withBravo((spec) => {
      spec.network.interfaces[0].vlan = 'exp';
    });
    const blank = withBravo((spec) => {
      spec.network.interfaces[0].vlan = '  ';
    });

    expect(warningsOf(named.doc)).toEqual([]);
    expect(warningsOf(blank.doc)).toEqual([]);
  });

  // What the diagram says about each interface of bravo's spec.
  function unconnected(doc) {
    return validateDocument(doc)
      .filter((issue) =>
        /^nodes\[2\]\.device\.spec\.network\.interfaces\[\d+\]$/.test(
          issue.path,
        ),
      )
      .map(({ level, message, blocksPublish }) => ({
        level,
        message,
        blocksPublish,
      }));
  }

  // Publishing puts an unconnected interface on the network its VLAN names,
  // and refuses one with none. The draft stays valid either way.
  test('an unconnected interface says what publishing does with its VLAN', () => {
    const of = 'interface "eth0" of "bravo"';
    const withVLAN = (vlan, external) =>
      withBravo((spec) => {
        spec.network.interfaces[0].vlan = vlan;

        if (external) {
          spec.external = true;
        }
      }).doc;

    expect(unconnected(sampleDocument().doc)).toEqual([
      {
        level: 'warning',
        message: `${of} is not connected to a network and has no VLAN. Thus you cannot publish the interface. Connect it, or type a VLAN for it`,
        blocksPublish: true,
      },
    ]);
    expect(errorsOf(sampleDocument().doc)).toEqual([]);
    expect(unconnected(withVLAN('EXP'))).toEqual([
      {
        level: 'warning',
        message: `${of} is not connected on the canvas, but its VLAN "EXP" puts it on network EXP when you publish the diagram`,
        blocksPublish: undefined,
      },
    ]);
    // Publishing keeps the VLAN as it is, and phenix matches VLAN names
    // exactly, so "exp" is a VLAN of its own, not network EXP.
    expect(unconnected(withVLAN('exp'))).toEqual([
      {
        level: 'warning',
        message: `${of} is not connected on the canvas, and its VLAN "exp" differs from network EXP only in case. phenix treats them as different VLANs. Thus publishing does not put the interface on network EXP`,
        blocksPublish: undefined,
      },
    ]);

    for (const doc of [withVLAN('GHOST'), withVLAN('', true)]) {
      expect(unconnected(doc)).toEqual([
        {
          level: 'warning',
          message: `${of} is not connected to a network`,
          blocksPublish: undefined,
        },
      ]);
    }
  });

  // The server checks every interface of the spec, so the diagram does too,
  // including those with no connection point, named by position if needed.
  test('every interface of the spec is checked, with a connection point or not', () => {
    // Each says why it has no connection point, and what fixes it.
    const none = (label, message) => ({
      level: 'warning',
      message: `interface ${label} of "bravo" ${message}`,
      blocksPublish: true,
    });
    const handleless =
      'has no VLAN and no connection point on the canvas. Thus you cannot publish the interface. Type a VLAN for it';
    const { doc } = withBravo((spec) => {
      spec.network.interfaces[0].vlan = 'EXP';
      spec.network.interfaces.push(
        { name: '', type: 'ethernet', proto: 'dhcp', vlan: '' },
        { name: 'eth0', type: 'ethernet', proto: 'dhcp' },
        { name: 'eth2', type: 'ethernet', proto: 'dhcp', vlan: null },
        { name: 'eth3', type: 'ethernet', proto: 'dhcp', vlan: 'exp' },
      );
    });

    expect(unconnected(doc)).toEqual([
      {
        level: 'warning',
        message:
          'interface "eth0" (#1) of "bravo" is not connected on the canvas, but its VLAN "EXP" puts it on network EXP when you publish the diagram',
        blocksPublish: undefined,
      },
      none(
        '#2',
        'has no name and no VLAN. Thus you cannot publish the interface. Name it. Then connect it, or type a VLAN for it',
      ),
      none(
        '"eth0" (#3)',
        'has no VLAN, and another interface has the same name. Thus you cannot connect or publish the interface. Rename it. Then connect it, or type a VLAN for it',
      ),
      none('"eth2"', handleless),
      {
        level: 'warning',
        message:
          'interface "eth3" of "bravo" is not connected on the canvas, and its VLAN "exp" differs from network EXP only in case. phenix treats them as different VLANs. Thus publishing does not put the interface on network EXP',
        blocksPublish: undefined,
      },
    ]);
    expect(errorsOf(doc)).toEqual([]);

    // A device with no connection points at all is checked too.
    const bare = withBravo((_, node) => {
      node.device.interfaces = [];
    });

    expect(unconnected(bare.doc)).toEqual([none('"eth0"', handleless)]);

    // An external device is not started, so its interfaces need no VLAN.
    const external = withBravo((spec, node) => {
      spec.external = true;
      spec.network.interfaces.push({ name: '' });
      node.device.interfaces = [];
    });

    expect(unconnected(external.doc)).toEqual(
      ['"eth0"', '#2'].map((label) => ({
        level: 'warning',
        message: `interface ${label} of "bravo" is not connected to a network`,
        blocksPublish: undefined,
      })),
    );
  });

  test('drive images are checked only while the disk images are known', () => {
    const { doc, alpha, bravo } = withBravo((spec) => {
      spec.hardware.drives.push(
        { image: '/phenix/images/ubuntu.qc2' },
        { image: 'missing.qc2' },
        { image: '' },
      );
    });

    expect(warningsOf(doc)).toEqual([]);
    expect(warningsOf(doc, { disks: null })).toEqual([]);
    // Matched by file name, as the server lists them.
    expect(warningsOf(doc, { disks: ['ubuntu.qc2'] })).toEqual([
      {
        code: 'device.image.unknown',
        path: 'nodes[2].device.spec.hardware.drives[2].image',
        message:
          'drive image "missing.qc2" of "bravo" is not among the server\'s disk images',
        level: 'warning',
        severity: 'warning',
        field: 'spec.hardware.drives.2.image',
        nodeId: bravo.id,
      },
    ]);
    expect(
      warningsOf(doc, { disks: ['other.qc2'] }).map((issue) => issue.nodeId),
    ).toEqual([alpha.id, bravo.id, bravo.id, bravo.id]);
    expect(errorsOf(doc)).toEqual([]);
  });

  test('an external node and an included device are not checked', () => {
    const external = withBravo((spec) => {
      spec.external = true;
    });
    const included = withBravo((spec, node) => {
      spec.network.interfaces[0].vlan = 'GHOST';
      node.device.includedFrom = 'shared';
    });
    const bravoOnly = (issue) => issue.path.startsWith('nodes[2]');

    expect(warningsOf(external.doc, { disks: [] }).filter(bravoOnly)).toEqual(
      [],
    );
    expect(
      warningsOf(
        {
          ...included.doc,
          source: { kind: 'manual', includeTopologies: ['shared'] },
        },
        { disks: [] },
      ).filter(bravoOnly),
    ).toEqual([]);
  });

  test('issues carry the id of the node, connection or network they are about', () => {
    const { doc, alpha, edge } = sampleDocument();
    const broken = {
      ...doc,
      networks: [...doc.networks, { id: 'n2', name: 'EXP' }],
      nodes: doc.nodes.map((node) =>
        node.id === alpha.id
          ? { ...node, device: { ...node.device, iconKey: 'nope' } }
          : node,
      ),
      edges: [{ ...edge, networkId: 'n2' }],
    };
    const issues = validateDocument(broken);
    const about = (path) => issues.find((issue) => issue.path === path);

    expect(about('nodes[1].device.iconKey')).toMatchObject({
      nodeId: alpha.id,
    });
    expect(about('edges[0].networkId')).toMatchObject({ edgeId: edge.id });
    expect(about('networks[1].name')).toMatchObject({ networkId: 'n2' });
    expect(about('networks[1]')).toMatchObject({ networkId: 'n2' });
    // Issues about the document itself carry none.
    expect(
      validateDocument({
        ...broken,
        metadata: { ...broken.metadata, id: '' },
      }).find((issue) => issue.path === 'metadata.id'),
    ).toEqual({
      code: 'metadata.id.required',
      path: 'metadata.id',
      message: 'a document needs an ID',
      level: 'error',
      severity: 'error',
    });
  });

  // Publishing refuses the hostnames phenix refuses (hostname.go), but a
  // draft keeps them, and those phenix only warns about.
  test('a hostname phenix refuses blocks publishing, and one it warns about does not', () => {
    const renamed = (hostname, { osType = 'linux', external = false } = {}) =>
      withBravo((spec, node) => {
        node.device.hostname = hostname;
        spec.general = { ...spec.general, hostname };
        spec.hardware = { ...spec.hardware, os_type: osType };

        if (external) {
          spec.external = true;
        }
      }).doc;
    const findings = (doc) =>
      validateDocument(doc)
        .filter((issue) => issue.path === 'nodes[2].device.hostname')
        .map(({ level, message, blocksPublish }) => ({
          level,
          blocksPublish,
          message,
        }));
    const refused = (hostname, why, options) => {
      const doc = renamed(hostname, options);

      expect(errorsOf(doc)).toEqual([]);
      expect(findings(doc)).toEqual([
        {
          level: 'warning',
          blocksPublish: true,
          message: expect.stringMatching(
            new RegExp(
              `^hostname "${hostname}" ${why}, so the device cannot be published: `,
            ),
          ),
        },
      ]);
    };

    refused('all', 'is reserved');
    refused('42', 'is all digits');
    refused('b', 'is 1 character long');
    refused('phenix', 'cannot be used for a Windows node', {
      osType: 'windows',
    });

    expect(findings(renamed('All'))).toEqual([
      {
        level: 'warning',
        blocksPublish: undefined,
        message: expect.stringMatching(
          /^hostname "All" differs from the reserved name "all" only by case: /,
        ),
      },
    ]);
    expect(findings(renamed('Phenix'))).toEqual([
      {
        level: 'warning',
        blocksPublish: undefined,
        message: expect.stringMatching(/^hostname "Phenix" matches "phenix", /),
      },
    ]);

    // phenix checks no external node.
    expect(findings(renamed('all', { external: true }))).toEqual([]);
    expect(findings(sampleDocument().doc)).toEqual([]);
  });

  test('field warnings are keyed by the form path of the field', () => {
    const { doc } = sampleDocument();
    const spec = {
      hardware: { drives: [{ image: 'ubuntu.qc2' }, { image: 'gone.qc2' }] },
      network: {
        interfaces: [
          { name: 'eth0', vlan: 'EXP' },
          { name: 'eth1', vlan: 'GHOST' },
        ],
      },
    };

    expect(deviceFieldWarnings(doc, spec)).toEqual({
      'spec.network.interfaces.1.vlan': [
        'This diagram has no network named "GHOST".',
      ],
    });
    expect(deviceFieldWarnings(doc, spec, { disks: ['ubuntu.qc2'] })).toEqual({
      'spec.network.interfaces.1.vlan': [
        'This diagram has no network named "GHOST".',
      ],
      'spec.hardware.drives.1.image': [
        'The server has no disk image named "gone.qc2".',
      ],
    });
    // A device with no drive cannot be published.
    expect(deviceFieldWarnings(doc, undefined, { disks: [] })).toEqual({
      'spec.hardware.drives': [
        'The device has no drive, which phenix needs to start it. Add a drive.',
      ],
    });

    // The device's hostname, at its field.
    expect(
      deviceFieldWarnings(
        doc,
        { hardware: { drives: [{ image: 'ubuntu.qc2' }] } },
        { hostname: '007' },
      ),
    ).toEqual({
      hostname: [
        expect.stringMatching(
          /^Hostname "007" is all digits, so the device cannot be published: .*\.$/,
        ),
      ],
    });
  });

  // What phenix's apps do not set up as drawn, at the field to change. A
  // label or an annotation is under its map, named by its key.
  test('device setup warnings are at the field to change', () => {
    const { doc } = sampleDocument();
    const spec = {
      type: 'Router',
      general: { snapshot: false },
      hardware: { os_type: 'vyatta', drives: [{ image: 'vyatta.qc2' }] },
      network: {
        interfaces: [
          { name: 'eth0', proto: 'ospf', address: '10.0.0.1' },
          { name: 'eth1', proto: 'dhcp' },
        ],
      },
      injections: [{ src: 'a', dst: '/b' }],
      labels: { 'ntp-server': 'eth1' },
      annotations: {
        'vrouter/enable-ssh': 'ETH9',
        'phenix/startup-autotunnel': ['22', 'x'],
        'phenix/default-apps': 'true',
      },
    };

    expect(deviceFieldWarnings(doc, spec)).toEqual({
      'spec.network.interfaces.0.proto': [
        expect.stringMatching(/^The device has no network\.ospf, /),
      ],
      'spec.general.snapshot': [
        'Without a snapshot, phenix neither injects nor deletes the files of this device.',
      ],
      'spec.labels': [
        'ntp-server: interface "eth1" has no IP address. Thus the experiment fails to start.',
      ],
      'spec.annotations': [
        'vrouter/enable-ssh: "ETH9" is neither an IP address nor an interface of this device. Thus phenix does not enable SSH.',
        'phenix/startup-autotunnel: entry 2 is not "port", "port:dport" or "port:host:dport". Thus phenix does not open that tunnel.',
        'phenix/default-apps: not true or false. Thus phenix runs its default apps. To disable them, type false without quotes.',
      ],
    });

    // phenix starts no external device, but its ntp app reads the label of
    // one. The label's interface below has an address.
    expect(
      Object.keys(deviceFieldWarnings(doc, { ...spec, external: true })),
    ).toEqual(['spec.labels']);
    expect(
      deviceFieldWarnings(doc, {
        ...spec,
        general: {},
        injections: [],
        annotations: {},
        labels: { 'ntp-server': 'ETH0' },
        network: { ...spec.network, ospf: { router_id: '0.0.0.1', areas: [] } },
      }),
    ).toEqual({});
  });

  // phenix reads the ntp-server label only of the first device with it, so
  // the Inspector checks the value only there, as the Issues list does.
  test('the ntp-server label of a later device is ignored, not checked', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const labeled = (node, value) => ({
      ...node.device.spec,
      labels: { 'ntp-server': value },
    });
    const withAlpha = {
      ...doc,
      nodes: doc.nodes.map((node) =>
        node.id === alpha.id
          ? {
              ...node,
              device: { ...node.device, spec: labeled(alpha, 'eth9') },
            }
          : node,
      ),
    };

    expect(
      deviceFieldWarnings(withAlpha, labeled(bravo, 'eth9'), {
        nodeId: bravo.id,
      }),
    ).toEqual({
      'spec.labels': [
        'ntp-server: phenix uses only the first device with this label, "alpha". Thus phenix ignores the label here.',
      ],
    });
    expect(
      deviceFieldWarnings(withAlpha, labeled(alpha, 'eth9'), {
        nodeId: alpha.id,
      }),
    ).toEqual({
      'spec.labels': [
        'ntp-server: "eth9" names no interface of this device. Thus the experiment fails to start.',
      ],
    });
    // Bravo is first when alpha has no label.
    expect(
      deviceFieldWarnings(doc, labeled(bravo, 'eth9'), { nodeId: bravo.id }),
    ).toEqual({
      'spec.labels': [
        'ntp-server: "eth9" names no interface of this device. Thus the experiment fails to start.',
      ],
    });
  });
});

describe('IP and MAC addresses that two interfaces use', () => {
  // The sample with alpha's and bravo's interfaces changed by `edits`, both
  // on network EXP (bravo's by its VLAN), where addresses clash.
  function withInterfaces(edits) {
    const sample = sampleDocument();
    const doc = {
      ...sample.doc,
      nodes: sample.doc.nodes.map((node) => {
        const edit = edits[node.device?.hostname];

        if (!edit) {
          return node;
        }

        const copy = JSON.parse(JSON.stringify(node));

        if (copy.device.hostname === 'bravo') {
          copy.device.spec.network.interfaces[0].vlan = 'EXP';
        }

        edit(copy.device.spec.network.interfaces, copy);

        return copy;
      }),
    };

    return { ...sample, doc };
  }

  function addressIssues(doc) {
    return validateDocument(doc).filter((issue) =>
      /\.(address|mac)$/.test(issue.path),
    );
  }

  // What the diagram says about the addresses once alpha's eth0 has the
  // fields of `first`, and bravo's those of `second`.
  function pair(first, second) {
    const { doc } = withInterfaces({
      alpha: ([eth0]) => Object.assign(eth0, first),
      bravo: ([eth0]) => Object.assign(eth0, second),
    });

    return addressIssues(doc).map((issue) => issue.message);
  }

  function fixed(address, more = {}) {
    return { proto: 'static', address, ...more };
  }

  test('both interfaces get a warning that blocks publishing, and the draft stays valid', () => {
    const { doc, alpha, bravo } = withInterfaces({
      alpha: ([eth0]) => Object.assign(eth0, fixed('10.0.0.5', { mask: 24 })),
      bravo: ([eth0]) =>
        Object.assign(
          eth0,
          fixed('10.0.0.5', { mask: 16, gateway: '10.0.0.1' }),
        ),
    });

    expect(addressIssues(doc)).toEqual([
      {
        code: 'interface.ip.shared',
        path: 'nodes[1].device.spec.network.interfaces[0].address',
        message:
          'IP address 10.0.0.5 of interface "eth0" of "alpha" on VLAN "EXP" is also the address of interface "eth0" of "bravo"',
        level: 'warning',
        severity: 'warning',
        blocksPublish: true,
        field: 'spec.network.interfaces.0.address',
        nodeId: alpha.id,
      },
      {
        code: 'interface.ip.shared',
        path: 'nodes[2].device.spec.network.interfaces[0].address',
        message:
          'IP address 10.0.0.5 of interface "eth0" of "bravo" on VLAN "EXP" is also the address of interface "eth0" of "alpha"',
        level: 'warning',
        severity: 'warning',
        blocksPublish: true,
        field: 'spec.network.interfaces.0.address',
        nodeId: bravo.id,
      },
    ]);
    expect(errorsOf(doc)).toEqual([]);
  });

  // As the server compares them (interfaceIP and interfaceMAC in
  // types/builder/topology.go). Each message quotes its own field.
  test('addresses are compared parsed, and MACs in any case and with any separators', () => {
    for (const [first, second, shown] of [
      ['10.0.0.5', '10.0.0.5/24', '10.0.0.5'],
      ['10.0.0.5', ' 10.0.0.5 ', '10.0.0.5'],
      ['2001:db8::1', '2001:DB8:0:0:0:0:0:1/64', '2001:DB8:0:0:0:0:0:1'],
      ['::ffff:10.0.0.5', '10.0.0.5', '10.0.0.5'],
      ['fe80::1%eth0', 'fe80::1', 'fe80::1'],
      ['::1.2.3.4', '::102:304', '::102:304'],
      ['10.0.0.5', '\t\v\f10.0.0.5\r\n', '10.0.0.5'],
    ]) {
      expect(pair(fixed(first), fixed(second))).toEqual([
        `IP address ${first} of interface "eth0" of "alpha" on VLAN "EXP" is also the address of interface "eth0" of "bravo"`,
        `IP address ${shown} of interface "eth0" of "bravo" on VLAN "EXP" is also the address of interface "eth0" of "alpha"`,
      ]);
    }

    for (const [second, shown] of [
      ['AA-BB-CC-DD-EE-FF', 'AA-BB-CC-DD-EE-FF'],
      ['aabb.ccdd.eeff', 'aabb.ccdd.eeff'],
      ['\taa:bb:cc:dd:ee:ff\n', 'aa:bb:cc:dd:ee:ff'],
    ]) {
      expect(pair({ mac: 'aa:bb:cc:dd:ee:ff' }, { mac: second })).toEqual([
        'MAC address aa:bb:cc:dd:ee:ff of interface "eth0" of "alpha" on VLAN "EXP" is also the address of interface "eth0" of "bravo"',
        `MAC address ${shown} of interface "eth0" of "bravo" on VLAN "EXP" is also the address of interface "eth0" of "alpha"`,
      ]);
    }
  });

  // minirouter, which the Router template makes, and Vyatta give a QinQ
  // interface its address (vrouter.go, vyatta.tmpl).
  test('the address of a QinQ interface is compared', () => {
    expect(pair(fixed('10.0.0.5', { qinq: true }), fixed('10.0.0.5'))).toEqual([
      'IP address 10.0.0.5 of interface "eth0" of "alpha" on VLAN "EXP" is also the address of interface "eth0" of "bravo"',
      'IP address 10.0.0.5 of interface "eth0" of "bravo" on VLAN "EXP" is also the address of interface "eth0" of "alpha"',
    ]);
  });

  // Addresses clash only on one network: the same bridge and VLAN
  // (checkInterfaceAddresses in types/builder/topology.go).
  describe('only on one network', () => {
    // Both devices disconnected, so each is on the VLAN it has.
    function unconnected(first, second) {
      const { doc } = withInterfaces({
        alpha: ([eth0]) => Object.assign(eth0, first),
        bravo: ([eth0]) => Object.assign(eth0, second),
      });

      const issues = addressIssues({ ...doc, edges: [] });

      return issues.map((issue) => issue.message);
    }

    const isolated = (vlan, more = {}) =>
      fixed('10.0.0.1', { mask: 24, vlan, ...more });

    test('two VLANs may use one address, and one VLAN may not', () => {
      expect(
        unconnected(isolated('ISOLATED-1'), isolated('ISOLATED-2')),
      ).toEqual([]);
      expect(
        unconnected(isolated('ISOLATED-1'), isolated(' ISOLATED-1 ')),
      ).toEqual([
        'IP address 10.0.0.1 of interface "eth0" of "alpha" on VLAN "ISOLATED-1" is also the address of interface "eth0" of "bravo"',
        'IP address 10.0.0.1 of interface "eth0" of "bravo" on VLAN "ISOLATED-1" is also the address of interface "eth0" of "alpha"',
      ]);
      // phenix matches VLAN names exactly.
      expect(
        unconnected(isolated('ISOLATED-1'), isolated('isolated-1')),
      ).toEqual([]);
    });

    test('a MAC address may be used on two VLANs, and not twice on one', () => {
      const mac = (vlan) => ({ vlan, mac: '00:00:00:00:00:01' });

      expect(unconnected(mac('ISOLATED-1'), mac('ISOLATED-2'))).toEqual([]);
      expect(unconnected(mac('ISOLATED-1'), mac('ISOLATED-1'))).toEqual([
        'MAC address 00:00:00:00:00:01 of interface "eth0" of "alpha" on VLAN "ISOLATED-1" is also the address of interface "eth0" of "bravo"',
        'MAC address 00:00:00:00:00:01 of interface "eth0" of "bravo" on VLAN "ISOLATED-1" is also the address of interface "eth0" of "alpha"',
      ]);
    });

    // A VLAN of one name on two bridges is two networks. A blank bridge and
    // phenix are both the experiment's default bridge.
    test('a VLAN of one name on two bridges may use one address', () => {
      expect(
        unconnected(
          isolated('EXP', { bridge: 'lab-a' }),
          isolated('EXP', { bridge: 'lab-b' }),
        ),
      ).toEqual([]);
      expect(
        unconnected(
          isolated('EXP', { bridge: 'lab-a' }),
          isolated('EXP', { bridge: 'lab-a' }),
        ),
      ).toEqual([
        'IP address 10.0.0.1 of interface "eth0" of "alpha" on VLAN "EXP" of bridge "lab-a" is also the address of interface "eth0" of "bravo"',
        'IP address 10.0.0.1 of interface "eth0" of "bravo" on VLAN "EXP" of bridge "lab-a" is also the address of interface "eth0" of "alpha"',
      ]);
      expect(
        unconnected(
          isolated('EXP', { bridge: 'phenix' }),
          isolated('EXP', { bridge: '' }),
        ),
      ).toEqual([
        'IP address 10.0.0.1 of interface "eth0" of "alpha" on VLAN "EXP" is also the address of interface "eth0" of "bravo"',
        'IP address 10.0.0.1 of interface "eth0" of "bravo" on VLAN "EXP" is also the address of interface "eth0" of "alpha"',
      ]);
    });

    // Publishing refuses an interface without a VLAN, unless its device is
    // external. Until then it is compared with the others that have none.
    test('interfaces without a VLAN are compared with each other only', () => {
      expect(unconnected(isolated(''), isolated('  '))).toEqual([
        'IP address 10.0.0.1 of interface "eth0" of "alpha" without a VLAN is also the address of interface "eth0" of "bravo"',
        'IP address 10.0.0.1 of interface "eth0" of "bravo" without a VLAN is also the address of interface "eth0" of "alpha"',
      ]);
      expect(unconnected(isolated(''), isolated('EXP'))).toEqual([]);
    });

    // A connected interface is published on its network, whatever VLAN its
    // spec still names.
    test('a connected interface is on its network', () => {
      expect(pair(isolated('ISOLATED-2'), isolated('ISOLATED-2'))).toEqual([]);
      expect(pair(isolated('ISOLATED-2'), isolated('EXP'))).toEqual([
        'IP address 10.0.0.1 of interface "eth0" of "alpha" on VLAN "EXP" is also the address of interface "eth0" of "bravo"',
        'IP address 10.0.0.1 of interface "eth0" of "bravo" on VLAN "EXP" is also the address of interface "eth0" of "alpha"',
      ]);
    });

    // Networks ISOLATED-1 and ISOLATED-2 with a switch each, and alpha and
    // bravo, whose eth0 has `fields` and is on the network `networks` names.
    function connectedTo(fields, networks) {
      let doc = createDocument({ id: testId(), name: 'Isolated' });
      const switches = new Map();

      for (const [index, name] of ['ISOLATED-1', 'ISOLATED-2'].entries()) {
        const added = addNetwork(doc, { name });
        const hub = addNode(added.doc, {
          kind: 'switch',
          networkId: added.network.id,
          position: { x: 200, y: 200 * index },
        });

        doc = hub.doc;
        switches.set(name, hub.node);
      }

      for (const [index, hostname] of ['alpha', 'bravo'].entries()) {
        const added = addNode(doc, {
          kind: 'device',
          hostname,
          position: { x: 0, y: 200 * index },
          interfaces: [{ name: 'eth0' }],
        });

        doc = connect(added.doc, {
          sourceNodeId: added.node.id,
          sourceHandleId: added.node.device.interfaces[0].id,
          targetNodeId: switches.get(networks[hostname]).id,
        }).doc;
      }

      return {
        ...doc,
        nodes: doc.nodes.map((node) => {
          if (!node.device) {
            return node;
          }

          const copy = JSON.parse(JSON.stringify(node));

          Object.assign(copy.device.spec.network.interfaces[0], fields);

          return copy;
        }),
      };
    }

    // Each device connected to a network of its own, both connected to one.
    const apart = { alpha: 'ISOLATED-1', bravo: 'ISOLATED-2' };
    const together = { alpha: 'ISOLATED-1', bravo: 'ISOLATED-1' };

    test('devices connected to two networks may use one address, and on one network may not', () => {
      const blockers = (doc) =>
        validateDocument(doc).filter((issue) => issue.blocksPublish);

      for (const fields of [
        fixed('10.0.0.1', { mask: 24 }),
        { mac: '00:00:00:00:00:01' },
      ]) {
        const doc = connectedTo(fields, apart);

        expect(doc.edges).toHaveLength(2);
        expect(blockers(doc)).toEqual([]);
        expect(addressIssues(doc)).toEqual([]);
        expect(blockers(connectedTo(fields, together))).toHaveLength(2);
      }

      const messages = (fields) =>
        addressIssues(connectedTo(fields, together)).map(
          (issue) => issue.message,
        );

      expect(messages(fixed('10.0.0.1', { mask: 24 }))).toEqual([
        'IP address 10.0.0.1 of interface "eth0" of "alpha" on VLAN "ISOLATED-1" is also the address of interface "eth0" of "bravo"',
        'IP address 10.0.0.1 of interface "eth0" of "bravo" on VLAN "ISOLATED-1" is also the address of interface "eth0" of "alpha"',
      ]);
      expect(messages({ mac: '00:00:00:00:00:01' })).toEqual([
        'MAC address 00:00:00:00:00:01 of interface "eth0" of "alpha" on VLAN "ISOLATED-1" is also the address of interface "eth0" of "bravo"',
        'MAC address 00:00:00:00:00:01 of interface "eth0" of "bravo" on VLAN "ISOLATED-1" is also the address of interface "eth0" of "alpha"',
      ]);
    });
  });

  // DHCP, manual and unparsable addresses are not compared, and only ASCII
  // white space is trimmed, as on the server (topology_test.go).
  test('blank, DHCP, manual and unreadable addresses, masks and gateways are not compared', () => {
    expect(pair(fixed('10.0.0.5'), fixed('10.0.0.6'))).toEqual([]);
    expect(pair(fixed(''), fixed(''))).toEqual([]);
    expect(pair(fixed('  '), fixed('  '))).toEqual([]);

    for (const proto of ['dhcp', 'manual', ' DHCP\t', '\tManual ']) {
      expect(
        pair({ proto, address: '10.0.0.5', mask: 24 }, fixed('10.0.0.5')),
      ).toEqual([]);
    }

    for (const address of [
      '\ufeff10.0.0.5',
      '10.0.0.5\u0085',
      '\u00a010.0.0.5',
    ]) {
      expect(pair(fixed(address), fixed('10.0.0.5'))).toEqual([]);
    }

    for (const mac of ['\ufeffaa:bb:cc:dd:ee:ff', 'aa:bb:cc:dd:ee:ff\u0085']) {
      expect(pair({ mac }, { mac: 'aa:bb:cc:dd:ee:ff' })).toEqual([]);
    }

    for (const address of [
      '10.0.0.05',
      '10.0.0.256',
      '10.0.0',
      '10.0.0.5%eth0',
      '2001:db8::1::2',
      '1:2:3:4:5:6:7:8::',
      'fe80::1%',
      'host',
    ]) {
      expect(pair(fixed(address), fixed(address))).toEqual([]);
    }

    expect(
      pair(
        fixed('10.0.0.5', { mask: 24, gateway: '10.0.0.1' }),
        fixed('10.0.0.6', { mask: 24, gateway: '10.0.0.1' }),
      ),
    ).toEqual([]);
    expect(pair({ mac: '' }, { mac: '' })).toEqual([]);
    expect(pair({ mac: 'aa:bb:cc' }, { mac: 'aa:bb:cc' })).toEqual([]);
  });

  test('interfaces of one device are compared, and past two users counted', () => {
    const { doc } = withInterfaces({
      alpha: ([eth0]) => Object.assign(eth0, fixed('10.0.0.5')),
      bravo: (interfaces) => {
        Object.assign(interfaces[0], fixed('10.0.0.5'), {
          mac: '00:00:00:00:00:01',
        });
        interfaces.push(
          { name: 'eth1', vlan: 'EXP', ...fixed('10.0.0.5') },
          { name: 'eth1', vlan: 'EXP', mac: '00-00-00-00-00-01' },
        );
      },
    });

    expect(addressIssues(doc).map((issue) => issue.message)).toEqual([
      'IP address 10.0.0.5 of interface "eth0" of "alpha" on VLAN "EXP" is also the address of interface "eth0" of "bravo" and 1 more interface',
      'IP address 10.0.0.5 of interface "eth0" of "bravo" on VLAN "EXP" is also the address of interface "eth0" of "alpha" and 1 more interface',
      'MAC address 00:00:00:00:00:01 of interface "eth0" of "bravo" on VLAN "EXP" is also the address of interface "eth1" (#3) of "bravo"',
      'IP address 10.0.0.5 of interface "eth1" (#2) of "bravo" on VLAN "EXP" is also the address of interface "eth0" of "alpha" and 1 more interface',
      'MAC address 00-00-00-00-00-01 of interface "eth1" (#3) of "bravo" on VLAN "EXP" is also the address of interface "eth0" of "bravo"',
    ]);
  });

  // phenix does not start an external device and its schema has no MAC, so
  // only its IP address counts.
  test('an external device’s IP address counts, and its MAC does not', () => {
    const external = (address) => (interfaces, node) => {
      Object.assign(interfaces[0], fixed(address), {
        mac: 'aa:bb:cc:dd:ee:ff',
      });
      node.device.spec = { ...node.device.spec, external: true, type: 'HIL' };
    };
    const own = ([eth0]) =>
      Object.assign(eth0, fixed('10.0.0.5'), { mac: 'AA:BB:CC:DD:EE:FF' });

    expect(
      addressIssues(
        withInterfaces({ alpha: own, bravo: external('10.0.0.6') }).doc,
      ),
    ).toEqual([]);
    expect(
      addressIssues(
        withInterfaces({ alpha: own, bravo: external('10.0.0.5') }).doc,
      ).map((issue) => issue.message),
    ).toEqual([
      'IP address 10.0.0.5 of interface "eth0" of "alpha" on VLAN "EXP" is also the address of interface "eth0" of "bravo"',
      'IP address 10.0.0.5 of interface "eth0" of "bravo" on VLAN "EXP" is also the address of interface "eth0" of "alpha"',
    ]);
  });

  // phenix merges an included topology's devices into the experiment, so
  // their addresses count, but they are that topology's to fix.
  test('an included device counts, but only this diagram’s devices are reported', () => {
    const included = (interfaces, node) => {
      Object.assign(interfaces[0], fixed('10.0.0.5'));
      node.device.includedFrom = 'shared';
    };
    const own = ([eth0]) => Object.assign(eth0, fixed('10.0.0.5'));
    const source = { kind: 'manual', includeTopologies: ['shared'] };
    const mixed = withInterfaces({ alpha: included, bravo: own });
    const both = withInterfaces({ alpha: included, bravo: included });

    expect(
      addressIssues({ ...mixed.doc, source }).map(({ path, message }) => ({
        path,
        message,
      })),
    ).toEqual([
      {
        path: 'nodes[2].device.spec.network.interfaces[0].address',
        message:
          'IP address 10.0.0.5 of interface "eth0" of "bravo" on VLAN "EXP" is also the address of interface "eth0" of "alpha"',
      },
    ]);
    expect(addressIssues({ ...both.doc, source })).toEqual([]);
  });

  // The Inspector's working copy stands for the device's spec in the
  // diagram, and is compared with the other devices'.
  test('field warnings compare the working copy with the other devices', () => {
    const { doc, alpha, bravo } = withInterfaces({
      alpha: ([eth0]) => Object.assign(eth0, fixed('10.0.0.5')),
      bravo: ([eth0]) => Object.assign(eth0, fixed('10.0.0.9')),
    });
    const specOf = (...interfaces) => ({
      hardware: { drives: [{ image: 'ubuntu.qc2' }] },
      network: {
        interfaces: interfaces.map((iface, index) => ({
          name: `eth${index}`,
          vlan: 'EXP',
          ...iface,
        })),
      },
    });

    expect(
      deviceFieldWarnings(
        doc,
        specOf(
          fixed('10.0.0.5'),
          { mac: 'aa:bb:cc:dd:ee:ff' },
          { mac: 'AABB.CCDD.EEFF' },
        ),
        { nodeId: bravo.id },
      ),
    ).toEqual({
      'spec.network.interfaces.0.address': [
        'This IP address on VLAN "EXP" is also the address of interface "eth0" of "alpha".',
      ],
      'spec.network.interfaces.1.mac': [
        'This MAC address on VLAN "EXP" is also the address of interface "eth2" of "bravo".',
      ],
      'spec.network.interfaces.2.mac': [
        'This MAC address on VLAN "EXP" is also the address of interface "eth1" of "bravo".',
      ],
    });
    // Its own spec in the diagram is not compared with it.
    expect(
      deviceFieldWarnings(doc, specOf(fixed('10.0.0.9')), {
        nodeId: bravo.id,
      }),
    ).toEqual({});
    expect(
      deviceFieldWarnings(doc, specOf(fixed('10.0.0.6')), {
        nodeId: alpha.id,
      }),
    ).toEqual({});
  });

  // Applying a typed VLAN connects the interface by it, so the address is
  // compared on the changed VLAN, or on the connection when unchanged.
  test('field warnings compare an address on the VLAN the working copy has', () => {
    const { doc, bravo } = withInterfaces({
      alpha: ([eth0]) => Object.assign(eth0, fixed('10.0.0.5')),
      bravo: ([eth0]) => Object.assign(eth0, fixed('10.0.0.9')),
    });
    const working = (vlan) => ({
      hardware: { drives: [{ image: 'ubuntu.qc2' }] },
      network: { interfaces: [{ name: 'eth0', vlan, ...fixed('10.0.0.5') }] },
    });

    expect(
      deviceFieldWarnings(doc, working('ISOLATED-1'), { nodeId: bravo.id }),
    ).toEqual({
      'spec.network.interfaces.0.vlan': [
        'This diagram has no network named "ISOLATED-1".',
      ],
    });
    expect(
      deviceFieldWarnings(doc, working('EXP'), { nodeId: bravo.id }),
    ).toEqual({
      'spec.network.interfaces.0.address': [
        'This IP address on VLAN "EXP" is also the address of interface "eth0" of "alpha".',
      ],
    });

    // An unchanged VLAN keeps the interface on the network it is connected
    // to, whatever its spec names.
    const stale = withInterfaces({
      alpha: ([eth0]) => Object.assign(eth0, { vlan: 'STALE' }),
      bravo: ([eth0]) => Object.assign(eth0, fixed('10.0.0.5')),
    });

    expect(
      deviceFieldWarnings(stale.doc, working('STALE'), {
        nodeId: stale.alpha.id,
      }),
    ).toEqual({
      'spec.network.interfaces.0.address': [
        'This IP address on VLAN "EXP" is also the address of interface "eth0" of "bravo".',
      ],
      'spec.network.interfaces.0.vlan': [
        'This diagram has no network named "STALE".',
      ],
    });
  });

  test('a MAC address that 500 devices share is a warning on each', () => {
    const { doc } = sampleDocument();
    const hex = (value) => value.toString(16).padStart(2, '0');
    const devices = Array.from({ length: 500 }, (_, index) => ({
      id: testId(),
      kind: 'device',
      label: `host-${index}`,
      position: { x: index * 10, y: 0 },
      device: {
        hostname: `host-${index}`,
        spec: {
          type: 'VirtualMachine',
          general: { hostname: `host-${index}` },
          network: {
            // Every address differs but the last interface's MAC, which
            // all 500 share: 500 warnings.
            interfaces: [0, 1, 2, 3].map((port) => ({
              name: `eth${port}`,
              vlan: 'EXP',
              proto: 'static',
              address: `10.${port}.${index >> 8}.${index & 255}`,
              mac:
                port === 3
                  ? '02:00:00:ff:ff:ff'
                  : `02:00:00:${hex(port)}:${hex(index >> 8)}:${hex(index & 255)}`,
            })),
          },
        },
        interfaces: [],
      },
    }));
    const large = { ...doc, nodes: [...doc.nodes, ...devices] };
    const issues = addressIssues(large);

    expect(issues).toHaveLength(500);
    expect(
      issues.find((issue) => issue.path.startsWith('nodes[3].')),
    ).toMatchObject({
      path: 'nodes[3].device.spec.network.interfaces[3].mac',
      message:
        'MAC address 02:00:00:ff:ff:ff of interface "eth3" of "host-0" on VLAN "EXP" is also the address of interface "eth3" of "host-1" and 498 more interfaces',
    });
  });
});

// The rules are in the corpus below, which the server runs too. These are
// the editor's side: limits, the Scenario dialog's name check, a round trip.
describe('scenarios', () => {
  function withScenarios(scenarios) {
    return { ...sampleDocument().doc, scenarios };
  }

  test('the limits are the schema bundle’s', () => {
    const scenarios = bundle.properties.scenarios;

    expect(scenarios.maxItems).toBe(MAX_SCENARIOS);
    expect(scenarios.items.maxLength).toBe(MAX_SCENARIO_NAME_BYTES);
    expect(scenarios.items.pattern).toBe('^[A-Za-z0-9_@.-]+$');
  });

  test('a name the dialog would add is checked as validation checks it', () => {
    expect(scenarioNameProblem('plant-ntp')).toBe('');
    expect(scenarioNameProblem('')).toBe('a scenario needs a name');
    expect(scenarioNameProblem('a'.repeat(MAX_SCENARIO_NAME_BYTES + 1))).toBe(
      `scenario name must be at most ${MAX_SCENARIO_NAME_BYTES} bytes`,
    );
    expect(scenarioNameProblem('plant ntp')).toBe(
      'scenario name "plant ntp" may use only letters, numbers, ' +
        'underscores, at signs, periods and hyphens',
    );
  });

  test('a document that lists scenarios reopens with them', () => {
    const doc = withScenarios(['plant-ntp', 'plant-attack']);

    expect(errorsOf(doc)).toEqual([]);
    expect(parseDocument(doc).scenarios).toEqual(['plant-ntp', 'plant-attack']);
  });
});

// The rules are in the corpus below, which the server runs too. This is the
// editor's side of them: its limits against the schema bundle.
describe('layers', () => {
  test('the limits are the schema bundle’s', () => {
    const { layer, layerId } = bundle.$defs;

    expect(bundle.properties.layers.maxItems).toBe(MAX_LAYERS);
    expect(layerId.minimum).toBe(0);
    expect(layerId.maximum).toBe(MAX_LAYER_ID);
    expect(layer.properties.label.maxLength).toBe(MAX_LAYER_LABEL_BYTES);
    expect(bundle.properties.layers.description).toContain(
      `placeholder layers 0 to ${PLACEHOLDER_LAYERS - 1}`,
    );
  });

  test('a layer the document does not define is no error', () => {
    const { doc, alpha } = sampleDocument();
    const at = doc.nodes.findIndex((node) => node.id === alpha.id);

    doc.layers = [{ id: 3, label: 'Operations' }];
    doc.nodes[at].device.layer = 42;

    expect(errorsOf(doc)).toEqual([]);
  });
});

// The rules are in the corpus below, which the server runs too. These are
// the editor's side: limits, exported functions and undecoded values.
describe('colors, styles, custom icons and templates', () => {
  const template = (changes = {}) => ({
    id: testId(),
    name: 'PLC',
    description: 'Programmable logic controller',
    device: {
      iconKey: 'server',
      spec: { general: { hostname: 'plc' } },
    },
    ...changes,
  });

  const messages = (issues) => issues.map((entry) => entry.message);

  // The schema bundle is generated from the server's lists and limits
  // (validate.go, template.go), so one changed there fails here.
  test('the styles and limits are the ones the schema bundle carries', () => {
    expect(['', ...LINE_STYLES]).toEqual(bundle.$defs.lineStyle.enum);
    expect(['', ...BORDER_STYLES]).toEqual(bundle.$defs.borderStyle.enum);
    expect(bundle.$defs.hexColor.pattern).toBe(
      `^(${HEX_COLOR.source.slice(1, -1)})?$`,
    );
    expect(MAX_TEMPLATES).toBe(bundle.properties.templates.maxItems);

    // The schema's maxLength counts characters, where the server counts
    // bytes, so each is only equal, not the same bound.
    const properties = bundle.$defs.template.properties;

    expect(MAX_TEMPLATE_NAME_BYTES).toBe(properties.name.maxLength);
    expect(MAX_TEMPLATE_DESCRIPTION_BYTES).toBe(
      properties.description.maxLength,
    );
    expect(bundle.$defs.templateDevice.description).toBe(
      `Fields that a template sets, at most ${MAX_TEMPLATE_DEVICE_BYTES} bytes as JSON.`,
    );
  });

  test('a color is none, or six hex digits after a number sign', () => {
    for (const color of ['#000000', '#2f6fbf', '#ABCDEF', '#aBc123']) {
      expect(HEX_COLOR.test(color), color).toBe(true);
    }

    for (const color of [
      '',
      '#abc',
      'red',
      '2f6fbf',
      '#2f6fbf80',
      '#2f6fbg',
      ' #2f6fbf',
      '#2f6fbf\n',
      'rgb(0, 0, 0)',
    ]) {
      expect(HEX_COLOR.test(color), color).toBe(false);
    }
  });

  // Decoding refuses these, as the server does. A document the editor made
  // itself is validated without being decoded.
  test('values of the wrong type are errors, not crashes', () => {
    const { doc, alpha, sw, network, edge } = sampleDocument();
    const group = addNode(doc, { kind: 'group', title: 'Rack' });
    const broken = JSON.parse(JSON.stringify(group.doc));
    const index = (id) => broken.nodes.findIndex((node) => node.id === id);

    Object.assign(broken.nodes[index(alpha.id)].device, {
      icon: 7,
      outlineColor: ['#2f6fbf'],
      fillColor: 0x2f6fbf,
    });
    Object.assign(broken.nodes[index(sw.id)].switch, { fillColor: true });
    Object.assign(broken.nodes[index(group.node.id)].group, {
      borderStyle: 1,
      iconKey: {},
      icon: [ICON_KEY],
    });
    broken.networks.find((entry) => entry.id === network.id).lineStyle = 2;
    broken.edges.find((entry) => entry.id === edge.id).lineStyle = ['solid'];
    broken.icons = 'icons';
    broken.templates = 'templates';
    broken.source = { kind: 'manual', unresolvedIncludes: 'plant' };

    expect(paths(broken)).toEqual(
      [
        `edges[0].lineStyle`,
        'icons',
        'networks[0].lineStyle',
        `nodes[${index(alpha.id)}].device.fillColor`,
        `nodes[${index(alpha.id)}].device.icon`,
        `nodes[${index(alpha.id)}].device.outlineColor`,
        `nodes[${index(sw.id)}].switch.fillColor`,
        `nodes[${index(group.node.id)}].group.borderStyle`,
        `nodes[${index(group.node.id)}].group.icon`,
        `nodes[${index(group.node.id)}].group.iconKey`,
        'source.unresolvedIncludes',
        'templates',
      ].sort((a, b) => a.localeCompare(b)),
    );
  });

  // A custom icon is a name, which a document need not carry: the server's
  // icon library resolves it. A property every object has is a name too.
  test('a custom icon is any icon name, carried or not', () => {
    const { doc, alpha } = sampleDocument();
    const using = (icon, icons) => {
      const next = JSON.parse(JSON.stringify(doc));

      next.nodes.find((node) => node.id === alpha.id).device.icon = icon;

      if (icons) {
        next.icons = icons;
      }

      return paths(next);
    };
    const path = expect.stringMatching(/^nodes\[\d+\]\.device\.icon$/);
    const icons = { plc: { data: ICON_DATA } };

    expect(using('plc', icons)).toEqual([]);
    expect(using('plc')).toEqual([]);
    expect(using('constructor', icons)).toEqual([]);
    expect(using('__proto__')).toEqual([]);
    expect(using(ICON_KEY, icons)).toEqual([path]);
    expect(using('plc icon')).toEqual([path]);
    expect(using('..')).toEqual([path]);
  });

  test('iconNameProblem says why a name is none, in the server’s words', () => {
    expect(iconNameProblem('plc')).toBe('');
    expect(iconNameProblem('n'.repeat(MAX_ICON_NAME_BYTES))).toBe('');
    expect(iconNameProblem('')).toBe(
      'icon name "" must be 1 to 64 letters, digits, "_", "@", "." or "-"',
    );
    expect(iconNameProblem('tab\there')).toBe(
      'icon name "tab\\there" must be 1 to 64 letters, digits, "_", "@", "." or "-"',
    );
    expect(iconNameProblem('n'.repeat(65))).toBe(
      `icon name "${'n'.repeat(64)}..." must be 1 to 64 letters, digits, "_", "@", "." or "-"`,
    );
    expect(iconNameProblem('.')).toBe('icon name "." must not be "." or ".."');
    expect(iconNameProblem(7)).toBe(
      'icon name "7" must be 1 to 64 letters, digits, "_", "@", "." or "-"',
    );
  });

  test('validateIcons reports at the path it is given', () => {
    expect(validateIcons(undefined)).toEqual([]);
    expect(validateIcons(null)).toEqual([]);
    expect(validateIcons({})).toEqual([]);
    expect(validateIcons({ plc: { data: ICON_DATA } })).toEqual([]);
    expect(
      validateIcons({ [ICON_KEY]: { data: ICON_DATA } }, 'library.icons'),
    ).toEqual([
      {
        code: 'icon.name.invalid',
        path: 'library.icons',
        message: `icon name "${ICON_KEY.slice(0, 64)}..." must be 1 to 64 letters, digits, "_", "@", "." or "-"`,
        level: 'error',
        severity: 'error',
      },
    ]);
    expect(validateIcons({ plc: 'x' })[0]).toMatchObject({
      path: 'icons',
      message: expect.stringMatching(
        /^icon "plc" data must be base64 of at most \d+ bytes$/,
      ),
    });
    expect(messages(validateIcons([ICON_DATA]))).toEqual([
      'custom icons must be an object of icons by name',
    ]);
  });

  test('icons are counted, and reported in the order of their keys', () => {
    const icons = (count) =>
      Object.fromEntries(
        Array.from({ length: count }, (_, i) => [
          `icon ${String(i).padStart(2, '0')}`,
          { data: base64Of(png(1, 1, [i, 0, 7, 255])) },
        ]),
      );

    // Each key is no icon name, so each is reported, in order.
    const issues = messages(validateIcons(icons(MAX_DOCUMENT_ICONS + 1)));

    expect(issues[0]).toBe(
      'a document can carry at most 50 custom icons, not 51',
    );
    expect(issues).toHaveLength(MAX_DOCUMENT_ICONS + 2);
    expect(issues.slice(1)).toEqual([...issues.slice(1)].sort());
    expect(messages(validateIcons(icons(MAX_DOCUMENT_ICONS)))).not.toContain(
      issues[0],
    );
  });

  // Data that was checked is not decoded again on every edit. Other data is
  // checked, and every key is.
  test('an icon found valid is checked again when its data changes', () => {
    expect(validateIcons({ plc: { data: ICON_DATA } })).toEqual([]);
    expect(validateIcons({ plc: { data: ICON_DATA } })).toEqual([]);
    expect(messages(validateIcons({ plc: { data: 'x' } }))).toEqual([
      expect.stringContaining('data must be base64'),
    ]);
    expect(messages(validateIcons({ 'a b': { data: ICON_DATA } }))).toEqual([
      expect.stringContaining('icon name "a b" must be'),
    ]);
    expect(validateIcons({ plc: { data: ICON_DATA } })).toEqual([]);
  });

  test('templateIssues reports under the path it is given', () => {
    expect(templateIssues(template(), 'templates[0]')).toEqual([]);

    // The id is checked where the template is kept. The custom icon is a
    // name the template need not carry.
    expect(
      templateIssues(
        template({
          id: 'server',
          device: { icon: 'plc', spec: { general: { hostname: 'x' } } },
        }),
        'templates[0]',
      ),
    ).toEqual([]);
    expect(
      templateIssues(
        template({
          device: { icon: ICON_KEY, spec: { general: { hostname: 'x' } } },
        }),
        'templates[0]',
      ),
    ).toEqual([
      expect.objectContaining({
        path: 'templates[0].device.icon',
        message: expect.stringContaining('must be 1 to 64 letters'),
      }),
    ]);

    expect(
      templateIssues(
        template({
          name: ' ',
          description: 'one\ntwo',
          device: {
            iconKey: 'toaster',
            outlineColor: 'red',
            fillColor: '#abc',
            spec: { general: { hostname: 'two words' } },
          },
        }),
        'library.templates[3]',
      ),
    ).toEqual(
      [
        [
          'template.name.required',
          'library.templates[3].name',
          'a template needs a name',
        ],
        [
          'template.description.control',
          'library.templates[3].description',
          'template description must not contain control characters',
        ],
        [
          'template.hostname.whitespace',
          'library.templates[3].device.spec.general.hostname',
          'template hostname "two words" must not contain whitespace',
        ],
        [
          'template.icon-key.unknown',
          'library.templates[3].device.iconKey',
          'unknown icon key "toaster"',
        ],
        [
          'template.color.invalid',
          'library.templates[3].device.outlineColor',
          'color "red" must be a hex color such as #2f6fbf',
        ],
        [
          'template.color.invalid',
          'library.templates[3].device.fillColor',
          'color "#abc" must be a hex color such as #2f6fbf',
        ],
      ].map(([code, path, message]) => ({
        code,
        path,
        message,
        level: 'error',
        severity: 'error',
      })),
    );

    for (const broken of [undefined, null, 'PLC', [], {}]) {
      expect(
        templateIssues(broken, 't').map((entry) => entry.path),
        JSON.stringify(broken),
      ).toEqual(['t.name', 't.device.spec']);
    }
  });

  // The server measures a template's device as it encodes it: unset fields
  // left out, "<" as six bytes, and other text by its UTF-8 bytes.
  test('a template device is measured as the server encodes it', () => {
    const device = (description, look = {}) => ({
      iconKey: 'server',
      ...look,
      spec: { general: { hostname: 'big', description } },
    });
    const size = (description, look) =>
      templateIssues(template({ device: device(description, look) }), 't');
    const base =
      '{"iconKey":"server","spec":{"general":{"description":"","hostname":"big"}}}';
    const room = MAX_TEMPLATE_DEVICE_BYTES - base.length;

    expect(size('x'.repeat(room))).toEqual([]);
    expect(messages(size('x'.repeat(room + 1)))).toEqual([
      'template device must take at most 16384 bytes as JSON, not 16385',
    ]);
    // Filled to the last byte with a character and, where its bytes do not
    // divide the room, with "x".
    const filled = (character, bytes) => {
      const times = Math.floor(room / bytes);

      return character.repeat(times) + 'x'.repeat(room - times * bytes);
    };

    for (const [character, bytes] of [
      ['<', 6],
      ['&', 6],
      ['é', 2],
      ['€', 3],
      ['\u2028', 6],
      ['"', 2],
    ]) {
      expect(size(filled(character, bytes)), character).toEqual([]);
      expect(
        messages(size(`${filled(character, bytes)}x`)),
        character,
      ).toHaveLength(1);
    }

    // Unset look fields take no room, as the server leaves them out.
    expect(
      size('x'.repeat(room), { icon: '', outlineColor: null, fillColor: '' }),
    ).toEqual([]);
    expect(
      messages(size('x'.repeat(room), { fillColor: '#2f6fbf' })),
    ).toHaveLength(1);
  });

  test('templates are counted, and their ids are unique among them', () => {
    const { doc, alpha } = sampleDocument();
    const withTemplates = (templates) => paths({ ...doc, templates });
    const many = (count) => Array.from({ length: count }, () => template());

    expect(withTemplates(many(MAX_TEMPLATES))).toEqual([]);
    expect(withTemplates(many(MAX_TEMPLATES + 1))).toEqual(['templates']);

    // A template may have the id of a node: they are looked up apart.
    expect(withTemplates([template({ id: alpha.id })])).toEqual([]);

    const first = template();

    expect(
      withTemplates([first, template({ id: first.id.toUpperCase() })]),
    ).toEqual(['templates[1].id']);
    expect(withTemplates([template({ id: undefined })])).toEqual([
      'templates[0].id',
    ]);
    expect(withTemplates([null])).toEqual([
      'templates[0].device.spec',
      'templates[0].id',
      'templates[0].name',
    ]);
  });

  // The warnings about devices are about the devices of the diagram: a
  // template's spec is not one until a device is made from it.
  test('a template raises no warning about its spec', () => {
    const { doc } = sampleDocument();
    const before = validateDocument(doc);
    const after = validateDocument({
      ...doc,
      templates: [
        template({
          device: {
            spec: {
              general: { hostname: 'alpha' },
              network: { interfaces: [{ name: 'eth0', vlan: 'NOWHERE' }] },
            },
          },
        }),
      ],
    });

    expect(after).toEqual(before);
  });
});

// The documents validate.go must agree on (TestValidationCorpus in
// types/builder), run through parseDocument as the editor opens a document.
describe('the validation corpus shared with the server', () => {
  const testdata = new URL(
    '../../../go/types/builder/testdata/',
    import.meta.url,
  );
  const read = (name) => JSON.parse(readFileSync(new URL(name, testdata)));
  const corpus = read('validation-corpus.json');

  // Puts value at path, or removes the key there when remove is set.
  function setIn(doc, path, value, remove = false) {
    const parent = path
      .slice(0, -1)
      .reduce((container, key) => container[key], doc);

    if (remove) {
      delete parent[path[path.length - 1]];
    } else {
      parent[path[path.length - 1]] = value;
    }
  }

  // Whether a decoding error refuses the key at path: an unknown key is
  // named in the object it is in (rejectUnknown in decode.js).
  function unknownKey(message, path) {
    const at = path.lastIndexOf('.');
    const object = at < 0 ? 'document' : path.slice(0, at);

    return message.includes(`${object}: unknown field "${path.slice(at + 1)}"`);
  }

  // The text the join parts of a set entry make: each is text, or text and
  // how many times to repeat it.
  function joinParts(parts) {
    return parts
      .map((part) =>
        typeof part === 'string' ? part : part[0].repeat(part[1]),
      )
      .join('');
  }

  // The issues as sorted lines of their code, path and message.
  function issueLines(issues = []) {
    return issues
      .map((issue) => `${issue.code} ${issue.path}: ${issue.message}`)
      .sort();
  }

  // TestValidationCorpus runs the same cases by name, so each name is one
  // case. The table is counted, so the count holds whatever -t or .only picks.
  const table = corpus.cases.map((entry) => [entry.name, entry]);

  test('names each case once', () => {
    expect(new Set(table.map(([name]) => name)).size).toBe(corpus.cases.length);
  });

  test('runs every case', () => {
    expect(table.map(([, entry]) => entry)).toEqual(corpus.cases);
  });

  test.each(table)('%s', (_, entry) => {
    const doc = read(corpus.document);

    (entry.set || []).forEach(({ path, value, join, delete: remove }) =>
      setIn(doc, path, join ? joinParts(join) : value, remove),
    );

    if (!entry.error) {
      expect(() => parseDocument(doc)).not.toThrow();

      return;
    }

    let refusal;

    try {
      parseDocument(doc);
    } catch (error) {
      refusal = error;
    }

    // Every issue, in any order, when the case pins that there are no
    // others.
    if (entry.issues) {
      expect(issueLines(refusal?.issues)).toEqual(issueLines(entry.issues));
    }

    // The code of the issue at the path, when the case pins it.
    if (entry.code) {
      expect(
        refusal?.issues
          ?.filter((issue) => issue.path === entry.error)
          .map((issue) => issue.code),
        refusal?.message || 'accepted',
      ).toContain(entry.code);
    }

    // The words the server refuses it in, when the case pins them.
    if (entry.message) {
      expect(
        refusal?.issues
          ?.filter((issue) => issue.path === entry.error)
          .map((issue) => issue.message),
        refusal?.message || 'accepted',
      ).toContain(entry.message);

      return;
    }

    // An issue at the path, or a decoding error naming the key.
    expect(
      refusal?.issues?.some((issue) => issue.path === entry.error) ||
        refusal?.message.includes(`"${entry.error}"`) ||
        unknownKey(refusal?.message || '', entry.error),
      refusal?.message || 'accepted',
    ).toBe(true);
  });

  // Every issue the corpus raises has a code of the registry the server
  // holds, and the severity it is reported at.
  test('every issue of the corpus carries a code of the registry', () => {
    corpus.cases.forEach((entry) => {
      const doc = read(corpus.document);

      (entry.set || []).forEach(({ path, value, join, delete: remove }) =>
        setIn(doc, path, join ? joinParts(join) : value, remove),
      );

      let issues;

      // A document of the wrong shape is the decoder's to refuse, before
      // it is validated.
      try {
        issues = validateDocument(doc);
      } catch {
        return;
      }

      issues.forEach((found) => {
        expect(CODES[found.code], `${entry.name}: ${found.path}`).toBeTruthy();
        expect(found.severity).toBe(found.level);
      });
    });
  });
});

// The design warnings the server gives too (TestWarningCorpus in
// types/builder): the warnings of the corpus's codes, in any order.
describe('the warning corpus shared with the server', () => {
  const testdata = new URL(
    '../../../go/types/builder/testdata/',
    import.meta.url,
  );
  const read = (name) => JSON.parse(readFileSync(new URL(name, testdata)));
  const corpus = read('warning-corpus.json');
  const table = corpus.cases.map((entry) => [entry.name, entry]);

  // Puts value at path, or removes the key there when remove is set.
  function setIn(doc, path, value, remove = false) {
    const parent = path
      .slice(0, -1)
      .reduce((container, key) => container[key], doc);

    if (remove) {
      delete parent[path[path.length - 1]];
    } else {
      parent[path[path.length - 1]] = value;
    }
  }

  // The warnings as sorted lines of their code, path, field and message.
  const lines = (warnings) =>
    warnings
      .map(
        (warning) =>
          `${warning.code} ${warning.path} [${warning.field ?? ''}]: ${warning.message}`,
      )
      .sort();

  const codes = [...corpus.codes, ...corpus.blockers];

  test('names each case once, and has a case of each code', () => {
    expect(new Set(table.map(([name]) => name)).size).toBe(corpus.cases.length);
    expect(
      new Set(
        corpus.cases.flatMap((entry) => entry.warnings.map((w) => w.code)),
      ),
    ).toEqual(new Set(codes));
    corpus.codes.forEach((code) =>
      expect(CODES[code]?.severity, code).toBe('warning'),
    );
    corpus.blockers.forEach((code) =>
      expect(CODES[code]?.severity, code).toBe('error'),
    );
  });

  // An error that only publishing refuses is a warning while editing that
  // blocks publishing.
  test.each(table)('%s', (_, entry) => {
    const doc = read(corpus.document);

    entry.set.forEach(({ path, value, delete: remove }) =>
      setIn(doc, path, value, remove),
    );

    expect(() => parseDocument(doc)).not.toThrow();

    const found = validateDocument(doc).filter((issue) =>
      codes.includes(issue.code),
    );

    expect(lines(found)).toEqual(lines(entry.warnings));
    found.forEach((warning) => {
      expect(warning.level).toBe('warning');
      expect(Boolean(warning.blocksPublish), warning.code).toBe(
        corpus.blockers.includes(warning.code),
      );
    });
  });
});

// Each quoted dotted name in validate.js and decode.js is a code of the
// registry (schema/codes.json) or a document path. Codes are never built.
describe('the codes the validator reports', () => {
  const sources = ['validate.js', 'decode.js'].map((name) =>
    readFileSync(new URL(`../../src/builder/${name}`, import.meta.url), 'utf8'),
  );
  const quoted = sources.flatMap((source) =>
    [...source.matchAll(/'([a-z]+(?:\.[a-z0-9-]+)+)'/g)].map(
      ([, text]) => text,
    ),
  );
  // The first word of each code: what the codes are about.
  const subjects = new Set(
    Object.keys(CODES).map((code) => code.split('.')[0]),
  );

  // The schemas a schema stands for: itself, the one it refers to, and
  // each of its alternatives.
  function resolved(schema) {
    if (!schema || typeof schema !== 'object') {
      return [];
    }

    if (schema.$ref) {
      return resolved(bundle.$defs[schema.$ref.replace('#/$defs/', '')]);
    }

    return [
      schema,
      ...[
        ...(schema.anyOf || []),
        ...(schema.oneOf || []),
        ...(schema.allOf || []),
      ].flatMap(resolved),
    ];
  }

  // Whether dotted names a property of the document schema: each word a
  // property, a key of a map, or an index of a list in the one before.
  function documentPath(dotted) {
    let schemas = [bundle];

    for (const word of dotted.split('.')) {
      schemas = schemas
        .flatMap(resolved)
        .flatMap((schema) => [
          ...(schema.properties?.[word] ? [schema.properties[word]] : []),
          ...(typeof schema.additionalProperties === 'object'
            ? [schema.additionalProperties]
            : []),
          ...(/^\d+$/.test(word) && schema.items ? [schema.items] : []),
        ]);

      if (schemas.length === 0) {
        return false;
      }
    }

    return true;
  }

  test('are codes of the registry, or paths of the document', () => {
    expect(quoted.filter((text) => CODES[text]).length).toBeGreaterThan(100);
    expect(
      quoted.filter((text) => !CODES[text] && !documentPath(text)),
    ).toEqual([]);
    // A code is not taken for a path.
    expect(documentPath('metadata.id.required')).toBe(false);
    expect(documentPath('metadata.id')).toBe(true);
  });

  test('are not put together in template strings', () => {
    const built = sources
      .flatMap((source) =>
        [
          ...source.matchAll(/`([a-z]+(?:\.(?:[a-z0-9-]|\$\{[^}]*\})+)+)`/g),
        ].map(([, text]) => text),
      )
      .filter(
        (text) =>
          subjects.has(text.split('.')[0]) &&
          !documentPath(text.replace(/\$\{[^}]*\}/g, '0')),
      );

    expect(built).toEqual([]);
  });

  test('match the registry form and severities', () => {
    Object.entries(CODES).forEach(([code, { severity, description }]) => {
      expect(code).toMatch(/^[a-z]+(\.[a-z0-9-]+)+$/);
      expect(['error', 'warning']).toContain(severity);
      expect(description).toMatch(/\.$/);
    });
  });
});
