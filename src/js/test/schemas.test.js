// The JSON schemas in the repository's schemas/ directory, which
// src/go/tools/schema-export generates for editors and SchemaStore: each is a
// valid JSON Schema 2020-12 document, and each per-kind schema describes a
// whole phenix config file, as `phenix config create` checks it, with its spec
// in the shared defs file.

import { readdirSync, readFileSync } from 'node:fs';

import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';
import YAML from 'js-yaml';
import { describe, expect, test } from 'vitest';

const repository = new URL('../../../', import.meta.url);
const schemasDir = new URL('schemas/', repository);
const defaultConfigsDir = new URL('src/go/api/config/default/', repository);
const defsFile = 'defs.schema.json';

const files = readdirSync(schemasDir).filter((name) =>
  name.endsWith('.schema.json'),
);
const schemas = Object.fromEntries(
  files.map((name) => [
    name,
    JSON.parse(readFileSync(new URL(name, schemasDir))),
  ]),
);

// Strict, as SchemaStore compiles the schemas it lists. Every file is added,
// so the per-kind schemas' references into the defs file, relative to their
// $id, resolve.
const ajv = new Ajv2020({ strict: true, allErrors: true });
addFormats(ajv);
ajv.addSchema(Object.values(schemas));

// The $id of each config kind's schema.
const kinds = Object.fromEntries(
  files
    .filter((name) => name !== defsFile)
    .map((name) => [schemas[name].properties.kind.const, schemas[name].$id]),
);

function validatorFor(kind) {
  return ajv.getSchema(kinds[kind]);
}

function errorsOf(config) {
  const validate = validatorFor(config.kind);

  return validate(config)
    ? []
    : validate.errors.map((error) => `${error.instancePath} ${error.message}`);
}

const metadata = { name: 'example' };

const topology = {
  apiVersion: 'phenix.sandia.gov/v1',
  kind: 'Topology',
  metadata,
  spec: {
    nodes: [
      {
        type: 'VirtualMachine',
        general: { hostname: 'host-a' },
        hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
        network: {
          interfaces: [
            {
              name: 'eth0',
              type: 'ethernet',
              proto: 'static',
              address: '10.0.0.1',
              mask: 24,
              vlan: 'EXP',
            },
          ],
        },
      },
    ],
  },
};

const scenario = {
  apiVersion: 'phenix.sandia.gov/v2',
  kind: 'Scenario',
  metadata: { ...metadata, annotations: { topology: 'example' } },
  spec: { apps: [{ name: 'ntp', hosts: [{ hostname: 'host-a' }] }] },
};

// A node as phenix writes it (structs.MapDefaultCase): every field is there,
// null or "" where nothing is set.
const writtenNode = {
  advanced: null,
  annotations: null,
  commands: null,
  delay: null,
  deletions: [],
  external: null,
  general: {
    description: '',
    do_not_boot: null,
    hostname: 'host-00',
    snapshot: null,
    vm_type: 'kvm',
  },
  hardware: {
    cpu: '',
    drives: [
      {
        cache_mode: '',
        image: 'bennu.qc2',
        inject_partition: null,
        interface: '',
      },
    ],
    memory: 512,
    os_type: 'linux',
    vcpus: 1,
  },
  injections: [],
  labels: null,
  network: {
    interfaces: [
      {
        address: '10.0.0.1',
        autostart: false,
        baud_rate: 0,
        bridge: '',
        device: '',
        dns: null,
        driver: '',
        gateway: '',
        mac: '',
        mask: 24,
        mtu: 0,
        name: 'eth0',
        proto: 'static',
        qinq: false,
        ruleset_in: '',
        ruleset_out: '',
        type: 'ethernet',
        udp_port: 0,
        vlan: 'EXP',
      },
    ],
    nat: [],
    ospf: null,
    routes: [],
    rulesets: [],
  },
  overrides: null,
  type: 'VirtualMachine',
};

describe('exported JSON schemas', () => {
  test('there is one per config kind, and the defs file', () => {
    expect(Object.keys(kinds).sort()).toEqual([
      'Experiment',
      'Image',
      'Role',
      'Scenario',
      'Topology',
      'User',
    ]);
    expect(files).toContain(defsFile);
  });

  test.each(files)('%s is a valid JSON Schema 2020-12 document', (name) => {
    const schema = schemas[name];

    expect(ajv.validateSchema(schema), JSON.stringify(ajv.errors)).toBe(true);
    expect(ajv.getSchema(schema.$id)).toBeTypeOf('function');
  });

  test.each(readdirSync(defaultConfigsDir))(
    'the default config %s is valid',
    (name) => {
      const config = YAML.load(
        readFileSync(new URL(name, defaultConfigsDir), 'utf8'),
      );

      expect(errorsOf(config)).toEqual([]);
    },
  );

  test('whole config files are valid, and their spec alone is not', () => {
    const experiment = {
      apiVersion: 'phenix.sandia.gov/v1',
      kind: 'Experiment',
      metadata: {
        ...metadata,
        annotations: { topology: 'example', scenario: 'example' },
      },
      spec: {
        experimentName: 'example',
        topology: topology.spec,
        scenario: scenario.spec,
        vlans: { aliases: { EXP: 101 } },
      },
    };
    const user = {
      apiVersion: 'phenix.sandia.gov/v1',
      kind: 'User',
      metadata,
      spec: { username: 'alice', first_name: 'Alice', last_name: 'Example' },
    };

    expect(errorsOf(topology)).toEqual([]);
    expect(errorsOf(scenario)).toEqual([]);
    expect(errorsOf(experiment)).toEqual([]);
    expect(errorsOf(user)).toEqual([]);

    // The spec on its own is no config file.
    expect(validatorFor('Topology')(topology.spec)).toBe(false);
    expect(errorsOf({ ...topology, metadata: {} })).toEqual([
      "/metadata must have required property 'name'",
    ]);
  });

  test('a config as phenix itself writes it is valid', () => {
    const stored = { ...topology, spec: { nodes: [writtenNode] } };

    expect(errorsOf(stored)).toEqual([]);
    expect(
      errorsOf({
        apiVersion: 'phenix.sandia.gov/v1',
        kind: 'Experiment',
        metadata,
        spec: {
          experimentName: 'example',
          topology: stored.spec,
          vlans: { aliases: { EXP: 101 } },
        },
      }),
    ).toEqual([]);
  });

  test('a config phenix cannot read at its apiVersion is refused', () => {
    // There is no Topology v2.
    expect(
      errorsOf({ ...topology, apiVersion: 'phenix.sandia.gov/v2' }),
    ).toEqual(['/apiVersion must be equal to constant']);
    // A v1 scenario, whose apps are grouped by experiment and host.
    expect(
      errorsOf({
        ...scenario,
        apiVersion: 'phenix.sandia.gov/v1',
        spec: { apps: { experiment: [{ name: 'ntp' }] } },
      }),
    ).toEqual(
      expect.arrayContaining([
        '/apiVersion must be equal to constant',
        '/spec/apps must be array,null',
      ]),
    );
  });
});
