import { describe, expect, test, vi } from 'vitest';
import YAML from 'js-yaml';

import {
  computeExportViewport,
  documentBounds,
  exportCopy,
  exportFileName,
  exportImage,
  IMAGE_PADDING,
  inlineSvgPaint,
  saveText,
  toJSONString,
  toYAMLString,
} from '@/builder/exporters.js';
import { parseDocument } from '@/builder/decode.js';

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
});
