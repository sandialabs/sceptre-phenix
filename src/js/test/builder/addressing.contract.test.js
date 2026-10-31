import { describe, expect, test } from 'vitest';
import fc from 'fast-check';

import {
  assignAddresses,
  documentMacs,
  formatIPv4,
  formatSubnet,
  freeAddressFor,
  hostAddress,
  hostRange,
  interfacesByNetwork,
  interfacesOnNetwork,
  isRoutingType,
  isSubnetHost,
  macKey,
  needsAddress,
  nextFreeHost,
  overlappingSubnets,
  parseIPv4,
  parseSubnet,
  randomMac,
  subnetsOverlap,
  suggestedSubnet,
  suggestSubnet,
  withNetworkGateway,
} from '@/builder/addressing.js';
import { copySelection, pasteClipboard } from '@/builder/clipboard.js';
import { canonicalJSON } from '@/builder/digest.js';
import {
  addNode,
  connect,
  connectNodes,
  createDocument,
  findNetwork,
  findNode,
  networkOfSwitch,
  specInterfaces,
  updateNetwork,
} from '@/builder/model.js';

const ip = (text) => parseIPv4(text);
const subnet = (text) => parseSubnet(text);

describe('IPv4 addresses', () => {
  test('parses and formats an address', () => {
    expect(ip('10.1.0.2')).toBe(10 * 2 ** 24 + 1 * 2 ** 16 + 2);
    expect(ip('0.0.0.0')).toBe(0);
    expect(ip('255.255.255.255')).toBe(2 ** 32 - 1);
    expect(formatIPv4(ip('192.0.2.17'))).toBe('192.0.2.17');
    expect(formatIPv4(2 ** 32 - 1)).toBe('255.255.255.255');
  });

  test.each([
    '',
    '10.1.0',
    '10.1.0.2.3',
    '10.01.0.2',
    '256.0.0.1',
    ' 10.1.0.2',
    '10.1.0.2/24',
    '::1',
    '1e1.0.0.1',
  ])('refuses %j', (text) => {
    expect(parseIPv4(text)).toBeNull();
  });

  test('refuses what is not text', () => {
    expect(parseIPv4(10)).toBeNull();
    expect(parseIPv4(null)).toBeNull();
  });
});

describe('subnets', () => {
  test.each([
    ['10.1.0.0/24', 24, 256],
    ['0.0.0.0/0', 0, 2 ** 32],
    ['192.0.2.4/31', 31, 2],
    ['192.0.2.7/32', 32, 1],
    ['172.16.0.0/12', 12, 2 ** 20],
  ])('reads %s', (text, bits, size) => {
    const parsed = subnet(text);

    expect(parsed).toEqual({ base: expect.any(Number), bits, size });
    expect(formatSubnet(parsed)).toBe(text);
  });

  // The same as TestParseSubnet in types/builder.
  test.each([
    '',
    '10.1.0.0',
    '10.1.0.1/24',
    '10.1.0.0/33',
    '10.1.0.0/024',
    '10.1.0.0/+24',
    '10.1.0.0/-0',
    '10.01.0.0/16',
    ' 10.1.0.0/24',
    '10.1.0.0/24 ',
    '2001:db8::/64',
    '::ffff:10.1.0.0/120',
    '10.1.0.0/',
    '/24',
    '10.1.0/24',
    '256.0.0.0/8',
  ])('refuses %j', (text) => {
    expect(parseSubnet(text)).toBeNull();
  });

  test('host ranges leave out the network and broadcast addresses', () => {
    expect(hostRange(subnet('10.1.0.0/24'))).toEqual({
      first: ip('10.1.0.1'),
      last: ip('10.1.0.254'),
    });
    expect(hostRange(subnet('10.1.0.0/30'))).toEqual({
      first: ip('10.1.0.1'),
      last: ip('10.1.0.2'),
    });
  });

  test('a /31 has two hosts and a /32 one', () => {
    expect(hostRange(subnet('192.0.2.4/31'))).toEqual({
      first: ip('192.0.2.4'),
      last: ip('192.0.2.5'),
    });
    expect(hostRange(subnet('192.0.2.7/32'))).toEqual({
      first: ip('192.0.2.7'),
      last: ip('192.0.2.7'),
    });
  });

  // The same as TestIsSubnetHost in types/builder.
  test.each([
    ['10.1.0.0/24', '10.1.0.1', true],
    ['10.1.0.0/24', '10.1.0.254', true],
    ['10.1.0.0/24', '10.1.0.0', false],
    ['10.1.0.0/24', '10.1.0.255', false],
    ['10.1.0.0/24', '10.1.1.1', false],
    ['10.1.0.0/30', '10.1.0.1', true],
    ['10.1.0.0/30', '10.1.0.3', false],
    ['192.0.2.4/31', '192.0.2.4', true],
    ['192.0.2.4/31', '192.0.2.5', true],
    ['192.0.2.4/31', '192.0.2.6', false],
    ['192.0.2.7/32', '192.0.2.7', true],
    ['192.0.2.7/32', '192.0.2.6', false],
    ['0.0.0.0/0', '0.0.0.0', false],
    ['0.0.0.0/0', '255.255.255.255', false],
    ['0.0.0.0/0', '8.8.8.8', true],
  ])('in %s, %s is a host: %s', (text, address, want) => {
    expect(isSubnetHost(subnet(text), ip(address))).toBe(want);
  });

  test('overlaps', () => {
    expect(subnetsOverlap(subnet('10.1.0.0/24'), subnet('10.1.0.128/25'))).toBe(
      true,
    );
    expect(subnetsOverlap(subnet('10.0.0.0/8'), subnet('10.200.3.0/24'))).toBe(
      true,
    );
    expect(subnetsOverlap(subnet('10.1.0.0/24'), subnet('10.1.1.0/24'))).toBe(
      false,
    );
    expect(subnetsOverlap(subnet('192.0.2.4/31'), subnet('192.0.2.6/31'))).toBe(
      false,
    );
  });

  test('the next free host skips the taken ones', () => {
    const net = subnet('10.1.0.0/29');

    expect(formatIPv4(nextFreeHost(net, new Set()))).toBe('10.1.0.1');
    expect(
      formatIPv4(nextFreeHost(net, new Set([ip('10.1.0.1'), ip('10.1.0.3')]))),
    ).toBe('10.1.0.2');

    const all = new Set([1, 2, 3, 4, 5, 6].map((n) => ip(`10.1.0.${n}`)));

    expect(nextFreeHost(net, all)).toBeNull();
  });

  test('the next free host of a /31 and a /32', () => {
    expect(formatIPv4(nextFreeHost(subnet('192.0.2.4/31'), new Set()))).toBe(
      '192.0.2.4',
    );
    expect(
      formatIPv4(
        nextFreeHost(subnet('192.0.2.4/31'), new Set([ip('192.0.2.4')])),
      ),
    ).toBe('192.0.2.5');
    expect(
      nextFreeHost(subnet('192.0.2.7/32'), new Set([ip('192.0.2.7')])),
    ).toBeNull();
  });

  test('suggests the first free 10.<a>.<b>.0/24', () => {
    expect(suggestSubnet([])).toBe('10.1.0.0/24');
    expect(suggestSubnet([subnet('10.1.0.0/24')])).toBe('10.2.0.0/24');
    expect(suggestSubnet([subnet('10.1.0.0/16')])).toBe('10.2.0.0/24');
    expect(suggestSubnet([subnet('10.0.0.0/9')])).toBe('10.128.0.0/24');
    expect(suggestSubnet([], [ip('10.1.0.7')])).toBe('10.2.0.0/24');
    expect(suggestSubnet([subnet('10.0.0.0/8')])).toBe('');
    expect(suggestSubnet([subnet('0.0.0.0/0')])).toBe('');
  });

  test('past 10.255.0.0/24, it suggests the next third number', () => {
    const taken = Array.from({ length: 255 }, (_, n) =>
      subnet(`10.${n + 1}.0.0/24`),
    );

    expect(suggestSubnet(taken)).toBe('10.1.1.0/24');
  });
});

describe('MAC addresses', () => {
  test('keys ignore case and separators', () => {
    expect(macKey('02:AB:cd:00:11:22')).toBe('02abcd001122');
    expect(macKey('02-ab-cd-00-11-22')).toBe('02abcd001122');
    expect(macKey('02ab.cd00.1122')).toBe('02abcd001122');
    expect(macKey('nope')).toBe('');
    expect(macKey(undefined)).toBe('');
  });

  test('a random address is locally administered and unicast', () => {
    const fill = (bytes) => bytes.fill(0xff);

    expect(randomMac(new Set(), fill)).toBe('fe:ff:ff:ff:ff:ff');
    expect(randomMac(new Set(), (bytes) => bytes.fill(0))).toBe(
      '02:00:00:00:00:00',
    );
  });

  test('a random address is one not taken', () => {
    let call = 0;
    const fill = (bytes) => bytes.fill(call++);

    expect(randomMac(new Set(['020000000000', '030101010101']), fill)).toBe(
      '02:01:01:01:01:01',
    );
  });

  test('gives up when every try is taken', () => {
    const fill = (bytes) => bytes.fill(0);

    expect(randomMac(new Set(['020000000000']), fill)).toBe('');
  });

  test('uses crypto.getRandomValues by default', () => {
    const mac = randomMac();

    expect(mac).toMatch(/^[0-9a-f]{2}(:[0-9a-f]{2}){5}$/);
    expect(parseInt(mac.slice(0, 2), 16) & 0x03).toBe(0x02);
  });
});

// LAN (10.1.0.0/24, gateway 10.1.0.1), WAN, and devices a and b: a's eth0
// is connected to LAN, and b's eth0 is not, but its VLAN names LAN.
function diagram() {
  const device = (id, interfaces, handles = []) => ({
    id,
    kind: 'device',
    position: { x: 0, y: 0 },
    device: {
      hostname: id,
      spec: { network: { interfaces } },
      interfaces: handles,
    },
  });

  return {
    networks: [
      { id: 'lan', name: 'LAN', subnet: '10.1.0.0/24', gateway: '10.1.0.1' },
      { id: 'wan', name: 'WAN' },
    ],
    nodes: [
      { id: 'sw', kind: 'switch', switch: { networkId: 'lan' } },
      device(
        'a',
        [
          { name: 'eth0', type: 'ethernet', proto: 'static', vlan: 'LAN' },
          { name: 'eth1', type: 'ethernet', proto: 'dhcp', vlan: 'LAN' },
        ],
        [{ id: 'a0', name: 'eth0', index: 0 }],
      ),
      device('b', [
        { name: 'eth0', type: 'ethernet', proto: 'static', vlan: 'lan' },
        {
          name: 'eth1',
          type: 'ethernet',
          proto: 'static',
          address: '10.1.0.2',
          mask: 24,
          vlan: 'LAN',
        },
      ]),
    ],
    edges: [
      {
        id: 'e',
        sourceNodeId: 'a',
        sourceHandleId: 'a0',
        targetNodeId: 'sw',
        networkId: 'lan',
      },
    ],
  };
}

describe('addresses of a diagram', () => {
  test('an interface is on a network by its connection, else its VLAN', () => {
    const doc = diagram();

    expect(
      interfacesOnNetwork(doc, 'lan').map(
        ({ node, iface }) => `${node.id}.${iface.name}`,
      ),
    ).toEqual(['a.eth0', 'a.eth1', 'b.eth0', 'b.eth1']);

    // Connected elsewhere, the VLAN does not count.
    doc.edges[0].networkId = 'wan';
    expect(
      interfacesOnNetwork(doc, 'lan').map(
        ({ node, iface }) => `${node.id}.${iface.name}`,
      ),
    ).toEqual(['a.eth1', 'b.eth0', 'b.eth1']);
  });

  test('only static interfaces without an address need one', () => {
    expect(needsAddress({ proto: 'static' })).toBe(true);
    expect(needsAddress({ proto: 'ospf', address: ' ' })).toBe(true);
    expect(needsAddress({ proto: 'static', address: '10.0.0.1' })).toBe(false);
    expect(needsAddress({ proto: 'dhcp' })).toBe(false);
    expect(needsAddress({ proto: 'manual' })).toBe(false);
    expect(needsAddress({ type: 'serial', proto: 'static' })).toBe(false);
    expect(needsAddress(null)).toBe(false);
  });

  test('assigns the next free host addresses, the mask and the gateway', () => {
    const doc = diagram();
    const result = assignAddresses(doc, 'lan');

    // 10.1.0.1 is the gateway and b's eth1 has 10.1.0.2.
    expect(result.assigned).toEqual([
      { nodeId: 'a', name: 'eth0', address: '10.1.0.3', mask: 24 },
      { nodeId: 'b', name: 'eth0', address: '10.1.0.4', mask: 24 },
    ]);
    expect(result.full).toBe(false);

    const a = result.doc.nodes[1].device.spec.network.interfaces;
    const b = result.doc.nodes[2].device.spec.network.interfaces;

    expect(a[0]).toMatchObject({
      address: '10.1.0.3',
      mask: 24,
      gateway: '10.1.0.1',
    });
    // Not static, and an address it has: unchanged.
    expect(a[1]).toEqual(doc.nodes[1].device.spec.network.interfaces[1]);
    expect(b[1]).toEqual(doc.nodes[2].device.spec.network.interfaces[1]);
    // The document given is not changed.
    expect(doc.nodes[1].device.spec.network.interfaces[0].address).toBe(
      undefined,
    );
  });

  test('keeps a gateway the interface has', () => {
    const doc = diagram();

    doc.nodes[1].device.spec.network.interfaces[0].gateway = '10.1.0.254';

    const result = assignAddresses(doc, 'lan', { nodeId: 'a', name: 'eth0' });

    expect(result.assigned).toHaveLength(1);
    expect(result.doc.nodes[1].device.spec.network.interfaces[0].gateway).toBe(
      '10.1.0.254',
    );
  });

  test('without a gateway, the first host is free', () => {
    const doc = diagram();

    delete doc.networks[0].gateway;

    const result = assignAddresses(doc, 'lan', { nodeId: 'a', name: 'eth0' });

    expect(result.assigned).toEqual([
      { nodeId: 'a', name: 'eth0', address: '10.1.0.1', mask: 24 },
    ]);
    expect(
      result.doc.nodes[1].device.spec.network.interfaces[0],
    ).not.toHaveProperty('gateway');
  });

  test('fills one interface when asked', () => {
    const result = assignAddresses(diagram(), 'lan', {
      nodeId: 'b',
      name: 'eth0',
    });

    expect(result.assigned.map((entry) => entry.address)).toEqual(['10.1.0.3']);
  });

  test('a full subnet assigns nothing more and says so', () => {
    const doc = diagram();

    doc.networks[0].subnet = '10.1.0.0/30';

    const result = assignAddresses(doc, 'lan');

    // 10.1.0.1 is the gateway and 10.1.0.2 is in use: no host is left.
    expect(result.assigned).toEqual([]);
    expect(result.full).toBe(true);
    expect(result.left).toBe(2);
    expect(result.doc).toBe(doc);
  });

  test('a /31 subnet assigns both of its addresses', () => {
    const doc = diagram();

    doc.networks[0].subnet = '10.1.0.2/31';
    delete doc.networks[0].gateway;

    const result = assignAddresses(doc, 'lan');

    expect(result.assigned.map((entry) => entry.address)).toEqual(['10.1.0.3']);
    expect(result.full).toBe(true);
    expect(result.left).toBe(1);
  });

  test('a network without a subnet, or with an invalid one, assigns nothing', () => {
    const doc = diagram();

    expect(assignAddresses(doc, 'wan')).toEqual({
      doc,
      assigned: [],
      full: false,
      left: 0,
    });

    doc.networks[0].subnet = '10.1.0.1/24';
    expect(assignAddresses(doc, 'lan').assigned).toEqual([]);
    expect(assignAddresses(doc, 'nope').assigned).toEqual([]);
  });

  test('a device from an included topology is not changed', () => {
    const doc = diagram();

    doc.nodes[1].device.includedFrom = 'shared';

    expect(
      assignAddresses(doc, 'lan').assigned.map((entry) => entry.nodeId),
    ).toEqual(['b']);
  });

  test('the suggested subnet of a new network', () => {
    const doc = diagram();

    expect(suggestedSubnet(doc)).toBe('10.2.0.0/24');

    doc.networks[0].subnet = '';
    // 10.1.0.1 is the gateway of a network, and 10.1.0.2 an address.
    expect(suggestedSubnet(doc)).toBe('10.2.0.0/24');

    doc.networks[0].gateway = '';
    doc.nodes[2].device.spec.network.interfaces[1].address = '172.16.0.1';
    expect(suggestedSubnet(doc)).toBe('10.1.0.0/24');
    expect(suggestedSubnet({})).toBe('10.1.0.0/24');
  });

  test('a free address for an interface whose address is taken', () => {
    const doc = diagram();

    // b's eth1 has 10.1.0.2, which a's eth0 takes too: 10.1.0.1 is the
    // gateway, so 10.1.0.3 is the first free host. a's eth0 has no gateway,
    // so it gets the network's.
    doc.nodes[1].device.spec.network.interfaces[0].address = '10.1.0.2';
    expect(freeAddressFor(doc, 'a', 0)).toEqual({
      network: doc.networks[0],
      address: '10.1.0.3',
      mask: 24,
      gateway: '10.1.0.1',
    });
    expect(freeAddressFor(doc, 'b', 1)?.address).toBe('10.1.0.3');

    // No address, a node that is not a device, or a network without a
    // subnet: no free address.
    expect(freeAddressFor(doc, 'b', 0)).toBeNull();
    expect(freeAddressFor(doc, 'sw', 0)).toBeNull();
    expect(freeAddressFor(doc, 'a', 5)).toBeNull();
    doc.edges[0].networkId = 'wan';
    expect(freeAddressFor(doc, 'a', 0)).toBeNull();
  });

  test('a free address says when the subnet is full', () => {
    const doc = diagram();

    doc.networks[0].subnet = '10.1.0.0/30';
    doc.nodes[2].device.spec.network.interfaces[0].address = '10.1.0.2';

    expect(freeAddressFor(doc, 'b', 1)).toEqual({
      network: doc.networks[0],
      full: true,
    });
  });

  test('the MAC addresses of a diagram', () => {
    const doc = diagram();

    doc.nodes[1].device.spec.network.interfaces[0].mac = '02:AA:00:00:00:01';
    doc.nodes[2].device.spec.network.interfaces[1].mac = 'bad';

    expect(documentMacs(doc)).toEqual(new Set(['02aa00000001']));
  });
});

describe('networks and connections of the editor', () => {
  // A diagram with one switch, added as the palette adds one, and a device
  // without interfaces.
  function editorDiagram() {
    const switched = addNode(createDocument({ name: 'Lab' }), {
      kind: 'switch',
    });
    const device = addNode(switched.doc, { kind: 'device', hostname: 'pc' });

    return {
      doc: device.doc,
      sw: switched.node,
      network: switched.network,
      pc: device.node,
    };
  }

  const ifaceOf = (doc, nodeId, index = 0) =>
    specInterfaces(findNode(doc, nodeId))[index];

  test('a new switch gets a subnet of its own', () => {
    const { doc, network } = editorDiagram();

    expect(network.subnet).toBe('10.1.0.0/24');
    expect(network).not.toHaveProperty('gateway');

    const second = addNode(doc, { kind: 'switch' });

    expect(second.network.subnet).toBe('10.2.0.0/24');

    // Another switch of the same network adds no network.
    const same = addNode(doc, { kind: 'switch', networkId: network.id });

    expect(same.network).toBeUndefined();
    expect(same.doc.networks).toHaveLength(1);
  });

  test('a connection gives a new interface the next free address', () => {
    const { doc, sw, pc } = editorDiagram();
    const result = connect(doc, { sourceNodeId: pc.id, targetNodeId: sw.id });

    expect(ifaceOf(result.doc, pc.id)).toEqual({
      name: 'eth0',
      type: 'ethernet',
      proto: 'static',
      mask: 24,
      vlan: 'EXP',
      address: '10.1.0.1',
    });
    expect(result.assigned).toEqual({
      nodeId: pc.id,
      name: 'eth0',
      address: '10.1.0.1',
      mask: 24,
    });

    // The next device gets the next address.
    const other = addNode(result.doc, { kind: 'device', hostname: 'pc2' });
    const again = connect(other.doc, {
      sourceNodeId: other.node.id,
      targetNodeId: sw.id,
    });

    expect(ifaceOf(again.doc, other.node.id).address).toBe('10.1.0.2');
  });

  test("a connection sets the network's gateway and its prefix length", () => {
    const { doc, sw, pc, network } = editorDiagram();
    const routed = updateNetwork(doc, network.id, {
      subnet: '192.0.2.0/28',
      gateway: '192.0.2.1',
    });
    const result = connect(routed, {
      sourceNodeId: pc.id,
      targetNodeId: sw.id,
    });

    expect(ifaceOf(result.doc, pc.id)).toMatchObject({
      address: '192.0.2.2',
      mask: 28,
      gateway: '192.0.2.1',
    });
  });

  test('a connection never changes an address, or an interface that is not static', () => {
    const { doc, sw, network } = editorDiagram();
    const device = addNode(doc, {
      kind: 'device',
      hostname: 'typed',
      interfaces: [{ name: 'eth0' }, { name: 'eth1', proto: 'dhcp' }],
    });
    const handles = device.node.device.interfaces;
    let next = device.doc;

    next = {
      ...next,
      nodes: next.nodes.map((node) =>
        node.id === device.node.id
          ? {
              ...node,
              device: {
                ...node.device,
                spec: {
                  ...node.device.spec,
                  network: {
                    interfaces: [
                      { ...ifaceOf(next, node.id, 0), address: '10.1.0.77' },
                      ifaceOf(next, node.id, 1),
                    ],
                  },
                },
              },
            }
          : node,
      ),
    };

    for (const handle of handles) {
      const result = connect(next, {
        sourceNodeId: device.node.id,
        sourceHandleId: handle.id,
        targetNodeId: sw.id,
      });

      expect(result.assigned).toBeUndefined();
      next = result.doc;
    }

    expect(specInterfaces(findNode(next, device.node.id))).toEqual([
      expect.objectContaining({ address: '10.1.0.77', mask: 24 }),
      expect.not.objectContaining({ address: expect.anything() }),
    ]);
    expect(findNetwork(next, network.id).subnet).toBe('10.1.0.0/24');
  });

  test('a connection to a network without a subnet assigns nothing', () => {
    const { doc, sw, pc, network } = editorDiagram();
    const plain = updateNetwork(doc, network.id, { subnet: '' });
    const result = connect(plain, { sourceNodeId: pc.id, targetNodeId: sw.id });

    expect(findNetwork(plain, network.id)).not.toHaveProperty('subnet');
    expect(ifaceOf(result.doc, pc.id)).not.toHaveProperty('address');
    expect(result.assigned).toBeUndefined();
    expect(result.full).toBe(false);
  });

  test('a connection to a full subnet assigns nothing and says so', () => {
    const { doc, sw, pc, network } = editorDiagram();
    const tiny = updateNetwork(doc, network.id, { subnet: '10.1.0.4/32' });
    const first = connect(tiny, { sourceNodeId: pc.id, targetNodeId: sw.id });

    expect(ifaceOf(first.doc, pc.id).address).toBe('10.1.0.4');

    const second = connect(first.doc, {
      sourceNodeId: pc.id,
      targetNodeId: sw.id,
    });

    expect(second.assigned).toBeUndefined();
    expect(second.full).toBe(true);
    expect(ifaceOf(second.doc, pc.id, 1)).not.toHaveProperty('address');
  });

  test('two devices joined through a new switch get addresses of its subnet', () => {
    const { doc, pc } = editorDiagram();
    const other = addNode(doc, { kind: 'device', hostname: 'pc2' });
    const result = connectNodes(other.doc, {
      sourceNodeId: pc.id,
      targetNodeId: other.node.id,
    });

    expect(networkOfSwitch(result.doc, result.node).subnet).toBe('10.2.0.0/24');
    expect(
      [pc.id, other.node.id].map((id) => ifaceOf(result.doc, id).address),
    ).toEqual(['10.2.0.1', '10.2.0.2']);
    expect(result.assigned.map((entry) => entry.address)).toEqual([
      '10.2.0.1',
      '10.2.0.2',
    ]);
    expect(result.full).toBe(false);
  });

  // The viewer's Assign addresses on connect setting, off.
  test('a connection asked for no address gives none, and names the interface', () => {
    const { doc, sw, pc, network } = editorDiagram();
    const result = connect(
      doc,
      { sourceNodeId: pc.id, targetNodeId: sw.id },
      { assign: false },
    );

    expect(ifaceOf(result.doc, pc.id)).toEqual({
      name: 'eth0',
      type: 'ethernet',
      proto: 'static',
      mask: 24,
      vlan: 'EXP',
    });
    expect(result.assigned).toBeUndefined();
    expect(result.full).toBe(false);
    expect(result.unaddressed).toEqual({ nodeId: pc.id, name: 'eth0' });
    expect(findNetwork(result.doc, network.id).subnet).toBe('10.1.0.0/24');

    // assign: true is the default.
    expect(
      connect(
        doc,
        { sourceNodeId: pc.id, targetNodeId: sw.id },
        { assign: true },
      ).assigned,
    ).toMatchObject({ address: '10.1.0.1' });
  });

  test('two devices joined through a new switch asked for no address get none, and their switch keeps its subnet', () => {
    const { doc, pc } = editorDiagram();
    const other = addNode(doc, { kind: 'device', hostname: 'pc2' });
    const result = connectNodes(
      other.doc,
      { sourceNodeId: pc.id, targetNodeId: other.node.id },
      { assign: false },
    );

    expect(networkOfSwitch(result.doc, result.node).subnet).toBe('10.2.0.0/24');
    expect([pc.id, other.node.id].map((id) => ifaceOf(result.doc, id))).toEqual(
      [
        expect.not.objectContaining({ address: expect.anything() }),
        expect.not.objectContaining({ address: expect.anything() }),
      ],
    );
    expect(result.assigned).toEqual([]);
    expect(result.unaddressed).toEqual([
      { nodeId: pc.id, name: 'eth0' },
      { nodeId: other.node.id, name: 'eth0' },
    ]);
  });

  // A paste makes its connections again, so it follows the same option.
  test('a pasted connection asked for no address gives none', () => {
    const { doc, sw, pc } = editorDiagram();
    const linked = connect(
      doc,
      { sourceNodeId: pc.id, targetNodeId: sw.id },
      { assign: false },
    ).doc;
    const payload = copySelection(linked, { nodes: [pc.id, sw.id] });
    const copyOf = (pasted) =>
      pasted.nodeIds.find((id) => findNode(pasted.doc, id).kind === 'device');

    const unassigned = pasteClipboard(linked, payload, { assign: false });

    expect(unassigned.doc.edges).toHaveLength(2);
    expect(ifaceOf(unassigned.doc, copyOf(unassigned))).toMatchObject({
      name: 'eth0',
      proto: 'static',
    });
    expect(ifaceOf(unassigned.doc, copyOf(unassigned))).not.toHaveProperty(
      'address',
    );

    // assign: true is the default.
    const assigned = pasteClipboard(linked, payload);

    expect(ifaceOf(assigned.doc, copyOf(assigned))).toMatchObject({
      address: '10.1.0.1',
    });
  });

  test('a pasted network keeps its subnet and gateway', () => {
    const { doc, sw, network } = editorDiagram();
    const routed = updateNetwork(doc, network.id, {
      subnet: '10.7.0.0/24',
      gateway: '10.7.0.1',
    });
    const payload = copySelection(routed, { nodes: [sw.id] });
    const pasted = pasteClipboard(createDocument({ name: 'Other' }), payload);

    expect(pasted.doc.networks).toEqual([
      expect.objectContaining({
        name: 'EXP',
        subnet: '10.7.0.0/24',
        gateway: '10.7.0.1',
      }),
    ]);
  });
});

describe('addressing rules, for any address and subnet', () => {
  const address = fc.integer({ min: 0, max: 2 ** 32 - 1 });
  const aligned = fc
    .tuple(fc.integer({ min: 0, max: 32 }), address)
    .map(([bits, at]) => {
      const size = 2 ** (32 - bits);

      return { base: Math.floor(at / size) * size, bits, size };
    });

  test('an address or a subnet reads back as written', () => {
    fc.assert(
      fc.property(address, aligned, (value, block) => {
        expect(parseIPv4(formatIPv4(value))).toBe(value);
        expect(parseSubnet(formatSubnet(block))).toEqual(block);
      }),
      { numRuns: 300 },
    );
    fc.assert(
      fc.property(
        fc.array(fc.nat(255), { minLength: 4, maxLength: 4 }),
        (octets) => {
          const text = octets.join('.');

          expect(formatIPv4(parseIPv4(text))).toBe(text);
        },
      ),
      { numRuns: 200 },
    );
  });

  test('a subnet whose base has host bits is no subnet', () => {
    fc.assert(
      fc.property(aligned, fc.nat(), (block, pick) => {
        fc.pre(block.bits < 32);
        const base = block.base + 1 + (pick % (block.size - 1));

        expect(parseSubnet(`${formatIPv4(base)}/${block.bits}`)).toBeNull();
      }),
      { numRuns: 300 },
    );
  });

  test('two subnets overlap, either way round, when one holds the other', () => {
    const holds = (a, b) =>
      a.base <= b.base && b.base + b.size <= a.base + a.size;

    fc.assert(
      fc.property(aligned, aligned, (a, b) => {
        expect(subnetsOverlap(a, b)).toBe(subnetsOverlap(b, a));
        expect(subnetsOverlap(a, b)).toBe(holds(a, b) || holds(b, a));
      }),
      { numRuns: 300 },
    );
  });

  test('the next free host is the lowest one, or null only when all are taken', () => {
    fc.assert(
      fc.property(
        fc.integer({ min: 24, max: 32 }),
        address,
        fc.array(fc.nat(300), { maxLength: 300, size: 'max' }),
        (bits, at, offsets) => {
          const size = 2 ** (32 - bits);
          const block = { base: Math.floor(at / size) * size, bits, size };
          const { first, last } = hostRange(block);
          const taken = new Set(offsets.map((offset) => block.base + offset));
          let lowest = null;

          for (let host = first; host <= last && lowest === null; host += 1) {
            lowest = taken.has(host) ? null : host;
          }
          expect(nextFreeHost(block, taken)).toBe(lowest);
        },
      ),
      { numRuns: 200 },
    );
  });

  // Subnets over the first suggestions (10.1.0.0/24 on), and others. 20 of
  // them cannot cover all of 10.0.0.0/8, so there is always a suggestion.
  const nearby = fc
    .tuple(
      fc.integer({ min: 16, max: 30 }),
      fc.integer({ min: 1, max: 6 }),
      fc.nat(2),
      fc.nat(255),
    )
    .map(([bits, second, third, fourth]) => {
      const size = 2 ** (32 - bits);
      const at = ((10 * 256 + second) * 256 + third) * 256 + fourth;

      return { base: Math.floor(at / size) * size, bits, size };
    });
  const elsewhere = aligned.filter(
    ({ base, bits, size }) =>
      bits >= 16 || base + size <= 10 * 2 ** 24 || base >= 11 * 2 ** 24,
  );

  test('a suggested subnet is a /24 clear of every taken subnet and address', () => {
    fc.assert(
      fc.property(
        fc.array(fc.oneof(nearby, elsewhere), { maxLength: 20 }),
        fc.array(
          fc.oneof(
            nearby.map((block) => block.base + 1),
            address,
          ),
          {
            maxLength: 20,
          },
        ),
        (taken, used) => {
          const block = parseSubnet(suggestSubnet(taken, used));

          expect(block.bits).toBe(24);
          expect(taken.some((other) => subnetsOverlap(other, block))).toBe(
            false,
          );
          expect(
            used.some(
              (value) => value >= block.base && value < block.base + block.size,
            ),
          ).toBe(false);
        },
      ),
      { numRuns: 200 },
    );
  });

  const mac = fc.uint8Array({ minLength: 6, maxLength: 6 });
  const hex = (bytes) =>
    [...bytes].map((byte) => byte.toString(16).padStart(2, '0'));

  test('a random MAC address is local, unicast and not taken', () => {
    fc.assert(
      fc.property(
        fc.array(mac, { minLength: 1, maxLength: 4 }),
        fc.array(fc.boolean(), { minLength: 4, maxLength: 4 }),
        (draws, taking) => {
          let next = 0;
          // Gives the draws in turn, as a seeded random source would.
          const fill = (bytes) => {
            bytes.set(draws[next++ % draws.length]);

            return bytes;
          };
          const local = (bytes) => [
            (bytes[0] & 0xfc) | 0x02,
            ...bytes.slice(1),
          ];
          const taken = new Set(
            draws
              .filter((_, i) => taking[i])
              .map((bytes) => hex(local(bytes)).join('')),
          );
          const result = randomMac(taken, fill);

          if (result) {
            expect(result).toMatch(/^[0-9a-f]{2}(:[0-9a-f]{2}){5}$/);
            expect(parseInt(result.slice(0, 2), 16) & 0x03).toBe(0x02);
            expect(taken.has(macKey(result))).toBe(false);
          } else {
            expect(draws.every((_, i) => taking[i])).toBe(true);
          }
        },
      ),
      { numRuns: 200 },
    );
  });

  test('a MAC key ignores separators and case, and keys itself', () => {
    fc.assert(
      fc.property(
        mac,
        fc.constantFrom(':', '-', '.', ''),
        fc.boolean(),
        (bytes, separator, upper) => {
          const text = hex(bytes).join(separator);
          const key = macKey(upper ? text.toUpperCase() : text);

          expect(key).toBe(hex(bytes).join(''));
          expect(macKey(key)).toBe(key);
        },
      ),
      { numRuns: 200 },
    );
  });
});

describe('assigning addresses, for any network', () => {
  const iface = fc.record({
    proto: fc.constantFrom('static', 'static', 'ospf', 'dhcp'),
    type: fc.constantFrom('ethernet', 'ethernet', 'serial'),
    has: fc.option(fc.nat(), { nil: undefined }),
  });
  // A network with a subnet of /24 to /32, maybe a gateway, and devices on it
  // by VLAN, some of whose interfaces have an address.
  const network = fc
    .record({
      bits: fc.integer({ min: 24, max: 32 }),
      at: fc.integer({ min: 0, max: 2 ** 32 - 1 }),
      gateway: fc.option(fc.nat(), { nil: undefined }),
      devices: fc.array(fc.array(iface, { minLength: 1, maxLength: 3 }), {
        minLength: 1,
        maxLength: 6,
      }),
    })
    .map(({ bits, at, gateway, devices }) => {
      const size = 2 ** (32 - bits);
      const block = { base: Math.floor(at / size) * size, bits, size };
      const { first, last } = hostRange(block);
      const host = (n) => formatIPv4(first + (n % (last - first + 1)));

      return {
        networks: [
          {
            id: 'lan',
            name: 'LAN',
            subnet: formatSubnet(block),
            ...(gateway === undefined ? {} : { gateway: host(gateway) }),
          },
        ],
        nodes: devices.map((interfaces, d) => ({
          id: `d${d}`,
          kind: 'device',
          position: { x: 0, y: 0 },
          device: {
            hostname: `d${d}`,
            interfaces: [],
            spec: {
              network: {
                interfaces: interfaces.map(({ proto, type, has }, i) => ({
                  name: `eth${i}`,
                  type,
                  proto,
                  vlan: 'LAN',
                  ...(has === undefined ? {} : { address: host(has) }),
                })),
              },
            },
          },
        })),
        edges: [],
      };
    });
  const addressesOf = (doc) =>
    interfacesOnNetwork(doc, 'lan').map(({ iface }) => iface.address ?? '');

  test('gives free host addresses once, and changes nothing else', () => {
    fc.assert(
      fc.property(network, (doc) => {
        const before = canonicalJSON(doc);
        const block = parseSubnet(doc.networks[0].subnet);
        const gateway = parseIPv4(doc.networks[0].gateway);
        const had = addressesOf(doc);
        const targets = interfacesOnNetwork(doc, 'lan').filter(({ iface }) =>
          needsAddress(iface),
        );
        const result = assignAddresses(doc, 'lan');
        const given = result.assigned.map((entry) => parseIPv4(entry.address));

        expect(canonicalJSON(doc)).toBe(before);
        expect(result.assigned.length + result.left).toBe(targets.length);
        expect(new Set(given).size).toBe(given.length);
        for (const value of given) {
          expect(isSubnetHost(block, value)).toBe(true);
          expect(value).not.toBe(gateway);
          expect(had.map(parseIPv4)).not.toContain(value);
        }
        addressesOf(result.doc).forEach((value, i) => {
          if (had[i]) {
            expect(value).toBe(had[i]);
          }
        });
        expect(assignAddresses(result.doc, 'lan').assigned).toEqual([]);
      }),
      { numRuns: 200 },
    );
  });
});

describe('what the checks read of a diagram', () => {
  test('a router or a firewall routes, in any case', () => {
    expect(isRoutingType('Router')).toBe(true);
    expect(isRoutingType('firewall')).toBe(true);
    expect(isRoutingType('FIREWALL')).toBe(true);
    expect(isRoutingType('VirtualMachine')).toBe(false);
    expect(isRoutingType(' Router')).toBe(false);
    expect(isRoutingType(undefined)).toBe(false);
  });

  // As networkMembers in types/builder/warnings.go: Go lowercases each
  // character on its own, and strings.TrimSpace keeps U+FEFF.
  test('a VLAN names a network in another case as Go folds it', () => {
    const doc = {
      networks: [{ id: 'n1', name: '\u039f\u0394\u039f\u03a3' }],
      nodes: [
        {
          id: 'd1',
          kind: 'device',
          device: {
            hostname: 'host',
            interfaces: [],
            spec: {
              network: {
                interfaces: [
                  { name: 'eth0', vlan: '\u03bf\u03b4\u03bf\u03c3' },
                  { name: 'eth1', vlan: '\u03bf\u03b4\u03bf\u03c2' },
                  { name: 'eth2', vlan: '\ufeff\u039f\u0394\u039f\u03a3' },
                ],
              },
            },
          },
        },
      ],
      edges: [],
    };

    expect(
      (interfacesByNetwork(doc).get('n1') || []).map(({ index }) => index),
    ).toEqual([0]);
  });

  test("an interface's own address skips DHCP, manual and prefix lengths", () => {
    expect(hostAddress({ address: ' 10.0.0.1/24 ' })).toBe(ip('10.0.0.1'));
    expect(hostAddress({ proto: 'OSPF', address: '10.0.0.1' })).toBe(
      ip('10.0.0.1'),
    );
    expect(hostAddress({ proto: ' DHCP', address: '10.0.0.1' })).toBeNull();
    expect(hostAddress({ proto: 'manual', address: '10.0.0.1' })).toBeNull();
    expect(hostAddress({ address: '::ffff:10.0.0.1' })).toBeNull();
    expect(hostAddress({ address: 10 })).toBeNull();
    expect(hostAddress(null)).toBeNull();
  });

  test('the interfaces of every network at once', () => {
    const doc = diagram();
    const members = interfacesByNetwork(doc);

    expect([...members.keys()]).toEqual(['lan']);
    expect(members.get('lan')).toEqual(interfacesOnNetwork(doc, 'lan'));
  });

  test('a network overlaps another exactly when its subnet holds or is held by one', () => {
    const block = fc
      .tuple(fc.integer({ min: 8, max: 32 }), fc.integer({ min: 0, max: 255 }))
      .map(([bits, at]) => {
        const size = 2 ** (32 - bits);
        const base = Math.floor((10 * 2 ** 24 + at * 65536) / size) * size;

        return formatSubnet({ base, bits, size });
      });

    fc.assert(
      fc.property(
        fc.array(fc.option(block, { nil: '' }), { maxLength: 12 }),
        (subnets) => {
          const networks = subnets.map((subnet) => ({ subnet }));
          const found = overlappingSubnets(networks);
          const parsed = subnets.map(parseSubnet);
          const overlaps = (i, j) =>
            i !== j &&
            parsed[i] !== null &&
            parsed[j] !== null &&
            subnetsOverlap(parsed[i], parsed[j]);

          expect(found.map(({ index }) => index).sort((a, b) => a - b)).toEqual(
            parsed
              .map((_, i) => i)
              .filter((i) => parsed.some((_, j) => overlaps(i, j))),
          );
          found.forEach(({ index, other }) =>
            expect(overlaps(index, other)).toBe(true),
          );
        },
      ),
      { numRuns: 300 },
    );
  });
});

// diagram() with device a a Router, unless `type` names another type.
function routed(type = 'Router') {
  const doc = diagram();

  doc.nodes[1].device.spec.type = type;

  return doc;
}

const interfacesOf = (doc, position) =>
  doc.nodes[position].device.spec.network.interfaces;

describe('routers and gateways', () => {
  test('a router takes a free gateway, and the other interfaces point at it', () => {
    const doc = routed();
    const result = assignAddresses(doc, 'lan');

    expect(result.assigned).toEqual([
      {
        nodeId: 'a',
        name: 'eth0',
        address: '10.1.0.1',
        mask: 24,
        holdsGateway: true,
      },
      { nodeId: 'b', name: 'eth0', address: '10.1.0.3', mask: 24 },
    ]);
    expect(interfacesOf(result.doc, 1)[0]).not.toHaveProperty('gateway');
    expect(interfacesOf(result.doc, 2)[0].gateway).toBe('10.1.0.1');
    expect(result.doc.networks).toBe(doc.networks);
  });

  test('a gateway that an interface has is not free', () => {
    const doc = routed('firewall');

    interfacesOf(doc, 2)[1].address = '10.1.0.1';

    const result = assignAddresses(doc, 'lan', { nodeId: 'a', name: 'eth0' });

    expect(result.assigned).toEqual([
      { nodeId: 'a', name: 'eth0', address: '10.1.0.2', mask: 24 },
    ]);
    expect(interfacesOf(result.doc, 1)[0]).not.toHaveProperty('gateway');
  });

  test('a router sets a missing gateway in the same edit', () => {
    const doc = routed();

    delete doc.networks[0].gateway;

    const result = assignAddresses(doc, 'lan');

    expect(result.assigned).toEqual([
      {
        nodeId: 'a',
        name: 'eth0',
        address: '10.1.0.1',
        mask: 24,
        holdsGateway: true,
        setGateway: true,
      },
      { nodeId: 'b', name: 'eth0', address: '10.1.0.3', mask: 24 },
    ]);
    expect(result.doc.networks[0].gateway).toBe('10.1.0.1');
    expect(interfacesOf(result.doc, 2)[0].gateway).toBe('10.1.0.1');
    // The document given is not changed.
    expect(doc.networks[0]).not.toHaveProperty('gateway');
  });

  test('a gateway that does not parse, or is outside the subnet, stays', () => {
    const doc = routed();

    doc.networks[0].gateway = 'nope';

    const odd = assignAddresses(doc, 'lan');

    expect(odd.doc.networks[0].gateway).toBe('nope');
    expect(odd.assigned.some((entry) => entry.holdsGateway)).toBe(false);
    expect(interfacesOf(odd.doc, 2)[0]).not.toHaveProperty('gateway');

    doc.networks[0].gateway = '10.9.0.1';

    const outside = assignAddresses(doc, 'lan');

    expect(outside.assigned[0].address).toBe('10.1.0.1');
    expect(outside.assigned.some((entry) => entry.holdsGateway)).toBe(false);
  });

  test('only one router holds the gateway, and routers go first', () => {
    const doc = routed('Router');

    // b, a router after a: its interfaces come after a's.
    doc.nodes[2].device.spec.type = 'Router';
    // a is a host now, before router b in the order of the nodes.
    doc.nodes[1].device.spec.type = 'VirtualMachine';
    delete doc.networks[0].gateway;

    const result = assignAddresses(doc, 'lan');

    expect(result.assigned).toEqual([
      {
        nodeId: 'b',
        name: 'eth0',
        address: '10.1.0.1',
        mask: 24,
        holdsGateway: true,
        setGateway: true,
      },
      { nodeId: 'a', name: 'eth0', address: '10.1.0.3', mask: 24 },
    ]);
    expect(interfacesOf(result.doc, 1)[0].gateway).toBe('10.1.0.1');

    // A second router interface on the network gets a normal address.
    const second = routed();

    second.nodes[2].device.spec.type = 'Firewall';

    expect(
      assignAddresses(second, 'lan').assigned.map((entry) => [
        entry.address,
        Boolean(entry.holdsGateway),
      ]),
    ).toEqual([
      ['10.1.0.1', true],
      ['10.1.0.3', false],
    ]);
  });

  test('a free address for a router follows the same rules', () => {
    const doc = routed();

    // a's eth0 shares 10.1.0.2 with b's eth1: the gateway 10.1.0.1 is free.
    interfacesOf(doc, 1)[0].address = '10.1.0.2';
    expect(freeAddressFor(doc, 'a', 0)).toEqual({
      network: doc.networks[0],
      address: '10.1.0.1',
      mask: 24,
      holdsGateway: true,
    });

    // With no gateway, the address becomes the gateway.
    delete doc.networks[0].gateway;
    expect(freeAddressFor(doc, 'a', 0)).toEqual({
      network: doc.networks[0],
      address: '10.1.0.1',
      mask: 24,
      holdsGateway: true,
      setGateway: true,
    });

    // A router that has the gateway keeps it: the other interface moves.
    doc.networks[0].gateway = '10.1.0.2';
    expect(freeAddressFor(doc, 'a', 0)).toBeNull();
    expect(freeAddressFor(doc, 'b', 1)).toEqual({
      network: doc.networks[0],
      address: '10.1.0.1',
      mask: 24,
      gateway: '10.1.0.2',
    });
  });

  test('the interfaces given an address before the gateway get it', () => {
    const doc = routed();

    interfacesOf(doc, 2)[0].address = '10.1.0.9';

    const next = withNetworkGateway(doc, 'lan', [
      { nodeId: 'b', name: 'eth0' },
      { nodeId: 'a', name: 'eth0' },
      { nodeId: 'b', name: 'nope' },
    ]);

    expect(interfacesOf(next, 2)[0].gateway).toBe('10.1.0.1');
    // A router's interface gets none.
    expect(interfacesOf(next, 1)[0]).not.toHaveProperty('gateway');
    expect(interfacesOf(doc, 2)[0]).not.toHaveProperty('gateway');
    expect(
      withNetworkGateway(next, 'lan', [{ nodeId: 'b', name: 'eth0' }]),
    ).toBe(next);
    expect(
      withNetworkGateway(doc, 'wan', [{ nodeId: 'b', name: 'eth0' }]),
    ).toBe(doc);
  });

  test('a router joined to a host through a new switch gives the host its gateway', () => {
    const switched = addNode(createDocument({ name: 'Lab' }), {
      kind: 'switch',
    });
    const host = addNode(switched.doc, { kind: 'device', hostname: 'pc' });
    const router = addNode(host.doc, {
      kind: 'device',
      hostname: 'rtr',
      spec: { type: 'Router' },
    });
    const doc = router.doc;
    const result = connectNodes(doc, {
      sourceNodeId: host.node.id,
      targetNodeId: router.node.id,
    });
    const network = networkOfSwitch(result.doc, result.node);
    const ifaceOf = (id) => specInterfaces(findNode(result.doc, id))[0];

    expect(findNode(doc, router.node.id).device.spec.type).toBe('Router');
    expect(network.gateway).toBe(ifaceOf(router.node.id).address);
    expect(ifaceOf(router.node.id)).not.toHaveProperty('gateway');
    expect(ifaceOf(host.node.id).gateway).toBe(network.gateway);
    expect(result.assigned.map((entry) => Boolean(entry.setGateway))).toEqual([
      false,
      true,
    ]);
  });
});

describe('routers and gateways, for any network', () => {
  const iface = fc.record({
    proto: fc.constantFrom('static', 'static', 'ospf', 'dhcp'),
    has: fc.option(fc.nat(), { nil: undefined }),
  });
  const device = fc.record({
    type: fc.constantFrom('Router', 'firewall', 'VirtualMachine', 'Server'),
    interfaces: fc.array(iface, { minLength: 1, maxLength: 3 }),
  });
  // A network with a subnet of /24 to /30, maybe a gateway, and devices on
  // it by VLAN, routers among them, some of whose interfaces have an
  // address.
  const network = fc
    .record({
      bits: fc.integer({ min: 24, max: 30 }),
      at: fc.integer({ min: 0, max: 2 ** 32 - 1 }),
      gateway: fc.option(fc.nat(), { nil: undefined }),
      devices: fc.array(device, { minLength: 1, maxLength: 6 }),
    })
    .map(({ bits, at, gateway, devices }) => {
      const size = 2 ** (32 - bits);
      const block = { base: Math.floor(at / size) * size, bits, size };
      const { first, last } = hostRange(block);
      const host = (n) => formatIPv4(first + (n % (last - first + 1)));

      return {
        networks: [
          {
            id: 'lan',
            name: 'LAN',
            subnet: formatSubnet(block),
            ...(gateway === undefined ? {} : { gateway: host(gateway) }),
          },
        ],
        nodes: devices.map(({ type, interfaces }, d) => ({
          id: `d${d}`,
          kind: 'device',
          position: { x: 0, y: 0 },
          device: {
            hostname: `d${d}`,
            interfaces: [],
            spec: {
              type,
              network: {
                interfaces: interfaces.map(({ proto, has }, i) => ({
                  name: `eth${i}`,
                  type: 'ethernet',
                  proto,
                  vlan: 'LAN',
                  ...(has === undefined ? {} : { address: host(has) }),
                })),
              },
            },
          },
        })),
        edges: [],
      };
    });

  test('a router holds the gateway, the others point at it, and no address is used twice', () => {
    fc.assert(
      fc.property(network, (doc) => {
        const before = interfacesOnNetwork(doc, 'lan');
        const routerTargets = before.filter(
          ({ node, iface: entry }) =>
            needsAddress(entry) && isRoutingType(node.device.spec.type),
        );
        const gatewayBefore = parseIPv4(doc.networks[0].gateway);
        const heldBefore = before.some(
          ({ iface: entry }) =>
            gatewayBefore !== null &&
            parseIPv4(entry.address ?? '') === gatewayBefore,
        );
        const result = assignAddresses(doc, 'lan');
        const gateway = parseIPv4(result.doc.networks[0].gateway);
        const holders = result.assigned.filter((entry) => entry.holdsGateway);
        const given = result.assigned.map((entry) => parseIPv4(entry.address));
        const had = before
          .map(({ iface: entry }) => parseIPv4(entry.address ?? ''))
          .filter((value) => value !== null);

        // No address is given twice, or given that an interface had.
        expect(new Set(given).size).toBe(given.length);
        given.forEach((value) => expect(had).not.toContain(value));

        // At most one interface holds the gateway, a router's, with the
        // gateway's address.
        expect(holders.length).toBeLessThanOrEqual(1);
        holders.forEach((entry) => {
          expect(
            isRoutingType(findNode(doc, entry.nodeId).device.spec.type),
          ).toBe(true);
          expect(parseIPv4(entry.address)).toBe(gateway);
        });

        // A router given an address holds the gateway when it was free or
        // missing.
        const first = result.assigned.find((entry) =>
          isRoutingType(findNode(doc, entry.nodeId).device.spec.type),
        );

        if (first && (gatewayBefore === null || !heldBefore)) {
          expect(first.holdsGateway).toBe(true);
        }

        if (routerTargets.length === 0) {
          expect(result.doc.networks).toBe(doc.networks);
        }

        // A router's interface gets no gateway, and every other interface
        // given an address points at the network's gateway.
        interfacesOnNetwork(result.doc, 'lan').forEach(
          ({ node, iface: entry }) => {
            const given = result.assigned.some(
              (assigned) =>
                assigned.nodeId === node.id && assigned.name === entry.name,
            );

            if (!given) {
              return;
            }

            if (isRoutingType(node.device.spec.type)) {
              expect(entry).not.toHaveProperty('gateway');
            } else if (gateway !== null) {
              expect(parseIPv4(entry.gateway)).toBe(gateway);
            }
          },
        );
      }),
      { numRuns: 300 },
    );
  });
});
