import { describe, expect, test } from 'vitest';

import {
  NOTE_PLACEHOLDER,
  connectionList,
  diagramCounts,
  labelIndex,
  nodeAccessibleName,
} from '@/builder/diagramText.js';
import {
  addNetwork,
  addNode,
  connect,
  createDocument,
  findNode,
  groupNodes,
  placeholderLayers,
  setLayers,
  setNodeLayers,
  updateNode,
} from '@/builder/model.js';
import { BUILTIN_TEMPLATES } from '@/builder/catalog.js';
import { nodeOptionsFromTemplate } from '@/builder/templates.js';

import { sampleDocument } from './fixtures.js';

describe('the diagram in words', () => {
  test('accessible names name the network, not just a colour', () => {
    const { doc, sw, alpha, bravo } = sampleDocument();
    const switchNode = doc.nodes.find((node) => node.id === sw.id);
    const device = doc.nodes.find((node) => node.id === alpha.id);

    expect(nodeAccessibleName(doc, switchNode)).toContain('network EXP');
    expect(nodeAccessibleName(doc, switchNode)).toContain('VLAN alias 100');
    expect(nodeAccessibleName(doc, device)).toBe(
      'Device alpha, VirtualMachine, 1 interface, 1 connection, on EXP',
    );

    // A device names each of its networks once, sorted by name. One with
    // no connection names none.
    const added = addNetwork(doc, { name: 'DMZ' });
    const other = addNode(added.doc, {
      kind: 'switch',
      networkId: added.network.id,
      position: { x: 400, y: 0 },
    });
    let next = connect(other.doc, {
      sourceNodeId: alpha.id,
      sourceHandleId: null,
      targetNodeId: other.node.id,
    }).doc;
    next = connect(next, {
      sourceNodeId: alpha.id,
      sourceHandleId: null,
      targetNodeId: sw.id,
    }).doc;

    expect(nodeAccessibleName(next, findNode(next, alpha.id))).toBe(
      'Device alpha, VirtualMachine, 3 interfaces, 3 connections, on DMZ and EXP',
    );
    expect(nodeAccessibleName(next, findNode(next, bravo.id))).toBe(
      'Device bravo, VirtualMachine, 1 interface, 0 connections',
    );

    // Looked up once for every node, the names are the same.
    const index = labelIndex(next);

    for (const node of next.nodes) {
      expect(nodeAccessibleName(next, node, index)).toBe(
        nodeAccessibleName(next, node),
      );
    }
  });

  test('the comment follows the text a device shows, as the canvas draws it', () => {
    const { doc, alpha } = sampleDocument();
    const node = doc.nodes.find((entry) => entry.id === alpha.id);

    node.device.spec.general.description = 'jump host';

    expect(nodeAccessibleName(doc, node)).toBe(
      'Device alpha, VirtualMachine, 1 interface, jump host, 1 connection, on EXP',
    );
  });

  test('names say which group a node is in and how many members a group has', () => {
    const { doc, alpha, bravo, sw } = sampleDocument();
    const grouped = groupNodes(doc, [alpha.id, bravo.id]).doc;
    const node = (id) => grouped.nodes.find((entry) => entry.id === id);
    const group = grouped.nodes.find((entry) => entry.kind === 'group');

    // A label that only repeats the kind is not said twice.
    expect(nodeAccessibleName(grouped, group)).toBe('Group, 2 members');
    expect(nodeAccessibleName(grouped, node(alpha.id))).toBe(
      'Device alpha, VirtualMachine, 1 interface, in Group, 1 connection, on EXP',
    );
    expect(nodeAccessibleName(grouped, node(sw.id))).not.toContain(' in ');

    const titled = { ...group, label: 'DMZ', group: { title: 'DMZ' } };
    const renamed = {
      ...grouped,
      nodes: grouped.nodes.map((entry) =>
        entry.id === group.id ? titled : entry,
      ),
    };
    expect(nodeAccessibleName(renamed, titled)).toBe('Group DMZ, 2 members');
    expect(nodeAccessibleName(renamed, node(bravo.id))).toContain(
      'in Group DMZ',
    );
  });

  // A group's description is its comment, which the canvas draws below its
  // title. Its colors and border are not said.
  test("a group's description follows its title", () => {
    const { doc, alpha } = sampleDocument();
    const grouped = groupNodes(doc, [alpha.id], { title: 'DMZ' });
    const described = updateNode(grouped.doc, grouped.group.id, {
      group: {
        description: 'Public servers',
        borderStyle: 'double',
        iconKey: 'firewall',
      },
    });
    const group = findNode(described, grouped.group.id);

    expect(nodeAccessibleName(described, group)).toBe(
      'Group DMZ, Public servers, 1 member',
    );

    // A device's or a switch's colors change no name.
    const colored = updateNode(described, alpha.id, {
      device: { outlineColor: '#2f6fbf', fillColor: '#ffd400' },
    });

    expect(nodeAccessibleName(colored, findNode(colored, alpha.id))).toBe(
      nodeAccessibleName(described, findNode(described, alpha.id)),
    );
  });

  test('a note is named by its heading and all of its text', () => {
    const { doc } = sampleDocument();
    const text = `Firewall rules pending review ${'x'.repeat(100)}`;
    const { node: note } = addNode(doc, { kind: 'note', text });
    const { node: blank } = addNode(doc, { kind: 'note' });

    expect(nodeAccessibleName(doc, note)).toBe(`Note: ${text}`);
    expect(nodeAccessibleName(doc, { ...note, label: 'Firewall' })).toBe(
      `Note Firewall: ${text}`,
    );
    expect(nodeAccessibleName(doc, blank)).toBe(`Note: ${NOTE_PLACEHOLDER}`);
  });

  // The canvas shows a device's type, so its name says it. The icon's word
  // follows the shown text, unless the label or the type says it.
  test.each([
    ['dc-01', 'windows', {}, 'Device, 0 interfaces, Windows'],
    [
      'web',
      'linux',
      { type: 'VirtualMachine' },
      'VirtualMachine, 0 interfaces',
    ],
    ['edge-1', 'linux', { type: 'Router' }, 'Router, 0 interfaces'],
    // As stored, not as the icon's word is written.
    ['plc-1', 'server', { type: 'SCEPTRE' }, 'SCEPTRE, 0 interfaces'],
    ['fw-1', 'router', { type: 'Firewall' }, 'Firewall, 0 interfaces, Router'],
    ['edge-1', 'router', { type: 'Router' }, 'Router, 0 interfaces'],
    [
      'plc-1',
      'external',
      { external: true, type: 'HIL' },
      'External, 0 interfaces',
    ],
    ['core-router', 'router', {}, 'Device, 0 interfaces'],
    ['router2', 'router', {}, 'Device, 0 interfaces'],
    ['unit-7', 'plc', {}, 'Device, 0 interfaces, PLC'],
    ['ops-phone', 'smartphone', {}, 'Device, 0 interfaces'],
    ['attacker', 'blackhat', {}, 'Device, 0 interfaces, Black Hat'],
    ['scan', 'kali', {}, 'Device, 0 interfaces, Kali Linux'],
    // Only whole words count: "shredder" merely contains "red".
    ['shredder', 'redhat', {}, 'Device, 0 interfaces, Red Hat'],
    ['rhel9', 'redhat', {}, 'Device, 0 interfaces'],
  ])('device %s with the %s icon and %o', (hostname, iconKey, spec, shown) => {
    const { doc } = sampleDocument();
    const { node } = addNode(doc, {
      kind: 'device',
      hostname,
      iconKey,
      spec: { general: { hostname }, ...spec },
    });

    expect(nodeAccessibleName(doc, node)).toBe(
      `Device ${hostname}, ${shown}, 0 connections`,
    );
  });

  // The names of the devices the palette's templates add.
  test('a device added from a built-in template is named by its kind', () => {
    let doc = createDocument();
    const names = BUILTIN_TEMPLATES.map((template) => {
      const added = addNode(doc, nodeOptionsFromTemplate(template, doc));

      doc = added.doc;

      return nodeAccessibleName(doc, added.node);
    });

    expect(names).toEqual([
      'Device server, VirtualMachine, 0 interfaces, 0 connections',
      'Device workstation, VirtualMachine, 0 interfaces, 0 connections',
      'Device router, Router, 0 interfaces, 0 connections',
      'Device firewall, Firewall, 0 interfaces, 0 connections',
      'Device external, External, 0 interfaces, 0 connections',
    ]);
  });

  test('connections are named from the device end, for the palette', () => {
    const { doc, bravo, sw } = sampleDocument();
    // bravo is connected the other way round, and its connection labelled.
    const second = connect(doc, {
      sourceNodeId: sw.id,
      targetNodeId: bravo.id,
      targetHandleId: bravo.device.interfaces[0].id,
    }).doc;
    const labelled = {
      ...second,
      edges: second.edges.map((edge) =>
        edge.sourceNodeId === sw.id ? { ...edge, label: 'uplink' } : edge,
      ),
    };
    const unassigned = { ...labelled, networks: [] };

    expect(connectionList(labelled)).toEqual([
      {
        id: doc.edges[0].id,
        name: 'alpha (eth0) to EXP',
        networkName: 'EXP',
        label: '',
        locked: '',
      },
      expect.objectContaining({
        name: 'bravo (eth0) to EXP',
        networkName: 'EXP',
        label: 'uplink',
      }),
    ]);
    expect(connectionList(unassigned)[0].networkName).toBe('no network');
    expect(connectionList({})).toEqual([]);
  });

  test('the counts cover every element kind, each with its plural', () => {
    const { doc } = sampleDocument();
    const withNote = addNode(doc, { kind: 'note', text: 'hi' }).doc;
    const counts = diagramCounts(withNote);

    expect(counts.map((entry) => entry.text).join(', ')).toBe(
      '2 devices, 1 switch, 1 network, 1 connection, 0 groups, 1 note',
    );
    // The header shows each count's number beside its icon, and reads it
    // with its noun.
    expect(counts.slice(0, 2)).toEqual([
      {
        key: 'devices',
        count: 2,
        noun: 'devices',
        text: '2 devices',
        tip: 'Select all 2 devices',
      },
      {
        key: 'switches',
        count: 1,
        noun: 'switch',
        text: '1 switch',
        tip: 'Select the 1 switch',
      },
    ]);
  });

  test('a layers count comes last, only while a node is at a layer, and counts the layers the diagram offers', () => {
    const { doc, alpha } = sampleDocument();
    const keys = (document) =>
      diagramCounts(document).map((entry) => entry.key);
    const layered = setLayers(doc, [
      { id: 3, label: 'Enterprise' },
      { id: 1, label: 'Control' },
    ]);

    // No node at a layer: no count, whatever layers the diagram has.
    expect(keys(doc)).not.toContain('layers');
    expect(keys({ ...doc, layers: placeholderLayers() })).not.toContain(
      'layers',
    );
    expect(keys(layered)).not.toContain('layers');

    // A layer chosen for a node.
    expect(
      diagramCounts(setNodeLayers(layered, [alpha.id], 1).doc).at(-1),
    ).toEqual({
      key: 'layers',
      count: 2,
      noun: 'layers',
      text: '2 layers',
      tip: 'Select all nodes on a layer',
    });

    // The placeholder layers count, stored or not, as the Inspector says.
    for (const layers of [[], placeholderLayers()]) {
      expect(
        diagramCounts(setNodeLayers({ ...doc, layers }, [alpha.id], 2).doc).at(
          -1,
        ).text,
      ).toBe('7 layers');
    }

    // A layer that a matcher puts a device at.
    const matched = setLayers(doc, [
      { id: 1, label: 'Control', matchers: { 'general.hostname': '^alpha$' } },
    ]);

    expect(diagramCounts(matched).at(-1).text).toBe('1 layer');
    expect(
      keys(
        setLayers(doc, [
          { id: 1, label: 'Control', matchers: { 'general.hostname': '^z' } },
        ]),
      ),
    ).not.toContain('layers');
  });

  test('each count says what pressing it selects', () => {
    const { doc } = sampleDocument();
    const tips = (document) =>
      Object.fromEntries(
        diagramCounts(document).map((entry) => [entry.key, entry.tip]),
      );

    // One of a kind, and none: a count of none selects nothing, and its
    // tip is the count in words.
    expect(tips(doc)).toEqual({
      devices: 'Select all 2 devices',
      switches: 'Select the 1 switch',
      networks: 'Select the switches of the 1 network',
      links: 'Select the 1 connection',
      groups: '0 groups',
      notes: '0 notes',
    });
    expect(tips({ nodes: [], edges: [], networks: [] })).toEqual({
      devices: '0 devices',
      switches: '0 switches',
      networks: '0 networks',
      links: '0 connections',
      groups: '0 groups',
      notes: '0 notes',
    });

    // Several of each.
    let many = addNetwork(doc, { name: 'OT' }).doc;

    many = addNode(many, {
      kind: 'switch',
      networkId: many.networks[1].id,
    }).doc;
    many = addNode(many, { kind: 'note', text: 'one' }).doc;
    many = addNode(many, { kind: 'note', text: 'two' }).doc;
    many = groupNodes(many, [many.nodes[0].id]).doc;
    many = groupNodes(many, [many.nodes[1].id]).doc;

    expect(tips(many)).toMatchObject({
      switches: 'Select all 2 switches',
      networks: 'Select the switches of all 2 networks',
      groups: 'Select all 2 groups',
      notes: 'Select all 2 notes',
    });
  });
});
