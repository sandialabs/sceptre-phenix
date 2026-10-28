// Shared fixtures for the builder unit tests, built through the real model so
// they stay valid as the wire contract changes.

import { expect } from 'vitest';

import {
  addNetwork,
  addNode,
  connect,
  createDocument,
} from '@/builder/model.js';
import { paletteTemplateGroups, templateByKey } from '@/builder/templates.js';
import { validateDocument } from '@/builder/validate.js';

let counter = 0;

// The errors validateDocument reports for doc, without its warnings.
export function errorsOf(doc) {
  return validateDocument(doc).filter((issue) => issue.level === 'error');
}

// A deterministic version 4 UUID ending in a counter (validate.js wants
// UUIDs).
export function testId() {
  counter += 1;

  return `00000000-0000-4000-8000-${String(counter).padStart(12, '0')}`;
}

// Gives a store stand-in paletteTemplateGroups and templateByKey, which
// follow its `doc` and `templates`.
export function withTemplates(store) {
  Object.defineProperty(store, 'paletteTemplateGroups', {
    configurable: true,
    enumerable: true,
    get() {
      return paletteTemplateGroups(this.doc, this.templates);
    },
  });
  store.templateByKey = (key) => templateByKey(store.doc, key, store.templates);

  return store;
}

// A template library as the store keeps one it read (fetchTemplates), its
// items of the library unless they say otherwise.
export function libraryOf({ templates = [], collections = [], ...rest } = {}) {
  const listed = (item, index) => ({
    source: 'library',
    version: 1,
    etag: '"1"',
    ...item,
    id: item.id ?? `item-${index}`,
  });

  return {
    status: 'ready',
    error: '',
    loaded: true,
    items: templates.map((template, index) => ({
      description: '',
      collections: [],
      ...listed(template, index),
    })),
    collections: collections.map((collection, index) => ({
      description: '',
      templateIds: [],
      ...listed(collection, index),
    })),
    builtins: [],
    damaged: false,
    limits: {
      templates: 200,
      collections: 50,
      nameBytes: 128,
      descriptionBytes: 1024,
      deviceBytes: 16384,
    },
    ...rest,
  };
}

// A document with one network, one switch, two devices and one connection.
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

// A node spec as an experiment stores it (zero values kept, so null or empty
// fields), which phenix accepts and the Inspector's schema does not.
export function experimentNodeSpec() {
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
}

// A Storage kept in memory, as localStorage is. `entries` holds what it
// stores.
export function memoryStorage(initial = {}) {
  const entries = new Map(Object.entries(initial));

  return {
    entries,
    get length() {
      return entries.size;
    },
    key: (index) => [...entries.keys()][index] ?? null,
    getItem: (key) => (entries.has(key) ? entries.get(key) : null),
    setItem: (key, value) => {
      entries.set(key, String(value));
    },
    removeItem: (key) => {
      entries.delete(key);
    },
    clear: () => entries.clear(),
  };
}

// Each opening tag of `name` in rendered HTML. `skipDialogs` leaves out those
// inside a <dialog>.
export function tags(html, name, { skipDialogs = false } = {}) {
  const visible = skipDialogs
    ? html.replace(/<dialog[\s\S]*?<\/dialog>/g, '')
    : html;

  return visible.match(new RegExp(`<${name}\\b[^>]*>`, 'g')) || [];
}

// The text of rendered HTML: no comments or tags, entities decoded, white
// space as one space, and none before a comma, period or colon.
export function textOf(html) {
  return html
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/<[^>]*>/g, ' ')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&')
    .replace(/\s+/g, ' ')
    .replace(/ ([,.:])/g, '$1')
    .trim();
}

// The first opening tag whose attributes match pattern. The test fails
// without one.
export function tag(html, pattern) {
  const found = html.match(
    new RegExp(`<[a-z][a-z0-9]*\\b[^>]*${pattern}[^>]*>`),
  );

  expect(found, pattern).not.toBeNull();

  return found[0];
}

// The opening tag with the test id, or null without one.
export function tagOf(html, testId) {
  return (
    html.match(
      new RegExp(`<[a-z][a-z0-9]*\\b[^>]*data-testid="${testId}"[^>]*>`),
    )?.[0] ?? null
  );
}

// The value of an attribute of an opening tag, or null without it.
export function attribute(tagHtml, name) {
  return tagHtml?.match(new RegExp(`\\s${name}="([^"]*)"`))?.[1] ?? null;
}
