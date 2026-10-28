// One-click fixes of the Diagram checks (issueFixes.js).

import fc from 'fast-check';
import { describe, expect, test } from 'vitest';

import { issueFix } from '@/builder/issueFixes.js';
import {
  addNetwork,
  addNode,
  createDocument,
  findNode,
} from '@/builder/model.js';
import { validateDocument } from '@/builder/validate.js';

// A device whose interfaces are on network LAN by their VLAN.
function spec(hostname, interfaces) {
  return {
    type: 'VirtualMachine',
    general: { hostname, vm_type: 'kvm' },
    hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
    network: {
      interfaces: interfaces.map((iface, index) => ({
        name: `eth${index}`,
        type: 'ethernet',
        proto: 'static',
        mask: 24,
        vlan: 'LAN',
        ...iface,
      })),
    },
  };
}

// LAN (10.1.0.0/24, gateway 10.1.0.1, or no subnet), and a device for each
// list of interfaces.
function diagram(devices, { subnet = '10.1.0.0/24' } = {}) {
  let doc = createDocument({ id: 'doc', name: 'Fixes' });

  doc = addNetwork(doc, {
    id: 'lan',
    name: 'LAN',
    ...(subnet ? { subnet, gateway: '10.1.0.1' } : {}),
  }).doc;
  doc = addNode(doc, { kind: 'switch', networkId: 'lan' }).doc;

  devices.forEach((interfaces, index) => {
    doc = addNode(doc, {
      id: `d${index}`,
      hostname: `d${index}`,
      spec: spec(`d${index}`, interfaces),
      position: { x: 0, y: 100 * (index + 1) },
    }).doc;
  });

  return doc;
}

// The diagram with device d0 a Router.
function routerFirst(doc) {
  return {
    ...doc,
    nodes: doc.nodes.map((node) =>
      node.id === 'd0'
        ? {
            ...node,
            device: {
              ...node.device,
              spec: { ...node.device.spec, type: 'Router' },
            },
          }
        : node,
    ),
  };
}

function issuesOf(doc, code) {
  return validateDocument(doc).filter((issue) => issue.code === code);
}

// Gives a device another device's hostname, as an import can.
function rename(doc, id, hostname) {
  return {
    ...doc,
    nodes: doc.nodes.map((node) =>
      node.id === id
        ? {
            ...node,
            label: hostname,
            device: {
              ...node.device,
              hostname,
              spec: {
                ...node.device.spec,
                general: { ...node.device.spec.general, hostname },
              },
            },
          }
        : node,
    ),
  };
}

function include(doc, id) {
  return {
    ...doc,
    nodes: doc.nodes.map((node) =>
      node.id === id
        ? { ...node, device: { ...node.device, includedFrom: 'shared' } }
        : node,
    ),
  };
}

describe('the codes', () => {
  test('another code has no fix', () => {
    const doc = diagram([[{ address: '10.1.0.5' }]]);

    expect(
      issueFix(doc, { code: 'node.hostname.required', path: 'nodes[1]' }),
    ).toBeNull();
    expect(issueFix(doc, null)).toBeNull();
    expect(issueFix(doc, { code: 'toString', nodeId: 'd0' })).toBeNull();
  });
});

describe('a duplicate hostname', () => {
  test('renames the device to the next free hostname, as one edit', () => {
    const doc = rename(diagram([[], []]), 'd1', 'D0');
    const [issue] = issuesOf(doc, 'node.hostname.duplicate');
    const fix = issueFix(doc, issue);

    expect(fix.name).toBe('Rename D0 to D0-2');
    expect(fix.label).toBe('Renamed D0 to D0-2');

    const fixed = fix.apply(doc);
    const node = findNode(fixed, 'd1');

    expect(node.device.hostname).toBe('D0-2');
    expect(node.device.spec.general.hostname).toBe('D0-2');
    expect(issuesOf(fixed, 'node.hostname.duplicate')).toEqual([]);
    expect(findNode(fixed, 'd0')).toEqual(findNode(doc, 'd0'));
  });

  test('renames the diagram’s own device when an included one has the issue', () => {
    const doc = include(rename(diagram([[], []]), 'd1', 'd0'), 'd1');
    const fix = issueFix(doc, {
      code: 'node.hostname.duplicate',
      nodeId: 'd1',
      path: 'nodes[3].device.hostname',
    });

    expect(fix.name).toBe('Rename d0 to d0-2');
    expect(findNode(fix.apply(doc), 'd0').device.hostname).toBe('d0-2');
    expect(findNode(fix.apply(doc), 'd1')).toEqual(findNode(doc, 'd1'));
  });

  // "İx" and "ix" are one hostname for the server (foldKey), not for
  // toLowerCase, which gives "i̇x".
  test('finds the own device in case as the server folds it', () => {
    const doc = include(
      rename(rename(diagram([[], []]), 'd0', 'ix'), 'd1', 'İx'),
      'd1',
    );
    const fix = issueFix(doc, {
      code: 'node.hostname.duplicate',
      nodeId: 'd1',
    });

    expect(fix?.name).toBe('Rename ix to ix-2');
  });

  test('has no fix when only included devices have the hostname', () => {
    const doc = include(
      include(rename(diagram([[], []]), 'd1', 'd0'), 'd0'),
      'd1',
    );

    expect(
      issueFix(doc, { code: 'node.hostname.duplicate', nodeId: 'd1' }),
    ).toBeNull();
  });

  test('finds the device by the issue’s path without a node id', () => {
    const doc = rename(diagram([[], []]), 'd1', 'd0');
    const index = doc.nodes.findIndex((node) => node.id === 'd1');

    expect(
      issueFix(doc, {
        code: 'node.hostname.duplicate',
        path: `nodes[${index}].device.hostname`,
      })?.name,
    ).toBe('Rename d0 to d0-2');
  });
});

describe('a shared IP address', () => {
  test('gives the interface a free address of its network', () => {
    const doc = diagram([
      [{ address: '10.1.0.2' }],
      [{ address: '10.1.0.2' }, { address: '10.1.0.3' }],
    ]);
    const issues = issuesOf(doc, 'interface.ip.shared');

    expect(issues).toHaveLength(2);

    const fix = issueFix(doc, issues[1]);

    expect(fix.name).toBe(
      'Change the IP address of interface eth0 of d1 to 10.1.0.4',
    );

    const fixed = fix.apply(doc);
    const iface = findNode(fixed, 'd1').device.spec.network.interfaces[0];

    expect(iface.address).toBe('10.1.0.4');
    expect(iface.mask).toBe(24);
    expect(issuesOf(fixed, 'interface.ip.shared')).toEqual([]);
  });

  test("gives another interface the network's gateway too", () => {
    const doc = diagram([[{ address: '10.1.0.2' }], [{ address: '10.1.0.2' }]]);
    const fixed = issueFix(doc, issuesOf(doc, 'interface.ip.shared')[1]).apply(
      doc,
    );

    expect(
      findNode(fixed, 'd1').device.spec.network.interfaces[0],
    ).toMatchObject({ address: '10.1.0.3', gateway: '10.1.0.1' });
  });

  test("gives a router's interface the free gateway, and the router that has it keeps it", () => {
    const doc = routerFirst(
      diagram([[{ address: '10.1.0.2' }], [{ address: '10.1.0.2' }]]),
    );
    const [router] = issuesOf(doc, 'interface.ip.shared');
    const fix = issueFix(doc, router);

    expect(fix.name).toBe(
      'Change the IP address of interface eth0 of d0 to 10.1.0.1, the gateway of network LAN',
    );

    const fixed = fix.apply(doc);
    const iface = findNode(fixed, 'd0').device.spec.network.interfaces[0];

    expect(iface.address).toBe('10.1.0.1');
    expect(iface).not.toHaveProperty('gateway');
    expect(issuesOf(fixed, 'interface.ip.shared')).toEqual([]);
    expect(issuesOf(fixed, 'network.gateway.unowned')).toEqual([]);

    // When the router has the gateway, only the other interface moves.
    const held = routerFirst(
      diagram([[{ address: '10.1.0.1' }], [{ address: '10.1.0.1' }]]),
    );
    const [first, second] = issuesOf(held, 'interface.ip.shared');

    expect(issueFix(held, first)).toBeNull();
    expect(issueFix(held, second)?.name).toBe(
      'Change the IP address of interface eth0 of d1 to 10.1.0.2',
    );
  });

  test('moves the second router that has the gateway, and the first keeps it', () => {
    const doc = routerFirst(
      diagram([[{ address: '10.1.0.1' }], [{ address: '10.1.0.1' }]]),
    );
    const routers = {
      ...doc,
      nodes: doc.nodes.map((node) =>
        node.id === 'd1'
          ? {
              ...node,
              device: {
                ...node.device,
                spec: { ...node.device.spec, type: 'Firewall' },
              },
            }
          : node,
      ),
    };
    const [first, second] = issuesOf(routers, 'interface.ip.shared');

    expect(issueFix(routers, first)).toBeNull();

    const fix = issueFix(routers, second);

    expect(fix?.name).toBe(
      'Change the IP address of interface eth0 of d1 to 10.1.0.2',
    );

    const fixed = fix.apply(routers);

    expect(
      findNode(fixed, 'd1').device.spec.network.interfaces[0],
    ).not.toHaveProperty('gateway');
    expect(issuesOf(fixed, 'interface.ip.shared')).toEqual([]);
  });

  test("makes a router's new address the gateway of a network that has none", () => {
    const doc = routerFirst(
      diagram([[{ address: '10.1.0.2' }], [{ address: '10.1.0.2' }]]),
    );
    const plain = {
      ...doc,
      networks: doc.networks.map(({ gateway: _, ...network }) => network),
    };
    const fix = issueFix(plain, issuesOf(plain, 'interface.ip.shared')[0]);

    expect(fix.name).toBe(
      'Change the IP address of interface eth0 of d0 to 10.1.0.1, and make it the gateway of network LAN',
    );
    expect(fix.label).toBe(
      'Changed the IP address of interface eth0 of d0 to 10.1.0.1, and made it the gateway of network LAN',
    );

    const fixed = fix.apply(plain);

    expect(fixed.networks[0].gateway).toBe('10.1.0.1');
    expect(
      findNode(fixed, 'd0').device.spec.network.interfaces[0].address,
    ).toBe('10.1.0.1');
  });

  test('has no fix on a network without a subnet, or with none free', () => {
    const plain = diagram(
      [[{ address: '10.1.0.2' }], [{ address: '10.1.0.2' }]],
      { subnet: '' },
    );
    const [issue] = issuesOf(plain, 'interface.ip.shared');

    expect(issue).toBeTruthy();
    expect(issueFix(plain, issue)).toBeNull();

    // A /30 holds two hosts: the gateway and the shared address.
    const full = diagram(
      [[{ address: '10.1.0.2' }], [{ address: '10.1.0.2' }]],
      {
        subnet: '10.1.0.0/30',
      },
    );

    expect(issueFix(full, issuesOf(full, 'interface.ip.shared')[0])).toBeNull();
  });

  test('has no fix on an included device', () => {
    const doc = include(
      diagram([[{ address: '10.1.0.2' }], [{ address: '10.1.0.2' }]]),
      'd1',
    );

    expect(
      issueFix(doc, {
        code: 'interface.ip.shared',
        nodeId: 'd1',
        field: 'spec.network.interfaces.0.address',
      }),
    ).toBeNull();
  });

  // Up to 8 devices with 1 to 3 addresses from 4, on 10.1.0.0/24: Fix on
  // the first shared address, again and again, ends with none shared, and
  // changes nothing but one address each time.
  test('fixing each issue in turn leaves no address shared', () => {
    const devices = fc.array(
      fc.array(fc.integer({ min: 2, max: 5 }), {
        minLength: 1,
        maxLength: 3,
      }),
      { minLength: 2, maxLength: 8 },
    );

    fc.assert(
      fc.property(devices, (hosts) => {
        let doc = diagram(
          hosts.map((list) =>
            list.map((host) => ({ address: `10.1.0.${host}` })),
          ),
        );

        for (let step = 0; step < 30; step += 1) {
          const [issue] = issuesOf(doc, 'interface.ip.shared');

          if (!issue) {
            break;
          }

          const before = validateDocument(doc).length;
          const fixed = issueFix(doc, issue).apply(doc);
          const changed = fixed.nodes.filter(
            (node, index) => node !== doc.nodes[index],
          );

          expect(changed.map((node) => node.id)).toEqual([issue.nodeId]);
          expect(validateDocument(fixed).length).toBeLessThan(before);
          doc = fixed;
        }

        expect(issuesOf(doc, 'interface.ip.shared')).toEqual([]);
      }),
      { numRuns: 60 },
    );
  });
});

describe('a shared MAC address', () => {
  test('clears the MAC address, so phenix makes one', () => {
    const doc = diagram([
      [{ address: '10.1.0.2', mac: '02:00:00:00:00:01' }],
      [{ address: '10.1.0.3', mac: '02-00-00-00-00-01' }],
    ]);
    const issues = issuesOf(doc, 'interface.mac.shared');

    expect(issues).toHaveLength(2);

    const fix = issueFix(doc, issues[0]);

    expect(fix.name).toBe('Clear the MAC address of interface eth0 of d0');
    expect(fix.label).toBe('Cleared the MAC address of interface eth0 of d0');

    const fixed = fix.apply(doc);

    expect(
      'mac' in findNode(fixed, 'd0').device.spec.network.interfaces[0],
    ).toBe(false);
    expect(issuesOf(fixed, 'interface.mac.shared')).toEqual([]);
    expect(findNode(fixed, 'd1')).toEqual(findNode(doc, 'd1'));
  });
});
