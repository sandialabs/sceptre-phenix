import { readFileSync } from 'node:fs';

import { describe, expect, test } from 'vitest';

import { parseDocument } from '@/builder/decode.js';
import { MAX_DOCUMENT_ICONS, MAX_ICON_NAME_BYTES } from '@/builder/icons.js';
import {
  addNode,
  BORDER_STYLES,
  createDocument,
  HEX_COLOR,
  LINE_STYLES,
} from '@/builder/model.js';
import bundle from '@/builder/schema/builder-v1.schema.json';
import {
  deviceFieldWarnings,
  isTime,
  MAX_ANNOTATION_BYTES,
  MAX_ANNOTATIONS,
  MAX_NAME_BYTES,
  MAX_TEMPLATE_DESCRIPTION_BYTES,
  MAX_TEMPLATE_DEVICE_BYTES,
  MAX_TEMPLATE_NAME_BYTES,
  MAX_TEMPLATES,
  MAX_USER_BYTES,
  MAX_VLAN_ALIAS,
  SCENARIO_API_VERSION,
  templateIssues,
  validateDocument,
  validateIcons,
} from '@/builder/validate.js';

import { sampleDocument, testId } from './fixtures.js';
import { base64Of, ICON_DATA, ICON_KEY, png } from './png.js';

function errorsFor(doc) {
  return validateDocument(doc).filter((issue) => issue.level === 'error');
}

function paths(doc) {
  return errorsFor(doc).map((issue) => issue.path);
}

describe('document validation', () => {
  test('a well formed document has no errors', () => {
    const { doc } = sampleDocument();

    expect(errorsFor(doc)).toEqual([]);
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
    expect(MAX_NAME_BYTES).toBe(bundle.properties.name.maxLength);
    expect(MAX_USER_BYTES).toBe(bundle.properties.author.maxLength);
    expect(MAX_USER_BYTES).toBe(bundle.properties.updatedBy.maxLength);
  });

  test('the name is bounded the way the server bounds a draft title', () => {
    const named = (name) => ({ ...createDocument(), name });

    expect(paths(named('n'.repeat(MAX_NAME_BYTES)))).toEqual([]);
    // Bytes, not characters: 257 two-byte characters are 514 bytes.
    expect(paths(named('é'.repeat(MAX_NAME_BYTES / 2 + 1)))).toEqual(['name']);
    expect(paths(named('my\ttopology'))).toEqual(['name']);
  });

  // validateUser and validateTime in validate.go, message for message.
  test('the author and the last editor are bounded text', () => {
    const errors = (field, value) =>
      errorsFor({ ...createDocument(), [field]: value }).map(
        ({ path, message }) => [path, message],
      );

    for (const field of ['author', 'updatedBy']) {
      expect(errors(field, 'alice')).toEqual([]);
      expect(errors(field, 'a'.repeat(MAX_USER_BYTES))).toEqual([]);
      // None, as Go decodes them.
      expect(errors(field, '')).toEqual([]);
      expect(errors(field, null)).toEqual([]);
      expect(errors(field, undefined)).toEqual([]);
      expect(errors(field, 'a'.repeat(MAX_USER_BYTES + 1))).toEqual([
        [field, `${field} must be at most 256 bytes`],
      ]);
      // Bytes, not characters: 86 three-byte characters are 258 bytes.
      expect(errors(field, '€'.repeat(86))).toEqual([
        [field, `${field} must be at most 256 bytes`],
      ]);
      expect(errors(field, 'alice\tsmith')).toEqual([
        [field, `${field} must not contain control characters`],
      ]);
      expect(errors(field, 'bob\u007f')).toEqual([
        [field, `${field} must not contain control characters`],
      ]);
      // Too long is said once, not with the control character too.
      expect(errors(field, `${'a'.repeat(MAX_USER_BYTES)}\n`)).toEqual([
        [field, `${field} must be at most 256 bytes`],
      ]);
      expect(errors(field, 7)).toEqual([[field, `${field} must be a string`]]);
      expect(errors(field, { user: 'bob' })).toEqual([
        [field, `${field} must be a string`],
      ]);
    }
  });

  test('the creation time and the last edit time have one form', () => {
    const errors = (field, value) =>
      errorsFor({ ...createDocument(), [field]: value }).map(
        ({ path, message }) => [path, message],
      );

    for (const field of ['createdAt', 'updatedAt']) {
      const refused = [
        [field, `${field} must be a UTC time in the form YYYY-MM-DDTHH:MM:SSZ`],
      ];

      expect(errors(field, '2026-10-01T15:04:05Z')).toEqual([]);
      expect(errors(field, '')).toEqual([]);
      expect(errors(field, null)).toEqual([]);
      expect(errors(field, '2026-10-01T15:04:05+00:00')).toEqual(refused);
      expect(errors(field, '2026-10-01T15:04:05.000Z')).toEqual(refused);
      expect(errors(field, '2026-02-30T00:00:00Z')).toEqual(refused);
      expect(errors(field, 1790866800)).toEqual([
        [field, `${field} must be a string`],
      ]);
      expect(errors(field, true)).toEqual([
        [field, `${field} must be a string`],
      ]);
    }

    // There is no rule between the four: an editor without a time, and a
    // creation after the last edit, are valid.
    expect(
      errorsFor({
        ...createDocument(),
        updatedBy: 'bob',
        createdAt: '2026-10-02T00:00:00Z',
        updatedAt: '2024-02-29T23:59:59Z',
      }),
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
      errorsFor(annotated(annotations)).map((issue) => issue.message);
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
      `at most ${MAX_ANNOTATIONS} annotations are allowed, not ${MAX_ANNOTATIONS + 1}`,
    ]);
    // Quoted as the server quotes them (Go's %q).
    expect(messages({ '\t': 'tab', 'a\u0007b': 'bell' })).toEqual([
      'annotation key "\\t" must not be blank',
      'annotation key "a\\ab" must not contain control characters',
    ]);
    expect(paths(annotated(null))).toEqual([]);
  });

  test('issues are sorted by path', () => {
    const doc = { ...createDocument(), $schema: '', revision: 0, id: '' };
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
      errorsFor(broken)
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

    expect(errorsFor(broken).length).toBeGreaterThan(0);
  });

  // A spec is free-form, and a file or a legacy diagram edited by hand may
  // hold anything in it. The check reports what it finds; it never throws.
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
      expect(errorsFor(withInterfaces(interfaces, []))).toEqual([]);
      expect(() => parseDocument(withInterfaces(interfaces, []))).not.toThrow();
      // One with a connection point is told that the list lacks its entry.
      expect(
        errorsFor(withInterfaces(interfaces)).map((issue) => issue.message),
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
      errorsFor(broken)
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
      errorsFor(broken).map(({ path, message }) => ({ path, message })),
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
    expect(errorsFor(doc)).toEqual([]);
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
        path: 'nodes[2].device.spec.network.interfaces[0].vlan',
        message:
          'interface "eth0" of "bravo" uses VLAN "GHOST", which is not a network in this diagram',
        level: 'warning',
        nodeId: bravo.id,
      },
    ]);
    expect(errorsFor(doc)).toEqual([]);
  });

  // Typing a VLAN in another case connects the interface to that network
  // (connectByVLAN in model.js), so the field has no warning; an
  // unconnected interface's is reported with its connection (below).
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

  // Publishing puts an unconnected interface on the network its VLAN
  // names, and refuses one with none, which minimega would refuse when the
  // experiment starts. The draft stays valid either way.
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
        message: `${of} is not connected to a network and has no VLAN, so it cannot be published: connect it, or type a VLAN for it`,
        blocksPublish: true,
      },
    ]);
    expect(errorsFor(sampleDocument().doc)).toEqual([]);
    expect(unconnected(withVLAN('EXP'))).toEqual([
      {
        level: 'warning',
        message: `${of} is not connected on the canvas, but its VLAN "EXP" puts it on network EXP when published`,
        blocksPublish: undefined,
      },
    ]);
    // Publishing keeps the VLAN as it is, and phenix matches VLAN names
    // exactly, so "exp" is a VLAN of its own, not network EXP.
    expect(unconnected(withVLAN('exp'))).toEqual([
      {
        level: 'warning',
        message: `${of} is not connected on the canvas, and its VLAN "exp" differs from network EXP only in case: phenix treats them as different VLANs, so publishing does not put it on network EXP`,
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

  // The server checks every interface of the device spec, so the diagram
  // does too, including those with no connection point on the canvas: an
  // unnamed one, a second one of the same name, and one an uploaded
  // document gave none. They are named by position where the name does not
  // tell them apart.
  test('every interface of the spec is checked, with a connection point or not', () => {
    // Each says why it has no connection point, and what fixes it.
    const none = (label, message) => ({
      level: 'warning',
      message: `interface ${label} of "bravo" ${message}`,
      blocksPublish: true,
    });
    const handleless =
      'has no VLAN and no connection point on the canvas, so it cannot be published: type a VLAN for it';
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
          'interface "eth0" (#1) of "bravo" is not connected on the canvas, but its VLAN "EXP" puts it on network EXP when published',
        blocksPublish: undefined,
      },
      none(
        '#2',
        'has no name and no VLAN, so it cannot be published: name it, then connect it or type a VLAN for it',
      ),
      none(
        '"eth0" (#3)',
        'has no VLAN, and another interface has its name, so it cannot be connected or published: rename it, then connect it or type a VLAN for it',
      ),
      none('"eth2"', handleless),
      {
        level: 'warning',
        message:
          'interface "eth3" of "bravo" is not connected on the canvas, and its VLAN "exp" differs from network EXP only in case: phenix treats them as different VLANs, so publishing does not put it on network EXP',
        blocksPublish: undefined,
      },
    ]);
    expect(errorsFor(doc)).toEqual([]);

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
        path: 'nodes[2].device.spec.hardware.drives[2].image',
        message:
          'drive image "missing.qc2" of "bravo" is not among the server\'s disk images',
        level: 'warning',
        nodeId: bravo.id,
      },
    ]);
    expect(
      warningsOf(doc, { disks: ['other.qc2'] }).map((issue) => issue.nodeId),
    ).toEqual([alpha.id, bravo.id, bravo.id, bravo.id]);
    expect(errorsFor(doc)).toEqual([]);
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
      validateDocument({ ...broken, id: '' }).find(
        (issue) => issue.path === 'id',
      ),
    ).toEqual({
      path: 'id',
      message: 'document ID is required',
      level: 'error',
    });
  });

  // phenix refuses some hostnames and warns about others (see
  // types/version/v1/hostname.go), and publishing refuses the first. A draft
  // keeps either, as one imported from a topology an older phenix stored has
  // them.
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

      expect(errorsFor(doc)).toEqual([]);
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
        'No network in this diagram is named "GHOST".',
      ],
    });
    expect(deviceFieldWarnings(doc, spec, { disks: ['ubuntu.qc2'] })).toEqual({
      'spec.network.interfaces.1.vlan': [
        'No network in this diagram is named "GHOST".',
      ],
      'spec.hardware.drives.1.image': [
        'The server has no disk image named "gone.qc2".',
      ],
    });
    expect(deviceFieldWarnings(doc, undefined, { disks: [] })).toEqual({});

    // The device's hostname, at its field.
    expect(deviceFieldWarnings(doc, {}, { hostname: '007' })).toEqual({
      hostname: [
        expect.stringMatching(
          /^Hostname "007" is all digits, so the device cannot be published: .*\.$/,
        ),
      ],
    });
  });
});

describe('IP and MAC addresses that two interfaces use', () => {
  // The sample with the spec interfaces of alpha and bravo changed by
  // `edits`, keyed by hostname.
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
        path: 'nodes[1].device.spec.network.interfaces[0].address',
        message:
          'IP address 10.0.0.5 of interface "eth0" of "alpha" is also used by interface "eth0" of "bravo"',
        level: 'warning',
        blocksPublish: true,
        nodeId: alpha.id,
      },
      {
        path: 'nodes[2].device.spec.network.interfaces[0].address',
        message:
          'IP address 10.0.0.5 of interface "eth0" of "bravo" is also used by interface "eth0" of "alpha"',
        level: 'warning',
        blocksPublish: true,
        nodeId: bravo.id,
      },
    ]);
    expect(errorsFor(doc)).toEqual([]);
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
        `IP address ${first} of interface "eth0" of "alpha" is also used by interface "eth0" of "bravo"`,
        `IP address ${shown} of interface "eth0" of "bravo" is also used by interface "eth0" of "alpha"`,
      ]);
    }

    for (const [second, shown] of [
      ['AA-BB-CC-DD-EE-FF', 'AA-BB-CC-DD-EE-FF'],
      ['aabb.ccdd.eeff', 'aabb.ccdd.eeff'],
      ['\taa:bb:cc:dd:ee:ff\n', 'aa:bb:cc:dd:ee:ff'],
    ]) {
      expect(pair({ mac: 'aa:bb:cc:dd:ee:ff' }, { mac: second })).toEqual([
        'MAC address aa:bb:cc:dd:ee:ff of interface "eth0" of "alpha" is also used by interface "eth0" of "bravo"',
        `MAC address ${shown} of interface "eth0" of "bravo" is also used by interface "eth0" of "alpha"`,
      ]);
    }
  });

  // minirouter, which the Router template makes, and Vyatta give a QinQ
  // interface its address (vrouter.go, vyatta.tmpl).
  test('the address of a QinQ interface is compared', () => {
    expect(pair(fixed('10.0.0.5', { qinq: true }), fixed('10.0.0.5'))).toEqual([
      'IP address 10.0.0.5 of interface "eth0" of "alpha" is also used by interface "eth0" of "bravo"',
      'IP address 10.0.0.5 of interface "eth0" of "bravo" is also used by interface "eth0" of "alpha"',
    ]);
  });

  // An interface that asks DHCP for its address has none of its own, phenix
  // brings a manual one up with none, and minimega makes a MAC for one that
  // has none. An address that does not parse is the Inspector form's to
  // report. Only ASCII whitespace is trimmed, as the server trims it
  // (topology_test.go has the same cases).
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
          { name: 'eth1', ...fixed('10.0.0.5') },
          { name: 'eth1', mac: '00-00-00-00-00-01' },
        );
      },
    });

    expect(addressIssues(doc).map((issue) => issue.message)).toEqual([
      'IP address 10.0.0.5 of interface "eth0" of "alpha" is also used by interface "eth0" of "bravo" and 1 more interface',
      'IP address 10.0.0.5 of interface "eth0" of "bravo" is also used by interface "eth0" of "alpha" and 1 more interface',
      'MAC address 00:00:00:00:00:01 of interface "eth0" of "bravo" is also used by interface "eth1" (#3) of "bravo"',
      'IP address 10.0.0.5 of interface "eth1" (#2) of "bravo" is also used by interface "eth0" of "alpha" and 1 more interface',
      'MAC address 00-00-00-00-00-01 of interface "eth1" (#3) of "bravo" is also used by interface "eth0" of "bravo"',
    ]);
  });

  // phenix does not start an external device, and its schema has no MAC,
  // so its MAC does not count. Its IP address, which the real device uses,
  // does.
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
      'IP address 10.0.0.5 of interface "eth0" of "alpha" is also used by interface "eth0" of "bravo"',
      'IP address 10.0.0.5 of interface "eth0" of "bravo" is also used by interface "eth0" of "alpha"',
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
          'IP address 10.0.0.5 of interface "eth0" of "bravo" is also used by interface "eth0" of "alpha"',
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
        'This IP address is also used by interface "eth0" of "alpha".',
      ],
      'spec.network.interfaces.1.mac': [
        'This MAC address is also used by interface "eth2" of "bravo".',
      ],
      'spec.network.interfaces.2.mac': [
        'This MAC address is also used by interface "eth1" of "bravo".',
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

  // The checks run on every edit, so each interface is looked up by its
  // address, never compared with every other one.
  test('a diagram of 500 devices is checked quickly', () => {
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
    const started = performance.now();
    const issues = addressIssues(large);
    const elapsed = performance.now() - started;

    expect(issues).toHaveLength(500);
    expect(
      issues.find((issue) => issue.path.startsWith('nodes[3].')),
    ).toMatchObject({
      path: 'nodes[3].device.spec.network.interfaces[3].mac',
      message:
        'MAC address 02:00:00:ff:ff:ff of interface "eth3" of "host-0" is also used by interface "eth3" of "host-1" and 498 more interfaces',
    });
    expect(elapsed).toBeLessThan(500);
  });
});

describe('scenario references', () => {
  // A scenario spec and the digest ContentDigest (ids.go) computes for it.
  const content = {
    apps: [
      {
        name: 'a<b&c',
        disabled: true,
        metadata: { z: 1.5, y: 'é', x: [1, 2] },
      },
    ],
  };
  const digest =
    'sha256:f19af28402bc72df46242888d480096bb8be566101e5992953f3f3ac195505e2';
  const other = `sha256:${'0'.repeat(64)}`;

  function withScenario(scenario) {
    return { ...sampleDocument().doc, scenario };
  }

  function scenarioErrors(scenario) {
    return errorsFor(withScenario(scenario)).filter((issue) =>
      issue.path.startsWith('scenario'),
    );
  }

  test('a stored reference without content is valid and reopens', () => {
    // What the Scenario dialog attaches: GET /builder/sources lists stored
    // scenarios by apiVersion and digest, never with their content.
    const stored = {
      kind: 'stored',
      name: 'my-scenario',
      apiVersion: 'phenix.sandia.gov/v1',
      digest: other,
    };

    expect(scenarioErrors(stored)).toEqual([]);
    expect(parseDocument(withScenario(stored)).scenario).toEqual(stored);
  });

  test('every reference requires an apiVersion and a well formed digest', () => {
    for (const kind of ['stored', 'uploaded']) {
      const ref = { kind, name: 'my-scenario', content };

      expect(scenarioErrors(ref), kind).toEqual([
        expect.objectContaining({ path: 'scenario.apiVersion' }),
        expect.objectContaining({
          path: 'scenario.digest',
          message: 'scenario reference requires a content digest',
        }),
      ]);
      expect(
        scenarioErrors({
          ...ref,
          apiVersion: SCENARIO_API_VERSION,
          digest: 'sha256:short',
        }).map((issue) => issue.message),
        kind,
      ).toEqual([
        'malformed scenario digest "sha256:short" (expected sha256:<64 hex>)',
      ]);
    }

    expect(
      scenarioErrors({ kind: 'stored', name: 'my-scenario', digest: other }),
    ).toEqual([expect.objectContaining({ path: 'scenario.apiVersion' })]);
  });

  test('the digest must match content when the reference carries it', () => {
    const uploaded = {
      kind: 'uploaded',
      name: 'scenario.yaml',
      apiVersion: SCENARIO_API_VERSION,
      content,
      digest,
    };

    expect(scenarioErrors(uploaded)).toEqual([]);
    expect(scenarioErrors({ ...uploaded, kind: 'stored' })).toEqual([]);
    expect(scenarioErrors({ ...uploaded, digest: other })).toEqual([
      {
        path: 'scenario.digest',
        message: `content digest mismatch (expected ${digest})`,
        level: 'error',
      },
    ]);
    // Content is checked as the latest scenario version; a reference without
    // content names a stored config of any version.
    expect(
      scenarioErrors({ ...uploaded, apiVersion: 'phenix.sandia.gov/v1' }),
    ).toEqual([
      expect.objectContaining({
        path: 'scenario.apiVersion',
        message: `unsupported scenario apiVersion "phenix.sandia.gov/v1" (expected "${SCENARIO_API_VERSION}")`,
      }),
    ]);
  });
});

// The rules themselves are in the corpus below, which the server runs too.
// These are the editor's side of them: its limits against the schema
// bundle, the functions other modules call, and values only a document that
// was not decoded can hold.
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
      `Fields a template fills in. At most ${MAX_TEMPLATE_DEVICE_BYTES} bytes as JSON.`,
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

  // Decoding refuses these, as the server does; a document the editor made
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

  // An icon id names a key of the document's icons, never a property every
  // object has.
  test('a custom icon is looked up among the document icons only', () => {
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
    const icons = { [ICON_KEY]: { data: ICON_DATA } };

    expect(using(ICON_KEY, icons)).toEqual([]);
    expect(using(ICON_KEY)).toEqual([path]);
    expect(using('constructor', icons)).toEqual([path]);
    expect(using('toString', icons)).toEqual([path]);
    expect(using('__proto__', icons)).toEqual([path]);
  });

  test('validateIcons reports at the path it is given', () => {
    expect(validateIcons(undefined)).toEqual([]);
    expect(validateIcons(null)).toEqual([]);
    expect(validateIcons({})).toEqual([]);
    expect(validateIcons({ [ICON_KEY]: { data: ICON_DATA } })).toEqual([]);
    expect(
      validateIcons({ plc: { data: ICON_DATA } }, 'library.icons'),
    ).toEqual([
      {
        path: 'library.icons',
        message: 'icon key "plc" must be sha256: and 64 hex digits',
        level: 'error',
      },
    ]);
    expect(validateIcons({ [ICON_KEY]: 'x' })[0]).toMatchObject({
      path: 'icons',
      message: expect.stringContaining('data must be base64'),
    });
    expect(messages(validateIcons([ICON_DATA]))).toEqual([
      'custom icons must be an object of icons by icon id',
    ]);
  });

  test('icons are counted, and reported in the order of their keys', () => {
    const icons = (count) =>
      Object.fromEntries(
        Array.from({ length: count }, (_, i) => {
          const data = base64Of(png(1, 1, [i, 0, 7, 255]));

          return [`sha256:${String(i).padStart(64, '0')}`, { data }];
        }),
      );

    // Each key is the id of other bytes, so each is reported, in order.
    const issues = messages(validateIcons(icons(MAX_DOCUMENT_ICONS + 1)));

    expect(issues[0]).toBe('at most 32 custom icons are allowed, not 33');
    expect(issues).toHaveLength(MAX_DOCUMENT_ICONS + 2);
    expect(issues.slice(1)).toEqual([...issues.slice(1)].sort());
    expect(messages(validateIcons(icons(MAX_DOCUMENT_ICONS)))).not.toContain(
      issues[0],
    );
  });

  // An icon that was checked is not decoded and hashed again on every edit:
  // its id is the digest of its data. Other data under the same id is.
  test('an icon found valid is checked again when its data changes', () => {
    const other = base64Of(png(2, 2, [1, 2, 3, 255]));

    expect(validateIcons({ [ICON_KEY]: { data: ICON_DATA } })).toEqual([]);
    expect(validateIcons({ [ICON_KEY]: { data: ICON_DATA } })).toEqual([]);
    expect(messages(validateIcons({ [ICON_KEY]: { data: other } }))).toEqual([
      expect.stringContaining('does not match its data'),
    ]);
    expect(messages(validateIcons({ [ICON_KEY]: { data: 'x' } }))).toEqual([
      expect.stringContaining('data must be base64'),
    ]);
    // The name is checked each time: it is no part of the id.
    expect(
      messages(
        validateIcons({
          [ICON_KEY]: {
            name: 'n'.repeat(MAX_ICON_NAME_BYTES + 1),
            data: ICON_DATA,
          },
        }),
      ),
    ).toEqual([expect.stringContaining('name must be at most 64 bytes')]);
    expect(validateIcons({ [ICON_KEY]: { data: ICON_DATA } })).toEqual([]);
  });

  test('templateIssues reports under the path it is given', () => {
    expect(templateIssues(template(), 'templates[0]')).toEqual([]);

    // The id and the custom icon are checked where the template is kept.
    expect(
      templateIssues(
        template({
          id: 'server',
          device: { icon: ICON_KEY, spec: { general: { hostname: 'x' } } },
        }),
        'templates[0]',
      ),
    ).toEqual([]);

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
    ).toEqual([
      {
        path: 'library.templates[3].name',
        message: 'template name is required',
        level: 'error',
      },
      {
        path: 'library.templates[3].description',
        message: 'template description must not contain control characters',
        level: 'error',
      },
      {
        path: 'library.templates[3].device.spec.general.hostname',
        message: 'template hostname "two words" must not contain whitespace',
        level: 'error',
      },
      {
        path: 'library.templates[3].device.iconKey',
        message: 'unknown icon key "toaster"',
        level: 'error',
      },
      {
        path: 'library.templates[3].device.outlineColor',
        message: 'color "red" must be a hex color such as #2f6fbf',
        level: 'error',
      },
      {
        path: 'library.templates[3].device.fillColor',
        message: 'color "#abc" must be a hex color such as #2f6fbf',
        level: 'error',
      },
    ]);

    for (const broken of [undefined, null, 'PLC', [], {}]) {
      expect(
        templateIssues(broken, 't').map((entry) => entry.path),
        JSON.stringify(broken),
      ).toEqual(['t.name', 't.device.spec']);
    }
  });

  // The server counts a template's device as it encodes it: without the
  // icon and color fields that are not set, "<" as six bytes and a
  // character outside ASCII by its UTF-8 bytes.
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

  function setIn(doc, path, value) {
    const parent = path
      .slice(0, -1)
      .reduce((container, key) => container[key], doc);

    parent[path[path.length - 1]] = value;
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

  function issueLines(issues = []) {
    return issues.map((issue) => `${issue.path}: ${issue.message}`).sort();
  }

  test.each(corpus.cases.map((entry) => [entry.name, entry]))(
    '%s',
    (_, entry) => {
      const doc = read(corpus.document);

      (entry.set || []).forEach(({ path, value, join }) =>
        setIn(doc, path, join ? joinParts(join) : value),
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
          refusal?.message.includes(`"${entry.error}"`),
        refusal?.message || 'accepted',
      ).toBe(true);
    },
  );
});
