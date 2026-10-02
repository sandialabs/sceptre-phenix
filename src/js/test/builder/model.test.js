import { readFileSync } from 'node:fs';

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
  deviceTypeLabel,
  documentSummary,
  edgeEndpoints,
  findEdge,
  findNode,
  fitGroups,
  groupMinimumSize,
  groupNodes,
  LOOK_KEYS,
  lookOf,
  moveNodes,
  networkOfSwitch,
  nextInterfaceName,
  nodeComment,
  nodeLabel,
  removeElements,
  removeInterface,
  removeNetworks,
  renameInterface,
  sameButStamp,
  savedStamp,
  scenarioApps,
  SCHEMA_REVISION,
  SCHEMA_URI,
  setParent,
  setScenario,
  setViewport,
  sizeOf,
  sourceAnnotations,
  specInterfaceFor,
  specInterfaces,
  STAMP_KEYS,
  storedScenarioName,
  syncInterfaceVLANs,
  ungroup,
  updateEdge,
  updateNetwork,
  updateNode,
  validateConnection,
  withStamp,
} from '@/builder/model.js';
import { copySelection, pasteClipboard } from '@/builder/clipboard.js';
import { BUILTIN_TEMPLATES } from '@/builder/catalog.js';
import { nodeOptionsFromTemplate } from '@/builder/templates.js';
import { validateDocument } from '@/builder/validate.js';

import { sampleDocument } from './fixtures.js';
import { ICON_DATA, ICON_KEY } from './png.js';

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
  // named after them must say both: the Router runs minirouter and the
  // Firewall VyOS, each on the image named after it.
  test.each([
    ['server', 'VirtualMachine', 'linux', 'ubuntu.qc2'],
    ['workstation', 'VirtualMachine', 'windows', 'windows10.qc2'],
    ['router', 'Router', 'minirouter', 'minirouter.qc2'],
    ['firewall', 'Firewall', 'vyos', 'vyos.qc2'],
    ['external', 'HIL', undefined, undefined],
  ])(
    'the %s template adds a device of type %s on %s from %s',
    (id, type, osType, image) => {
      const template = BUILTIN_TEMPLATES.find((entry) => entry.id === id);
      const doc = createDocument();
      const { node } = addNode(doc, nodeOptionsFromTemplate(template, doc));

      expect(node.device.hostname).toBe(id);

      expect(node.device.spec.type).toBe(type);
      expect(node.device.spec.hardware?.os_type).toBe(osType);
      expect(node.device.spec.hardware?.drives?.[0]?.image).toBe(image);
    },
  );

  // A template's description says what the template is, in the palette. It
  // is not the node's description.
  test('a template writes no description into its device', () => {
    for (const template of BUILTIN_TEMPLATES) {
      const doc = createDocument();
      const { node } = addNode(doc, nodeOptionsFromTemplate(template, doc));

      expect(template.description, template.id).toBeTruthy();
      expect(node.device.spec.general.description, template.id).toBe('');
      expect(nodeComment(node), template.id).toBe('');
    }

    // Not from an option of that name either.
    const { node } = addNode(createDocument(), {
      kind: 'device',
      description: 'A description',
    });

    expect(node.device.spec.general.description).toBe('');
  });

  // A device is made from a template through its spec and look alone: a
  // `template` option, which addNode once read, is nothing to it.
  test('addNode reads no template option', () => {
    const [, , router] = BUILTIN_TEMPLATES;
    const { node } = addNode(createDocument(), {
      kind: 'device',
      template: router,
    });

    expect(node.device.hostname).toBe('node');
    expect(node.device.spec.type).toBe('VirtualMachine');
    expect(node.device.iconKey).toBe('linux');
  });

  // The server's built-in templates (BuiltinTemplates in types/builder,
  // checked against the same file) are these: same ids, names, tooltips,
  // icons and specs. And a palette entry adds a device with that icon and
  // that spec.
  test('the built-in templates are those of the server, and each makes its device', () => {
    const { templates } = JSON.parse(
      readFileSync(
        new URL(
          '../../../go/types/builder/testdata/builtin-templates.json',
          import.meta.url,
        ),
      ),
    );

    expect(BUILTIN_TEMPLATES).toEqual(templates);
    expect(
      BUILTIN_TEMPLATES.map((template) => {
        const doc = createDocument();
        const { node } = addNode(doc, nodeOptionsFromTemplate(template, doc));

        return {
          id: template.id,
          name: template.name,
          description: template.description,
          device: { iconKey: node.device.iconKey, spec: node.device.spec },
        };
      }),
    ).toEqual(templates);
  });

  // They are shared by every diagram, so nothing may change one.
  test('a built-in template cannot be changed', () => {
    const [server] = BUILTIN_TEMPLATES;

    expect(Object.isFrozen(BUILTIN_TEMPLATES)).toBe(true);
    expect(Object.isFrozen(server.device.spec.hardware.drives[0])).toBe(true);
    expect(() => {
      server.device.spec.general.hostname = 'changed';
    }).toThrow(TypeError);

    // A device made from one is a copy, free to change.
    const doc = createDocument();
    const { node } = addNode(doc, nodeOptionsFromTemplate(server, doc));

    node.device.spec.general.description = 'mine';
    expect(server.device.spec.general.description).toBe('');
  });

  test('the type table above covers every template', () => {
    expect(BUILTIN_TEMPLATES.map((template) => template.id)).toEqual([
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

  // What a device shows on the canvas in place of the word "Device".
  test('a device is labelled with its node type as stored, or External', () => {
    const device = (spec) => ({ kind: 'device', device: { spec } });
    const types = Object.fromEntries(
      BUILTIN_TEMPLATES.map((template) => [
        template.id,
        deviceTypeLabel(
          addNode(
            createDocument(),
            nodeOptionsFromTemplate(template, createDocument()),
          ).node,
        ),
      ]),
    );

    expect(types).toEqual({
      server: 'VirtualMachine',
      workstation: 'VirtualMachine',
      router: 'Router',
      firewall: 'Firewall',
      external: 'External',
    });
    expect(deviceTypeLabel(device({ type: 'SCEPTRE' }))).toBe('SCEPTRE');
    expect(deviceTypeLabel(device({ type: ' Router ' }))).toBe('Router');
    // An external device says so whatever its type, or without one.
    expect(deviceTypeLabel(device({ external: true }))).toBe('External');
    expect(deviceTypeLabel(device({ external: true, type: 'Router' }))).toBe(
      'External',
    );
    // Only true is external: a spec from an experiment holds null.
    expect(deviceTypeLabel(device({ external: null, type: 'Router' }))).toBe(
      'Router',
    );
    for (const spec of [{}, { type: '' }, { type: '  ' }, { type: 7 }]) {
      expect(deviceTypeLabel(device(spec)), JSON.stringify(spec)).toBe(
        'Device',
      );
    }
    expect(deviceTypeLabel({ kind: 'device' })).toBe('Device');
    expect(deviceTypeLabel(undefined)).toBe('Device');
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

  // The switch is named after its network wherever a node is named.
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

  // A spec is free-form: one whose interfaces are no list has none.
  test('a device whose spec interfaces are no list is edited like one without interfaces', () => {
    const { doc, bravo } = sampleDocument();
    const odd = updateNode(doc, bravo.id, {
      device: {
        interfaces: [],
        spec: { ...bravo.device.spec, network: { interfaces: 'x' } },
      },
    });
    const renamed = updateNode(odd, bravo.id, {
      device: { hostname: 'charlie' },
    });
    const node = renamed.nodes.find((entry) => entry.id === bravo.id);

    expect(node.device.hostname).toBe('charlie');
    expect(deviceHandles(node)).toEqual([]);
    expect(node.device.spec.network.interfaces).toBe('x');
  });

  // An import keeps an entry of the list that is no object as it found it.
  test('entries of the spec interfaces that are no objects are passed over', () => {
    const { doc, alpha } = sampleDocument();
    const [handle] = alpha.device.interfaces;
    const entry = alpha.device.spec.network.interfaces[0];
    const odd = {
      ...doc,
      nodes: doc.nodes.map((node) =>
        node.id === alpha.id
          ? {
              ...node,
              device: {
                ...node.device,
                spec: {
                  ...node.device.spec,
                  network: { interfaces: [null, 'x', entry] },
                },
              },
            }
          : node,
      ),
    };
    const node = (document) =>
      document.nodes.find((candidate) => candidate.id === alpha.id);

    expect(specInterfaceFor(node(odd), handle.id)).toEqual(entry);

    const renamed = renameInterface(odd, alpha.id, handle.id, 'mgmt0');

    expect(node(renamed).device.spec.network.interfaces).toEqual([
      null,
      'x',
      { ...entry, name: 'mgmt0' },
    ]);

    const removed = removeInterface(odd, alpha.id, handle.id);

    expect(node(removed).device.spec.network.interfaces).toEqual([null, 'x']);
    expect(deviceHandles(node(removed))).toEqual([]);
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
    // The copied connection uses the copy's interface: none is added for it.
    const connected = both.doc.nodes.find(
      (node) => both.nodeIds.includes(node.id) && node.kind === 'device',
    );
    expect(deviceHandles(connected).map((handle) => handle.name)).toEqual([
      'eth0',
    ]);
    expect(specInterfaces(connected).map((iface) => iface.name)).toEqual([
      'eth0',
    ]);
  });

  // A copy is named after its own hostname, and pasting again
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
    // minimega keeps such VLANs apart.
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

describe('routes a layout drew', () => {
  test('a connection keeps its route only while its ends stay where they were', () => {
    const { doc, sw, alpha, bravo, edge } = sampleDocument();
    const route = [
      { x: 160, y: 48 },
      { x: 180, y: 48 },
      { x: 180, y: 36 },
      { x: 200, y: 36 },
    ];
    const withRoute = (next) => ({
      ...next,
      edges: next.edges.map((entry) => ({ ...entry, route })),
    });
    const routed = withRoute(doc);
    const routeOf = (next) => findEdge(next, edge.id)?.route;
    const alphaAt = findNode(routed, alpha.id).position;

    // Edits that leave its ends where they were keep it.
    for (const next of [
      moveNodes(routed, [{ id: bravo.id, position: { x: 0, y: 400 } }]),
      moveNodes(routed, [{ id: alpha.id, position: { ...alphaAt } }]),
      updateNode(routed, alpha.id, { device: { hostname: 'alpha-2' } }),
      updateEdge(routed, edge.id, { label: 'uplink' }),
    ]) {
      expect(routeOf(next)).toBe(route);
    }

    // An end moved, resized, put in a group or given another interface,
    // whose handles then move down its side, drops it.
    for (const next of [
      moveNodes(routed, [{ id: alpha.id, position: { x: 16, y: 0 } }]),
      moveNodes(routed, [{ id: sw.id, position: { x: 200, y: 16 } }]),
      updateNode(routed, sw.id, { size: { width: 240, height: 72 } }),
      groupNodes(routed, [alpha.id]).doc,
      addInterface(routed, alpha.id).doc,
    ]) {
      expect(findEdge(next, edge.id)).toBeDefined();
      expect(routeOf(next)).toBeUndefined();
    }

    // A pasted copy of a routed connection is drawn from its new place.
    const pasted = pasteClipboard(
      routed,
      copySelection(routed, { nodes: [alpha.id, sw.id] }),
    ).doc;
    const copies = pasted.edges.filter(
      (entry) => !routed.edges.some((old) => old.id === entry.id),
    );

    expect(copies).toHaveLength(1);
    expect(copies[0].route).toBeUndefined();

    // So does its group moving it, or its leaving the group.
    const grouped = groupNodes(doc, [alpha.id]);
    const inGroup = withRoute(grouped.doc);
    const group = findNode(inGroup, grouped.group.id);

    for (const next of [
      moveNodes(inGroup, [
        {
          id: group.id,
          position: { x: group.position.x + 32, y: group.position.y },
        },
      ]),
      ungroup(inGroup, group.id),
      setParent(inGroup, alpha.id, null),
    ]) {
      expect(routeOf(next)).toBeUndefined();
    }
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

    // Each new group has a title of its own.
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

  // A node moves into a group, between groups and out of one, and its
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

  // A note is named after its text, not "Note".
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

  test('the apps of a scenario are listed in its order, with their hosts', () => {
    expect(
      scenarioApps({
        apps: [
          {
            name: 'ntp',
            hosts: [
              { hostname: 'router', metadata: { server: true } },
              { metadata: {} },
              { hostname: 'host-b' },
            ],
          },
          { name: 'soh', disabled: true },
          { assetDir: '/phenix/assets' },
          'vrouter',
        ],
      }),
    ).toEqual([
      { name: 'ntp', hosts: ['router', 'host-b'], disabled: false },
      { name: 'soh', hosts: [], disabled: true },
    ]);
    expect(scenarioApps(undefined)).toEqual([]);
    expect(scenarioApps({ apps: null })).toEqual([]);
  });

  test('only a stored scenario without content is read from its config', () => {
    const stored = { kind: 'stored', name: 'plant', digest: 'sha256:1' };

    expect(storedScenarioName(stored)).toBe('plant');
    expect(storedScenarioName({ ...stored, content: { apps: [] } })).toBe('');
    expect(
      storedScenarioName({ kind: 'uploaded', name: 'plant', content: {} }),
    ).toBe('');
    expect(storedScenarioName(undefined)).toBe('');
  });
});

describe('source annotations', () => {
  test("are sorted by key, without the Builders' own or values that are not text", () => {
    const doc = createDocument();

    doc.source = {
      kind: 'experiment',
      name: 'exp',
      annotations: {
        topology: 'core',
        'builder-xml': '<mxGraphModel/>',
        'builder-experiment': '{}',
        scenario: 'ntp',
        count: 3,
        Owner: 'alice',
      },
    };

    expect(sourceAnnotations(doc)).toEqual([
      ['Owner', 'alice'],
      ['scenario', 'ntp'],
      ['topology', 'core'],
    ]);
    expect(sourceAnnotations(createDocument())).toEqual([]);
    expect(sourceAnnotations({ source: { annotations: null } })).toEqual([]);
  });
});

// The author, creation time, last editor and last edit time the server
// writes into every document it stores (stampedSnapshot in api/builder).
describe('the stamp of a stored document', () => {
  const stamp = {
    author: 'alice',
    createdAt: '2026-10-01T15:04:05Z',
    updatedBy: 'bob',
    updatedAt: '2026-10-01T16:10:00Z',
  };

  test('is set after the description, as the server encodes it', () => {
    const doc = createDocument({ name: 'Lab', description: 'A lab' });
    const stamped = withStamp(doc, stamp);

    expect(stamped).toMatchObject(stamp);
    expect(Object.keys(stamped).slice(0, 10)).toEqual([
      '$schema',
      'revision',
      'id',
      'name',
      'description',
      'author',
      'createdAt',
      'updatedBy',
      'updatedAt',
      'nodes',
    ]);
    // A new document: the one given keeps what it had.
    expect(stamped).not.toBe(doc);
    expect(doc.author).toBeUndefined();
    expect(stamped.nodes).toBe(doc.nodes);
  });

  test('follows the last header key a document without a description has', () => {
    const { description, ...bare } = createDocument({ name: 'Lab' });
    const { name, ...nameless } = bare;

    expect(description).toBe('');
    expect(name).toBe('Lab');
    expect(Object.keys(withStamp(bare, stamp)).slice(3, 9)).toEqual([
      'name',
      ...STAMP_KEYS,
      'nodes',
    ]);
    expect(Object.keys(withStamp(nameless, stamp)).slice(2, 8)).toEqual([
      'id',
      ...STAMP_KEYS,
      'nodes',
    ]);
  });

  test('replaces every value: one the stamp lacks is removed', () => {
    const doc = withStamp(createDocument(), stamp);
    const saved = withStamp(doc, {
      updatedBy: 'carol',
      updatedAt: '2026-10-02T08:00:00Z',
    });

    expect(saved.updatedBy).toBe('carol');
    expect(saved.updatedAt).toBe('2026-10-02T08:00:00Z');
    expect('author' in saved).toBe(false);
    expect('createdAt' in saved).toBe(false);

    // An empty stamp is that of a document that names no one. Empty text
    // and values that are not text are none.
    const cleared = withStamp(doc, {});

    expect(STAMP_KEYS.some((key) => key in cleared)).toBe(false);
    expect(
      Object.keys(
        withStamp(doc, { author: '', createdAt: null, updatedBy: 7 }),
      ),
    ).toEqual(Object.keys(createDocument()));
  });

  test('moves keys that are out of place, and keeps a document that holds it already', () => {
    const doc = withStamp(createDocument(), stamp);
    const { author, ...rest } = doc;
    const misplaced = { ...rest, author };

    expect(Object.keys(misplaced).at(-1)).toBe('author');
    expect(Object.keys(withStamp(misplaced, stamp))).toEqual(Object.keys(doc));
    expect(withStamp(doc, { ...stamp })).toBe(doc);
    expect(withStamp(createDocument(), {})).not.toHaveProperty('author');

    const plain = createDocument();

    expect(withStamp(plain, {})).toBe(plain);
    expect(withStamp(plain, undefined)).toBe(plain);
  });

  test('a listed snapshot gives the stamp its save wrote', () => {
    const doc = withStamp(createDocument(), stamp);

    // The server lists times with a fraction of a second; the document
    // holds the second it falls in.
    expect(
      savedStamp(doc, {
        createdBy: 'dana',
        createdAt: '2026-10-03T09:30:15.987654321Z',
      }),
    ).toEqual({
      author: 'alice',
      createdAt: '2026-10-01T15:04:05Z',
      updatedBy: 'dana',
      updatedAt: '2026-10-03T09:30:15Z',
    });
    expect(
      savedStamp(doc, { createdBy: 'dana', createdAt: '2026-10-03T09:30:15Z' })
        .updatedAt,
    ).toBe('2026-10-03T09:30:15Z');
    // A time with an offset is the same moment in UTC.
    expect(
      savedStamp(doc, {
        createdBy: 'dana',
        createdAt: '2026-10-03T03:30:15.5-06:00',
      }).updatedAt,
    ).toBe('2026-10-03T09:30:15Z');
    // Nothing is made up for a snapshot that does not say.
    expect(savedStamp(createDocument(), {})).toEqual({
      author: undefined,
      createdAt: undefined,
      updatedBy: undefined,
      updatedAt: '',
    });
    expect(savedStamp(doc, { createdAt: 'yesterday' }).updatedAt).toBe('');
    expect(
      withStamp(createDocument(), savedStamp(createDocument(), undefined)),
    ).not.toHaveProperty('updatedAt');
  });

  test('two documents that differ only in it hold the same content', () => {
    const doc = createDocument();
    const stamped = withStamp(doc, stamp);

    expect(sameButStamp(doc, stamped)).toBe(true);
    expect(sameButStamp(stamped, withStamp(stamped, {}))).toBe(true);
    expect(sameButStamp(doc, doc)).toBe(true);
    // Any other change is content, a pan included.
    expect(
      sameButStamp(doc, setViewport(stamped, { x: 4, y: 0, zoom: 1 })),
    ).toBe(false);
    expect(sameButStamp(doc, { ...stamped, name: 'Other' })).toBe(false);
    expect(sameButStamp(doc, { ...stamped, layout: 'elk' })).toBe(false);
    expect(sameButStamp(doc, null)).toBe(false);
  });
});

// The presentation fields of nodes, networks and connections: none is set
// unless asked for, and one emptied again leaves no key behind, so a
// document that uses none keeps its bytes.
describe('colors, line styles and group fields', () => {
  const errorsOf = (doc) =>
    validateDocument(doc).filter((issue) => issue.level !== 'warning');

  test('a new node, network or connection has none of them', () => {
    const { doc, alpha, sw, network, edge } = sampleDocument();
    const group = addNode(doc, { kind: 'group', title: 'Core' }).node;

    expect(Object.keys(findNode(doc, alpha.id).device)).toEqual([
      'hostname',
      'iconKey',
      'spec',
      'interfaces',
    ]);
    expect(findNode(doc, sw.id).switch).toEqual({ networkId: network.id });
    expect(group.group).toEqual({ title: 'Core', color: '', collapsed: false });
    expect(Object.keys(network)).not.toContain('lineStyle');
    expect(Object.keys(edge)).not.toContain('lineStyle');
  });

  test("a new device takes its look from addNode's look option", () => {
    const { doc } = sampleDocument();
    const look = {
      iconKey: 'router',
      icon: '',
      outlineColor: '#2f6fbf',
      fillColor: '#1f7a5a',
    };
    const added = addNode(doc, { kind: 'device', hostname: 'edge', look });

    expect(lookOf(added.node.device)).toEqual(look);
    expect(LOOK_KEYS).toEqual(['iconKey', 'icon', 'outlineColor', 'fillColor']);
    expect(errorsOf(added.doc)).toEqual([]);

    // An icon key that is none of the registry's is picked from the spec,
    // as the iconKey option's is, and wins over that option.
    expect(
      addNode(doc, {
        kind: 'device',
        iconKey: 'firewall',
        look: { iconKey: 'router' },
      }).node.device.iconKey,
    ).toBe('router');
    expect(
      addNode(doc, { kind: 'device', look: { iconKey: 'https://x/y.png' } })
        .node.device.iconKey,
    ).toBe('linux');
    // An empty color is not written, nor is no custom icon.
    expect(
      Object.keys(
        addNode(doc, {
          kind: 'device',
          look: { icon: '', outlineColor: '', fillColor: undefined },
        }).node.device,
      ),
    ).toEqual(['hostname', 'iconKey', 'spec', 'interfaces']);
  });

  // The document comes to carry the icon when the edit is committed (see
  // settleIcons): the writers only name it.
  test('a custom icon is named by a device and by a group, and emptied away', () => {
    const { doc, alpha } = sampleDocument();
    const device = (document) => findNode(document, alpha.id).device;
    const added = addNode(doc, {
      kind: 'device',
      hostname: 'plc',
      look: { iconKey: 'router', icon: ICON_KEY },
    });

    expect(added.node.device).toMatchObject({
      iconKey: 'router',
      icon: ICON_KEY,
    });
    expect('icons' in added.doc).toBe(false);

    const named = updateNode(doc, alpha.id, { device: { icon: ICON_KEY } });

    expect(device(named).icon).toBe(ICON_KEY);
    // A rename, a new icon key or a color keeps it.
    for (const patch of [
      { hostname: 'a2' },
      { iconKey: 'router' },
      { fillColor: '#1f7a5a' },
    ]) {
      expect(device(updateNode(named, alpha.id, { device: patch })).icon).toBe(
        ICON_KEY,
      );
    }
    expect(
      device(updateNode(named, alpha.id, { device: { icon: '' } })),
    ).toEqual(device(doc));

    const group = addNode(doc, {
      kind: 'group',
      title: 'Zone',
      icon: ICON_KEY,
    });

    expect(group.node.group).toEqual({
      title: 'Zone',
      color: '',
      collapsed: false,
      icon: ICON_KEY,
    });
    expect(
      findNode(
        updateNode(group.doc, group.node.id, { group: { title: 'Cell' } }),
        group.node.id,
      ).group.icon,
    ).toBe(ICON_KEY);
    expect(
      findNode(
        updateNode(group.doc, group.node.id, { group: { icon: '' } }),
        group.node.id,
      ).group,
    ).toEqual({ title: 'Zone', color: '', collapsed: false });

    // A document that carries the icon passes the checks; one that names
    // it without carrying it does not.
    const icons = { [ICON_KEY]: { name: 'plc', data: ICON_DATA } };

    expect(errorsOf({ ...named, icons })).toEqual([]);
    expect(errorsOf({ ...group.doc, icons })).toEqual([]);
    expect(errorsOf(named).map((issue) => issue.path)).toEqual([
      `nodes[${named.nodes.indexOf(findNode(named, alpha.id))}].device.icon`,
    ]);
  });

  test("a device's colors are set, kept by other edits, and emptied away", () => {
    const { doc, alpha } = sampleDocument();
    const colored = updateNode(doc, alpha.id, {
      device: { outlineColor: '#2f6fbf', fillColor: '#1f7a5a' },
    });
    const device = (document) => findNode(document, alpha.id).device;

    expect(device(colored)).toMatchObject({
      outlineColor: '#2f6fbf',
      fillColor: '#1f7a5a',
    });
    expect(errorsOf(colored)).toEqual([]);
    // A rename, or a new icon, keeps them.
    expect(
      device(updateNode(colored, alpha.id, { device: { hostname: 'a2' } })),
    ).toMatchObject({ outlineColor: '#2f6fbf', fillColor: '#1f7a5a' });
    expect(
      device(updateNode(colored, alpha.id, { device: { iconKey: 'router' } })),
    ).toMatchObject({ iconKey: 'router', fillColor: '#1f7a5a' });

    const cleared = device(
      updateNode(colored, alpha.id, {
        device: { outlineColor: '', fillColor: '' },
      }),
    );

    expect('outlineColor' in cleared).toBe(false);
    expect('fillColor' in cleared).toBe(false);
    expect(cleared).toEqual(device(doc));
    // The writers store what they are given; the diagram checks refuse a
    // color that is not #rrggbb.
    expect(
      errorsOf(updateNode(doc, alpha.id, { device: { fillColor: 'red' } })).map(
        (issue) => issue.message,
      ),
    ).toEqual(['color "red" must be a hex color such as #2f6fbf']);
  });

  // updateNode once replaced a switch's payload with its network id alone,
  // which would have dropped its colors on every edit.
  test("a switch's colors are set, kept by other edits, and emptied away", () => {
    const { doc, sw, network } = sampleDocument();
    const colored = updateNode(doc, sw.id, {
      switch: { outlineColor: '#a3273f', fillColor: '#6b6f18' },
    });
    const payload = (document) => findNode(document, sw.id).switch;

    expect(payload(colored)).toEqual({
      networkId: network.id,
      outlineColor: '#a3273f',
      fillColor: '#6b6f18',
    });
    expect(errorsOf(colored)).toEqual([]);
    // A move keeps the payload, and so does a patch of one color.
    expect(
      payload(updateNode(colored, sw.id, { position: { x: 1, y: 2 } })),
    ).toEqual(payload(colored));
    expect(
      payload(updateNode(colored, sw.id, { switch: { fillColor: '' } })),
    ).toEqual({ networkId: network.id, outlineColor: '#a3273f' });

    // Bound to another network, it keeps its colors.
    const other = addNetwork(colored, { name: 'MGMT' });
    const moved = updateNode(other.doc, sw.id, {
      switch: { networkId: other.network.id },
    });

    expect(payload(moved)).toEqual({
      networkId: other.network.id,
      outlineColor: '#a3273f',
      fillColor: '#6b6f18',
    });
    expect(findNode(moved, sw.id).label).toBe('MGMT');
    // A new switch is given its colors, or none.
    expect(
      addNode(doc, {
        kind: 'switch',
        networkId: network.id,
        outlineColor: '#111111',
        fillColor: '#eeeeee',
      }).node.switch,
    ).toEqual({
      networkId: network.id,
      outlineColor: '#111111',
      fillColor: '#eeeeee',
    });
  });

  test("a network's line style is set by addNetwork and updateNetwork, and '' removes it", () => {
    const { doc, network } = sampleDocument();
    const styled = updateNetwork(doc, network.id, { lineStyle: 'dotted' });
    const found = (document) =>
      document.networks.find((entry) => entry.id === network.id);

    expect(found(styled).lineStyle).toBe('dotted');
    expect(errorsOf(styled)).toEqual([]);
    // Another edit keeps it.
    expect(
      found(updateNetwork(styled, network.id, { description: 'core' }))
        .lineStyle,
    ).toBe('dotted');
    expect(
      'lineStyle' in
        found(updateNetwork(styled, network.id, { lineStyle: '' })),
    ).toBe(false);
    expect(
      addNetwork(doc, { name: 'MGMT', lineStyle: 'dash-dot' }).network
        .lineStyle,
    ).toBe('dash-dot');
    expect('lineStyle' in addNetwork(doc, { name: 'MGMT' }).network).toBe(
      false,
    );
  });

  test("a connection's line style is set by connect and updateEdge, and '' removes it", () => {
    const { doc, edge, bravo, sw } = sampleDocument();
    const styled = updateEdge(doc, edge.id, { lineStyle: 'dashed' });

    expect(findEdge(styled, edge.id).lineStyle).toBe('dashed');
    expect(errorsOf(styled)).toEqual([]);
    // A new label keeps it.
    expect(
      findEdge(updateEdge(styled, edge.id, { label: 'uplink' }), edge.id)
        .lineStyle,
    ).toBe('dashed');
    expect(
      'lineStyle' in
        findEdge(updateEdge(styled, edge.id, { lineStyle: '' }), edge.id),
    ).toBe(false);

    const connected = connect(doc, {
      sourceNodeId: bravo.id,
      targetNodeId: sw.id,
      lineStyle: 'dotted',
    });

    expect(connected.edge.lineStyle).toBe('dotted');
    expect(errorsOf(connected.doc)).toEqual([]);
  });

  test("a group's description, border pattern and icon are set, and emptied away", () => {
    const { doc } = sampleDocument();
    const plain = addNode(doc, { kind: 'group', title: 'Core' });
    const described = updateNode(plain.doc, plain.node.id, {
      group: {
        description: 'DMZ hosts',
        borderStyle: 'double',
        iconKey: 'firewall',
      },
    });
    const payload = (document) => findNode(document, plain.node.id).group;

    expect(payload(described)).toEqual({
      title: 'Core',
      color: '',
      collapsed: false,
      description: 'DMZ hosts',
      borderStyle: 'double',
      iconKey: 'firewall',
    });
    expect(errorsOf(described)).toEqual([]);
    // Its description is its comment, which its accessible name ends with.
    expect(nodeComment(findNode(described, plain.node.id))).toBe('DMZ hosts');
    expect(nodeComment(plain.node)).toBe('');
    // A new title keeps them.
    expect(
      payload(
        updateNode(described, plain.node.id, { group: { title: 'Edge' } }),
      ),
    ).toMatchObject({ title: 'Edge', description: 'DMZ hosts' });
    expect(
      payload(
        updateNode(described, plain.node.id, {
          group: { description: '', borderStyle: '', iconKey: '' },
        }),
      ),
    ).toEqual(plain.node.group);

    // A new group is given them, or none; grouping nodes gives none.
    expect(
      addNode(doc, {
        kind: 'group',
        title: 'Zone',
        description: 'Level 2',
        borderStyle: 'solid',
        iconKey: 'vlan',
      }).node.group,
    ).toEqual({
      title: 'Zone',
      color: '',
      collapsed: false,
      description: 'Level 2',
      borderStyle: 'solid',
      iconKey: 'vlan',
    });
    expect(
      groupNodes(doc, [doc.nodes[0].id], { title: 'Made' }).group.group,
    ).toEqual({ title: 'Made', color: '', collapsed: false });
  });

  // A paste into an empty document carries every one of them: the copy is
  // self contained.
  test('a copy carries colors, line styles and group fields into another document', () => {
    const { doc, alpha, sw, network, edge } = sampleDocument();
    const grouped = groupNodes(doc, [alpha.id, sw.id], { title: 'Core' });
    let source = updateNode(grouped.doc, grouped.group.id, {
      group: { description: 'DMZ', borderStyle: 'dotted', iconKey: 'vlan' },
    });

    source = updateNode(source, alpha.id, {
      device: { outlineColor: '#2f6fbf', fillColor: '#1f7a5a' },
    });
    source = updateNode(source, sw.id, {
      switch: { outlineColor: '#a3273f', fillColor: '#6b6f18' },
    });
    source = updateNetwork(source, network.id, { lineStyle: 'dash-dot' });
    source = updateEdge(source, edge.id, { lineStyle: 'dotted' });

    const pasted = pasteClipboard(
      createDocument({ name: 'Other' }),
      copySelection(source, { nodes: [grouped.group.id] }),
    ).doc;
    const byKind = (kind) => pasted.nodes.find((node) => node.kind === kind);

    expect(byKind('device').device).toMatchObject({
      iconKey: 'linux',
      outlineColor: '#2f6fbf',
      fillColor: '#1f7a5a',
    });
    expect(byKind('switch').switch).toEqual({
      networkId: pasted.networks[0].id,
      outlineColor: '#a3273f',
      fillColor: '#6b6f18',
    });
    expect(byKind('group').group).toMatchObject({
      title: 'Core',
      description: 'DMZ',
      borderStyle: 'dotted',
      iconKey: 'vlan',
    });
    expect(pasted.networks[0]).toMatchObject({
      name: 'EXP',
      lineStyle: 'dash-dot',
    });
    expect(pasted.edges).toHaveLength(1);
    expect(pasted.edges[0].lineStyle).toBe('dotted');
    expect(errorsOf(pasted)).toEqual([]);

    // A copy of nodes that have none of them has none either.
    const plain = pasteClipboard(
      createDocument({ name: 'Other' }),
      copySelection(grouped.doc, { nodes: [grouped.group.id] }),
    ).doc;

    expect(
      Object.keys(plain.nodes.find((node) => node.kind === 'device').device),
    ).toEqual(['hostname', 'iconKey', 'spec', 'interfaces']);
    expect(plain.nodes.find((node) => node.kind === 'switch').switch).toEqual({
      networkId: plain.networks[0].id,
    });
    expect(
      Object.keys(plain.nodes.find((node) => node.kind === 'group').group),
    ).toEqual(['title', 'color', 'collapsed']);
    expect('lineStyle' in plain.networks[0]).toBe(false);
    expect('lineStyle' in plain.edges[0]).toBe(false);
  });

  // The copy carries the icons its nodes use, and the paste puts them into
  // the document: it is valid on its own, before any commit.
  test('a copy carries the custom icons its nodes use, and no others', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const other =
      'sha256:0000000000000000000000000000000000000000000000000000000000000000';
    const group = addNode(doc, {
      kind: 'group',
      title: 'Zone',
      icon: ICON_KEY,
    });
    const source = {
      ...updateNode(
        updateNode(group.doc, alpha.id, { device: { icon: ICON_KEY } }),
        bravo.id,
        { device: { icon: other } },
      ),
      icons: {
        [ICON_KEY]: { name: 'plc', data: ICON_DATA },
        [other]: { data: 'AAAA' },
      },
    };
    const copied = copySelection(source, {
      nodes: [alpha.id, group.node.id],
    });

    expect(copied.icons).toEqual({
      [ICON_KEY]: { name: 'plc', data: ICON_DATA },
    });
    // The copy is its own: a later change of the document is not in it.
    expect(copied.icons[ICON_KEY]).not.toBe(source.icons[ICON_KEY]);
    expect(copySelection(doc, { nodes: [alpha.id] }).icons).toEqual({});

    const pasted = pasteClipboard(createDocument({ name: 'Other' }), copied);

    expect(pasted.dropped).toBe(0);
    expect(pasted.doc.icons).toEqual({
      [ICON_KEY]: { name: 'plc', data: ICON_DATA },
    });
    expect(
      pasted.doc.nodes.map((node) => (node.device || node.group).icon),
    ).toEqual([ICON_KEY, ICON_KEY]);
    expect(errorsOf(pasted.doc)).toEqual([]);

    // A payload without the icon (one made before the icon was there, or
    // by hand) pastes the node with its built-in icon, and says so.
    const bare = pasteClipboard(createDocument({ name: 'Other' }), {
      ...copied,
      icons: undefined,
    });

    expect(bare.dropped).toBe(1);
    expect('icons' in bare.doc).toBe(false);
    expect(
      bare.doc.nodes.map((node) => (node.device || node.group).icon),
    ).toEqual([undefined, undefined]);
    expect(errorsOf(bare.doc)).toEqual([]);
    // Nothing to paste leaves nothing out.
    expect(pasteClipboard(doc, { nodes: [] })).toEqual({
      doc,
      nodeIds: [],
      dropped: 0,
    });
  });
});
