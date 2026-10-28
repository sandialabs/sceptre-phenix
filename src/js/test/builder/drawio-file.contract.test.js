// What every draw.io download (drawio.js) holds, for any diagram: well
// formed XML, unique cell ids, containers and ends that exist, finite
// geometry that adds up to the diagram's positions, images a style can hold,
// and labels that keep their text. Also the file of the example lab.

import { readFileSync } from 'node:fs';

import fc from 'fast-check';
import { describe, expect, test } from 'vitest';

import { toDrawio } from '@/builder/drawio.js';
import { NOT_XML } from '@/builder/xml.js';

import { ICON_DATA } from './png.js';
import { parseXML } from './xmlReader.js';

// Characters a label may hold: markup, references, line breaks, a tab,
// control characters and a lone surrogate, which XML forbids.
const AWKWARD = [
  '<',
  '>',
  '&',
  '"',
  "'",
  '&amp;',
  '<br>',
  '\n',
  '\r\n',
  '\r',
  '\t',
  ';',
  '=',
  String.fromCharCode(0),
  String.fromCharCode(1),
  String.fromCharCode(0x1b),
  String.fromCharCode(0xd800),
  String.fromCharCode(0xfffe),
];

const label = fc.oneof(
  fc.string({ maxLength: 8 }),
  fc
    .array(fc.oneof(fc.constantFrom(...AWKWARD), fc.string({ maxLength: 3 })), {
      maxLength: 6,
    })
    .map((parts) => parts.join('')),
);

const coordinate = fc.oneof(
  fc.integer({ min: -5000, max: 5000 }),
  fc.double({ min: -1e5, max: 1e5, noNaN: true }),
  fc.constantFrom(NaN, Infinity, -Infinity, undefined, '12'),
);
const extent = fc.oneof(
  fc.integer({ min: 0, max: 800 }),
  fc.constantFrom(NaN, -5, undefined),
);
const color = fc.constantFrom(
  undefined,
  '',
  '#2f6fbf',
  '#ABCDEF',
  'red',
  'rgb(1, 2, 3)',
  'bad;color',
  '#1f5fa9',
);
const pattern = fc.constantFrom(
  undefined,
  'solid',
  'dashed',
  'dotted',
  'dash-dot',
  'double',
  'wavy',
);
const icon = fc.constantFrom(undefined, 'pump.png', 'missing.png');
const point = fc.record({ x: coordinate, y: coordinate });

// Ids of the messy diagrams: repeats, empty, and the page's own "0" and "1".
const MESSY_IDS = ['0', '1', '', 'a', 'a', 'b', 'g', 'h', 'line'];

const nodeShape = fc.record({
  kind: fc.constantFrom(
    'device',
    'switch',
    'group',
    'note',
    'shape',
    'icon',
    'line',
    'unknown',
  ),
  pick: fc.nat(MESSY_IDS.length - 1),
  parent: fc.option(fc.nat(9), { nil: undefined }),
  position: point,
  width: extent,
  height: extent,
  text: label,
  fill: color,
  outline: color,
  border: pattern,
  icon,
  network: fc.constantFrom('n1', 'n2', 'missing'),
  circle: fc.boolean(),
  points: fc.array(point, { maxLength: 5 }),
  arrows: fc.tuple(fc.boolean(), fc.boolean()),
  interfaces: fc.nat(3),
});

const edgeShape = fc.record({
  pick: fc.nat(MESSY_IDS.length - 1),
  source: fc.nat(9),
  target: fc.nat(9),
  handle: fc.nat(3),
  network: fc.constantFrom('n1', 'n2', 'missing'),
  text: label,
  color,
  lineStyle: pattern,
  route: fc.option(fc.array(point, { maxLength: 6 }), { nil: undefined }),
});

function payload(shape, id) {
  const { kind, text } = shape;

  switch (kind) {
    case 'device':
      return {
        device: {
          hostname: text,
          icon: shape.icon,
          fillColor: shape.fill,
          outlineColor: shape.outline,
          includedFrom: shape.circle ? text : undefined,
          spec: {
            type: text,
            general: { description: text },
            hardware: {
              os_type: text,
              vcpus: shape.interfaces,
              memory: text,
              drives: [{ image: text }],
            },
            network: {
              interfaces: Array.from(
                { length: shape.interfaces },
                (_, index) => ({
                  name: `eth${index}`,
                  vlan: text,
                  address: text,
                  mask: 24,
                }),
              ),
            },
          },
          interfaces: Array.from({ length: shape.interfaces }, (_, index) => ({
            id: `${id}-eth${index}`,
            name: `eth${index}`,
            index,
          })),
        },
      };
    case 'switch':
      return {
        switch: {
          networkId: shape.network,
          fillColor: shape.fill,
          outlineColor: shape.outline,
        },
      };
    case 'group':
      return {
        group: {
          title: text,
          description: text,
          color: shape.outline,
          borderStyle: shape.border,
          icon: shape.icon,
        },
      };
    case 'note':
      return { note: { text, color: shape.outline } };
    case 'shape':
      return {
        shape: {
          shape: shape.circle ? 'circle' : 'rectangle',
          label: text,
          fillColor: shape.fill,
          outlineColor: shape.outline,
          borderStyle: shape.border,
        },
      };
    case 'icon':
      return { icon: { icon: shape.icon, iconKey: 'router', label: text } };
    case 'line':
      return {
        line: {
          points: shape.points,
          label: text,
          color: shape.outline,
          lineStyle: shape.border,
          startArrow: shape.arrows[0],
          endArrow: shape.arrows[1],
        },
      };
    default:
      return {};
  }
}

// A diagram of these nodes and edges. Clean ids are n0, n1 … and e0, e1 …,
// each its own. Messy ids repeat, are empty or are "0" and "1".
function documentOf({ nodes, edges, name, grid }, clean) {
  const nodeId = (shape, index) =>
    clean ? `n${index}` : MESSY_IDS[shape.pick];
  const ref = (index) =>
    clean ? `n${index}` : MESSY_IDS[index % MESSY_IDS.length];

  return {
    metadata: { id: name, name },
    grid,
    networks: [
      { id: 'n1', name, color: '#ff0000', lineStyle: 'dotted' },
      { id: 'n2', name: 'two' },
    ],
    nodes: nodes.map((shape, index) => {
      const id = nodeId(shape, index);

      return {
        id,
        kind: shape.kind,
        label: shape.text,
        parentId: shape.parent === undefined ? undefined : ref(shape.parent),
        position: shape.position,
        size: { width: shape.width, height: shape.height },
        ...payload(shape, id),
      };
    }),
    edges: edges.map((shape, index) => ({
      id: clean ? `e${index}` : MESSY_IDS[shape.pick],
      sourceNodeId: ref(shape.source),
      sourceHandleId: `${ref(shape.source)}-eth${shape.handle}`,
      targetNodeId: ref(shape.target),
      networkId: shape.network,
      label: shape.text,
      color: shape.color,
      lineStyle: shape.lineStyle,
      route: shape.route,
    })),
  };
}

const diagramShape = fc.record({
  nodes: fc.array(nodeShape, { maxLength: 10 }),
  edges: fc.array(edgeShape, { maxLength: 8 }),
  name: label,
  grid: fc.record({
    enabled: fc.boolean(),
    size: fc.oneof(fc.integer({ min: 4, max: 200 }), fc.constant('x')),
  }),
});
const settings = fc.record({
  showNodeIcons: fc.boolean(),
  showNodeDescriptions: fc.boolean(),
  iconSize: fc.constantFrom(undefined, 'small', 'medium', 'large'),
});

const library = {
  lookup: (name) => (name === 'pump.png' ? { name, data: ICON_DATA } : null),
};

function fileOf(doc, options) {
  return parseXML(toDrawio(doc, { ...options, library }));
}

// Every element of the file, depth first.
function* walk(element) {
  yield element;
  for (const child of element.children) {
    yield* walk(child);
  }
}

// The cells: each mxCell, with the id, label and data of the <object> that
// holds it.
function cellsOf(file) {
  const cells = [];

  for (const element of walk(file)) {
    for (const child of element.children) {
      if (child.name === 'mxCell') {
        const holder = element.name === 'object' ? element : null;

        cells.push({
          ...child.attributes,
          ...(holder
            ? { id: holder.attributes.id, label: holder.attributes.label }
            : {}),
          cell: child,
        });
      }
    }
  }

  return cells;
}

const finite = (value) => {
  const number = Number(value);

  return Number.isFinite(number) ? number : 0;
};

// The text that HTML holds, with line breaks for <br>.
function htmlText(html) {
  return html
    .replace(/<br>/g, '\n')
    .replace(/<[^>]*>/g, '')
    .replace(
      /&(lt|gt|quot|#39|amp);/g,
      (_, name) =>
        ({ lt: '<', gt: '>', quot: '"', '#39': "'", amp: '&' })[name],
    );
}

// Text as a label keeps it: no character that XML forbids, one kind of
// line break.
const kept = (text) =>
  String(text).replace(NOT_XML, '').replace(/\r\n?/g, '\n');

const RUNS = { numRuns: 200 };

describe('every draw.io download', () => {
  test('is well formed, with unique ids, and every container and end it names', () => {
    fc.assert(
      fc.property(
        diagramShape,
        fc.boolean(),
        settings,
        (shape, clean, options) => {
          const cells = cellsOf(fileOf(documentOf(shape, clean), options));
          const ids = cells.map((cell) => cell.id);
          const seen = new Set();

          expect(new Set(ids).size).toBe(ids.length);
          expect(ids.slice(0, 2)).toEqual(['0', '1']);
          for (const cell of cells) {
            // A container comes before what it holds.
            for (const name of ['parent', 'source', 'target']) {
              if (cell[name] !== undefined) {
                expect(ids, `${name} ${cell[name]}`).toContain(cell[name]);
              }
            }
            if (cell.parent !== undefined) {
              expect(seen.has(cell.parent), cell.id).toBe(true);
            }
            seen.add(cell.id);
          }
        },
      ),
      RUNS,
    );
  });

  test('has finite geometry, and images without ";base64"', () => {
    fc.assert(
      fc.property(
        diagramShape,
        fc.boolean(),
        settings,
        (shape, clean, options) => {
          const text = toDrawio(documentOf(shape, clean), {
            ...options,
            library,
          });

          expect(text).not.toContain(';base64');
          for (const element of walk(parseXML(text))) {
            if (element.name === 'mxGeometry' || element.name === 'mxPoint') {
              for (const name of ['x', 'y', 'width', 'height']) {
                const value = element.attributes[name];

                if (value !== undefined) {
                  expect(
                    Number.isFinite(Number(value)),
                    `${name}=${value}`,
                  ).toBe(true);
                }
              }
            }
            for (const [, image] of (element.attributes.style || '').matchAll(
              /(?:^|;)image=([^;]*)/g,
            )) {
              expect(image).toMatch(
                /^data:image\/(svg\+xml|png),[A-Za-z0-9+/]+=*$/,
              );
            }
          }
        },
      ),
      RUNS,
    );
  });

  test('places each node so that its geometry and its containers add up to its position', () => {
    fc.assert(
      fc.property(diagramShape, settings, (shape, options) => {
        const doc = documentOf(shape, true);
        const cells = cellsOf(fileOf(doc, options));
        const byId = new Map(cells.map((cell) => [cell.id, cell]));
        const geometry = (cell) =>
          cell.cell.children.find((child) => child.name === 'mxGeometry');
        // A cell's position on the page: its geometry and its containers'.
        const absolute = (cell) => {
          if (!cell || cell.id === '1') {
            return { x: 0, y: 0 };
          }

          const { x = 0, y = 0 } = geometry(cell).attributes;
          const container = absolute(byId.get(cell.parent));

          return { x: container.x + Number(x), y: container.y + Number(y) };
        };

        for (const node of doc.nodes) {
          const cell = byId.get(node.id);

          if (!cell) {
            continue;
          }

          const at = { x: finite(node.position.x), y: finite(node.position.y) };

          if (node.kind === 'line') {
            const start = geometry(cell).children.find(
              (child) => child.attributes.as === 'sourcePoint',
            ).attributes;
            const container = absolute(byId.get(cell.parent));
            const [first] = node.line.points;

            expect(container.x + Number(start.x)).toBeCloseTo(
              at.x + finite(first.x),
              6,
            );
            expect(container.y + Number(start.y)).toBeCloseTo(
              at.y + finite(first.y),
              6,
            );
          } else {
            const found = absolute(cell);

            expect(found.x, node.id).toBeCloseTo(at.x, 6);
            expect(found.y, node.id).toBeCloseTo(at.y, 6);
          }
        }
        // Every kind the Builder knows has a cell, but a line of one point.
        expect(
          doc.nodes.filter(
            (node) =>
              node.kind !== 'unknown' &&
              !(node.kind === 'line' && node.line.points.length < 2),
          ).length,
        ).toBe(doc.nodes.filter((node) => byId.has(node.id)).length);
      }),
      RUNS,
    );
  });

  test('keeps the text of labels, whatever characters they hold', () => {
    fc.assert(
      fc.property(diagramShape, settings, (shape, options) => {
        const doc = documentOf(shape, true);
        const byId = new Map(
          cellsOf(fileOf(doc, options)).map((cell) => [cell.id, cell]),
        );

        for (const node of doc.nodes) {
          const cell = byId.get(node.id);

          if (node.kind === 'device' && node.label) {
            const [, name] = cell.label.match(/^<b>(.*?)<\/b>/s);

            expect(htmlText(name)).toBe(kept(node.label));
            expect(cell.label).not.toMatch(/<(?!\/?(b|br|span)\b)/);
          }
          if (node.kind === 'shape') {
            expect(htmlText(cell.value)).toBe(kept(node.shape.label));
          }
        }
        for (const edge of doc.edges) {
          const cell = byId.get(edge.id);

          if (cell && edge.label) {
            expect(htmlText(cell.value)).toBe(kept(edge.label));
          }
        }
      }),
      RUNS,
    );
  });
});

describe('the draw.io file of the example lab', () => {
  test('stays as it is', async () => {
    const doc = JSON.parse(
      readFileSync(
        new URL(
          '../../../go/types/builder/testdata/examples/riverside-water.builder.json',
          import.meta.url,
        ),
        'utf8',
      ),
    );

    await expect(toDrawio(doc)).toMatchFileSnapshot(
      './__snapshots__/riverside-water.drawio',
    );
  });
});
