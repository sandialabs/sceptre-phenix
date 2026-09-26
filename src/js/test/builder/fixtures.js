// Shared fixtures for the builder unit tests.
//
// Documents are built through the real model so the fixtures stay valid as the
// wire contract evolves; a fixture that drifts from model.js would hide bugs.

import {
  addNetwork,
  addNode,
  connect,
  createDocument,
} from '@/builder/model.js';

let counter = 0;

/**
 * Deterministic ids keep snapshot comparisons readable in tests: a fixed
 * version 4 UUID ending in a counter. Real ids come from crypto.randomUUID,
 * and every id must be a UUID (see validate.js).
 *
 * @returns {string}
 */
export function testId() {
  counter += 1;

  return `00000000-0000-4000-8000-${String(counter).padStart(12, '0')}`;
}

export function resetIds() {
  counter = 0;
}

/**
 * A document with one network, one switch, two devices and one connection.
 *
 * @returns {{doc: object, network: object, sw: object, alpha: object,
 *   bravo: object, edge: object}}
 */
export function sampleDocument() {
  let doc = createDocument({ id: testId(), name: 'Sample' });

  const created = addNetwork(doc, { name: 'EXP', alias: 100 });
  doc = created.doc;

  const network = created.network;

  const switchNode = addNode(doc, {
    kind: 'switch',
    networkId: network.id,
    position: { x: 200, y: 0 },
  });
  doc = switchNode.doc;

  const first = addNode(doc, {
    kind: 'device',
    hostname: 'alpha',
    position: { x: 0, y: 0 },
    interfaces: [{ name: 'eth0' }],
  });
  doc = first.doc;

  const second = addNode(doc, {
    kind: 'device',
    hostname: 'bravo',
    position: { x: 0, y: 200 },
    interfaces: [{ name: 'eth0' }],
  });
  doc = second.doc;

  const connected = connect(doc, {
    sourceNodeId: first.node.id,
    sourceHandleId: first.node.device.interfaces[0].id,
    targetNodeId: switchNode.node.id,
  });

  doc = connected.doc;

  return {
    doc,
    network,
    sw: switchNode.node,
    alpha: first.node,
    bravo: second.node,
    edge: connected.edge,
  };
}

/**
 * A node spec as phenix stores it in an experiment, and so as Generate
 * brings it into a draft: structs.MapDefaultCase keeps every zero value,
 * so unset fields are null or empty (advanced null, mac "", gateway "",
 * ruleset_in ""). phenix accepts it; the Inspector form's schema does not.
 * Dumped from a v1.Node through MapDefaultCase, as api/experiment does.
 *
 * @param {string} [hostname]
 * @returns {object}
 */
export function experimentNodeSpec(hostname = 'host-00') {
  return {
    advanced: null,
    annotations: null,
    commands: null,
    delay: null,
    deletions: [],
    external: null,
    general: {
      description: '',
      do_not_boot: null,
      hostname,
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
}
