// What the info tooltip of a device or a switch shows, and the sentence
// that says the same to assistive technology.

import { describe, expect, test } from 'vitest';

import {
  cut,
  deviceInfo,
  interfaceAddress,
  switchInfo,
} from '@/builder/nodeInfo.js';

function device(spec) {
  return { id: 'n1', kind: 'device', device: { hostname: 'web-01', spec } };
}

// The value of a row, by its label.
function row(info, label) {
  return info.rows.find((entry) => entry.label === label)?.lines;
}

describe('cut', () => {
  test('a text of 80 characters stays whole, and one of 81 is cut after 80', () => {
    const eighty = 'x'.repeat(80);

    expect(cut(eighty)).toBe(eighty);
    expect(cut(`${eighty}y`)).toBe(`${eighty}…`);
    expect(cut('short')).toBe('short');
  });

  test('white space is one space, and none at the ends', () => {
    expect(cut('  Front\tend\n\n web   server ')).toBe('Front end web server');
    // Counted after the white space is gone.
    expect(cut(`${'a '.repeat(40)}   `)).toBe('a '.repeat(40).trim());
  });

  test('characters are counted, not their code units', () => {
    const faces = '😀'.repeat(81);

    expect(cut(faces)).toBe(`${'😀'.repeat(80)}…`);
    expect(cut('😀'.repeat(80))).toBe('😀'.repeat(80));
  });

  test('nothing is an empty text, and the length can be given', () => {
    expect(cut(undefined)).toBe('');
    expect(cut(null)).toBe('');
    expect(cut('   ')).toBe('');
    expect(cut('abcdef', 3)).toBe('abc…');
  });
});

describe('interfaceAddress', () => {
  test.each([
    [{ address: '10.0.0.5', mask: 24 }, '10.0.0.5/24'],
    [{ address: '10.0.0.5' }, '10.0.0.5'],
    [{ address: '10.0.0.5', mask: null }, '10.0.0.5'],
    [{ address: '10.0.0.5', mask: '' }, '10.0.0.5'],
    [{ address: ' 10.0.0.5 ', mask: 0 }, '10.0.0.5/0'],
    [{ address: '2001:db8::1', mask: 64 }, '2001:db8::1/64'],
    // An address written with its mask is shown as it is.
    [{ address: '10.0.0.5/24', mask: 24 }, '10.0.0.5/24'],
    [{ proto: 'dhcp' }, 'DHCP'],
    [{ proto: ' DHCP ', address: '' }, 'DHCP'],
    // phenix gives a DHCP interface no address of its own, so one left in
    // the spec is not shown.
    [{ proto: 'dhcp', address: '10.0.0.5', mask: 24 }, 'DHCP'],
    [{ proto: 'static', address: '10.0.0.5', mask: 24 }, '10.0.0.5/24'],
    [{ type: 'serial' }, 'serial'],
    [{ type: 'serial', address: '' }, 'serial'],
    [{ type: 'ethernet', proto: 'manual' }, 'no address'],
    [{ address: '', mask: 24 }, 'no address'],
    [{ address: null }, 'no address'],
    [{}, 'no address'],
    [undefined, 'no address'],
  ])('%j is %s', (iface, shown) => {
    expect(interfaceAddress(iface)).toBe(shown);
  });
});

describe('deviceInfo', () => {
  test('a device shows its description, its interfaces with their addresses and its OS type', () => {
    const info = deviceInfo(
      device({
        general: { hostname: 'web-01', description: ' Front end\nweb server ' },
        hardware: { os_type: 'linux' },
        network: {
          interfaces: [
            { name: 'eth0', address: '10.0.0.5', mask: 24 },
            { name: 'eth1', proto: 'dhcp' },
            { name: 'S0', type: 'serial' },
            { name: 'eth2' },
          ],
        },
      }),
    );

    expect(info.rows).toEqual([
      { label: 'Description', lines: ['Front end web server'] },
      {
        label: 'Interfaces',
        lines: [
          'eth0 — 10.0.0.5/24',
          'eth1 — DHCP',
          'S0 — serial',
          'eth2 — no address',
        ],
      },
      { label: 'OS type', lines: ['linux'] },
    ]);
    // The description is not said again: the node's name ends with it.
    expect(info.text).toBe(
      'Interfaces: eth0 10.0.0.5/24, eth1 DHCP, S0 serial, eth2 no address. OS type linux.',
    );
  });

  test('a description is cut to 80 characters', () => {
    const info = deviceInfo(
      device({ general: { description: 'd'.repeat(200) } }),
    );

    expect(row(info, 'Description')).toEqual([`${'d'.repeat(80)}…`]);
  });

  test('a device with nothing to show says so in every row', () => {
    for (const spec of [
      // An external device has no hardware.
      { external: true, general: { hostname: 'plc', description: '' } },
      { network: { interfaces: [] }, hardware: { os_type: '' } },
      { network: { interfaces: null }, hardware: null },
      undefined,
    ]) {
      const info = deviceInfo(device(spec));

      expect(info.rows).toEqual([
        { label: 'Description', lines: ['None'] },
        { label: 'Interfaces', lines: ['None'] },
        { label: 'OS type', lines: ['Not set'] },
      ]);
      expect(info.text).toBe('No interfaces. OS type not set.');
    }

    expect(deviceInfo(undefined).rows).toHaveLength(3);
  });

  test('eight interfaces are listed whole, and a ninth is counted', () => {
    const interfaces = (n) =>
      Array.from({ length: n }, (_, index) => ({
        name: `eth${index}`,
        address: `10.0.0.${index + 1}`,
        mask: 24,
      }));
    const eight = deviceInfo(
      device({ network: { interfaces: interfaces(8) } }),
    );
    const nine = deviceInfo(device({ network: { interfaces: interfaces(9) } }));
    const twelve = deviceInfo(
      device({ network: { interfaces: interfaces(12) } }),
    );

    expect(row(eight, 'Interfaces')).toHaveLength(8);
    expect(row(eight, 'Interfaces')[7]).toBe('eth7 — 10.0.0.8/24');
    expect(eight.text).not.toContain('more');

    expect(row(nine, 'Interfaces')).toHaveLength(9);
    expect(row(nine, 'Interfaces').slice(7)).toEqual([
      'eth7 — 10.0.0.8/24',
      '+1 more',
    ]);
    expect(nine.text).toContain('eth7 10.0.0.8/24, and 1 more. OS type');
    expect(nine.text).not.toContain('eth8');

    expect(row(twelve, 'Interfaces').at(-1)).toBe('+4 more');
    expect(twelve.text).toContain(', and 4 more.');
  });

  test('an interface without a name is still listed', () => {
    const info = deviceInfo(
      device({ network: { interfaces: [{ address: '10.0.0.5' }, null] } }),
    );

    expect(row(info, 'Interfaces')).toEqual([
      'unnamed — 10.0.0.5',
      'unnamed — no address',
    ]);
  });
});

describe('switchInfo', () => {
  const network = {
    id: 'net-1',
    name: 'EXP',
    alias: 100,
    description: 'Experiment  network',
  };
  const devices = (n) =>
    Array.from({ length: n }, (_, index) => ({
      label: `web-${String(index + 1).padStart(2, '0')}`,
      addresses: [`10.0.0.${index + 1}/24`],
    }));

  test('a switch shows its network, alias, description and connected devices', () => {
    const info = switchInfo(network, [
      { label: 'web-02', addresses: ['DHCP'] },
      { label: 'web-01', addresses: ['10.0.0.5/24'] },
    ]);

    expect(info.rows).toEqual([
      { label: 'Network', lines: ['EXP'] },
      { label: 'VLAN alias', lines: ['100'] },
      { label: 'Description', lines: ['Experiment network'] },
      {
        label: 'Connected devices (2)',
        lines: ['web-01 — 10.0.0.5/24', 'web-02 — DHCP'],
      },
    ]);
    // The network and the alias are not said again: the node's name says
    // both.
    expect(info.text).toBe(
      'Description: Experiment network. 2 connected devices: web-01 10.0.0.5/24, web-02 DHCP.',
    );
  });

  test('a switch without an alias, a description or devices says so', () => {
    const info = switchInfo({ id: 'net-1', name: 'EXP', description: '' });

    expect(info.rows).toEqual([
      { label: 'Network', lines: ['EXP'] },
      { label: 'VLAN alias', lines: ['no alias'] },
      { label: 'Description', lines: ['None'] },
      { label: 'Connected devices (0)', lines: ['None'] },
    ]);
    expect(info.text).toBe('No connected devices.');
  });

  test('a switch whose network is gone says it has none', () => {
    const info = switchInfo(undefined, []);

    expect(row(info, 'Network')).toEqual(['No network']);
    expect(row(info, 'VLAN alias')).toEqual(['no alias']);
    expect(info.text).toBe('No connected devices.');
  });

  test('a description is cut to 80 characters', () => {
    const info = switchInfo({ name: 'EXP', description: 'd'.repeat(81) }, []);

    expect(row(info, 'Description')).toEqual([`${'d'.repeat(80)}…`]);
    expect(info.text).toBe(
      `Description: ${'d'.repeat(80)}…. No connected devices.`,
    );
  });

  test('one device is one device', () => {
    const info = switchInfo(network, devices(1));

    expect(row(info, 'Connected devices (1)')).toEqual([
      'web-01 — 10.0.0.1/24',
    ]);
    expect(info.text).toContain('1 connected device: web-01 10.0.0.1/24.');
  });

  test('eight devices are listed whole, and a ninth is counted', () => {
    const eight = switchInfo(network, devices(8));
    const nine = switchInfo(network, devices(9));

    expect(row(eight, 'Connected devices (8)')).toHaveLength(8);
    expect(eight.text).not.toContain('more');

    expect(row(nine, 'Connected devices (9)')).toEqual([
      ...devices(8).map((entry) => `${entry.label} — ${entry.addresses[0]}`),
      '+1 more',
    ]);
    expect(nine.text).toContain(
      '9 connected devices: web-01 10.0.0.1/24, web-02 10.0.0.2/24,',
    );
    expect(nine.text).toContain('web-08 10.0.0.8/24, and 1 more.');
    expect(nine.text).not.toContain('web-09');

    const twenty = switchInfo(network, devices(20));

    expect(row(twenty, 'Connected devices (20)').at(-1)).toBe('+12 more');
  });

  test('a device connected twice is one device with both addresses', () => {
    const info = switchInfo(network, [
      { label: 'router', addresses: ['10.0.0.1/24', '10.0.1.1/24'] },
    ]);

    expect(row(info, 'Connected devices (1)')).toEqual([
      'router — 10.0.0.1/24, 10.0.1.1/24',
    ]);
    expect(info.text).toContain(
      '1 connected device: router 10.0.0.1/24, 10.0.1.1/24.',
    );
  });

  test('devices are in the order of their names, numbers by value', () => {
    const connected = ['web-10', 'Web-2', 'db', 'web-1'].map((label) => ({
      label,
      addresses: ['DHCP'],
    }));
    const info = switchInfo(network, connected);

    expect(row(info, 'Connected devices (4)')).toEqual([
      'db — DHCP',
      'web-1 — DHCP',
      'Web-2 — DHCP',
      'web-10 — DHCP',
    ]);
    // The list given is left as it was.
    expect(connected.map((entry) => entry.label)).toEqual([
      'web-10',
      'Web-2',
      'db',
      'web-1',
    ]);
  });

  test('a device with no address listed is named alone', () => {
    const info = switchInfo(network, [{ label: 'web-01', addresses: [] }]);

    expect(row(info, 'Connected devices (1)')).toEqual(['web-01']);
    expect(info.text).toContain('1 connected device: web-01.');
  });
});
