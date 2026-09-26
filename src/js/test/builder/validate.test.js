import { readFileSync } from 'node:fs';

import { describe, expect, test } from 'vitest';

import { parseDocument } from '@/builder/decode.js';
import { createDocument } from '@/builder/model.js';
import {
  deviceFieldWarnings,
  isValidDocument,
  MAX_NAME_BYTES,
  MAX_VLAN_ALIAS,
  SCENARIO_API_VERSION,
  validateDocument,
} from '@/builder/validate.js';

import { sampleDocument } from './fixtures.js';

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
    expect(isValidDocument(doc)).toBe(true);
  });

  test('the schema URI and revision are checked', () => {
    const doc = { ...createDocument(), $schema: 'other', revision: 2 };

    expect(paths(doc)).toEqual(expect.arrayContaining(['$schema', 'revision']));
  });

  test('the name is bounded the way the server bounds a draft title (R61)', () => {
    const named = (name) => ({ ...createDocument(), name });

    expect(paths(named('n'.repeat(MAX_NAME_BYTES)))).toEqual([]);
    // Bytes, not characters: 257 two-byte characters are 514 bytes.
    expect(paths(named('é'.repeat(MAX_NAME_BYTES / 2 + 1)))).toEqual(['name']);
    expect(paths(named('my\ttopology'))).toEqual(['name']);
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
        // minimega VLAN names are case sensitive (R22).
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
    expect(isValidDocument(doc)).toBe(true);
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
    expect(isValidDocument(doc)).toBe(true);
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
    expect(isValidDocument(sampleDocument().doc)).toBe(true);
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
    expect(isValidDocument(doc)).toBe(true);

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
    expect(isValidDocument(doc)).toBe(true);
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

// The documents validate.go must agree on (TestValidationCorpus in
// types/builder), run through parseDocument as the editor opens a document
// (R65).
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

  test.each(corpus.cases.map((entry) => [entry.name, entry]))(
    '%s',
    (_, entry) => {
      const doc = read(corpus.document);

      (entry.set || []).forEach(({ path, value }) => setIn(doc, path, value));

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

      // An issue at the path, or a decoding error naming the key.
      expect(
        refusal?.issues?.some((issue) => issue.path === entry.error) ||
          refusal?.message.includes(`"${entry.error}"`),
        refusal?.message || 'accepted',
      ).toBe(true);
    },
  );
});
