import { beforeEach, describe, expect, test } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

import { findNode, specInterfaces } from '@/builder/model.js';
import { resetSettings, setSetting } from '@/builder/settings.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

// The store's address assignment: on a connection, and with Assign
// addresses. Each is one Undo step and says what it did.
describe('network addresses in the store', () => {
  let store;
  let said;
  let sample;

  const ifaces = (id) => specInterfaces(findNode(store.doc, id));

  beforeEach(() => {
    resetSettings(null);
    setActivePinia(createPinia());
    store = useBuilderStore();
    sample = sampleDocument();

    const { doc, network } = sample;

    store.setDocument({
      ...doc,
      networks: doc.networks.map((entry) =>
        entry.id === network.id
          ? { ...entry, subnet: '10.5.0.0/29', gateway: '10.5.0.1' }
          : entry,
      ),
    });
    said = [];
    store.$onAction(({ name, args }) => {
      if (name === 'announce') {
        said.push(args[0]);
      }
    });
  });

  test('a connection sets the address of a new interface and says so', () => {
    const { bravo, sw } = sample;
    const result = store.connect({
      sourceNodeId: bravo.id,
      targetNodeId: sw.id,
    });

    expect(result.edge).toBeTruthy();
    expect(ifaces(bravo.id)[1]).toMatchObject({
      name: 'eth1',
      proto: 'static',
      address: '10.5.0.2',
      mask: 29,
      gateway: '10.5.0.1',
    });
    expect(said).toEqual([
      'Connected nodes',
      'Set the address of eth1 of bravo to 10.5.0.2/29.',
    ]);

    // One Undo step takes the connection and the address back.
    store.undo();
    expect(ifaces(bravo.id)).toHaveLength(1);
  });

  test('Assign addresses fills the static interfaces of a network as one step', () => {
    const { alpha, bravo, network } = sample;

    // alpha's eth0 is on the network, without an address. bravo's eth0
    // names the network by its VLAN.
    store.updateNode(bravo.id, {
      device: {
        spec: {
          ...findNode(store.doc, bravo.id).device.spec,
          network: {
            interfaces: [
              { name: 'eth0', type: 'ethernet', proto: 'static', vlan: 'EXP' },
            ],
          },
        },
      },
    });

    const steps = store.history.size;
    const result = store.assignAddresses(network.id);

    expect(result.assigned.map((entry) => entry.address)).toEqual([
      '10.5.0.2',
      '10.5.0.3',
    ]);
    expect(ifaces(alpha.id)[0]).toMatchObject({
      address: '10.5.0.2',
      mask: 29,
    });
    expect(ifaces(bravo.id)[0]).toMatchObject({ address: '10.5.0.3' });
    expect(said.at(-1)).toBe('Assigned 2 addresses on network EXP');
    expect(store.history.size).toBe(steps + 1);
    expect(store.history.undoLabel()).toBe(
      'Assigned 2 addresses on network EXP',
    );

    store.undo();
    expect(ifaces(alpha.id)[0]).not.toHaveProperty('address');
    expect(ifaces(bravo.id)[0]).not.toHaveProperty('address');

    store.redo();
    // Nothing is left to fill.
    expect(store.assignAddresses(network.id)).toBeNull();
    expect(said.at(-1)).toBe(
      'No addresses assigned: each static interface on network EXP has an address.',
    );
  });

  test("a router's connection takes the free gateway and says so", () => {
    const { bravo, sw } = sample;

    store.updateNode(bravo.id, {
      device: {
        spec: { ...findNode(store.doc, bravo.id).device.spec, type: 'Router' },
      },
    });
    said = [];
    store.connect({ sourceNodeId: bravo.id, targetNodeId: sw.id });

    expect(ifaces(bravo.id)[1]).toMatchObject({ address: '10.5.0.1' });
    expect(ifaces(bravo.id)[1]).not.toHaveProperty('gateway');
    expect(said).toEqual([
      'Connected nodes',
      'Set the address of eth1 of bravo to 10.5.0.1/29.',
      'eth1 of bravo has the gateway address of network EXP.',
    ]);
  });

  test("Assign addresses sets a missing gateway to a router's address in the same step", () => {
    const { alpha, bravo, network } = sample;

    store.updateNetwork(network.id, { gateway: '' });
    store.updateNode(bravo.id, {
      device: {
        spec: {
          ...findNode(store.doc, bravo.id).device.spec,
          type: 'firewall',
          network: {
            interfaces: [
              { name: 'eth0', type: 'ethernet', proto: 'static', vlan: 'EXP' },
            ],
          },
        },
      },
    });

    const steps = store.history.size;

    said = [];
    store.assignAddresses(network.id);

    expect(store.doc.networks[0].gateway).toBe('10.5.0.1');
    expect(ifaces(bravo.id)[0]).toMatchObject({ address: '10.5.0.1' });
    expect(ifaces(alpha.id)[0]).toMatchObject({
      address: '10.5.0.2',
      gateway: '10.5.0.1',
    });
    expect(said).toEqual([
      'Assigned 2 addresses on network EXP',
      'Set the gateway of network EXP to 10.5.0.1, the address of eth0 of bravo.',
    ]);
    expect(store.history.size).toBe(steps + 1);

    store.undo();
    expect(store.doc.networks[0]).not.toHaveProperty('gateway');
  });

  test('a full subnet says how many interfaces have no address', () => {
    const { alpha, network } = sample;

    // 10.5.0.0 is the gateway: only 10.5.0.1 is free.
    store.updateNetwork(network.id, {
      subnet: '10.5.0.0/31',
      gateway: '10.5.0.0',
    });

    expect(store.assignAddresses(network.id).assigned).toEqual([
      expect.objectContaining({ nodeId: alpha.id, address: '10.5.0.1' }),
    ]);

    // A second interface on the network finds no free address.
    store.connect({ sourceNodeId: alpha.id, targetNodeId: sample.sw.id });
    expect(said.at(-1)).toBe(
      'No address set: subnet 10.5.0.0/31 of network EXP is full.',
    );
    expect(ifaces(alpha.id)[1]).not.toHaveProperty('address');

    expect(store.assignAddresses(network.id)).toBeNull();
    expect(said.at(-1)).toBe(
      'No addresses assigned on network EXP. Subnet 10.5.0.0/31 is full: 1 interface has no address.',
    );
  });

  test('a network without a subnet is refused', () => {
    const { network } = sample;

    store.updateNetwork(network.id, { subnet: '', gateway: '' });

    expect(store.assignAddresses(network.id)).toBeNull();
    expect(said.at(-1)).toBe(
      'Network EXP has no subnet. Set its Subnet in the Inspector, then select Assign addresses again.',
    );
    expect(store.assignAddresses('missing')).toBeNull();
  });

  // The new interface is static with no address, which its Inspector requires
  // before Apply, so the connection says the user must type it.
  test('a connection to a network without a subnet says the interface has no address', () => {
    const { alpha, bravo, sw, network } = sample;

    store.updateNetwork(network.id, { subnet: '', gateway: '' });

    expect(
      store.connect({ sourceNodeId: bravo.id, targetNodeId: sw.id }).edge,
    ).toBeTruthy();
    expect(ifaces(bravo.id)[1]).toMatchObject({
      name: 'eth1',
      proto: 'static',
    });
    expect(ifaces(bravo.id)[1]).not.toHaveProperty('address');
    expect(said.slice(-2)).toEqual([
      'Connected nodes',
      'No address set: network EXP has no subnet. Type an address for eth1 of bravo in the Inspector.',
    ]);

    // Drag-to-connect says the same.
    expect(
      store.connectNodes({ sourceNodeId: alpha.id, targetNodeId: sw.id }).edges,
    ).toHaveLength(1);
    expect(said.at(-1)).toBe(
      'No address set: network EXP has no subnet. Type an address for eth1 of alpha in the Inspector.',
    );
  });
  // With Assign addresses on connect off, the interface stays without an
  // address and the connection says so. Assign addresses still fills it.
  test('with Assign addresses on connect off, a connection sets no address and says the interface has none', () => {
    const { alpha, bravo, sw, network } = sample;

    setSetting('assignAddressesOnConnect', false, null);

    try {
      expect(
        store.connect({ sourceNodeId: bravo.id, targetNodeId: sw.id }).edge,
      ).toBeTruthy();
      expect(ifaces(bravo.id)[1]).toMatchObject({
        name: 'eth1',
        proto: 'static',
      });
      expect(ifaces(bravo.id)[1]).not.toHaveProperty('address');
      expect(said.slice(-2)).toEqual([
        'Connected nodes',
        'No address set. Type an address for eth1 of bravo in the Inspector, or select Assign addresses.',
      ]);

      // Drag-to-connect does the same.
      store.connectNodes({ sourceNodeId: alpha.id, targetNodeId: sw.id });
      expect(ifaces(alpha.id)[1]).not.toHaveProperty('address');
      expect(said.at(-1)).toBe(
        'No address set. Type an address for eth1 of alpha in the Inspector, or select Assign addresses.',
      );
      expect(said.join(' ')).not.toMatch(/Set the address/);

      // Assign addresses works whatever the setting.
      const result = store.assignAddresses(network.id);

      expect(result.assigned.map((entry) => entry.address)).toEqual([
        '10.5.0.2',
        '10.5.0.3',
        '10.5.0.4',
      ]);
      expect(ifaces(alpha.id).map((iface) => iface.address)).toEqual([
        '10.5.0.2',
        '10.5.0.3',
      ]);
      expect(ifaces(bravo.id)[1]).toMatchObject({ address: '10.5.0.4' });
    } finally {
      resetSettings(null);
    }
  });

  // Paste and Duplicate connect again, so they follow the setting too. Alpha's
  // eth0 has no address, as the sample connects it before the subnet.
  test('with Assign addresses on connect off, Paste and Duplicate set no address', () => {
    const { alpha, sw } = sample;
    const copyOf = (ids) =>
      ids.find((id) => findNode(store.doc, id).kind === 'device');

    expect(ifaces(alpha.id)[0]).not.toHaveProperty('address');
    setSetting('assignAddressesOnConnect', false, null);

    try {
      store.selection = { nodes: [alpha.id, sw.id], edges: [] };
      store.copy();

      const pasted = copyOf(store.paste());

      expect(ifaces(pasted)[0]).toMatchObject({ name: 'eth0' });
      expect(ifaces(pasted)[0]).not.toHaveProperty('address');

      store.selection = { nodes: [alpha.id, sw.id], edges: [] };

      const duplicated = copyOf(store.duplicate());

      expect(ifaces(duplicated)[0]).not.toHaveProperty('address');

      // With the setting on, a paste sets the address.
      setSetting('assignAddressesOnConnect', true, null);

      expect(ifaces(copyOf(store.paste()))[0]).toMatchObject({
        address: '10.5.0.2',
      });
    } finally {
      resetSettings(null);
    }
  });

  // Drag-to-connect takes both settings: Snap to grid places the switch
  // between two devices, and Assign addresses on connect sets the address.
  test('with Snap to grid on and Assign addresses on connect off, drag-to-connect puts a new switch on the grid and sets no address', () => {
    const { alpha, bravo, sw } = sample;
    const grid = store.doc.grid.size;
    const onGrid = ({ x, y }) => x % grid === 0 && y % grid === 0;

    store.updateNode(bravo.id, { position: { x: 5, y: 203 } });
    setSetting('snapToGrid', true, null);
    setSetting('assignAddressesOnConnect', false, null);

    try {
      const joined = store.connectNodes({
        sourceNodeId: alpha.id,
        targetNodeId: bravo.id,
      });

      expect(joined.node.kind).toBe('switch');
      expect(
        onGrid(findNode(store.doc, joined.node.id).position),
        JSON.stringify(findNode(store.doc, joined.node.id).position),
      ).toBe(true);

      store.connectNodes({ sourceNodeId: bravo.id, targetNodeId: sw.id });
      expect(ifaces(bravo.id).at(-1)).toMatchObject({ proto: 'static' });
      expect(ifaces(bravo.id).at(-1)).not.toHaveProperty('address');
    } finally {
      resetSettings(null);
    }
  });
});
