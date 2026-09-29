import { describe, expect, test, vi } from 'vitest';
import YAML from 'js-yaml';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

import {
  computeExportViewport,
  describeTopologyExport,
  documentBounds,
  exportCopy,
  exportFileName,
  exportImage,
  IMAGE_PADDING,
  inlineSvgPaint,
  saveText,
  saveTopologyYAML,
  toJSONString,
  toYAMLString,
} from '@/builder/exporters.js';
import { parseDocument } from '@/builder/decode.js';
import { toGEXF } from '@/builder/gexf.js';
import {
  addNetwork,
  addNode,
  connect,
  createDocument,
  groupNodes,
} from '@/builder/model.js';

import { sampleDocument } from './fixtures.js';

// Vitest runs without a DOM, so these stand in for the canvas's elements:
// enough of Element for the export helpers, matching the selectors they use
// (class lists, `[id]`, `[name="value"]` and `svg *`).
const fakeDocument = { createElement: (tag) => fakeElement(tag) };

function fakeElement(
  tag,
  classes = '',
  { id = null, attributes = {}, children = [] } = {},
) {
  const element = {
    tag,
    classes: new Set(classes.split(' ').filter(Boolean)),
    id,
    attributes: { ...attributes },
    inline: {},
    parent: null,
    children: [],
    ownerDocument: fakeDocument,
    classList: {
      remove: (...names) =>
        names.forEach((name) => element.classes.delete(name)),
    },
    style: {
      setProperty: (name, value) => {
        element.inline[name] = value;
      },
    },
    setAttribute: (name, value) => {
      element.attributes[name] = value;
    },
    removeAttribute: (name) => {
      if (name === 'id') {
        element.id = null;
      }
      delete element.attributes[name];
    },
    matches: (selector) =>
      selector.split(',').some((part) => {
        const name = part.trim();

        if (name === '[id]') {
          return element.id !== null;
        }
        const attribute = name.match(/^\[([\w-]+)="([^"]*)"\]$/);
        if (attribute) {
          return element.attributes[attribute[1]] === attribute[2];
        }
        if (name === 'svg *') {
          return ancestors(element).some((up) => up.tag === 'svg');
        }

        return element.classes.has(name.slice(1));
      }),
    querySelectorAll: (selector) =>
      descendants(element).filter((item) => item.matches(selector)),
    remove: () => {
      element.parent.children.splice(
        element.parent.children.indexOf(element),
        1,
      );
      element.parent = null;
    },
    after: (sibling) => {
      const siblings = element.parent.children;

      siblings.splice(siblings.indexOf(element) + 1, 0, sibling);
      sibling.parent = element.parent;
    },
    append: (child) => {
      element.children.push(child);
      child.parent = element;
    },
    cloneNode: () =>
      fakeElement(tag, [...element.classes].join(' '), {
        id: element.id,
        attributes: element.attributes,
        children: element.children.map((child) => child.cloneNode(true)),
      }),
  };

  for (const child of children) {
    element.children.push(child);
    child.parent = element;
  }

  return element;
}

function descendants(element) {
  return element.children.flatMap((child) => [child, ...descendants(child)]);
}

function ancestors(element) {
  return element.parent ? [element.parent, ...ancestors(element.parent)] : [];
}

function find(root, className) {
  return descendants(root).find((item) => item.classes.has(className));
}

// What builder.css gives a connection line: its network's colour, and a
// wider stroke while it is selected.
function computedStyle(element) {
  const values = element.classes.has('builder-edge--network-1')
    ? {
        stroke: 'rgb(138, 83, 0)',
        'stroke-width': element.classes.has('is-selected') ? '4px' : '2px',
        'stroke-dasharray': '8px, 4px',
      }
    : {};

  return { getPropertyValue: (name) => values[name] ?? '' };
}

// Vue Flow's transformation pane under its parent, as the canvas has it with
// a selected device and a selected connection, each a pressed toggle button:
// the device with its handles, the connection as NetworkEdge draws it (hit
// area, focus band, line) and the connection's label.
function fakeCanvas() {
  const pane = fakeElement('div', 'vue-flow__transformationpane', {
    children: [
      fakeElement('svg', 'vue-flow__edges', {
        children: [
          fakeElement('g', 'vue-flow__edge selected', {
            attributes: {
              role: 'button',
              tabIndex: '0',
              'aria-pressed': 'true',
              'aria-describedby': 'builder-edge-hint',
            },
            children: [
              fakeElement('path', 'builder-edge__hit'),
              fakeElement('path', 'builder-edge__focus'),
              fakeElement(
                'path',
                'builder-edge builder-edge--network-1 is-selected',
                { id: 'edge-1' },
              ),
            ],
          }),
        ],
      }),
      fakeElement('div', 'builder-edge__label is-selected'),
      fakeElement('div', 'vue-flow__node selected', {
        attributes: {
          role: 'button',
          tabindex: '0',
          'aria-pressed': 'true',
          'aria-describedby': 'builder-node-hint',
        },
        children: [
          fakeElement('div', 'builder-node builder-node--device is-selected', {
            children: [
              fakeElement('div', 'vue-flow__handle builder-handle'),
              fakeElement(
                'div',
                'vue-flow__handle builder-handle builder-handle--new',
              ),
              fakeElement('span', 'builder-node__issue'),
              fakeElement('span', 'builder-node__issue-text'),
            ],
          }),
        ],
      }),
    ],
  });
  const parent = fakeElement('div', 'vue-flow__pane', { children: [pane] });

  return { pane, parent };
}

describe('document export', () => {
  test('JSON exports re-import unchanged', () => {
    const { doc } = sampleDocument();
    const text = toJSONString(doc);

    expect(parseDocument(JSON.parse(text))).toEqual(doc);
  });

  test('YAML exports re-import unchanged', () => {
    const { doc } = sampleDocument();

    expect(parseDocument(YAML.load(toYAMLString(doc)))).toEqual(doc);
  });

  test('file names are filesystem safe', () => {
    expect(exportFileName({ name: 'My Topology!' }, 'json')).toBe(
      'my-topology.json',
    );
    expect(exportFileName({}, 'png')).toBe('topology.png');
  });
});

describe('image export geometry', () => {
  test('bounds cover every node, not the visible viewport', () => {
    const { doc } = sampleDocument();
    const bounds = documentBounds(doc);
    const xs = doc.nodes.map((node) => node.position.x);

    expect(bounds.x).toBeLessThanOrEqual(Math.min(...xs) - IMAGE_PADDING + 1);
    expect(bounds.width).toBeGreaterThan(0);
    expect(bounds.height).toBeGreaterThan(0);
  });

  test('an empty document still exports a usable canvas', () => {
    expect(documentBounds({ nodes: [] })).toEqual({
      x: 0,
      y: 0,
      width: 320,
      height: 240,
    });
  });

  test('the viewport transform fits the bounds', () => {
    const viewport = computeExportViewport({
      x: -100,
      y: -50,
      width: 800,
      height: 400,
    });

    expect(viewport.zoom).toBeLessThanOrEqual(2);
    expect(viewport.transform).toContain('scale(');
    expect(viewport.x).toBe(100 * viewport.zoom);
  });

  test('large diagrams are scaled down rather than clipped', () => {
    const viewport = computeExportViewport(
      { x: 0, y: 0, width: 20000, height: 400 },
      { maxWidth: 4096 },
    );

    expect(viewport.width).toBeLessThanOrEqual(4096);
    expect(viewport.zoom).toBeLessThan(1);
  });
});

describe('image export content', () => {
  test('the export copy has no editing affordances, selection or IDs, and the canvas keeps them', () => {
    const { pane, parent } = fakeCanvas();
    const { copy, holder } = exportCopy(pane);

    // The copy's holder, beside the pane, keeps it out of sight, the
    // accessibility tree and the keyboard order. The copy itself is not
    // marked: an image keeps the attributes of the element it is rendered
    // from.
    expect(parent.children).toEqual([pane, holder]);
    expect(holder.children).toEqual([copy]);
    expect(holder.attributes).toEqual({ 'aria-hidden': 'true', inert: '' });
    expect(holder.inline).toEqual({
      position: 'absolute',
      inset: '0',
      opacity: '0',
      'pointer-events': 'none',
    });
    expect(copy.attributes).toEqual({});
    expect(copy.inline).toEqual({});

    const classes = descendants(copy).flatMap((item) => [...item.classes]);

    expect(classes).toContain('builder-edge');
    expect(classes).toContain('builder-edge__label');
    expect(classes).toContain('builder-node--device');
    for (const gone of [
      'vue-flow__handle',
      'builder-edge__hit',
      'builder-edge__focus',
      'builder-node__issue',
      'builder-node__issue-text',
      'is-selected',
      'selected',
    ]) {
      expect(classes).not.toContain(gone);
    }
    expect(descendants(copy).filter((item) => item.id)).toEqual([]);
    // Nodes and connections are labelled pictures, not toggle buttons.
    expect(find(copy, 'vue-flow__node').attributes).toEqual({ role: 'img' });
    expect(find(copy, 'vue-flow__edge').attributes).toEqual({ role: 'img' });

    // The live canvas is left as it is.
    expect(pane.inline).toEqual({});
    expect(find(pane, 'builder-edge').classes.has('is-selected')).toBe(true);
    expect(find(pane, 'vue-flow__node').attributes['aria-pressed']).toBe(
      'true',
    );
    expect(find(pane, 'builder-edge').id).toBe('edge-1');
    expect(find(pane, 'vue-flow__handle')).toBeTruthy();
  });

  // html-to-image copies an <svg> without its class styles, so a connection
  // line exported with no stroke at all until its paint was inlined.
  test('a connection line carries its computed paint inline', () => {
    const { pane } = fakeCanvas();
    const { copy } = exportCopy(pane);

    inlineSvgPaint(copy, computedStyle);

    expect(find(copy, 'builder-edge').inline).toMatchObject({
      stroke: 'rgb(138, 83, 0)',
      'stroke-width': '2px',
      'stroke-dasharray': '8px, 4px',
    });
    expect(find(copy, 'builder-node').inline).toEqual({});
  });

  test('image export renders the copy with its own transform in place of the pan and zoom, then removes it', async () => {
    const { doc } = sampleDocument();
    const { pane, parent } = fakeCanvas();
    let rendered;
    let holder;
    let paint;
    const toSvg = vi.fn(async (element) => {
      rendered = element;
      holder = element.parent;
      paint = { ...find(element, 'builder-edge').inline };
      return 'data:image/svg+xml,x';
    });

    await exportImage({
      element: pane,
      doc,
      format: 'svg',
      toSvg,
      computedStyle,
    });

    const [, options] = toSvg.mock.calls[0];
    const viewport = computeExportViewport(documentBounds(doc));

    expect(rendered).not.toBe(pane);
    expect(rendered.classes.has('vue-flow__transformationpane')).toBe(true);
    // The image's root is the copy, which is visible and not marked hidden
    // or inert; its holder, left out of the image, is.
    expect(rendered.attributes).toEqual({});
    expect(rendered.inline).toEqual({});
    expect(holder).not.toBe(parent);
    expect(holder.attributes).toEqual({ 'aria-hidden': 'true', inert: '' });
    expect(options.style).toEqual({
      width: `${viewport.width}px`,
      height: `${viewport.height}px`,
      transform: viewport.transform,
      transformOrigin: '0 0',
      overflow: 'visible',
    });
    // Unselected paint, though the line on the canvas is selected.
    expect(paint).toMatchObject({
      stroke: 'rgb(138, 83, 0)',
      'stroke-width': '2px',
    });
    expect(parent.children).toEqual([pane]);
    expect(descendants(pane).filter((item) => item.inline.stroke)).toEqual([]);
  });

  test('a failed render still removes the copy and its holder', async () => {
    const { pane, parent } = fakeCanvas();
    let holder;

    await expect(
      exportImage({
        element: pane,
        doc: sampleDocument().doc,
        format: 'png',
        toPng: vi.fn(async (element) => {
          holder = element.parent;
          throw new Error('canvas too large');
        }),
        computedStyle,
      }),
    ).rejects.toThrow(/canvas too large/);
    expect(parent.children).toEqual([pane]);
    expect(holder.parent).toBeNull();
  });
});

describe('savers', () => {
  test('image export renders the whole diagram through the injected renderer', async () => {
    const { doc } = sampleDocument();
    const toPng = vi.fn(async () => 'data:image/png;base64,x');
    const saveAs = vi.fn();

    const url = await exportImage({
      element: fakeCanvas().pane,
      doc,
      format: 'png',
      toPng,
      saveAs,
      backgroundColor: '#fff',
      computedStyle,
    });

    expect(url).toContain('data:image/png');
    expect(toPng).toHaveBeenCalledTimes(1);

    const [, options] = toPng.mock.calls[0];

    expect(options.width).toBe(
      computeExportViewport(documentBounds(doc)).width,
    );
    expect(saveAs).toHaveBeenCalledWith(url, 'sample.png');
  });

  test('image export refuses without a canvas element', async () => {
    await expect(exportImage({ doc: {}, toPng: vi.fn() })).rejects.toThrow(
      /No canvas element/,
    );
  });

  test('text export builds a blob with a charset', () => {
    const saveAs = vi.fn();

    class FakeBlob {
      constructor(parts, options) {
        this.parts = parts;
        this.options = options;
      }
    }

    const blob = saveText({
      text: '{}',
      mime: 'application/json',
      fileName: 'a.json',
      saveAs,
      BlobCtor: FakeBlob,
    });

    expect(blob.options.type).toBe('application/json;charset=utf-8');
    expect(saveAs).toHaveBeenCalledWith(blob, 'a.json');
  });

  test('Topology YAML is the text the server sends, named as Publish names it, in a file apart from Builder YAML', async () => {
    const { doc } = sampleDocument();
    const named = { ...doc, name: 'Lab #2 (copy)' };
    const saveAs = vi.fn();
    const yaml = 'apiVersion: phenix.sandia.gov/v1\nkind: Topology\n';
    const exportTopology = vi.fn(async (_, name) => ({
      name,
      yaml,
      warnings: ['device "a" carries a node spec with hostname "b"'],
      publishBlockers: [],
    }));

    class FakeBlob {
      constructor(parts, options) {
        this.parts = parts;
        this.options = options;
      }
    }

    const saved = await saveTopologyYAML({
      doc: named,
      exportTopology,
      saveAs,
      BlobCtor: FakeBlob,
    });

    expect(exportTopology).toHaveBeenCalledWith(named, 'Lab-2-copy');
    expect(saved).toEqual({
      name: 'Lab-2-copy',
      fileName: 'lab-2-copy.topology.yaml',
      warnings: ['device "a" carries a node spec with hostname "b"'],
      publishBlockers: [],
    });
    expect(exportFileName(named, 'yaml')).not.toBe(saved.fileName);

    const [blob, fileName] = saveAs.mock.calls[0];

    expect(fileName).toBe(saved.fileName);
    expect(blob.parts).toEqual([yaml]);
    expect(blob.options.type).toBe('text/yaml;charset=utf-8');

    // A failed request saves nothing.
    const failure = new Error('status 422');

    await expect(
      saveTopologyYAML({
        doc,
        exportTopology: vi.fn(async () => Promise.reject(failure)),
        saveAs,
      }),
    ).rejects.toBe(failure);
    expect(saveAs).toHaveBeenCalledTimes(1);
  });

  test('a saved topology says what keeps it from being published yet', () => {
    expect(describeTopologyExport({ fileName: 'lab.topology.yaml' })).toBe(
      'Saved lab.topology.yaml.',
    );
    expect(
      describeTopologyExport({
        fileName: 'lab.topology.yaml',
        warnings: ['device "a" carries a node spec with hostname "b"'],
        publishBlockers: [
          'interface "eth1" of device "a" has no VLAN: connect it to a network, or type a VLAN for it',
        ],
      }),
    ).toBe(
      'Saved lab.topology.yaml. Device "a" carries a node spec with hostname "b". ' +
        'This topology cannot be published yet: interface "eth1" of device "a" has no VLAN: ' +
        'connect it to a network, or type a VLAN for it.',
    );
  });
});

// --- GEXF --------------------------------------------------------------------

// A strict reader for what toGEXF writes (Vitest runs without a DOM): the
// declaration, elements with quoted attributes, and text. Anything else, a
// tag closed out of order, or an & that starts no reference, throws.
const XML_TOKEN =
  /<\?xml[^?]*\?>|<(\/?)([\w:.-]+)((?:\s+[\w:.-]+="[^"<]*")*)\s*(\/?)>|([^<]+)/y;
const XML_REFERENCES = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'" };
// Characters XML 1.0 does not allow.
const NOT_XML = /[^\t\n\r -퟿-�\u{10000}-\u{10FFFF}]/u;

function unescapeXML(text) {
  return text.replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);|&/gi, (whole, name) => {
    if (!name || (name[0] !== '#' && !(name in XML_REFERENCES))) {
      throw new Error(`not a reference: ${whole} in ${text.slice(0, 40)}`);
    }

    if (name[0] !== '#') {
      return XML_REFERENCES[name];
    }

    return String.fromCodePoint(
      name[1] === 'x' ? parseInt(name.slice(2), 16) : Number(name.slice(1)),
    );
  });
}

function parseXML(text) {
  const root = { name: '', attributes: {}, children: [], text: '' };
  const open = [root];

  XML_TOKEN.lastIndex = 0;

  while (XML_TOKEN.lastIndex < text.length) {
    const at = XML_TOKEN.lastIndex;
    const match = XML_TOKEN.exec(text);

    if (!match) {
      throw new Error(`not well formed at ${at}: ${text.slice(at, at + 40)}`);
    }

    const [, closing, name, attributes, empty, content] = match;
    const parent = open[open.length - 1];

    if (content !== undefined) {
      parent.text += unescapeXML(content);
    } else if (closing) {
      if (open.pop().name !== name) {
        throw new Error(`</${name}> closes another element`);
      }
    } else if (name) {
      const element = { name, attributes: {}, children: [], text: '' };

      for (const [, key, value] of attributes.matchAll(
        /([\w:.-]+)="([^"]*)"/g,
      )) {
        element.attributes[key] = unescapeXML(value);
      }

      parent.children.push(element);

      if (!empty) {
        open.push(element);
      }
    }
  }

  if (open.length !== 1 || root.children.length !== 1) {
    throw new Error('not one root element, or one left open');
  }

  return root.children[0];
}

function childOf(element, name) {
  return element.children.find((child) => child.name === name);
}

// id -> {title, type, default} of one class of columns.
function columnsOf(graph, cls) {
  const attributes = graph.children.find(
    (child) => child.name === 'attributes' && child.attributes.class === cls,
  );

  return Object.fromEntries(
    attributes.children.map(({ attributes: column, children }) => [
      column.id,
      {
        title: column.title,
        type: column.type,
        ...(children.length ? { default: children[0].text } : {}),
      },
    ]),
  );
}

// for -> value of a node's or edge's attvalues.
function valuesOf(element) {
  return Object.fromEntries(
    (childOf(element, 'attvalues')?.children || []).map(({ attributes }) => [
      attributes.for,
      attributes.value,
    ]),
  );
}

// The viz: elements of a node or edge, by name.
function vizOf(element) {
  return Object.fromEntries(
    element.children
      .filter((child) => child.name.startsWith('viz:'))
      .map((child) => [child.name, child.attributes]),
  );
}

function graphOf(text) {
  const root = parseXML(text);
  const graph = childOf(root, 'graph');

  return {
    root,
    graph,
    nodes: childOf(graph, 'nodes'),
    edges: childOf(graph, 'edges'),
  };
}

// A router, and a device on EXP twice (through its two switches) and on
// MGMT, grouped, with awkward labels and annotations; and a note.
function gexfDocument() {
  let doc = createDocument({
    name: 'Plant & <lab>',
    description: 'Line one\n"two" & <three> 🚀',
  });
  const exp = addNetwork(doc, { name: 'EXP', alias: 100 });

  doc = exp.doc;

  const mgmt = addNetwork(doc, { name: 'MGMT', color: '#12345680' });

  doc = mgmt.doc;

  const switches = [exp, exp, mgmt].map(({ network }, index) => {
    const added = addNode(doc, {
      kind: 'switch',
      networkId: network.id,
      position: { x: 400, y: index * 200 },
    });

    doc = added.doc;

    return added.node;
  });
  const iface = (name, extra = {}) => ({
    name,
    type: 'ethernet',
    proto: 'static',
    vlan: '',
    ...extra,
  });
  const web = addNode(doc, {
    kind: 'device',
    hostname: 'web',
    position: { x: 10, y: 20 },
    interfaces: [{ name: 'eth0' }, { name: 'eth1' }, { name: 'eth2' }],
    spec: {
      type: 'VirtualMachine',
      general: { hostname: 'web' },
      hardware: {
        os_type: 'linux',
        vcpus: 2,
        memory: 4096,
        drives: [{ image: 'ubuntu.qc2' }],
      },
      labels: {
        role: 'web',
        Role: 'upper',
        'site name': 'R&D <lab> "a" \'b\'',
      },
      annotations: {
        criticality: 1,
        big: 3000000000,
        ratio: 0.5,
        monitored: true,
        tags: ['a,b', "it's", 'back\\slash', '🚀'],
        nested: { k: [1] },
        control: 'bell\u0007 lone\uD800 end￾',
      },
      network: {
        interfaces: [
          iface('eth0', {
            address: '10.0.0.10',
            mask: 24,
            mac: '52:54:00:00:00:10',
          }),
          iface('eth1', { address: '10.0.0.11', mask: 24 }),
          iface('eth2', { proto: 'dhcp' }),
        ],
      },
    },
  });

  doc = web.doc;

  const router = addNode(doc, {
    kind: 'device',
    hostname: 'rtr',
    position: { x: 800, y: 0 },
    interfaces: [{ name: 'eth0' }],
    spec: {
      type: 'Router',
      general: { hostname: 'rtr' },
      hardware: { os_type: 'minirouter' },
      network: {
        interfaces: [iface('eth0', { address: '10.0.0.1', mask: 24 })],
      },
    },
  });

  doc = router.doc;

  for (const [device, handle, sw] of [
    [web.node, 0, switches[0]],
    [web.node, 1, switches[1]],
    [web.node, 2, switches[2]],
    [router.node, 0, switches[0]],
  ]) {
    doc = connect(doc, {
      sourceNodeId: device.id,
      sourceHandleId: device.device.interfaces[handle].id,
      targetNodeId: sw.id,
    }).doc;
  }

  doc = groupNodes(doc, [web.node.id], { title: 'Site & <A>' }).doc;
  doc = addNode(doc, { kind: 'note', text: 'Not part of the network' }).doc;

  // A VLAN the spec kept from before its connection: publishing writes the
  // network's name instead.
  doc.nodes.find(
    (node) => node.id === web.node.id,
  ).device.spec.network.interfaces[2].vlan = 'STALE';

  return { doc, web: web.node };
}

describe('GEXF export', () => {
  const modified = '2026-03-04T12:00:00Z';

  test('writes a GEXF 1.3 graph of the devices and networks', () => {
    const { doc } = gexfDocument();
    const gexf = toGEXF(doc, { modified });
    const lines = gexf.text.split('\n');
    const { root, graph, nodes, edges } = graphOf(gexf.text);

    expect(lines[0]).toBe('<?xml version="1.0" encoding="UTF-8"?>');
    // Gephi Lite reads a file as GEXF when <gexf is on its first two lines.
    expect(lines[1]).toMatch(/^<gexf /);
    expect(root.name).toBe('gexf');
    expect(root.attributes).toEqual({
      xmlns: 'http://gexf.net/1.3',
      'xmlns:viz': 'http://gexf.net/1.3/viz',
      'xmlns:xsi': 'http://www.w3.org/2001/XMLSchema-instance',
      'xsi:schemaLocation': 'http://gexf.net/1.3 http://gexf.net/1.3/gexf.xsd',
      version: '1.3',
    });

    const meta = childOf(root, 'meta');

    expect(meta.attributes.lastmodifieddate).toBe('2026-03-04');
    expect(childOf(meta, 'creator').text).toBe('phēnix Builder Flow');
    expect(childOf(meta, 'description').text).toBe(doc.description);
    expect(graph.attributes).toEqual({
      mode: 'static',
      defaultedgetype: 'undirected',
      idtype: 'string',
    });

    // EXP's second switch, the group and the note are no nodes.
    const labels = new Map(
      nodes.children.map(({ attributes }) => [attributes.id, attributes.label]),
    );

    expect([...labels.values()]).toEqual(['EXP', 'MGMT', 'web', 'rtr']);
    expect(nodes.attributes.count).toBe('4');
    expect(
      edges.children.map(({ attributes }) => [
        labels.get(attributes.source),
        labels.get(attributes.target),
      ]),
    ).toEqual([
      ['web', 'EXP'],
      ['web', 'EXP'],
      ['web', 'MGMT'],
      ['rtr', 'EXP'],
    ]);
    expect(edges.attributes.count).toBe('4');
    expect(
      new Set(edges.children.map(({ attributes }) => attributes.id)).size,
    ).toBe(4);
    expect(gexf).toMatchObject({ devices: 2, networks: 2, connections: 4 });
  });

  test('types its columns, with one for each label and annotation key', () => {
    const { doc } = gexfDocument();
    const { graph, nodes, edges } = graphOf(toGEXF(doc, { modified }).text);
    const nodeColumns = columnsOf(graph, 'node');

    expect(nodeColumns).toMatchObject({
      kind: { title: 'Node kind', type: 'string' },
      memory_mb: { title: 'Memory (MB)', type: 'integer' },
      ip_addresses: { title: 'IP addresses', type: 'liststring' },
      vlans_text: { title: 'VLANs (text)', type: 'string' },
      external: { title: 'External', type: 'boolean', default: 'false' },
      'label.role': { title: 'Label: role', type: 'string' },
      'label.Role': { title: 'Label: Role', type: 'string' },
      'label.site_name': { title: 'Label: site name', type: 'string' },
      'annotation.criticality': { type: 'integer' },
      'annotation.big': { type: 'long' },
      'annotation.ratio': { type: 'double' },
      'annotation.monitored': { type: 'boolean' },
      'annotation.tags': { type: 'liststring' },
      'annotation.nested': { type: 'string' },
    });
    // Only types every reader takes, and ids of letters, digits, _ . and -.
    for (const [id, { type }] of Object.entries(nodeColumns)) {
      expect(['string', 'integer', 'long', 'double', 'boolean']).toContain(
        type.replace(/^liststring$/, 'string'),
      );
      expect(id).toMatch(/^[\p{L}\p{N}_.-]+$/u);
    }
    expect(columnsOf(graph, 'edge')).toMatchObject({
      network: { type: 'string' },
      mask: { type: 'integer' },
      qinq: { type: 'boolean', default: 'false' },
    });

    const web = nodes.children.find(
      ({ attributes }) => attributes.label === 'web',
    );

    expect(valuesOf(web)).toMatchObject({
      kind: 'device',
      memory_mb: '4096',
      interface_count: '3',
      ip_addresses: '[10.0.0.10, 10.0.0.11]',
      // The connected interface's network, not the VLAN its spec kept.
      vlans: '[EXP, MGMT]',
      vlans_text: 'EXP|MGMT',
      group: 'Site & <A>',
      'label.site_name': 'R&D <lab> "a" \'b\'',
      'annotation.big': '3000000000',
      'annotation.tags': `['a,b', "it's", 'back\\\\slash', 🚀]`,
      'annotation.nested': '{"k":[1]}',
      // What XML 1.0 does not allow is left out.
      'annotation.control': 'bell lone end',
    });
    expect(valuesOf(edges.children[0])).toMatchObject({
      device: 'web',
      network: 'EXP',
      interface: 'eth0',
      ip_address: '10.0.0.10',
      mask: '24',
      mac: '52:54:00:00:00:10',
    });
  });

  test('gives keys cut inside a character their own columns', () => {
    const { doc, web } = gexfDocument();
    const stem = 'k'.repeat(47);
    // Each key's 48th character is outside the BMP, two UTF-16 code units.
    const keys = [stem, `${stem}\u{20000}`, `${stem}\u{21000}`];
    const spec = doc.nodes.find((node) => node.id === web.id).device.spec;

    spec.labels = Object.fromEntries(keys.map((key, i) => [key, `v${i}`]));
    spec.annotations = Object.fromEntries(keys.map((key, i) => [key, i]));

    const { text } = toGEXF(doc, { modified });
    const { graph, nodes } = graphOf(text);
    const columns = columnsOf(graph, 'node');

    expect(text).not.toMatch(NOT_XML);

    for (const prefix of ['label', 'annotation']) {
      const ids = Object.keys(columns).filter((id) =>
        id.startsWith(`${prefix}.`),
      );

      expect(ids.sort()).toEqual(keys.map((key) => `${prefix}.${key}`).sort());
    }

    const values = valuesOf(
      nodes.children.find(({ attributes }) => attributes.label === 'web'),
    );

    keys.forEach((key, i) => {
      expect(values[`label.${key}`]).toBe(`v${i}`);
      expect(values[`annotation.${key}`]).toBe(String(i));
    });
  });

  test('lists the scenario apps each device runs, when the scenario is known', () => {
    const { doc } = gexfDocument();
    const content = {
      apps: [
        { name: 'scada', hosts: [{ hostname: 'web' }, { hostname: 'rtr' }] },
        {
          name: 'protonuke',
          hosts: [{ hostname: 'web', metadata: { args: '-serve' } }],
        },
        { name: 'wireshark', disabled: true, hosts: [{ hostname: 'web' }] },
        { name: 'ntp' },
        { name: 'elsewhere', hosts: [{ hostname: 'not-in-diagram' }] },
      ],
    };
    const devices = (text) => {
      const { graph, nodes } = graphOf(text);
      const byLabel = Object.fromEntries(
        nodes.children.map((node) => [node.attributes.label, valuesOf(node)]),
      );

      return { columns: columnsOf(graph, 'node'), byLabel };
    };
    const appValues = (values) =>
      Object.fromEntries(
        Object.entries(values).filter(([id]) => /app/.test(id)),
      );

    // An uploaded scenario carries its content.
    doc.scenario = {
      kind: 'uploaded',
      name: 'scn.yaml',
      apiVersion: 'phenix.sandia.gov/v2',
      content,
    };

    const uploaded = devices(toGEXF(doc, { modified }).text);

    expect(uploaded.columns).toMatchObject({
      apps: { title: 'Scenario apps', type: 'liststring' },
      apps_text: { title: 'Scenario apps (text)', type: 'string' },
      app_count: { title: 'Scenario app count', type: 'integer' },
      disabled_apps: { title: 'Disabled scenario apps', type: 'liststring' },
    });
    expect(appValues(uploaded.byLabel.web)).toEqual({
      apps: '[scada, protonuke]',
      apps_text: 'scada|protonuke',
      app_count: '2',
      disabled_apps: '[wireshark]',
    });
    expect(appValues(uploaded.byLabel.rtr)).toEqual({
      apps: '[scada]',
      apps_text: 'scada',
      app_count: '1',
    });
    // Networks run no apps.
    expect(appValues(uploaded.byLabel.EXP)).toEqual({});

    // A stored scenario's content is read for the export: without it, the
    // file says nothing of apps rather than that there are none.
    doc.scenario = {
      kind: 'stored',
      name: 'scn',
      apiVersion: 'phenix.sandia.gov/v2',
      digest: `sha256:${'0'.repeat(64)}`,
    };

    const unread = devices(toGEXF(doc, { modified }).text);

    expect(Object.keys(unread.columns).filter((id) => /app/.test(id))).toEqual(
      [],
    );
    expect(toGEXF(doc, { modified, scenario: content }).text).toBe(
      toGEXF({ ...doc, scenario: { ...doc.scenario, content } }, { modified })
        .text,
    );
    expect(
      appValues(
        devices(toGEXF(doc, { modified, scenario: content }).text).byLabel.web,
      ),
    ).toMatchObject({ apps: '[scada, protonuke]', app_count: '2' });

    // A scenario of no apps: every device runs none.
    const none = devices(toGEXF(doc, { modified, scenario: {} }).text);

    expect(appValues(none.byLabel.web)).toEqual({ app_count: '0' });
    expect(appValues(none.byLabel.rtr)).toEqual({ app_count: '0' });
  });

  test('escapes what it writes, and writes nothing XML 1.0 does not allow', () => {
    const { text } = toGEXF(gexfDocument().doc, { modified });

    expect(text).not.toMatch(NOT_XML);
    expect(text).toContain('<description>Line one\n"two" &amp; &lt;three&gt;');
    expect(text).toContain('title="Label: site name"');
    // A line break in an attribute is a reference: a parser reads a space.
    expect(text).not.toMatch(/="[^"]*\n/);
  });

  test('puts each node at its center, y up, in r g b a colors', () => {
    const { nodes, edges } = graphOf(
      toGEXF(gexfDocument().doc, { modified }).text,
    );
    const node = (label) =>
      vizOf(
        nodes.children.find(({ attributes }) => attributes.label === label),
      );

    // A 160 × 96 device placed by its top-left corner at (10, 20).
    expect(node('web')).toEqual({
      'viz:color': { r: '107', g: '124', b: '147', a: '1' },
      'viz:position': { x: '90', y: '-68', z: '0' },
      'viz:size': { value: '10' },
      'viz:shape': { value: 'disc' },
    });
    expect(node('rtr')['viz:shape']).toEqual({ value: 'diamond' });
    expect(node('EXP')['viz:color']).toEqual({
      r: '47',
      g: '111',
      b: '191',
      a: '1',
    });
    expect(node('MGMT')['viz:color']).toEqual({
      r: '18',
      g: '52',
      b: '86',
      a: '0.502',
    });
    // One size for every node: Gephi scales sizes and positions together.
    expect(
      new Set(nodes.children.map((item) => vizOf(item)['viz:size'].value)),
    ).toEqual(new Set(['10']));
    // An edge has its network's color, and its dash pattern as a shape.
    expect(vizOf(edges.children[2])).toEqual({
      'viz:color': { r: '18', g: '52', b: '86', a: '0.502' },
      'viz:shape': { value: 'dashed' },
    });
  });

  test('tells apart the edges of a device with two interfaces on one network', () => {
    const { doc, web } = gexfDocument();
    const kinds = (text) =>
      graphOf(text).edges.children.map(({ attributes }) => attributes.kind);

    expect(kinds(toGEXF(doc, { modified }).text)).toEqual([
      'eth0',
      'eth1',
      undefined,
      undefined,
    ]);

    // Two interfaces of one name: the second is told by its place.
    const renamed = structuredClone(doc);

    renamed.nodes.find((node) => node.id === web.id).device.interfaces[1].name =
      'eth0';
    expect(kinds(toGEXF(renamed, { modified }).text).slice(0, 2)).toEqual([
      'eth0',
      '#2',
    ]);
  });

  test('writes the same file for the same document', () => {
    const { doc } = gexfDocument();
    const first = toGEXF(doc, { modified, now: () => new Date(2020, 0, 1) });

    expect(toGEXF(structuredClone(doc), { modified }).text).toBe(first.text);
    // Without its last change, the file is dated today.
    expect(toGEXF(doc, { now: () => new Date(2026, 0, 2) }).text).toContain(
      'lastmodifieddate="2026-01-02"',
    );
  });

  test('exports a 500-device diagram quickly', () => {
    let doc = createDocument({ name: 'Large' });
    let sw = null;

    for (let index = 0; index < 500; index += 1) {
      if (index % 50 === 0) {
        const created = addNetwork(doc, { name: `NET-${index / 50 + 1}` });
        const added = addNode(created.doc, {
          kind: 'switch',
          networkId: created.network.id,
        });

        doc = added.doc;
        sw = added.node;
      }

      const device = addNode(doc, {
        kind: 'device',
        hostname: `host-${index + 1}`,
        position: { x: index * 10, y: index * 10 },
        interfaces: [{ name: 'eth0' }],
      });

      doc = connect(device.doc, {
        sourceNodeId: device.node.id,
        sourceHandleId: device.node.device.interfaces[0].id,
        targetNodeId: sw.id,
      }).doc;
    }

    const started = performance.now();
    const { text } = toGEXF(doc, { modified });
    const elapsed = performance.now() - started;
    const { nodes, edges } = graphOf(text);

    expect(nodes.children).toHaveLength(510);
    expect(edges.children).toHaveLength(500);
    expect(elapsed).toBeLessThan(1000);
  });
});
