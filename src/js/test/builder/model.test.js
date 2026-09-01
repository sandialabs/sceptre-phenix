import { describe, expect, test } from 'vitest';

import {
  addInterface,
  addNetwork,
  addNode,
  connect,
  createDocument,
  DEFAULT_GRID_SIZE,
  deviceHandles,
  documentSummary,
  edgeEndpoints,
  groupNodes,
  moveNodes,
  networkOfSwitch,
  nodeComment,
  nodeLabel,
  removeElements,
  removeInterface,
  removeNetworks,
  SCHEMA_REVISION,
  SCHEMA_URI,
  setScenario,
  specInterfaceFor,
  syncInterfaceVLANs,
  ungroup,
  updateNetwork,
  updateNode,
  validateConnection,
} from '@/builder/model.js';

import { sampleDocument } from './fixtures.js';

describe('document shape', () => {
  test('matches the server wire contract', () => {
    const doc = createDocument({ name: 'Topology' });

    expect(doc.$schema).toBe(SCHEMA_URI);
    expect(SCHEMA_URI).toBe('https://phenix.sandia.gov/schemas/builder/v1');
    expect(doc.revision).toBe(SCHEMA_REVISION);
    expect(doc.revision).toBe(1);
    expect(doc.id).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/,
    );
    expect(doc.nodes).toEqual([]);
    expect(doc.networks).toEqual([]);
    expect(doc.edges).toEqual([]);
    expect(doc.viewport).toEqual({ x: 0, y: 0, zoom: 1 });
    expect(doc.grid).toEqual({
      enabled: true,
      size: DEFAULT_GRID_SIZE,
      snap: true,
    });
    expect(doc).not.toHaveProperty('owner');
    expect(doc).not.toHaveProperty('schemaVersion');
  });

  test('summarizes nodes, networks and edges', () => {
    const { doc } = sampleDocument();

    expect(documentSummary(doc)).toMatchObject({
      devices: 2,
      switches: 1,
      networks: 1,
      links: 1,
    });
  });
});

describe('nodes', () => {
  test('a device carries hostname, icon key, spec and handles', () => {
    const { doc, alpha } = sampleDocument();
    const node = doc.nodes.find((entry) => entry.id === alpha.id);

    expect(node.kind).toBe('device');
    expect(node.device.hostname).toBe('alpha');
    expect(node.device.iconKey).toBe('linux');
    expect(node.device.spec.general.hostname).toBe('alpha');
    expect(node.device.interfaces).toHaveLength(1);
    expect(node.device.interfaces[0]).toMatchObject({ name: 'eth0', index: 0 });
    expect(node.device.interfaces[0].id).toBeTruthy();
    expect(node).not.toHaveProperty('vlan');
  });

  test('hostnames stay unique', () => {
    let doc = createDocument();

    doc = addNode(doc, { kind: 'device', hostname: 'alpha' }).doc;
    const second = addNode(doc, { kind: 'device', hostname: 'alpha' });

    expect(second.node.device.hostname).toBe('alpha-2');
  });

  test('a switch without a network creates one', () => {
    const doc = createDocument();
    const created = addNode(doc, { kind: 'switch', networkName: 'MGMT' });

    expect(created.network.name).toBe('MGMT');
    expect(created.node.switch.networkId).toBe(created.network.id);
    expect(networkOfSwitch(created.doc, created.node).name).toBe('MGMT');
  });

  test('notes and groups carry their own payload only', () => {
    let doc = createDocument();

    const note = addNode(doc, { kind: 'note', text: 'check me' });
    doc = note.doc;

    const group = addNode(doc, { kind: 'group', title: 'Rack 1' });

    expect(note.node.note).toEqual({ text: 'check me', color: '' });
    expect(note.node.device).toBeUndefined();
    expect(group.node.group).toMatchObject({
      title: 'Rack 1',
      collapsed: false,
    });
  });

  test('the node comment is the phenix spec description', () => {
    const { doc, alpha } = sampleDocument();
    const next = updateNode(doc, alpha.id, {
      device: { spec: { general: { description: 'jump host' } } },
    });

    expect(nodeComment(next.nodes.find((n) => n.id === alpha.id))).toBe(
      'jump host',
    );
    expect(nodeLabel(next.nodes.find((n) => n.id === alpha.id))).toBe('alpha');
  });

  test('updating a hostname does not mutate the prior device spec', () => {
    const { doc, alpha } = sampleDocument();
    const original = doc.nodes.find((node) => node.id === alpha.id);
    const originalSpec = JSON.parse(JSON.stringify(original.device.spec));

    const next = updateNode(doc, alpha.id, {
      device: { hostname: 'renamed' },
    });

    expect(original.device.spec).toEqual(originalSpec);
    expect(original.device.spec.general.hostname).toBe('alpha');
    expect(
      next.nodes.find((node) => node.id === alpha.id).device.spec.general
        .hostname,
    ).toBe('renamed');
  });

  test('multiple nodes move in one operation', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const next = moveNodes(doc, [
      { id: alpha.id, position: { x: 10, y: 20 } },
      { id: bravo.id, position: { x: 30, y: 40 } },
    ]);

    expect(next.nodes.find((n) => n.id === alpha.id).position).toEqual({
      x: 10,
      y: 20,
    });
    expect(next.nodes.find((n) => n.id === bravo.id).position).toEqual({
      x: 30,
      y: 40,
    });
  });
});

describe('networks', () => {
  test('names are unique regardless of case', () => {
    let doc = createDocument();

    doc = addNetwork(doc, { name: 'EXP' }).doc;
    const again = addNetwork(doc, { name: 'exp' });

    expect(again.network.name).toBe('exp-2');
    expect(again.doc.networks).toHaveLength(2);
  });

  test('suffix checks are also case insensitive', () => {
    let doc = createDocument();

    doc = addNetwork(doc, { name: 'EXP' }).doc;
    doc = addNetwork(doc, { name: 'exp-2' }).doc;

    expect(addNetwork(doc, { name: 'Exp' }).network.name).toBe('Exp-3');
  });

  test('renaming a network rewrites the interface vlan of connected devices', () => {
    const { doc, network, alpha } = sampleDocument();
    const renamed = updateNetwork(doc, network.id, { name: 'CORE' });
    const node = renamed.nodes.find((entry) => entry.id === alpha.id);

    expect(node.device.spec.network.interfaces[0].vlan).toBe('CORE');
    expect(renamed.networks[0].name).toBe('CORE');
  });

  test('interface vlans resync from the document', () => {
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
                  network: { interfaces: [{ name: 'eth0', vlan: 'WRONG' }] },
                },
              },
            }
          : node,
      ),
    };

    const fixed = syncInterfaceVLANs(broken);

    expect(
      fixed.nodes.find((n) => n.id === alpha.id).device.spec.network
        .interfaces[0].vlan,
    ).toBe('EXP');
  });

  test('removing a network removes its switches and edges', () => {
    const { doc, network } = sampleDocument();
    const next = removeNetworks(doc, [network.id]);

    expect(next.networks).toHaveLength(0);
    expect(next.nodes.filter((node) => node.kind === 'switch')).toHaveLength(0);
    expect(next.edges).toHaveLength(0);
  });
});

describe('interfaces', () => {
  test('adding an interface adds a handle and a spec entry', () => {
    const { doc, alpha } = sampleDocument();
    const { doc: next, handle } = addInterface(doc, alpha.id, {});
    const node = next.nodes.find((entry) => entry.id === alpha.id);

    expect(handle.name).toBe('eth1');
    expect(handle.index).toBe(1);
    expect(deviceHandles(node)).toHaveLength(2);
    expect(specInterfaceFor(node, handle.id).name).toBe('eth1');
  });

  test('removing an interface removes its edge and reindexes handles', () => {
    const { doc, alpha } = sampleDocument();
    const handle = doc.nodes.find((n) => n.id === alpha.id).device
      .interfaces[0];
    const next = removeInterface(doc, alpha.id, handle.id);
    const node = next.nodes.find((entry) => entry.id === alpha.id);

    expect(node.device.interfaces).toHaveLength(0);
    expect(next.edges).toHaveLength(0);
  });

  test('spec interfaces added by the inspector become handles', () => {
    const { doc, bravo } = sampleDocument();
    const next = updateNode(doc, bravo.id, {
      device: {
        spec: {
          general: { hostname: 'bravo' },
          network: {
            interfaces: [
              { name: 'eth0', type: 'ethernet', proto: 'dhcp' },
              { name: 'mgmt0', type: 'ethernet', proto: 'static' },
            ],
          },
        },
      },
    });

    const node = next.nodes.find((entry) => entry.id === bravo.id);

    expect(deviceHandles(node).map((handle) => handle.name)).toEqual([
      'eth0',
      'mgmt0',
    ]);
  });
});

describe('connections', () => {
  test('an edge joins one device handle to a switch on the same network', () => {
    const { doc, edge, alpha, sw, network } = sampleDocument();

    expect(edge.sourceNodeId).toBe(alpha.id);
    expect(edge.targetNodeId).toBe(sw.id);
    expect(edge.networkId).toBe(network.id);
    expect(edge.sourceHandleId).toBeTruthy();
    expect(edge).not.toHaveProperty('vlan');
    expect(edgeEndpoints(doc, edge).device.id).toBe(alpha.id);
  });

  test('connecting without a handle creates one', () => {
    const { doc, bravo, sw } = sampleDocument();
    const result = connect(doc, {
      sourceNodeId: bravo.id,
      targetNodeId: sw.id,
    });

    expect(result.edge.sourceHandleId).toBeTruthy();
    expect(result.error).toBeFalsy();
  });

  test('device to device is refused', () => {
    const { doc, alpha, bravo } = sampleDocument();

    expect(
      validateConnection(doc, {
        sourceNodeId: alpha.id,
        targetNodeId: bravo.id,
      }).valid,
    ).toBe(false);
  });

  test('a handle can only be used once', () => {
    const { doc, alpha, sw, edge } = sampleDocument();
    const result = validateConnection(doc, {
      sourceNodeId: alpha.id,
      sourceHandleId: edge.sourceHandleId,
      targetNodeId: sw.id,
    });

    expect(result.valid).toBe(false);
    expect(result.reason).toMatch(/already/i);
  });
});

describe('grouping and deletion', () => {
  test('grouping reparents nodes and ungrouping restores positions', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const grouped = groupNodes(doc, [alpha.id, bravo.id]);
    const groupNode = grouped.doc.nodes.find((node) => node.kind === 'group');

    expect(groupNode).toBeTruthy();
    expect(
      grouped.doc.nodes.find((node) => node.id === alpha.id).parentId,
    ).toBe(groupNode.id);

    const flat = ungroup(grouped.doc, groupNode.id);

    expect(
      flat.nodes.find((node) => node.id === alpha.id).parentId,
    ).toBeFalsy();
    expect(flat.nodes.find((node) => node.id === alpha.id).position).toEqual(
      doc.nodes.find((node) => node.id === alpha.id).position,
    );
  });

  test('deleting a device deletes its edges', () => {
    const { doc, alpha } = sampleDocument();
    const next = removeElements(doc, { nodes: [alpha.id], edges: [] });

    expect(next.nodes.find((node) => node.id === alpha.id)).toBeUndefined();
    expect(next.edges).toHaveLength(0);
  });
});

describe('scenario', () => {
  test('a scenario reference is stored on the document', () => {
    const doc = setScenario(createDocument(), {
      kind: 'stored',
      name: 'foo',
      apiVersion: 'phenix.sandia.gov/v1',
      digest: `sha256:${'a'.repeat(64)}`,
    });

    expect(doc.scenario).toMatchObject({ kind: 'stored', name: 'foo' });
    expect(setScenario(doc, null).scenario).toBeUndefined();
  });
});
