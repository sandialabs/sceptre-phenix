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
 * Deterministic ids keep snapshot comparisons readable in tests. Real ids come
 * from crypto.randomUUID.
 *
 * @returns {string}
 */
export function testId(prefix = 'id') {
  counter += 1;

  return `${prefix}-${String(counter).padStart(4, '0')}`;
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
  let doc = createDocument({ id: testId('doc'), name: 'Sample' });

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
