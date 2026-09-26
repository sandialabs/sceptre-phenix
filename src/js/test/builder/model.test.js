import { describe, expect, test } from 'vitest';

import {
  addInterface,
  addNetwork,
  addNode,
  connect,
  canConnect,
  connectionChanges,
  connectNodes,
  createDocument,
  DEFAULT_GRID_SIZE,
  deviceHandles,
  documentSummary,
  edgeEndpoints,
  findEdge,
  findNode,
  fitGroups,
  groupMinimumSize,
  groupNodes,
  moveNodes,
  networkOfSwitch,
  nextInterfaceName,
  nodeComment,
  nodeLabel,
  removeElements,
  removeInterface,
  removeNetworks,
  SCHEMA_REVISION,
  SCHEMA_URI,
  setParent,
  setScenario,
  sizeOf,
  specInterfaceFor,
  specInterfaces,
  syncInterfaceVLANs,
  ungroup,
  updateEdge,
  updateNetwork,
  updateNode,
  validateConnection,
} from '@/builder/model.js';
import { copySelection, pasteClipboard } from '@/builder/clipboard.js';
import { DEVICE_TEMPLATES } from '@/builder/catalog.js';

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

  // phenix's vrouter app configures only nodes of type Router or Firewall
  // (app/vrouter.go), and those through a router OS type, so the templates
  // named after them must say both (R4).
  test.each([
    ['server', 'VirtualMachine', 'linux'],
    ['workstation', 'VirtualMachine', 'windows'],
    ['router', 'Router', 'minirouter'],
    ['firewall', 'Firewall', 'minirouter'],
    ['external', 'HIL', undefined],
  ])('the %s template adds a device of type %s on %s', (id, type, osType) => {
    const template = DEVICE_TEMPLATES.find((entry) => entry.id === id);
    const { node } = addNode(createDocument(), {
      kind: 'device',
      template,
      hostname: id,
    });

    expect(node.device.spec.type).toBe(type);
    expect(node.device.spec.hardware?.os_type).toBe(osType);
  });

  test('the type table above covers every template', () => {
    expect(DEVICE_TEMPLATES.map((template) => template.id)).toEqual([
      'server',
      'workstation',
      'router',
      'firewall',
      'external',
    ]);
  });

  test('a device added without a template is a VirtualMachine', () => {
    const { node } = addNode(createDocument(), { kind: 'device' });

    expect(node.device.spec.type).toBe('VirtualMachine');
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

  test('a network added after one was removed takes a color no network uses', () => {
    let doc = createDocument();

    doc = addNetwork(doc, { name: 'A' }).doc;
    const middle = addNetwork(doc, { name: 'B' });
    doc = addNetwork(middle.doc, { name: 'C' }).doc;
    doc = removeNetworks(doc, [middle.network.id]);
    doc = addNetwork(doc, { name: 'D' }).doc;

    const colors = doc.networks.map((network) => network.color);
    expect(new Set(colors).size).toBe(3);
  });

  test('a pasted network keeps its color only if no network here uses it', () => {
    let source = createDocument();
    const net = addNetwork(source, { name: 'LAN' });
    source = addNode(net.doc, {
      kind: 'switch',
      networkId: net.network.id,
    }).doc;
    const switchId = source.nodes[0].id;
    const clip = copySelection(source, { nodes: [switchId], edges: [] });

    // The target already has a network in LAN's color.
    let target = addNetwork(createDocument(), { name: 'CORE' }).doc;
    expect(target.networks[0].color).toBe(net.network.color);

    target = pasteClipboard(target, clip).doc;
    const colors = target.networks.map((network) => network.color);
    expect(new Set(colors).size).toBe(target.networks.length);
  });

  test('renaming a network rewrites the interface vlan of connected devices', () => {
    const { doc, network, alpha } = sampleDocument();
    const renamed = updateNetwork(doc, network.id, { name: 'CORE' });
    const node = renamed.nodes.find((entry) => entry.id === alpha.id);

    expect(node.device.spec.network.interfaces[0].vlan).toBe('CORE');
    expect(renamed.networks[0].name).toBe('CORE');
  });

  // R37: the switch is named after its network wherever a node is named.
  test('a switch is named after its network, renamed or not', () => {
    const { doc, network, sw } = sampleDocument();
    const second = addNode(doc, {
      kind: 'switch',
      networkId: network.id,
      label: 'something else',
    });
    const renamed = updateNetwork(second.doc, network.id, { name: 'MGMT' });

    expect(nodeLabel(findNode(second.doc, second.node.id))).toBe('EXP');
    expect(nodeLabel(findNode(renamed, sw.id))).toBe('MGMT');
    expect(nodeLabel(findNode(renamed, second.node.id))).toBe('MGMT');

    // Moved to another network, it takes that one's name.
    const lan = addNetwork(renamed, { name: 'LAN' });
    const moved = updateNode(lan.doc, sw.id, {
      switch: { networkId: lan.network.id },
      label: 'ignored',
    });
    expect(nodeLabel(findNode(moved, sw.id))).toBe('LAN');
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

  test('a new interface is one past the highest ethN, or eth0', () => {
    const device = (names, spec = names) => ({
      kind: 'device',
      device: {
        interfaces: names.map((name, index) => ({ id: name, name, index })),
        spec: { network: { interfaces: spec.map((name) => ({ name })) } },
      },
    });

    expect(nextInterfaceName(device([]))).toBe('eth0');
    expect(nextInterfaceName(device(['eth1', 'eth2']))).toBe('eth3');
    expect(nextInterfaceName(device(['eth2', 'eth0']))).toBe('eth3');
    expect(nextInterfaceName(device(['mgmt0', 'eth10']))).toBe('eth11');
    expect(nextInterfaceName(device(['mgmt0', 'ethernet1']))).toBe('eth0');
    // A spec entry counts without a handle of its own.
    expect(nextInterfaceName(device(['eth0'], ['eth0', 'eth4']))).toBe('eth5');
  });

  test('drawn connections and Add connection point add manual Ethernet interfaces', () => {
    const { doc, alpha, sw } = sampleDocument();
    const drawn = (connection) =>
      connect(doc, {
        sourceNodeId: alpha.id,
        targetNodeId: sw.id,
        ...connection,
      });
    const specOf = (next, id) =>
      specInterfaces(next.nodes.find((node) => node.id === id));

    // eth0 is taken; the drawn connection and the button both add eth1.
    for (const next of [drawn({}).doc, addInterface(doc, alpha.id).doc]) {
      expect(specOf(next, alpha.id)).toEqual([
        expect.objectContaining({ name: 'eth0', vlan: 'EXP' }),
        expect.objectContaining({
          name: 'eth1',
          type: 'ethernet',
          proto: 'manual',
        }),
      ]);
    }

    // With eth1 and eth2 on the device, the next is eth3, not eth0.
    let next = updateNode(doc, alpha.id, {
      device: {
        spec: {
          general: { hostname: 'alpha' },
          network: {
            interfaces: [
              { name: 'eth1', type: 'ethernet', proto: 'dhcp' },
              { name: 'eth2', type: 'ethernet', proto: 'static' },
            ],
          },
        },
      },
    });
    next = connect(next, { sourceNodeId: alpha.id, targetNodeId: sw.id }).doc;
    expect(specOf(next, alpha.id)).toEqual([
      expect.objectContaining({ name: 'eth1', proto: 'dhcp' }),
      expect.objectContaining({ name: 'eth2', proto: 'static' }),
      expect.objectContaining({ name: 'eth3', proto: 'manual', vlan: 'EXP' }),
    ]);
  });

  test('a pasted device keeps its spec interfaces, once each, on no network', () => {
    const { doc, alpha, sw, edge } = sampleDocument();
    const pasted = pasteClipboard(
      doc,
      copySelection(doc, { nodes: [alpha.id] }),
    );
    const copy = pasted.doc.nodes.find((node) => node.id === pasted.nodeIds[0]);

    expect(deviceHandles(copy).map((handle) => handle.name)).toEqual(['eth0']);
    expect(specInterfaces(copy)).toEqual([
      expect.objectContaining({ name: 'eth0', vlan: '' }),
    ]);

    // Pasted with its switch, it keeps its connection's label and color.
    const styled = updateEdge(doc, edge.id, {
      label: 'uplink',
      color: '#c0392b',
    });
    const both = pasteClipboard(
      styled,
      copySelection(styled, { nodes: [alpha.id, sw.id] }),
    );

    expect(both.doc.edges.slice(styled.edges.length)).toEqual([
      expect.objectContaining({ label: 'uplink', color: '#c0392b' }),
    ]);
  });

  // R79 and N15: a copy is named after its own hostname, and pasting again
  // cascades instead of stacking copies on each other.
  test('each paste is named after its own hostname and lands clear of the last', () => {
    const { doc, alpha } = sampleDocument();
    const clipboard = copySelection(doc, { nodes: [alpha.id] });
    const first = pasteClipboard(doc, clipboard);
    const second = pasteClipboard(first.doc, clipboard);
    const copyOf = (result) => findNode(result.doc, result.nodeIds[0]);

    expect(nodeLabel(copyOf(first))).toBe('alpha-2');
    expect(nodeLabel(copyOf(second))).toBe('alpha-3');
    expect(copyOf(first).position).toEqual({ x: 40, y: 40 });
    expect(copyOf(second).position).toEqual({ x: 80, y: 80 });

    // A label of its own is kept.
    const labelled = updateNode(doc, alpha.id, { label: 'Uplink box' });
    const pasted = pasteClipboard(
      labelled,
      copySelection(labelled, { nodes: [alpha.id] }),
    );
    expect(nodeLabel(copyOf(pasted))).toBe('Uplink box');
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

    // A color of its own, drawn in place of its network's, which '' removes.
    const colored = updateEdge(doc, edge.id, { color: '#c0392b' });

    expect(findEdge(colored, edge.id)).toEqual({ ...edge, color: '#c0392b' });
    expect(findEdge(doc, edge.id)).toEqual(edge);
    expect(
      findEdge(updateEdge(colored, edge.id, { color: '' }), edge.id),
    ).toEqual(edge);
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

// An interface's VLAN is its connection: typing one in the Inspector chooses
// it, and an unconnected interface keeps the VLAN it has.
describe('interface VLANs', () => {
  const nodeIn = (doc, id) => doc.nodes.find((node) => node.id === id);
  const vlans = (doc, id) =>
    Object.fromEntries(
      specInterfaces(nodeIn(doc, id)).map((iface) => [iface.name, iface.vlan]),
    );
  const edgesOf = (doc, id) =>
    doc.edges.filter(
      (edge) => edge.sourceNodeId === id || edge.targetNodeId === id,
    );

  // Applies a device's spec as the Inspector does, with these VLANs, and
  // interfaces added as { name: vlan }.
  function applyVLANs(doc, id, typed, added = {}) {
    const spec = JSON.parse(JSON.stringify(nodeIn(doc, id).device.spec));

    spec.network.interfaces = [
      ...spec.network.interfaces.map((iface) =>
        iface.name in typed ? { ...iface, vlan: typed[iface.name] } : iface,
      ),
      ...Object.entries(added).map(([name, vlan]) => ({
        name,
        type: 'ethernet',
        proto: 'dhcp',
        vlan,
      })),
    ];

    return updateNode(doc, id, { device: { spec } });
  }

  // The sample with a second network, LAN, and a second switch of EXP,
  // after its first.
  function twoNetworks() {
    const sample = sampleDocument();
    const lan = addNetwork(sample.doc, { name: 'LAN' });
    const lanSwitch = addNode(lan.doc, {
      kind: 'switch',
      networkId: lan.network.id,
    });
    const second = addNode(lanSwitch.doc, {
      kind: 'switch',
      networkId: sample.network.id,
    });

    return {
      ...sample,
      doc: second.doc,
      lan: lan.network,
      lanSwitch: lanSwitch.node,
    };
  }

  test('a typed VLAN naming a network connects the interface to its first switch', () => {
    const { doc, bravo, sw, network } = twoNetworks();
    const next = applyVLANs(doc, bravo.id, { eth0: 'exp' });
    const [edge] = edgesOf(next, bravo.id);

    expect(edge).toMatchObject({
      sourceNodeId: bravo.id,
      sourceHandleId: deviceHandles(nodeIn(next, bravo.id))[0].id,
      targetNodeId: sw.id,
      networkId: network.id,
    });
    // Written as the network is named, regardless of the case typed.
    expect(vlans(next, bravo.id)).toEqual({ eth0: 'EXP' });
    expect(connectionChanges(doc, next, bravo.id)).toEqual([
      'connected eth0 to network EXP',
    ]);

    // A network of that very name wins over one differing only by case:
    // minimega keeps such VLANs apart (R22).
    const lower = addNode(
      { ...doc, networks: [...doc.networks, { id: 'net-lower', name: 'exp' }] },
      { kind: 'switch', networkId: 'net-lower' },
    );
    const exact = applyVLANs(lower.doc, bravo.id, { eth0: 'exp' });

    expect(edgesOf(exact, bravo.id)).toEqual([
      expect.objectContaining({
        targetNodeId: lower.node.id,
        networkId: 'net-lower',
      }),
    ]);
    expect(vlans(exact, bravo.id)).toEqual({ eth0: 'exp' });
  });

  test('a typed VLAN naming another network moves the connection', () => {
    let { doc, alpha, edge, lan, lanSwitch } = twoNetworks();
    doc = updateEdge(doc, edge.id, { label: 'uplink', color: '#c0392b' });
    const next = applyVLANs(doc, alpha.id, { eth0: 'LAN' });

    expect(edgesOf(next, alpha.id)).toEqual([
      {
        ...findEdge(doc, edge.id),
        targetNodeId: lanSwitch.id,
        networkId: lan.id,
      },
    ]);
    expect(vlans(next, alpha.id)).toEqual({ eth0: 'LAN' });
    expect(connectionChanges(doc, next, alpha.id)).toEqual([
      'moved eth0 from network EXP to network LAN',
    ]);
  });

  test('a VLAN naming no network, or none, disconnects and is kept', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const ghost = applyVLANs(doc, alpha.id, { eth0: 'GHOST' });

    expect(edgesOf(ghost, alpha.id)).toEqual([]);
    expect(vlans(ghost, alpha.id)).toEqual({ eth0: 'GHOST' });
    expect(connectionChanges(doc, ghost, alpha.id)).toEqual([
      'disconnected eth0 from network EXP',
    ]);

    // Nothing else sets it back: not a resync, an edit of the device that
    // leaves its VLAN, or one of another device.
    let later = syncInterfaceVLANs(ghost);
    later = updateNode(later, alpha.id, { device: { hostname: 'alpha-2' } });
    later = applyVLANs(later, alpha.id, {});
    later = applyVLANs(later, bravo.id, { eth0: 'EXP' });
    expect(vlans(later, alpha.id)).toEqual({ eth0: 'GHOST' });
    expect(edgesOf(later, alpha.id)).toEqual([]);

    const emptied = applyVLANs(doc, alpha.id, { eth0: '' });
    expect(edgesOf(emptied, alpha.id)).toEqual([]);
    expect(vlans(emptied, alpha.id)).toEqual({ eth0: '' });
    expect(connectionChanges(doc, emptied, alpha.id)).toEqual([
      'disconnected eth0 from network EXP',
    ]);

    // An emptied field has no value in the form; a blank one, spaces. Each
    // is stored as a disconnect on the canvas leaves it.
    for (const vlan of [undefined, '  ']) {
      const cleared = applyVLANs(doc, alpha.id, { eth0: vlan });

      expect(edgesOf(cleared, alpha.id)).toEqual([]);
      expect(vlans(cleared, alpha.id)).toEqual({ eth0: '' });
    }
  });

  // Publishing puts the interface on that network, so the canvas shows it
  // there, as a connection drawn between two devices adds its switch.
  test('a typed VLAN naming a network with no switch adds one beside the device', () => {
    let { doc, alpha, bravo } = sampleDocument();
    const lan = addNetwork(doc, { name: 'LAN' });
    const group = groupNodes(lan.doc, [bravo.id], { title: 'Rack' });
    doc = group.doc;

    const next = applyVLANs(doc, bravo.id, { eth0: 'lan' }, { eth1: 'LAN' });
    const added = next.nodes.filter(
      (node) => !doc.nodes.some((entry) => entry.id === node.id),
    );
    const device = nodeIn(next, bravo.id);

    // One switch for both interfaces, in the device's group, clear of it.
    expect(added).toEqual([
      expect.objectContaining({
        kind: 'switch',
        label: 'LAN',
        parentId: device.parentId,
        switch: { networkId: lan.network.id },
      }),
    ]);
    expect(device.parentId).toBeTruthy();

    const [hub] = added;
    const overlaps = (a, b) =>
      a.position.x < b.position.x + sizeOf(b).width &&
      b.position.x < a.position.x + sizeOf(a).width &&
      a.position.y < b.position.y + sizeOf(b).height &&
      b.position.y < a.position.y + sizeOf(a).height;

    expect(
      next.nodes.filter(
        (node) => node.kind !== 'group' && node !== hub && overlaps(node, hub),
      ),
    ).toEqual([]);
    expect(Math.abs(hub.position.y - device.position.y)).toBeLessThan(200);

    expect(
      edgesOf(next, bravo.id).map((edge) => [
        edge.targetNodeId,
        edge.networkId,
      ]),
    ).toEqual([
      [hub.id, lan.network.id],
      [hub.id, lan.network.id],
    ]);
    expect(vlans(next, bravo.id)).toEqual({ eth0: 'LAN', eth1: 'LAN' });
    expect(connectionChanges(doc, next, bravo.id)).toEqual([
      'added a switch for network LAN',
      'connected eth0 to network LAN',
      'connected eth1 to network LAN',
    ]);

    // The edit of another device leaves it be.
    expect(applyVLANs(next, alpha.id, {}).nodes).toEqual(next.nodes);
  });

  test('only a VLAN the edit changes sets the connection', () => {
    const { doc, alpha, bravo, edge } = sampleDocument();
    // An unconnected interface that already names EXP, as an upload can.
    const unconnected = {
      ...doc,
      nodes: doc.nodes.map((node) =>
        node.id === bravo.id
          ? {
              ...node,
              device: {
                ...node.device,
                spec: {
                  ...node.device.spec,
                  network: { interfaces: [{ name: 'eth0', vlan: 'EXP' }] },
                },
              },
            }
          : node,
      ),
    };

    const described = applyVLANs(unconnected, bravo.id, {});
    expect(edgesOf(described, bravo.id)).toEqual([]);
    expect(applyVLANs(described, alpha.id, {}).edges).toEqual([edge]);

    const typed = applyVLANs(described, bravo.id, { eth0: 'exp' });
    expect(edgesOf(typed, bravo.id)).toHaveLength(1);
  });

  test('an interface added with a VLAN gets a handle and its connection', () => {
    const { doc, bravo, sw } = sampleDocument();
    const next = applyVLANs(doc, bravo.id, {}, { eth1: 'EXP' });
    const handle = deviceHandles(nodeIn(next, bravo.id)).find(
      (entry) => entry.name === 'eth1',
    );

    expect(edgesOf(next, bravo.id)).toEqual([
      expect.objectContaining({
        sourceHandleId: handle.id,
        targetNodeId: sw.id,
      }),
    ]);
  });

  test('removing a connection or its network empties the VLAN it set', () => {
    const { doc, alpha, bravo, edge, network } = sampleDocument();
    const ghost = applyVLANs(doc, bravo.id, { eth0: 'GHOST' });

    for (const next of [
      removeElements(ghost, { edges: [edge.id] }),
      removeNetworks(ghost, [network.id]),
    ]) {
      expect(vlans(next, alpha.id)).toEqual({ eth0: '' });
      expect(vlans(next, bravo.id)).toEqual({ eth0: 'GHOST' });
    }
  });

  test('a device pasted without its connection keeps a VLAN naming no network', () => {
    const { doc, bravo } = sampleDocument();
    const ghost = applyVLANs(doc, bravo.id, { eth0: 'GHOST' }, { eth1: 'EXP' });
    const pasted = pasteClipboard(
      ghost,
      copySelection(ghost, { nodes: [bravo.id] }),
    );
    const [copy] = pasted.nodeIds;

    expect(edgesOf(pasted.doc, copy)).toEqual([]);
    expect(vlans(pasted.doc, copy)).toEqual({ eth0: 'GHOST', eth1: '' });
  });
});

describe('drag-to-connect between any nodes', () => {
  const vlanOf = (doc, node, handleId) =>
    doc.nodes
      .find((entry) => entry.id === node.id)
      .device.spec.network.interfaces.find(
        (iface) =>
          iface.name ===
          deviceHandles(doc.nodes.find((entry) => entry.id === node.id)).find(
            (handle) => handle.id === handleId,
          ).name,
      ).vlan;

  test('switch to a device with no interfaces adds one on the network', () => {
    let { doc, sw, network } = sampleDocument();
    const added = addNode(doc, { kind: 'device', hostname: 'charlie' });
    doc = added.doc;

    const result = connectNodes(doc, {
      sourceNodeId: sw.id,
      targetNodeId: added.node.id,
    });

    expect(result.error).toBeFalsy();
    expect(result.edges).toHaveLength(1);
    const [edge] = result.edges;
    const charlie = result.doc.nodes.find((node) => node.id === added.node.id);
    expect(deviceHandles(charlie).map((handle) => handle.name)).toEqual([
      'eth0',
    ]);
    expect(edge.networkId).toBe(network.id);
    expect(edge.targetHandleId).toBe(deviceHandles(charlie)[0].id);
    expect(vlanOf(result.doc, charlie, edge.targetHandleId)).toBe('EXP');
  });

  test('device to switch adds the next free interface', () => {
    const { doc, alpha, sw, network } = sampleDocument();
    const result = connectNodes(doc, {
      sourceNodeId: alpha.id,
      targetNodeId: sw.id,
    });

    expect(result.error).toBeFalsy();
    const updated = result.doc.nodes.find((node) => node.id === alpha.id);
    expect(deviceHandles(updated).map((handle) => handle.name)).toEqual([
      'eth0',
      'eth1',
    ]);
    expect(result.edges[0].sourceHandleId).toBe(deviceHandles(updated)[1].id);
    expect(result.edges[0].networkId).toBe(network.id);
  });

  test('two devices get a network of their own through a new switch', () => {
    let doc = createDocument();
    const a = addNode(doc, {
      kind: 'device',
      hostname: 'a',
      position: { x: 0, y: 0 },
    });
    doc = a.doc;
    const b = addNode(doc, {
      kind: 'device',
      hostname: 'b',
      position: { x: 400, y: 200 },
    });
    doc = b.doc;

    const result = connectNodes(doc, {
      sourceNodeId: a.node.id,
      targetNodeId: b.node.id,
    });

    expect(result.error).toBeFalsy();
    expect(result.node.kind).toBe('switch');
    expect(result.doc.networks).toHaveLength(1);
    expect(result.edges).toHaveLength(2);
    for (const edge of result.edges) {
      expect(edge.networkId).toBe(result.doc.networks[0].id);
      expect([edge.sourceNodeId, edge.targetNodeId]).toContain(result.node.id);
    }

    // Halfway between the two devices.
    const center = (node) => ({
      x: node.position.x + 80,
      y: node.position.y + 48,
    });
    const middle = {
      x: (center(a.node).x + center(b.node).x) / 2,
      y: (center(a.node).y + center(b.node).y) / 2,
    };
    // On the grid, so within one grid step of the exact middle.
    expect(
      Math.abs(result.node.position.x + 90 - middle.x),
    ).toBeLessThanOrEqual(DEFAULT_GRID_SIZE);
    expect(
      Math.abs(result.node.position.y + 36 - middle.y),
    ).toBeLessThanOrEqual(DEFAULT_GRID_SIZE);

    for (const device of [a.node, b.node]) {
      const updated = result.doc.nodes.find((node) => node.id === device.id);
      const [handle] = deviceHandles(updated);
      expect(handle.name).toBe('eth0');
      expect(vlanOf(result.doc, updated, handle.id)).toBe(
        result.doc.networks[0].name,
      );
    }
  });

  test('two devices keep the interfaces they were dragged between', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const free = addInterface(doc, bravo.id, {});
    const alphaFree = addInterface(free.doc, alpha.id, {});

    const result = connectNodes(alphaFree.doc, {
      sourceNodeId: alpha.id,
      sourceHandleId: alphaFree.handle.id,
      targetNodeId: bravo.id,
      targetHandleId: free.handle.id,
    });

    expect(result.error).toBeFalsy();
    const handles = result.edges.flatMap((edge) => [
      edge.sourceHandleId,
      edge.targetHandleId,
    ]);
    expect(handles).toContain(alphaFree.handle.id);
    expect(handles).toContain(free.handle.id);
  });

  test('an interface already in use is refused', () => {
    const { doc, alpha, bravo, edge } = sampleDocument();
    const result = connectNodes(doc, {
      sourceNodeId: alpha.id,
      sourceHandleId: edge.sourceHandleId,
      targetNodeId: bravo.id,
    });

    expect(result.error).toMatch(/already connected/);
    expect(result.doc).toBe(doc);
  });

  test('the new switch never lands on top of other nodes', () => {
    // Palette spacing: 160px-wide devices 220px apart leave only a 60px gap.
    let doc = createDocument();
    const a = addNode(doc, {
      kind: 'device',
      hostname: 'a',
      position: { x: 80, y: 80 },
    });
    doc = a.doc;
    const b = addNode(doc, {
      kind: 'device',
      hostname: 'b',
      position: { x: 300, y: 80 },
    });
    doc = b.doc;

    const result = connectNodes(doc, {
      sourceNodeId: a.node.id,
      targetNodeId: b.node.id,
    });
    const box = (node) => ({
      left: node.position.x,
      top: node.position.y,
      right: node.position.x + (node.kind === 'switch' ? 180 : 160),
      bottom: node.position.y + (node.kind === 'switch' ? 72 : 96),
    });
    const overlaps = (p, q) =>
      p.left < q.right &&
      p.right > q.left &&
      p.top < q.bottom &&
      p.bottom > q.top;

    const sw = box(result.node);
    for (const device of [a.node, b.node]) {
      expect(overlaps(sw, box(device))).toBe(false);
    }
    // Still between the two devices horizontally, just below them.
    expect(sw.left + 90).toBeGreaterThan(160);
    expect(sw.left + 90).toBeLessThan(380);
    expect(sw.top).toBeGreaterThanOrEqual(176);
  });

  test('two devices in a group keep their new switch in that group', () => {
    const { doc, alpha, bravo, edge } = sampleDocument();
    const grouped = groupNodes(
      removeElements(doc, { nodes: [], edges: [edge.id] }),
      [alpha.id, bravo.id],
    );
    const group = grouped.doc.nodes.find((node) => node.kind === 'group');

    const result = connectNodes(grouped.doc, {
      sourceNodeId: alpha.id,
      targetNodeId: bravo.id,
    });

    expect(result.error).toBeFalsy();
    expect(result.node.parentId).toBe(group.id);
  });

  test('canConnect reports why a pair cannot be joined', () => {
    let { doc, alpha, bravo, sw, edge } = sampleDocument();
    const note = addNode(doc, { kind: 'note' });
    doc = note.doc;

    expect(
      canConnect(doc, { sourceNodeId: sw.id, targetNodeId: bravo.id }),
    ).toEqual({
      valid: true,
    });
    expect(
      canConnect(doc, {
        sourceNodeId: sw.id,
        targetNodeId: alpha.id,
        targetHandleId: edge.sourceHandleId,
      }).reason,
    ).toMatch(/already connected/);
    expect(
      canConnect(doc, {
        sourceNodeId: sw.id,
        targetNodeId: alpha.id,
        targetHandleId: 'nope',
      }).reason,
    ).toMatch(/Unknown interface/);
    expect(
      canConnect(doc, { sourceNodeId: alpha.id, targetNodeId: note.node.id })
        .valid,
    ).toBe(false);
    expect(
      canConnect(doc, { sourceNodeId: alpha.id, targetNodeId: alpha.id }).valid,
    ).toBe(false);
  });

  test('switches, notes, groups and self-connections are refused', () => {
    let { doc, alpha, sw } = sampleDocument();
    const other = addNode(doc, { kind: 'switch' });
    doc = other.doc;
    const note = addNode(doc, { kind: 'note' });
    doc = note.doc;
    const group = addNode(doc, { kind: 'group' });
    doc = group.doc;

    const refuse = (sourceNodeId, targetNodeId, pattern) => {
      const result = connectNodes(doc, { sourceNodeId, targetNodeId });
      expect(result.error).toMatch(pattern);
      expect(result.doc).toBe(doc);
      expect(result.edges).toEqual([]);
    };

    refuse(sw.id, other.node.id, /Two switches/);
    refuse(alpha.id, note.node.id, /Notes and groups/);
    refuse(group.node.id, sw.id, /Notes and groups/);
    refuse(alpha.id, alpha.id, /itself/);
  });
});

describe('moving nodes', () => {
  test('moving a group carries its members and keeps their connections', () => {
    const { doc, alpha, bravo, edge } = sampleDocument();
    const grouped = groupNodes(doc, [alpha.id, bravo.id]);
    const group = grouped.doc.nodes.find((node) => node.kind === 'group');
    const at = (next, id) => next.nodes.find((node) => node.id === id).position;

    const moved = moveNodes(grouped.doc, [
      {
        id: group.id,
        position: { x: group.position.x + 300, y: group.position.y + 40 },
      },
    ]);

    for (const id of [alpha.id, bravo.id]) {
      expect(at(moved, id)).toEqual({
        x: at(grouped.doc, id).x + 300,
        y: at(grouped.doc, id).y + 40,
      });
    }
    expect(moved.edges).toEqual(grouped.doc.edges);
    expect(moved.edges[0].id).toBe(edge.id);
  });

  test('a member moved together with its group keeps its own position', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const grouped = groupNodes(doc, [alpha.id, bravo.id]);
    const group = grouped.doc.nodes.find((node) => node.kind === 'group');
    const alphaAt = grouped.doc.nodes.find(
      (node) => node.id === alpha.id,
    ).position;

    const moved = moveNodes(grouped.doc, [
      {
        id: group.id,
        position: { x: group.position.x + 10, y: group.position.y },
      },
      { id: alpha.id, position: { x: alphaAt.x + 10, y: alphaAt.y } },
    ]);

    expect(moved.nodes.find((node) => node.id === alpha.id).position).toEqual({
      x: alphaAt.x + 10,
      y: alphaAt.y,
    });
  });

  test('nested groups move with their parent', () => {
    const { doc, alpha } = sampleDocument();
    const inner = groupNodes(doc, [alpha.id]);
    const innerGroup = inner.doc.nodes.find((node) => node.kind === 'group');
    const outer = groupNodes(inner.doc, [innerGroup.id]);
    const outerGroup = outer.doc.nodes.find(
      (node) => node.kind === 'group' && node.id !== innerGroup.id,
    );
    const before = outer.doc.nodes.find(
      (node) => node.id === alpha.id,
    ).position;

    const moved = moveNodes(outer.doc, [
      {
        id: outerGroup.id,
        position: { x: outerGroup.position.x, y: outerGroup.position.y + 50 },
      },
    ]);

    expect(moved.nodes.find((node) => node.id === alpha.id).position).toEqual({
      x: before.x,
      y: before.y + 50,
    });
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

    // Each new group has a title of its own (R56).
    expect(groupNode.group.title).toBe('Group');
    expect(
      groupNodes(ungroup(grouped.doc, groupNode.id), [alpha.id]).group.group
        .title,
    ).toBe('Group');
    expect(addNode(grouped.doc, { kind: 'group' }).node.group.title).toBe(
      'Group 2',
    );

    const flat = ungroup(grouped.doc, groupNode.id);

    expect(
      flat.nodes.find((node) => node.id === alpha.id).parentId,
    ).toBeFalsy();
    expect(flat.nodes.find((node) => node.id === alpha.id).position).toEqual(
      doc.nodes.find((node) => node.id === alpha.id).position,
    );
  });

  // R56: a node moves into a group, between groups and out of one, and its
  // place on the canvas follows.
  describe("changing a node's group", () => {
    const boxOf = (node) => ({ ...node.position, ...sizeOf(node) });
    const inside = (outer, inner) =>
      inner.x >= outer.x &&
      inner.y >= outer.y &&
      inner.x + inner.width <= outer.x + outer.width &&
      inner.y + inner.height <= outer.y + outer.height;
    const overlaps = (a, b) =>
      a.x < b.x + b.width &&
      b.x < a.x + a.width &&
      a.y < b.y + b.height &&
      b.y < a.y + a.height;

    function grouped() {
      const { doc, alpha, bravo, sw } = sampleDocument();
      const made = groupNodes(doc, [alpha.id]);

      return { doc: made.doc, group: made.group, alpha, bravo, sw };
    }

    test('a node joins a group inside its box, which grows to hold it', () => {
      const { doc, group, bravo } = grouped();
      const next = setParent(doc, bravo.id, group.id);
      const box = boxOf(findNode(next, group.id));

      expect(findNode(next, bravo.id).parentId).toBe(group.id);
      expect(inside(box, boxOf(findNode(next, bravo.id)))).toBe(true);
      expect(inside(box, boxOf(findNode(next, group.id)))).toBe(true);
      for (const member of next.nodes.filter((n) => n.parentId === group.id)) {
        expect(inside(box, boxOf(member))).toBe(true);
      }
    });

    test('a node that leaves a group leaves its box too', () => {
      const { doc, group, alpha } = grouped();
      const next = setParent(doc, alpha.id, null);

      expect(findNode(next, alpha.id).parentId).toBeUndefined();
      expect(
        overlaps(
          boxOf(findNode(next, group.id)),
          boxOf(findNode(next, alpha.id)),
        ),
      ).toBe(false);
    });

    test('a nested group moves with its members, out to the group above', () => {
      const { doc, group, alpha, bravo } = grouped();
      const outer = groupNodes(doc, [group.id, bravo.id]);
      const next = setParent(outer.doc, group.id, null);
      const moved = findNode(next, group.id);

      expect(moved.parentId).toBeUndefined();
      expect(inside(boxOf(moved), boxOf(findNode(next, alpha.id)))).toBe(true);
      expect(
        overlaps(boxOf(findNode(next, outer.group.id)), boxOf(moved)),
      ).toBe(false);
    });

    test('a move that changes nothing, or would close a loop, is no change', () => {
      const { doc, group, alpha, bravo } = grouped();

      expect(setParent(doc, alpha.id, group.id)).toBe(doc);
      expect(setParent(doc, bravo.id, null)).toBe(doc);
      expect(setParent(doc, group.id, group.id)).toBe(doc);
      expect(setParent(doc, bravo.id, alpha.id)).toBe(doc);
      // Ungrouping anything but a group changes nothing either.
      expect(ungroup(doc, alpha.id)).toBe(doc);
    });

    test('a group grows to its members and resizes no smaller than they need', () => {
      const { doc, group, alpha } = grouped();
      const moved = moveNodes(doc, [
        { id: alpha.id, position: { x: 900, y: 700 } },
      ]);
      const fitted = fitGroups(moved, [group.id]);

      expect(
        inside(
          boxOf(findNode(fitted, group.id)),
          boxOf(findNode(fitted, alpha.id)),
        ),
      ).toBe(true);
      expect(fitGroups(fitted, [group.id])).toBe(fitted);

      const least = groupMinimumSize(fitted, group.id);
      const box = boxOf(findNode(fitted, group.id));
      const member = boxOf(findNode(fitted, alpha.id));
      expect(box.x + least.width).toBeGreaterThanOrEqual(
        member.x + member.width,
      );
      expect(box.y + least.height).toBeGreaterThanOrEqual(
        member.y + member.height,
      );
      expect(least.width).toBeLessThan(box.width);
    });
  });

  // N15: a note is named after its text, not "Note".
  test('a note is named after its first line', () => {
    const doc = createDocument();
    const blank = addNode(doc, { kind: 'note' }).node;
    const written = addNode(doc, {
      kind: 'note',
      text: '\n  Firewall   rules\npending review',
    }).node;
    const renamed = addNode(doc, {
      kind: 'note',
      label: 'Todo',
      text: 'x',
    }).node;

    expect(blank.label).toBe('');
    expect(nodeLabel(blank)).toBe('Note');
    expect(nodeLabel(written)).toBe('Firewall rules');
    expect(nodeLabel(renamed)).toBe('Todo');
    // Notes once were all labelled "Note".
    expect(nodeLabel({ ...written, label: 'Note' })).toBe('Firewall rules');
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
