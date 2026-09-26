import { describe, expect, test } from 'vitest';

import {
  closestBranch,
  createFormValidator,
  errorMessage,
  fieldErrors,
  fieldLabel,
  isGoDuration,
  isIPv4Network,
  itemNoun,
  startCase,
  workingCopyErrors,
} from '@/builder/form-validator.js';
import { builderSchemaV1, schemaForKind } from '@/builder/schema.js';

import { experimentNodeSpec } from './fixtures.js';

const device = schemaForKind(builderSchemaV1, 'device');
const network = schemaForKind(builderSchemaV1, 'switch');

// The ajv errors the Inspector's form reports for `data`.
function errorsFor(schema, data) {
  const validate = createFormValidator().compile(schema);

  validate(data);

  return validate.errors || [];
}

function deviceData({ hostname = 'web01', drives, interfaces = [] } = {}) {
  return {
    hostname,
    spec: {
      type: 'VirtualMachine',
      // Apply copies the device hostname here; the form shows it read-only.
      general: { hostname: 'web01' },
      hardware: {
        os_type: 'linux',
        drives: drives ?? [{ image: 'ubuntu.qc2' }],
      },
      network: { interfaces },
    },
  };
}

describe('field names', () => {
  test('come from schema titles, or the start-cased key', () => {
    expect(startCase('os_type')).toBe('Os Type');
    expect(startCase('useUUID')).toBe('Use UUID');
    expect(fieldLabel(device, 'hostname')).toEqual({
      label: 'Hostname',
      context: '',
    });
    expect(fieldLabel(device, 'spec.hardware.os_type').label).toBe('OS type');
  });

  test('name the list item a field sits in', () => {
    expect(fieldLabel(device, 'spec.hardware.drives.1.image')).toEqual({
      label: 'Image',
      context: 'Drive 2',
    });
    // A primitive list item is named by its position.
    expect(fieldLabel(device, 'spec.commands.0')).toEqual({
      label: 'Command 1',
      context: '',
    });
    // Interface fields live in the oneOf branches.
    expect(fieldLabel(device, 'spec.network.interfaces.0.vlan')).toEqual({
      label: 'VLAN',
      context: 'Interface 1',
    });
  });

  test('singular item nouns keep acronyms', () => {
    expect(itemNoun('Drives')).toBe('drive');
    expect(itemNoun('C2 dependencies')).toBe('C2 dependency');
    expect(itemNoun('Area networks')).toBe('area network');
  });
});

describe('plain-language errors', () => {
  test('name the field and say what a valid value looks like', () => {
    const [pattern] = errorsFor(device, deviceData({ hostname: 'bad host' }));
    const missing = errorsFor(device, { spec: deviceData().spec }).find(
      (error) => error.keyword === 'required',
    );
    const [range] = errorsFor(network, { name: 'EXP', alias: 99999 });

    expect(errorMessage(pattern, device)).toBe(
      'Hostname cannot contain spaces',
    );
    expect(errorMessage(missing, device)).toBe('Hostname is required');
    expect(errorMessage(range, network)).toBe(
      'VLAN alias must be between 1 and 4094',
    );
  });

  // The Inspector bounds the MTU (see SPEC_BOUNDS in schema.js).
  test('say what range a number must be in', () => {
    const iface = { name: 'eth0', type: 'ethernet', proto: 'dhcp', vlan: 'A' };
    const errors = errorsFor(
      device,
      deviceData({ interfaces: [{ ...iface, mtu: -1 }] }),
    );

    expect(fieldErrors(errors, device).map((error) => error.message)).toEqual([
      'Interface 1: MTU must be between 0 and 16000',
    ]);
  });

  // The Inspector checks the text phenix parses (see SPEC_FORMATS in
  // schema.js).
  test('say what a duration, an address and a network look like', () => {
    const { spec } = deviceData();
    const errors = errorsFor(device, {
      ...deviceData(),
      spec: {
        ...spec,
        delay: { timer: '5 minutes' },
        network: {
          ...spec.network,
          routes: [{ destination: '10.0.0.1/24', next: '10.0.0' }],
          ospf: {
            router_id: '1',
            areas: [{ area_id: 0, area_networks: [{ network: '10.1.25.0' }] }],
          },
        },
      },
    });

    expect(fieldErrors(errors, device).map((error) => error.message)).toEqual([
      'Timer must be a duration, such as 30s, 5m or 1h30m',
      'Area 1, Area network 1: Network must be an IPv4 network, with the host part zero, such as 10.0.0.0/24',
      'Router ID must be an IPv4 address, such as 10.0.0.1',
      'Route 1: Destination must be an IPv4 network, with the host part zero, such as 10.0.0.0/24',
      'Route 1: Next hop must be an IPv4 address, such as 10.0.0.1',
    ]);

    // Go's time.ParseDuration reads these, and IPv4 networks are what
    // net.ParseCIDR reads with no host bits set.
    const reads = (check, texts, expected) =>
      texts.forEach((text) => expect(check(text), text).toBe(expected));

    reads(
      isGoDuration,
      ['0', '-0', '5m', '1h30m', '250ms', '-1.5h', '.5s', '5.s', '2562047h'],
      true,
    );
    reads(isGoDuration, ['1h2m3s4ms5us6ns', '12\u00b5s', '12\u03bcs'], true);
    reads(
      isGoDuration,
      ['', '5', '00', '5 m', ' 5m', '5M', '1d', '.s', '-', '5mh', '1.2.3s'],
      false,
    );
    // More than a Go duration holds.
    reads(isGoDuration, ['2562048h'], false);
    reads(
      isIPv4Network,
      ['10.0.0.0/24', '0.0.0.0/0', '192.168.1.128/25', '255.255.255.255/32'],
      true,
    );
    reads(
      isIPv4Network,
      ['10.0.0.1/24', '10.0.0.0', '10.0.0.0/33', '010.0.0.0/8', '10.0.0/24'],
      false,
    );
    reads(isIPv4Network, ['fd00::/8', ' 10.0.0.0/24'], false);
  });

  test('the summary has one entry per field, with its list item', () => {
    const errors = errorsFor(
      device,
      deviceData({ hostname: 'bad host', drives: [{ image: '' }] }),
    );

    expect(fieldErrors(errors, device)).toEqual([
      {
        path: 'hostname',
        label: 'Hostname',
        message: 'Hostname cannot contain spaces',
      },
      {
        path: 'spec.hardware.drives.0.image',
        label: 'Image',
        message: 'Drive 1: Image cannot be empty',
      },
    ]);
  });

  // A static interface without its address and mask fails all three
  // interface variants; the summary lists what the static variant needs,
  // not the DHCP variant's "proto" mismatch or the serial variant's fields.
  // The form leaves its VLAN optional (see optionalVLANs in schema.js).
  test('a failed oneOf reports only the closest branch', () => {
    const iface = { name: 'eth0', type: 'ethernet', proto: 'static' };
    const errors = errorsFor(device, deviceData({ interfaces: [iface] }));

    expect(fieldErrors(errors, device).map((error) => error.message)).toEqual([
      'Interface 1: Address is required',
      'Interface 1: Mask is required',
    ]);

    const branches =
      device.properties.spec.properties.network.properties.interfaces.items
        .oneOf;

    expect(closestBranch(createFormValidator(), branches, iface)).toBe(0);
    expect(
      closestBranch(createFormValidator(), branches, {
        ...iface,
        proto: 'dhcp',
      }),
    ).toBe(1);
    expect(closestBranch(createFormValidator(), branches, undefined)).toBe(
      undefined,
    );
  });
});

// Errors an element had already, on fields an edit leaves as they were,
// do not keep the edit from being applied. A node spec of a phenix
// experiment has several: phenix accepts it, the form's schema does not.
describe('errors that count against a working copy', () => {
  const base = {
    hostname: 'host-00',
    iconKey: '',
    spec: experimentNodeSpec(),
  };
  const edit = (change) => {
    const data = JSON.parse(JSON.stringify(base));

    change(data.spec, data);

    return workingCopyErrors(createFormValidator(), device, data, base);
  };
  const messages = (result) => result.fields.map((error) => error.message);

  test('an imported node spec has errors in the form', () => {
    expect(
      fieldErrors(errorsFor(device, base), device).map((e) => e.message),
    ).toEqual([
      'Advanced settings must be a group of fields',
      'Interface 1: MAC address must be a MAC address, such as 00:11:22:33:44:55',
      'Interface 1: Gateway must be at least 7 characters',
      'Interface 1: Inbound ruleset can use only letters, digits, underscores and hyphens',
      'Interface 1: Outbound ruleset can use only letters, digits, underscores and hyphens',
      'OSPF must be a group of fields',
    ]);
    // Nothing edited, nothing counts.
    expect(edit(() => {})).toEqual({ errors: [], fields: [] });
  });

  test('an edit elsewhere is not held back by them', () => {
    expect(
      edit((spec, data) => {
        spec.general.description = 'Imported';
        spec.network.interfaces[0].address = '10.0.0.2';
        data.hostname = 'host-01';
      }),
    ).toEqual({ errors: [], fields: [] });
  });

  test('an error on a field the edit changed counts', () => {
    const result = edit((spec) => {
      spec.network.interfaces[0].mac = 'zz';
    });

    expect(messages(result)).toEqual([
      'Interface 1: MAC address must be a MAC address, such as 00:11:22:33:44:55',
    ]);
    // For JSON Forms: the error in each interface alternative, and the
    // oneOf they are under, so the field shows only its own alternative's.
    expect(
      result.errors.map((error) => `${error.instancePath} ${error.keyword}`),
    ).toEqual([
      ...Array(3).fill('/spec/network/interfaces/0/mac pattern'),
      '/spec/network/interfaces/0 oneOf',
    ]);
  });

  test('an error the edit brought about elsewhere counts', () => {
    expect(
      messages(
        edit((spec) => {
          delete spec.network.interfaces[0].address;
        }),
      ),
    ).toEqual(['Interface 1: Address is required']);
  });

  // An item the edit adds may have an error another item had already.
  test('an error of an added list item counts', () => {
    const missing = JSON.parse(JSON.stringify(base));

    delete missing.spec.network.interfaces[0].address;

    const data = JSON.parse(JSON.stringify(missing));

    // As Add interface makes it, then made static.
    data.spec.network.interfaces.push({
      name: 'eth1',
      type: 'ethernet',
      proto: 'static',
      mask: 24,
      vlan: 'EXP',
    });

    expect(
      workingCopyErrors(
        createFormValidator(),
        device,
        data,
        missing,
      ).fields.map((error) => error.message),
    ).toEqual(['Interface 2: Address is required']);
  });

  test('an error an item had still does not count once the item moves', () => {
    expect(
      edit((spec) => {
        spec.network.interfaces.unshift({
          ...spec.network.interfaces[0],
          name: 'eth1',
        });
        spec.network.interfaces.splice(0, 1);
      }).fields,
    ).toEqual([]);
  });
});
