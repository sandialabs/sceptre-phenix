import { describe, expect, test } from 'vitest';

import { createFormValidator } from '@/builder/form-validator.js';
import { BORDER_STYLES, LINE_STYLES } from '@/builder/model.js';
import {
  builderSchemaV1,
  EXTERNAL_NODE_DEF,
  MAP_KEYS_KEYWORD,
  MAP_KEYWORD,
  MINIMEGA_NODE_DEF,
  normalizeSchemaBundle,
  orderedFields,
  schemaForKind,
  specDefName,
  SUGGESTIONS_KEYWORD,
  UNSET_KEYWORD,
} from '@/builder/schema.js';

describe('bundled builder schema', () => {
  test('is the server schema, keyed by $defs', () => {
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
});

describe('inspector schemas', () => {
  test('every built-in icon key is offered, and the empty default', () => {
    const offered = schemaForKind(builderSchemaV1, 'device').properties.iconKey
      .enum;

    expect(offered).toEqual(builderSchemaV1.$defs.iconKey.enum);
    expect(offered).toEqual(expect.arrayContaining(['', 'printer']));
  });

  test('a device exposes the complete phenix node spec', () => {
    const schema = schemaForKind(builderSchemaV1, 'device');
    const spec = schema.properties.spec;

    expect(schema.required).toContain('spec');
    expect(Object.keys(spec.properties)).toEqual(
      expect.arrayContaining(['general', 'hardware', 'network']),
    );
  });

  // The Inspector asks for its schema on every document change, and a
  // new object each time was compiled again by JSON Forms and kept by ajv.
  test('an element kind has one schema object, compiled once', () => {
    const validator = createFormValidator();
    const device = (context) =>
      schemaForKind(builderSchemaV1, 'device', context);
    const first = device({ spec: { type: 'VirtualMachine' }, iconKey: 'pc' });

    expect(device({ spec: { type: 'Router' }, iconKey: 'router' })).toBe(first);
    expect(schemaForKind({ schema: builderSchemaV1 }, 'device')).toBe(first);
    expect(device({ spec: { external: true } })).not.toBe(first);
    expect(device({ iconKey: 'printer' })).toBe(first);
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

  // Labels, annotations and `advanced` have no fields of their own, so each
  // is edited as keys and values. An imported node's are null.
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
      type: ['object', 'null'],
      [MAP_KEYWORD]: 'text',
    });
    expect(minimega.properties.advanced[MAP_KEYS_KEYWORD]).toContain(
      'qemu-append',
    );
    expect(external.properties.advanced).toBeUndefined();
  });

  // JSON Forms compiles each oneOf branch alone, so a branch that points at
  // #/$defs cannot compile.
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
    // Each by what tells it apart, short enough for a narrow picker.
    expect(iface.oneOf.map((b) => b.title)).toEqual([
      'Static or OSPF',
      'DHCP or manual',
      'Serial',
    ]);
    // Error messages name them too ("Injection 1: Source is required").
    const injection = spec.properties.injections.items.properties;

    expect([injection.src.title, injection.dst.title]).toEqual([
      'Source',
      'Destination',
    ]);
  });

  // The Address of a static interface, the kind a new interface has, comes
  // first under the Interface kind picker.
  test('interface fields start with the address', () => {
    const fields = (schema) => Object.keys(schema.properties);
    const iface = (context) =>
      schemaForKind(builderSchemaV1, 'device', context).properties.spec
        .properties.network.properties.interfaces.items;
    const [staticKind, dhcpKind, serialKind] = iface({}).oneOf;

    expect(fields(staticKind).slice(0, 7)).toEqual([
      'address',
      'mask',
      'gateway',
      'proto',
      'name',
      'vlan',
      'mac',
    ]);
    expect(fields(staticKind)).toContain('type');
    // The other kinds keep the schema's order.
    expect(fields(dhcpKind)[0]).not.toBe('address');
    expect(fields(serialKind)[0]).not.toBe('address');
    // An external device's interface starts with its Protocol.
    expect(fields(iface({ spec: { external: true } })).slice(0, 6)).toEqual([
      'proto',
      'address',
      'mask',
      'gateway',
      'name',
      'vlan',
    ]);
  });

  test('external nodes get the external node schema', () => {
    expect(specDefName({ external: true })).toBe(EXTERNAL_NODE_DEF);
    expect(specDefName({ type: 'VirtualMachine' })).toBe(MINIMEGA_NODE_DEF);
  });

  test('a switch edits its network, including the VLAN alias', () => {
    const schema = schemaForKind(builderSchemaV1, 'switch');

    expect(schema.title).toBe('Network');
    expect(schema.required).toContain('name');
    expect(schema.properties.alias.title).toBe('VLAN alias');
  });

  // The network's color is labelled by what it colors, now that the switch
  // has an outline and a fill of its own.
  test("a switch's fields are its network's, then its own outline, fill and icon size", () => {
    const { properties } = schemaForKind(builderSchemaV1, 'switch');

    expect(Object.keys(properties)).toEqual([
      'name',
      'alias',
      'subnet',
      'gateway',
      'description',
      'color',
      'lineStyle',
      'outlineColor',
      'fillColor',
      'iconSize',
    ]);
    expect(
      ['color', 'lineStyle', 'outlineColor', 'fillColor'].map(
        (key) => properties[key].title,
      ),
    ).toEqual(['Edge Color', 'Line style', 'Outline Color', 'Fill Color']);
    // The network's color takes any CSS color. The node's own only #rrggbb.
    expect(properties.color.pattern).toBeUndefined();
    expect(properties.outlineColor.pattern).toBe('^(#[0-9a-fA-F]{6})?$');
    expect(properties.fillColor.pattern).toBe('^(#[0-9a-fA-F]{6})?$');
  });

  test("a device's icon is followed by its custom icon, its icon size and its outline and fill colors", () => {
    const { properties } = schemaForKind(builderSchemaV1, 'device');

    expect(Object.keys(properties)).toEqual([
      'hostname',
      'iconKey',
      'icon',
      'iconSize',
      'outlineColor',
      'fillColor',
      'spec',
    ]);
    expect(properties.outlineColor).toMatchObject({
      type: 'string',
      title: 'Outline Color',
      pattern: '^(#[0-9a-fA-F]{6})?$',
    });
    expect(properties.fillColor).toMatchObject({
      type: 'string',
      title: 'Fill Color',
      pattern: '^(#[0-9a-fA-F]{6})?$',
    });
    // Many fields apply at once, so none says so.
    for (const key of ['iconKey', 'icon', 'iconSize', 'outlineColor']) {
      expect(properties[key].description, key).not.toMatch(/Apply/);
    }
  });

  // The field's own schema says an icon id is what it takes: the
  // definition itself, not the reference the bundle holds at the property.
  test('a device and a group have a Custom icon, which takes an icon id', () => {
    const device = schemaForKind(builderSchemaV1, 'device').properties;
    const group = schemaForKind(builderSchemaV1, 'group').properties;

    expect(Object.keys(group)).toEqual([
      'title',
      'description',
      'color',
      'borderStyle',
      'iconKey',
      'icon',
      'iconSize',
    ]);

    for (const { icon } of [device, group]) {
      expect(icon).toMatchObject({
        type: 'string',
        title: 'Custom icon',
        pattern: builderSchemaV1.$defs.iconRef.pattern,
      });
      expect(icon.$ref).toBeUndefined();
    }

    for (const { icon } of [device, group]) {
      expect(icon.description).toBe(
        "An image of the server's icon library, drawn in place of the icon. The node names it, so a renamed icon keeps showing.",
      );
    }
    // The Icon field chooses the custom icon too, and says how.
    expect(device.iconKey.description).toBe(
      "Icon of the node on the canvas. Choose Custom icon… to use an image of the server's icon library.",
    );
    expect(group.iconKey.description).toBe(
      "Icon beside the group's title. Choose Custom icon… to use an image of the server's icon library.",
    );
    // A switch and a note have none.
    expect(schemaForKind(builderSchemaV1, 'switch').properties.icon).toBe(
      undefined,
    );
    expect(schemaForKind(builderSchemaV1, 'note').properties.icon).toBe(
      undefined,
    );
    // A served schema without the definition still gives a text field.
    expect(
      schemaForKind({ $defs: { device: {} } }, 'device').properties.icon,
    ).toMatchObject({ type: 'string', title: 'Custom icon' });
  });

  // The choice that stands for no style is "Auto", not "Not set".
  test('a line style is one of four named patterns, or Auto', () => {
    for (const kind of ['switch', 'edge']) {
      const field = schemaForKind(builderSchemaV1, kind).properties.lineStyle;

      expect(field.title, kind).toBe('Line style');
      expect(field.oneOf, kind).toEqual([
        { const: 'solid', title: 'Solid' },
        { const: 'dashed', title: 'Dashed' },
        { const: 'dotted', title: 'Dotted' },
        { const: 'dash-dot', title: 'Dash-dot' },
      ]);
      expect(field[UNSET_KEYWORD], kind).toBe('Auto');
      expect(field.enum, kind).toBeUndefined();
    }

    // The patterns are those the document schema allows, and the editor's
    // own list when a served schema names none.
    expect(
      schemaForKind(
        { $defs: { network: {} } },
        'switch',
      ).properties.lineStyle.oneOf.map((choice) => choice.const),
    ).toEqual(LINE_STYLES);
    expect(
      builderSchemaV1.$defs.lineStyle.enum.filter((value) => value !== ''),
    ).toEqual(LINE_STYLES);
  });

  test("a group's border pattern is one of four named patterns", () => {
    const { borderStyle, description, iconKey } = schemaForKind(
      builderSchemaV1,
      'group',
    ).properties;

    expect(borderStyle.title).toBe('Border pattern');
    expect(borderStyle.oneOf).toEqual([
      { const: 'solid', title: 'Solid' },
      { const: 'dashed', title: 'Dashed' },
      { const: 'dotted', title: 'Dotted' },
      { const: 'double', title: 'Double' },
    ]);
    expect(borderStyle[UNSET_KEYWORD]).toBeUndefined();
    expect(
      builderSchemaV1.$defs.borderStyle.enum.filter((value) => value !== ''),
    ).toEqual(BORDER_STYLES);
    expect(description).toMatchObject({ type: 'string', title: 'Description' });
    expect(iconKey.title).toBe('Icon');
  });

  test("a group's icon offers every built-in key", () => {
    const offered = (context) =>
      schemaForKind(builderSchemaV1, 'group', context).properties.iconKey.enum;

    expect(offered()).toEqual(builderSchemaV1.$defs.iconKey.enum);
    // The same object for every icon, so it is compiled once.
    expect(
      schemaForKind(builderSchemaV1, 'group', { iconKey: 'printer' }),
    ).toBe(schemaForKind(builderSchemaV1, 'group'));
  });

  test('notes, groups, edges and the document each have a schema', () => {
    ['note', 'group', 'edge', 'document'].forEach((kind) => {
      const schema = schemaForKind(builderSchemaV1, kind);

      expect(schema.type).toBe('object');
      expect(Object.keys(schema.properties).length).toBeGreaterThan(0);
    });

    // A connection has a color and a line style of its own, drawn in place
    // of its network's.
    expect(
      Object.keys(schemaForKind(builderSchemaV1, 'edge').properties),
    ).toEqual(['label', 'color', 'lineStyle']);
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

// A node spec's form fields by data path without list indexes, including
// list items and every oneOf alternative.
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
  // Descriptions are the Inspector's tooltips. Groups have none, nor has DNS,
  // whose description is on its first alternative.
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

  // The server's schema lists a drive's fields alphabetically. The form
  // shows the image and the inject partition first.
  test("a drive's fields come in the order users look for them", () => {
    const drive = deviceSpec({}).properties.hardware.properties.drives.items;

    expect(Object.keys(drive.properties)).toEqual([
      'image',
      'inject_partition',
      'cache_mode',
      'interface',
    ]);
  });

  test('a field order names the fields that come first', () => {
    const properties = { c: 3, a: 1, d: 4, b: 2 };

    expect(Object.keys(orderedFields(properties, ['b', 'z', 'a']))).toEqual([
      'b',
      'a',
      'c',
      'd',
    ]);
    // Nothing moves: the same object.
    expect(orderedFields(properties, ['c', 'a'])).toBe(properties);
    expect(orderedFields(properties, [])).toBe(properties);
  });

  // Firewall rulesets and miniccc commands say what they are for. miniccc
  // keeps its lowercase.
  test('rulesets and commands are named for what they are', () => {
    const spec = deviceSpec({});

    expect(spec.properties.network.properties.rulesets.title).toBe(
      'Firewall rulesets',
    );
    expect(spec.properties.commands.title).toBe('miniccc commands');
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
  // yet. The diagram checks and publishing refuse it instead.
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
        withInterface(kinds[0]),
      ),
    ).toBe(false);
  });

  test("a drive's image suggests disk images, and an interface's rulesets the device's", () => {
    const suggesting = specFields(deviceSpec({}))
      .filter(({ schema }) => schema[SUGGESTIONS_KEYWORD])
      .map(({ path, schema }) => [path, schema[SUGGESTIONS_KEYWORD]]);

    expect(new Set(suggesting.map((entry) => entry.join(' ')))).toEqual(
      new Set([
        'hardware.drives.image disks',
        'network.interfaces.ruleset_in rulesets',
        'network.interfaces.ruleset_out rulesets',
      ]),
    );
  });
});
