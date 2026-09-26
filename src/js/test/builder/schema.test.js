import { describe, expect, test } from 'vitest';

import { createFormValidator } from '@/builder/form-validator.js';
import {
  builderSchemaV1,
  BUILDER_SCHEMA_ID,
  deref,
  EXTERNAL_NODE_DEF,
  MAP_KEYS_KEYWORD,
  MAP_KEYWORD,
  MINIMEGA_NODE_DEF,
  normalizeSchemaBundle,
  offeredIconKeys,
  resolveRef,
  schemaForKind,
  specDefName,
  specSchema,
  SUGGESTIONS_KEYWORD,
} from '@/builder/schema.js';

describe('bundled builder schema', () => {
  test('is the server schema, keyed by $defs', () => {
    expect(builderSchemaV1.$id).toBe(BUILDER_SCHEMA_ID);
    expect(builderSchemaV1.$schema).toContain('2020-12');
    expect(builderSchemaV1.$defs).toBeTruthy();
    expect(builderSchemaV1.definitions).toBeUndefined();
  });

  test('carries the phenix node schemas the inspector needs', () => {
    expect(builderSchemaV1.$defs[MINIMEGA_NODE_DEF]).toBeTruthy();
    expect(builderSchemaV1.$defs[EXTERNAL_NODE_DEF]).toBeTruthy();
  });

  test('an unusable server payload falls back to the bundle', () => {
    expect(normalizeSchemaBundle(null)).toBe(builderSchemaV1);
    expect(normalizeSchemaBundle({ definitions: {} })).toBe(builderSchemaV1);
    expect(normalizeSchemaBundle({ $defs: { a: {} } }).$defs.a).toEqual({});
  });

  test('refs resolve inside the bundle', () => {
    const resolved = resolveRef(
      builderSchemaV1,
      `#/$defs/${MINIMEGA_NODE_DEF}`,
    );

    expect(resolved.type).toBe('object');
    expect(deref(builderSchemaV1, { $ref: '#/$defs/network' }).type).toBe(
      'object',
    );
  });
});

describe('inspector schemas', () => {
  test('retired icon keys are offered only to nodes that already use them', () => {
    const offered = (current) =>
      schemaForKind(builderSchemaV1, 'device', { iconKey: current }).properties
        .iconKey.enum;

    expect(builderSchemaV1.$defs.iconKey.enum).toContain('printer');
    expect(offered('')).not.toContain('printer');
    expect(offered('router')).not.toContain('printer');
    expect(offered('router')).toContain('router');
    expect(offered('printer')).toContain('printer');
    expect(offeredIconKeys(undefined)).toEqual({ type: 'string' });
    expect(offeredIconKeys({ type: 'string' })).toEqual({ type: 'string' });
  });

  test('a device exposes the complete phenix node spec', () => {
    const schema = schemaForKind(builderSchemaV1, 'device');
    const spec = schema.properties.spec;

    expect(schema.required).toContain('spec');
    expect(Object.keys(spec.properties)).toEqual(
      expect.arrayContaining(['general', 'hardware', 'network']),
    );
  });

  // R35: the Inspector asks for its schema on every document change, and a
  // new object each time was compiled again by JSON Forms and kept by ajv.
  test('an element kind has one schema object, compiled once', () => {
    const validator = createFormValidator();
    const device = (context) =>
      schemaForKind(builderSchemaV1, 'device', context);
    const first = device({ spec: { type: 'VirtualMachine' }, iconKey: 'pc' });

    expect(device({ spec: { type: 'Router' }, iconKey: 'router' })).toBe(first);
    expect(schemaForKind({ schema: builderSchemaV1 }, 'device')).toBe(first);
    expect(device({ spec: { external: true } })).not.toBe(first);
    expect(device({ iconKey: 'printer' })).not.toBe(first);
    expect(schemaForKind(builderSchemaV1, 'switch')).toBe(
      schemaForKind(builderSchemaV1, 'switch'),
    );

    validator.compile(first);
    const compiled = validator._cache.size;

    for (let edit = 0; edit < 20; edit += 1) {
      validator.compile(device({ spec: { type: 'VirtualMachine' } }));
    }

    expect(validator._cache.size).toBe(compiled);
  });

  // R34: labels and annotations are node fields phenix reads that its
  // schema leaves out, and `advanced` has no fields of its own: each is
  // edited as keys and values. An imported node's are null.
  test('labels, annotations and advanced settings are maps of keys and values', () => {
    const minimega = deviceSpec({});
    const external = deviceSpec({ spec: { external: true } });

    for (const spec of [minimega, external]) {
      expect(spec.properties.labels).toMatchObject({
        title: 'Labels',
        type: ['object', 'null'],
        additionalProperties: { type: 'string' },
        [MAP_KEYWORD]: 'text',
      });
      expect(spec.properties.annotations).toMatchObject({
        title: 'Annotations',
        type: ['object', 'null'],
        [MAP_KEYWORD]: 'value',
      });
      expect(spec.properties.annotations[MAP_KEYS_KEYWORD]).toContain(
        'phenix/default-apps',
      );
    }

    expect(minimega.properties.advanced).toMatchObject({
      title: 'Advanced settings',
      type: 'object',
      [MAP_KEYWORD]: 'text',
    });
    expect(minimega.properties.advanced[MAP_KEYS_KEYWORD]).toContain(
      'qemu-append',
    );
    expect(external.properties.advanced).toBeUndefined();
  });

  // JSON Forms compiles each oneOf branch with ajv on its own. Branches that
  // point at #/$defs cannot compile, and the inspector then re-renders a
  // device with two or more interfaces forever (the tab freezes).
  test('inspector schemas are self-contained so oneOf branches compile alone', () => {
    const refs = [];
    const branches = [];
    const walk = (value, path) => {
      if (Array.isArray(value)) {
        value.forEach((item, index) => walk(item, `${path}/${index}`));
        return;
      }
      if (!value || typeof value !== 'object') {
        return;
      }
      if (typeof value.$ref === 'string') {
        refs.push(`${path}: ${value.$ref}`);
      }
      if (Array.isArray(value.oneOf)) {
        branches.push(...value.oneOf);
      }
      for (const [key, child] of Object.entries(value)) {
        walk(child, `${path}/${key}`);
      }
    };

    for (const [kind, context] of [
      ['device', {}],
      ['device', { spec: { external: true } }],
      ['switch', {}],
      ['note', {}],
      ['group', {}],
      ['edge', {}],
      ['document', {}],
    ]) {
      walk(schemaForKind(builderSchemaV1, kind, context), kind);
    }

    expect(refs).toEqual([]);
    expect(branches.length).toBeGreaterThan(0);
    const ajv = createFormValidator();
    for (const branch of branches) {
      expect(() => ajv.compile(branch)).not.toThrow();
    }
  });

  // Generated labels were "Vm Type", "C 2" and "Qinq", and oneOf pickers
  // offered "oneOf-0".
  test('device spec fields have readable labels and named alternatives', () => {
    const spec = schemaForKind(builderSchemaV1, 'device').properties.spec;
    const { general, hardware, network } = spec.properties;
    const iface = network.properties.interfaces.items;

    expect(general.properties.vm_type.title).toBe('VM type');
    // Apply copies the device Hostname into the spec, so its copy there is
    // shown read-only and nothing about it can block Apply.
    expect(general.properties.hostname).toMatchObject({
      title: 'Node hostname',
      readOnly: true,
    });
    expect(general.properties.hostname.pattern).toBeUndefined();
    expect(general.required || []).not.toContain('hostname');
    expect(spec.properties.delay.properties.c2.title).toBe('C2 dependencies');
    expect(hardware.properties.os_type.title).toBe('OS type');
    expect(hardware.properties.memory.oneOf.map((b) => b.title)).toEqual([
      'Megabytes',
      'Text',
    ]);
    expect(hardware.properties.vcpus.oneOf.map((b) => b.title)).toEqual([
      'Whole number',
      'Text',
    ]);
    // Named by their key ("Interfaces"), apart from the Inspector's
    // Connection points by their section.
    expect(network.properties.interfaces.title).toBeUndefined();
    expect(iface.title).toBe('Interface kind');
    expect(iface.oneOf.map((b) => b.title)).toEqual([
      'Ethernet, static or OSPF',
      'Ethernet, DHCP or manual',
      'Serial, static',
    ]);
    // Error messages name them too ("Injection 1: Source is required").
    const injection = spec.properties.injections.items.properties;

    expect([injection.src.title, injection.dst.title]).toEqual([
      'Source',
      'Destination',
    ]);
  });

  test('external nodes get the external node schema', () => {
    expect(specDefName({ external: true })).toBe(EXTERNAL_NODE_DEF);
    expect(specDefName({ type: 'VirtualMachine' })).toBe(MINIMEGA_NODE_DEF);

    const schema = specSchema(builderSchemaV1, { external: true });

    expect(schema.title).toBe('Node');
    expect(schema.properties).toBeTruthy();
  });

  test('a switch edits its network, including the VLAN alias', () => {
    const schema = schemaForKind(builderSchemaV1, 'switch');

    expect(schema.title).toBe('Network');
    expect(schema.required).toContain('name');
    expect(schema.properties.alias.title).toBe('VLAN alias');
  });

  test('notes, groups, edges and the document each have a schema', () => {
    ['note', 'group', 'edge', 'document'].forEach((kind) => {
      const schema = schemaForKind(builderSchemaV1, kind);

      expect(schema.type).toBe('object');
      expect(Object.keys(schema.properties).length).toBeGreaterThan(0);
    });

    // A connection has a color of its own, as networks, notes and groups do.
    expect(
      Object.keys(schemaForKind(builderSchemaV1, 'edge').properties),
    ).toEqual(['label', 'color']);
  });

  test('identifiers and geometry are never editable fields', () => {
    ['device', 'switch', 'note', 'group', 'edge'].forEach((kind) => {
      const schema = schemaForKind(builderSchemaV1, kind);

      expect(schema.properties.id).toBeUndefined();
      expect(schema.properties.position).toBeUndefined();
      expect(schema.properties.parentId).toBeUndefined();
    });
  });

  test('every inspector schema compiles with its JSON Forms validator', () => {
    const validator = createFormValidator();

    ['device', 'switch', 'note', 'group', 'edge', 'document'].forEach(
      (kind) => {
        expect(() =>
          validator.compile(schemaForKind(builderSchemaV1, kind)),
        ).not.toThrow();
      },
    );
  });
});

// The fields of a node spec as the Inspector's form shows them, by their data
// path without list indexes: those in list items and in each alternative of
// a oneOf too.
function specFields(spec) {
  const fields = [];
  const visit = (schema, trail) => {
    for (const [name, child] of Object.entries(schema?.properties || {})) {
      fields.push({ path: [...trail, name].join('.'), schema: child });
      visit(child, [...trail, name]);
    }

    if (schema?.items && typeof schema.items === 'object') {
      visit(schema.items, trail);
    }

    for (const branch of [...(schema?.oneOf || []), ...(schema?.anyOf || [])]) {
      visit(branch, trail);
    }
  };

  visit(spec, []);

  return fields;
}

const deviceSpec = (context) =>
  schemaForKind(builderSchemaV1, 'device', context).properties.spec;

function fieldAt(spec, path) {
  return specFields(spec).find((field) => field.path === path)?.schema;
}

describe('node spec fields in the Inspector', () => {
  // Their descriptions are the Inspector's tooltips. A group (an object with
  // fields) has none of its own, and nor has DNS, whose description is on
  // the first alternative of its nullable union.
  test('every field of both node kinds has a description', () => {
    for (const spec of [
      deviceSpec({}),
      deviceSpec({ spec: { external: true } }),
    ]) {
      const undescribed = specFields(spec)
        .filter(({ schema }) => !schema.properties && !schema.anyOf)
        .filter(({ schema }) => !schema.description)
        .map(({ path }) => path);

      expect(undescribed).toEqual([]);
    }
  });

  test('snapshot defaults to on, as phenix sets it', () => {
    const general =
      builderSchemaV1.$defs[MINIMEGA_NODE_DEF].properties.general.properties;

    expect(general.snapshot.default).toBe(true);
    expect(general.do_not_boot.default).toBe(false);
  });

  test('numeric fields are bounded in the form, where phenix acts on them', () => {
    const spec = deviceSpec({});
    const bounds = (path) => {
      const field = fieldAt(spec, path);
      const numeric = [field, ...(field.oneOf || [])].find((schema) =>
        [schema.type].flat().includes('integer'),
      );

      return [numeric.minimum, numeric.maximum];
    };

    expect(bounds('network.interfaces.mtu')).toEqual([0, 16000]);
    expect(bounds('hardware.vcpus')).toEqual([1, undefined]);
    expect(bounds('hardware.memory')).toEqual([1, undefined]);
    expect(bounds('hardware.drives.inject_partition')).toEqual([0, undefined]);
    expect(bounds('network.routes.cost')).toEqual([1, 255]);
    expect(bounds('network.ospf.hello_interval')).toEqual([1, 65535]);
    expect(bounds('network.ospf.areas.area_id')).toEqual([0, 4294967295]);
    expect(bounds('network.rulesets.rules.id')).toEqual([1, 999999]);
    expect(bounds('network.rulesets.rules.source.port')).toEqual([0, 65535]);
    expect(bounds('network.rulesets.rules.destination.port')).toEqual([
      0, 65535,
    ]);
    // Bounds the schema sets itself stay.
    expect(bounds('network.interfaces.mask')).toEqual([0, 32]);
    expect(bounds('network.interfaces.udp_port')).toEqual([0, 65535]);
  });

  test('the form refuses out-of-range values that documents still accept', () => {
    const validate = createFormValidator().compile(deviceSpec({}));
    const withMtu = (mtu) => ({
      type: 'VirtualMachine',
      general: { hostname: 'web01' },
      hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
      network: {
        interfaces: [
          { name: 'eth0', type: 'ethernet', proto: 'dhcp', vlan: 'EXP', mtu },
        ],
      },
    });

    expect(validate(withMtu(-1))).toBe(false);
    expect(validate(withMtu(16001))).toBe(false);
    expect(validate(withMtu(0))).toBe(true);
    expect(validate(withMtu(9000))).toBe(true);

    // The bundled document schema has no such bounds, so a config imported
    // with another value still opens.
    const iface = builderSchemaV1.$defs['phenix.v1.iface'].properties;

    expect(iface.mtu.minimum).toBeUndefined();
    expect(
      createFormValidator().validate(
        { $defs: builderSchemaV1.$defs, $ref: `#/$defs/${MINIMEGA_NODE_DEF}` },
        withMtu(-1),
      ),
    ).toBe(true);
  });

  test('delay timers, routes and OSPF settings are checked in the form, as phenix reads them', () => {
    const spec = deviceSpec({});
    const format = (path) => fieldAt(spec, path).format;

    expect(format('delay.timer')).toBe('go-duration');
    expect(format('network.routes.destination')).toBe('ipv4-network');
    expect(format('network.routes.next')).toBe('ipv4');
    expect(format('network.ospf.router_id')).toBe('ipv4');
    expect(format('network.ospf.areas.area_networks.network')).toBe(
      'ipv4-network',
    );

    const validate = createFormValidator().compile(spec);
    const withTimer = (timer) => ({
      type: 'VirtualMachine',
      general: { hostname: 'web01' },
      hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
      network: { interfaces: [] },
      delay: { timer },
    });

    expect(validate(withTimer('1h30m'))).toBe(true);
    // phenix reads an empty timer as no delay.
    expect(validate(withTimer(''))).toBe(true);
    expect(validate(withTimer('90 seconds'))).toBe(false);
    // Documents, and configs imported with such a value, still open.
    expect(
      createFormValidator().validate(
        { $defs: builderSchemaV1.$defs, $ref: `#/$defs/${MINIMEGA_NODE_DEF}` },
        withTimer('90 seconds'),
      ),
    ).toBe(true);
  });

  // A VLAN is its interface's connection, which an interface may not have
  // yet (R31). The diagram checks and publishing refuse it instead.
  test('the form leaves an interface VLAN optional, which the phenix schema requires', () => {
    const spec = deviceSpec({});
    const validate = createFormValidator().compile(spec);
    const withInterface = (iface) => ({
      type: 'VirtualMachine',
      general: { hostname: 'web01' },
      hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
      network: { interfaces: [{ name: 'eth0', type: 'ethernet', ...iface }] },
    });
    const kinds = [
      { proto: 'dhcp' },
      { proto: 'static', address: '10.0.0.1', mask: 24 },
      {
        type: 'serial',
        proto: 'static',
        address: '10.0.0.1',
        mask: 24,
        udp_port: 8989,
        baud_rate: 9600,
        device: '/dev/ttyS0',
      },
    ];

    for (const kind of kinds) {
      expect(validate(withInterface({ ...kind, vlan: '' })), kind.proto).toBe(
        true,
      );
      expect(validate(withInterface(kind)), kind.proto).toBe(true);
    }

    const vlans = specFields(spec).filter(
      ({ path }) => path === 'network.interfaces.vlan',
    );

    expect(vlans).toHaveLength(3);
    expect(vlans.filter(({ schema }) => 'minLength' in schema)).toEqual([]);
    expect(
      createFormValidator().validate(
        { $defs: builderSchemaV1.$defs, $ref: `#/$defs/${MINIMEGA_NODE_DEF}` },
        withInterface({ ...kinds[0], vlan: '' }),
      ),
    ).toBe(false);
  });

  test("only a drive's image suggests the server's disk images", () => {
    const suggesting = specFields(deviceSpec({}))
      .filter(({ schema }) => schema[SUGGESTIONS_KEYWORD])
      .map(({ path, schema }) => [path, schema[SUGGESTIONS_KEYWORD]]);

    expect(suggesting).toEqual([['hardware.drives.image', 'disks']]);
  });
});
