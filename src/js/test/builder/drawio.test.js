// @vitest-environment happy-dom
// The draw.io download (drawio.js), read back with the browser's DOMParser:
// its page, its cells, their containers, styles, labels and data.

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import process from 'node:process';

import { describe, expect, test } from 'vitest';

import { DRAWIO_MIME, LIGHT_COLORS, toDrawio } from '@/builder/drawio.js';
import { createDocument } from '@/builder/model.js';

import { ICON_DATA } from './png.js';

// A file by its path from src/js, where Vitest runs. Under happy-dom,
// import.meta.url is not a file URL.
const fromRoot = (path) => resolve(process.cwd(), path);

const example = () =>
  JSON.parse(
    readFileSync(
      fromRoot(
        '../go/types/builder/testdata/examples/riverside-water.builder.json',
      ),
      'utf8',
    ),
  );

function parse(text) {
  const xml = new DOMParser().parseFromString(text, 'application/xml');

  expect(xml.getElementsByTagName('parsererror')).toHaveLength(0);

  return xml;
}

// The cells of a file by id: an mxCell, or the mxCell inside an <object>
// that carries the id, the label and the data.
function cellsOf(xml) {
  const cells = new Map();

  for (const cell of xml.getElementsByTagName('mxCell')) {
    const holder =
      cell.parentElement.tagName === 'object' ? cell.parentElement : null;
    const id = (holder || cell).getAttribute('id');

    cells.set(id, { cell, holder, id });
  }

  return cells;
}

// A style as key to value. A key alone has the value true.
function styleOf(cell) {
  return Object.fromEntries(
    (cell.getAttribute('style') || '')
      .split(';')
      .filter(Boolean)
      .map((pair) => {
        const at = pair.indexOf('=');

        return at < 0 ? [pair, true] : [pair.slice(0, at), pair.slice(at + 1)];
      }),
  );
}

function geometryOf(cell) {
  const geometry = [...cell.children].find(
    (child) => child.tagName === 'mxGeometry',
  );

  return Object.fromEntries(
    ['x', 'y', 'width', 'height'].map((name) => [
      name,
      Number(geometry.getAttribute(name)),
    ]),
  );
}

function pointsOf(cell) {
  const geometry = [...cell.children].find(
    (child) => child.tagName === 'mxGeometry',
  );
  const points = (as) =>
    [...geometry.getElementsByTagName('mxPoint')]
      .filter((point) => point.getAttribute('as') === as)
      .map((point) => ({
        x: Number(point.getAttribute('x')),
        y: Number(point.getAttribute('y')),
      }));
  const bends = [...geometry.getElementsByTagName('Array')].flatMap((array) =>
    [...array.getElementsByTagName('mxPoint')].map((point) => ({
      x: Number(point.getAttribute('x')),
      y: Number(point.getAttribute('y')),
    })),
  );

  return {
    source: points('sourcePoint')[0],
    target: points('targetPoint')[0],
    bends,
  };
}

// The text of an HTML label, as draw.io shows it.
function labelText(value) {
  const holder = document.createElement('div');

  holder.innerHTML = value.replace(/<br>/g, '\n');

  return holder.textContent;
}

// The SVG of a built-in icon's image in a style.
function svgOf(image) {
  const [, data] = image.match(/^data:image\/svg\+xml,(.*)$/);

  return new TextDecoder().decode(
    Uint8Array.from(atob(data), (char) => char.charCodeAt(0)),
  );
}

// A document of these nodes, edges and networks.
function diagram({ nodes = [], edges = [], networks = [], ...rest } = {}) {
  return {
    ...createDocument({ id: 'doc-1', name: 'Lab' }),
    nodes,
    edges,
    networks,
    ...rest,
  };
}

const device = (id, position, extra = {}) => ({
  id,
  kind: 'device',
  label: id,
  position,
  device: {
    hostname: id,
    spec: { hostname: id, type: 'VirtualMachine' },
    interfaces: [],
    ...extra,
  },
});

describe('the draw.io file', () => {
  test('is one uncompressed page of the diagram, named after it, with no page bounds', () => {
    const xml = parse(toDrawio(example()));
    const file = xml.documentElement;
    const [diagram] = file.getElementsByTagName('diagram');
    const [model] = file.getElementsByTagName('mxGraphModel');
    const [root] = file.getElementsByTagName('root');

    expect(DRAWIO_MIME).toBe('application/vnd.jgraph.mxfile');
    expect(file.tagName).toBe('mxfile');
    expect(file.getAttribute('host')).toBe('phenix');
    expect(file.getAttribute('type')).toBe('device');
    expect(diagram.getAttribute('name')).toBe('Riverside Water');
    expect(diagram.getAttribute('id')).toBe(example().metadata.id);
    expect(model.getAttribute('page')).toBe('0');
    expect(model.getAttribute('grid')).toBe('1');
    expect(model.getAttribute('gridSize')).toBe('16');
    expect(model.getAttribute('background')).toBe('#ffffff');
    // The page's root and its layer come first.
    expect(
      [...root.children]
        .slice(0, 2)
        .map((cell) => [cell.getAttribute('id'), cell.getAttribute('parent')]),
    ).toEqual([
      ['0', null],
      ['1', '0'],
    ]);
  });

  test('has a cell for each node and connection of the example lab, with the Builder ids', () => {
    const doc = example();
    const cells = cellsOf(parse(toDrawio(doc)));
    const ids = [...cells.keys()];

    expect(ids.slice(0, 2)).toEqual(['0', '1']);
    expect(new Set(ids.slice(2))).toEqual(
      new Set([...doc.nodes, ...doc.edges].map((item) => item.id)),
    );

    const vertices = [...cells.values()].filter(
      ({ cell }) => cell.getAttribute('vertex') === '1',
    );
    const edges = [...cells.values()].filter(
      ({ cell }) => cell.getAttribute('edge') === '1',
    );

    expect(vertices).toHaveLength(doc.nodes.length);
    expect(edges).toHaveLength(doc.edges.length);
    // Each device carries its data on an <object>, and nothing else does.
    expect(vertices.filter(({ holder }) => holder)).toHaveLength(
      doc.nodes.filter((node) => node.kind === 'device').length,
    );
  });

  test("puts a group's nodes in its container, relative to it, after it", () => {
    const doc = example();
    const xml = parse(toDrawio(doc));
    const cells = cellsOf(xml);
    const order = [...cells.keys()];

    for (const node of doc.nodes) {
      const { cell } = cells.get(node.id);
      const at = geometryOf(cell);

      if (node.kind === 'group') {
        expect(cell.getAttribute('parent'), node.id).toBe('1');
        expect(styleOf(cell), node.id).toMatchObject({
          container: '1',
          collapsible: '0',
          fillColor: 'none',
          strokeColor: node.group.color,
          dashed: '1',
        });
        expect(at, node.id).toEqual({ ...node.position, ...node.size });
      } else if (node.parentId) {
        const group = doc.nodes.find((entry) => entry.id === node.parentId);

        expect(cell.getAttribute('parent'), node.id).toBe(group.id);
        expect(order.indexOf(node.id)).toBeGreaterThan(order.indexOf(group.id));
        expect({ x: at.x, y: at.y }, node.id).toEqual({
          x: node.position.x - group.position.x,
          y: node.position.y - group.position.y,
        });
      }
    }
  });

  test('nests groups in groups, each before the nodes in it', () => {
    const doc = diagram({
      nodes: [
        { ...device('leaf', { x: 130, y: 140 }), parentId: 'inner' },
        device('other', { x: 0, y: 0 }),
        {
          id: 'inner',
          kind: 'group',
          position: { x: 110, y: 120 },
          size: { width: 300, height: 200 },
          parentId: 'outer',
          group: { title: 'Inner' },
        },
        {
          id: 'outer',
          kind: 'group',
          position: { x: 100, y: 100 },
          size: { width: 500, height: 400 },
          group: { title: 'Outer' },
        },
      ],
    });
    const cells = cellsOf(parse(toDrawio(doc)));

    expect([...cells.keys()]).toEqual([
      '0',
      '1',
      'outer',
      'other',
      'inner',
      'leaf',
    ]);
    expect(cells.get('inner').cell.getAttribute('parent')).toBe('outer');
    expect(cells.get('leaf').cell.getAttribute('parent')).toBe('inner');
    expect(geometryOf(cells.get('inner').cell)).toMatchObject({ x: 10, y: 20 });
    expect(geometryOf(cells.get('leaf').cell)).toMatchObject({ x: 20, y: 20 });
  });

  test('puts a node whose group is missing, not a group, or in a loop of groups on the layer', () => {
    const doc = diagram({
      nodes: [
        { ...device('lost', { x: 5, y: 6 }), parentId: 'nowhere' },
        { ...device('host', { x: 0, y: 0 }) },
        { ...device('guest', { x: 7, y: 8 }), parentId: 'host' },
        {
          id: 'a',
          kind: 'group',
          position: { x: 10, y: 10 },
          parentId: 'b',
          group: { title: 'A' },
        },
        {
          id: 'b',
          kind: 'group',
          position: { x: 20, y: 20 },
          parentId: 'a',
          group: { title: 'B' },
        },
      ],
    });
    const cells = cellsOf(parse(toDrawio(doc)));

    for (const id of ['lost', 'guest', 'a', 'b']) {
      expect(cells.get(id).cell.getAttribute('parent'), id).toBe('1');
    }
    expect(geometryOf(cells.get('guest').cell)).toMatchObject({ x: 7, y: 8 });
  });

  test('gives a device its look, its lines as HTML, and a short list of its settings as data', () => {
    const doc = example();
    const cells = cellsOf(parse(toDrawio(doc)));
    const { cell, holder } = cells.get('e17c844d-1f78-5461-9b23-72ad49957bf4');
    const style = styleOf(cell);

    expect(style).toMatchObject({
      shape: 'label',
      html: '1',
      whiteSpace: 'wrap',
      fillColor: LIGHT_COLORS['--bx-surface'],
      strokeColor: LIGHT_COLORS['--bx-border-strong'],
      fontColor: LIGHT_COLORS['--bx-text'],
      imageWidth: '16',
      imageHeight: '16',
      imageAlign: 'left',
      imageVerticalAlign: 'top',
    });
    expect(geometryOf(cell)).toEqual({ x: 48, y: 48, width: 160, height: 96 });
    // The built-in firewall icon, in the accent color.
    expect(svgOf(style.image)).toContain('<title>Firewall</title>');
    expect(svgOf(style.image)).toContain(
      `stroke="${LIGHT_COLORS['--bx-accent']}"`,
    );
    expect(svgOf(style.image)).not.toContain('currentColor');
    expect(labelText(holder.getAttribute('label'))).toBe(
      'ot-fw\nFirewall\n2 interfaces\nFirewall between the corporate and OT networks',
    );
    expect(holder.getAttribute('label')).toMatch(/^<b>ot-fw<\/b><br>/);
    expect(
      Object.fromEntries(
        [...holder.attributes]
          .filter((attribute) => !['id', 'label'].includes(attribute.name))
          .map((attribute) => [attribute.name, attribute.value]),
      ),
    ).toEqual({
      hostname: 'ot-fw',
      type: 'Firewall',
      os_type: 'minirouter',
      image: 'minirouter.qc2',
      vcpus: '1',
      memory: '1024',
      interfaces:
        'eth0: VLAN CORP, 10.10.20.254/24\neth1: VLAN OT, 10.10.30.1/24',
    });

    // An included device has a dashed border and says where it comes from.
    const dns = cells.get('8ce19627-3418-5d84-8060-4df4c86ab6dc');

    expect(styleOf(dns.cell).dashed).toBe('1');
    expect(labelText(dns.holder.getAttribute('label'))).toContain(
      'Included from corp-services',
    );
  });

  test('never carries the spec, its injections, metadata or credentials', () => {
    const doc = diagram({
      nodes: [
        device(
          'host',
          { x: 0, y: 0 },
          {
            spec: {
              hostname: 'host',
              type: 'VirtualMachine',
              injections: [{ src: '/secret/key', dst: '/root/key' }],
              metadata: { password: 'hunter2' },
              annotations: { token: 'abc123' },
              labels: { owner: 'alice' },
              hardware: { os_type: 'linux', vcpus: 2, memory: 2048 },
            },
          },
        ),
      ],
    });
    const text = toDrawio(doc);

    for (const secret of [
      '/secret/key',
      'hunter2',
      'abc123',
      'alice',
      'injections',
      'metadata',
    ]) {
      expect(text, secret).not.toContain(secret);
    }
    const { holder } = cellsOf(parse(text)).get('host');

    expect(holder.getAttribute('os_type')).toBe('linux');
    expect(holder.hasAttribute('image')).toBe(false);
    expect(holder.hasAttribute('interfaces')).toBe(false);
  });

  test('draws a switch as a pill in its network color, and a connection in it, along its route', () => {
    const doc = example();
    const cells = cellsOf(parse(toDrawio(doc)));
    const corp = cells.get('febae525-2c3c-5e50-8081-82055d556fa1');

    expect(styleOf(corp.cell)).toMatchObject({
      shape: 'label',
      rounded: '1',
      arcSize: '50',
      strokeColor: LIGHT_COLORS['--bx-net-0'],
      imageVerticalAlign: 'middle',
    });
    expect(labelText(corp.cell.getAttribute('value'))).toBe('CORP');

    // ntp-01 to the CORP switch: from the right of ntp-01 to the left of
    // the switch, at their handles, with the layout's bends between.
    const routed = doc.edges.find(
      (edge) => edge.id === '079f6027-2e25-5da6-82d9-234360272c80',
    );
    const { cell } = cells.get(routed.id);

    expect(cell.getAttribute('source')).toBe(routed.sourceNodeId);
    expect(cell.getAttribute('target')).toBe(routed.targetNodeId);
    expect(cell.getAttribute('parent')).toBe('1');
    expect(cell.getAttribute('value')).toBe('CORP');
    expect(styleOf(cell)).toMatchObject({
      edgeStyle: 'orthogonalEdgeStyle',
      exitX: '1',
      exitY: '0.5',
      entryX: '0',
      entryY: '0.5',
      endArrow: 'none',
      strokeColor: LIGHT_COLORS['--bx-net-0'],
      strokeWidth: '2',
    });
    expect(pointsOf(cell).bends).toEqual(routed.route.slice(1, -1));

    // edge-rtr's third interface of three leaves at three quarters down.
    const plain = cells.get('13c6adc3-7680-5eb9-b728-8d3ecc29084f');

    expect(styleOf(plain.cell).exitY).toBe('0.75');
    expect(pointsOf(plain.cell).bends).toEqual([]);
  });

  test("draws connections in their network's dash pattern and color, or their own", () => {
    const doc = diagram({
      networks: [
        { id: 'n1', name: 'RED', color: '#ff0000', lineStyle: 'dashed' },
        { id: 'n2', name: 'PLAIN' },
      ],
      nodes: [
        device('a', { x: 0, y: 0 }),
        {
          id: 's1',
          kind: 'switch',
          position: { x: 300, y: 0 },
          switch: { networkId: 'n1' },
        },
        {
          id: 's2',
          kind: 'switch',
          position: { x: 300, y: 200 },
          switch: { networkId: 'n2' },
        },
      ],
      edges: [
        { id: 'e1', sourceNodeId: 'a', targetNodeId: 's1', networkId: 'n1' },
        {
          id: 'e2',
          sourceNodeId: 'a',
          targetNodeId: 's2',
          networkId: 'n2',
          color: '#00aa00',
          lineStyle: 'dash-dot',
          label: 'uplink <1>',
        },
        {
          id: 'e3',
          sourceNodeId: 'a',
          targetNodeId: 'missing',
          networkId: 'n2',
        },
      ],
    });
    const cells = cellsOf(parse(toDrawio(doc)));

    expect(styleOf(cells.get('e1').cell)).toMatchObject({
      strokeColor: '#ff0000',
      dashed: '1',
      fixDash: '1',
      dashPattern: '8 4',
    });
    expect(styleOf(cells.get('s1').cell).strokeColor).toBe('#ff0000');
    expect(styleOf(cells.get('e2').cell)).toMatchObject({
      strokeColor: '#00aa00',
      dashPattern: '10 4 2 4',
    });
    expect(labelText(cells.get('e2').cell.getAttribute('value'))).toBe(
      'uplink <1>',
    );
    // The second network takes the second token, and a solid line.
    expect(styleOf(cells.get('s2').cell).strokeColor).toBe(
      LIGHT_COLORS['--bx-net-1'],
    );
    // A connection with an end that is not in the diagram is left out.
    expect(cells.has('e3')).toBe(false);
  });

  test('draws notes', () => {
    const doc = diagram({
      nodes: [
        device('host', { x: 0, y: 0 }),
        {
          id: 'note-1',
          kind: 'note',
          position: { x: 400, y: 0 },
          size: { width: 200, height: 120 },
          note: { text: 'Line one\nLine two', color: '#2f6fbf' },
        },
      ],
    });
    const cells = cellsOf(parse(toDrawio(doc)));

    expect([...cells.keys()]).toEqual(['0', '1', 'host', 'note-1']);

    const note = cells.get('note-1');

    expect(labelText(note.cell.getAttribute('value'))).toBe(
      'Note\nLine one\nLine two',
    );
    expect(styleOf(note.cell)).toMatchObject({
      fillColor: LIGHT_COLORS['--bx-bg-alt'],
      strokeColor: '#2f6fbf',
      dashed: '1',
    });
    expect(svgOf(styleOf(note.cell).image)).toContain(
      '<title>sticky-note</title>',
    );
  });

  test('draws icons at the size the user chose, or none while icons are hidden', () => {
    const doc = example();
    const large = cellsOf(parse(toDrawio(doc, { iconSize: 'large' })));
    const device = large.get('e17c844d-1f78-5461-9b23-72ad49957bf4');

    // A Large icon stands beside all the lines, at their middle.
    expect(styleOf(device.cell)).toMatchObject({
      imageWidth: '32',
      imageHeight: '32',
      imageVerticalAlign: 'middle',
    });
    expect(svgOf(styleOf(device.cell).image)).toContain('width="32"');

    const hidden = toDrawio(doc, { showNodeIcons: false });

    // The note keeps its icon, as on the canvas.
    expect(hidden.match(/style="[^"]*image=/g)).toHaveLength(1);
  });

  test('draws a custom icon as a PNG, and a fill with its ink', () => {
    const library = {
      lookup: (name) =>
        name === 'pump.png' ? { name, data: ICON_DATA } : null,
    };
    const doc = diagram({
      nodes: [
        device(
          'pump',
          { x: 0, y: 0 },
          { icon: 'pump.png', fillColor: '#000080' },
        ),
        {
          id: 'icon-1',
          kind: 'icon',
          position: { x: 300, y: 0 },
          size: { width: 64, height: 80 },
          icon: { icon: 'pump.png', label: 'Pump' },
        },
      ],
    });
    const cells = cellsOf(parse(toDrawio(doc, { library })));
    const pump = styleOf(cells.get('pump').cell);
    const icon = styleOf(cells.get('icon-1').cell);

    expect(pump.image).toBe(`data:image/png,${ICON_DATA}`);
    expect(pump).toMatchObject({ fillColor: '#000080', fontColor: '#ffffff' });
    expect(icon).toMatchObject({
      image: `data:image/png,${ICON_DATA}`,
      imageAlign: 'center',
      verticalAlign: 'bottom',
      fillColor: 'none',
      strokeColor: 'none',
    });
    expect(labelText(cells.get('icon-1').cell.getAttribute('value'))).toBe(
      'Pump',
    );

    // Without the library, the built-in icon of the kind.
    expect(
      styleOf(cellsOf(parse(toDrawio(doc))).get('pump').cell).image,
    ).toMatch(/^data:image\/svg\+xml,/);
  });

  test('draws rectangles and circles with their fill, outline and border pattern', () => {
    const shape = (id, shape) => ({
      id,
      kind: 'shape',
      position: { x: 0, y: 0 },
      shape,
    });
    const doc = diagram({
      nodes: [
        shape('box', {
          shape: 'rectangle',
          label: 'Zone',
          borderStyle: 'dotted',
          outlineColor: '#123456',
        }),
        shape('ring', {
          shape: 'circle',
          borderStyle: 'double',
          fillColor: '#ffff00',
        }),
        shape('frame', { shape: 'rectangle', borderStyle: 'double' }),
      ],
    });
    const cells = cellsOf(parse(toDrawio(doc)));

    expect(styleOf(cells.get('box').cell)).toMatchObject({
      rounded: '1',
      fillColor: 'none',
      strokeColor: '#123456',
      dashed: '1',
      dashPattern: '2 4',
    });
    expect(labelText(cells.get('box').cell.getAttribute('value'))).toBe('Zone');
    expect(styleOf(cells.get('ring').cell)).toMatchObject({
      ellipse: true,
      shape: 'doubleEllipse',
      fillColor: '#ffff00',
      fontColor: '#000000',
    });
    expect(geometryOf(cells.get('ring').cell)).toMatchObject({
      width: 120,
      height: 120,
    });
    expect(styleOf(cells.get('frame').cell)).toMatchObject({
      shape: 'ext',
      double: '1',
    });
  });

  test('draws a line as an edge with no ends, through its points, with its arrowheads', () => {
    const doc = diagram({
      nodes: [
        {
          id: 'g',
          kind: 'group',
          position: { x: 100, y: 100 },
          size: { width: 400, height: 300 },
          group: { title: 'G', borderStyle: 'solid' },
        },
        {
          id: 'arrow',
          kind: 'line',
          parentId: 'g',
          position: { x: 150, y: 120 },
          line: {
            points: [
              { x: 0, y: 0 },
              { x: 50, y: 40 },
              { x: 100, y: 0 },
            ],
            endArrow: true,
            lineStyle: 'dotted',
            color: '#AA00AA',
            label: 'flow',
          },
        },
        {
          id: 'stub',
          kind: 'line',
          position: { x: 0, y: 0 },
          line: { points: [{ x: 0, y: 0 }] },
        },
      ],
    });
    const cells = cellsOf(parse(toDrawio(doc)));
    const { cell } = cells.get('arrow');

    expect(cell.getAttribute('edge')).toBe('1');
    expect(cell.getAttribute('parent')).toBe('g');
    expect(cell.hasAttribute('source')).toBe(false);
    expect(pointsOf(cell)).toEqual({
      source: { x: 50, y: 20 },
      target: { x: 150, y: 20 },
      bends: [{ x: 100, y: 60 }],
    });
    expect(styleOf(cell)).toMatchObject({
      startArrow: 'none',
      endArrow: 'block',
      endFill: '1',
      strokeColor: '#aa00aa',
      dashPattern: '2 4',
    });
    expect(cell.getAttribute('value')).toBe('flow');
    expect(styleOf(cells.get('g').cell).dashed).toBeUndefined();
    // A line of one point draws nothing.
    expect(cells.has('stub')).toBe(false);
  });

  test("gives a cell a new id when its Builder id is empty, taken, or one of the page's", () => {
    const doc = diagram({
      nodes: [device('0', { x: 0, y: 0 }), device('1', { x: 0, y: 200 })],
      networks: [{ id: 'n', name: 'N' }],
      edges: [
        { id: '0', sourceNodeId: '0', targetNodeId: '1', networkId: 'n' },
      ],
    });
    const cells = cellsOf(parse(toDrawio(doc)));

    expect([...cells.keys()]).toEqual(['0', '1', '0-2', '1-2', '0-3']);
    expect(cells.get('0-3').cell.getAttribute('source')).toBe('0-2');
    expect(cells.get('0-3').cell.getAttribute('target')).toBe('1-2');
  });

  test('keeps the light colors of builder.css', () => {
    const css = readFileSync(fromRoot('src/builder/builder.css'), 'utf8');
    const block = css.slice(css.indexOf('.builder-root {'));
    const lightBlock = block.slice(0, block.indexOf('}'));

    for (const [token, color] of Object.entries(LIGHT_COLORS)) {
      expect(lightBlock, token).toMatch(new RegExp(`${token}: ${color};`));
    }
  });
});
