import { describe, expect, test } from 'vitest';

import {
  buildOutline,
  connectionList,
  diagramCounts,
  labelIndex,
  networkOutline,
  outlineLabel,
  rowNodeIds,
} from '@/builder/outline.js';
import {
  addNetwork,
  addNode,
  connect,
  createDocument,
  findNode,
  groupNodes,
  updateNode,
} from '@/builder/model.js';
import { BUILTIN_TEMPLATES } from '@/builder/catalog.js';
import { nodeOptionsFromTemplate } from '@/builder/templates.js';

import { sampleDocument } from './fixtures.js';

describe('semantic outline', () => {
  test('mirrors the canvas contents', () => {
    const { doc } = sampleDocument();
    const outline = buildOutline(doc);

    expect(outline.map((item) => item.kind)).toEqual([
      'switch',
      'device',
      'device',
    ]);
    expect(outline.map((item) => item.label)).toContain('alpha');
  });

  test('rows are nodes only: a connection is counted in its node name', () => {
    const { doc, alpha } = sampleDocument();
    const item = buildOutline(doc).find((entry) => entry.id === alpha.id);

    expect(item).not.toHaveProperty('connections');
    expect(item).not.toHaveProperty('interfaces');
    expect(item.accessibleName).toBe('Device alpha, 1 connection, on EXP');
  });

  test('groups nest their members', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const grouped = groupNodes(doc, [alpha.id, bravo.id]).doc;
    const outline = buildOutline(grouped);
    const group = outline.find((item) => item.kind === 'group');

    expect(group.children.map((child) => child.id).sort()).toEqual(
      [alpha.id, bravo.id].sort(),
    );
    expect(outline.filter((item) => item.id === alpha.id)).toHaveLength(0);
  });

  test('a row stands for its node, a group with its members, a switch with its network', () => {
    const { doc, sw, alpha, bravo } = sampleDocument();
    const inner = groupNodes(doc, [bravo.id]);
    const outer = groupNodes(inner.doc, [inner.group.id]);
    const grouped = outer.doc;
    const sorted = (ids) => [...ids].sort();

    expect(rowNodeIds(grouped, alpha.id)).toEqual([alpha.id]);
    expect(sorted(rowNodeIds(grouped, outer.group.id))).toEqual(
      sorted([outer.group.id, inner.group.id, bravo.id]),
    );
    // Bravo is on no network: the switch's network is the switch and alpha.
    expect(sorted(rowNodeIds(grouped, sw.id))).toEqual(
      sorted([sw.id, alpha.id]),
    );
    expect(rowNodeIds(grouped, 'gone')).toEqual([]);
  });

  test('accessible names name the network, not just a colour', () => {
    const { doc, sw, alpha, bravo } = sampleDocument();
    const switchNode = doc.nodes.find((node) => node.id === sw.id);
    const device = doc.nodes.find((node) => node.id === alpha.id);

    expect(outlineLabel(doc, switchNode)).toContain('network EXP');
    expect(outlineLabel(doc, switchNode)).toContain('VLAN alias 100');
    expect(outlineLabel(doc, device)).toBe(
      'Device alpha, 1 connection, on EXP',
    );

    // A device names each of its networks once, sorted as the Networks list
    // sorts them; one with no connection names none.
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

    expect(outlineLabel(next, findNode(next, alpha.id))).toBe(
      'Device alpha, 3 connections, on DMZ and EXP',
    );
    expect(outlineLabel(next, findNode(next, bravo.id))).toBe(
      'Device bravo, 0 connections',
    );

    // Looked up once for every node, the names are the same.
    const index = labelIndex(next);

    for (const node of next.nodes) {
      expect(outlineLabel(next, node, index)).toBe(outlineLabel(next, node));
    }
  });

  test('the comment is part of the accessible name', () => {
    const { doc, alpha } = sampleDocument();
    const node = doc.nodes.find((entry) => entry.id === alpha.id);

    node.device.spec.general.description = 'jump host';

    expect(outlineLabel(doc, node)).toContain('comment: jump host');
  });

  test('names say which group a node is in and how many members a group has', () => {
    const { doc, alpha, bravo, sw } = sampleDocument();
    const grouped = groupNodes(doc, [alpha.id, bravo.id]).doc;
    const node = (id) => grouped.nodes.find((entry) => entry.id === id);
    const group = grouped.nodes.find((entry) => entry.kind === 'group');

    // A label that only repeats the kind is not said twice.
    expect(outlineLabel(grouped, group)).toBe('Group, 2 members');
    expect(outlineLabel(grouped, node(alpha.id))).toBe(
      'Device alpha, in Group, 1 connection, on EXP',
    );
    expect(outlineLabel(grouped, node(sw.id))).not.toContain(' in ');

    const titled = { ...group, label: 'DMZ', group: { title: 'DMZ' } };
    const renamed = {
      ...grouped,
      nodes: grouped.nodes.map((entry) =>
        entry.id === group.id ? titled : entry,
      ),
    };
    expect(outlineLabel(renamed, titled)).toBe('Group DMZ, 2 members');
    expect(outlineLabel(renamed, node(bravo.id))).toContain('in Group DMZ');
  });

  // A group's description is its comment, and its row shows the icon it
  // was given. Its colors and its border are not said: they are how it
  // looks, and its name and description say what it is.
  test("a group's description ends its name, and its row has its icon", () => {
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
    const row = buildOutline(described).find((item) => item.id === group.id);

    expect(outlineLabel(described, group)).toBe(
      'Group DMZ, 1 member, comment: Public servers',
    );
    expect(row).toMatchObject({
      label: 'DMZ',
      iconKey: 'firewall',
      description: 'Public servers',
      accessibleName: 'Group DMZ, 1 member, comment: Public servers',
    });
    expect(
      buildOutline(grouped.doc).find((item) => item.id === group.id),
    ).toMatchObject({ iconKey: 'container', description: '' });

    // A device's or a switch's colors change no name.
    const colored = updateNode(described, alpha.id, {
      device: { outlineColor: '#2f6fbf', fillColor: '#ffd400' },
    });

    expect(outlineLabel(colored, findNode(colored, alpha.id))).toBe(
      outlineLabel(described, findNode(described, alpha.id)),
    );
  });

  test('a note is named by its text, and a device by a non-default type', () => {
    const { doc } = sampleDocument();
    const text = `Firewall rules pending review ${'x'.repeat(100)}`;
    const { node: note } = addNode(doc, { kind: 'note', text });
    const { node: blank } = addNode(doc, { kind: 'note' });
    const { node: windows } = addNode(doc, {
      kind: 'device',
      hostname: 'dc-01',
      iconKey: 'windows',
    });
    const { node: router } = addNode(doc, {
      kind: 'device',
      hostname: 'router',
      iconKey: 'router',
    });
    const { node: server } = addNode(doc, { kind: 'device', hostname: 'web' });
    const redHat = (hostname) =>
      addNode(doc, { kind: 'device', hostname, iconKey: 'redhat' }).node;

    const noteName = outlineLabel(doc, note);
    expect(noteName.startsWith('Note: Firewall rules pending review')).toBe(
      true,
    );
    expect(noteName.length).toBeLessThanOrEqual('Note: '.length + 80);
    expect(noteName.endsWith('…')).toBe(true);
    expect(outlineLabel(doc, blank)).toBe('Note');
    expect(outlineLabel(doc, windows)).toBe(
      'Device dc-01, Windows, 0 connections',
    );
    // The label already names the type, and the default icon is not a type.
    expect(outlineLabel(doc, router)).toBe('Device router, 0 connections');
    expect(outlineLabel(doc, server)).toBe('Device web, 0 connections');
    // Only whole words name the type: "shredder" merely contains "red".
    expect(outlineLabel(doc, redHat('shredder'))).toBe(
      'Device shredder, Red Hat, 0 connections',
    );
    expect(outlineLabel(doc, redHat('red-team'))).toBe(
      'Device red-team, Red Hat, 0 connections',
    );
    expect(outlineLabel(doc, redHat('red-hat-01'))).toBe(
      'Device red-hat-01, 0 connections',
    );
    expect(outlineLabel(doc, redHat('rhel9'))).toBe(
      'Device rhel9, 0 connections',
    );
  });

  // The canvas shows a device's phenix node type; its name says the type
  // too, unless it is a plain device's or something in the name already
  // says it.
  test('a device is named by its node type, unless its label or its icon says it', () => {
    const { doc } = sampleDocument();
    const device = (hostname, iconKey, spec) =>
      addNode(doc, {
        kind: 'device',
        hostname,
        iconKey,
        spec: { general: { hostname }, ...spec },
      }).node;
    const name = (...args) => outlineLabel(doc, device(...args));

    expect(name('edge-1', 'linux', { type: 'Router' })).toBe(
      'Device edge-1, Router, 0 connections',
    );
    // As stored, not as the icon's word is written.
    expect(name('plc-1', 'server', { type: 'SCEPTRE' })).toBe(
      'Device plc-1, SCEPTRE, 0 connections',
    );
    // The icon's word comes first, then a type that says something else.
    expect(name('fw-1', 'router', { type: 'Firewall' })).toBe(
      'Device fw-1, router, Firewall, 0 connections',
    );
    // The icon's word says the type.
    expect(name('edge-1', 'router', { type: 'Router' })).toBe(
      'Device edge-1, router, 0 connections',
    );
    expect(name('plc-1', 'external', { external: true, type: 'HIL' })).toBe(
      'Device plc-1, external device, 0 connections',
    );
    // An external device with another icon says External, not its type.
    expect(name('plc-1', 'linux', { external: true, type: 'HIL' })).toBe(
      'Device plc-1, External, 0 connections',
    );
    // The label says the type: whole words, without a trailing number.
    expect(name('router2', 'linux', { type: 'Router' })).toBe(
      'Device router2, 0 connections',
    );
    expect(name('core-router', 'linux', { type: 'Router' })).toBe(
      'Device core-router, 0 connections',
    );
    expect(name('routers', 'linux', { type: 'Router' })).toBe(
      'Device routers, Router, 0 connections',
    );
    // A plain device has no type worth saying.
    expect(name('web', 'linux', { type: 'VirtualMachine' })).toBe(
      'Device web, 0 connections',
    );
    expect(name('web', 'linux', {})).toBe('Device web, 0 connections');
  });

  // The names of the devices the palette's templates add.
  test('template devices are named as before', () => {
    let doc = createDocument();
    const names = BUILTIN_TEMPLATES.map((template) => {
      const added = addNode(doc, nodeOptionsFromTemplate(template, doc));

      doc = added.doc;

      return outlineLabel(doc, added.node);
    });

    expect(names).toEqual([
      'Device server, 0 connections',
      'Device workstation, 0 connections',
      'Device router, 0 connections',
      'Device firewall, 0 connections',
      'Device external, 0 connections',
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

  test('the network outline lists members', () => {
    const { doc } = sampleDocument();
    const [network] = networkOutline(doc);

    expect(network.name).toBe('EXP');
    expect(network.members).toEqual(['alpha']);
    expect(network.devices).toBe('1 device');
    expect(network.accessibleName).toContain('VLAN alias 100');
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
